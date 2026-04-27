package metrics

import (
	"github.com/istio-ecosystem/mesh-operator/common/pkg/metrics"
	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ServiceNameTag = "service_name"
	NamespaceTag   = "namespace"
	ReasonTag      = "reason"

	ClusterLabel            = "k8s_cluster"
	NamespaceLabel          = "k8s_namespace"
	ResourceKind            = "kind"
	ResourceNameLabel       = "k8s_resource_name"
	TargetResourceNameLabel = "k8s_target_resource_name"
	TargetKindLabel         = "k8s_target_kind"
	ResourceIdentityLabel   = "resource_identity"
	TemplateTypeLabel       = "template_type"
	EventTypeLabel          = "event"
	MopType                 = "mop_type"

	// OverlayMopType - a value of the template-type/mop-type tag for overlay MOPs
	OverlayMopType = "overlay"
	//ExtensionMopType - a value of the mop-type tag for extension MOPs
	ExtensionMopType = "extension"
	// TargetAll - a value of the target-resource-name tag for namespace-level MOPs
	TargetAll = "all"

	ReconcilePhaseLabel = "phase"

	// informer related labels
	InformerName                 = "informer_name"
	InformerCacheSyncFailureType = "informer_cache_sync_failure_type"

	DeprecatedLabel = "deprecated"
)

func GetLabelsForK8sResource(kind, cluster, namespace, name string) map[string]string {
	return map[string]string{
		ClusterLabel:      cluster,
		NamespaceLabel:    namespace,
		ResourceKind:      kind,
		ResourceNameLabel: name,
	}
}

func GetLabelsForMopResourceWithTarget(kind, cluster, namespace, name, targetName, targetKind, mopType string) map[string]string {
	labels := GetLabelsForK8sResource(kind, cluster, namespace, name)
	labels[TargetResourceNameLabel] = targetName
	labels[TargetKindLabel] = targetKind
	labels[MopType] = mopType
	return labels
}

func GetLabelsForReconciledResource(kind string, cluster string, namespace, name, targetName, targetKind, templateType string) map[string]string {
	labels := GetLabelsForK8sResource(kind, cluster, namespace, name)
	labels[TemplateTypeLabel] = templateType
	labels[TargetResourceNameLabel] = targetName
	labels[TargetKindLabel] = targetKind
	return labels
}

func GetLabelsForConfiguredResource(kind, cluster, namespace, name, identity, templateType string) map[string]string {
	labels := GetLabelsForK8sResource(kind, cluster, namespace, name)
	labels[TemplateTypeLabel] = templateType
	labels[ResourceIdentityLabel] = identity
	return labels
}

// EraseMetricsForK8sResource unregister metrics by given name and cluster/ns/name labels
func EraseMetricsForK8sResource(registry *prometheus.Registry, metrics []string, matchLabels map[string]string) {
	var gaugesToErase []prometheus.Gauge
	stuff, _ := registry.Gather()
	for _, metric := range stuff {
		if common.SliceContains(metrics, metric.GetName()) {
			for _, metricWithLabels := range metric.GetMetric() {
				matchesFound := 0
				for _, label := range metricWithLabels.GetLabel() {
					matchValue, found := matchLabels[label.GetName()]
					if found {
						if matchValue == label.GetValue() {
							matchesFound += 1
						} else {
							break
						}
					}
				}

				if len(matchLabels) == matchesFound {
					// Convert all labels to map
					var metricLabels = make(map[string]string)
					for _, label := range metricWithLabels.GetLabel() {
						metricLabels[label.GetName()] = label.GetValue()
					}

					gauge := prometheus.NewGauge(
						prometheus.GaugeOpts{
							Name:        metric.GetName(),
							Help:        metric.GetName(),
							ConstLabels: metricLabels,
						},
					)
					gaugesToErase = append(gaugesToErase, gauge)
				}
			}
		}
	}

	for _, gaugeToErase := range gaugesToErase {
		registry.Unregister(gaugeToErase)
	}
}

const (
	KnativeIngressReconciledTotal                     = "knative_ingress_reconciled_total"
	KnativeIngressRetries                             = "knative_ingress_retries"
	KnativeIngressReconcileNonCriticalError           = "knative_ingress_reconcile_non_critical_error"
	KnativeIngressReconcileFailed                     = "knative_ingress_reconcile_failed"
	KnativeServerlessServiceReconciledTotal           = "knative_serverless_service_reconciled_total"
	KnativeServerlessServiceRetries                   = "knative_serverless_service_retries"
	KnativeServerlessServiceReconcileNonCriticalError = "knative_serverless_service_reconcile_non_critical_error"
	KnativeServerlessServiceReconcileFailed           = "knative_serverless_service_reconcile_failed"
	ServicesReconciledTotal                           = "services_reconciled_total"
	ServicesReconcileFailed                           = "services_reconcile_failed"
	ServiceReconcileUserError                         = "service_reconcile_user_error"
	ServiceReconcileNonCriticalError                  = "service_reconcile_non_critical_error"
	ServiceRetries                                    = "service_reconcile_retries"

	ServiceEntryReconciledTotal           = "service_entry_reconciled_total"
	ServiceEntryReconcileFailed           = "service_entry_reconcile_failed"
	ServiceEntryReconcileNonCriticalError = "service_entry_reconcile_non_critical_error"
	ServiceEntryRetries                   = "service_entry_retries"

	TSPReconciledTotal           = "traffic_sharding_policy_reconciled_total"
	TSPReconcileFailed           = "traffic_sharding_policy_reconcile_failed"
	TSPReconcileUserError        = "traffic_sharding_policy_reconcile_user_error"
	TSPRetries                   = "traffic_sharding_policy_retries"
	TSPConflictTotal             = "traffic_sharding_policy_conflict_total"
	TSPReconcileNonCriticalError = "traffic_sharding_policy_reconcile_non_critical_error"
	TSPMatchedServicesCount      = "tsp_matched_services_count"
	TSPConfigGenerationFailed    = "tsp_config_generation_failed"
	TSPClusterListingFailed      = "tsp_cluster_listing_failed"
	TSPStatusUpdateFailed        = "tsp_status_update_failed"

	MeshOperatorRecordsReconciledTotal    = "mop_records_reconciled_total"
	MeshOperatorReconcileFailed           = "mop_records_reconcile_failed"
	MeshOperatorReconcileUserError        = "mop_records_reconcile_user_error"
	MeshOperatorReconcileNonCriticalError = "mop_records_reconcile_non_critical_error"
	MeshOperatorRetries                   = "mop_retries"

	MeshServiceMetadatasReconciledTotal = "msm_reconsiled_total"
	MeshServiceMetadatasReconcileFailed = "msm_reconcile_failed"
	MeshServiceMetadataOwnersUnTracked  = "msm_owners_untracked"
	MeshServiceMetadataRetries          = "msm_retries"

	SecretRetries = "secret_retries"

	SidecarConfigReconciledTotal           = "sidecar_config_reconciled_total"
	SidecarConfigReconcileFailed           = "sidecar_config_reconcile_failed"
	SidecarConfigReconcileNonCriticalError = "sidecar_config_reconcile_non_critical_error"
	SidecarConfigRetries                   = "sidecar_config_retries"

	// ProxyAccess / SidecarConfig generation metrics
	ProxyAccessReconcileTotal   = "proxy_access_reconcile_total"
	ProxyAccessReconcileFailed  = "proxy_access_reconcile_failed"
	ProxyAccessReconcileLatency = "proxy_access_reconcile_latency"
	ProxyAccessReconcileSkipped = "proxy_access_reconcile_skipped"

	// ProxyAccess metric labels
	ProxyAccessTypeLabel     = "type"
	ProxyAccessTypeRoot      = "root"
	ProxyAccessTypeClient    = "client"
	ProxyAccessTypeNamespace = "namespace"
	ProxyAccessReasonLabel   = "reason"

	// MeshOperatorFailed Number of unique mop records in cluster in failed status
	MeshOperatorFailed = "mop_failed"
	// MeshOperatorResourcesTotal number of unique mop-generated resources in cluster
	MeshOperatorResourcesTotal = "mop_resources_total"
	// MeshOperatorResourcesFailed number of unique mop-generated resources in cluster that failed to render
	MeshOperatorResourcesFailed = "mop_resources_failed"

	// MeshConfigResourcesTotal number of unique mesh config objects generated in cluster
	MeshConfigResourcesTotal = "mesh_config_resources_total"

	// Svc/SE object should not reference both `routing.mesh.io/template` and `template.mesh.io/` annotation
	AnnotationConflict = "annotation_conflict"

	// ObjectsConfiguredTotal Number of unique objects in cluster that were configured
	ObjectsConfiguredTotal = "objects_configured"

	PrimaryNamespaceCreated         = "remote_event_primary_ns_created"
	PrimaryNamespaceRetrievalFailed = "remote_event_primary_ns_get_failed"
	PrimaryNamespaceCreationFailed  = "remote_event_primary_ns_create_failed"

	StaleConfigObjects          = "stale_config_objects"
	StaleConfigDeleted          = "stale_config_deleted"
	CrossNamespaceConfigDeleted = "cross_ns_config_deleted"

	// Dynamic Routing Metrics
	DynamicRoutingFailedMetrics         = "dynamic_routing_failed"
	DynamicRoutingMultipleLabelsMetrics = "dynamic_routing_multiple_labels"
	DynamicRoutingSingleLabelMetrics    = "dynamic_routing_single_label"

	// Rollout Metrics
	RolloutAdmissionRequestMetric = "rollout_total_admission_requests"
	RolloutAdmissionSuccessMetric = "rollout_total_admission_success"
	RolloutAdmissionFailureMetric = "rollout_total_admission_failure"

	RolloutTotalMutationErrors      = "rollout_total_mutation_errors"
	RolloutTotalEventsSkipped       = "rollout_total_events_skipped"
	RolloutTotalSuccessfulMutations = "rollout_total_successful_mutations"
	RolloutReMutateErrors           = "rollout_remutate_errors"
	RolloutReMutateSuccess          = "rollout_remutate_success"

	ReconcileLatencyMetric = "reconcile_latency"
	ReconcileSkipped       = "reconcile_skipped"

	// stats to record informer cache sync error
	InformerCacheSyncFailureMetric = "informer_cache_sync_failure_total"

	WatchErrorsMetric = "watch_errors"

	// Queue Watchdog Metrics
	QueueStuckWarning = "queue_stuck_warning"
	QueueStuckPanic   = "queue_stuck_panic"
)

func IncrementMetric(metricName string, objectType string, meta metav1.Object, metricsRegistry *prometheus.Registry) {
	GetOrRegisterCounterWithLabels(metricName, GetMetricsLabels(objectType, meta), metricsRegistry).Inc()
}

func EmitObjectsConfiguredStats(metricsRegistry *prometheus.Registry, clusterName, objectNamespace, objectName, kind, templateType string) {
	metricLabels := GetLabelsForReconciledResource(kind, clusterName, objectNamespace, objectName, objectName, kind, templateType)
	metrics.GetOrRegisterGaugeWithLabels(ObjectsConfiguredTotal, metricLabels, metricsRegistry).Set(1)
}

func GetMetricsLabels(objKind string, metaObj metav1.Object) map[string]string {
	labels := make(map[string]string, 3)
	labels["objectType"] = objKind
	labels["objectName"] = metaObj.GetName()
	labels["namespace"] = metaObj.GetNamespace()
	return labels
}

// GetObjectIdentity - obtains an object identity defined by the object's identity label if such specified. Object name otherwise
func GetObjectIdentity(name string, labels map[string]string) string {
	identity := name

	if len(features.IdentityLabel) > 0 {
		identityLabelValue, identitySpecified := labels[features.IdentityLabel]
		if identitySpecified && len(identityLabelValue) > 0 {
			identity = identityLabelValue
		}
	}

	return identity
}
