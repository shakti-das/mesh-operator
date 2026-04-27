//go:build !ignore_test_utils

package kube_test

import (
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type RolloutBuilder struct {
	rollout *unstructured.Unstructured
}

func NewRolloutBuilder(name string, namespace string) *RolloutBuilder {
	rolloutBuilder := &RolloutBuilder{
		rollout: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": constants.RolloutResource.GroupVersion().String(),
				"kind":       constants.RolloutKind.Kind,
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": namespace,
				},
			},
		},
	}
	return rolloutBuilder
}

func (r *RolloutBuilder) SetAnnotations(annotations map[string]string) *RolloutBuilder {
	r.rollout.SetAnnotations(annotations)
	return r
}

func (r *RolloutBuilder) SetLabels(labels map[string]string) *RolloutBuilder {
	r.rollout.SetLabels(labels)
	return r
}

// SetTemplateLabel set the labels in rollout template
func (r *RolloutBuilder) SetTemplateLabel(labels map[string]string) *RolloutBuilder {
	r.SetNestedStringMap(labels, "spec", "template", "metadata", "labels")
	return r
}

// SetTemplateAnnotations set the annotations in rollout template
func (r *RolloutBuilder) SetTemplateAnnotations(labels map[string]string) *RolloutBuilder {
	r.SetNestedStringMap(labels, "spec", "template", "metadata", "annotations")
	return r
}

// SetNestedMap sets the map[string]interface{} value of a nested field.
func (r *RolloutBuilder) SetNestedMap(value map[string]interface{}, fields ...string) *RolloutBuilder {
	_ = unstructured.SetNestedMap(r.rollout.Object, value, fields...)
	return r
}

// SetNestedStringMap sets the map[string]string value of a nested field.
func (r *RolloutBuilder) SetNestedStringMap(value map[string]string, fields ...string) *RolloutBuilder {
	_ = unstructured.SetNestedStringMap(r.rollout.Object, value, fields...)
	return r
}

func (r *RolloutBuilder) SetResourceVersion(version string) *RolloutBuilder {
	r.rollout.SetResourceVersion(version)
	return r
}

func (r *RolloutBuilder) SetGeneration(generation int64) *RolloutBuilder {
	r.rollout.SetGeneration(generation)
	return r
}

func (r *RolloutBuilder) Build() *unstructured.Unstructured {
	return r.rollout
}

func (r *RolloutBuilder) AddOwnerReference(ownerReference metav1.OwnerReference) *RolloutBuilder {
	r.rollout.SetOwnerReferences(append(r.rollout.GetOwnerReferences(), ownerReference))
	return r
}
