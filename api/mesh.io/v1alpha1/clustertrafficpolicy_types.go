/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterTrafficPolicySpec defines the desired state of ClusterTrafficPolicy
type ClusterTrafficPolicySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	ClusterTrafficStrategy ClusterTrafficStrategy `json:"clusterTrafficStrategy,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	ConfigContext *runtime.RawExtension `json:"configContext"`
}

// ClusterTrafficPolicyStatus defines the observed state of ClusterTrafficPolicy
type ClusterTrafficPolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Required
	State State `json:"state"`
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`
	// LastReconciledTime date-time of the last reconcile
	// +kubebuilder:validation:Optional
	LastReconciledTime *metav1.Time `json:"lastReconciledTime,omitempty"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=ctp

// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// ClusterTrafficPolicy is the Schema for the clustertrafficpolicies API
type ClusterTrafficPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterTrafficPolicySpec   `json:"spec,omitempty"`
	Status ClusterTrafficPolicyStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ClusterTrafficPolicyList contains a list of ClusterTrafficPolicy
type ClusterTrafficPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterTrafficPolicy `json:"items"`
}

type BlueGreenStrategy struct {
	// +kubebuilder:validation:required
	StableCluster string `json:"stableCluster"`
	// +kubebuilder:validation:optional
	PreviewCluster string `json:"previewCluster,omitempty"`
}

type GlobalCanaryStrategy struct {
	// +kubebuilder:validation:required
	// +kubebuilder:validation:MinProperties:=1
	StableSubsetLabels map[string]string `json:"stableSubsetLabels"`
	// +kubebuilder:validation:required
	// +kubebuilder:validation:MinProperties:=1
	CanarySubsetLabels map[string]string `json:"canarySubsetLabels"`
	// +kubebuilder:validation:required
	StableWeight int `json:"stableWeight"`
	// +kubebuilder:validation:required
	CanaryWeight int `json:"canaryWeight"`
	// +kubebuilder:validation:optional
	// +kubebuilder:validation:MinProperties:=1
	TestSubsetLabels map[string]string `json:"testSubsetLabels,omitempty"`
}

type ClusterTrafficStrategy struct {
	BlueGreen    BlueGreenStrategy    `json:"blueGreen,omitempty"`
	GlobalCanary GlobalCanaryStrategy `json:"globalCanary,omitempty"`
}

// +kubebuilder:validation:Enum=Success;Failure
// +kubebuilder:default:=Success
type State string

func init() {
	SchemeBuilder.Register(&ClusterTrafficPolicy{}, &ClusterTrafficPolicyList{})
}
