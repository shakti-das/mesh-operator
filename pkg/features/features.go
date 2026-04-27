package features

import (
	"os"
	"strconv"
	"strings"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
)

var (
	AllNamespaces = "All"

	EnableFiltersConfig                  = getBooleanEnvValue("ENABLE_FILTERS_CONFIG", true)
	EnableRoutingConfig                  = getBooleanEnvValue("ENABLE_ROUTING_CONFIG", true)
	EnableServiceConfigOverlays          = getBooleanEnvValue("ENABLE_SERVICE_CONFIG_OVERLAYS", false)
	EnableMaxConnectionDurationFilter    = getBooleanEnvValue("ENABLE_MAX_CONN_DURATION", false)
	EnableArgoIntegration                = getBooleanEnvValue("ENABLE_ARGO_INTEGRATION", false)
	EnableDynamicRouting                 = getBooleanEnvValue("ENABLE_DYNAMIC_ROUTING", false)
	FDType                               = getStringEnvValue("FD_TYPE")
	EnableCleanUpObsoleteResources       = getBooleanEnvValue("ENABLE_CLEANUP_OBSOLETE_RESOURCES", false)
	EnableCrossNamespaceResourceDeletion = getBooleanEnvValue("ENABLE_CROSS_NS_RESOURCE_DELETION", false)
	ConfigNamespace                      = getStringEnvValueOrDefault("CONFIG_NAMESPACE", constants.DefaultConfigNamespace)
	IngressConfigNamespace               = getStringEnvValueOrDefault("INGRESS_CONFIG_NAMESPACE", constants.DefaultConfigNamespace)
	EnableStaleMsmCleanup                = getBooleanEnvValue("ENABLE_MSM_CLEANUP", false)
	CleanupDisabledConfig                = getBooleanEnvValue("CLEANUP_DISABLED_SE_CONFIG", false)
	EnableServiceEntryOverlays           = getBooleanEnvValue("ENABLE_SE_OVERLAYS", false)
	IdentityLabel                        = getStringEnvValue("IDENTITY_LABEL")
	MyPodName                            = getStringEnvValue("MY_POD_NAME")
	MyPodNamespace                       = getStringEnvValue("MY_POD_NAMESPACE")

	ServiceControllerThreads                   = getIntEnvValueOrDefault("SERVICE_CONTROLLER_THREADS", 2)
	ServiceEntryControllerThreads              = getIntEnvValueOrDefault("SERVICE_ENTRY_CONTROLLER_THREADS", 2)
	MsmControllerThreads                       = getIntEnvValueOrDefault("MSM_CONTROLLER_THREADS", 2)
	MeshOperatorControllerThreads              = getIntEnvValueOrDefault("MESHOP_CONTROLLER_THREADS", 2)
	ClusterControllerThreads                   = getIntEnvValueOrDefault("CLUSTER_CONTROLLER_THREADS", 2)
	EnableMopConcurrencyControl                = getBooleanEnvValue("ENABLE_MOP_CONCURRENCY_CONTROL", true)
	EnableMopPendingTracking                   = getBooleanEnvValue("ENABLE_MOP_PENDING_TRACKING", false)
	EnableMsmOwnerFix                          = getBooleanEnvValue("ENABLE_MSM_OWNER_FIX", true)
	EnableThreadDump                           = getBooleanEnvValue("ENABLE_THREAD_DUMP", false)
	EnableMulticlusterCTP                      = getBooleanEnvValue("ENABLE_MULTICLUSTER_CTP", false)
	EnableSkipReconcileLatencyMetric           = getBooleanEnvValue("ENABLE_SKIP_RECONCILE_LATENCY_METRIC", false)
	EnableDynamicRoutingForBGAndCanaryServices = getBooleanEnvValue("ENABLE_DYNAMIC_ROUTING_FOR_BG_AND_CANARY_SERVICES", false)
	RetainArgoManagedFields                    = getBooleanEnvValue("RETAIN_ARGO_MANAGED_FIELDS", false)
	AutomatedShutdownInterval                  = getStringEnvValue("AUTOMATED_SHUTDOWN_INTERVAL")
	EnableKnativeControllers                   = getBooleanEnvValue("ENABLE_KNATIVE_CONTROLLERS", false)
	EnableKnativeIngressController             = getBooleanEnvValue("ENABLE_KNATIVE_INGRESS_CONTROLLER", false)
	KnativeIngressControllerThreads            = getIntEnvValueOrDefault("KNATIVE_INGRESS_CONTROLLER_THREADS", 2)
	KnativeServerlessServiceControllerThreads  = getIntEnvValueOrDefault("KNATIVE_SERVERLESS_SERVICE_CONTROLLER_THREADS", 2)
	EnableSkipReconcileOnRestart               = getBooleanEnvValue("ENABLE_SKIP_RECONCILE_ON_RESTART", false)
	SkipSvcEnqueueForRelatedObjects            = getBooleanEnvValue("SKIP_SVC_ENQUEUE_FOR_RELATED_OBJECTS", false)
	MopConfigTemplatesHash                     = getStringEnvValueOrDefault("MOP_CONFIG_TEMPLATES_HASH", constants.DefaultTemplateHash)
	UseInformerInTrackingComponent             = getBooleanEnvValue("USE_INFORMER_IN_TRACKING_COMPONENT", false)
	EnableLeaderPodLabel                       = getBooleanEnvValue("ENABLE_LEADER_POD_LABEL", false)
	EnableServiceEntryExtension                = getBooleanEnvValue("ENABLE_SE_EXTENSION", false)
	EnableServiceEntryAdditionalObjects        = getBooleanEnvValue("ENABLE_SE_ADDITIONAL_OBJECTS", false)
	UseInformerInMsmOwnerCheck                 = getBooleanEnvValue("USE_INFORMER_IN_MSM_OWNER_CHECK", false)
	UseInformerToEnqueueSvcSe                  = getBooleanEnvValue("USE_INFORMER_TO_ENQUEUE_SVC_SE", false)
	EnableTrafficShardingPolicyController      = getBooleanEnvValue("ENABLE_TRAFFIC_SHARDING_POLICY_CONTROLLER", false)
	TspControllerThreads                       = getIntEnvValueOrDefault("TSP_CONTROLLER_THREADS", 2)
	EnableSidecarConfig                        = getBooleanEnvValue("ENABLE_SIDECAR_CONFIG", false)
	EnableSEInSidecarConfig                    = getBooleanEnvValue("ENABLE_SE_IN_SIDECAR_CONFIG", false)
	IncludeAllPortsInRootSidecarConfig         = getBooleanEnvValue("INCLUDE_ALL_PORTS_IN_ROOT_SIDECAR_CONFIG", false)
	EnableAccessLogEnvoyFilter                 = getBooleanEnvValue("ENABLE_ACCESS_LOG_ENVOY_FILTER", true)
	SidecarConfigControllerThreads             = getIntEnvValueOrDefault("SIDECAR_CONFIG_CONTROLLER_THREADS", 2)
	EnableShadowSidecarConfigNamespaces        = getStringSetEnvValue("ENABLE_SHADOW_SIDECAR_CONFIG_NAMESPACES")
	EnableSidecarConfigNamespaces              = getStringSetEnvValue("ENABLE_SIDECAR_CONFIG_NAMESPACES")
	PriorityNamespaces                         = getStringSetEnvValue("PRIORITY_NAMESPACES")
	UsePriorityQueue                           = getBooleanEnvValue("USE_PRIORITY_QUEUE", false)
	EnableClusterSyncRetry                     = getBooleanEnvValue("ENABLE_CLUSTER_SYNC_RETRY", false)
	EnableOcmIntegration                       = getBooleanEnvValue("ENABLE_OCM_INTEGRATION", false)
	EnableQueueWatchdog                        = getBooleanEnvValue("ENABLE_QUEUE_WATCHDOG", false)
	QueueWatchdogThresholdMinutes              = getIntEnvValueOrDefault("QUEUE_WATCHDOG_THRESHOLD_MINUTES", 15)
	QueueWatchdogWarningPercentage             = getIntEnvValueOrDefault("QUEUE_WATCHDOG_WARNING_PERCENTAGE", 70)
	EnableTemplateMetadata                     = getBooleanEnvValue("ENABLE_TEMPLATE_METADATA", false)
	EnableFMTMetadataCache                     = getBooleanEnvValue("ENABLE_FMT_METADATA_CACHE", false)
	PlainTextMutatingWebhook                   = getBooleanEnvValue("PLAIN_TEXT_WEBHOOK", false)
	FMTSEHost                                  = getStringEnvValueOrDefault("FMT_SE_HOST", constants.DefaultFMTSEHost)
	FMTSEPath                                  = getStringEnvValueOrDefault("FMT_SE_PATH", constants.DefaultFMTSEPath)
)

// SidecarConfigAllNamespacesWildcard: when present in ENABLE_SIDECAR_CONFIG_NAMESPACES or ENABLE_SHADOW_SIDECAR_CONFIG_NAMESPACES, all namespaces are allowed.
const SidecarConfigAllNamespacesWildcard = "*"

func getBooleanEnvValue(name string, defaultValue bool) bool {
	if val, ok := os.LookupEnv(name); ok {
		booleanVal, err := strconv.ParseBool(val)
		if err != nil {
			return defaultValue
		}
		return booleanVal
	}
	return defaultValue
}

func getStringEnvValue(name string) string {
	val, _ := os.LookupEnv(name)
	return val
}

func getStringEnvValueOrDefault(name, defaultValue string) string {
	val := getStringEnvValue(name)
	if val == "" {
		return defaultValue
	}

	return val
}

func getStringSetEnvValue(name string) map[string]struct{} {
	result := make(map[string]struct{})

	// Get the environment variable value
	envValue, isPresent := os.LookupEnv(name)
	if !isPresent || envValue == "" {
		return result
	}

	// Split by comma and create set
	parts := strings.Split(envValue, ",")
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result[trimmed] = struct{}{}
		}
	}
	return result
}

func getIntEnvValueOrDefault(name string, defaultValue int) int {
	if val, ok := os.LookupEnv(name); ok {
		intVal, convertError := strconv.Atoi(val)
		if convertError != nil {
			return defaultValue
		}
		return intVal
	}
	return defaultValue
}
