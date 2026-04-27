//go:build !ignore_test_utils

package kube_test

import (
	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

type MopBuilder struct {
	mop *v1alpha1.MeshOperator
}

var DefaultAbortHttpStatus int32 = 418

func NewMopBuilder(namespace string, name string) *MopBuilder {
	mopBuilder := &MopBuilder{
		mop: &v1alpha1.MeshOperator{
			TypeMeta: metav1.TypeMeta{
				Kind:       "MeshOperator",
				APIVersion: "v1alpha1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
		},
	}
	return mopBuilder
}

func NewMopBuilderFrom(cloneFrom *v1alpha1.MeshOperator) *MopBuilder {
	mopBuilder := &MopBuilder{
		mop: cloneFrom.DeepCopy(),
	}

	return mopBuilder
}

func (m *MopBuilder) AddFilter(filter v1alpha1.ExtensionElement) *MopBuilder {
	m.mop.Spec.Extensions = append(m.mop.Spec.Extensions, filter)
	return m
}

func (m *MopBuilder) AddOverlay(overlay v1alpha1.Overlay) *MopBuilder {
	if m.mop.Spec.Overlays == nil {
		m.mop.Spec.Overlays = []v1alpha1.Overlay{overlay}
		return m
	}
	m.mop.Spec.Overlays = append(m.mop.Spec.Overlays, overlay)
	return m
}

func (m *MopBuilder) SetOverlays(overlays []v1alpha1.Overlay) *MopBuilder {
	m.mop.Spec.Overlays = overlays
	return m
}

func (m *MopBuilder) SetServiceHash(serviceName string, serviceHash string) *MopBuilder {
	if m.mop.Status.Services == nil {
		m.mop.Status.Services = make(map[string]*v1alpha1.ServiceStatus)
	}
	if _, exists := m.mop.Status.Services[serviceName]; !exists {
		m.mop.Status.Services[serviceName] = &v1alpha1.ServiceStatus{Hash: serviceHash}
	} else {
		m.mop.Status.Services[serviceName].Hash = serviceHash
	}
	return m
}

func (m *MopBuilder) SetNamespaceHash(namespaceHash string) *MopBuilder {
	m.mop.Status.NamespaceHash = namespaceHash
	return m
}

func (m *MopBuilder) AddSelector(key string, value string) *MopBuilder {
	if m.mop.Spec.ServiceSelector == nil {
		m.mop.Spec.ServiceSelector = make(map[string]string)
	}
	m.mop.Spec.ServiceSelector[key] = value
	return m
}

func (m *MopBuilder) SetUID(uid types.UID) *MopBuilder {
	m.mop.SetUID(uid)
	return m
}

func (m *MopBuilder) SetFinalizers(finalizers ...string) *MopBuilder {
	if len(finalizers) == 0 {
		m.mop.SetFinalizers([]string{})
	} else {
		m.mop.SetFinalizers(finalizers)
	}
	return m
}

func (m *MopBuilder) SetGeneration(generation int64) *MopBuilder {
	m.mop.SetGeneration(generation)
	return m
}

func (m *MopBuilder) SetResourceVersion(version string) *MopBuilder {
	m.mop.SetResourceVersion(version)
	return m
}

func (m *MopBuilder) SetPhase(phase v1alpha1.Phase) *MopBuilder {
	m.mop.Status.Phase = phase
	return m
}

func (m *MopBuilder) SetLastReconciledTime(reconciledTime *metav1.Time) *MopBuilder {
	m.mop.Status.LastReconciledTime = reconciledTime
	return m
}

func (m *MopBuilder) SetObservedGeneration(generation int64) *MopBuilder {
	m.mop.Status.ObservedGeneration = generation
	return m
}

func (m *MopBuilder) SetStatusMessage(message string) *MopBuilder {
	m.mop.Status.Message = message
	return m
}

func (m *MopBuilder) SetServiceStatusMessage(svc, message string) *MopBuilder {
	m.mop.Status.Services[svc].Message = message
	return m
}

func (m *MopBuilder) SetPhaseService(serviceName string, phase v1alpha1.Phase) *MopBuilder {
	if m.mop.Status.Services == nil {
		m.mop.Status.Services = make(map[string]*v1alpha1.ServiceStatus)
	}
	if serviceStatus, exists := m.mop.Status.Services[serviceName]; !exists {
		m.mop.Status.Services[serviceName] = &v1alpha1.ServiceStatus{Phase: phase}
	} else {
		serviceStatus.Phase = phase
	}
	return m
}

func (m *MopBuilder) SetPhaseServiceEntries(seName string, phase v1alpha1.Phase) *MopBuilder {
	if m.mop.Status.ServiceEntries == nil {
		m.mop.Status.ServiceEntries = make(map[string]*v1alpha1.ServiceStatus)
	}
	if seStatus, exists := m.mop.Status.ServiceEntries[seName]; !exists {
		m.mop.Status.ServiceEntries[seName] = &v1alpha1.ServiceStatus{Phase: phase}
	} else {
		seStatus.Phase = phase
	}
	return m
}

func (m *MopBuilder) SetRelatedResourcesInStatus(phase v1alpha1.Phase,
	resources []*v1alpha1.ResourceStatus) *MopBuilder {
	m.mop.Status.Phase = phase
	m.mop.Status.RelatedResources = resources
	return m
}

func (m *MopBuilder) AddRelatedResourceToStatus(phase v1alpha1.Phase,
	message string, filterName string, filterNamespace string) *MopBuilder {
	relatedResources := m.mop.Status.RelatedResources
	resourceStatus := v1alpha1.ResourceStatus{
		Phase:      phase,
		Message:    message,
		Name:       filterName,
		Namespace:  filterNamespace,
		ApiVersion: constants.EnvoyFilterResource.GroupVersion().String(),
		Kind:       constants.EnvoyFilterKind.Kind,
	}
	relatedResources = append(relatedResources, &resourceStatus)
	m.mop.Status.RelatedResources = relatedResources
	return m
}

func (m *MopBuilder) AddRelatedResourceToService(svc, filterNs, filterName string, phase v1alpha1.Phase) *MopBuilder {
	resourceStatus := v1alpha1.ResourceStatus{
		Phase:      phase,
		Message:    "",
		Name:       filterName,
		Namespace:  filterNs,
		ApiVersion: constants.EnvoyFilterResource.GroupVersion().String(),
		Kind:       constants.EnvoyFilterKind.Kind,
	}

	m.mop.Status.Services[svc].RelatedResources = append(m.mop.Status.Services[svc].RelatedResources, &resourceStatus)
	return m
}

func (m *MopBuilder) SetObjectRelatedResourcesPhasesForKind(kind string, objectName string, phases ...v1alpha1.Phase) *MopBuilder {
	var serviceStatusMap map[string]*v1alpha1.ServiceStatus
	if kind == constants.ServiceEntryKind.Kind {
		if m.mop.Status.ServiceEntries == nil {
			m.mop.Status.ServiceEntries = make(map[string]*v1alpha1.ServiceStatus)
		}
		serviceStatusMap = m.mop.Status.ServiceEntries
	} else {
		if m.mop.Status.Services == nil {
			m.mop.Status.Services = make(map[string]*v1alpha1.ServiceStatus)
		}
		serviceStatusMap = m.mop.Status.Services
	}
	serviceStatus := &v1alpha1.ServiceStatus{}
	var relatedResources []*v1alpha1.ResourceStatus
	for _, phase := range phases {
		relatedResource := &v1alpha1.ResourceStatus{Phase: phase}
		relatedResources = append(relatedResources, relatedResource)
	}
	serviceStatus.RelatedResources = relatedResources
	serviceStatusMap[objectName] = serviceStatus
	return m
}

func (m *MopBuilder) SetStatus(status v1alpha1.MeshOperatorStatus) *MopBuilder {
	m.mop.Status = status
	return m
}

func (m *MopBuilder) SetDeletionTimestamp() *MopBuilder {
	now := metav1.Now()
	m.mop.GetObjectMeta().SetDeletionTimestamp(&now)
	return m
}

func (m *MopBuilder) Build() *v1alpha1.MeshOperator {
	return m.mop
}

func AddPhaseRelatedResource(mop *v1alpha1.MeshOperator, serviceName string, phase v1alpha1.Phase, filter *unstructured.Unstructured) {
	mop.Status.Phase = phase
	if mop.Status.Services == nil {
		mop.Status.Services = make(map[string]*v1alpha1.ServiceStatus)
	}
	if _, exists := mop.Status.Services[serviceName]; !exists {
		mop.Status.Services[serviceName] = &v1alpha1.ServiceStatus{Phase: phase}
	}
	if mop.Status.Services[serviceName].RelatedResources == nil {
		mop.Status.Services[serviceName].RelatedResources = []*v1alpha1.ResourceStatus{}
	}
	relatedResource := &v1alpha1.ResourceStatus{
		ApiVersion: constants.EnvoyFilterResource.GroupVersion().String(),
		Kind:       constants.EnvoyFilterKind.Kind,
		Namespace:  filter.GetNamespace(),
		Name:       filter.GetName(),
		Phase:      phase,
	}
	mop.Status.Services[serviceName].RelatedResources = append(mop.Status.Services[serviceName].RelatedResources, relatedResource)
}

type FakeTimeProvider struct{}

func NewFakeTimeProvider() common.TimeProvider {
	return &FakeTimeProvider{}
}

func (g *FakeTimeProvider) Now() *metav1.Time {
	return &constants.FakeTime
}
