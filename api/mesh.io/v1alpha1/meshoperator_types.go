/*
  Copyright, 2021, salesforce.com, inc.
  All Rights Reserved.
  Company Confidential.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=mop
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// MeshOperator is the Schema for the meshoperators API
type MeshOperator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MeshOperatorSpec   `json:"spec,omitempty"`
	Status MeshOperatorStatus `json:"status,omitempty"`
}

// MeshOperatorSpec defines the desired state of MeshOperator
type MeshOperatorSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Extensions      []ExtensionElement `json:"extensions,omitempty"`
	ServiceSelector map[string]string  `json:"serviceSelector,omitempty"`
	TemplateType    TemplateType       `json:"templateType,omitempty"`
	Overlays        []Overlay          `json:"overlays,omitempty"`
}

// +kubebuilder:validation:Enum=Pending;Succeeded;Failed;Terminating;Unknown
// +kubebuilder:default:=Pending
type Phase string

// MeshOperatorStatus defines the observed state of MeshOperator
type MeshOperatorStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Phase, Message of MOP level configuration errors:
	// +kubebuilder:validation:Required
	Phase Phase `json:"phase"`
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
	// +kubebuilder:validation:Optional
	NamespaceHash string `json:"namespaceHash,omitempty"`
	// +kubebuilder:validation:Optional
	Services map[string]*ServiceStatus `json:"services,omitempty"`
	// +kubebuilder:validation:Optional
	ServiceEntries map[string]*ServiceStatus `json:"serviceEntries,omitempty"`
	// +kubebuilder:validation:Optional
	RelatedResources []*ResourceStatus `json:"relatedResources,omitempty"`
	// +kubebuilder:validation:Optional
	LastReconciledTime *metav1.Time `json:"lastReconciledTime,omitempty"`
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// ServiceStatus defines the status per Service including resources generated for the Service
type ServiceStatus struct {
	// +kubebuilder:validation:Required
	Phase Phase `json:"phase"`
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
	// +kubebuilder:validation:Optional
	Hash string `json:"hash,omitempty"`
	// +kubebuilder:validation:Optional
	RelatedResources []*ResourceStatus `json:"relatedResources,omitempty"`
}

// ResourceStatus defines the status and details of the generated resource
type ResourceStatus struct {
	// +kubebuilder:validation:Required
	Phase Phase `json:"phase"`
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
	// +kubebuilder:validation:Required
	ApiVersion string `json:"apiVersion"`
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace"`
	// +kubebuilder:validation:Optional
	UID string `json:"uid,omitempty"`
}

// +kubebuilder:object:root=true
// MeshOperatorList contains a list of MeshOperator
type MeshOperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MeshOperator `json:"items"`
}

type TemplateType string

type Overlay struct {
	// +kubebuilder:validation:Enum=VirtualService;DestinationRule
	// +kubebuilder:validation:optional
	Kind string `json:"kind,omitempty"`
	// +kubebuilder:validation:optional
	Name string `json:"name,omitempty"`
	// +kubebuilder:validation:required
	// +kubebuilder:pruning:PreserveUnknownFields
	StrategicMergePatch runtime.RawExtension `json:"strategicMergePatch"`
}

func init() {
	SchemeBuilder.Register(&MeshOperator{}, &MeshOperatorList{})
}
