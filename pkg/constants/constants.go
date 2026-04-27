package constants

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Attribute string

const (
	RoutingConfigEnabledAnnotation   Attribute = "routing.mesh.io/enabled"
	TemplateOverrideAnnotation       Attribute = "routing.mesh.io/template"
	TemplateConfigAnnotationPrefix   Attribute = "template.mesh.io/"
	DynamicRoutingServiceLabel       Attribute = "routing.mesh.io/dynamic-routing-service"
	DynamicRoutingLabels             Attribute = "routing.mesh.io/dynamic-routing-labels"
	DynamicRoutingDefaultCoordinates Attribute = "routing.mesh.io/dynamic-routing-default-coordinates"
	ContextDynamicRoutingSubsetName            = "dynamicRoutingSubsetName"
	IsOcmManagedRollout                        = "isOcmManagedRollout"
)

const (
	ControlPlaneNamespace  = "mesh-control-plane"
	GatewayNamespace       = "istio-ingress" // gateway namespace; sidecar config rendering is skipped here
	DefaultConfigNamespace = "istio-system"

	MeshOperatorName = "mesh-operator"

	// K8s events reason
	MeshConfigError            = "MeshConfigError"
	UnableToGenerateMeshConfig = "UnableToGenerateMeshConfig"

	IstioSideCarInjectLabel = "sidecar.istio.io/inject"

	// BgActiveServiceAnnotation Rollout Active Service Label
	BgActiveServiceAnnotation = "mesh.io/activeService"

	ExtensionSourceAnnotation          = "mesh.io/filterSource"
	ConfigNamespaceExtensionAnnotation = "mesh.io/configNamespaceExtension"

	// ResourceParent tracks the owners/parents of a config resource (e.g. owner service for an envoyfilter)
	ResourceParent = "mesh.io/resourceParent"

	// Supported Index for Informers
	ServiceIndexName = "byServiceName"

	ExtensionIndexName = "byExtensionName"

	TemplateKeyDelimiter  = "_"
	TemplateNameSeparator = "/"

	ServiceEntryExtensionTemplatePrefix = "se"

	MultiClusterSecretLabel = "istio/multiCluster"

	// WASM image pull secret configuration
	WasmImagePullSecretName      = "wasm-image-pull-secret"
	WasmImagePullSecretNamespace = "mesh-control-plane"
	WasmSecretDataKey            = ".dockerconfigjson"

	//DefaultLeaderElect is the default true leader election should be enabled
	DefaultLeaderElect = true

	// DefaultLeaderElectionNamespace is the default namespace used to perform leader election. Only used if leader election is enabled
	DefaultLeaderElectionNamespace = "mesh-control-plane"

	// DefaultLeaderElectionLeaseDuration is the default time in seconds that non-leader candidates will wait to force acquire leadership
	DefaultLeaderElectionLeaseDuration = 15 * time.Second

	// DefaultLeaderElectionRenewDeadline is the default time in seconds that the acting master will retry refreshing leadership before giving up
	DefaultLeaderElectionRenewDeadline = 10 * time.Second

	// DefaultLeaderElectionRetryPeriod is the default time in seconds that the leader election clients should wait between tries of actions
	DefaultLeaderElectionRetryPeriod = 2 * time.Second

	DefaultLeaderElectionLeaseLockName = "mop-controller-lock"

	// Cache sync timeout configured per informer configured to 10min
	DefaultInformerCacheSyncTimeoutSeconds = 600
	DefaultClusterSyncRetryTimeoutSeconds  = 900

	// Kubernetes API client throttling defaults (matching k8s defaults)
	DefaultKubeAPIQPS   float32 = 5.0
	DefaultKubeAPIBurst int     = 10

	MaxAllowedFilters    = 1000
	MaxMopNameLenAllowed = 238 //  238 = 253 - 10 - 2 - 3; where 253 - metadata.name max length, 10 - max length of svcHash, 2 - hyphens(`-`) used in filter name, 3 - max length of filterList (`0` - `999`)

	ContextStatefulSets                = "statefulSets"
	ContextStsReplicaName              = "statefulSetReplicaName"
	ContextStsReplicas                 = "statefulSetReplicas"
	ContextStsOrdinalsStart            = "statefulSetOrdinalsStart"
	ContextDynamicRoutingCombinations  = "dynamicRoutingCombinations"
	ContextDynamicRoutingDefaultSubset = "dynamicRoutingDefaultSubset"

	ConfigNamespaceEnabled        = "ConfigNamespaceEnabled"
	IngressConfigNamespaceEnabled = "IngressConfigNamespaceEnabled"

	LBServiceType = "LoadBalancer"

	// Prefix for multicluster templates. Dash is used to keep it separate from cases like `default/multicluster/default
	MulticlusterTemplatePrefix = "multicluster-"

	DefaultTemplateHash = "defaultTemplateHash"

	NotAvailable = "NA"

	// DefaultFmtSEHost is the default FMT SE host
	DefaultFMTSEHost = "http://svc-metadata.example.com:443"
	DefaultFMTSEPath = "/graphql"
)

const (
	ArgoRolloutsNetworkingGroup   = "argoproj.io"
	ArgoRolloutsNetworkingVersion = "v1alpha1"

	MeshIoManagedByLabel = "mesh.io/managed-by"
	MeshOperatorEnabled  = "operator.mesh.io/enabled"

	// SidecarShadowLabel is added to the workloadSelector of generated Sidecars in Shadow mode so the Sidecar does not match any workload (no injection).
	SidecarShadowLabel = "sidecar.mesh.io/shadow"

	IstioNetworkingGroup   = "networking.istio.io"
	IstioNetworkingVersion = "v1alpha3" // Both v1alpha3 and v1beta1 are served right now. Update to v1beta1 if that changes in future.
	IstioInjectionLabel    = "istio-injection"

	IstioSecurityGroup             = "security.istio.io"
	IstioSecurityNetworkingVersion = "v1beta1"

	// Policy names applied to services. These names are used to register additional object in the corresponding manager.
	// Also, these names are used to expose policies in the rendering rendering context.
	ClusterTrafficPolicyName  = "clusterTrafficPolicy"
	TrafficShardingPolicyName = "trafficShardingPolicy"

	CTPSuccessState = "Success"
	CTPFailureState = "Failure"

	SharedNamespace = "shared"
	SharedMigPrefix = "mig-"

	KnativeGroup   = "networking.internal.knative.dev"
	KnativeVersion = "v1alpha1"
)

var (
	NamespaceKind     = metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}
	NamespaceResource = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}

	ServiceKind     = metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"}
	ServiceResource = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}

	EnvoyFilterResource = schema.GroupVersionResource{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Resource: "envoyfilters"}
	EnvoyFilterKind     = metav1.GroupVersionKind{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Kind: "EnvoyFilter"}

	ServiceEntryKind     = metav1.GroupVersionKind{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Kind: "ServiceEntry"}
	ServiceEntryResource = schema.GroupVersionResource{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Resource: "serviceentries"}

	RolloutKind     = metav1.GroupVersionKind{Group: ArgoRolloutsNetworkingGroup, Version: ArgoRolloutsNetworkingVersion, Kind: "Rollout"}
	RolloutResource = schema.GroupVersionResource{Group: ArgoRolloutsNetworkingGroup, Version: ArgoRolloutsNetworkingVersion, Resource: "rollouts"}

	DestinationRuleKind     = metav1.GroupVersionKind{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Kind: "DestinationRule"}
	DestinationRuleResource = schema.GroupVersionResource{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Resource: "destinationrules"}

	VirtualServiceKind     = metav1.GroupVersionKind{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Kind: "VirtualService"}
	VirtualServiceResource = schema.GroupVersionResource{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Resource: "virtualservices"}

	StatefulSetKind     = metav1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}
	StatefulSetResource = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}

	SidecarResource = schema.GroupVersionResource{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Resource: "sidecars"}

	DeploymentKind     = metav1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	DeploymentResource = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	RequestAuthenticationKind     = schema.GroupVersionKind{Group: IstioSecurityGroup, Version: IstioSecurityNetworkingVersion, Kind: "RequestAuthentication"}
	RequestAuthenticationResource = schema.GroupVersionResource{Group: IstioSecurityGroup, Version: IstioSecurityNetworkingVersion, Resource: "requestauthentications"}

	KnativeIngressKind     = schema.GroupVersionKind{Group: KnativeGroup, Version: KnativeVersion, Kind: "Ingress"}
	KnativeIngressResource = schema.GroupVersionResource{Group: KnativeGroup, Version: KnativeVersion, Resource: "ingresses"}

	KnativeServerlessServiceKind     = schema.GroupVersionKind{Group: KnativeGroup, Version: KnativeVersion, Kind: "ServerlessService"}
	KnativeServerlessServiceResource = schema.GroupVersionResource{Group: KnativeGroup, Version: KnativeVersion, Resource: "serverlessservices"}

	TrafficShardingPolicyKind     = schema.GroupVersionKind{Group: "mesh.io", Version: "v1alpha1", Kind: "TrafficShardingPolicy"}
	TrafficShardingPolicyResource = schema.GroupVersionResource{Group: "mesh.io", Version: "v1alpha1", Resource: "trafficshardingpolicies"}

	SidecarConfigKind     = schema.GroupVersionKind{Group: "mesh.io", Version: "v1alpha1", Kind: "SidecarConfig"}
	SidecarConfigResource = schema.GroupVersionResource{Group: "mesh.io", Version: "v1alpha1", Resource: "sidecarconfigs"}
)

var FakeTime = metav1.NewTime(time.Date(2021, time.June, 30, 0, 0, 0, 0, time.UTC))
