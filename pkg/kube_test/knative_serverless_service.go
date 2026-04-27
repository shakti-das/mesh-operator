//go:build !ignore_test_utils

package kube_test

import (
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

type KnativeServerlessServiceBuilder struct {
	knativeServerlessService runtime.Object
}

func NewKnativeServerlessServiceBuilder(name, namespace, resourceVersion string) *KnativeServerlessServiceBuilder {
	obj := unstructured.Unstructured{}
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetKind(constants.KnativeServerlessServiceKind.Kind)
	obj.SetAPIVersion("networking.internal.knative.dev/v1alpha1")
	obj.SetUID("test-uid")
	obj.SetResourceVersion(resourceVersion)

	kssBuilder := &KnativeServerlessServiceBuilder{
		knativeServerlessService: &obj,
	}

	return kssBuilder
}

func (k *KnativeServerlessServiceBuilder) Build() runtime.Object {
	return k.knativeServerlessService
}
