/*
  Copyright, 2021, salesforce.com, inc.
  All Rights Reserved.
  Company Confidential.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MeshServiceMetadataSpec defines the desired state of MeshServiceMetadata
type MeshServiceMetadataSpec struct {
	OwnedResources []OwnedResource `json:"ownedResources"`
	Owners         []Owner         `json:"owners"`
}

// +kubebuilder:validation:Required

type OwnedResource struct {
	// +kubebuilder:validation:Required
	ApiVersion string `json:"apiVersion"`
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
	// +kubebuilder:validation:Required
	UID types.UID `json:"uid"`

	Stale bool `json:"stale,omitempty"`
}

// +kubebuilder:validation:Required

type Owner struct {
	// +kubebuilder:validation:Required
	Cluster string `json:"cluster"`
	// +kubebuilder:validation:Required
	ApiVersion string `json:"apiVersion"`
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	UID types.UID `json:"uid"`
}

// MeshServiceMetadataStatus defines the observed state of MeshServiceMetadata
type MeshServiceMetadataStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Optional
	ReconciledObjectHash map[string]string `json:"reconciledObjectHash,omitempty"`
	// +kubebuilder:validation:Optional
	ReconciledTemplateHash string `json:"reconciledTemplateHash,omitempty"`
	// +kubebuilder:validation:Optional
	LastReconciledTime *metav1.Time `json:"lastReconciledTime,omitempty"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=msm,path=meshservicemetadatas
//+kubebuilder:storageversion

// MeshServiceMetadata is the Schema for the meshservicemetadata API
type MeshServiceMetadata struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MeshServiceMetadataSpec   `json:"spec,omitempty"`
	Status MeshServiceMetadataStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// MeshServiceMetadataList contains a list of MeshServiceMetadata
type MeshServiceMetadataList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MeshServiceMetadata `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MeshServiceMetadata{}, &MeshServiceMetadataList{})
}
