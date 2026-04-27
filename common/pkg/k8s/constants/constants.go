//nolint:golint
package constants

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ArgoRolloutsNetworkingGroup   = "argoproj.io"
	ArgoRolloutsNetworkingVersion = "v1alpha1"

	MeshOperatorGroup   = "mesh.io"
	MeshOperatorVersion = "v1alpha1"

	MeshOperatorEnabledLabel = "operator.mesh.io/enabled"
	ManagedByLabel           = "mesh.io.example.com/managed-by"
	MeshIoManagedByLabel     = "mesh.io/managed-by"
	MeshOperatorName         = "mesh-operator"
	TypeLabel                = "mesh.io.example.com/type"
	VersionLabel             = "mesh.io.example.com/version"
	IstioInjectionLabel      = "istio-injection"

	IstioNetworkingGroup   = "networking.istio.io"
	IstioNetworkingVersion = "v1alpha3" // Both v1alpha3 and v1beta1 are served right now. Update to v1beta1 if that changes in future.

	IstioSecurityGroup             = "security.istio.io"
	IstioSecurityNetworkingVersion = "v1beta1"
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

	MeshOperatorKind     = metav1.GroupVersionKind{Group: MeshOperatorGroup, Version: MeshOperatorVersion, Kind: "MeshOperator"}
	MeshOperatorResource = schema.GroupVersionResource{Group: MeshOperatorGroup, Version: MeshOperatorVersion, Resource: "meshoperators"}

	ClusterTrafficPolicyKind     = metav1.GroupVersionKind{Group: MeshOperatorGroup, Version: MeshOperatorVersion, Kind: "ClusterTrafficPolicy"}
	ClusterTrafficPolicyResource = schema.GroupVersionResource{Group: MeshOperatorGroup, Version: MeshOperatorVersion, Resource: "clustertrafficpolicies"}

	TrafficShardingPolicyKind     = metav1.GroupVersionKind{Group: MeshOperatorGroup, Version: MeshOperatorVersion, Kind: "TrafficShardingPolicy"}
	TrafficShardingPolicyResource = schema.GroupVersionResource{Group: MeshOperatorGroup, Version: MeshOperatorVersion, Resource: "trafficshardingpolicies"}

	RolloutKind     = metav1.GroupVersionKind{Group: ArgoRolloutsNetworkingGroup, Version: ArgoRolloutsNetworkingVersion, Kind: "Rollout"}
	RolloutResource = schema.GroupVersionResource{Group: ArgoRolloutsNetworkingGroup, Version: ArgoRolloutsNetworkingVersion, Resource: "rollouts"}

	DestinationRuleKind     = metav1.GroupVersionKind{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Kind: "DestinationRule"}
	DestinationRuleResource = schema.GroupVersionResource{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Resource: "destinationrules"}

	VirtualServiceKind     = metav1.GroupVersionKind{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Kind: "VirtualService"}
	VirtualServiceResource = schema.GroupVersionResource{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Resource: "virtualservices"}

	WorkloadEntryKind     = metav1.GroupVersionKind{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Kind: "WorkloadEntry"}
	WorkloadEntryResource = schema.GroupVersionResource{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Resource: "workloadentries"}

	SidecarKind     = metav1.GroupVersionKind{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Kind: "Sidecar"}
	SidecarResource = schema.GroupVersionResource{Group: IstioNetworkingGroup, Version: IstioNetworkingVersion, Resource: "sidecars"}

	DeploymentKind     = metav1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	DeploymentResource = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	StatefulSetKind     = metav1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}
	StatefulSetResource = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}

	RequestAuthenticationKind     = schema.GroupVersionKind{Group: IstioSecurityGroup, Version: IstioSecurityNetworkingVersion, Kind: "RequestAuthentication"}
	RequestAuthenticationResource = schema.GroupVersionResource{Group: IstioSecurityGroup, Version: IstioSecurityNetworkingVersion, Resource: "requestauthentications"}

	PodKind     = metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
	PodResource = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "Pods"}
)
