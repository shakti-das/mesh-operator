//go:build !ignore_test_utils

package kube_test

import (
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	networkingv1alpha3 "istio.io/api/networking/v1alpha3"
	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ServiceEntryBuilder struct {
	se istiov1alpha3.ServiceEntry
}

func NewServiceEntryBuilder(name string, namespace string) *ServiceEntryBuilder {
	seBuilder := &ServiceEntryBuilder{
		se: istiov1alpha3.ServiceEntry{
			TypeMeta: metav1.TypeMeta{
				Kind:       constants.ServiceEntryKind.Kind,
				APIVersion: constants.ServiceEntryResource.GroupVersion().String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
				UID:       "test-uid",
			},
		},
	}
	return seBuilder
}

func (s *ServiceEntryBuilder) SetAnnotations(annotations map[string]string) *ServiceEntryBuilder {
	s.se.SetAnnotations(annotations)
	return s
}

func (s *ServiceEntryBuilder) SetLabels(labels map[string]string) *ServiceEntryBuilder {
	s.se.SetLabels(labels)
	return s
}

func (s *ServiceEntryBuilder) SetResourceVersion(string string) *ServiceEntryBuilder {
	s.se.SetResourceVersion(string)
	return s
}

func (s *ServiceEntryBuilder) SetHosts(hosts []string) *ServiceEntryBuilder {
	s.se.Spec.Hosts = hosts
	return s
}

func (s *ServiceEntryBuilder) SetExportTo(exportTo []string) *ServiceEntryBuilder {
	s.se.Spec.ExportTo = exportTo
	return s
}

func (s *ServiceEntryBuilder) SetPorts(ports []*networkingv1alpha3.ServicePort) *ServiceEntryBuilder {
	s.se.Spec.Ports = ports
	return s
}

func (s *ServiceEntryBuilder) Build() istiov1alpha3.ServiceEntry {
	return s.se
}

func (s *ServiceEntryBuilder) GetServiceEntryAsUnstructuredObject() *unstructured.Unstructured {
	svcAsUnstructuredObject, _ := kube.ObjectToUnstructured(s.se)
	return svcAsUnstructuredObject
}
