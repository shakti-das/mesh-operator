// Copyright The mesh-operator Authors
// SPDX-License-Identifier: Apache-2.0

// Package v1alpha1 contains API Schema definitions for the meshoperator v1alpha1 API group
// +kubebuilder:object:generate=true
// +groupName=mesh.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	SchemeGroupVersion = schema.GroupVersion{Group: "mesh.io", Version: "v1alpha1"}
	ApiVersion         = SchemeGroupVersion.String()

	ClusterTrafficPolicyKind  = metav1.GroupVersionKind{Group: SchemeGroupVersion.Group, Version: SchemeGroupVersion.Version, Kind: "ClusterTrafficPolicy"}
	TrafficShardingPolicyKind = metav1.GroupVersionKind{Group: SchemeGroupVersion.Group, Version: SchemeGroupVersion.Version, Kind: "TrafficShardingPolicy"}

	MopKind            = metav1.GroupVersionKind{Group: SchemeGroupVersion.Group, Version: SchemeGroupVersion.Version, Kind: "MeshOperator"}
	MopVersionResource = schema.GroupVersionResource{Group: SchemeGroupVersion.Group, Version: SchemeGroupVersion.Version, Resource: "meshoperators"}

	MsmKind            = metav1.GroupVersionKind{Group: SchemeGroupVersion.Group, Version: SchemeGroupVersion.Version, Kind: "MeshServiceMetadata"}
	MsmVersionResource = schema.GroupVersionResource{Group: SchemeGroupVersion.Group, Version: SchemeGroupVersion.Version, Resource: "meshservicemetadatas"}
	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}
