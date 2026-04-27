//go:build !ignore_test_utils

package kube_test

import (
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

type ServiceBuilder struct {
	service v1.Service
}

func NewServiceBuilder(name string, namespace string) *ServiceBuilder {
	serviceBuilder := &ServiceBuilder{
		service: v1.Service{
			TypeMeta: metav1.TypeMeta{
				Kind:       constants.ServiceKind.Kind,
				APIVersion: constants.ServiceResource.GroupVersion().String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
				UID:       "test-uid",
			},
		},
	}
	return serviceBuilder
}

func (s *ServiceBuilder) SetAnnotations(annotations map[string]string) *ServiceBuilder {
	s.service.SetAnnotations(annotations)
	return s
}

func (s *ServiceBuilder) SetLabels(labels map[string]string) *ServiceBuilder {
	s.service.SetLabels(labels)
	return s
}

func (s *ServiceBuilder) SetResourceVersion(resourceVersion string) *ServiceBuilder {
	s.service.SetResourceVersion(resourceVersion)
	return s
}

func (s *ServiceBuilder) SetClusterIp(clusterIp string) *ServiceBuilder {
	s.service.Spec.ClusterIP = clusterIp
	return s
}

func (s *ServiceBuilder) SetPorts(ports []v1.ServicePort) *ServiceBuilder {
	s.service.Spec.Ports = ports
	return s
}

func (s *ServiceBuilder) SetType(svcType v1.ServiceType) *ServiceBuilder {
	s.service.Spec.Type = svcType
	return s
}

func (s *ServiceBuilder) SetExternalName(extName string) *ServiceBuilder {
	s.service.Spec.ExternalName = extName
	return s
}

func (s *ServiceBuilder) SetUID(uid types.UID) *ServiceBuilder {
	s.service.SetUID(uid)
	return s
}

func (s *ServiceBuilder) Build() v1.Service {
	return s.service
}

func (s *ServiceBuilder) GetServiceAsUnstructuredObject() *unstructured.Unstructured {
	svcAsUnstructuredObject, _ := kube.ObjectToUnstructured(s.service)
	return svcAsUnstructuredObject
}
