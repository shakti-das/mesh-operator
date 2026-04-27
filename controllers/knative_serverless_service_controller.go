package controllers

import (
	"context"
	"fmt"
	"time"

	mopv1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"
	meshOpErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/resources"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
)

type KnativeServerlessServiceController struct {
	logger          *zap.SugaredLogger
	metricsRegistry *prometheus.Registry
	controllerCfg   *common.ControllerConfig

	serverlessServiceEnqueuer controllers_api.QueueAccessor

	namespaceFilter controllers_api.NamespaceFilter

	objectReader                 objectReader
	resourceManager              resources.ResourceManager
	multiClusterReconcileTracker ReconcileManager

	configGenerator ConfigGenerator

	// clusterManager will retrieve clusters and their clients
	clusterManager controllers_api.ClusterManager
}

func NewKnativeServerlessServiceController(
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
	serverlessServiceQueue := createWorkQueueWithWatchdog(context.Background(), "knative-ingress-multicluster", ctxLogger, metricsRegistry)

	configMutators := CreateServiceConfigMutators()

	configGenerator := NewConfigGenerator(renderer, configMutators, nil, applicator, templating.DontApplyOverlays, ctxLogger, metricsRegistry)

	controller := &KnativeServerlessServiceController{
		logger:                       ctxLogger,
		configGenerator:              configGenerator,
		serverlessServiceEnqueuer:    NewSingleQueueEnqueuer(serverlessServiceQueue),
		clusterManager:               clusterManager,
		resourceManager:              resourceManager,
		metricsRegistry:              metricsRegistry,
		multiClusterReconcileTracker: multiClusterReconcileTracker,
		controllerCfg:                controllerCfg,
	}

	return controller
}

func (c *KnativeServerlessServiceController) GetObjectEnqueuer() controllers_api.ObjectEnqueuer {
	return c.serverlessServiceEnqueuer
}

// Run
/*
1. Sync the caches for the informers before starting the workers
2. Start the workers to process the ServerlessService resources
*/
func (c *KnativeServerlessServiceController) Run(workerThreads int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	c.logger.Info("starting multi-cluster knative serverless service controller...")

	onRetryHandler := OnControllerRetryHandler(
		metrics.KnativeServerlessServiceRetries,
		c.metricsRegistry)

	c.logger.Info("starting workers")
	// Launch workers to process ServerlessService resources
	for i := 0; i < workerThreads; i++ {
		go wait.Until(func() {
			RunWorker(
				c.logger,
				c.serverlessServiceEnqueuer.GetQueue(),
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

func (c *KnativeServerlessServiceController) OnNewClusterAdded(cluster controllers_api.Cluster) error {
	c.logger.Infof("initializing cluster: %s", cluster.GetId())

	// checks if ServerlessService resource is present in cluster
	c.logger.Info("checking if ServerlessService resource is present in cluster")

	if !serverlessServiceResourcePresentInCluster(cluster) {
		c.logger.Infof("ServerlessService resource is not present in cluster: %s", cluster.GetId())
		return nil
	}

	c.logger.Infof("creating informer for cluster: %s", cluster.GetId())
	serverlessServiceInformer := cluster.GetKubeClient().DynamicInformerFactory().ForResource(constants.KnativeServerlessServiceResource)
	serverlessServiceInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			return cluster.GetNamespaceFilter().IsNamespaceMopEnabledForObject(obj)
		},
		Handler: NewServerlessServiceControllerEventHandler(c.serverlessServiceEnqueuer, cluster.GetId().String(), c.logger),
	})
	errorHandler := NewWatchErrorHandlerWithMetrics(c.logger, cluster.GetId().String(), "knative ServerlessService", c.metricsRegistry)
	_ = serverlessServiceInformer.Informer().SetWatchErrorHandler(errorHandler)

	return nil
}

func (c *KnativeServerlessServiceController) reconcile(item QueueItem) error {
	reconcileId := uuid.New().String()
	namespace, name, err := cache.SplitMetaNamespaceKey(item.key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", item.key))
		return nil
	}

	clusterInEvent := item.cluster
	serverlessServiceCtxLogger := c.logger.With("serverlessServiceNamespace", namespace, "serverlessServiceName", name, "serverlessServiceReconcileId", reconcileId, "cluster", clusterInEvent)

	// Make sure we're not running concurrent reconciles for the same ServerlessService object
	serverlessServiceKeyAlreadyTracked := c.multiClusterReconcileTracker.trackKey(namespace, name)
	if serverlessServiceKeyAlreadyTracked {
		return &meshOpErrors.NonCriticalReconcileError{
			Message: fmt.Sprintf("ServerlessService is already being reconciled: %s", namespace+"/"+name),
		}
	}
	defer c.multiClusterReconcileTracker.unTrackKey(serverlessServiceCtxLogger, clusterInEvent, namespace, name)
	defer IncrementCounterForQueueItem(c.metricsRegistry, metrics.KnativeServerlessServiceReconciledTotal, clusterInEvent, item)
	_, clusterClient := c.clusterManager.GetClusterById(clusterInEvent)
	var serverlessServiceInEvent *unstructured.Unstructured = nil
	if item.event == controllers_api.EventAdd || item.event == controllers_api.EventUpdate {
		var serverlessServiceObject runtime.Object
		serverlessServiceObject, err = clusterClient.GetKubeClient().DynamicInformerFactory().ForResource(constants.KnativeServerlessServiceResource).Lister().ByNamespace(namespace).Get(name)
		if err != nil {
			c.logger.Infof("ServerlessService object not found for given cluster - %s", clusterInEvent)
			return nil
		}
		serverlessServiceInEvent = serverlessServiceObject.(*unstructured.Unstructured)
		serverlessServiceCtxLogger.Infof("reconciling add or update event - %s/%s", serverlessServiceInEvent.GetNamespace(), serverlessServiceInEvent.GetName())
		err = c.createServerlessServiceConfig(clusterInEvent, serverlessServiceInEvent, serverlessServiceCtxLogger)
	} else if item.event == controllers_api.EventDelete {
		serverlessServiceCtxLogger.Infof("reconciling delete event - %s/%s", namespace, name)
		err = c.deleteMeshConfig(clusterInEvent, namespace, name, item.uid, serverlessServiceCtxLogger)
	}

	if err != nil {
		if meshOpErrors.IsNonCriticalReconcileError(err) {
			// Non-critical err, retrying
			IncrementCounterForQueueItem(c.metricsRegistry, metrics.KnativeServerlessServiceReconcileNonCriticalError, clusterInEvent, item)
		} else {
			IncrementCounterForQueueItem(c.metricsRegistry, metrics.KnativeServerlessServiceReconcileFailed, clusterInEvent, item)
			serverlessServiceCtxLogger.Debugf("recording event for %d for %s/%s", item.event, namespace, name)
			if item.event != controllers_api.EventDelete {
				clusterClient.GetEventRecorder().Event(serverlessServiceInEvent, v1.EventTypeWarning, constants.MeshConfigError, err.Error())
			}
		}
	}

	return err
}

func (c *KnativeServerlessServiceController) createServerlessServiceConfig(clusterName string, serverlessService *unstructured.Unstructured, ctxLogger *zap.SugaredLogger) error {
	isServerlessServiceTracked := true
	if serverlessService.GetUID() == "" {
		return nil
	}
	trackedServerlessService := resources.ServerlessServiceToReference(clusterName, serverlessService)
	msm, err := c.resourceManager.TrackOwner(clusterName, serverlessService.GetNamespace(), trackedServerlessService, ctxLogger)
	if err != nil {
		isServerlessServiceTracked = false
		c.logger.Errorf("encountered error while tracking ServerlessService: %v", err)
	}

	configObjects, err := c.generateConfig(clusterName, serverlessService, msm, ctxLogger)
	if err != nil {
		return err
	}

	_, cluster := c.clusterManager.GetClusterById(clusterName)

	if isServerlessServiceTracked {
		trackingError := c.resourceManager.OnOwnerChange(clusterName, cluster.GetKubeClient(), msm, serverlessService, trackedServerlessService, templating.GetConfigObjects(configObjects), ctxLogger)
		return common.GetFirstNonNil(err, trackingError)
	}
	return err
}

func (c *KnativeServerlessServiceController) generateConfig(clusterName string, serverlessService *unstructured.Unstructured, msm *mopv1.MeshServiceMetadata, ctxLogger *zap.SugaredLogger) ([]*templating.AppliedConfigObject, error) {
	metadata := map[string]string{}

	renderCtx := templating.NewRenderRequestContext(serverlessService, metadata, MsmToOwnerRef(msm), clusterName, "", nil, nil)
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

func (c *KnativeServerlessServiceController) deleteMeshConfig(clusterName, namespace, name string, uid types.UID, ctxLogger *zap.SugaredLogger) error {
	ctxLogger.Infof("deleting mesh config for ServerlessService: %s/%s/%s/%s", clusterName, namespace, name, uid)
	return c.resourceManager.UnTrackOwner(clusterName, namespace, constants.KnativeServerlessServiceKind.Kind, name, uid, ctxLogger)
}

func serverlessServiceResourcePresentInCluster(cluster controllers_api.Cluster) bool {
	_, err := kube.ConvertGvkToGvr(
		cluster.GetId().String(),
		cluster.GetKubeClient().Discovery(),
		schema.GroupVersionKind{
			Group:   constants.KnativeServerlessServiceKind.Group,
			Version: constants.KnativeServerlessServiceKind.Version,
			Kind:    constants.KnativeServerlessServiceKind.Kind})

	return err == nil
}

// NewServerlessServiceControllerEventHandler creates an event handler for ServerlessService resources
func NewServerlessServiceControllerEventHandler(enqueuer controllers_api.ObjectEnqueuer, clusterName string, ctxLogger *zap.SugaredLogger) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			serverlessServiceObj := obj.(*unstructured.Unstructured)
			ctxLogger.Debugw("informer event: Add", "namespace", serverlessServiceObj.GetNamespace(), "serverlessServiceName", serverlessServiceObj.GetName())
			enqueuer.Enqueue(clusterName, serverlessServiceObj, controllers_api.EventAdd)
		},
		UpdateFunc: func(old, new interface{}) {
			newServerlessServiceObj := new.(*unstructured.Unstructured)
			oldServerlessServiceObj := old.(*unstructured.Unstructured)
			ctxLogger.Debugw("informer event: Update", "namespace", newServerlessServiceObj.GetNamespace(), "serverlessServiceName", newServerlessServiceObj.GetName(),
				"newServerlessServiceRV", newServerlessServiceObj.GetResourceVersion(), "oldServerlessServiceRV", oldServerlessServiceObj.GetResourceVersion())
			if newServerlessServiceObj.GetResourceVersion() == oldServerlessServiceObj.GetResourceVersion() {
				return
			}
			enqueuer.Enqueue(clusterName, newServerlessServiceObj, controllers_api.EventUpdate)
		},
		DeleteFunc: func(obj interface{}) {
			serverlessServiceObj := obj.(*unstructured.Unstructured)
			ctxLogger.Debugw("informer event: Delete", "namespace", serverlessServiceObj.GetNamespace(), "name", serverlessServiceObj.GetName())
			enqueuer.Enqueue(clusterName, serverlessServiceObj, controllers_api.EventDelete)
		},
	}
}
