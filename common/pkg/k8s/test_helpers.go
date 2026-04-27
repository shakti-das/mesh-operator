package k8s

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/istio-ecosystem/mesh-operator/common/pkg/k8s/constants"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var (
	existingServiceLabels = map[string]string{"existing": "true"}

	testUID       = types.UID("test-uid")
	testUIDFormat = "test-uid-%s-%s"
)

type ResourceNotFoundError struct {
	Resource  schema.GroupVersionResource
	Name      string
	Namespace string
}

func (err *ResourceNotFoundError) Error() string {
	return fmt.Sprintf("resource %s.%s of type %q not found", err.Name, err.Namespace, err.Resource.String())
}

type PortInfo struct {
	Name       string
	PortNumber int64
	Protocol   string
	TargetPort string
}

type ServiceBuilder struct {
	service *unstructured.Unstructured
}

func NewServiceBuilder(name string, namespace string) *ServiceBuilder {
	svc := &ServiceBuilder{
		service: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": constants.ServiceResource.GroupVersion().String(),
				"kind":       constants.ServiceKind.Kind,
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": namespace,
				},
			},
		},
	}

	testUid := types.UID(fmt.Sprintf(testUIDFormat, namespace, name))
	svc.service.SetUID(testUid)
	svc.SetType("ClusterIP")
	return svc
}

func (s *ServiceBuilder) SetUID(uid types.UID) *ServiceBuilder {
	s.service.SetUID(uid)
	return s
}

// SetClusterName method SetZZZ_DeprecatedClusterName has been deprecated. This is a hack for upgrading to client-go v0.30.x
func (s *ServiceBuilder) SetClusterName(name string) *ServiceBuilder {
	if len(name) == 0 {
		unstructured.RemoveNestedField(s.service.UnstructuredContent(), "metadata", "clusterName")
		return s
	}
	_ = unstructured.SetNestedField(s.service.UnstructuredContent(), name, "metadata", "clusterName")
	return s
}

func (s *ServiceBuilder) SetType(serviceType string) *ServiceBuilder {
	_ = unstructured.SetNestedField(s.service.UnstructuredContent(), serviceType, "spec", "type")
	return s
}

func (s *ServiceBuilder) SetClusterIP(clusterIP string) *ServiceBuilder {
	_ = unstructured.SetNestedField(s.service.UnstructuredContent(), clusterIP, "spec", "clusterIP")
	return s
}

func (s *ServiceBuilder) SetExternalName(externalName string) *ServiceBuilder {
	s.SetType("ExternalName")
	_ = unstructured.SetNestedField(s.service.UnstructuredContent(), externalName, "spec", "externalName")
	return s
}

func (s *ServiceBuilder) SetAnnotations(annotations map[string]string) *ServiceBuilder {
	s.service.SetAnnotations(annotations)
	return s
}

func (s *ServiceBuilder) SetLabels(labels map[string]string) *ServiceBuilder {
	s.service.SetLabels(labels)
	return s
}

func (s *ServiceBuilder) SetPorts(ports map[string]int64) *ServiceBuilder {
	var portsSpec []interface{}
	for portName, portNumber := range ports {
		portsSpec = append(portsSpec, map[string]interface{}{"name": portName, "port": portNumber})
	}
	_ = unstructured.SetNestedField(s.service.UnstructuredContent(), portsSpec, "spec", "ports")
	return s
}

func (s *ServiceBuilder) SetPortsInOrder(ports []PortInfo) *ServiceBuilder {
	var portsSpec []interface{}
	for _, port := range ports {
		portInfo := map[string]interface{}{"name": port.Name, "port": port.PortNumber}
		if port.Protocol != "" {
			portInfo["protocol"] = port.Protocol
		}
		if port.TargetPort != "" {
			if n, err := strconv.ParseInt(port.TargetPort, 10, 64); err == nil {
				portInfo["targetPort"] = n
			} else {
				portInfo["targetPort"] = port.TargetPort
			}
		}
		portsSpec = append(portsSpec, portInfo)
	}
	_ = unstructured.SetNestedField(s.service.UnstructuredContent(), portsSpec, "spec", "ports")
	return s
}

func (s *ServiceBuilder) SetSelector(selectors map[string]interface{}) *ServiceBuilder {
	_ = unstructured.SetNestedField(s.service.UnstructuredContent(), selectors, "spec", "selector")
	return s
}

func (s *ServiceBuilder) Build() *unstructured.Unstructured {
	return s.service
}

type WorkloadEntryBuilder struct {
	workloadEntry *unstructured.Unstructured
}

func NewWorkloadEntryBuilder(name string, namespace string) *WorkloadEntryBuilder {
	wle := &WorkloadEntryBuilder{
		workloadEntry: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": constants.WorkloadEntryResource.GroupVersion().String(),
				"kind":       constants.WorkloadEntryKind.Kind,
				"metadata": map[string]interface{}{
					"name":      name,
					"uid":       string(testUID),
					"namespace": namespace,
				},
			},
		},
	}
	return wle
}

func (w *WorkloadEntryBuilder) SetOwnerReferences(ownerRefs []metav1.OwnerReference) *WorkloadEntryBuilder {
	var ownerRefsCopy []interface{}
	for _, ownerRef := range ownerRefs {
		ownerRefsCopy = append(ownerRefsCopy, map[string]interface{}{
			"apiVersion": ownerRef.APIVersion,
			"kind":       ownerRef.Kind,
			"name":       ownerRef.Name,
			"uid":        string(ownerRef.UID),
		})
	}
	_ = unstructured.SetNestedField(w.workloadEntry.UnstructuredContent(), ownerRefsCopy, "metadata", "ownerReferences")
	return w
}

func (w *WorkloadEntryBuilder) SetPorts(ports map[string]int64) *WorkloadEntryBuilder {
	portsCopy := make(map[string]interface{}, len(ports))
	for k, v := range ports {
		portsCopy[k] = v
	}

	_ = unstructured.SetNestedMap(w.workloadEntry.UnstructuredContent(), portsCopy, "spec", "ports")
	return w
}

func (w *WorkloadEntryBuilder) SetLabels(labels map[string]string) *WorkloadEntryBuilder {
	w.workloadEntry.SetLabels(labels)
	return w
}

func (w *WorkloadEntryBuilder) Build() *unstructured.Unstructured {
	return w.workloadEntry
}

// Use k8s.NewServiceBuilder instead of this method.
func CreateServiceObj(name string, namespace string, annotations map[string]string, labels map[string]string, externalName string, clusterIP string) *unstructured.Unstructured {
	svc := NewServiceBuilder(name, namespace)
	if annotations != nil {
		svc.SetAnnotations(annotations)
	}
	if labels != nil {
		svc.SetLabels(labels)
	}
	if externalName != "" {
		svc.SetExternalName(externalName)
	}
	if clusterIP != "" {
		svc.SetClusterIP(clusterIP)
	}

	return svc.Build()
}

type FakeServiceInterceptor func(*ServiceBuilder) *ServiceBuilder
type FakeServiceInterceptor2 func(*ServiceBuilder, int) *ServiceBuilder

type FakeServiceStore struct {
	GetAllServicesResponse      map[string][]unstructured.Unstructured
	ExistingServicesByNamespace map[string][]*unstructured.Unstructured

	invocationCounter int

	Interceptor  FakeServiceInterceptor
	Interceptor2 FakeServiceInterceptor2
}

func (f *FakeServiceStore) createServiceObj(name string, namespace string, annotations map[string]string, labels map[string]string, externalName string, clusterIP string) *unstructured.Unstructured {
	f.invocationCounter += 1

	svc := NewServiceBuilder(name, namespace)
	if annotations != nil {
		svc.SetAnnotations(annotations)
	}
	if labels != nil {
		svc.SetLabels(labels)
	}
	if externalName != "" {
		svc.SetExternalName(externalName)
	}
	if clusterIP != "" {
		svc.SetClusterIP(clusterIP)
	}

	if f.Interceptor != nil {
		return f.Interceptor(svc).Build()
	}
	if f.Interceptor2 != nil {
		return f.Interceptor2(svc, f.invocationCounter).Build()
	}

	return svc.Build()
}

func (f *FakeServiceStore) Get(name string, namespace string) (*unstructured.Unstructured, error) {
	if strings.HasSuffix(name, "not-found") {
		return nil, &ResourceNotFoundError{Resource: constants.ServiceResource, Name: name, Namespace: namespace}
	}

	if name == "get-service-failed" {
		return nil, k8serrors.NewInternalError(fmt.Errorf("failed getting service"))
	}
	if existingServices, exists := f.ExistingServicesByNamespace[namespace]; exists {
		for _, existingService := range existingServices {
			if existingService != nil && existingService.GetName() == name && existingService.GetNamespace() == namespace {
				return existingService, nil
			}
		}
	}

	return CreateServiceObj(name, namespace, nil, existingServiceLabels, "", ""), nil
}

func (f *FakeServiceStore) GetFirstServiceByLabels(labelSelectorString string, namespace string) (*unstructured.Unstructured, error) {
	if labelSelectorString == "p_servicename=not-found" {
		return nil, k8serrors.NewInternalError(fmt.Errorf("Failed to find service by label = %s", labelSelectorString))
	}

	labelSelector, err := metav1.ParseToLabelSelector(labelSelectorString)
	if err != nil {
		return nil, k8serrors.NewInternalError(fmt.Errorf("Error parsing labelSelectorString = %s", labelSelectorString))
	}
	selector, err := metav1.LabelSelectorAsMap(labelSelector)
	if err != nil {
		return nil, k8serrors.NewInternalError(fmt.Errorf("Failed in metav1.LabelSelectorAsMap"))
	}
	for k, v := range selector {
		existingServiceLabels[k] = v
	}
	return f.createServiceObj(existingServiceLabels["p_servicename"], namespace, nil, existingServiceLabels, "", ""), err
}

func (f *FakeServiceStore) GetServicesByLabels(labelSelector string, namespace string) ([]*unstructured.Unstructured, error) {

	x, err := f.GetFirstServiceByLabels(labelSelector, namespace)

	if err != nil {
		return nil, err
	}

	result := []*unstructured.Unstructured{x}

	if namespace == "mesh-multi-ns" {
		// Create one more service that belongs to the same p_servicename
		x2 := f.createServiceObj(existingServiceLabels["p_servicename"]+"_2", namespace, nil, existingServiceLabels, "", "")
		result = append(result, x2)
	}

	return result, nil
}

func (f *FakeServiceStore) GetAll(namespaceSelector string) (map[string][]unstructured.Unstructured, error) {
	if f.GetAllServicesResponse != nil {
		return f.GetAllServicesResponse, nil
	} else {
		return nil, fmt.Errorf("no response defined")
	}
}

type DeploymentBuilder struct {
	db *unstructured.Unstructured
}

func NewDeploymentBuilder(namespace string, name string, labels map[string]interface{}, annotations map[string]interface{}) *DeploymentBuilder {
	builder := &DeploymentBuilder{
		db: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": constants.DeploymentResource.GroupVersion().String(),
				"kind":       constants.DeploymentKind.Kind,
				"metadata": map[string]interface{}{
					"labels":      labels,
					"annotations": annotations,
					"name":        name,
					"namespace":   namespace,
				},
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"metadata": map[string]interface{}{
							"labels": labels,
						},
					},
				},
			},
		},
	}
	return builder
}

func (db *DeploymentBuilder) SetTemplateLabels(labels map[string]string) *DeploymentBuilder {
	_ = unstructured.SetNestedStringMap(db.db.UnstructuredContent(), labels, "spec", "template", "metadata", "labels")
	return db
}

func (db *DeploymentBuilder) SetTemplateAnnotations(annotations map[string]string) *DeploymentBuilder {
	_ = unstructured.SetNestedStringMap(db.db.UnstructuredContent(), annotations, "spec", "template", "metadata", "annotations")
	return db
}

func (db *DeploymentBuilder) Build() *unstructured.Unstructured {
	return db.db
}

type StatefulSetBuilder struct {
	sb *unstructured.Unstructured
}

func NewStatefulSetBuilder(namespace string, name string, labels map[string]interface{}, annotations map[string]interface{}) *StatefulSetBuilder {
	builder := &StatefulSetBuilder{
		sb: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": constants.StatefulSetResource.GroupVersion().String(),
				"kind":       constants.StatefulSetKind.Kind,
				"metadata": map[string]interface{}{
					"labels":      labels,
					"annotations": annotations,
					"name":        name,
					"namespace":   namespace,
				},
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"metadata": map[string]interface{}{
							"labels": labels,
						},
					},
				},
			},
		},
	}
	return builder
}

func (sb *StatefulSetBuilder) Build() *unstructured.Unstructured {
	return sb.sb
}

func NewPartialObjectMetadataBuilder(namespace, name, apiVersion, kind string) *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{
			Kind:       kind,
			APIVersion: apiVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}
