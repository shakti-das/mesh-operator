package rollout

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/k8swebhook"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	ocm2 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/ocm"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/dynamicrouting"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/secretdiscovery"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
)

const (
	SkipMutation                     = "skipped"
	SkipMutationUnsupportedKind      = "unsupported_kind"
	SkipMutationUnsupportedOperation = "unsupported_operation"
	SkipMutationNotManaged           = "not_managed"
	SkipMutationMissingServiceRecord = "missing_service_record"

	NullPatch = "null"
)

type rolloutAdmittor struct {
	logger                   *zap.SugaredLogger
	kubernetesClient         kubernetes.Interface
	mutationTemplatesManager templating.TemplatesManager
	serviceTemplatesManager  templating.TemplatesManager
	metricsRegistry          *prometheus.Registry
	admitOnFailure           bool
	discovery                secretdiscovery.Discovery
	primaryClusterName       string
}

// NewRolloutAdmittor returns a k8swebhook.Admittor instance
func NewRolloutAdmittor(kubernetesClient kubernetes.Interface, mutationTemplatesManager templating.TemplatesManager, serviceTemplatesManager templating.TemplatesManager, logger *zap.SugaredLogger, registry *prometheus.Registry, admitOnFailure bool, discovery secretdiscovery.Discovery, primaryClusterName string) (k8swebhook.AdmittorV1, error) {
	return &rolloutAdmittor{logger: logger, kubernetesClient: kubernetesClient,
		mutationTemplatesManager: mutationTemplatesManager, serviceTemplatesManager: serviceTemplatesManager, metricsRegistry: registry, admitOnFailure: admitOnFailure, discovery: discovery, primaryClusterName: primaryClusterName}, nil
}

func (a *rolloutAdmittor) Admit(ar *v1.AdmissionReview, clusterName string) *v1.AdmissionResponse {
	if clusterName == "" {
		clusterName = a.primaryClusterName
	}
	contextLogger := a.establishLoggingContext(ar, clusterName)
	contextLogger.Infow(
		"handling mutation request",
		"arKind", ar.Kind,
		"arAPIVersion", ar.APIVersion,
		"dryRun", ar.Request.DryRun,
		"operation", ar.Request.Operation,
		"resourceKind", ar.Request.Kind,
		"subResource", ar.Request.SubResource,
		"userinfo", ar.Request.UserInfo)

	metrics.GetOrRegisterCounter(metrics.RolloutAdmissionRequestMetric, a.metricsRegistry).Inc()

	if ar.Request.Kind != constants.RolloutKind {
		return a.createNoOpResponse(contextLogger, "mutation skipped for unhandled kind",
			SkipMutationUnsupportedKind, clusterName)
	}

	if ar.Request.Operation == v1.Delete {
		return a.createNoOpResponse(contextLogger, "mutation skipped for Delete request",
			SkipMutationUnsupportedOperation, clusterName)
	}

	obj, unmarshalError := common.UnmarshallObject(ar)
	if unmarshalError != nil {
		// Let this case slide, so we don't block rollouts we don't know how to handle.
		return a.createErrorResponse(contextLogger, unmarshalError, MutatingErrorUnmarshall, clusterName)
	}

	var isManagedRollout, rolloutStrategy = a.isManagedRollout(obj)
	if !isManagedRollout {
		return a.createNoOpResponse(contextLogger, "mutation skipped for non B/G, canary Rollout",
			SkipMutationNotManaged, clusterName)
	}

	var serviceName string
	if rolloutStrategy == StrategyLabelBG {
		serviceName, _, _ = unstructured.NestedString(obj.Object, "spec", "strategy", "blueGreen", "activeService")
	}
	if rolloutStrategy == StrategyLabelCanary {
		serviceName, _, _ = unstructured.NestedString(obj.Object, "spec", "strategy", "canary", "stableService")
	}

	cluster := GetClusterByName(clusterName, a.discovery)
	if cluster == nil {
		return a.createErrorResponse(contextLogger, fmt.Errorf("cluster %s is not known", clusterName), UnknownClusterError, clusterName)
	}

	client, clientDiscoveryError := cluster.GetClient()
	if clientDiscoveryError != nil {
		return a.createErrorResponse(contextLogger, clientDiscoveryError, ClientDiscoveryError, clusterName)
	}

	serviceRecord, lookupError := client.Kube().CoreV1().Services(ar.Request.Namespace).Get(context.TODO(), serviceName, metav1.GetOptions{})

	if lookupError != nil || serviceRecord == nil {
		// We don't know if service chose to opt-out, so let it through.
		return a.createNoOpResponse(contextLogger, fmt.Sprintf("mutation skipped, no service record: %s found", serviceName), SkipMutationMissingServiceRecord, clusterName)
	}

	if !a.isManagedService(serviceRecord, rolloutStrategy) {
		// Service is not opted in and using non B/G, canary template
		return a.createNoOpResponse(contextLogger, fmt.Sprintf("mutation skipped, service not opt-in: %s", serviceName), SkipMutationNotManaged, clusterName)
	}

	argoManaged, mutationTemplate := GetMutationTemplate(a.serviceTemplatesManager, serviceRecord)
	if !argoManaged {
		return a.createNoOpResponse(contextLogger, fmt.Sprintf("mutation skipped, service not opted into argo rollouts: %s", serviceName), SkipMutationNotManaged, clusterName)
	}

	if rolloutStrategy == StrategyLabelBG && !a.isAnalysisTemplatePresent(obj) {
		contextLogger.Infof("missing Rollout.strategy.blueGreen.prePromotionAnalysis in mutation request")
	}

	metadata := map[string]string{}
	if features.EnableDynamicRoutingForBGAndCanaryServices {
		dynamicRoutingMetadata := dynamicrouting.GetMetadataIfDynamicRoutingEnabled(obj, serviceRecord, a.logger, a.metricsRegistry)
		metadata = common.MergeMaps(metadata, dynamicRoutingMetadata)
	}
	// Pass a flag that this is an ocm-managed rollout
	if features.EnableOcmIntegration && ocm2.IsOcmManagedObject(obj) {
		metadata[constants.IsOcmManagedRollout] = "true"
	}

	patches, err := a.createPatches(serviceRecord, ar, mutationTemplate, metadata)
	if err != nil {
		return a.createErrorResponse(contextLogger, err, MutatingErrorPatching, clusterName)
	}

	return a.createSuccessResponse(patches)
}

func getLabel(metricType string, errorType string, clusterName string) map[string]string {
	return map[string]string{
		metricType: errorType,
		"cluster":  clusterName,
	}
}

func getSkipMutationLabel(skipReason string, clusterName string) map[string]string {
	return getLabel(SkipMutation, skipReason, clusterName)
}

func (a *rolloutAdmittor) createErrorResponse(contextLogger *zap.SugaredLogger, err error, mutationErrorLabel string, clusterName string) *v1.AdmissionResponse {
	contextLogger.Errorw("mutating request failed", "error", err)

	metrics.GetOrRegisterCounterWithLabels(metrics.RolloutTotalMutationErrors, GetMutatingErrorLabel(mutationErrorLabel, clusterName), a.metricsRegistry).Inc()
	metrics.GetOrRegisterCounter(metrics.RolloutAdmissionFailureMetric, a.metricsRegistry).Inc()
	return a.errorResponse(err)
}

func (a *rolloutAdmittor) createNoOpResponse(contextLogger *zap.SugaredLogger, infoLog string, skipReason string, clusterName string) *v1.AdmissionResponse {
	contextLogger.Infof(infoLog)
	metrics.GetOrRegisterCounterWithLabels(metrics.RolloutTotalEventsSkipped, getSkipMutationLabel(skipReason, clusterName), a.metricsRegistry).Inc()
	metrics.GetOrRegisterCounter(metrics.RolloutAdmissionSuccessMetric, a.metricsRegistry).Inc()
	return a.noOpResponse()
}

func (a *rolloutAdmittor) createSuccessResponse(patches string) *v1.AdmissionResponse {
	metrics.GetOrRegisterCounter(metrics.RolloutAdmissionSuccessMetric, a.metricsRegistry).Inc()
	metrics.GetOrRegisterCounter(metrics.RolloutTotalSuccessfulMutations, a.metricsRegistry).Inc()
	return a.patchResponse(patches)
}

func (a *rolloutAdmittor) isManagedRollout(obj *unstructured.Unstructured) (bool, string) {
	bgStrategy, found, err := unstructured.NestedMap(obj.Object, "spec", "strategy", "blueGreen")
	if found && err == nil {
		previewService, found, _ := unstructured.NestedString(bgStrategy, "previewService")
		if !found || previewService == "" {
			return true, StrategyLabelBG
		}
	}
	canaryStrategy, found, err := unstructured.NestedMap(obj.Object, "spec", "strategy", "canary")
	if found && err == nil {
		_, foundStableSvc, _ := unstructured.NestedString(canaryStrategy, "stableService")
		if !foundStableSvc {
			return false, ""
		}
		_, foundCanarySvc, _ := unstructured.NestedString(canaryStrategy, "canaryService")
		if !foundCanarySvc {
			return true, StrategyLabelCanary
		}
	}
	return false, ""
}

func (a *rolloutAdmittor) isManagedService(svc *corev1.Service, strategy string) bool {
	template := common.GetAnnotationOrAlias(constants.TemplateOverrideAnnotation, svc.GetAnnotations())
	if template == "" {
		return false
	}

	if features.EnableTemplateMetadata {
		return HasRolloutMetadata(a.serviceTemplatesManager, template)
	}

	if strategy == StrategyLabelBG {
		return template == OptInServiceTemplateBG
	}
	if strategy == StrategyLabelCanary {
		return template == OptInServiceTemplateCanary
	}
	return false
}

func (a *rolloutAdmittor) isAnalysisTemplatePresent(obj *unstructured.Unstructured) bool {
	analysisTemplates, found, err := unstructured.NestedSlice(obj.Object,
		"spec", "strategy", "blueGreen", "prePromotionAnalysis", "templates")
	return found && err != nil && len(analysisTemplates) > 0
}

func (a *rolloutAdmittor) createPatches(service *corev1.Service, ar *v1.AdmissionReview, strategy string, metadata map[string]string) (string, error) {
	return CreateRolloutPatches(a.mutationTemplatesManager, service, ar.Request.Object.Raw, strategy, metadata)
}

func (a *rolloutAdmittor) establishLoggingContext(ar *v1.AdmissionReview, clusterName string) *zap.SugaredLogger {
	return a.logger.With(
		"kind", ar.Request.Kind,
		"namespace", ar.Request.Namespace,
		"name", ar.Request.Name,
		"operation", ar.Request.Operation,
		"uid", ar.Request.UID,
		"clusterName", clusterName)
}

func (a *rolloutAdmittor) noOpResponse() *v1.AdmissionResponse {
	return &v1.AdmissionResponse{
		Allowed: true,
	}
}

func (a *rolloutAdmittor) errorResponse(err error) *v1.AdmissionResponse {
	return &v1.AdmissionResponse{
		Allowed: a.admitOnFailure,
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

func (a *rolloutAdmittor) patchResponse(patches string) *v1.AdmissionResponse {
	patchBytes := []byte(patches)

	return &v1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *v1.PatchType {
			pt := v1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

func GetClusterByName(clusterName string, discovery secretdiscovery.Discovery) secretdiscovery.DynamicCluster {
	for _, cluster := range discovery.GetClusters() {
		if cluster.GetName() == clusterName {
			return cluster
		}
	}
	return nil
}
