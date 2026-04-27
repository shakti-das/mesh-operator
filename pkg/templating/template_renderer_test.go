package templating

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"

	"github.com/joeyb/goldenfiles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	realTemplatesPath = "projects/services/servicemesh/mesh-operator/pkg/testdata/test-templates"
)

var (
	defaultServiceName = "service-name"
	defaultMsmOwnerRef = &metav1.OwnerReference{
		APIVersion: v1alpha1.ApiVersion,
		Kind:       v1alpha1.MsmKind.Kind,
		Name:       defaultServiceName,
	}
	defaultMopOwnerRef = &metav1.OwnerReference{
		APIVersion: v1alpha1.ApiVersion,
		Kind:       v1alpha1.MopKind.Kind,
		Name:       "mop-owner-ref",
	}
	defaultTemplateType = map[string]string{"Service": "default/default", "ServiceEntry": "default/external-service", "TrafficShardingPolicy": "default/trafficsharding"}
	defaultFaultFilter  = v1alpha1.ExtensionElement{FaultFilter: &v1alpha1.HttpFaultFilter{
		Abort: &v1alpha1.HttpFaultFilterAbort{HttpStatus: kube_test.DefaultAbortHttpStatus}}}
)

func TestTemplateRenderer(t *testing.T) {

	svcWithTargetPortAsString := kube_test.NewServiceBuilder("my-service-name", "remote-namespace").SetType("ClusterIP").SetPorts([]v1.ServicePort{{Name: "http", Port: 7442, TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "whatever"}}}).GetServiceAsUnstructuredObject()
	svcWithTargetPortAsStringContext := NewTestContextBuilder(svcWithTargetPortAsString).build()

	svcWithTargetPortAsStringLbType := kube_test.NewServiceBuilder("my-service-name", "test-namespace").SetType(constants.LBServiceType).SetPorts([]v1.ServicePort{{Name: "http", Port: 7442, TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "whatever"}}}).GetServiceAsUnstructuredObject()
	svcWithTargetPortAsStringLbTypeContext := NewTestContextBuilder(svcWithTargetPortAsStringLbType).build()

	primarySvc := kube_test.NewServiceBuilder("my-service-name", "test-namespace").SetPorts([]v1.ServicePort{{Name: "http", Port: 7442}}).SetLabels(map[string]string{"app": "my-app", "app_instance": "my-identity"}).GetServiceAsUnstructuredObject()
	remoteSvc := kube_test.NewServiceBuilder("my-service-name", "remote-namespace").SetPorts([]v1.ServicePort{{Name: "http", Port: 7442}}).SetLabels(map[string]string{"app": "my-app", "app_instance": "my-identity"}).GetServiceAsUnstructuredObject()

	se := kube_test.NewServiceEntryBuilder("my-se", "test-namespace").GetServiceEntryAsUnstructuredObject()

	nilServiceContext := NewRenderRequestContext(nil, map[string]string{}, defaultMsmOwnerRef, "cluster1", "", nil, nil)
	workspacePath := os.Getenv("WORKSPACE_PATH") // required for bazel to write goldfiles back to source
	goldenfiles.GoldenFilePath = workspacePath + "projects/services/servicemesh/mesh-operator/pkg/testdata/TestTemplateRenderer/"

	testCases := []struct {
		name                     string
		remoteClusterEvent       bool
		templatesPath            string
		requestContext           *RenderRequestContext
		expectedBaseTemplateType string
		expectedErr              string
	}{
		{
			name:               "RenderDefaultTemplatesForPrimaryClusterEvent",
			remoteClusterEvent: false,
			templatesPath:      realTemplatesPath,
			requestContext: NewTestContextBuilder(primarySvc).
				setOwner(&metav1.OwnerReference{
					APIVersion: v1alpha1.ApiVersion,
					Kind:       v1alpha1.MsmKind.Kind,
					Name:       "my-service-name-error",
					UID:        "bb515f8c-aec4-4e45-94b0-eb47b1abacc4",
				}).build(),
			expectedBaseTemplateType: "default_default",
		},
		{
			name:               "RenderDefaultTemplatesForRemoteClusterEvent",
			remoteClusterEvent: true,
			templatesPath:      realTemplatesPath,
			requestContext: NewTestContextBuilder(remoteSvc).
				setOwner(&metav1.OwnerReference{
					APIVersion: v1alpha1.ApiVersion,
					Kind:       v1alpha1.MsmKind.Kind,
					Name:       "my-service-name",
					UID:        "bb515f8c-aec4-4e45-94b0-eb47b1abacc4",
				}).build(),
			expectedBaseTemplateType: "default_default",
		},
		{
			name:                     "RenderServiceEntryForPrimaryClusterEvent",
			remoteClusterEvent:       false,
			templatesPath:            realTemplatesPath,
			requestContext:           NewTestContextBuilder(se).build(),
			expectedBaseTemplateType: "default_external-service",
		},
		{
			name:                     "ServiceReferenceTargetPortOfTypeString",
			remoteClusterEvent:       true,
			templatesPath:            realTemplatesPath,
			requestContext:           svcWithTargetPortAsStringContext,
			expectedBaseTemplateType: "",
			expectedErr:              "unable to generate mesh routing config, please provide exact integer port as targetPort",
		},
		{
			name:                     "LBTypeServiceWithStringTargetPort",
			templatesPath:            realTemplatesPath,
			requestContext:           svcWithTargetPortAsStringLbTypeContext,
			expectedBaseTemplateType: "default_default",
		},
		{
			name:          "MissingService",
			templatesPath: realTemplatesPath,
			expectedErr:   "request context must not be nil",
		},
		{
			name:           "NilServiceInContext",
			templatesPath:  realTemplatesPath,
			requestContext: &nilServiceContext,
			expectedErr:    "both MOP and object cannot be nil, either one of them needs to be passed",
		},
	}

	metricsRegistry := prometheus.NewRegistry()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t).Sugar()
			manager := filesystemTemplatesManager{
				logger:        zaptest.NewLogger(t).Sugar(),
				templatePaths: []string{tc.templatesPath},
				cacheMutex:    &sync.RWMutex{},
				fs:            &Os{},
			}
			_, err := manager.reloadTemplates()
			if err != nil {
				panic(fmt.Sprintf("Template renderer test cannot load templates manager, quitting. %v", err))
			}

			templateSelector := NewTemplateSelector(logger, &manager, []string{"app", "app_instance"}, defaultTemplateType, metricsRegistry)
			tr := NewTemplateRenderer(logger, []string{tc.templatesPath}, templateSelector)
			output, err := tr.Render(tc.requestContext)

			if tc.expectedErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedBaseTemplateType, output.TemplateType)
				for name, objects := range output.Config {
					goldenfiles.EqualString(t, objectsToGoldFileText(t, objects), goldenfiles.Config{Name: name, Suffix: ".json"})
				}
			} else {
				assert.EqualError(t, err, tc.expectedErr)
			}
		})
	}
}

func TestGetVM(t *testing.T) {

	service := kube_test.CreateServiceAsUnstructuredObject("test-ns", "testsvc")
	clusterTrafficPolicy := &unstructured.Unstructured{}

	testCases := []struct {
		name           string
		requestContext *RenderRequestContext
	}{
		{
			name: "PopulateTypeMetaField",
			requestContext: &RenderRequestContext{
				Object:   service,
				Metadata: map[string]string{},
				policies: map[string]*unstructured.Unstructured{
					constants.ClusterTrafficPolicyName: clusterTrafficPolicy,
				}},
		},
	}
	metricsRegistry := prometheus.NewRegistry()

	for _, tc := range testCases {

		logger := zaptest.NewLogger(t).Sugar()
		manager := filesystemTemplatesManager{}
		template := "function(service, context) [{apiVersion: service.apiVersion, kind: service.kind,}]"
		templateSelector := NewTemplateSelector(logger, &manager, []string{}, defaultTemplateType, metricsRegistry)
		renderer := templateRenderer{[]string{realTemplatesPath}, logger, templateSelector}

		vm, err := renderer.getVM(tc.requestContext)
		assert.Nil(t, err)

		renderedtemplate, err := vm.EvaluateAnonymousSnippet("dummy.jsonnet", template)

		assert.Nil(t, err)

		assert.True(t, strings.Contains(renderedtemplate, "v1"))
		assert.True(t, strings.Contains(renderedtemplate, "Service"))
	}
}

func TestRenderRequestContextGetObjectName(t *testing.T) {
	service := kube_test.CreateServiceAsUnstructuredObject("test-namespace", "service-name")
	metadata := map[string]string{}
	ctx := NewRenderRequestContext(service, metadata, defaultMsmOwnerRef, "cluster1", "", nil, nil)

	assert.Equal(t, metadata, ctx.Metadata)
	assert.Equal(t, service, ctx.Object)
}

func TestRenderMeshOperator(t *testing.T) {
	service := kube_test.CreateServiceAsUnstructuredObject("test-ns", "svc")
	mop := kube_test.NewMopBuilder("testns", "mop").AddFilter(defaultFaultFilter).Build()
	object := map[string]interface{}{
		"someKey": map[string]string{"objectField": "some-value"}, // Not a real k8s object, but good enough for testing
	}
	additionalObjects := map[string]*AdditionalObjects{
		"faultInjection": {
			Singletons: object,
		},
		"activeHealthCheck": {
			Singletons: object,
		},
	}
	metadata := map[string]string{}
	clusterName := "primary"
	context := NewMOPRenderRequestContext(service, mop, metadata, clusterName, defaultMsmOwnerRef, additionalObjects)
	context.OwnerRef = &metav1.OwnerReference{
		APIVersion: v1alpha1.ApiVersion,
		Kind:       v1alpha1.MopKind.Kind,
		Name:       "owner-mop",
		UID:        "bb515f8c-aec4-4e45-94b0-eb47b1abacc4",
	}
	metricsRegistry := prometheus.NewRegistry()

	logger := zaptest.NewLogger(t).Sugar()
	template := "function(mop, filter, service=null, context) [{apiVersion: \"networking.istio.io/v1alpha3\", kind: \"EnvoyFilter\", additionalValue: context.additionalObjects.singletons.someKey.objectField}]"
	manager := filesystemTemplatesManager{
		templatePaths: []string{"doesnt-matter"},
		templates: map[string]string{
			"filters_faultinjection":    template,
			"filters_activehealthcheck": template,
		},
		cacheMutex: &sync.RWMutex{}}
	templateSelector := NewTemplateSelector(logger, &manager, []string{}, defaultTemplateType, metricsRegistry)
	renderer := templateRenderer{logger: logger, templateSelector: templateSelector}

	otherObjectTemplate := "function(mop, filter, service=null, context) [{apiVersion: \"networking.istio.io/v1alpha3\", kind: \"VirtualService\", additionalValue: context.additionalObjects.singletons.someKey.objectField}]"
	managerWithMultiObjectTemplates := filesystemTemplatesManager{
		templatePaths: []string{"doesnt-matter"},
		templates: map[string]string{
			"filters_faultinjection_ef":    template,
			"filters_faultinjection_vs":    otherObjectTemplate,
			"filters_activehealthcheck_ef": template,
			"filters_activehealthcheck_vs": otherObjectTemplate,
		},
		cacheMutex: &sync.RWMutex{}}
	multiobjectTemplateSelector := NewTemplateSelector(logger, &managerWithMultiObjectTemplates, []string{}, defaultTemplateType, metricsRegistry)
	multiobjectRenderer := templateRenderer{logger: logger, templateSelector: multiobjectTemplateSelector}

	emptyResultTemplate := "function(mop, filter, service=null, context) []"
	managerWithEmptyOutput := filesystemTemplatesManager{
		templatePaths: []string{"doesnt-matter"},
		templates: map[string]string{
			"filters_faultinjection":    emptyResultTemplate,
			"filters_activehealthcheck": emptyResultTemplate,
		},
		cacheMutex: &sync.RWMutex{}}
	emptyResultTemplateSelector := NewTemplateSelector(logger, &managerWithEmptyOutput, []string{}, defaultTemplateType, metricsRegistry)
	emptyResultRenderer := templateRenderer{logger: logger, templateSelector: emptyResultTemplateSelector}

	configNsExtensionMop := kube_test.NewMopBuilder("testns", "mop").
		AddFilter(v1alpha1.ExtensionElement{ActiveHealthCheckFilter: &v1alpha1.ActiveHealthCheck{}}).
		Build()
	configNsContext := NewMOPRenderRequestContext(service, configNsExtensionMop, metadata, clusterName, defaultMsmOwnerRef, additionalObjects)

	filter := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion":      "networking.istio.io/v1alpha3",
			"kind":            "EnvoyFilter",
			"additionalValue": "some-value",
			"metadata": map[string]interface{}{
				"name": "filter-0",
			},
		},
	}

	multiobjectVs := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion":      "networking.istio.io/v1alpha3",
			"kind":            "VirtualService",
			"additionalValue": "some-value",
			"metadata": map[string]interface{}{
				"name": "filter-0.1",
			},
		},
	}

	configNsFilter := filter.DeepCopy()
	configNsFilter.SetAnnotations(map[string]string{"mesh.io/configNamespaceExtension": constants.DefaultConfigNamespace})

	configNsVs := multiobjectVs.DeepCopy()
	configNsVs.SetAnnotations(map[string]string{"mesh.io/configNamespaceExtension": constants.DefaultConfigNamespace})

	testCases := []struct {
		name                 string
		renderContext        RenderRequestContext
		renderer             TemplateRenderer
		expectedRenderResult map[string][]*unstructured.Unstructured
	}{
		{
			name:          "ClusterMopEvent",
			renderContext: context,
			renderer:      &renderer,
			expectedRenderResult: map[string][]*unstructured.Unstructured{
				"0": {filter},
			},
		},
		{
			name:          "ClusterMopEvent - multi-object",
			renderContext: context,
			renderer:      &multiobjectRenderer,
			expectedRenderResult: map[string][]*unstructured.Unstructured{
				"0": {filter, multiobjectVs},
			},
		},
		{
			name:          "ConfigNamespace - ClusterMopEvent",
			renderContext: configNsContext,
			renderer:      &renderer,
			expectedRenderResult: map[string][]*unstructured.Unstructured{
				"0": {configNsFilter},
			},
		},
		{
			name:          "ConfigNamespace - ClusterMopEvent: multi-object",
			renderContext: configNsContext,
			renderer:      &multiobjectRenderer,
			expectedRenderResult: map[string][]*unstructured.Unstructured{
				"0": {configNsFilter, configNsVs},
			},
		},
		{
			name:          "Empty result",
			renderContext: context,
			renderer:      &emptyResultRenderer,
			expectedRenderResult: map[string][]*unstructured.Unstructured{
				"0": []*unstructured.Unstructured{},
			},
		},
		{
			name:          "ConfigNamespace - Empty result",
			renderContext: configNsContext,
			renderer:      &emptyResultRenderer,
			expectedRenderResult: map[string][]*unstructured.Unstructured{
				"0": []*unstructured.Unstructured{},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.renderContext
			renderedTemplates, err := tc.renderer.Render(&ctx)
			assert.Nil(t, err)

			if tc.expectedRenderResult == nil {
				assert.Nil(t, renderedTemplates)
			} else {
				assert.EqualValues(t, tc.expectedRenderResult, renderedTemplates.Config)
			}
		})
	}
}

func TestRenderFilter(t *testing.T) {
	filter := v1alpha1.ExtensionElement{
		FaultFilter: &v1alpha1.HttpFaultFilter{Abort: &v1alpha1.HttpFaultFilterAbort{
			HttpStatus: kube_test.DefaultAbortHttpStatus}},
	}
	service := kube_test.CreateServiceAsUnstructuredObject("test-ns", "svc")
	mop := kube_test.NewMopBuilder("testns", "mop").Build()

	metadata := map[string]string{}
	clusterName := "primary"
	logger := zaptest.NewLogger(t).Sugar()
	ctx := NewMOPRenderRequestContext(service, mop, metadata, clusterName, defaultMopOwnerRef, nil)
	renderedFilterObject := unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "networking.istio.io/v1alpha3",
		"kind":       "EnvoyFilter",
	}}
	renderedVsObject := unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "networking.istio.io/v1alpha3",
		"kind":       "VirtualService",
	}}

	testCases := []struct {
		name                    string
		context                 *RenderRequestContext
		filter                  v1alpha1.ExtensionElement
		templates               map[string]string
		expectedRenderedObjects []*unstructured.Unstructured
		expectedErrorString     string
	}{
		{
			name:                    "Render",
			context:                 &ctx,
			filter:                  filter,
			templates:               map[string]string{"filters_faultinjection": "function(mop, filter, service=null, context) [{apiVersion: \"networking.istio.io/v1alpha3\", kind: \"EnvoyFilter\",}]"},
			expectedRenderedObjects: []*unstructured.Unstructured{&renderedFilterObject},
		},
		{
			name:                "RenderError",
			context:             &ctx,
			filter:              filter,
			templates:           map[string]string{"filters_faultinjection": "function(mop, filter, service=null, context) [{apiVersion: networking.istio.io/v1alpha3, kind: \"EnvoyFilter\",}]"},
			expectedErrorString: "Unknown variable",
		},
		{
			name:                "ExtractionError",
			context:             &ctx,
			filter:              filter,
			templates:           map[string]string{"filters_faultinjection": "function(mop, filter, service=null, context) {Invalid: \"Object\" }"},
			expectedErrorString: "failed to extract objects from extension template",
		},
		{
			name:    "Render: multi-object extension",
			context: &ctx,
			filter:  filter,
			templates: map[string]string{
				"filters_faultinjection": "function(mop, filter, service=null, context) [{apiVersion: \"networking.istio.io/v1alpha3\", kind: \"EnvoyFilter\",},{apiVersion: \"networking.istio.io/v1alpha3\", kind: \"VirtualService\",}]",
			},
			expectedRenderedObjects: []*unstructured.Unstructured{&renderedFilterObject, &renderedVsObject},
		},
		{
			name:    "Render: multi-file extension",
			context: &ctx,
			filter:  filter,
			templates: map[string]string{
				"filters_faultinjection_ef_vs": "function(mop, filter, service=null, context) [{apiVersion: \"networking.istio.io/v1alpha3\", kind: \"EnvoyFilter\",}]",
				"filters_faultinjection_dr":    "function(mop, filter, service=null, context) [{apiVersion: \"networking.istio.io/v1alpha3\", kind: \"VirtualService\",}]",
			},
			expectedRenderedObjects: []*unstructured.Unstructured{&renderedFilterObject, &renderedVsObject},
		},
		{
			name:    "Render: empty result",
			context: &ctx,
			filter:  filter,
			templates: map[string]string{
				"filters_faultinjection": "function(mop, filter, service=null, context) []",
			},
			expectedRenderedObjects: []*unstructured.Unstructured{},
		},
	}
	metricsRegistry := prometheus.NewRegistry()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := filesystemTemplatesManager{templatePaths: []string{"doesnt-matter"}, templates: tc.templates, cacheMutex: &sync.RWMutex{}}
			templateSelector := NewTemplateSelector(logger, &manager, []string{}, defaultTemplateType, metricsRegistry)
			renderer := templateRenderer{logger: logger, templateSelector: templateSelector}

			_, actualRenderedObjects, err := renderer.renderExtension(tc.context, tc.filter, NewMessageCollector())
			// To avoid tests failing unpredictably due to nondeterministic map order, sort objects by kind and name
			slices.SortFunc(actualRenderedObjects, func(a, b *unstructured.Unstructured) int {
				return cmp.Or(
					strings.Compare(a.GetKind(), b.GetKind()),
					strings.Compare(a.GetName(), b.GetName()),
				)
			})

			if tc.expectedErrorString == "" {
				assert.Nil(t, err)
				assert.Equal(t, tc.expectedRenderedObjects, actualRenderedObjects)
			} else {
				assert.ErrorContains(t, err, tc.expectedErrorString)
			}
		})
	}
}

func TestFilterTemplateSelectionError(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	filter := v1alpha1.ExtensionElement{
		FaultFilter: &v1alpha1.HttpFaultFilter{Abort: &v1alpha1.HttpFaultFilterAbort{
			HttpStatus: kube_test.DefaultAbortHttpStatus}},
	}

	testCases := []struct {
		name              string
		objectKind        string
		templates         map[string]string
		errorExpected     bool
		expectedErrPrefix string
	}{
		{
			name:              "No matching templates (NS level MOP)",
			objectKind:        "",
			templates:         map[string]string{},
			errorExpected:     true,
			expectedErrPrefix: "no templates provided for filter type",
		},
		{
			name:              "No matching templates (Service extension MOP)",
			objectKind:        constants.ServiceKind.Kind,
			templates:         map[string]string{},
			errorExpected:     true,
			expectedErrPrefix: "no templates provided for filter type",
		},
		{
			name:          "No matching templates (Non Service extension MOP)",
			objectKind:    constants.ServiceEntryKind.Kind,
			templates:     map[string]string{},
			errorExpected: false,
		},
	}
	metricsRegistry := prometheus.NewRegistry()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := filesystemTemplatesManager{
				templatePaths: []string{"../testdata/end-to-end-templates"},
				templates:     tc.templates,
				cacheMutex:    &sync.RWMutex{},
			}

			templateSelector := NewTemplateSelector(logger, &manager, []string{}, defaultTemplateType, metricsRegistry)
			renderer := templateRenderer{logger: logger, templateSelector: templateSelector}

			var contextObject *unstructured.Unstructured
			if tc.objectKind != "" {
				contextObject = &unstructured.Unstructured{}
				contextObject.SetKind(tc.objectKind)
			}
			_, renderedExtension, err := renderer.renderExtension(&RenderRequestContext{Object: contextObject}, filter, NewMessageCollector())

			assert.Empty(t, renderedExtension)
			if tc.errorExpected {
				assert.ErrorContains(t, err, tc.expectedErrPrefix)
			} else {
				assert.NoError(t, err)
			}
		})
	}

}

func TestBuildExtensionContext_NoAdditionalObjects(t *testing.T) {
	clusterName := "primary"

	service := kube_test.CreateServiceAsUnstructuredObject("test-ns", "svc")
	mop := kube_test.NewMopBuilder("testns", "mop").AddFilter(defaultFaultFilter).Build()

	renderContextNoAdditionalObjects := NewMOPRenderRequestContext(
		service,
		mop,
		map[string]string{},
		clusterName,
		defaultMsmOwnerRef,
		nil)

	extensionContext := buildExtensionContext(&renderContextNoAdditionalObjects, "faultInjection")
	assert.Equal(t, map[string]interface{}{}, extensionContext)
}

func TestBuildExtensionContext_WithAdditionalObjects(t *testing.T) {
	clusterName := "primary"

	service := kube_test.CreateServiceAsUnstructuredObject("test-ns", "svc")
	mop := kube_test.NewMopBuilder("testns", "mop").AddFilter(defaultFaultFilter).Build()
	fakeIgc := &unstructured.Unstructured{}
	additionalObjects := map[string]*AdditionalObjects{
		"faultInjection": {
			Singletons: map[string]interface{}{
				"igc": fakeIgc,
			},
		},
	}

	renderContextWithAdditionalObjects := NewMOPRenderRequestContext(
		service,
		mop,
		map[string]string{},
		clusterName,
		defaultMsmOwnerRef,
		additionalObjects)

	extensionContext := buildExtensionContext(&renderContextWithAdditionalObjects, "faultInjection")
	assert.Equal(t, map[string]interface{}{
		"additionalObjects": map[string]interface{}{
			"singletons": map[string]interface{}{
				"igc": fakeIgc,
			},
		},
	}, extensionContext)
}

type TestContextBuilder struct {
	object   *unstructured.Unstructured
	metadata map[string]string
	ownerRef *metav1.OwnerReference
}

func NewTestContextBuilder(object *unstructured.Unstructured) *TestContextBuilder {
	return &TestContextBuilder{
		object:   object,
		metadata: map[string]string{},
	}
}

func (b *TestContextBuilder) setOwner(owner *metav1.OwnerReference) *TestContextBuilder {
	b.ownerRef = owner
	return b
}

func (b *TestContextBuilder) build() *RenderRequestContext {
	context := NewRenderRequestContext(b.object, b.metadata, b.ownerRef, "cluster1", "", nil, nil)
	return &context
}

func objectsToGoldFileText(t *testing.T, objects []*unstructured.Unstructured) string {
	objestsAsJson, err := json.MarshalIndent(objects, "", "   ")

	require.NoError(t, err)
	return string(objestsAsJson)
}
