//go:build !ignore_test_utils

package kube_test

import (
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

type KnativeIngressBuilder struct {
	knativeIngress runtime.Object
}

func NewKnativeIngressBuilder(name, namespace, resourceVersion string) *KnativeIngressBuilder {
	obj := unstructured.Unstructured{}
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetKind(constants.KnativeIngressKind.Kind)
	obj.SetAPIVersion("networking.internal.knative.dev/v1alpha1")
	obj.SetUID("test-uid")
	obj.SetResourceVersion(resourceVersion)

	kiBuilder := &KnativeIngressBuilder{
		knativeIngress: &obj,
	}

	return kiBuilder
}

func (k *KnativeIngressBuilder) Build() runtime.Object {
	return k.knativeIngress
}
