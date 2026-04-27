package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mopv1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/google/uuid"
	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	"github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	meshOpErrors "github.com/istio-ecosystem/mesh-operator/pkg/errors"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	"github.com/istio-ecosystem/mesh-operator/pkg/resources"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
)

type KnativeIngressController struct {
	logger          *zap.SugaredLogger
	metricsRegistry *prometheus.Registry
	controllerCfg   *common.ControllerConfig

	ingressEnqueuer controllers_api.QueueAccessor

	namespaceFilter controllers_api.NamespaceFilter

	objectReader                 objectReader
	resourceManager              resources.ResourceManager
	multiClusterReconcileTracker ReconcileManager

	configGenerator ConfigGenerator

	// clusterManager will retrieve clusters and their clients
	clusterManager controllers_api.ClusterManager
	primaryClient  kube.Client
}

func NewKnativeIngressController(
	applicator templating.Applicator,
	logger *zap.SugaredLogger,
	metricsRegistry *prometheus.Registry,
	primaryKubeClient kube.Client,
	renderer templating.TemplateRenderer,
	clusterManager controllers_api.ClusterManager,
	resourceManager resources.ResourceManager,
	controllerCfg *common.ControllerConfig,
	multiClusterReconcileTracker ReconcileManager,
) controllers_api.MulticlusterController {

	ctxLogger := logger.With("controller", "knative-ingress-multicluster")
	ingressQueue := createWorkQueueWithWatchdog(context.Background(), "knative-ingress-multicluster", ctxLogger, metricsRegistry)

	configMutators := CreateServiceConfigMutators()

	configGenerator := NewConfigGenerator(renderer, configMutators, nil, applicator, templating.DontApplyOverlays, ctxLogger, metricsRegistry)

	controller := &KnativeIngressController{
		logger:                       ctxLogger,
		configGenerator:              configGenerator,
		ingressEnqueuer:              NewSingleQueueEnqueuer(ingressQueue),
		clusterManager:               clusterManager,
		resourceManager:              resourceManager,
		metricsRegistry:              metricsRegistry,
		multiClusterReconcileTracker: multiClusterReconcileTracker,
		primaryClient:                primaryKubeClient,
		controllerCfg:                controllerCfg,
	}

	return controller
}

// Run
/*
1. Sync the caches for the informers before starting the workers
2. Start the workers to process the Ingress resources
*/
func (c *KnativeIngressController) Run(workerThreads int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	c.logger.Info("starting multi-cluster knative ingress controller...")

	onRetryHandler := OnControllerRetryHandler(
		metrics.KnativeIngressRetries,
		c.metricsRegistry)

	c.logger.Info("starting workers")
	// Launch workers to process Ingress resources
	for i := 0; i < workerThreads; i++ {
		go wait.Until(func() {
			RunWorker(
				c.logger,
				c.ingressEnqueuer.GetQueue(),
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

func (c *KnativeIngressController) OnNewClusterAdded(cluster controllers_api.Cluster) error {
	c.logger.Infof("initializing cluster: %s", cluster.GetId())

	// checks if Ingress resource is present in cluster
	c.logger.Info("checking if Ingress resource is present in cluster")

	if !resourcePresentInCluster(cluster) {
		c.logger.Infof("Ingress resource is not present in cluster: %s", cluster.GetId())
		return nil
	}

	c.logger.Infof("creating informer for cluster: %s", cluster.GetId())
	ingressesInformer := cluster.GetKubeClient().DynamicInformerFactory().ForResource(constants.KnativeIngressResource)
	ingressesInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			return cluster.GetNamespaceFilter().IsNamespaceMopEnabledForObject(obj)
		},
		Handler: NewKnativeControllerEventHandler(c.ingressEnqueuer, cluster.GetId().String(), c.logger),
	})
	errorHandler := NewWatchErrorHandlerWithMetrics(c.logger, cluster.GetId().String(), "knative ingresses", c.metricsRegistry)
	_ = ingressesInformer.Informer().SetWatchErrorHandler(errorHandler)

	return nil
}

func NewKnativeControllerEventHandler(ingressEnqueuer controllers_api.QueueAccessor, clusterName string, ctxLogger *zap.SugaredLogger) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ingressObj := obj.(*unstructured.Unstructured)
			ctxLogger.Debugw("informer event: Add", "namespace", ingressObj.GetNamespace(), "ingressName", ingressObj.GetName())
			ingressEnqueuer.Enqueue(clusterName, ingressObj, controllers_api.EventAdd)
		},
		UpdateFunc: func(old, new interface{}) {
			newIngress := new.(*unstructured.Unstructured)
			oldIngress := old.(*unstructured.Unstructured)
			ctxLogger.Debugw("informer event: Update", "namespace", newIngress.GetNamespace(), "ingressName", newIngress.GetName(),
				"newIngressRV", newIngress.GetResourceVersion(), "oldIngressRV", oldIngress.GetResourceVersion())
			if newIngress.GetResourceVersion() == oldIngress.GetResourceVersion() {
				return
			}
			ingressEnqueuer.Enqueue(clusterName, newIngress, controllers_api.EventUpdate)
		},
		DeleteFunc: func(obj interface{}) {
			ingressObj := obj.(*unstructured.Unstructured)
			ctxLogger.Debugw("informer event: Delete", "namespace", ingressObj.GetNamespace(), "name", ingressObj.GetName())
			ingressEnqueuer.Enqueue(clusterName, ingressObj, controllers_api.EventDelete)
		},
	}
}

func resourcePresentInCluster(cluster controllers_api.Cluster) bool {
	_, err := kube.ConvertGvkToGvr(
		cluster.GetId().String(),
		cluster.GetKubeClient().Discovery(),
		schema.GroupVersionKind{
			Group:   constants.KnativeIngressKind.Group,
			Version: constants.KnativeIngressKind.Version,
			Kind:    constants.KnativeIngressKind.Kind})

	return err == nil
}

// reconcile
/*
1. extract the ingress object using the item's metadata
2. determine action based off of the event
*/
func (c *KnativeIngressController) reconcile(item QueueItem) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(item.key)
	if err != nil {
		return fmt.Errorf("invalid resource key: %s", item.key)
	}

	reconcileId := uuid.New()
	clusterInEvent := item.cluster

	ingressCtxLogger := c.logger.With("ingressNamespace", namespace, "ingressName", name, "ingressReconcileId", reconcileId, "cluster", clusterInEvent)

	// Make sure we're not running concurrent reconciles for the same service object
	ingressKeyAlreadyTracked := c.multiClusterReconcileTracker.trackKey(namespace, name)
	if ingressKeyAlreadyTracked {
		return &meshOpErrors.NonCriticalReconcileError{
			Message: fmt.Sprintf("ingress is already being reconciled: %s", namespace+"/"+name),
		}
	}
	defer c.multiClusterReconcileTracker.unTrackKey(ingressCtxLogger, clusterInEvent, namespace, name)
	defer IncrementCounterForQueueItem(c.metricsRegistry, metrics.KnativeIngressReconciledTotal, clusterInEvent, item)

	if item.event == controllers_api.EventDelete {
		ingressCtxLogger.Infof("reconciling delete event - %s/%s", namespace, name)
		err = c.deleteMeshConfig(clusterInEvent, namespace, name, item.uid, ingressCtxLogger)
		return err
	}

	var ingressObject runtime.Object
	_, clusterClient := c.clusterManager.GetClusterById(clusterInEvent)
	ingressObject, err = clusterClient.GetKubeClient().DynamicInformerFactory().ForResource(constants.KnativeIngressResource).Lister().ByNamespace(namespace).Get(name)
	if err != nil {
		c.logger.Infof("ingress object not found for given cluster - %s", clusterInEvent)
		return nil
	}
	ingressInEvent := ingressObject.(*unstructured.Unstructured)

	//var reconcileError error
	if item.event == controllers_api.EventAdd || item.event == controllers_api.EventUpdate {
		ingressCtxLogger.Infof("reconciling add or update event - %s/%s", ingressInEvent.GetNamespace(), ingressInEvent.GetName())
		err = c.createIngressConfig(clusterInEvent, ingressInEvent, ingressCtxLogger)
	}

	ingressStatusBuilder := NewIngressStatusBuilder()
	ingressStatusBuilder.SetIngressStatus(ingressInEvent)

	if err != nil {
		if meshOpErrors.IsNonCriticalReconcileError(err) {
			// Non-critical err, retrying
			IncrementCounterForQueueItem(c.metricsRegistry, metrics.KnativeIngressReconcileNonCriticalError, clusterInEvent, item)
		}
		ingressStatusBuilder.AddCondition(LoadBalancerReady, metav1.ConditionFalse, ReconcileVirtualServiceFailed, err.Error())
		IncrementCounterForQueueItem(c.metricsRegistry, metrics.KnativeIngressReconcileFailed, clusterInEvent, item)
		ingressCtxLogger.Debugf("recording event for %d for %s/%s", item.event, namespace, name)
		if item.event != controllers_api.EventDelete {
			clusterClient.GetEventRecorder().Event(ingressInEvent, v1.EventTypeWarning, constants.MeshConfigError, err.Error())
		}
	} else {
		ingressStatusBuilder.AddCondition(LoadBalancerReady, metav1.ConditionTrue, "", "")
	}

	if item.event == controllers_api.EventAdd || item.event == controllers_api.EventUpdate {
		ingressStatusBuilder.AddCondition(NetworkConfigured, metav1.ConditionTrue, "", "")
		ingressStatusBuilder.AddCondition(Ready, metav1.ConditionTrue, "", "")
		updateStatusErr := c.UpdateIngressStatus(ingressInEvent, clusterClient, ingressCtxLogger, ingressStatusBuilder.Build())
		if updateStatusErr != nil {
			return updateStatusErr
		}
	}

	return err
}

func (c *KnativeIngressController) createIngressConfig(clusterName string, ingress *unstructured.Unstructured, ctxLogger *zap.SugaredLogger) error {
	isIngressTracked := true
	trackedIngress := resources.IngressToReference(clusterName, ingress)
	msm, err := c.resourceManager.TrackOwner(clusterName, ingress.GetNamespace(), trackedIngress, ctxLogger)
	if err != nil {
		isIngressTracked = false
		c.logger.Errorf("encountered error while tracking ingress: %v", err)
	}

	configObjects, err := c.generateConfig(clusterName, ingress, msm, ctxLogger)
	if err != nil {
		return err
	}

	_, cluster := c.clusterManager.GetClusterById(clusterName)

	if isIngressTracked {
		trackingError := c.resourceManager.OnOwnerChange(clusterName, cluster.GetKubeClient(), msm, ingress, trackedIngress, templating.GetConfigObjects(configObjects), ctxLogger)
		return common.GetFirstNonNil(err, trackingError)
	}

	return err
}

func (c *KnativeIngressController) generateConfig(clusterName string, ingress *unstructured.Unstructured, msm *mopv1.MeshServiceMetadata, ctxLogger *zap.SugaredLogger) ([]*templating.AppliedConfigObject, error) {
	renderCtx := templating.NewRenderRequestContext(ingress, map[string]string{}, MsmToOwnerRef(msm), clusterName, "", nil, nil)
	applicatorResults, generateConfigErr := c.configGenerator.GenerateConfig(&renderCtx, NoOpOnBeforeApply, ctxLogger)

	if generateConfigErr != nil {
		return nil, generateConfigErr
	}

	for _, result := range applicatorResults {
		if result.Error != nil {
			obj := result.Object
			return nil, fmt.Errorf("failed to upsert object %s.%s of kind %v: %w",
				obj.GetName(), obj.GetNamespace(), obj.GroupVersionKind(), result.Error)
		}
	}

	return applicatorResults, nil
}

func (c *KnativeIngressController) deleteMeshConfig(clusterName, namespace, name string, uid types.UID, ctxLogger *zap.SugaredLogger) error {
	ctxLogger.Infof("deleting mesh config for Ingress: %s/%s/%s/%s", clusterName, namespace, name, uid)
	return c.resourceManager.UnTrackOwner(clusterName, namespace, constants.KnativeIngressKind.Kind, name, uid, ctxLogger)
}

func (c *KnativeIngressController) GetObjectEnqueuer() controllers_api.ObjectEnqueuer {
	return c.ingressEnqueuer
}

func (c *KnativeIngressController) UpdateIngressStatus(ingress *unstructured.Unstructured, clusterClient controllers_api.Cluster, logger *zap.SugaredLogger, ingressStatus *IngressStatus) error {
	unstructuredStatus, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&ingressStatus.Status)
	if err != nil {
		return err
	}
	unstructured.SetNestedField(ingress.UnstructuredContent(), unstructuredStatus, "status")

	_, err = clusterClient.GetKubeClient().Dynamic().Resource(constants.KnativeIngressResource).Namespace(ingress.GetNamespace()).UpdateStatus(context.TODO(), ingress, metav1.UpdateOptions{})
	if err != nil {
		if k8serrors.IsConflict(err) {
			logger.Infof("conflict while updating Ingress status, will retry: %s/%s", ingress.GetNamespace(), ingress.GetName())
			return &meshOpErrors.NonCriticalReconcileError{Message: err.Error()}
		} else {
			return fmt.Errorf("error updating Ingress status for %v/%v, error: %v", ingress.GetNamespace(), ingress.GetName(), err)
		}
	}
	return nil
}
