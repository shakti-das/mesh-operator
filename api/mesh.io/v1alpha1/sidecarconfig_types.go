package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SidecarConfigSpec defines the desired state of SidecarConfig
type SidecarConfigSpec struct {
	// WorkloadSelector specifies the criteria used to select workloads that this configuration applies to.
	// If defined in the mesh control plane namespace, it applies to all workloads across all namespaces.
	// If not specified, the configuration applies to all workloads in the same namespace and gets merged
	// with any other applicable configurations for the workload or namespace.
	// +kubebuilder:validation:Optional
	WorkloadSelector *WorkloadSelector `json:"workloadSelector,omitempty"`

	// EgressHosts configures the egress hosts that sidecars can communicate with.
	// This controls which external services the sidecar proxy can route outbound traffic to.
	// +kubebuilder:validation:Optional
	EgressHosts *EgressHosts `json:"egressHosts,omitempty"`
}

// WorkloadSelector specifies the criteria used to determine if the configuration applies to a workload
type WorkloadSelector struct {
	// Labels specifies a set of key-value pairs that must match the workload labels for this configuration to apply.
	// All specified labels must match for a workload to be selected.
	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty"`
}

// EgressHosts defines egress host configuration for sidecar proxies.
type EgressHosts struct {
	// Discovered contains hosts that are automatically discovered and updated by controllers.
	// These should not be manually configured as they are managed by the system.
	// +kubebuilder:validation:Optional
	Discovered *DiscoveredEgressHosts `json:"discovered,omitempty"`

	// TODO: Add a static host list for manual configuration
}

// DiscoveredEgressHosts contains automatically discovered egress hosts from various sources
type DiscoveredEgressHosts struct {
	// Runtime contains hosts dynamically discovered based on runtime observations such as
	// traffic patterns, metrics, events, and other behavioral data.
	// +kubebuilder:validation:Optional
	Runtime []Host `json:"runtime,omitempty"`

	// Config contains hosts discovered from configuration sources such as SBOMs
	// (Software Bill of Materials) and other declarative configuration artifacts.
	// +kubebuilder:validation:Optional
	Config []Host `json:"config,omitempty"`
}

// Host defines a destination host for egress traffic
type Host struct {
	// Hostname specifies the DNS name or IP address of the destination host.
	// This is the primary identifier for the egress destination.
	// +kubebuilder:validation:Required
	Hostname string `json:"hostname"`

	// Namespace specifies the namespace of the service (if applicable).
	// This is typically used for internal service references within the cluster.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`

	// Port specifies the port number to access on the destination host.
	// If not specified, the default port for the protocol is used.
	// +kubebuilder:validation:Optional
	Port int32 `json:"port,omitempty"`
}

// SidecarConfigStatus defines the observed state of SidecarConfig
type SidecarConfigStatus struct {
	// Phase represents the current state of the SidecarConfig reconciliation
	// +kubebuilder:validation:Optional
	Phase Phase `json:"phase,omitempty"`

	// Message provides additional information about the current phase
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// LastReconciledTime is the timestamp of the last successful reconciliation
	// +kubebuilder:validation:Optional
	LastReconciledTime *metav1.Time `json:"lastReconciledTime,omitempty"`

	// ObservedGeneration reflects the generation of the most recently observed SidecarConfig
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=scc

// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=`.status.message`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// SidecarConfig is the Schema for the sidecarconfigs API
type SidecarConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SidecarConfigSpec   `json:"spec,omitempty"`
	Status SidecarConfigStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SidecarConfigList contains a list of SidecarConfig
type SidecarConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SidecarConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SidecarConfig{}, &SidecarConfigList{})
}
