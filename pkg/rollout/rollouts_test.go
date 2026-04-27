package rollout

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
)

var templateOverrideAnnotation = common.GetStringForAttribute(constants.TemplateOverrideAnnotation)

func TestGetServiceKeyFromRollout(t *testing.T) {
	namespace := "test-namespace"
	testCases := []struct {
		name               string
		rolloutObject      *unstructured.Unstructured
		expectedServiceKey []string
	}{
		{
			name:               "RetrieveKey",
			rolloutObject:      kube_test.NewRolloutBuilder("test", namespace).SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: "activeSvc"}).SetTemplateLabel(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build(),
			expectedServiceKey: []string{"test-namespace/activeSvc"},
		},
		{
			name:               "MissingActiveServiceAnnotation",
			rolloutObject:      kube_test.NewRolloutBuilder("test", namespace).SetTemplateAnnotations(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build(),
			expectedServiceKey: nil,
		},
		{
			name:               "EmptyActiveServiceAnnotation",
			rolloutObject:      kube_test.NewRolloutBuilder("test", namespace).SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: ""}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build(),
			expectedServiceKey: nil,
		},
	}

	for _, tc := range testCases {
		serviceKey := getServiceKeyFromRollout(tc.rolloutObject)
		assert.Equal(t, serviceKey, tc.expectedServiceKey)
	}
}

func TestGetRolloutsByService(t *testing.T) {
	rolloutObj1 := kube_test.NewRolloutBuilder("rollout1", "namespace1").SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: "activeSvc"}).SetTemplateLabel(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build()
	rolloutObj2 := kube_test.NewRolloutBuilder("rollout2", "namespace2").SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: "activeSvc"}).SetTemplateLabel(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build()
	rolloutObj3 := kube_test.NewRolloutBuilder("rollout3", "namespace1").SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: "activeSvc"}).SetTemplateLabel(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build()

	testCases := []struct {
		name                            string
		serviceName                     string
		serviceNamespace                string
		rolloutObjsInCluster            []runtime.Object
		expectedRolloutObjsByServiceKey []*unstructured.Unstructured
		errExpected                     string
	}{
		{
			name:                            "FoundOneRollout",
			serviceName:                     "activeSvc",
			serviceNamespace:                "namespace1",
			rolloutObjsInCluster:            []runtime.Object{rolloutObj1, rolloutObj2},
			expectedRolloutObjsByServiceKey: []*unstructured.Unstructured{rolloutObj1},
			errExpected:                     "",
		},
		{
			name:                            "FoundTwoRollout",
			serviceName:                     "activeSvc",
			serviceNamespace:                "namespace1",
			rolloutObjsInCluster:            []runtime.Object{rolloutObj1, rolloutObj2, rolloutObj3},
			expectedRolloutObjsByServiceKey: []*unstructured.Unstructured{rolloutObj1, rolloutObj3},
			errExpected:                     "",
		},
		{
			name:                            "IndexError",
			serviceName:                     "activeSvc",
			serviceNamespace:                "namespace1",
			rolloutObjsInCluster:            []runtime.Object{rolloutObj1, rolloutObj2},
			expectedRolloutObjsByServiceKey: nil,
			errExpected:                     "Index with name byServiceName does not exist",
		},
		{
			name:                            "NoRolloutObjFound",
			serviceName:                     "testsvc",
			serviceNamespace:                "namespace1",
			rolloutObjsInCluster:            []runtime.Object{rolloutObj1, rolloutObj2, rolloutObj3},
			expectedRolloutObjsByServiceKey: nil,
			errExpected:                     "",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {

		listMapping := map[schema.GroupVersionResource]string{
			constants.RolloutResource: "rolloutsList",
		}
		fakeClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listMapping, tc.rolloutObjsInCluster...)
		dynamicInformerFactory := dynamicinformer.NewDynamicSharedInformerFactory(fakeClient, 0)

		// register Rollout GVR
		rolloutInformer := dynamicInformerFactory.ForResource(constants.RolloutResource)

		if tc.errExpected == "" {
			// add indexer
			err := AddServiceNameToRolloutIndexer(rolloutInformer.Informer())
			assert.Nil(t, err)
		}

		go rolloutInformer.Informer().Run(ctx.Done())
		cache.WaitForCacheSync(ctx.Done(), rolloutInformer.Informer().HasSynced)

		rolloutObjsRetrieved, err := GetRolloutsByService(rolloutInformer.Informer(), tc.serviceNamespace, tc.serviceName)
		if tc.errExpected != "" {
			assert.Error(t, err, tc.errExpected)
		}
		assert.ElementsMatch(t, tc.expectedRolloutObjsByServiceKey, rolloutObjsRetrieved)
	}
}

func TestGetReMutationTemplate(t *testing.T) {
	bgService := kube_test.NewServiceBuilder("test-svc", "ns").
		SetAnnotations(map[string]string{templateOverrideAnnotation: "default/bg-stateless"}).Build()
	canaryService := kube_test.NewServiceBuilder("test-svc", "ns").
		SetAnnotations(map[string]string{templateOverrideAnnotation: "default/canary-stateless"}).Build()
	coreappService := kube_test.NewServiceBuilder("test-svc", "ns").
		SetAnnotations(map[string]string{templateOverrideAnnotation: "core-on-sam/coreapp-argo-bg"}).Build()
	unknownService := kube_test.NewServiceBuilder("test-svc", "ns").
		SetAnnotations(map[string]string{templateOverrideAnnotation: "unknown/template"}).Build()

	testCases := []struct {
		name                       string
		service                    *corev1.Service
		metadata                   map[string]*templating.TemplateMetadata
		featureEnabled             bool
		expectedManaged            bool
		expectedRemutationTemplate string
	}{
		{
			name:                       "templateMetadata disabled - bg-stateless template",
			service:                    &bgService,
			featureEnabled:             false,
			expectedManaged:            true,
			expectedRemutationTemplate: StrategyReMutateBG,
		},
		{
			name:                       "templateMetadata disabled - canary-stateless template",
			service:                    &canaryService,
			featureEnabled:             false,
			expectedManaged:            true,
			expectedRemutationTemplate: StrategyReMutateCanary,
		},
		{
			name:    "templateMetadata enabled - has reMutationTemplate",
			service: &coreappService,
			metadata: map[string]*templating.TemplateMetadata{
				"core-on-sam_coreapp-argo-bg": {
					Rollout: &templating.RolloutMetadata{
						MutationTemplate:   "coreappBlueGreen",
						ReMutationTemplate: "coreappReMutate",
					},
				},
			},
			featureEnabled:             true,
			expectedManaged:            true,
			expectedRemutationTemplate: "coreappReMutate",
		},
		{
			name:                       "templateMetadata enabled - no metadata found",
			service:                    &unknownService,
			metadata:                   map[string]*templating.TemplateMetadata{},
			featureEnabled:             true,
			expectedManaged:            false,
			expectedRemutationTemplate: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set feature flag
			originalValue := features.EnableTemplateMetadata
			features.EnableTemplateMetadata = tc.featureEnabled
			defer func() { features.EnableTemplateMetadata = originalValue }()

			manager := &mockTemplateManager{metadata: tc.metadata}
			managed, remutationTemplate := GetReMutationTemplate(manager, tc.service)

			assert.Equal(t, tc.expectedManaged, managed)
			assert.Equal(t, tc.expectedRemutationTemplate, remutationTemplate)
		})
	}
}

func TestGetMutationTemplate(t *testing.T) {
	bgService := kube_test.NewServiceBuilder("test-svc", "ns").
		SetAnnotations(map[string]string{templateOverrideAnnotation: "default/bg-stateless"}).Build()
	canaryService := kube_test.NewServiceBuilder("test-svc", "ns").
		SetAnnotations(map[string]string{templateOverrideAnnotation: "default/canary-stateless"}).Build()
	coreappService := kube_test.NewServiceBuilder("test-svc", "ns").
		SetAnnotations(map[string]string{templateOverrideAnnotation: "core-on-sam/coreapp-argo-bg"}).Build()
	unknownService := kube_test.NewServiceBuilder("test-svc", "ns").
		SetAnnotations(map[string]string{templateOverrideAnnotation: "unknown/template"}).Build()

	testCases := []struct {
		name                     string
		service                  *corev1.Service
		metadata                 map[string]*templating.TemplateMetadata
		featureEnabled           bool
		expectedManaged          bool
		expectedMutationTemplate string
	}{
		{
			name:                     "templateMetadata disabled - bg-stateless template",
			service:                  &bgService,
			featureEnabled:           false,
			expectedManaged:          true,
			expectedMutationTemplate: StrategyLabelBG,
		},
		{
			name:                     "templateMetadata disabled - canary-stateless template",
			service:                  &canaryService,
			featureEnabled:           false,
			expectedManaged:          true,
			expectedMutationTemplate: StrategyLabelCanary,
		},
		{
			name:    "templateMetadata enabled - has mutationTemplate",
			service: &coreappService,
			metadata: map[string]*templating.TemplateMetadata{
				"core-on-sam_coreapp-argo-bg": {
					Rollout: &templating.RolloutMetadata{
						MutationTemplate:   "coreappBlueGreen",
						ReMutationTemplate: "coreappReMutate",
					},
				},
			},
			featureEnabled:           true,
			expectedManaged:          true,
			expectedMutationTemplate: "coreappBlueGreen",
		},
		{
			name:                     "templateMetadata enabled - no metadata found",
			service:                  &unknownService,
			metadata:                 map[string]*templating.TemplateMetadata{},
			featureEnabled:           true,
			expectedManaged:          false,
			expectedMutationTemplate: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			originalValue := features.EnableTemplateMetadata
			features.EnableTemplateMetadata = tc.featureEnabled
			defer func() { features.EnableTemplateMetadata = originalValue }()

			manager := &mockTemplateManager{metadata: tc.metadata}
			managed, mutationTemplate := GetMutationTemplate(manager, tc.service)

			assert.Equal(t, tc.expectedManaged, managed)
			assert.Equal(t, tc.expectedMutationTemplate, mutationTemplate)
		})
	}
}

func TestHasRolloutMetadata(t *testing.T) {
	testCases := []struct {
		name        string
		templateKey string
		metadata    map[string]*templating.TemplateMetadata
		expected    bool
	}{
		{
			name:        "has both templates",
			templateKey: "default/bg-stateless",
			metadata: map[string]*templating.TemplateMetadata{
				"default_bg-stateless": {
					Rollout: &templating.RolloutMetadata{
						MutationTemplate:   "blueGreen",
						ReMutationTemplate: "reMutateBlueGreen",
					},
				},
			},
			expected: true,
		},
		{
			name:        "has mutation template only",
			templateKey: "default/bg-stateless",
			metadata: map[string]*templating.TemplateMetadata{
				"default_bg-stateless": {
					Rollout: &templating.RolloutMetadata{
						MutationTemplate: "blueGreen",
					},
				},
			},
			expected: false,
		},
		{
			name:        "has reMutation template only",
			templateKey: "default/bg-stateless",
			metadata: map[string]*templating.TemplateMetadata{
				"default_bg-stateless": {
					Rollout: &templating.RolloutMetadata{
						ReMutationTemplate: "reMutateBlueGreen",
					},
				},
			},
			expected: false,
		},
		{
			name:        "empty rollout metadata",
			templateKey: "default/bg-stateless",
			metadata: map[string]*templating.TemplateMetadata{
				"default_bg-stateless": {
					Rollout: &templating.RolloutMetadata{},
				},
			},
			expected: false,
		},
		{
			name:        "no metadata for template",
			templateKey: "unknown/template",
			metadata:    map[string]*templating.TemplateMetadata{},
			expected:    false,
		},
		{
			name:        "empty template key",
			templateKey: "",
			metadata:    map[string]*templating.TemplateMetadata{},
			expected:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := &mockTemplateManager{metadata: tc.metadata}
			result := HasRolloutMetadata(manager, tc.templateKey)
			assert.Equal(t, tc.expected, result)
		})
	}
}

type mockTemplateManager struct {
	metadata map[string]*templating.TemplateMetadata
}

func (m *mockTemplateManager) GetTemplatesByPrefix(_ string) map[string]string {
	return map[string]string{}
}

func (m *mockTemplateManager) Exists(_ string) bool {
	return true
}

func (m *mockTemplateManager) GetTemplatePaths() []string {
	return []string{}
}

func (m *mockTemplateManager) GetTemplateMetadata(templateKey string) *templating.TemplateMetadata {
	internalKey := strings.ReplaceAll(templateKey, constants.TemplateNameSeparator, templating.TemplateKeyDelimiter)
	if m.metadata != nil {
		return m.metadata[internalKey]
	}
	return nil
}
