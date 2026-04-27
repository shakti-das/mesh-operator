package controllers

import (
	"context"
	"fmt"
	"time"

	meshv1alpha1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/generated/listers/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	"github.com/istio-ecosystem/mesh-operator/pkg/resources"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type OwnerCheckResult int

const (
	Unknown OwnerCheckResult = iota + 1
	OwnerExists
	OwnerDoesntExist
)

type msmController struct {
	logger         *zap.SugaredLogger
	msmWorkQueue   workqueue.RateLimitingInterface
	msmInformer    cache.SharedIndexInformer
	msmLister      v1alpha1.MeshServiceMetadataLister
	clusterName    string
	clusterManager controllers_api.ClusterManager
	objectReader   objectReader

	resourceManager resources.ResourceManager
	metricsRegistry *prometheus.Registry

	informerOwnerChecks map[string]informerOwnerCheck
}

func NewMeshServiceMetadataController(
	baseLogger *zap.SugaredLogger,
	client kube.Client,
	clusterName string,
	clusterManager controllers_api.ClusterManager,
	resourceManager resources.ResourceManager,
	metricsRegistry *prometheus.Registry) Controller {

	logger := baseLogger.With("controller", "MSM")
	msmInformer := client.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()
	msmQueue := createWorkQueueWithWatchdog(context.Background(), "MSM", logger, metricsRegistry)

	informerChecks := map[string]informerOwnerCheck{
		constants.ServiceKind.Kind:                  &serviceInformerOwnerCheck{},
		constants.ServiceEntryKind.Kind:             &serviceEntryInformerOwnerCheck{},
		constants.KnativeIngressKind.Kind:           &knativeIngressInformerOwnerCheck{},
		constants.KnativeServerlessServiceKind.Kind: &knativeServerlessServiceInformerOwnerCheck{},
	}

	controller := &msmController{
		logger:              logger,
		clusterName:         clusterName,
		msmWorkQueue:        msmQueue,
		msmInformer:         msmInformer.Informer(),
		msmLister:           msmInformer.Lister(),
		clusterManager:      clusterManager,
		resourceManager:     resourceManager,
		metricsRegistry:     metricsRegistry,
		informerOwnerChecks: informerChecks,
	}
	controller.objectReader = controller.readObject

	msmInformer.Informer().AddEventHandler(&msmEventHandler{clusterName: clusterName, queue: msmQueue})
	return controller
}

func (c *msmController) Run(workerThreads int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.msmWorkQueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	c.logger.Info("starting msm controller")

	// Wait for the caches to be synced before starting workers
	c.logger.Info("waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh,
		c.msmInformer.HasSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	onRetryHandler := OnControllerRetryHandler(
		commonmetrics.MeshServiceMetadataRetries,
		c.metricsRegistry)

	c.logger.Info("starting workers")
	// Launch workers to process Service resources
	for i := 0; i < workerThreads; i++ {
		go wait.Until(func() {
			RunWorker(
				c.logger,
				c.msmWorkQueue,
				c.reconcile,
				onRetryHandler)
		},
			time.Second,
			stopCh)
	}

	c.logger.Info("started workers")
	<-stopCh
	c.logger.Info("shutting down workers")

	return nil
}

func (c *msmController) reconcile(item QueueItem) error {
	defer IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.MeshServiceMetadatasReconciledTotal, c.clusterName, item)

	namespace, name, object, err := extractOrRestoreObject(item, c.objectReader)
	if err != nil {
		return err
	}
	if object == nil {
		c.logger.Warnf("MSM '%s' in work queue no longer exists in the cluster", item.key)
		return nil
	}

	msm := object.(*meshv1alpha1.MeshServiceMetadata)
	for _, owner := range msm.Spec.Owners {
		exists, ownerCheckError := c.checkOwnerMightExist(namespace, owner)
		if ownerCheckError != nil {
			// Don't make assumptions - retry. Forever if needed.
			IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.MeshServiceMetadatasReconcileFailed, c.clusterName, item)
			return fmt.Errorf("error while verifying owner exists: %w", ownerCheckError)
		}
		if !exists {
			c.logger.Info("removing stale owner (%s) from MSM %s/%s", owner, namespace, name)
			untrackError := c.resourceManager.UnTrackOwner(owner.Cluster, namespace, owner.Kind, owner.Name, owner.UID, c.logger)
			if untrackError != nil {
				IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.MeshServiceMetadatasReconcileFailed, c.clusterName, item)
				return untrackError
			}
			IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.MeshServiceMetadataOwnersUnTracked, c.clusterName, item)
		}
	}

	return nil
}

// checkOwnerMightExist - this is a non-strict check, whether an owner still exists.
// If for any reason it can't be done, treat as if ownder does exist or return an error to retry.
func (c *msmController) checkOwnerMightExist(namespace string, owner meshv1alpha1.Owner) (bool, error) {
	found, cluster := c.clusterManager.GetClusterById(owner.Cluster)

	if !found {
		c.logger.Warnf("unknown cluster %s in owner: %s/%s/%s", owner.Cluster, namespace, owner.Kind, owner.Name)
		// Cluster doesn't exist. It could have been removed, but we don't know for sure.
		// Skip, don't do anything.
		return true, nil
	}

	cacheOwnerCheck := c.checkOwnerExistsInCache(cluster, namespace, &owner)
	if cacheOwnerCheck != Unknown {
		c.logger.Debugf("used cache for owner check: %s/%s/%s: %t", namespace, owner.Kind, owner.Name, cacheOwnerCheck == OwnerExists)
		return cacheOwnerCheck == OwnerExists, nil
	}

	gv, gvParseError := schema.ParseGroupVersion(owner.ApiVersion)
	if gvParseError != nil {
		// Owner in a bad format?
		c.logger.Warnf("can't parse api version owner: %s/%s/%s error: %s", namespace, owner.Kind, owner.Name, gvParseError)
		return true, nil
	}

	gvr, resourceDiscoveryError := kube.ConvertGvkToGvr(cluster.GetKubeClient().GetClusterName(), cluster.GetKubeClient().Discovery(), gv.WithKind(owner.Kind))
	if resourceDiscoveryError != nil {
		// Can't determine resource.
		c.logger.Warnf("unknown kind in owner: %s/%s/%s error: %s", namespace, owner.Kind, owner.Name, resourceDiscoveryError)
		return true, nil
	}

	_, getError := cluster.GetKubeClient().Dynamic().Resource(gvr).Namespace(namespace).Get(context.TODO(), owner.Name, metav1.GetOptions{})
	if getError != nil {
		if k8sErrors.IsNotFound(getError) {
			return false, nil
		}
		return true, getError
	}

	return true, nil
}

func (c *msmController) readObject(namespace string, name string) (interface{}, error) {
	return c.msmLister.MeshServiceMetadatas(namespace).Get(name)
}

// checkOwnerExistsInCache - use informer cache to determine if owner is still present in cluster.
// A few caveats:
// * only applies for Service, ServiceEntry and knative Ingress objects. Unknown for other objects.
// * if informer is not synced - Unknown.
// * if any problem accessing informer - Unknown.
func (c *msmController) checkOwnerExistsInCache(cluster controllers_api.Cluster, namespace string, owner *meshv1alpha1.Owner) OwnerCheckResult {

	// Feature not enabled
	if !features.UseInformerInMsmOwnerCheck {
		return Unknown
	}

	checkForKind, exists := c.informerOwnerChecks[owner.Kind]
	// This kind is not supported or informer not synced yet
	if !exists || !checkForKind.isInformerSynced(cluster) {
		return Unknown
	}

	_, err := checkForKind.readRecord(cluster, namespace, owner)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return OwnerDoesntExist
		}
		return Unknown
	}
	return OwnerExists
}

type msmEventHandler struct {
	clusterName string
	queue       workqueue.RateLimitingInterface
}

func (h *msmEventHandler) OnAdd(obj interface{}, isInInitialList bool) {
	enqueue(h.queue, h.clusterName, obj, controllers_api.EventAdd)
}

func (h *msmEventHandler) OnUpdate(_, _ interface{}) {}

func (h *msmEventHandler) OnDelete(_ interface{}) {}

// informerOwnerCheck interface and its implementations
type informerOwnerCheck interface {
	isInformerSynced(cluster controllers_api.Cluster) bool
	readRecord(cluster controllers_api.Cluster, namespace string, owner *meshv1alpha1.Owner) (interface{}, error)
}

type serviceInformerOwnerCheck struct {
}

func (oc *serviceInformerOwnerCheck) isInformerSynced(cluster controllers_api.Cluster) bool {
	return cluster.GetKubeClient().KubeInformerFactory().Core().V1().Services().Informer().HasSynced()
}

func (oc *serviceInformerOwnerCheck) readRecord(cluster controllers_api.Cluster, namespace string, owner *meshv1alpha1.Owner) (interface{}, error) {
	return cluster.GetKubeClient().KubeInformerFactory().Core().V1().Services().Lister().Services(namespace).Get(owner.Name)
}

type serviceEntryInformerOwnerCheck struct {
}

func (oc *serviceEntryInformerOwnerCheck) isInformerSynced(cluster controllers_api.Cluster) bool {
	return cluster.GetKubeClient().IstioInformerFactory().Networking().V1alpha3().ServiceEntries().Informer().HasSynced()
}

func (oc *serviceEntryInformerOwnerCheck) readRecord(cluster controllers_api.Cluster, namespace string, owner *meshv1alpha1.Owner) (interface{}, error) {
	return cluster.GetKubeClient().IstioInformerFactory().Networking().V1alpha3().ServiceEntries().Lister().ServiceEntries(namespace).Get(owner.Name)
}

type knativeIngressInformerOwnerCheck struct {
}

func (oc *knativeIngressInformerOwnerCheck) isInformerSynced(cluster controllers_api.Cluster) bool {
	// Do the feature-flag check to avoid accidentally enabling informer
	if !features.EnableKnativeControllers {
		return false
	}
	return cluster.GetKubeClient().DynamicInformerFactory().ForResource(constants.KnativeIngressResource).Informer().HasSynced()
}

func (oc *knativeIngressInformerOwnerCheck) readRecord(cluster controllers_api.Cluster, namespace string, owner *meshv1alpha1.Owner) (interface{}, error) {
	return cluster.GetKubeClient().DynamicInformerFactory().ForResource(constants.KnativeIngressResource).Lister().ByNamespace(namespace).Get(owner.Name)
}

type knativeServerlessServiceInformerOwnerCheck struct {
}

func (oc *knativeServerlessServiceInformerOwnerCheck) isInformerSynced(cluster controllers_api.Cluster) bool {
	// Do the feature-flag check to avoid accidentally enabling informer
	if !features.EnableKnativeControllers {
		return false
	}
	return cluster.GetKubeClient().DynamicInformerFactory().ForResource(constants.KnativeServerlessServiceResource).Informer().HasSynced()
}

func (oc *knativeServerlessServiceInformerOwnerCheck) readRecord(cluster controllers_api.Cluster, namespace string, owner *meshv1alpha1.Owner) (interface{}, error) {
	return cluster.GetKubeClient().DynamicInformerFactory().ForResource(constants.KnativeServerlessServiceResource).Lister().ByNamespace(namespace).Get(owner.Name)
}
