package controllers

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	prommodel "github.com/prometheus/client_model/go"

	"go.uber.org/zap"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	metricstesting "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics/testing"

	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"

	mopErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/transition"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	renderResult = templating.GeneratedConfig{
		TemplateType: "templateName",
		Config: map[string][]*unstructured.Unstructured{
			"templateName": {
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "config-name",
						},
					},
				},
			},
		},
	}

	serviceContext = &templating.RenderRequestContext{
		Object: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"kind": "Service",
				"metadata": map[string]interface{}{
					"namespace": namespace,
					"name":      "test-service",
				},
			},
		},
	}

	serviceMopContext = &templating.RenderRequestContext{
		Object: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"kind": "Service",
				"metadata": map[string]interface{}{
					"namespace": namespace,
					"name":      "test-service",
				},
			},
		},
		MeshOperator: &v1alpha1.MeshOperator{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "test-mop",
			},
		},
	}

	nsLevelMopContext = &templating.RenderRequestContext{
		MeshOperator: &v1alpha1.MeshOperator{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "test-mop",
			},
		},
	}
)

func TestGenerateConfigWhenRendererFails(t *testing.T) {
	renderError := "error during rendering"
	renderer := &fakeRenderer{
		results:      renderResult,
		errorToThrow: errors.New(renderError),
	}
	mutator := &fakeMutator{}
	fakeBeforeApply := &fakeOnBeforeApply{}
	var comparer transition.MeshConfigComparer
	comparer = &fakeComparer{}
	applicator := &fakeApplicator{}
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	configGenerator := NewConfigGenerator(renderer, []templating.Mutator{mutator}, comparer, applicator, templating.DontApplyOverlays, logger, registry)
	results, err := configGenerator.GenerateConfig(serviceContext, fakeBeforeApply.onBeforeApply, logger)

	assert.Nil(t, results)
	assert.True(t, renderer.wasCalled)
	assert.True(t, strings.Contains(err.Error(), renderError))

	assert.False(t, mutator.wasCalled)
	assert.False(t, comparer.(*fakeComparer).wasCalled)
	assert.False(t, comparer.(*fakeComparer).addonCompareCalled)
	assert.False(t, applicator.wasCalled)
	assert.False(t, fakeBeforeApply.wasCalled)

	assertEnableObjectMetric(t, registry, "Service", "test-service", "test-service", "Service", "NA")
	assertConfigResourcesMetric(t, registry, "Service", "test-service", "test-service", "na", 0)
}

func TestGeneratedConfigWhenMutatorFails(t *testing.T) {
	mutateError := "error during mutating"
	renderer := &fakeRenderer{
		results: renderResult,
	}
	mutator := &fakeMutator{
		errorToThrow: errors.New(mutateError),
	}
	fakeBeforeApply := &fakeOnBeforeApply{}
	var comparer transition.MeshConfigComparer
	comparer = &fakeComparer{}
	applicator := &fakeApplicator{}
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	configGenerator := NewConfigGenerator(renderer, []templating.Mutator{mutator}, comparer, applicator, templating.DontApplyOverlays, logger, registry)
	results, err := configGenerator.GenerateConfig(serviceContext, fakeBeforeApply.onBeforeApply, logger)

	assert.Nil(t, results)

	assert.True(t, renderer.wasCalled)
	assert.True(t, mutator.wasCalled)
	assert.True(t, strings.Contains(err.Error(), mutateError))

	assert.False(t, comparer.(*fakeComparer).wasCalled)
	assert.False(t, comparer.(*fakeComparer).addonCompareCalled)
	assert.False(t, applicator.wasCalled)
	assert.False(t, fakeBeforeApply.wasCalled)

	assertEnableObjectMetric(t, registry, "Service", "test-service", "test-service", "Service", "NA")
}

func TestGeneratedConfigWhenComparerFails(t *testing.T) {
	compareError := "error during comparing"
	renderer := &fakeRenderer{
		results: renderResult,
	}
	fakeBeforeApply := &fakeOnBeforeApply{}
	mutator := &fakeMutator{}
	var comparer transition.MeshConfigComparer
	comparer = &fakeComparer{
		errorToThrow: errors.New(compareError),
	}
	applicator := &fakeApplicator{}
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	configGenerator := NewConfigGenerator(renderer, []templating.Mutator{mutator}, comparer, applicator, templating.DontApplyOverlays, logger, registry)
	results, err := configGenerator.GenerateConfig(serviceContext, fakeBeforeApply.onBeforeApply, logger)

	assert.Nil(t, results)

	assert.True(t, renderer.wasCalled)
	assert.True(t, mutator.wasCalled)
	assert.True(t, comparer.(*fakeComparer).wasCalled)
	assert.False(t, comparer.(*fakeComparer).addonCompareCalled)
	assert.True(t, strings.Contains(err.Error(), compareError))
	assert.False(t, applicator.wasCalled)
	assert.False(t, fakeBeforeApply.wasCalled)

	assertEnableObjectMetric(t, registry, "Service", "test-service", "test-service", "Service", "NA")
}

func TestGeneratedConfigWhenApplicatorFails(t *testing.T) {
	applyError := "error during applying"
	renderer := &fakeRenderer{
		results: renderResult,
	}
	fakeBeforeApply := &fakeOnBeforeApply{}
	mutator := &fakeMutator{}
	var comparer transition.MeshConfigComparer
	comparer = &fakeComparer{}
	applicator := &fakeApplicator{
		errorToThrow: errors.New(applyError),
	}
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	configGenerator := NewConfigGenerator(renderer, []templating.Mutator{mutator}, comparer, applicator, templating.DontApplyOverlays, logger, registry)
	results, err := configGenerator.GenerateConfig(serviceContext, fakeBeforeApply.onBeforeApply, logger)

	assert.Nil(t, results)

	assert.True(t, renderer.wasCalled)
	assert.True(t, mutator.wasCalled)
	assert.True(t, comparer.(*fakeComparer).wasCalled)
	assert.True(t, comparer.(*fakeComparer).addonCompareCalled)
	assert.True(t, applicator.wasCalled)
	assert.True(t, strings.Contains(err.Error(), applyError))
	assert.True(t, fakeBeforeApply.wasCalled)

	assertEnableObjectMetric(t, registry, "Service", "test-service", "test-service", "Service", "NA")
}

func TestGeneratedConfigWhenOnBeforeApplyFails(t *testing.T) {
	beforeApplyError := "on before apply error"
	renderer := &fakeRenderer{
		results: renderResult,
	}
	fakeBeforeApply := &fakeOnBeforeApply{
		errorToThrow: errors.New(beforeApplyError),
	}
	mutator := &fakeMutator{}
	var comparer transition.MeshConfigComparer
	comparer = &fakeComparer{}
	applicator := &fakeApplicator{}
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	configGenerator := NewConfigGenerator(renderer, []templating.Mutator{mutator}, comparer, applicator, templating.DontApplyOverlays, logger, registry)
	results, err := configGenerator.GenerateConfig(serviceContext, fakeBeforeApply.onBeforeApply, logger)

	assert.Nil(t, results)

	assert.True(t, renderer.wasCalled)
	assert.True(t, mutator.wasCalled)
	assert.True(t, comparer.(*fakeComparer).wasCalled)
	assert.True(t, comparer.(*fakeComparer).addonCompareCalled)
	assert.True(t, fakeBeforeApply.wasCalled)
	assert.False(t, applicator.wasCalled)
	assert.True(t, strings.Contains(err.Error(), beforeApplyError))

	assertEnableObjectMetric(t, registry, "Service", "test-service", "test-service", "Service", "NA")
}

func TestGenerateConfigAndComparer(t *testing.T) {
	testCases := []struct {
		name     string
		comparer transition.MeshConfigComparer
	}{
		{
			name: "Happy path without comparer",
		},
		{
			name:     "Happy path with comparer",
			comparer: &fakeComparer{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			renderer := &fakeRenderer{results: renderResult}
			mutator := &fakeMutator{}
			applicator := &fakeApplicator{}
			logger := zaptest.NewLogger(t).Sugar()
			registry := prometheus.NewRegistry()

			configGenerator := NewConfigGenerator(renderer, []templating.Mutator{mutator}, tc.comparer, applicator, templating.DontApplyOverlays, logger, registry)
			_, err := configGenerator.GenerateConfig(serviceContext, NoOpOnBeforeApply, logger)

			assert.Nil(t, err)

			assertEnableObjectMetric(t, registry, "Service", "test-service", "test-service", "Service", "NA")
		})
	}
}

func TestGeneratedConfigWhenOverlayingFails(t *testing.T) {

	renderer := &fakeRenderer{
		results: renderResult,
	}
	mutator := &fakeMutator{}

	overlayer := func(config *templating.GeneratedConfig, mopNameToOverlays map[string][]v1alpha1.Overlay) (*templating.GeneratedConfig, mopErrors.OverlayingError) {
		return config, getOverlayingError("test-mop", 0, "error from overlayer")
	}

	logger := zaptest.NewLogger(t).Sugar()

	testCases := []struct {
		name                   string
		applicatorErrorToThrow error
		expectedErrorMessage   string
	}{
		{
			name:                 "RestoreBaseConfig",
			expectedErrorMessage: "error encountered when overlaying: mop test-mop, overlay <0>: error from overlayer",
		},
		// Even though, applicator fails to apply the config during restore, we expect the initial overlayer error
		{
			name:                   "ApplyConfigFailureDuringBaseConfigRestore",
			applicatorErrorToThrow: fmt.Errorf("applicator error"),
			expectedErrorMessage:   "error encountered when overlaying: mop test-mop, overlay <0>: error from overlayer",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			applicator := &fakeApplicator{}
			comparer := &fakeComparer{}
			if tc.applicatorErrorToThrow != nil {
				applicator.errorToThrow = tc.applicatorErrorToThrow
			}

			configGenerator := NewConfigGenerator(renderer, []templating.Mutator{mutator}, comparer, applicator, overlayer, logger, registry)
			results, err := configGenerator.GenerateConfig(serviceContext, NoOpOnBeforeApply, logger)

			assert.Nil(t, results)
			assert.True(t, comparer.wasCalled)
			assert.True(t, applicator.wasCalled)
			assert.EqualError(t, err, tc.expectedErrorMessage)

			assertEnableObjectMetric(t, registry, "Service", "test-service", "test-service", "Service", "NA")
		})
	}
}

func TestGenerateConfigWhenTemplateEmitsMessages(t *testing.T) {
	resultWithMessages := templating.GeneratedConfig{
		TemplateType: "templateName",
		Config:       map[string][]*unstructured.Unstructured{},
		Messages: []templating.TemplateMessage{
			{TemplateName: "filters_faultinjection", Message: "port out of range"},
		},
	}
	renderer := &fakeRenderer{results: resultWithMessages}
	mutator := &fakeMutator{}
	fakeBeforeApply := &fakeOnBeforeApply{}
	var comparer transition.MeshConfigComparer
	comparer = &fakeComparer{}
	applicator := &fakeApplicator{}
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	configGenerator := NewConfigGenerator(renderer, []templating.Mutator{mutator}, comparer, applicator, templating.DontApplyOverlays, logger, registry)
	results, err := configGenerator.GenerateConfig(serviceMopContext, fakeBeforeApply.onBeforeApply, logger)

	assert.Nil(t, results)
	assert.True(t, renderer.wasCalled)
	assert.True(t, mopErrors.IsUserConfigError(err))
	assert.Contains(t, err.Error(), "port out of range")

	assert.False(t, mutator.wasCalled, "mutator must not be called when template emits error messages")
	assert.False(t, comparer.(*fakeComparer).wasCalled)
	assert.False(t, applicator.wasCalled)
	assert.False(t, fakeBeforeApply.wasCalled)
}

func TestGenerateConfigForMop(t *testing.T) {
	testCases := []struct {
		name              string
		context           *templating.RenderRequestContext
		targetServiceName string
	}{
		{
			name:              "MOP with service selector",
			context:           serviceMopContext,
			targetServiceName: "test-service",
		},
		{
			name:              "NS level MOP",
			context:           nsLevelMopContext,
			targetServiceName: commonmetrics.TargetAll,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			renderer := &fakeRenderer{
				results: renderResult,
			}
			var comparer transition.MeshConfigComparer
			comparer = &fakeComparer{}
			applicator := &fakeApplicator{}
			logger := zaptest.NewLogger(t).Sugar()
			registry := prometheus.NewRegistry()

			configGenerator := NewConfigGenerator(renderer, []templating.Mutator{}, comparer, applicator, templating.DontApplyOverlays, logger, registry)
			_, _ = configGenerator.GenerateConfig(tc.context, NoOpOnBeforeApply, logger)

			assertEnableObjectMetric(t, registry, "MeshOperator", "test-mop", tc.targetServiceName, "Service", "templateName")
		})
	}
}

func TestGenerateConfigForService(t *testing.T) {
	renderer := &fakeRenderer{
		results: renderResult,
	}
	mutator := &fakeMutator{}
	var comparer transition.MeshConfigComparer
	comparer = &fakeComparer{}
	applicator := &fakeApplicator{}
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	configGenerator := NewConfigGenerator(renderer, []templating.Mutator{mutator}, comparer, applicator, templating.DontApplyOverlays, logger, registry)
	_, err := configGenerator.GenerateConfig(serviceContext, NoOpOnBeforeApply, logger)

	assert.NoError(t, err)

	assert.True(t, renderer.wasCalled)
	assert.True(t, mutator.wasCalled)
	assert.True(t, comparer.(*fakeComparer).wasCalled)
	assert.True(t, comparer.(*fakeComparer).addonCompareCalled)
	assert.True(t, applicator.wasCalled)

	assertEnableObjectMetric(t, registry, "Service", "test-service", "test-service", "Service", "NA")
	assertConfigResourcesMetric(t, registry, "Service", "test-service", "test-service", "templateName", 1)
	assertLatencyMetricsForService(t, registry, "Service", "test-service")
}

func assertEnableObjectMetric(t *testing.T, registry *prometheus.Registry, kind, name, targetName, targetKind, expectedTemplate string) {
	labels := map[string]string{
		commonmetrics.ResourceKind:            kind,
		commonmetrics.ClusterLabel:            "",
		commonmetrics.NamespaceLabel:          namespace,
		commonmetrics.ResourceNameLabel:       name,
		commonmetrics.TargetResourceNameLabel: targetName,
		commonmetrics.TargetKindLabel:         targetKind,
		commonmetrics.TemplateTypeLabel:       expectedTemplate,
	}
	metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.ObjectsConfiguredTotal, labels, 1)
}

func assertConfigResourcesMetric(t *testing.T, registry *prometheus.Registry, kind, name, identity, template string, expectedObjectsCount float64) {
	labels := map[string]string{
		commonmetrics.ResourceKind:          kind,
		commonmetrics.ClusterLabel:          "",
		commonmetrics.NamespaceLabel:        namespace,
		commonmetrics.ResourceNameLabel:     name,
		commonmetrics.ResourceIdentityLabel: identity,
		commonmetrics.TemplateTypeLabel:     template,
	}
	metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.MeshConfigResourcesTotal, labels, expectedObjectsCount)
}

func assertLatencyMetricsForService(t *testing.T, registry *prometheus.Registry, kind, name string) {
	for _, phase := range []string{phaseRender, phaseMutate, phaseOverlay, phaseTransitionDiff, phaseBeforeApply, phaseApply} {
		labels := map[string]string{
			commonmetrics.ClusterLabel:        "",
			commonmetrics.ResourceKind:        kind,
			commonmetrics.ReconcilePhaseLabel: phase,
		}

		summary := commonmetrics.GetOrRegisterSummaryWithLabels(
			commonmetrics.ReconcileLatencyMetric,
			registry,
			map[float64]float64{0.25: 0.1, 0.5: 0.1, 0.95: 0.1, 0.99: 0.1, 1.0: 0.1},
			labels,
		)

		// We can't assert the actual latency value, but make sure that sample count is non zero
		m := &prommodel.Metric{}
		_ = summary.Write(m)

		assert.Equal(t, uint64(1), m.Summary.GetSampleCount())
	}
}

type fakeRenderer struct {
	wasCalled    bool
	errorToThrow error
	results      templating.GeneratedConfig
}

func (r *fakeRenderer) Render(_ *templating.RenderRequestContext) (*templating.GeneratedConfig, error) {
	r.wasCalled = true
	if r.errorToThrow != nil {
		return nil, r.errorToThrow
	}
	return &r.results, nil
}

type fakeMutator struct {
	wasCalled    bool
	errorToThrow error
}

func (m *fakeMutator) Mutate(_ *templating.RenderRequestContext, _ *unstructured.Unstructured) (
	*unstructured.Unstructured, error) {
	m.wasCalled = true
	if m.errorToThrow != nil {
		return nil, m.errorToThrow
	}
	return nil, nil
}

type fakeComparer struct {
	wasCalled          bool
	addonCompareCalled bool
	errorToThrow       error
}

func (c *fakeComparer) DiffAdditionalTemplates(_ templating.GeneratedConfig, _ *unstructured.Unstructured) *templating.GeneratedConfig {
	c.addonCompareCalled = true
	return nil
}

func (c *fakeComparer) DiffWithCopilotConfig(config *templating.GeneratedConfig, _ *unstructured.Unstructured, _ map[string]string) (
	*templating.GeneratedConfig, error) {
	c.wasCalled = true
	if c.errorToThrow != nil {
		return nil, c.errorToThrow
	}
	return config, nil
}

type fakeApplicator struct {
	wasCalled    bool
	errorToThrow error
}

func (a *fakeApplicator) ApplyConfig(_ *templating.RenderRequestContext, _ *templating.GeneratedConfig, _ *zap.SugaredLogger) (
	[]*templating.AppliedConfigObject, error) {
	a.wasCalled = true
	if a.errorToThrow != nil {
		return nil, a.errorToThrow
	}
	return nil, nil
}

type fakeOnBeforeApply struct {
	wasCalled    bool
	errorToThrow error
}

func (ba *fakeOnBeforeApply) onBeforeApply(_ *templating.GeneratedConfig) error {
	ba.wasCalled = true
	return ba.errorToThrow
}
