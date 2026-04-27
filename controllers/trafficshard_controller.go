package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	meshv1alpha1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"
	meshOpErrors "github.com/istio-ecosystem/mesh-operator/pkg/errors"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	"github.com/istio-ecosystem/mesh-operator/pkg/matching"
	"github.com/istio-ecosystem/mesh-operator/pkg/resources"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
)

// TSPLabelConfig holds the label keys used to resolve route destinations to services.
// These are parametrized so they can be configured via CLI flags in the helm chart.
type TSPLabelConfig struct {
	CellTypeLabel        string
	ServiceInstanceLabel string
	CellLabel            string
	ServiceNameLabel     string
}

type TrafficShardController struct {
	logger                       *zap.SugaredLogger
	configGenerator              ConfigGenerator
	resourceManager              resources.ResourceManager
	clusterManager               controllers_api.ClusterManager
	tspEnqueuer                  controllers_api.QueueAccessor
	metricsRegistry              *prometheus.Registry
	multiClusterReconcileTracker ReconcileManager
	labelConfig                  TSPLabelConfig
}

func NewTrafficShardController(
	logger *zap.SugaredLogger,
	renderer templating.TemplateRenderer,
	applicator templating.Applicator,
	resourceManager resources.ResourceManager,
	clusterManager controllers_api.ClusterManager,
	metricsRegistry *prometheus.Registry,
	labelConfig TSPLabelConfig,
) controllers_api.MulticlusterController {

	ctxLogger := logger.With("controller", "tsp-multicluster")
	workQueue := createWorkQueueWithWatchdog(context.Background(), "tsp-multicluster", ctxLogger, metricsRegistry)

	mutators := []templating.Mutator{
		&templating.ManagedByMutator{},
		&templating.OwnerRefMutator{},
		&templating.ConfigResourceParentMutator{},
	}

	configGenerator := NewConfigGenerator(renderer, mutators, nil, applicator, templating.DontApplyOverlays, ctxLogger, metricsRegistry)

	controller := &TrafficShardController{
		logger:                       ctxLogger,
		configGenerator:              configGenerator,
		resourceManager:              resourceManager,
		clusterManager:               clusterManager,
		tspEnqueuer:                  NewSingleQueueEnqueuer(workQueue),
		metricsRegistry:              metricsRegistry,
		multiClusterReconcileTracker: NewMultiClusterReconcileTracker(ctxLogger),
		labelConfig:                  labelConfig,
	}

	return controller
}

func (c *TrafficShardController) Run(workerThreads int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.tspEnqueuer.GetQueue().ShutDown()

	c.logger.Info("Starting TSP multicluster controller")
	onRetryHandler := OnControllerRetryHandler(commonmetrics.TSPRetries, c.metricsRegistry)

	c.logger.Infof("Starting %d workerThreads", workerThreads)
	for i := 0; i < workerThreads; i++ {
		go wait.Until(func() {
			RunWorker(c.logger, c.tspEnqueuer.GetQueue(), c.reconcile, onRetryHandler)
		}, time.Second, stopCh)
	}

	c.logger.Info("TSP controller started")
	<-stopCh
	c.logger.Info("Shutting down TSP controller")

	return nil
}

func (c *TrafficShardController) reconcile(item QueueItem) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(item.key)
	if err != nil {
		return err
	}
	clusterName := item.cluster

	tspCtxLogger := c.logger.With("tspNamespace", namespace, "tspName", name, "cluster", clusterName)

	// Make sure we're not running concurrent reconciles for the same TSP object
	tspKeyAlreadyTracked := c.multiClusterReconcileTracker.trackKey(namespace, name)
	if tspKeyAlreadyTracked {
		IncrementCounterForObject(c.metricsRegistry, commonmetrics.TSPReconcileNonCriticalError, clusterName, namespace, name, "concurrent-reconcile")
		return &meshOpErrors.NonCriticalReconcileError{Message: fmt.Sprintf("TSP is already being reconciled: %s", namespace+"/"+name)}
	}
	defer c.multiClusterReconcileTracker.unTrackKey(tspCtxLogger, clusterName, namespace, name)
	defer IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.TSPReconciledTotal, clusterName, item)

	// Handle delete events first, before trying to read from cache
	if item.event == controllers_api.EventDelete {
		tspCtxLogger.Infof("reconciling delete event - %s/%s", namespace, name)
		err = c.deleteMeshConfig(clusterName, namespace, name, item.uid)
		return err
	}

	object, err := c.readObject(clusterName, namespace, name)
	if err != nil {
		return err
	}
	if object == nil {
		c.logger.Warnf("TSP %s/%s in cluster %s no longer exists", namespace, name, clusterName)
		return nil
	}

	tsp := object.(*meshv1alpha1.TrafficShardingPolicy)
	if !tsp.DeletionTimestamp.IsZero() {
		c.logger.Infof("TSP %s/%s in cluster %s is being deleted, cleaning up resources", namespace, name, clusterName)
		return c.deleteMeshConfig(clusterName, namespace, name, tsp.UID)
	}

	err = c.reconcileTSP(clusterName, tsp)
	if err != nil {
		if meshOpErrors.IsUserConfigError(err) {
			tspCtxLogger.Warnf("user config error in TSP: %v", err)
			IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.TSPReconcileUserError, clusterName, item)
			return nil
		}
		tspCtxLogger.Errorf("failed to reconcile TSP: %v", err)
		IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.TSPReconcileFailed, clusterName, item)
	}
	return err
}

func (c *TrafficShardController) reconcileTSP(clusterName string, tsp *meshv1alpha1.TrafficShardingPolicy) error {
	startTime := time.Now()
	c.logger.Infof("Starting reconciliation for TSP %s/%s in cluster %s", tsp.Namespace, tsp.Name, clusterName)
	defer func() {
		c.logger.Infof("Completed reconciliation for TSP %s/%s in cluster %s in %v", tsp.Namespace, tsp.Name, clusterName, time.Since(startTime))
	}()

	services, err := c.listMatchingServices(tsp)
	if err != nil {
		c.recordReconcileLatency(clusterName, "Failed", time.Since(startTime))
		_ = c.updateTSPStatus(clusterName, tsp, "Failed", fmt.Sprintf("Failed to list services: %v", err), nil)
		return fmt.Errorf("error listing services for TSP %s/%s: %w", tsp.Namespace, tsp.Name, err)
	}

	// Build matched services list for status
	matchedServices := []string{}
	for _, svc := range services {
		matchedServices = append(matchedServices, fmt.Sprintf("%s/%s", svc.Namespace, svc.Name))
	}

	commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.TSPMatchedServicesCount, map[string]string{
		commonmetrics.ClusterLabel:      clusterName,
		commonmetrics.NamespaceLabel:    tsp.Namespace,
		commonmetrics.ResourceNameLabel: tsp.Name,
	}, c.metricsRegistry).Set(float64(len(matchedServices)))

	// Validate routes against matched services
	statusMsg, validationErr := c.validateRoutes(tsp, services)
	if validationErr != nil {
		c.recordReconcileLatency(clusterName, "Failed", time.Since(startTime))
		_ = c.updateTSPStatus(clusterName, tsp, "Failed", validationErr.Error(), matchedServices)
		return &meshOpErrors.UserConfigError{Message: fmt.Sprintf("validation failed for TSP %s/%s: %s", tsp.Namespace, tsp.Name, validationErr.Error())}
	}

	// Only pass services that match a route destination to the template
	targetServices := c.filterServicesMatchingRoutes(tsp, services)

	if err := c.applyResources(clusterName, tsp, targetServices); err != nil {
		c.logger.Errorf("Failed to apply resources for TSP %s/%s in cluster %s: %v", tsp.Namespace, tsp.Name, clusterName, err)
		c.recordReconcileLatency(clusterName, "Failed", time.Since(startTime))
		_ = c.updateTSPStatus(clusterName, tsp, "Failed", err.Error(), matchedServices)
		return fmt.Errorf("failed to apply resources for TSP %s/%s: %w", tsp.Namespace, tsp.Name, err)
	}

	// Update status - only retry on conflicts to avoid re-running entire reconciliation
	if err := c.updateTSPStatus(clusterName, tsp, "Succeeded", statusMsg, matchedServices); err != nil {
		if k8serrors.IsConflict(err) {
			// Conflict means TSP was modified, retry entire reconciliation
			IncrementCounterForObject(c.metricsRegistry, commonmetrics.TSPConflictTotal, clusterName, tsp.Namespace, tsp.Name, "status-update")
			c.recordReconcileLatency(clusterName, "Failed", time.Since(startTime))
			return err
		}
		IncrementCounterForObject(c.metricsRegistry, commonmetrics.TSPStatusUpdateFailed, clusterName, tsp.Namespace, tsp.Name, "status-update")
		c.logger.Warnf("Failed to update TSP %s/%s status but reconciliation succeeded: %v", tsp.Namespace, tsp.Name, err)
	}
	c.logger.Infof("Successfully reconciled TSP %s/%s in cluster %s with %d matched services", tsp.Namespace, tsp.Name, clusterName, len(matchedServices))
	c.recordReconcileLatency(clusterName, "Succeeded", time.Since(startTime))
	return nil
}

func (c *TrafficShardController) listMatchingServices(tsp *meshv1alpha1.TrafficShardingPolicy) ([]*corev1.Service, error) {
	var allServices []*corev1.Service

	for _, cluster := range c.clusterManager.GetExistingHealthyClusters() {
		clusterID := cluster.GetId()

		serviceListFunc := func(ns string, selector labels.Selector) ([]*corev1.Service, error) {
			return cluster.GetKubeClient().KubeInformerFactory().Core().V1().Services().Lister().Services(ns).List(selector)
		}
		services, err := matching.ListServicesForTsp(tsp, serviceListFunc)
		if err != nil {
			c.logger.Warnf("Failed to list services from cluster %s: %v", clusterID, err)
			// Continue with other clusters instead of failing completely
			commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.TSPClusterListingFailed, map[string]string{
				commonmetrics.ClusterLabel: clusterID.String(),
			}, c.metricsRegistry).Inc()
			continue
		}
		c.logger.Debugf("Found %d service(s) in cluster %s matching TSP %s/%s", len(services), clusterID, tsp.Namespace, tsp.Name)
		allServices = append(allServices, services...)
	}

	c.logger.Infof("Total services found across all clusters: %d", len(allServices))
	return allServices, nil
}

func (c *TrafficShardController) applyResources(
	clusterName string,
	tsp *meshv1alpha1.TrafficShardingPolicy,
	services []*corev1.Service,
) error {
	_, cluster := c.clusterManager.GetClusterById(clusterName)
	if cluster == nil {
		return fmt.Errorf("cluster %s not found", clusterName)
	}

	isTspTracked := true
	trackedTsp := resources.TspToReference(clusterName, tsp)
	msm, err := c.resourceManager.TrackOwner(clusterName, tsp.Namespace, trackedTsp, c.logger)
	if err != nil {
		isTspTracked = false
		c.logger.Errorf("encountered error while tracking TSP: %v", err)
	}

	configObjects, err := c.generateConfig(clusterName, tsp, services, msm)
	if err != nil {
		return err
	}

	if isTspTracked {
		trackingError := c.resourceManager.OnOwnerChange(clusterName, cluster.GetKubeClient(), msm, tsp, trackedTsp, templating.GetConfigObjects(configObjects), c.logger)
		return trackingError
	}

	return nil
}

func (c *TrafficShardController) generateConfig(
	clusterName string,
	tsp *meshv1alpha1.TrafficShardingPolicy,
	services []*corev1.Service,
	msm *meshv1alpha1.MeshServiceMetadata,
) ([]*templating.AppliedConfigObject, error) {
	tspCopy := tsp.DeepCopy()
	tspCopy.SetGroupVersionKind(constants.TrafficShardingPolicyKind)
	tspUnstructured, err := kube.ObjectToUnstructured(tspCopy)
	if err != nil {
		IncrementCounterForObject(c.metricsRegistry, commonmetrics.TSPConfigGenerationFailed, clusterName, tsp.Namespace, tsp.Name, "object-to-unstructured")
		return nil, fmt.Errorf("failed to convert TSP to unstructured: %w", err)
	}

	tspUnstructured.Object["matchedServices"] = c.servicesToUnstructured(services)

	renderCtx := templating.NewRenderRequestContext(
		tspUnstructured,
		map[string]string{},
		MsmToOwnerRef(msm),
		clusterName,
		"",
		nil,
		nil,
	)

	applicatorResults, generateConfigErr := c.configGenerator.GenerateConfig(&renderCtx, NoOpOnBeforeApply, c.logger)
	if generateConfigErr != nil {
		IncrementCounterForObject(c.metricsRegistry, commonmetrics.TSPConfigGenerationFailed, clusterName, tsp.Namespace, tsp.Name, "generate-config")
		return nil, fmt.Errorf("failed to generate and apply config: %w", generateConfigErr)
	}

	if len(applicatorResults) == 0 {
		c.logger.Infof("No resources generated for TSP %s/%s", tsp.Namespace, tsp.Name)
		return []*templating.AppliedConfigObject{}, nil
	}

	for _, result := range applicatorResults {
		if result.Error != nil {
			obj := result.Object
			IncrementCounterForObject(c.metricsRegistry, commonmetrics.TSPConfigGenerationFailed, clusterName, tsp.Namespace, tsp.Name, "upsert-object")
			return nil, fmt.Errorf("failed to upsert object %s.%s of kind %v: %w",
				obj.GetName(), obj.GetNamespace(), obj.GroupVersionKind(), result.Error)
		}
	}

	c.logger.Infof("Successfully applied resources for TSP %s/%s", tsp.Namespace, tsp.Name)
	return applicatorResults, nil
}

func (c *TrafficShardController) OnNewClusterAdded(cluster controllers_api.Cluster) error {
	c.logger.Infof("Initializing TSP controller for cluster: %s", cluster.GetId())
	clusterName := cluster.GetId().String()

	tspInformer := cluster.GetKubeClient().MopInformerFactory().Mesh().V1alpha1().TrafficShardingPolicies().Informer()
	tspInformer.AddEventHandler(&tspEventHandler{clusterName: clusterName, queue: c.tspEnqueuer})

	errorHandler := NewWatchErrorHandlerWithMetrics(c.logger, clusterName, "tsp", c.metricsRegistry)
	_ = tspInformer.SetWatchErrorHandler(errorHandler)

	serviceInformer := cluster.GetKubeClient().KubeInformerFactory().Core().V1().Services().Informer()

	serviceInformer.AddEventHandler(&cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) { c.enqueueMatchingTsps(cluster, obj.(*corev1.Service)) },
		UpdateFunc: func(old, new interface{}) {
			oldSvc, ok1 := old.(*corev1.Service)
			newSvc, ok2 := new.(*corev1.Service)
			if !ok1 || !ok2 {
				c.enqueueMatchingTsps(cluster, new.(*corev1.Service))
				return
			}
			if !reflect.DeepEqual(oldSvc.Labels, newSvc.Labels) ||
				!reflect.DeepEqual(oldSvc.Spec.Ports, newSvc.Spec.Ports) ||
				!reflect.DeepEqual(oldSvc.Spec.Selector, newSvc.Spec.Selector) {
				c.enqueueMatchingTsps(cluster, newSvc)
			}
		},
		DeleteFunc: func(obj interface{}) { c.enqueueMatchingTsps(cluster, obj.(*corev1.Service)) },
	})

	return nil
}

func (c *TrafficShardController) GetObjectEnqueuer() controllers_api.ObjectEnqueuer {
	return c.tspEnqueuer
}

func (c *TrafficShardController) enqueueMatchingTsps(cluster controllers_api.Cluster, svc *corev1.Service) {
	clusterName := cluster.GetId().String()

	tspListFunc := func(namespace string) ([]*meshv1alpha1.TrafficShardingPolicy, error) {
		return cluster.GetKubeClient().MopInformerFactory().Mesh().V1alpha1().TrafficShardingPolicies().Lister().
			TrafficShardingPolicies(namespace).List(labels.Everything())
	}

	matchingTsps, err := matching.ListTspsForService(svc, tspListFunc)
	if err != nil {
		c.logger.Errorf("Error finding TSPs for service %s/%s in cluster %s: %v", svc.Namespace, svc.Name, clusterName, err)
		return
	}

	for _, tsp := range matchingTsps {
		enqueue(c.tspEnqueuer.GetQueue(), clusterName, tsp, controllers_api.EventUpdate)
	}
}

func (c *TrafficShardController) readObject(clusterName string, namespace string, name string) (interface{}, error) {
	_, cluster := c.clusterManager.GetClusterById(clusterName)
	if cluster == nil {
		return nil, fmt.Errorf("cluster %s not found", clusterName)
	}

	return cluster.GetKubeClient().MopInformerFactory().Mesh().V1alpha1().TrafficShardingPolicies().Lister().
		TrafficShardingPolicies(namespace).Get(name)
}

func (c *TrafficShardController) deleteMeshConfig(clusterName, namespace, name string, uid types.UID) error {
	c.logger.Infof("deleting mesh config for TSP: %s/%s/%s/%s", clusterName, namespace, name, uid)
	return c.resourceManager.UnTrackOwner(clusterName, namespace, constants.TrafficShardingPolicyKind.Kind, name, uid, c.logger)
}

func (c *TrafficShardController) updateTSPStatus(
	clusterName string,
	tsp *meshv1alpha1.TrafficShardingPolicy,
	state string,
	message string,
	matchedServices []string,
) error {
	tspCopy := tsp.DeepCopy()
	tspCopy.Status.State = meshv1alpha1.Phase(state)
	tspCopy.Status.Message = message
	tspCopy.Status.ObservedGeneration = tsp.Generation
	tspCopy.Status.MatchedServices = matchedServices

	_, cluster := c.clusterManager.GetClusterById(clusterName)
	if cluster == nil {
		return fmt.Errorf("cluster %s not found, cannot update TSP status", clusterName)
	}

	// Create context with timeout for status update
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := cluster.GetKubeClient().MopApiClient().MeshV1alpha1().TrafficShardingPolicies(tsp.Namespace).UpdateStatus(
		ctx,
		tspCopy,
		metav1.UpdateOptions{},
	)
	return err
}

// Hard failures (error):
//   - p_servicename does not match any K8s service name (only temporary limitation)
//   - A route destination does not resolve to any matched service (previously supported via helm templates)
//
// Soft (no error, info in message):
//   - No services matched the serviceSelector
func (c *TrafficShardController) validateRoutes(tsp *meshv1alpha1.TrafficShardingPolicy, services []*corev1.Service) (string, error) {
	// No services matched — succeed with informational message
	if len(services) == 0 {
		return "No services matched the serviceSelector; ensure target services exist with the correct labels", nil
	}

	// Hard check: p_servicename must NOT equal any K8s service name (not supported)
	if c.labelConfig.ServiceNameLabel != "" {
		if svcName, ok := tsp.Spec.ServiceSelector.Labels[c.labelConfig.ServiceNameLabel]; ok && svcName != "" {
			for _, svc := range services {
				if svc.Name == svcName {
					return "", fmt.Errorf("%s=%q matches Kubernetes service name %q; selecting services by name is not currently supported", c.labelConfig.ServiceNameLabel, svcName, svc.Name)
				}
			}
		}
	}

	// Hard check: each route destination must resolve to at least one matched service
	for _, route := range tsp.Spec.Routes {
		dest := route.Destination
		found := false
		for _, svc := range services {
			svcLabels := svc.Labels
			if dest.CellType != "" && svcLabels[c.labelConfig.CellTypeLabel] == dest.CellType {
				found = true
				break
			}
			if dest.ServiceInstance != "" && svcLabels[c.labelConfig.ServiceInstanceLabel] == dest.ServiceInstance {
				found = true
				break
			}
			if dest.Cell != "" && svcLabels[c.labelConfig.CellLabel] == dest.Cell {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("route %q: destination does not match any service", route.Name)
		}
	}

	return "Successfully reconciled", nil
}

// filterServicesMatchingRoutes returns only the services that match at least one
// route destination. This ensures the template only receives services relevant
// to the TSP's routing rules.
func (c *TrafficShardController) filterServicesMatchingRoutes(tsp *meshv1alpha1.TrafficShardingPolicy, services []*corev1.Service) []*corev1.Service {
	var filtered []*corev1.Service
	for _, svc := range services {
		svcLabels := svc.Labels
		for _, route := range tsp.Spec.Routes {
			dest := route.Destination
			if dest.CellType != "" && svcLabels[c.labelConfig.CellTypeLabel] == dest.CellType {
				filtered = append(filtered, svc)
				break
			}
			if dest.ServiceInstance != "" && svcLabels[c.labelConfig.ServiceInstanceLabel] == dest.ServiceInstance {
				filtered = append(filtered, svc)
				break
			}
			if dest.Cell != "" && svcLabels[c.labelConfig.CellLabel] == dest.Cell {
				filtered = append(filtered, svc)
				break
			}
		}
	}
	return filtered
}

func (c *TrafficShardController) recordReconcileLatency(clusterName string, phase string, duration time.Duration) {
	latencyLabels := map[string]string{
		commonmetrics.ClusterLabel:        clusterName,
		commonmetrics.ResourceKind:        "TrafficShardingPolicy",
		commonmetrics.ReconcilePhaseLabel: phase,
	}
	commonmetrics.GetOrRegisterSummaryWithLabels(
		commonmetrics.ReconcileLatencyMetric,
		c.metricsRegistry,
		map[float64]float64{0.25: 0.1, 0.5: 0.1, 0.95: 0.1, 0.99: 0.1, 1.0: 0.1},
		latencyLabels,
	).Observe(duration.Seconds())
}

func (c *TrafficShardController) servicesToUnstructured(services []*corev1.Service) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(services))
	for _, svc := range services {
		svcUnstructured, err := kube.ObjectToUnstructured(svc)
		if err != nil {
			c.logger.Warnf("Failed to convert service %s/%s to unstructured: %v", svc.Namespace, svc.Name, err)
			continue
		}
		result = append(result, svcUnstructured.Object)
	}
	return result
}
