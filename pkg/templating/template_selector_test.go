package templating

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"

	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
)

func TestGetTemplatesOrDefault(t *testing.T) {
	clusterTrafficPolicy := kube_test.BuildCustomCtpObjectWithStatus("hawkin", "prediction", "cluster1", "cluster2", nil)

	testCases := []struct {
		name                 string
		namespace            string
		kind                 string
		resourceName         string
		existingTemplates    map[string]string
		clusterTrafficPolicy *unstructured.Unstructured

		expectedTemplates map[string]string
	}{
		{
			name:         "SpecificTemplate",
			namespace:    "hawking",
			kind:         "Service",
			resourceName: "prediction",
			existingTemplates: map[string]string{
				"hawking_prediction_something": "<template-content>",
				"default_default_something":    "<template-content>",
			},
			expectedTemplates: map[string]string{
				"hawking_prediction_something": "<template-content>",
			},
		},
		{
			name:         "DefaultTemplates",
			namespace:    "unknown",
			kind:         "Service",
			resourceName: "unknown",
			existingTemplates: map[string]string{
				"hawking_prediction_virtualService": "<template-content>",
				"default_default_virtualService":    "<template-content>",
				"default_default_destinationRule":   "<template-content>",
			},
			expectedTemplates: map[string]string{
				"default_default_virtualService":  "<template-content>",
				"default_default_destinationRule": "<template-content>",
			},
		},
		{
			name:         "ServiceEntryDefaultTemplate",
			namespace:    "unknown",
			kind:         "ServiceEntry",
			resourceName: "whatever",
			existingTemplates: map[string]string{
				"default_default_virtualService":          "<template-content>",
				"default_external-service_virtualService": "<template-content>",
			},
			expectedTemplates: map[string]string{
				"default_external-service_virtualService": "<template-content>",
			},
		},
		{
			name:                 "Multicluster-specific template exists",
			namespace:            "hawking",
			resourceName:         "prediction",
			kind:                 "Service",
			clusterTrafficPolicy: clusterTrafficPolicy,
			existingTemplates: map[string]string{
				"hawking_prediction_vs":              "<template-content>",
				"hawking_multicluster-prediction_vs": "<template-content>", // multicluster specific template
				"default_multicluster-default_vs":    "<template-content>",
				"default_default_vs":                 "<template-content>",
			},
			expectedTemplates: map[string]string{
				"hawking_multicluster-prediction_vs": "<template-content>",
			},
		},
		{
			name:                 "Multicluster-specific template doesn't exist",
			namespace:            "hawking",
			resourceName:         "prediction",
			kind:                 "Service",
			clusterTrafficPolicy: clusterTrafficPolicy,
			existingTemplates: map[string]string{
				"hawking_prediction_vs":           "<template-content>",
				"default_multicluster-default_vs": "<template-content>",
				"default_default_vs":              "<template-content>",
			},
			expectedTemplates: map[string]string{
				"hawking_prediction_vs": "<template-content>",
			},
		},
		{
			name:                 "Multicluster-default template exists",
			namespace:            "hawking",
			resourceName:         "prediction",
			kind:                 "Service",
			clusterTrafficPolicy: clusterTrafficPolicy,
			existingTemplates: map[string]string{
				"default_multicluster-default_vs": "<template-content>",
				"default_default_vs":              "<template-content>",
			},
			expectedTemplates: map[string]string{
				"default_multicluster-default_vs": "<template-content>",
			},
		},
		{
			name:                 "Multicluster-default template doesn't exist",
			namespace:            "hawking",
			resourceName:         "prediction",
			kind:                 "Service",
			clusterTrafficPolicy: clusterTrafficPolicy,
			existingTemplates: map[string]string{
				"default_default_vs": "<template-content>",
			},
			expectedTemplates: map[string]string{
				"default_default_vs": "<template-content>",
			},
		},
		{
			name:         "CTP does not exist",
			namespace:    "hawking",
			resourceName: "prediction",
			kind:         "Service",
			existingTemplates: map[string]string{
				"hawking_prediction_vs":              "<template-content>",
				"hawking_multicluster-prediction_vs": "<template-content>",
				"default_multicluster-default_vs":    "<template-content>",
				"default_default_vs":                 "<template-content>",
			},
			expectedTemplates: map[string]string{
				"hawking_prediction_vs": "<template-content>",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			metricsRegistry := prometheus.NewRegistry()
			manager := TestableTemplateManager{templates: tc.existingTemplates}
			selector := NewTemplateSelector(zaptest.NewLogger(t).Sugar(), &manager, []string{}, defaultTemplateType, metricsRegistry)

			contextObject := unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": tc.kind,
					"metadata": map[string]string{
						"namespace": tc.namespace,
						"name":      tc.resourceName,
					},
				},
			}
			policies := map[string]*unstructured.Unstructured{
				constants.ClusterTrafficPolicyName: tc.clusterTrafficPolicy,
			}
			ctx := NewRenderRequestContext(&contextObject, map[string]string{}, nil, "", "", nil, policies)
			templates, _ := selector.GetTemplatesOrDefault(&ctx, tc.namespace, tc.resourceName, tc.kind)

			assert.Equal(t, tc.expectedTemplates, templates)

		})
	}
}

func TestGetTemplateForService(t *testing.T) {
	var templateOverrideAnnotationAsStr = common.GetStringForAttribute(constants.TemplateOverrideAnnotation)

	testCases := []struct {
		name                   string
		templateSelectorLabels []string
		namespace              string
		serviceName            string
		instanceLabel          string
		appLabel               string

		annotationOverride string
		mopOverride        string

		existingTemplates map[string]string

		expectedTemplateNamespace string
		expectedTemplateName      string
		expectedError             string

		metadata map[string]string

		clusterTrafficPolicy *unstructured.Unstructured
	}{
		{
			name:                      "NoOverride",
			namespace:                 "hawking",
			serviceName:               "prediction",
			expectedTemplateNamespace: "hawking",
			expectedTemplateName:      "prediction",
		},
		{
			name:                      "WithAnnotationOverride",
			namespace:                 "hawking",
			serviceName:               "prediction",
			annotationOverride:        "einstein/templates",
			expectedTemplateNamespace: "einstein",
			expectedTemplateName:      "templates",
		},
		{
			name:               "InvalidAnnotationOverride",
			namespace:          "hawking",
			serviceName:        "prediction",
			annotationOverride: "einstein/bad/templates",
			expectedError:      "value was \"einstein/bad/templates\" but it is expected to be given as \"<namespace>/<name>\"",
		},
		{
			name:                      "WithAppLabelMatch",
			templateSelectorLabels:    []string{"app", "app_instance"},
			namespace:                 "hawking",
			serviceName:               "prediction",
			appLabel:                  "einstein-prediction",
			instanceLabel:             "einstein-prediction",
			existingTemplates:         map[string]string{"hawking_einstein-prediction_vs": "<template-content>"},
			expectedTemplateNamespace: "hawking",
			expectedTemplateName:      "einstein-prediction",
		},
		{
			name:                      "WithInstanceLabelMatch",
			templateSelectorLabels:    []string{"app", "app_instance"},
			namespace:                 "hawking",
			serviceName:               "prediction",
			instanceLabel:             "platform-prediction",
			existingTemplates:         map[string]string{"hawking_platform-prediction_vs": "<template-content>"},
			expectedTemplateNamespace: "hawking",
			expectedTemplateName:      "platform-prediction",
		},
		{
			name:                      "NoTemplateSelectorLabels (Fallback to namespace/servicename)",
			templateSelectorLabels:    []string{},
			namespace:                 "hawking",
			serviceName:               "prediction",
			appLabel:                  "einstein-prediction",
			instanceLabel:             "platform-prediction",
			existingTemplates:         map[string]string{"hawking_einstein-prediction_vs": "<template-content>"},
			expectedTemplateNamespace: "hawking",
			expectedTemplateName:      "prediction",
		},
		{
			name:                      "WithAppLabelMatchAndAnnotationOverride - override wins",
			templateSelectorLabels:    []string{"app", "app_instance"},
			namespace:                 "hawking",
			serviceName:               "prediction",
			appLabel:                  "einstein-prediction",
			instanceLabel:             "einstein-prediction",
			annotationOverride:        "einstein/templates",
			existingTemplates:         map[string]string{"hawking_einstein-prediction_vs": "<template-content>", "hawking_platform-prediction_vs": "<template-content>"},
			expectedTemplateNamespace: "einstein",
			expectedTemplateName:      "templates",
		},
		{
			name:                      "MetadataOfNoUse",
			namespace:                 "test-namespace",
			serviceName:               "test-svc",
			expectedTemplateNamespace: "test-namespace",
			expectedTemplateName:      "test-svc",
			metadata:                  map[string]string{"test": "true"},
		},
		{
			name:          "MopOverride_InvalidFormat",
			mopOverride:   "not/valid/override",
			expectedError: "invalid template override provided not/valid/override",
		},
		{
			name:          "MopOverride_NonExistentTemplate",
			mopOverride:   "my/override",
			expectedError: "template override doesn't exist: my/override",
		},
		{
			name:        "MopOverride",
			mopOverride: "my/override",
			existingTemplates: map[string]string{
				"my_override_vs":     "<template-content>",
				"default_default_vs": "<template-content>",
			},
			expectedTemplateNamespace: "my",
			expectedTemplateName:      "override",
		},
	}

	metricsRegistry := prometheus.NewRegistry()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := TestableTemplateManager{templates: tc.existingTemplates}
			selector := NewTemplateSelector(zaptest.NewLogger(t).Sugar(), &manager, tc.templateSelectorLabels, defaultTemplateType, metricsRegistry)

			service := kube_test.CreateServiceAsUnstructuredObject(tc.namespace, tc.serviceName)
			service.SetAnnotations(map[string]string{templateOverrideAnnotationAsStr: tc.annotationOverride})
			service.SetLabels(map[string]string{
				"app":          tc.appLabel,
				"app_instance": tc.instanceLabel,
			})

			policies := map[string]*unstructured.Unstructured{
				constants.ClusterTrafficPolicyName: tc.clusterTrafficPolicy,
			}
			ctx := NewRenderRequestContext(service, tc.metadata, nil, "cluster1", tc.mopOverride, nil, policies)

			tNamespace, tService, err := selector.GetTemplate(&ctx)

			if tc.expectedError != "" {
				assert.Error(t, err, "Error expected")
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tc.expectedTemplateNamespace, tNamespace)
				assert.Equal(t, tc.expectedTemplateName, tService)
			}
		})
	}
}

type TestableTemplateManager struct {
	templates          map[string]string
	templatesRequested []string
	templateMetadata   map[string]*TemplateMetadata
}

func (m *TestableTemplateManager) Exists(prefix string) bool {
	return len(m.GetTemplatesByPrefix(prefix)) > 0
}

func (m *TestableTemplateManager) GetTemplatePaths() []string {
	return []string{}
}

func (m *TestableTemplateManager) GetTemplatesByPrefix(prefix string) map[string]string {
	m.templatesRequested = append(m.templatesRequested, prefix)
	templates := make(map[string]string)
	for key, val := range m.templates {
		if strings.HasPrefix(key, prefix) {
			templates[key] = val
		}
	}
	return templates
}

func (m *TestableTemplateManager) GetTemplateMetadata(templateKey string) *TemplateMetadata {
	internalKey := strings.ReplaceAll(templateKey, constants.TemplateNameSeparator, TemplateKeyDelimiter)
	if m.templateMetadata != nil {
		return m.templateMetadata[internalKey]
	}
	return nil
}
