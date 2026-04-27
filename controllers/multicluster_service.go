package controllers

import (
	"context"
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/cluster"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	"github.com/istio-ecosystem/mesh-operator/pkg/deployment"

	"github.com/istio-ecosystem/mesh-operator/pkg/statefulset"

	"github.com/istio-ecosystem/mesh-operator/pkg/reconcilemetadata"

	"k8s.io/apimachinery/pkg/runtime"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	constants2 "github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/generated/clientset/versioned"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/istio-ecosystem/mesh-operator/pkg/matching"

	"time"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/multicluster"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	meshOpErrors "github.com/istio-ecosystem/mesh-operator/pkg/errors"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/google/uuid"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube"

	"github.com/istio-ecosystem/mesh-operator/pkg/transition"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"

	"github.com/istio-ecosystem/mesh-operator/pkg/dynamicrouting"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/resources"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
)

// MulticlusterServiceController - A Service controller implementation, capable of handling reconciliation for multiple clusters
type MulticlusterServiceController struct {
	logger                       *zap.SugaredLogger
	configGenerator              ConfigGenerator
	serviceEnqueuer              controllers_api.QueueAccessor
	resourceManager              resources.ResourceManager
	metricsRegistry              *prometheus.Registry
	clusterManager               controllers_api.ClusterManager
	multiClusterReconcileTracker ReconcileManager
	dryRun                       bool
	primaryClient                kube.Client
	mutatingTemplatesManager     templating.TemplatesManager
	serviceTemplatesManager      templating.TemplatesManager
	additionalObjectManager      AdditionalObjectManager
	controllerCfg                *common.ControllerConfig
	reconcileOptimizer           ReconcileOptimizer
}

func NewMulticlusterServiceController(
	logger *zap.SugaredLogger,
	primaryKubeClient kube.Client,
	renderer templating.TemplateRenderer,
	applicator templating.Applicator,
	clusterManager controllers_api.ClusterManager,
	resourceManager resources.ResourceManager,
	multiClusterReconcileTracker ReconcileManager,
	metricsRegistry *prometheus.Registry,
	mutatingTemplatesManager templating.TemplatesManager,
	serviceTemplatesManager templating.TemplatesManager,
	additionalObjectManager AdditionalObjectManager,
	dryRun bool,
	controllerCfg *common.ControllerConfig,
) controllers_api.MulticlusterController {

	ctxLogger := logger.With("controller", "Services-multicluster")

	// Multicluster work queue
	svcQueue := createWorkQueueWithWatchdog(context.Background(), "Services-multicluster", ctxLogger, metricsRegistry)

	configMutators := CreateServiceConfigMutators()

	comparer := transition.NewMeshConfigComparer(logger, primaryKubeClient, metricsRegistry, "multicluster")

	configGenerator := NewConfigGenerator(renderer, configMutators, comparer, applicator, templating.ApplyOverlays, ctxLogger, metricsRegistry)

	return &MulticlusterServiceController{
		logger:                       ctxLogger,
		configGenerator:              configGenerator,
		serviceEnqueuer:              NewSingleQueueEnqueuer(svcQueue),
		clusterManager:               clusterManager,
		resourceManager:              resourceManager,
		metricsRegistry:              metricsRegistry,
		multiClusterReconcileTracker: multiClusterReconcileTracker,
		dryRun:                       dryRun,
		primaryClient:                primaryKubeClient,
		mutatingTemplatesManager:     mutatingTemplatesManager,
		serviceTemplatesManager:      serviceTemplatesManager,
		additionalObjectManager:      additionalObjectManager,
		controllerCfg:                controllerCfg,
		reconcileOptimizer:           NewServiceReconcileOptimizer(primaryKubeClient.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas(), reconcilemetadata.NewReconcileHashManager()),
	}
}

func (c *MulticlusterServiceController) Run(workerThreads int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.serviceEnqueuer.GetQueue().ShutDown()

	c.logger.Info("starting multicluster-service controller...")

	onRetryHandler := OnControllerRetryHandler(
		metrics.ServiceRetries,
		c.metricsRegistry)

	c.logger.Info("starting workers")
	// Launch workers to process Service resources
	for i := 0; i < workerThreads; i++ {
		go wait.Until(func() {
			RunWorker(
				c.logger,
				c.serviceEnqueuer.GetQueue(),
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

func (c *MulticlusterServiceController) OnNewClusterAdded(cluster controllers_api.Cluster) error {
	c.logger.Infof("initializing cluster: %s", cluster.GetId())

	// STS indexer and handler
	statefulSetInformer := cluster.GetKubeClient().KubeInformerFactory().Apps().V1().StatefulSets()
	err := statefulset.AddStsIndexerIfNotExists(statefulSetInformer.Informer())
	if err != nil {
		return fmt.Errorf("error adding indexer for STSInformer in cluster %s: %w", cluster.GetId(), err)
	}

	statefulSetInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			return cluster.GetNamespaceFilter().IsNamespaceMopEnabledForObject(obj)
		},
		Handler: CreateStsObjectHandler(func(event controllers_api.Event, objects ...interface{}) {
			c.enqueueServicesForStatefulSet(cluster, event, objects...)
		}),
	})
	errorHandler := NewWatchErrorHandlerWithMetrics(c.logger, cluster.GetId().String(), "sts", c.metricsRegistry)
	_ = statefulSetInformer.Informer().SetWatchErrorHandler(errorHandler)

	// Rollout informer and indexer
	rolloutInformer, err := createRolloutInformerAndIndexer(c.logger, cluster.GetKubeClient())
	if err != nil {
		return fmt.Errorf("error adding Rollout indexer in cluster %s: %w", cluster.GetId(), err)
	}

	if rolloutInformer != nil && features.EnableDynamicRoutingForBGAndCanaryServices && features.RetainArgoManagedFields {
		// We only need to re-trigger service reconciliation on Rollout change if such rollout is part of the dynamic-routing setup.
		// Similarly, only trigger reconcile when rollout.spec.template.metadata".labels has changed which might affect the dynamic-routing labels combination
		rolloutInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				return cluster.GetNamespaceFilter().IsNamespaceMopEnabledForObject(obj)
			},
			Handler: CreateRelatedRolloutObjectHandler(func(event controllers_api.Event, objects ...interface{}) {
				c.enqueueServicesForRollout(cluster, event, objects...)
			}),
		})
	}

	// Deployment indexer and handler (if enabled)
	if features.EnableDynamicRouting {
		deploymentsInformer := cluster.GetKubeClient().KubeInformerFactory().Apps().V1().Deployments()
		err = deployment.AddDeploymentIndexerIfNotExists(deploymentsInformer.Informer())
		if err != nil {
			return fmt.Errorf("error adding indexer for deployment informer in cluster %s: %w", cluster.GetId(), err)
		}

		deploymentsInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				containsLabel := dynamicrouting.ContainsDynamicRoutingServiceLabel(obj)
				return containsLabel && cluster.GetNamespaceFilter().IsNamespaceMopEnabledForObject(obj)
			},
			Handler: CreateDeploymentObjectHandler(func(event controllers_api.Event, objects ...interface{}) {
				c.enqueueServicesForDeployments(cluster, event, objects...)
			}),
		})
		errorHandler := NewWatchErrorHandlerWithMetrics(c.logger, cluster.GetId().String(), "deployments", c.metricsRegistry)
		_ = deploymentsInformer.Informer().SetWatchErrorHandler(errorHandler)
	}

	// Add service handler
	servicesInformer := cluster.GetKubeClient().KubeInformerFactory().Core().V1().Services().Informer()
	servicesInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			return cluster.GetNamespaceFilter().IsNamespaceMopEnabledForObject(obj)
		},
		Handler: cache.ResourceEventHandlerDetailedFuncs{
			AddFunc: func(obj interface{}, isInInitialList bool) {
				svc := obj.(*corev1.Service)
				c.logger.Debugf("informer event: Add namespace: %s service: %s", svc.Namespace, svc.Name)
				if isInInitialList && !c.reconcileOptimizer.ShouldReconcile(c.logger, obj, string(cluster.GetId()), "Service") {
					c.logger.Infof("Reconcile skipped for Service. No change. cluster: %s namespace: %s service: %s", cluster.GetId(), svc.Namespace, svc.Name)
					metrics.EmitObjectsConfiguredStats(c.metricsRegistry, string(cluster.GetId()), svc.Namespace, svc.Name, "Service", constants2.NotAvailable)
					metrics.GetOrRegisterCounterWithLabels(metrics.ReconcileSkipped, map[string]string{metrics.ClusterLabel: string(cluster.GetId()), metrics.NamespaceLabel: svc.Namespace, metrics.ResourceNameLabel: svc.Name}, c.metricsRegistry).Inc()
				} else {
					c.logger.Infow("enqueued on informer event",
						"namespace", svc.GetNamespace(),
						"svcName", svc.GetName(), "cluster",
						cluster.GetId(), "event", "add")
					c.serviceEnqueuer.Enqueue(cluster.GetId().String(), svc, controllers_api.EventAdd)
					EnqueueMopReferencingGivenService(c.logger, cluster.GetId().String(), cluster.GetKubeClient(), cluster.GetMopEnqueuer(), svc)
				}
			},
			UpdateFunc: func(old, new interface{}) {
				newSvc := new.(*corev1.Service)
				oldSvc := old.(*corev1.Service)
				c.logger.Debugw("informer event: Update", "namespace", newSvc.Namespace, "svcName", newSvc.Name,
					"newSvcRV", newSvc.ResourceVersion, "oldSvcRV", oldSvc.ResourceVersion)
				if newSvc.ResourceVersion == oldSvc.ResourceVersion {
					// Init and periodic resync will send update events for all known Services.
					// Two different versions of the same Service will always have different RVs.
					return
				}
				c.serviceEnqueuer.Enqueue(cluster.GetId().String(), newSvc, controllers_api.EventUpdate)
				c.logger.Infow("enqueued on informer event",
					"namespace", newSvc.Namespace,
					"svcName", newSvc.Name, "cluster",
					cluster.GetId(), "event", "update")
				EnqueueMopReferencingGivenService(c.logger, cluster.GetId().String(), cluster.GetKubeClient(), cluster.GetMopEnqueuer(), oldSvc)
				EnqueueMopReferencingGivenService(c.logger, cluster.GetId().String(), cluster.GetKubeClient(), cluster.GetMopEnqueuer(), newSvc)
			},
			DeleteFunc: func(obj interface{}) {
				svc := obj.(*corev1.Service)
				c.serviceEnqueuer.Enqueue(cluster.GetId().String(), svc, controllers_api.EventDelete)
				c.logger.Infow("enqueued on informer event",
					"namespace", svc.Namespace,
					"svcName", svc.Name, "cluster",
					cluster.GetId(), "event", "delete")
				EnqueueMopReferencingGivenService(c.logger, cluster.GetId().String(), cluster.GetKubeClient(), cluster.GetMopEnqueuer(), svc)
			},
		},
	})
	errorHandler = NewWatchErrorHandlerWithMetrics(c.logger, cluster.GetId().String(), "services", c.metricsRegistry)
	_ = servicesInformer.SetWatchErrorHandler(errorHandler)

	c.logger.Infof("completed cluster init: %s", cluster.GetId().String())
	return nil
}

func (c *MulticlusterServiceController) reconcile(item QueueItem) error {
	if !features.EnableRoutingConfig {
		// Even if just MOP config is enabled, we still need Service controller to handle re-queue of MOPs related to Service.
		// But, we don't want to render or process the Service events other than re-queue of the MOP.
		return nil
	}

	namespace, name, err := cache.SplitMetaNamespaceKey(item.key)
	if err != nil {
		return fmt.Errorf("invalid resource key: %s", item.key)
	}

	reconcileId := uuid.New()
	svcCtxLogger := c.logger.With("serviceNamespace", namespace, "serviceName", name, "serviceReconcileId", reconcileId, "cluster", item.cluster)

	// Make sure we're not running concurrent reconciles for the same service object
	serviceKeyAlreadyTracked := c.multiClusterReconcileTracker.trackKey(namespace, name)
	if serviceKeyAlreadyTracked {
		return &meshOpErrors.NonCriticalReconcileError{Message: fmt.Sprintf("service is already being reconciled: %s", namespace+"/"+name)}
	}
	defer c.multiClusterReconcileTracker.unTrackKey(svcCtxLogger, item.cluster, namespace, name)
	defer IncrementCounterForQueueItem(c.metricsRegistry, metrics.ServicesReconciledTotal, item.cluster, item)

	clusterInEvent := item.cluster

	// skip reconcile for cluster with cache sync issue
	if !c.clusterManager.IsClusterHealthy(cluster.ID(clusterInEvent)) {
		svcCtxLogger.Warnf("reconcile skipped for service %s/%s in cluster %s with cache sync issue", namespace, name, clusterInEvent)
		return nil
	}

	// Read service(s)
	services, err := c.readServicesFromKnownClusters(namespace, name)
	if err != nil {
		return fmt.Errorf("failed to fetch objects to reconcile: %w", err)
	}

	// Get service that triggered event
	serviceInEvent := services[clusterInEvent]

	var reconcileError error
	if item.event == controllers_api.EventDelete {
		reconcileError = deleteMeshConfig(c.resourceManager, c.metricsRegistry, item.cluster, namespace, name, item.uid, svcCtxLogger)
		// If there are services in other clusters, enqueue Update
		// This is necessary to account for possible change in config after service in another cluster deleted
		for clusterName, service := range GetEnabledServicesOnly(services) {
			if clusterName != clusterInEvent {
				// Pick and enqueue the 1st service that's not the same as one in event
				// This will cover the scenario, where one of the striped services has been deleted.
				c.serviceEnqueuer.Enqueue(clusterName, service, controllers_api.EventUpdate)
				break
			}
		}
	} else if item.event == controllers_api.EventAdd || item.event == controllers_api.EventUpdate {
		if len(services) == 0 || serviceInEvent == nil {
			svcCtxLogger.Warnf("object '%s' in work queue no longer exists", item.key)
			return nil
		}

		if common.IsOperatorDisabled(serviceInEvent) {
			svcCtxLogger.Infof("operator disabled for Service in cluster %s, issuing delete", clusterInEvent)
			c.serviceEnqueuer.Enqueue(clusterInEvent, serviceInEvent, controllers_api.EventDelete)
			return nil
		}

		// Only consider enabled services
		enabledServices := GetEnabledServicesOnly(services)

		stsMetadata, metadataError := c.extractStsMetadata(svcCtxLogger, enabledServices)
		if metadataError != nil {
			svcCtxLogger.Error("cannot extract STS Metadata for %s: %v ", item.key, metadataError.Error())
		}

		mergedService := multicluster.MergeServices(GetServices(enabledServices), serviceInEvent.ResourceVersion)
		dynamicRoutingMetadata, metadataError := c.extractDynamicRoutingMetadata(svcCtxLogger, enabledServices, mergedService)
		if metadataError != nil {
			svcCtxLogger.Error("cannot extract dynamic-routing Metadata for %s: %v", item.key, metadataError.Error())
		}

		metadata := common.MergeMaps(stsMetadata, dynamicRoutingMetadata)

		_, cluster := c.clusterManager.GetClusterById(clusterInEvent)

		isRemoteClusterEvent := !cluster.IsPrimary()
		clusterClient := cluster.GetKubeClient()

		mopsPerCluster, mopsFetchError := c.getMopsForService(enabledServices)

		mergedMops := multicluster.MergeMeshOperators(mopsPerCluster)

		err, policies := c.getPoliciesForService(mergedService)
		if err != nil {
			return err
		}

		// Tracking reference points to the service that triggered the event
		trackingReference := resources.ServiceToReference(clusterInEvent, serviceInEvent)
		reconcileError = createMeshConfig(
			svcCtxLogger,
			c.dryRun,
			clusterInEvent,
			clusterClient,
			isRemoteClusterEvent,
			mergedService,
			trackingReference,
			metadata,
			func(_ *corev1.Service) ([]*v1alpha1.MeshOperator, error) {
				return mergedMops, mopsFetchError
			},
			func(logger *zap.SugaredLogger, _ *corev1.Service, validationError error, errInfo *meshOpErrors.OverlayErrorInfo, mop *v1alpha1.MeshOperator) error {
				return c.updateMopStatusAcrossClusters(
					logger,
					mergedService,
					validationError,
					errInfo,
					mop,
					mopsPerCluster)
			},
			c.primaryClient,
			c.configGenerator,
			c.resourceManager,
			c.metricsRegistry,
			policies)

		if reconcileError == nil {
			for clusterName, svc := range enabledServices {
				found, cluster := c.clusterManager.GetClusterById(clusterName)
				if found {
					mutationError := reMutateRollouts(svcCtxLogger, c.mutatingTemplatesManager, c.serviceTemplatesManager, c.metricsRegistry, svc, clusterName, cluster.GetKubeClient())
					if mutationError != nil {
						svcCtxLogger.Errorf("failed to re-mutate rollout for service: %v", mutationError)
					}
				}
			}
		}

		if policies[constants2.ClusterTrafficPolicyName] != nil {
			clusterTrafficPolicy := policies[constants2.ClusterTrafficPolicyName]
			err = c.updateCTPStatusOnReconcile(reconcileError, clusterTrafficPolicy, v1.Time{time.Now()})
			if err != nil {
				return err
			}
		}
	}

	return handleReconcileError(
		reconcileError,
		item,
		item.cluster,
		c.metricsRegistry,
		func(eventType, reason, message string) {
			c.recordEventForServices(services, eventType, reason, message)
		})
}

func (c *MulticlusterServiceController) getPoliciesForService(service *corev1.Service) (error, map[string]*unstructured.Unstructured) {
	var policies = make(map[string]*unstructured.Unstructured)
	if features.EnableMulticlusterCTP {
		clusterTrafficPolicy, err := c.getPolicyObjectForService(constants2.ClusterTrafficPolicyName, service)
		if err != nil {
			return err, nil
		}
		if clusterTrafficPolicy != nil {
			policies[constants2.ClusterTrafficPolicyName] = clusterTrafficPolicy
		}
	}

	/*
		When the k8s service name matches the logical service identity, this policy can be re-used.
		if features.EnableTrafficShardingPolicy {
			// TSP is handled by dedicated controller, not as service policy
			// shardingPolicy, err := c.getPolicyObjectForService(constants2.TrafficShardingPolicyName, service)
			// if err != nil {
			// 	return err, nil
			// }
			// if shardingPolicy != nil {
			// 	policies[constants2.TrafficShardingPolicyName] = shardingPolicy
			// }
		}
	*/

	return nil, policies
}

func (c *MulticlusterServiceController) getPolicyObjectForService(policyObjectName string, service *corev1.Service) (*unstructured.Unstructured, error) {
	policies, err := c.additionalObjectManager.GetAdditionalObjectsForService(policyObjectName, service)
	if err != nil && !k8serrors.IsNotFound(err) {
		c.logger.Debugf("%s CRD not found in %v/%v/%v", policyObjectName, c.primaryClient.GetClusterName(), service.GetNamespace(), service.GetName())
	}

	// in the case for ClusterTrafficPolicy or TrafficShardingPolicy, there cannot be multiple matching CTP present
	if policies == nil {
		return nil, nil
	}
	if len(policies) > 1 {
		c.logger.Errorf("Multiple %s policy objects detected for service: %v/%v. Not applying any", c.primaryClient.GetClusterName(), service.GetNamespace(), service.GetName())
		return nil, nil
	}

	return policies[0], nil
}

func (c *MulticlusterServiceController) updateCTPStatusOnReconcile(reconcileError error, clusterTrafficPolicy *unstructured.Unstructured, reconciledTime v1.Time) error {
	ctpStatus := createCtpStatus(reconcileError, reconciledTime)
	return c.updateCTPStatusInTheCluster(clusterTrafficPolicy, c.primaryClient.MopApiClient(), c.logger, ctpStatus)
}

func (c *MulticlusterServiceController) enqueueServicesForStatefulSet(cluster controllers_api.Cluster, event controllers_api.Event, objects ...interface{}) {
	EnqueueServicesForStatefulSet(c.logger, cluster.GetId().String(), c.serviceEnqueuer, cluster.GetKubeClient(), event, c.reconcileOptimizer, objects...)
}

func (c *MulticlusterServiceController) enqueueServicesForDeployments(cluster controllers_api.Cluster, event controllers_api.Event, objects ...interface{}) {
	EnqueueServicesForDeployments(c.logger, cluster.GetId().String(), c.serviceEnqueuer, cluster.GetKubeClient(), event, c.reconcileOptimizer, objects...)
}

func (c *MulticlusterServiceController) enqueueServicesForRollout(cluster controllers_api.Cluster, event controllers_api.Event, objects ...interface{}) {
	EnqueueServiceForRolloutObj(c.logger, cluster.GetId().String(), c.serviceEnqueuer, cluster.GetKubeClient(), event, c.reconcileOptimizer, objects...)
}

func (c *MulticlusterServiceController) GetObjectEnqueuer() controllers_api.ObjectEnqueuer {
	return c.serviceEnqueuer
}

// readServiceObject - this method must try and fetch the service object from every known cluster.
// Returns a map of the service objects found where key = cluster name
func (c *MulticlusterServiceController) readServicesFromKnownClusters(namespace string, name string) (map[string]*corev1.Service, error) {
	result := map[string]*corev1.Service{}

	for _, cluster := range c.clusterManager.GetExistingHealthyClusters() {
		service, err := cluster.GetKubeClient().KubeInformerFactory().Core().V1().Services().Lister().Services(namespace).Get(name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		result[cluster.GetId().String()] = service
	}

	return result, nil
}

// getMopsForService - another key function in the multicluster controller.
// It is responsible for fetching MOPs targeting the service from different clusters
func (c *MulticlusterServiceController) getMopsForService(services map[string]*corev1.Service) (map[string][]*v1alpha1.MeshOperator, error) {
	mopsPerCluster := map[string][]*v1alpha1.MeshOperator{}

	for clusterName, service := range services {
		_, cluster := c.clusterManager.GetClusterById(clusterName)

		matchingMopsInCluster, err := matching.ListMopsForService(service, func(namespace string) ([]*v1alpha1.MeshOperator, error) {
			return cluster.GetKubeClient().MopInformerFactory().Mesh().V1alpha1().MeshOperators().Lister().MeshOperators(service.Namespace).List(labels.Everything())
		})

		if err != nil {
			return nil, err
		}

		mopsPerCluster[clusterName] = matchingMopsInCluster
	}
	return mopsPerCluster, nil
}

// recordEventForServices - records an event for each service
func (c *MulticlusterServiceController) recordEventForServices(services map[string]*corev1.Service, eventType, reason, message string) {
	for clusterName, service := range services {
		_, cluster := c.clusterManager.GetClusterById(clusterName)
		if cluster == nil {
			continue
		}

		cluster.GetEventRecorder().Event(ExtractServiceObjectReference(service), eventType, reason, message)
	}
}

// updateMopStatus - this function updates mop status in all clusters where MOP is present
// It's important to use correct original MOP record from the cluster during update because:
//   - object UIDs must match
//   - an MOP in one cluster might be applicable to a different set of services than in another cluster
func (c *MulticlusterServiceController) updateMopStatusAcrossClusters(
	logger *zap.SugaredLogger,
	service *corev1.Service,
	validationError error,
	errInfo *meshOpErrors.OverlayErrorInfo,
	mergedMop *v1alpha1.MeshOperator,
	mopsPerCluster map[string][]*v1alpha1.MeshOperator) error {

	var statusUpdateError error
	for clusterName, mopsInCluster := range mopsPerCluster {
		for _, clusterSpecificMop := range mopsInCluster {
			if clusterSpecificMop.Name == mergedMop.Name {
				found, cluster := c.clusterManager.GetClusterById(clusterName)
				if !found {
					return fmt.Errorf("unknown cluster encountered for service %s/%s: %s", service.Namespace, service.Name, clusterName)
				}
				err := UpdateOverlayMopStatus(
					logger,
					cluster.GetKubeClient().MopApiClient(),
					clusterName,
					service,
					validationError,
					errInfo,
					clusterSpecificMop,
					c.metricsRegistry)
				if err != nil {
					statusUpdateError = err
				}
			}
		}
	}
	return statusUpdateError
}

// extractStsMetadata - extract additional metadata if service is associated with a StatefulSet
// In case of striped services, we do not support it and should be enforced externally.
// In case of a striped service we just pick the oldest cluster/service and make attempt to obtain the metadata
func (c *MulticlusterServiceController) extractStsMetadata(ctxLogger *zap.SugaredLogger, activeServices map[string]*corev1.Service) (map[string]string, error) {
	clusterName, service := multicluster.GetOldestService(activeServices)

	if service == nil {
		return map[string]string{}, nil
	}

	_, cluster := c.clusterManager.GetClusterById(clusterName)

	return ExtractStsMetadata(ctxLogger, service, []kube.Client{cluster.GetKubeClient()}, cluster.GetEventRecorder())
}

// extractDynamicRoutingMetadata - extract dynamic-routing metadata permutations if such is enabled for a service
func (c *MulticlusterServiceController) extractDynamicRoutingMetadata(ctxLogger *zap.SugaredLogger, activeServices map[string]*corev1.Service, mergedService *corev1.Service) (map[string]string, error) {
	if len(activeServices) == 0 {
		return map[string]string{}, nil
	}

	var clients []kube.Client
	for clusterName, _ := range activeServices {
		_, cluster := c.clusterManager.GetClusterById(clusterName)
		clients = append(clients, cluster.GetKubeClient())
	}

	return ExtractDynamicRoutingMetadata(ctxLogger, mergedService, c.metricsRegistry, clients)
}

func createCtpStatus(reconcileError error, reconciledTime v1.Time) v1alpha1.ClusterTrafficPolicyStatus {
	ctpStatus := v1alpha1.ClusterTrafficPolicyStatus{
		State:              constants2.CTPSuccessState,
		LastReconciledTime: reconciledTime.DeepCopy(),
	}

	if reconcileError != nil {
		ctpStatus.State = constants2.CTPFailureState
		ctpStatus.Message = reconcileError.Error()
	}
	return ctpStatus
}

func (c *MulticlusterServiceController) updateCTPStatusInTheCluster(ctp *unstructured.Unstructured, client versioned.Interface, logger *zap.SugaredLogger, status v1alpha1.ClusterTrafficPolicyStatus) error {
	var objOfCTPType *v1alpha1.ClusterTrafficPolicy
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(ctp.UnstructuredContent(), &objOfCTPType)
	if err != nil {
		return err
	}
	objOfCTPType.Status = status

	_, err = client.MeshV1alpha1().ClusterTrafficPolicies(objOfCTPType.GetNamespace()).UpdateStatus(context.TODO(), objOfCTPType, v1.UpdateOptions{})
	if err != nil {
		if k8serrors.IsConflict(err) {
			logger.Infof("conflict while updating CTP status, will retry: %s/%s", objOfCTPType.GetNamespace(), objOfCTPType.GetName())
			return &meshOpErrors.NonCriticalReconcileError{Message: err.Error()}
		} else {
			return fmt.Errorf("error updating CTP status for %v/%v, error: %v", objOfCTPType.GetNamespace(), objOfCTPType.GetName(), err)
		}
	}
	return nil
}
