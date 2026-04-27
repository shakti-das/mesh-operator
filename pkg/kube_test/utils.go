//go:build !ignore_test_utils

package kube_test

import (
	commonconstants "github.com/istio-ecosystem/mesh-operator/common/pkg/k8s/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	networkingv1alpha3 "istio.io/api/networking/v1alpha3"
	"istio.io/client-go/pkg/apis/networking/v1alpha3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func CreateService(namespace string, name string) *corev1.Service {
	return CreateServiceWithLabels(namespace, name, map[string]string{})
}

func CreateServiceWithLabels(namespace string, name string, labels map[string]string) *corev1.Service {
	service := corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec:       corev1.ServiceSpec{},
		Status:     corev1.ServiceStatus{},
	}
	service.Namespace = namespace
	service.Name = name
	return &service
}

func CreateServiceEntryWithLabels(namespace, name string, labels map[string]string) *v1alpha3.ServiceEntry {
	return &v1alpha3.ServiceEntry{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceEntryKind.Kind,
			APIVersion: "networking.istio.io/v1alpha3",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: networkingv1alpha3.ServiceEntry{
			Hosts:    []string{"some-host.com"},
			Location: networkingv1alpha3.ServiceEntry_MESH_INTERNAL,
		},
	}
}

func CreateStatefulSet(namespace string, name string, servicename string, replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: servicename,
			Replicas:    &replicas,
		},
	}
}

func CreateEnvoyFilter(name string, namespace string, filterServiceAnnotationValue string) *unstructured.Unstructured {
	builder := NewUnstructuredBuilder(
		constants.EnvoyFilterResource,
		constants.EnvoyFilterKind, name, namespace)
	if filterServiceAnnotationValue != "" {
		builder.SetAnnotations(map[string]string{
			constants.ExtensionSourceAnnotation: filterServiceAnnotationValue,
		})
	}
	return builder.Build()
}

func UnstructuredToFilterObject(obj *unstructured.Unstructured) *v1alpha3.EnvoyFilter {
	filter := v1alpha3.EnvoyFilter{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		},
	}
	filter.SetLabels(obj.GetLabels())
	filter.SetAnnotations(obj.GetAnnotations())
	return &filter
}

func CreateNamespace(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.NamespaceKind.Kind,
			APIVersion: constants.NamespaceResource.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}

type UnstructuredBuilder struct {
	obj *unstructured.Unstructured
}

func NewUnstructuredBuilder(gvr schema.GroupVersionResource, gvk metav1.GroupVersionKind, name string, namespace string) *UnstructuredBuilder {
	builder := &UnstructuredBuilder{
		obj: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": gvr.GroupVersion().String(),
				"kind":       gvk.Kind,
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": namespace,
				},
			},
		},
	}
	return builder
}

func (u *UnstructuredBuilder) SetUID(uid types.UID) *UnstructuredBuilder {
	u.obj.SetUID(uid)
	return u
}

func (u *UnstructuredBuilder) SetLabels(labels map[string]string) *UnstructuredBuilder {
	u.obj.SetLabels(labels)
	return u
}

func (u *UnstructuredBuilder) SetAnnotations(annotations map[string]string) *UnstructuredBuilder {
	u.obj.SetAnnotations(annotations)
	return u
}

func (u *UnstructuredBuilder) Build() *unstructured.Unstructured {
	return u.obj
}

func CreateServiceAsUnstructuredObject(namespace string, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.ServiceResource.GroupVersion().String(),
			"kind":       constants.ServiceKind.Kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
		},
	}
}

func BuildCustomCtpObjectWithStatus(namespace string, name string, stableCluster string, previewCluster string, status map[string]interface{}) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": commonconstants.ClusterTrafficPolicyResource.GroupVersion().String(),
			"kind":       commonconstants.ClusterTrafficPolicyKind.Kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"clusterTrafficStrategy": map[string]interface{}{
					"blueGreen": map[string]interface{}{
						"stableCluster":  stableCluster,
						"previewCluster": previewCluster,
					},
				},
			},
		},
	}

	if status != nil {
		unstructured.SetNestedMap(obj.Object, status, "status")
	}
	return obj
}
