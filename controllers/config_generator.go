package controllers

import (
	"fmt"
	"time"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	meshOpErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/transition"
	"go.uber.org/zap"
)

const (
	phaseRender         = "render"
	phaseMutate         = "mutate"
	phaseOverlay        = "overlay"
	phaseTransitionDiff = "transition_diff"
	phaseBeforeApply    = "before_apply"
	phaseApply          = "apply"
)

type configGenerator struct {
	renderer        templating.TemplateRenderer
	mutators        []templating.Mutator
	comparer        transition.MeshConfigComparer
	applicator      templating.Applicator
	overlayer       templating.Overlayer
	logger          *zap.SugaredLogger
	metricsRegistry *prometheus.Registry
}

// OnBeforeApply - a callback function invoked before config is applied
type OnBeforeApply func(config *templating.GeneratedConfig) error

func NoOpOnBeforeApply(config *templating.GeneratedConfig) error {
	return nil
}

// ConfigGenerator handles all the config-generation components and processes them in order.
type ConfigGenerator interface {
	GenerateConfig(context *templating.RenderRequestContext, beforeApply OnBeforeApply, logger *zap.SugaredLogger) ([]*templating.AppliedConfigObject, error)
}

func NewConfigGenerator(
	renderer templating.TemplateRenderer,
	mutators []templating.Mutator,
	comparer transition.MeshConfigComparer,
	applicator templating.Applicator,
	overlayer templating.Overlayer,
	logger *zap.SugaredLogger,
	metricsRegistry *prometheus.Registry) ConfigGenerator {
	return &configGenerator{
		renderer:        renderer,
		mutators:        mutators,
		comparer:        comparer,
		applicator:      applicator,
		overlayer:       overlayer,
		logger:          logger,
		metricsRegistry: metricsRegistry,
	}
}

func (g *configGenerator) GenerateConfig(context *templating.RenderRequestContext, beforeApply OnBeforeApply, ctxLogger *zap.SugaredLogger) ([]*templating.AppliedConfigObject, error) {
	latencyMetricLabels := g.collectLatencyMetricLabels(context)
	// Render
	renderStartTime := time.Now()
	renderedTemplates, err := g.renderer.Render(context)
	if !features.EnableSkipReconcileLatencyMetric {
		g.recordLatencyMetric(latencyMetricLabels, phaseRender, renderStartTime)
	}
	g.recordObjectMetrics(context, renderedTemplates)

	if err != nil {
		ctxLogger.Errorf("encountered error while rendering templates: %v", err)
		return nil, err
	}

	if renderedTemplates != nil && len(renderedTemplates.Messages) > 0 {
		formatted := templating.FormatMessages(renderedTemplates.Messages)
		ctxLogger.Errorw("template reported user config error", "details", formatted)
		return nil, &meshOpErrors.UserConfigError{Message: fmt.Sprintf("template error: %s", formatted)}
	}

	// Mutate
	mutateStartTime := time.Now()
	currentConfig, err := templating.MutateRenderedConfigs(context, renderedTemplates, g.mutators)
	if !features.EnableSkipReconcileLatencyMetric {
		g.recordLatencyMetric(latencyMetricLabels, phaseMutate, mutateStartTime)
	}
	if err != nil {
		ctxLogger.Errorf("encountered error while mutating config: %v", err)
		return nil, err
	}

	// Overlay
	overlayStartTime := time.Now()
	currentConfig, overlayError := g.overlayer(currentConfig, context.MopOverlays)
	if !features.EnableSkipReconcileLatencyMetric {
		g.recordLatencyMetric(latencyMetricLabels, phaseOverlay, overlayStartTime)
	}
	if overlayError != nil {
		// overlayer returns config based on all successful overlaying mops. if overlay errors are seen and no mops are valid, the base config would have been restored by the overlayer itself.
		ctxLogger.Errorf(overlayError.Error())
	}

	// Transition-diff
	transitionDiffStartTime := time.Now()
	if g.comparer != nil {
		var comparerError error
		currentConfig, comparerError = g.comparer.DiffWithCopilotConfig(currentConfig, context.Object, context.Metadata)
		if comparerError != nil {
			ctxLogger.Errorf("encountered error while comparing config: %v", comparerError)
			return nil, common.GetFirstNonNil(overlayError, comparerError)
		}

		currentConfig = g.comparer.DiffAdditionalTemplates(*currentConfig, context.Object)
	}
	if !features.EnableSkipReconcileLatencyMetric {
		g.recordLatencyMetric(latencyMetricLabels, phaseTransitionDiff, transitionDiffStartTime)
	}

	// Before apply
	beforeApplyStartTime := time.Now()
	beforeApplyError := beforeApply(currentConfig)
	if !features.EnableSkipReconcileLatencyMetric {
		g.recordLatencyMetric(latencyMetricLabels, phaseBeforeApply, beforeApplyStartTime)
	}
	if beforeApplyError != nil {
		return nil, beforeApplyError
	}

	// Apply
	applyStartTime := time.Now()
	appliedConfig, applicationError := g.applicator.ApplyConfig(context, currentConfig, ctxLogger)
	if !features.EnableSkipReconcileLatencyMetric {
		g.recordLatencyMetric(latencyMetricLabels, phaseApply, applyStartTime)
	}
	if applicationError != nil {
		ctxLogger.Errorf("encountered error while applying config: %v", applicationError)
		return nil, common.GetFirstNonNil(overlayError, applicationError)
	}

	return appliedConfig, overlayError
}

func EraseObjectMetrics(metricsRegistry *prometheus.Registry, kind, clusterName, namespace, name string) {
	commonmetrics.EraseMetricsForK8sResource(
		metricsRegistry,
		[]string{commonmetrics.ObjectsConfiguredTotal},
		map[string]string{
			commonmetrics.ResourceKind:      kind,
			commonmetrics.ClusterLabel:      clusterName,
			commonmetrics.NamespaceLabel:    namespace,
			commonmetrics.ResourceNameLabel: name,
		})
}

func (g *configGenerator) recordObjectMetrics(context *templating.RenderRequestContext, generatedConfig *templating.GeneratedConfig) {
	templateType := "na"
	if generatedConfig != nil {
		templateType = generatedConfig.TemplateType
	}

	if context.MeshOperator != nil {
		targetName := commonmetrics.TargetAll
		targetKind := constants.ServiceKind.Kind
		if context.Object != nil {
			targetName = context.Object.GetName()
		}
		metricLabels := commonmetrics.GetLabelsForReconciledResource(v1alpha1.MopKind.Kind, context.ClusterName, context.MeshOperator.Namespace, context.MeshOperator.Name, targetName, targetKind, templateType)
		commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.ObjectsConfiguredTotal, metricLabels, g.metricsRegistry).Set(1)
	} else if context.Object != nil {
		commonmetrics.EmitObjectsConfiguredStats(g.metricsRegistry, context.ClusterName, context.Object.GetNamespace(), context.Object.GetName(), context.Object.GetKind(), constants.NotAvailable)

		objectsGenerated := 0
		if generatedConfig != nil {
			objectsGenerated = len(generatedConfig.FlattenConfig())
		}
		identity := commonmetrics.GetObjectIdentity(context.Object.GetName(), context.Object.GetLabels())
		configuredResourceLabels := commonmetrics.GetLabelsForConfiguredResource(context.Object.GetKind(), context.ClusterName, context.Object.GetNamespace(), context.Object.GetName(), identity, templateType)
		commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.MeshConfigResourcesTotal, configuredResourceLabels, g.metricsRegistry).Set(float64(objectsGenerated))
	}
}

func (g *configGenerator) collectLatencyMetricLabels(context *templating.RenderRequestContext) map[string]string {
	labels := map[string]string{
		commonmetrics.ClusterLabel: context.ClusterName,
		commonmetrics.ResourceKind: "n_a",
	}
	if context.MeshOperator != nil {
		labels[commonmetrics.ResourceKind] = v1alpha1.MopKind.Kind
	} else if context.Object != nil {
		labels[commonmetrics.ResourceKind] = context.Object.GetKind()
	}
	return labels
}

func (g *configGenerator) recordLatencyMetric(labels map[string]string, phase string, startTime time.Time) {
	labels[commonmetrics.ReconcilePhaseLabel] = phase
	commonmetrics.GetOrRegisterSummaryWithLabels(
		commonmetrics.ReconcileLatencyMetric,
		g.metricsRegistry,
		map[float64]float64{0.25: 0.1, 0.5: 0.1, 0.95: 0.1, 0.99: 0.1, 1.0: 0.1},
		labels,
	).Observe(time.Since(startTime).Seconds())
}
