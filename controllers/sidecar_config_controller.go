package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	meshv1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"
	meshOpErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/generated/clientset/versioned"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/generated/listers/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
)

const (
	// RootSidecarConfigName is the name for root-level SidecarConfig
	RootSidecarConfigName = "root-sidecar"
)

// namespaceSidecarConfigName returns the name of the namespace-level SidecarConfig for the given ns.
func namespaceSidecarConfigName(namespace string) string {
	return namespace + "-namespace--sidecar"
}

// Phase constants for SidecarConfig status
const (
	scPhasePending     meshv1alpha1.Phase = "Pending"
	scPhaseSucceeded   meshv1alpha1.Phase = "Succeeded"
	scPhaseFailed      meshv1alpha1.Phase = "Failed"
	scPhaseTerminating meshv1alpha1.Phase = "Terminating"
)

// SidecarRenderMode determines whether and how an Istio Sidecar is rendered for a namespace.
type SidecarRenderMode string

const (
	// SidecarRenderModeDisabled: namespace not in allowlists; SidecarConfig is reconciled but no Istio Sidecar is rendered.
	SidecarRenderModeDisabled SidecarRenderMode = "Disabled"
	// SidecarRenderModeShadow: namespace in ENABLE_SHADOW_SIDECAR_CONFIG_NAMESPACES only; Sidecar rendered with dummy workload selector.
	SidecarRenderModeShadow SidecarRenderMode = "Shadow"
	// SidecarRenderModeEnabled: namespace in ENABLE_SIDECAR_CONFIG_NAMESPACES; Sidecar rendered with actual workload selector.
	SidecarRenderModeEnabled SidecarRenderMode = "Enabled"
)

// sidecarRenderModeForNamespace returns the render mode for the given namespace from feature flags.
// If ENABLE_SIDECAR_CONFIG_NAMESPACES or ENABLE_SHADOW_SIDECAR_CONFIG_NAMESPACES contains "*", all namespaces are allowed for that mode.
func sidecarRenderModeForNamespace(namespace string) SidecarRenderMode {
	if _, ok := features.EnableSidecarConfigNamespaces[namespace]; ok {
		return SidecarRenderModeEnabled
	}
	if _, ok := features.EnableSidecarConfigNamespaces[features.SidecarConfigAllNamespacesWildcard]; ok {
		return SidecarRenderModeEnabled
	}
	if _, ok := features.EnableShadowSidecarConfigNamespaces[namespace]; ok {
		return SidecarRenderModeShadow
	}
	if _, ok := features.EnableShadowSidecarConfigNamespaces[features.SidecarConfigAllNamespacesWildcard]; ok {
		return SidecarRenderModeShadow
	}
	return SidecarRenderModeDisabled
}

// SidecarConfigController reconciles SidecarConfig CRDs into Istio Sidecar resources.
//
// SidecarConfigs exist at three levels:
//   - Root (mesh-control-plane/default, no workloadSelector): global egress hosts for all workloads
//   - Namespace (<ns>/default, no workloadSelector): namespace-wide egress hosts
//   - Workload (any name, with workloadSelector): service-specific egress hosts → generates Istio Sidecar
//
// Only workload-level SidecarConfigs produce Istio Sidecar CRs. Root/namespace configs trigger
// re-reconciliation of affected workload configs so their hosts get merged in.
// Deduplication is by (hostname, port). OutboundTrafficPolicy is hardcoded to ALLOW_ANY.
type SidecarConfigController struct {
	logger *zap.SugaredLogger

	sidecarConfigClient   versioned.Interface
	sidecarConfigInformer cache.SharedIndexInformer
	sidecarConfigLister   v1alpha1.SidecarConfigLister

	clusterName           string
	client                kube.Client
	recorder              record.EventRecorder
	metricsRegistry       *prometheus.Registry
	sidecarConfigEnqueuer controllers_api.QueueAccessor
	dryRun                bool
	reconcileTracker      ReconcileManager
}

// NewSidecarConfigController creates a new SidecarConfigController instance.
// NOTE: This controller only runs in the primary cluster.
func NewSidecarConfigController(
	logger *zap.SugaredLogger,
	namespaceFilter controllers_api.NamespaceFilter,
	recorder record.EventRecorder,
	applicator templating.Applicator,
	renderer templating.TemplateRenderer,
	metricsRegistry *prometheus.Registry,
	clusterName string,
	client kube.Client, // Primary cluster client
	sidecarConfigEnqueuer controllers_api.QueueAccessor,
	dryRun bool,
	reconcileTracker ReconcileManager,
) Controller {
	ctxLogger := logger.With("controller", "SidecarConfig", "cluster", clusterName)

	sidecarConfigInformer := client.MopInformerFactory().Mesh().V1alpha1().SidecarConfigs().Informer()

	errorHandler := NewWatchErrorHandlerWithMetrics(ctxLogger, clusterName, "sidecarconfigs", metricsRegistry)
	_ = sidecarConfigInformer.SetWatchErrorHandler(errorHandler)

	controller := &SidecarConfigController{
		logger:                ctxLogger,
		clusterName:           clusterName,
		client:                client,
		recorder:              recorder,
		metricsRegistry:       metricsRegistry,
		sidecarConfigClient:   client.MopApiClient(),
		sidecarConfigInformer: sidecarConfigInformer,
		sidecarConfigLister:   client.MopInformerFactory().Mesh().V1alpha1().SidecarConfigs().Lister(),
		sidecarConfigEnqueuer: sidecarConfigEnqueuer,
		dryRun:                dryRun,
		reconcileTracker:      reconcileTracker,
	}

	// Add event handlers for SidecarConfig resources
	sidecarConfigInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			// The control plane namespace typically has istio-injection disabled,
			// but we still need to process root SidecarConfig updates.
			if sc, ok := obj.(*meshv1alpha1.SidecarConfig); ok && isRootLevelSidecarConfig(sc) {
				return true
			}
			return namespaceFilter.IsNamespaceMopEnabledForObject(obj)
		},
		Handler: NewSidecarConfigEventHandler(sidecarConfigEnqueuer, clusterName, ctxLogger),
	})

	return controller
}

func (c *SidecarConfigController) Run(workerThreads int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.sidecarConfigEnqueuer.GetQueue().ShutDown()

	c.logger.Info("starting SidecarConfig controller")

	c.client.MopInformerFactory().Start(stopCh)

	if !cache.WaitForCacheSync(stopCh, c.sidecarConfigInformer.HasSynced) {
		return fmt.Errorf("timed out waiting for caches to sync")
	}

	c.logger.Info("SidecarConfig controller synced and ready")

	for i := 0; i < workerThreads; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	c.logger.Info("stopping SidecarConfig controller")

	return nil
}

func (c *SidecarConfigController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *SidecarConfigController) processNextWorkItem() bool {
	obj, shutdown := c.sidecarConfigEnqueuer.GetQueue().Get()
	if shutdown {
		return false
	}
	defer c.sidecarConfigEnqueuer.GetQueue().Done(obj)

	queueItem := obj.(QueueItem)
	err := c.reconcile(queueItem)
	if err != nil {
		if meshOpErrors.IsNonCriticalReconcileError(err) {
			// Non-critical error (e.g., status conflict). Just retry with rate limiting.
			c.logger.Infof("non-critical error reconciling SidecarConfig %s: %v, will retry", queueItem.key, err)

			// Emit non-critical error metric
			namespace, name, _ := cache.SplitMetaNamespaceKey(queueItem.key)
			metricLabels := commonmetrics.GetLabelsForK8sResource("SidecarConfig", c.clusterName, namespace, name)
			commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.SidecarConfigReconcileNonCriticalError, metricLabels, c.metricsRegistry).Inc()

			c.sidecarConfigEnqueuer.GetQueue().AddRateLimited(obj)
			return true
		}
		c.logger.Errorf("error reconciling SidecarConfig %s: %v, requeuing", queueItem.key, err)

		// Emit retry metric
		namespace, name, _ := cache.SplitMetaNamespaceKey(queueItem.key)
		metricLabels := commonmetrics.GetLabelsForK8sResource("SidecarConfig", c.clusterName, namespace, name)
		commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.SidecarConfigRetries, metricLabels, c.metricsRegistry).Inc()

		c.sidecarConfigEnqueuer.GetQueue().AddRateLimited(obj)
		return true
	}

	c.sidecarConfigEnqueuer.GetQueue().Forget(obj)
	return true
}

func (c *SidecarConfigController) reconcile(item QueueItem) error {
	startTime := time.Now()

	namespace, name, err := cache.SplitMetaNamespaceKey(item.key)
	if err != nil {
		c.logger.Errorf("invalid queue item key: %s", item.key)
		return nil
	}

	renderMode := sidecarRenderModeForNamespace(namespace)

	ctxLogger := c.logger.With("namespace", namespace, "name", name, "event", item.event)
	ctxLogger.Infof("reconciling SidecarConfig")

	if c.reconcileTracker.trackKey(item.key) {
		ctxLogger.Info("already reconciling this SidecarConfig, skipping")
		return nil
	}
	defer c.reconcileTracker.unTrackKey(ctxLogger, c.clusterName, item.key)

	if item.event == controllers_api.EventDelete {
		c.recordMetrics(nil, "deleted", time.Since(startTime))
		return c.handleDelete(namespace, name, string(item.uid))
	}

	sidecarConfig, err := c.sidecarConfigLister.SidecarConfigs(namespace).Get(name)
	if err != nil {
		ctxLogger.Errorf("failed to get SidecarConfig from lister: %v", err)
		return err
	}

	if isRootLevelSidecarConfig(sidecarConfig) {
		ctxLogger.Info("root-level SidecarConfig detected, enqueuing all workload-level SidecarConfigs")
		c.enqueueAllSidecarConfigs()
		// Root-level configs don't generate their own Sidecar, mark as succeeded
		c.recordMetrics(sidecarConfig, string(scPhaseSucceeded), time.Since(startTime))
		return c.updateSidecarConfigStatus(sidecarConfig, scPhaseSucceeded, "Root-level config applied, all workloads enqueued")
	}

	if isNamespaceLevelSidecarConfig(sidecarConfig) {
		ctxLogger.Infof("namespace-level SidecarConfig detected, enqueuing all workload-level SidecarConfigs in namespace %s", namespace)
		c.enqueueNamespaceSidecarConfigs(namespace)
		// Namespace-level configs don't generate their own Sidecar, mark as succeeded
		c.recordMetrics(sidecarConfig, string(scPhaseSucceeded), time.Since(startTime))
		return c.updateSidecarConfigStatus(sidecarConfig, scPhaseSucceeded, "Namespace-level config applied, all workloads in namespace enqueued")
	}

	return c.reconcileWorkloadSidecarConfig(sidecarConfig, namespace, startTime, renderMode)
}

// reconcileWorkloadSidecarConfig validates, merges hierarchical configs, and applies the Istio Sidecar.
// renderMode is computed in reconcile() from the namespace allowlists.
func (c *SidecarConfigController) reconcileWorkloadSidecarConfig(
	sidecarConfig *meshv1alpha1.SidecarConfig,
	namespace string,
	startTime time.Time,
	renderMode SidecarRenderMode,
) error {
	ctxLogger := c.logger.With("namespace", namespace, "name", sidecarConfig.Name)

	// Set status to Pending at the start of reconciliation (matches MeshOperator pattern)
	if err := c.updateSidecarConfigStatus(sidecarConfig, scPhasePending, "Reconciling"); err != nil {
		// If it's a conflict, continue reconciliation (will be retried)
		if !meshOpErrors.IsNonCriticalReconcileError(err) {
			ctxLogger.Errorf("failed to update status to Pending: %v", err)
			return err
		}
		ctxLogger.Debugf("conflict updating status to Pending, continuing reconciliation")
	}

	if err := c.validateSidecarConfig(sidecarConfig); err != nil {
		ctxLogger.Errorf("validation failed: %v", err)
		c.recorder.Eventf(sidecarConfig, "Warning", "ValidationFailed", "Validation error: %v", err)
		c.recordMetrics(sidecarConfig, string(scPhaseFailed), time.Since(startTime))
		return c.updateSidecarConfigStatus(sidecarConfig, scPhaseFailed, err.Error())
	}

	rootConfig, err := c.getRootSidecarConfig()
	if err != nil {
		ctxLogger.Errorf("failed to get root SidecarConfig: %v", err)
		c.recorder.Eventf(sidecarConfig, "Warning", "FetchFailed", "Failed to fetch root SidecarConfig: %v", err)
		c.recordMetrics(sidecarConfig, string(scPhaseFailed), time.Since(startTime))
		return c.updateSidecarConfigStatus(sidecarConfig, scPhaseFailed, fmt.Sprintf("failed to fetch root config: %v", err))
	}

	namespaceConfig, err := c.getNamespaceSidecarConfig(namespace)
	if err != nil {
		ctxLogger.Errorf("failed to get namespace SidecarConfig: %v", err)
		c.recorder.Eventf(sidecarConfig, "Warning", "FetchFailed", "Failed to fetch namespace SidecarConfig: %v", err)
		c.recordMetrics(sidecarConfig, string(scPhaseFailed), time.Since(startTime))
		return c.updateSidecarConfigStatus(sidecarConfig, scPhaseFailed, fmt.Sprintf("failed to fetch namespace config: %v", err))
	}

	ctxLogger.Debugf("hierarchical merge: root=%v, namespace=%v, workload=%s/%s",
		rootConfig != nil, namespaceConfig != nil, sidecarConfig.Namespace, sidecarConfig.Name)

	if renderMode == SidecarRenderModeDisabled {
		msg, err := c.deleteSidecarIfExists(sidecarConfig)
		if err != nil {
			ctxLogger.Error(err)
			c.recorder.Eventf(sidecarConfig, "Warning", "DeleteFailed", err.Error())
			c.recordMetrics(sidecarConfig, string(scPhaseFailed), time.Since(startTime))
			return c.updateSidecarConfigStatus(sidecarConfig, scPhaseFailed, err.Error())
		}
		ctxLogger.Info(msg)
		c.recordMetrics(sidecarConfig, string(scPhaseSucceeded), time.Since(startTime))
		return c.updateSidecarConfigStatus(sidecarConfig, scPhaseSucceeded, msg)
	}

	sidecar, err := c.generateSidecar(sidecarConfig, rootConfig, namespaceConfig, renderMode)
	if err != nil {
		ctxLogger.Errorf("failed to generate Sidecar: %v", err)
		c.recorder.Eventf(sidecarConfig, "Warning", "GenerationFailed", "Failed to generate Sidecar: %v", err)
		c.recordMetrics(sidecarConfig, string(scPhaseFailed), time.Since(startTime))
		return c.updateSidecarConfigStatus(sidecarConfig, scPhaseFailed, fmt.Sprintf("generation failed: %v", err))
	}

	if err := c.applySidecar(sidecarConfig, sidecar); err != nil {
		ctxLogger.Errorf("failed to apply Sidecar: %v", err)
		c.recorder.Eventf(sidecarConfig, "Warning", "ApplyFailed", "Failed to apply Sidecar: %v", err)
		c.recordMetrics(sidecarConfig, string(scPhaseFailed), time.Since(startTime))
		return c.updateSidecarConfigStatus(sidecarConfig, scPhaseFailed, fmt.Sprintf("apply failed: %v", err))
	}

	c.recorder.Event(sidecarConfig, "Normal", "Reconciled", "Successfully reconciled Sidecar")
	c.recordMetrics(sidecarConfig, string(scPhaseSucceeded), time.Since(startTime))
	ctxLogger.Infof("successfully reconciled SidecarConfig in %v", time.Since(startTime))

	msg := fmt.Sprintf("Sidecar rendered in %s mode", renderMode)
	return c.updateSidecarConfigStatus(sidecarConfig, scPhaseSucceeded, msg)
}

// validateSidecarConfig validates the SidecarConfig before reconciliation.
func (c *SidecarConfigController) validateSidecarConfig(sc *meshv1alpha1.SidecarConfig) error {
	// Workload-level SidecarConfigs must have a workloadSelector
	// (root and namespace-level configs are handled separately in reconcile())
	if !isWorkloadLevelSidecarConfig(sc) {
		return fmt.Errorf("workload-level SidecarConfig must have workloadSelector with at least one label")
	}

	if err := c.checkForConflictingSidecars(sc); err != nil {
		return err
	}

	return nil
}

// checkForConflictingSidecars placeholder for future overlap handling.
// TODO: Implement superset merging when multiple SidecarConfigs have overlapping workloadSelectors.
// Approach: Compute superset of overlapping labels, create single merged Sidecar with union of egress hosts,
// add multiple OwnerReferences, and delete old individual Sidecars.
func (c *SidecarConfigController) checkForConflictingSidecars(sc *meshv1alpha1.SidecarConfig) error {
	return nil
}

// getRootSidecarConfig fetches the root-level SidecarConfig, if it exists.
// Returns nil if not found (which is not an error - root config is optional).
func (c *SidecarConfigController) getRootSidecarConfig() (*meshv1alpha1.SidecarConfig, error) {
	sc, err := c.sidecarConfigLister.SidecarConfigs(constants.ControlPlaneNamespace).Get(RootSidecarConfigName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil // Not found is OK, root config is optional
		}
		return nil, fmt.Errorf("failed to get root SidecarConfig: %w", err)
	}
	return sc, nil
}

// getNamespaceSidecarConfig fetches the namespace-level SidecarConfig for a given namespace, if it exists.
// Returns nil if not found (which is not an error - namespace config is optional).
func (c *SidecarConfigController) getNamespaceSidecarConfig(namespace string) (*meshv1alpha1.SidecarConfig, error) {
	sc, err := c.sidecarConfigLister.SidecarConfigs(namespace).Get(namespaceSidecarConfigName(namespace))
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil // Not found is OK, namespace config is optional
		}
		return nil, fmt.Errorf("failed to get namespace SidecarConfig: %w", err)
	}
	return sc, nil
}

// applySidecar applies the generated Sidecar configuration to the cluster.
func (c *SidecarConfigController) applySidecar(sc *meshv1alpha1.SidecarConfig, config *templating.AppliedConfigObject) error {
	if config == nil {
		return fmt.Errorf("config is nil, cannot apply")
	}

	if c.dryRun {
		c.logger.Infof("dry-run mode: would apply Sidecar for %s/%s", sc.Namespace, sc.Name)
		return nil
	}

	if config.Error != nil {
		return fmt.Errorf("config contains error: %w", config.Error)
	}

	if config.Object == nil {
		return fmt.Errorf("config.Object is nil")
	}

	ctxLogger := c.logger.With("namespace", sc.Namespace, "name", sc.Name)
	ctxLogger.Infof("applying Sidecar: %s/%s", config.Object.GetNamespace(), config.Object.GetName())

	gvr, err := kube.ConvertGvkToGvr(
		c.client.GetClusterName(),
		c.client.Discovery(),
		config.Object.GroupVersionKind(),
	)
	if err != nil {
		return fmt.Errorf("failed to convert GVK to GVR: %w", err)
	}

	// Apply the Sidecar using dynamic client (handles Create/Update with retry logic)
	fieldManager := &kube.ArgoFieldManager{}
	err = kube.CreateOrUpdateObject(
		c.client.Dynamic(),
		config.Object,
		gvr,
		fieldManager,
		ctxLogger,
	)
	if err != nil {
		return fmt.Errorf("failed to create/update Sidecar: %w", err)
	}

	ctxLogger.Infof("successfully applied Sidecar: %s/%s", config.Object.GetNamespace(), config.Object.GetName())
	return nil
}

// deleteSidecarIfExists deletes the Istio Sidecar that would correspond to this SidecarConfig, if it exists.
// Used when render mode is Disabled so any previously created Sidecar (e.g. from when the namespace was in shadow/enabled) is removed.
func (c *SidecarConfigController) deleteSidecarIfExists(sc *meshv1alpha1.SidecarConfig) (message string, err error) {
	sidecarName := deriveSidecarName(sc.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deleteErr := c.client.Dynamic().Resource(constants.SidecarResource).Namespace(sc.Namespace).Delete(ctx, sidecarName, metav1.DeleteOptions{})
	if deleteErr != nil {
		if !k8serrors.IsNotFound(deleteErr) {
			return fmt.Sprintf("Disabled mode: failed to delete Sidecar %s/%s: %v", sc.Namespace, sidecarName, deleteErr), deleteErr
		}
		return fmt.Sprintf("Disabled mode: Sidecar %s/%s not rendered", sc.Namespace, sidecarName), nil
	}
	return fmt.Sprintf("Disabled mode: Sidecar %s/%s deleted", sc.Namespace, sidecarName), nil
}

// handleDelete handles the deletion of a SidecarConfig.
// The corresponding Istio Sidecar is automatically deleted via OwnerReferences.
func (c *SidecarConfigController) handleDelete(namespace, name string, uid string) error {
	c.logger.Infof("SidecarConfig deleted: %s/%s - Sidecar will be auto-deleted by OwnerReference", namespace, name)
	return nil
}

// updateSidecarConfigStatus updates the status of a SidecarConfig with optimistic locking.
func (c *SidecarConfigController) updateSidecarConfigStatus(sc *meshv1alpha1.SidecarConfig, phase meshv1alpha1.Phase, message string) error {
	scCopy := sc.DeepCopy()
	scCopy.Status.Phase = phase
	scCopy.Status.Message = message
	scCopy.Status.ObservedGeneration = sc.Generation

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.sidecarConfigClient.MeshV1alpha1().SidecarConfigs(sc.Namespace).UpdateStatus(ctx, scCopy, metav1.UpdateOptions{})
	if err != nil {
		if k8serrors.IsConflict(err) {
			// Optimistic locking conflict: The SidecarConfig was modified (by ALS or Static Hosts Plugin)
			// DURING our reconciliation, causing a ResourceVersion mismatch when we try to update status.
			// This is normal and expected when external services update the spec while we're processing.
			// We return NonCriticalReconcileError to trigger a retry with fresh data.
			c.logger.Infof("conflict while updating status for %s/%s, will retry", sc.Namespace, sc.Name)
			return &meshOpErrors.NonCriticalReconcileError{Message: err.Error()}
		}
		c.logger.Errorf("failed to update SidecarConfig %s/%s status: %v", sc.Namespace, sc.Name, err)
		return err
	}

	return nil
}

// recordMetrics records Prometheus metrics for SidecarConfig reconciliation.
func (c *SidecarConfigController) recordMetrics(sc *meshv1alpha1.SidecarConfig, phase string, duration time.Duration) {
	c.recordReconciliationCounters(sc, phase)
	c.recordReconciliationLatency(phase, duration)
	c.recordEgressHostMetrics(sc, phase)
}

// recordReconciliationCounters records reconciliation success/failure counters.
func (c *SidecarConfigController) recordReconciliationCounters(sc *meshv1alpha1.SidecarConfig, phase string) {
	namespace, name := c.getResourceIdentifiers(sc)
	metricLabels := commonmetrics.GetLabelsForK8sResource("SidecarConfig", c.clusterName, namespace, name)

	phaseLabels := make(map[string]string)
	for k, v := range metricLabels {
		phaseLabels[k] = v
	}
	phaseLabels[commonmetrics.ReconcilePhaseLabel] = phase

	if phase == string(scPhaseSucceeded) {
		commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.SidecarConfigReconciledTotal, phaseLabels, c.metricsRegistry).Inc()
	} else if phase == string(scPhaseFailed) || phase == "deleted" {
		if phase == string(scPhaseFailed) {
			commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.SidecarConfigReconcileFailed, metricLabels, c.metricsRegistry).Inc()
		}
		commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.SidecarConfigReconciledTotal, phaseLabels, c.metricsRegistry).Inc()
	}
}

// recordReconciliationLatency records the time taken to reconcile a SidecarConfig.
func (c *SidecarConfigController) recordReconciliationLatency(phase string, duration time.Duration) {
	latencyLabels := map[string]string{
		commonmetrics.ClusterLabel:        c.clusterName,
		commonmetrics.ResourceKind:        "SidecarConfig",
		commonmetrics.ReconcilePhaseLabel: phase,
	}

	commonmetrics.GetOrRegisterSummaryWithLabels(
		commonmetrics.ReconcileLatencyMetric,
		c.metricsRegistry,
		map[float64]float64{0.25: 0.1, 0.5: 0.1, 0.95: 0.1, 0.99: 0.1, 1.0: 0.1},
		latencyLabels,
	).Observe(duration.Seconds())
}

// recordEgressHostMetrics records egress host counts and ObjectsConfigured metric.
func (c *SidecarConfigController) recordEgressHostMetrics(sc *meshv1alpha1.SidecarConfig, phase string) {
	if sc == nil || sc.Spec.EgressHosts == nil {
		return
	}

	runtimeCount := 0
	configCount := 0
	if sc.Spec.EgressHosts.Discovered != nil {
		runtimeCount = len(sc.Spec.EgressHosts.Discovered.Runtime)
		configCount = len(sc.Spec.EgressHosts.Discovered.Config)
	}

	if phase == string(scPhaseSucceeded) {
		sidecarName := deriveSidecarName(sc.Name)
		sidecarLabels := commonmetrics.GetLabelsForReconciledResource(
			"SidecarConfig", c.clusterName, sc.Namespace, sc.Name, sidecarName, "Sidecar", "sidecar-config")
		commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.ObjectsConfiguredTotal, sidecarLabels, c.metricsRegistry).Set(1)
	}

	c.logger.Debugf("metrics: phase=%s, runtime=%d, config=%d, total=%d",
		phase, runtimeCount, configCount, runtimeCount+configCount)
}

// getResourceIdentifiers extracts namespace and name from a SidecarConfig, with fallback to "unknown".
func (c *SidecarConfigController) getResourceIdentifiers(sc *meshv1alpha1.SidecarConfig) (namespace, name string) {
	if sc != nil {
		return sc.Namespace, sc.Name
	}
	return "unknown", "unknown"
}

// enqueueAllSidecarConfigs enqueues all workload-level SidecarConfigs across all namespaces.
// Used when root-level SidecarConfig changes.
func (c *SidecarConfigController) enqueueAllSidecarConfigs() {
	c.logger.Info("root-level SidecarConfig changed, enqueuing all SidecarConfigs for reconciliation")

	allConfigs, err := c.sidecarConfigLister.List(labels.Everything())
	if err != nil {
		c.logger.Errorf("failed to list all SidecarConfigs: %v", err)
		return
	}

	count := 0
	for _, sc := range allConfigs {
		// Skip root and namespace-level configs themselves (they don't generate Sidecars)
		if isRootLevelSidecarConfig(sc) || isNamespaceLevelSidecarConfig(sc) {
			continue
		}
		c.sidecarConfigEnqueuer.Enqueue(c.clusterName, sc, controllers_api.EventUpdate)
		count++
	}

	c.logger.Infof("enqueued %d workload-level SidecarConfigs for reconciliation", count)
}

// enqueueNamespaceSidecarConfigs enqueues all workload-level SidecarConfigs in a specific namespace.
// Used when namespace-level SidecarConfig changes.
func (c *SidecarConfigController) enqueueNamespaceSidecarConfigs(namespace string) {
	c.logger.Infof("namespace-level SidecarConfig changed in %s, enqueuing all SidecarConfigs in that namespace", namespace)

	allConfigs, err := c.sidecarConfigLister.SidecarConfigs(namespace).List(labels.Everything())
	if err != nil {
		c.logger.Errorf("failed to list SidecarConfigs in namespace %s: %v", namespace, err)
		return
	}

	count := 0
	for _, sc := range allConfigs {
		// Skip namespace-level config itself (it doesn't generate a Sidecar)
		if isNamespaceLevelSidecarConfig(sc) {
			continue
		}
		c.sidecarConfigEnqueuer.Enqueue(c.clusterName, sc, controllers_api.EventUpdate)
		count++
	}

	c.logger.Infof("enqueued %d workload-level SidecarConfigs in namespace %s for reconciliation", count, namespace)
}

// isRootLevelSidecarConfig returns true if this is a root-level SidecarConfig.
// Root-level SidecarConfig:
//   - namespace = "mesh-control-plane"
//   - name = "default"
//   - workloadSelector is nil or empty
func isRootLevelSidecarConfig(sc *meshv1alpha1.SidecarConfig) bool {
	return sc.Namespace == constants.ControlPlaneNamespace &&
		sc.Name == RootSidecarConfigName &&
		(sc.Spec.WorkloadSelector == nil || len(sc.Spec.WorkloadSelector.Labels) == 0)
}

// isNamespaceLevelSidecarConfig returns true if this is a namespace-level SidecarConfig.
// Namespace-level SidecarConfig:
//   - namespace = any namespace (except root namespace)
//   - name = "default"
//   - workloadSelector is nil or empty
func isNamespaceLevelSidecarConfig(sc *meshv1alpha1.SidecarConfig) bool {
	// Not in root namespace
	if sc.Namespace == constants.ControlPlaneNamespace {
		return false
	}
	// Must have the namespace-level name
	if sc.Name != namespaceSidecarConfigName(sc.Namespace) {
		return false
	}
	// No workload selector
	return sc.Spec.WorkloadSelector == nil || len(sc.Spec.WorkloadSelector.Labels) == 0
}

// isWorkloadLevelSidecarConfig returns true if this is a workload-specific SidecarConfig.
// Workload-level SidecarConfig:
//   - has a non-empty workloadSelector
func isWorkloadLevelSidecarConfig(sc *meshv1alpha1.SidecarConfig) bool {
	return sc.Spec.WorkloadSelector != nil && len(sc.Spec.WorkloadSelector.Labels) > 0
}
