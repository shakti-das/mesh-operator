package templating

import (
	"fmt"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type TestableMutator struct {
	delegate func(context *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error)
}

func (m *TestableMutator) Mutate(ctx *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	return m.delegate(ctx, config)
}

func TestApplyMutators_EmptyMutators(t *testing.T) {
	configObjects := []*unstructured.Unstructured{
		kube_test.CreateEnvoyFilter("filter-1", "test", ""),
		kube_test.CreateEnvoyFilter("filter-2", "test", ""),
	}

	mutatedObjects, err := applyMutators(nil, configObjects, []Mutator{})

	assert.NoError(t, err)
	assert.Equal(t, configObjects, mutatedObjects)
}

func TestApplyMutators_EmptyConfig(t *testing.T) {
	mutator := TestableMutator{
		delegate: func(context *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			config.SetName("mutated-name")
			return config, nil
		},
	}

	mutatedObjects, err := applyMutators(nil, []*unstructured.Unstructured{}, []Mutator{&mutator})

	assert.NoError(t, err)
	assert.Equal(t, 0, len(mutatedObjects))
}

func TestApplyMutators_AppliedInOrder(t *testing.T) {
	mutator1 := TestableMutator{
		delegate: func(context *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			config.SetName(config.GetName() + "-1")
			return config, nil
		},
	}
	mutator2 := TestableMutator{
		delegate: func(context *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			config.SetName(config.GetName() + "-2")
			return config, nil
		},
	}
	configObject := unstructured.Unstructured{}
	configObject.SetName("object")

	mutatedConfig, err := applyMutators(nil, []*unstructured.Unstructured{&configObject}, []Mutator{&mutator1, &mutator2})

	assert.NoError(t, err)
	assert.Equal(t, 1, len(mutatedConfig))
	assert.Equal(t, "object-1-2", mutatedConfig[0].GetName())
}

func TestApplyMutators_ErrorPropagated(t *testing.T) {
	mutator := TestableMutator{
		delegate: func(context *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			return nil, fmt.Errorf("test-error")
		},
	}
	configObject := unstructured.Unstructured{}

	mutatedConfig, err := applyMutators(nil, []*unstructured.Unstructured{&configObject}, []Mutator{&mutator})

	assert.NotNil(t, err)
	assert.Nil(t, mutatedConfig)
	assert.Errorf(t, err, "test-error")
}

func TestManagedByMutator(t *testing.T) {
	emptyLabelsObject := unstructured.Unstructured{}
	emptyLabelsObject.SetLabels(make(map[string]string))

	nonEmptyLabels := map[string]string{"some-label": "some-value"}
	nonEmptyLabelsObject := unstructured.Unstructured{}
	nonEmptyLabelsObject.SetLabels(nonEmptyLabels)

	testCases := []struct {
		name           string
		configObject   *unstructured.Unstructured
		expectedLabels map[string]string
	}{
		{
			name:           "No Labels",
			configObject:   &unstructured.Unstructured{},
			expectedLabels: map[string]string{constants.MeshIoManagedByLabel: "mesh-operator"},
		},
		{
			name:           "Empty Labels",
			configObject:   &emptyLabelsObject,
			expectedLabels: map[string]string{constants.MeshIoManagedByLabel: "mesh-operator"},
		},
		{
			name:         "Non Empty Labels",
			configObject: &nonEmptyLabelsObject,
			expectedLabels: map[string]string{
				"some-label":                   "some-value",
				constants.MeshIoManagedByLabel: "mesh-operator",
			},
		},
	}

	mutator := ManagedByMutator{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mutatedObject, err := mutator.Mutate(nil, tc.configObject)

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedLabels, mutatedObject.GetLabels())
		})
	}
}

func TestOwnersRefMutator(t *testing.T) {

	var (
		serviceNamespace = "service-namespace"
		otherNamespace   = "other-namespace"
	)

	ownerRef := metav1.OwnerReference{
		APIVersion: v1alpha1.ApiVersion,
		Kind:       v1alpha1.MsmKind.Kind,
		Name:       "service-name",
	}

	otherOwnerRef := metav1.OwnerReference{
		APIVersion: v1alpha1.ApiVersion,
		Kind:       v1alpha1.MsmKind.Kind,
		Name:       "other-service",
	}

	testCases := []struct {
		name              string
		configNs          string
		ownerRefInContext *metav1.OwnerReference
		ownerRefsOnObject []metav1.OwnerReference
		expectedOwnerRefs []metav1.OwnerReference
	}{
		{
			name:              "No OwnerRef in context - same target object ns - no owners on object",
			configNs:          serviceNamespace,
			ownerRefInContext: nil,
			ownerRefsOnObject: nil,
			expectedOwnerRefs: nil,
		},
		{
			name:              "No OwnerRef in context - same target object ns - empty owners on object",
			configNs:          serviceNamespace,
			ownerRefInContext: nil,
			ownerRefsOnObject: []metav1.OwnerReference{},
			expectedOwnerRefs: nil,
		},
		{
			name:              "No OwnerRef in context - same target object ns - owners on object",
			configNs:          serviceNamespace,
			ownerRefInContext: nil,
			ownerRefsOnObject: []metav1.OwnerReference{otherOwnerRef},
			expectedOwnerRefs: []metav1.OwnerReference{otherOwnerRef},
		},
		{
			name:              "OwnerRef in context - same target object ns - no owners on object",
			configNs:          serviceNamespace,
			ownerRefInContext: &ownerRef,
			ownerRefsOnObject: nil,
			expectedOwnerRefs: []metav1.OwnerReference{ownerRef},
		},
		{
			name:              "OwnerRef in context - same target object ns - other owners on object",
			configNs:          serviceNamespace,
			ownerRefInContext: &ownerRef,
			ownerRefsOnObject: []metav1.OwnerReference{otherOwnerRef},
			expectedOwnerRefs: []metav1.OwnerReference{ownerRef, otherOwnerRef},
		},
		{
			name:              "OwnerRef in context - same target object ns - same owner on object",
			configNs:          serviceNamespace,
			ownerRefInContext: &ownerRef,
			ownerRefsOnObject: []metav1.OwnerReference{ownerRef, otherOwnerRef},
			expectedOwnerRefs: []metav1.OwnerReference{ownerRef, otherOwnerRef},
		},
		{
			name:              "OwnerRef in context - different target object ns - no owners on object",
			configNs:          otherNamespace,
			ownerRefInContext: &ownerRef,
			ownerRefsOnObject: nil,
			expectedOwnerRefs: nil,
		},
		{
			name:              "OwnerRef in context - different target object ns - other owners on object",
			configNs:          otherNamespace,
			ownerRefInContext: &ownerRef,
			ownerRefsOnObject: []metav1.OwnerReference{otherOwnerRef},
			expectedOwnerRefs: []metav1.OwnerReference{otherOwnerRef},
		},
		{
			name:              "OwnerRef in context - different target object ns - same owner on object",
			configNs:          otherNamespace,
			ownerRefInContext: &ownerRef,
			ownerRefsOnObject: []metav1.OwnerReference{ownerRef, otherOwnerRef},
			expectedOwnerRefs: []metav1.OwnerReference{ownerRef, otherOwnerRef},
		},
	}

	mutator := OwnerRefMutator{}
	targetObject := &unstructured.Unstructured{}
	targetObject.SetNamespace(serviceNamespace)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := RenderRequestContext{
				OwnerRef: tc.ownerRefInContext,
				Object:   targetObject,
			}
			configObject := unstructured.Unstructured{}
			configObject.SetOwnerReferences(tc.ownerRefsOnObject)
			configObject.SetNamespace(tc.configNs)

			mutatedObject, err := mutator.Mutate(&ctx, &configObject)

			assert.NoError(t, err)
			if tc.expectedOwnerRefs == nil {
				assert.Nil(t, mutatedObject.GetOwnerReferences())
			} else {
				assert.NotNil(t, mutatedObject.GetOwnerReferences())
				assert.Equal(t, len(tc.expectedOwnerRefs), len(mutatedObject.GetOwnerReferences()))
				assert.ElementsMatch(t, tc.expectedOwnerRefs, mutatedObject.GetOwnerReferences())
			}
		})
	}
}

func TestConfigNamespaceAnnotationCleanup(t *testing.T) {

	emptyAnnotationsMop := unstructured.Unstructured{}
	emptyAnnotationsMop.SetAnnotations(map[string]string{})

	nonEmptyAnnotationsMop := unstructured.Unstructured{}
	nonEmptyAnnotationsMop.SetAnnotations(map[string]string{
		"annotation1": "value1",
	})

	configNsAnnotationPresent := unstructured.Unstructured{}
	configNsAnnotationPresent.SetAnnotations(map[string]string{
		"annotation1":                      "value1",
		"mesh.io/configNamespaceExtension": "true",
	})

	testCases := []struct {
		name                string
		config              *unstructured.Unstructured
		expectedAnnotations map[string]string
	}{
		{
			name:                "Nil annotations",
			config:              &unstructured.Unstructured{},
			expectedAnnotations: nil,
		},
		{
			name:                "Empty annotations",
			config:              &emptyAnnotationsMop,
			expectedAnnotations: map[string]string{},
		},
		{
			name:   "Non empty annotations",
			config: &nonEmptyAnnotationsMop,
			expectedAnnotations: map[string]string{
				"annotation1": "value1",
			},
		},
		{
			name:   "Config-ns annotation present",
			config: &configNsAnnotationPresent,
			expectedAnnotations: map[string]string{
				"annotation1": "value1",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mutator := ConfigNamespaceAnnotationCleanup{}

			newConfig, err := mutator.Mutate(nil, tc.config)

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedAnnotations, newConfig.GetAnnotations())
		})
	}
}

func TestMutateRenderedConfigsWhenInputIsNull(t *testing.T) {
	renderCtx := RenderRequestContext{}
	mutatedConfig, err := MutateRenderedConfigs(&renderCtx, nil, nil)
	assert.Nil(t, mutatedConfig)
	assert.Nil(t, err)
}
