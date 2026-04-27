package transition

import (
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestConfigInfo(t *testing.T) {
	expectedName := "config-name"
	expectedNamespace := "config-namespace"
	ci := &configInfo{
		getName: func(_ *unstructured.Unstructured) (string, error) {
			return expectedName, nil
		},
		getNamespace: func(_ *unstructured.Unstructured) (string, error) {
			return expectedNamespace, nil
		},
		gvr: constants.DeploymentResource,
	}

	obj := &unstructured.Unstructured{}
	obj.SetNamespace("some-ns")
	obj.SetName("some-name")
	name, ns, err := ci.getConfigObjectNameAndNamespace(obj)
	assert.Nil(t, err)
	assert.Equal(t, expectedName, name)
	assert.Equal(t, expectedNamespace, ns)
}

func TestDeprecatedTemplates(t *testing.T) {

	testCases := []struct {
		name                              string
		enableCopilotToMopTransition      bool
		transitionTemplatesOwnedByCopilot []string
		templateType                      string
		expectedIsDeprecated              bool
	}{
		{
			name:                              "Template type deprecated in copilot",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"core-on-sam/coreapp", "generic/service-filters/wasm"},
			templateType:                      DefaultDefault,
			expectedIsDeprecated:              true,
		},
		{
			name:                              "Template type partially owned by copilot - consider deprecated in MOP transition logic",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"generic/service-filters/wasm"},
			templateType:                      GenericServiceFilters,
			expectedIsDeprecated:              true,
		},
		{
			name:                              "Template owned by copilot",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"core-on-sam/coreapp", "generic/service-filters/wasm"},
			templateType:                      "core-on-sam_coreapp",
			expectedIsDeprecated:              false,
		},
		{
			name:                              "EnableCopilotToMopTransition flag is disabled",
			enableCopilotToMopTransition:      false,
			transitionTemplatesOwnedByCopilot: []string{},
			templateType:                      "default_default",
			expectedIsDeprecated:              false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			transitionTemplatesOwnedByCopilotOriginalValue := TransitionTemplatesOwnedByCopilot
			enableCopilotToMopTransitionOriginalValue := EnableCopilotToMopTransition
			TransitionTemplatesOwnedByCopilot = tc.transitionTemplatesOwnedByCopilot
			EnableCopilotToMopTransition = tc.enableCopilotToMopTransition
			defer func() {
				TransitionTemplatesOwnedByCopilot = transitionTemplatesOwnedByCopilotOriginalValue
				EnableCopilotToMopTransition = enableCopilotToMopTransitionOriginalValue
			}()
			actual := isTemplateDeprecatedByCopilot(tc.templateType)
			assert.Equal(t, tc.expectedIsDeprecated, actual)
		})
	}
}

func TestFindConfigObjectByName(t *testing.T) {
	noName := &unstructured.Unstructured{
		Object: map[string]interface{}{},
	}
	withName := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test-name",
			},
		},
	}

	idx, obj := findConfigObjectByName([]*unstructured.Unstructured{noName}, "some-name")
	assert.Equal(t, -1, idx)
	assert.Nil(t, obj)

	idx, obj = findConfigObjectByName([]*unstructured.Unstructured{noName}, "some-name")
	assert.Equal(t, -1, idx)
	assert.Nil(t, obj)

	idx, obj = findConfigObjectByName([]*unstructured.Unstructured{noName, withName}, "some-name")
	assert.Equal(t, -1, idx)
	assert.Nil(t, obj)

	idx, obj = findConfigObjectByName([]*unstructured.Unstructured{withName}, "test-name")
	assert.Equal(t, 0, idx)
	assert.Equal(t, withName, obj)

	idx, obj = findConfigObjectByName([]*unstructured.Unstructured{withName, noName}, "test-name")
	assert.Equal(t, 0, idx)
	assert.Equal(t, withName, obj)

	idx, obj = findConfigObjectByName([]*unstructured.Unstructured{noName, withName}, "test-name")
	assert.Equal(t, 1, idx)
	assert.Equal(t, withName, obj)
}

func TestShoulHaveProtocolFilter(t *testing.T) {
	withThrift := unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"ports": []interface{}{
					map[string]interface{}{
						"name": "thrift-port",
					},
				},
			},
		},
	}

	noPorts := unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{},
		},
	}

	noThrift := unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"ports": []interface{}{
					map[string]interface{}{
						"name": "grpc-port",
					},
				},
			},
		},
	}

	assert.False(t, shouldHaveProtocolFilter(&noPorts))
	assert.False(t, shouldHaveProtocolFilter(&noThrift))
	assert.True(t, shouldHaveProtocolFilter(&withThrift))
}

func TestShouldHaveAdditionalServiceTemplates(t *testing.T) {
	testCases := []struct {
		name                      string
		service                   *unstructured.Unstructured
		templateType              string
		additionalServiceExpected bool
		expectedFilterList        []string
		errorExpected             bool
	}{
		{
			name:                      "SvcContainsServiceTemplateAnnotation",
			service:                   kube_test.NewServiceBuilder("test-svc", "test-namespace").SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{\"generic/service-filters\":[\"authority\"]}"}).GetServiceAsUnstructuredObject(),
			templateType:              GenericServiceFilters,
			expectedFilterList:        []string{"authority"},
			additionalServiceExpected: true,
		},
		{
			name:                      "SvcContainsServiceTemplateAnnotation",
			service:                   kube_test.NewServiceBuilder("test-svc", "test-namespace").SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{\"hawking/service-filters\":[\"gzip\"]}"}).GetServiceAsUnstructuredObject(),
			templateType:              HawkingServiceFilters,
			additionalServiceExpected: true,
			expectedFilterList:        []string{"gzip"},
		},
		{
			name:                      "Annotation value is empty json string",
			service:                   kube_test.NewServiceBuilder("test-svc", "test-namespace").SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{}"}).GetServiceAsUnstructuredObject(),
			templateType:              "whatever",
			additionalServiceExpected: false,
		},
		{
			name:                      "Empty Filter list",
			service:                   kube_test.NewServiceBuilder("test-svc", "test-namespace").SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{\"generic/service-filters\":[]}"}).GetServiceAsUnstructuredObject(),
			templateType:              "whatever",
			additionalServiceExpected: false,
			expectedFilterList:        []string{},
		},
		{
			name:                      "Empty annotation value",
			service:                   kube_test.NewServiceBuilder("test-svc", "test-namespace").SetAnnotations(map[string]string{ServiceTemplateAnnotation: ""}).GetServiceAsUnstructuredObject(),
			templateType:              "whatever",
			additionalServiceExpected: false,
			errorExpected:             true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			value, filterList, err := shouldHaveAdditionalServiceTemplates(tc.service, tc.templateType)
			assert.Equal(t, tc.additionalServiceExpected, value)
			if !tc.errorExpected {
				assert.Nil(t, err)
				assert.ElementsMatch(t, filterList, tc.expectedFilterList)
			} else {
				assert.NotNil(t, err)
			}
		})
	}

}

func TestGetEnvoyFilterNameFromMetadata(t *testing.T) {

	noPlatformLabels := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test_service",
			},
		},
	}

	hasCell := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test_service",
				"labels": map[string]interface{}{
					"p_cell":        "cell",
					"p_servicename": "p_service",
				},
			},
		},
	}

	hasPlatformLabels := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test_service",
				"labels": map[string]interface{}{
					"p_servicename": "p_service",
				},
			},
		},
	}

	hasMsiAndPlatformLabels := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test_service",
				"labels": map[string]interface{}{
					"p_servicename":         "p_service",
					"mesh_service_instance": "msi",
				},
			},
		},
	}

	hasCellAndMsi := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test_service",
				"labels": map[string]interface{}{
					"p_cell":                "cell",
					"p_servicename":         "p_service",
					"mesh_service_instance": "msi",
				},
			},
		},
	}

	noPlatformLabelsAndMsi := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test_service",
				"labels": map[string]interface{}{
					"mesh_service_instance": "msi",
				},
			},
		},
	}

	testCases := []struct {
		name               string
		object             *unstructured.Unstructured
		suffix             string
		expectedFilterName string
	}{
		{
			name:               "No platform labels",
			object:             noPlatformLabels,
			suffix:             "filter_suffix",
			expectedFilterName: "test_service-filter_suffix",
		},
		{
			name:               "Has cell",
			object:             hasCell,
			suffix:             "filter_suffix",
			expectedFilterName: "cell-p_service-filter_suffix",
		},
		{
			name:               "Has platform labels",
			object:             hasPlatformLabels,
			suffix:             "filter_suffix",
			expectedFilterName: "p_service-filter_suffix",
		},
		{
			name:               "Has MSI and platform labels",
			object:             hasMsiAndPlatformLabels,
			suffix:             "filter_suffix",
			expectedFilterName: "p_service-msi-filter_suffix",
		},
		{
			name:               "Has cell and MSI",
			object:             hasCellAndMsi,
			suffix:             "filter_suffix",
			expectedFilterName: "cell-p_service-msi-filter_suffix",
		},
		{
			name:               "No platform labels and MSI",
			object:             noPlatformLabelsAndMsi,
			suffix:             "filter_suffix",
			expectedFilterName: "test_service-filter_suffix",
		},
		{
			name:               "Has cell, no suffix",
			object:             hasCell,
			expectedFilterName: "test_service",
		},
		{
			name:               "Has platform labels, no suffix",
			object:             hasPlatformLabels,
			expectedFilterName: "test_service",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedFilterName, getResourceNameFromMetadata(tc.object, tc.suffix))
		})
	}
}
