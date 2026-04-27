package rollout

import (
	"reflect"
	"strings"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/secretdiscovery"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/alias"

	"k8s.io/apimachinery/pkg/runtime"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	metricstesting "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics/testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"

	v1 "k8s.io/api/admission/v1"
)

const (
	DummyTemplate = "[\n   {\n      \"op\": \"replace\",\n      \"path\": \"/spec/strategy\",\n      \"value\": \"test\"\n   }\n]\n"
)

var (
	templateOverrideAnnotationAsStr = common.GetStringForAttribute(constants.TemplateOverrideAnnotation)

	optInServiceBG      = kube_test.NewServiceBuilder("bg-service", "test-namespace").SetPorts([]corev1.ServicePort{{Name: "http", Port: 7442}}).SetAnnotations(map[string]string{templateOverrideAnnotationAsStr: "default/bg-stateless"}).Build()
	optInServiceCanary  = kube_test.NewServiceBuilder("canary-service", "test-namespace").SetPorts([]corev1.ServicePort{{Name: "http", Port: 7442}}).SetAnnotations(map[string]string{templateOverrideAnnotationAsStr: "default/canary-stateless"}).Build()
	optOutServiceBG     = kube_test.NewServiceBuilder("bg-service", "test-namespace").SetPorts([]corev1.ServicePort{{Name: "http", Port: 7442}}).Build()
	optOutServiceCanary = kube_test.NewServiceBuilder("canary-service", "test-namespace").SetPorts([]corev1.ServicePort{{Name: "http", Port: 7442}}).Build()
)

var (
	serviceLabelsBG = map[string]string{
		"app": "bg-service",
	}
)

func getRolloutPatchResponse(strategyLabel string) *v1.AdmissionResponse {
	var patch []byte
	if strategyLabel == StrategyLabelBG {
		patch = []byte(
			`[
   {
      "op": "replace",
      "path": "/spec/strategy",
      "value": "bg"
   }
]
`)
	}
	if strategyLabel == StrategyLabelCanary {
		patch = []byte(
			`[
   {
      "op": "replace",
      "path": "/spec/strategy",
      "value": "canary"
   }
]
`)
	}
	return &v1.AdmissionResponse{
		Allowed: true,
		Patch:   patch,
		PatchType: func() *v1.PatchType {
			pt := v1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

func getOcmRolloutPatchResponse(strategyLabel string) *v1.AdmissionResponse {
	var patch []byte
	if strategyLabel == StrategyLabelBG {
		patch = []byte(
			`[
   {
      "op": "replace",
      "path": "/spec/strategy",
      "value": "ocm-bg"
   }
]
`)
	}
	if strategyLabel == StrategyLabelCanary {
		patch = []byte(
			`[
   {
      "op": "replace",
      "path": "/spec/strategy",
      "value": "ocm-canary"
   }
]
`)
	}
	return &v1.AdmissionResponse{
		Allowed: true,
		Patch:   patch,
		PatchType: func() *v1.PatchType {
			pt := v1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

func TestAdmitForRollout(t *testing.T) {

	primaryClusterName := "primary"
	remoteClusterName := "remote"

	type metricStruct struct {
		name   string
		value  float64
		labels map[string]string
	}
	testCases := []struct {
		name                 string
		isPrimary            bool
		admissionReview      *v1.AdmissionReview
		existingService      *corev1.Service
		expectedResponse     *v1.AdmissionResponse
		expectedMetrics      []metricStruct
		admitOnFailure       bool
		enableOcmIntegration bool
	}{
		{
			name:      "UnknownKind",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.ServiceKind,
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationUnsupportedKind, primaryClusterName),
				},
			},
		},
		{
			name:      "DeleteRequest",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind:      constants.RolloutKind,
					Operation: v1.Delete,
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationUnsupportedOperation, primaryClusterName),
				},
			},
		},
		{
			name:      "EmptySpecShouldFail",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Message: "failed to unmarshal argoproj.io/v1alpha1, Kind=Rollout from mutation request: unexpected end of JSON input",
				},
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalMutationErrors,
					value:  1,
					labels: GetMutatingErrorLabel(MutatingErrorUnmarshall, primaryClusterName),
				},
				{
					name:  metrics.RolloutAdmissionFailureMetric,
					value: 1,
				},
			},
		},
		{
			name:           "EmptySpecShouldAllow",
			isPrimary:      true,
			admitOnFailure: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
				Result: &metav1.Status{
					Message: "failed to unmarshal argoproj.io/v1alpha1, Kind=Rollout from mutation request: unexpected end of JSON input",
				},
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalMutationErrors,
					value:  1,
					labels: GetMutatingErrorLabel(MutatingErrorUnmarshall, primaryClusterName),
				},
				{
					name:  metrics.RolloutAdmissionFailureMetric,
					value: 1,
				},
			},
		},
		{
			name:      "NotManagedRolloutBG",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"blueGreen\":{\"previewService\":\"preview-service\"}}}}"),
					},
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationNotManaged, primaryClusterName),
				},
			},
		},
		{
			name:      "NotManagedRolloutCanary",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"canary\":{\"canaryService\":\"some-service\"}}}}"),
					},
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationNotManaged, primaryClusterName),
				},
			},
		},
		{
			name:      "MissingActiveServiceFieldInBlueGreenStrategy",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"blueGreen\":{}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationMissingServiceRecord, primaryClusterName),
				},
			},
		},
		{
			name:      "MissingStableServiceFieldInCanary",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"canary\":{}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationNotManaged, primaryClusterName),
				},
			},
		},
		{
			name:      "MissingServiceRecordBG",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"blueGreen\":{\"activeService\":\"bg-service\",\"prePromotionAnalysis\":{\"templates\":[{\"templateName\":\"fit\"}]}}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationMissingServiceRecord, primaryClusterName),
				},
			},
		},
		{
			name:      "MissingServiceRecordBG - Remote Cluster",
			isPrimary: false,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"blueGreen\":{\"activeService\":\"bg-service\",\"prePromotionAnalysis\":{\"templates\":[{\"templateName\":\"fit\"}]}}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationMissingServiceRecord, remoteClusterName),
				},
			},
		},
		{
			name:      "MissingServiceRecordCanary",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"canary\":{\"stableService\":\"canary-service\"}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationMissingServiceRecord, primaryClusterName),
				},
			},
		},
		{
			name:      "MissingServiceRecordCanary - Remote Cluster",
			isPrimary: false,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"canary\":{\"stableService\":\"canary-service\"}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationMissingServiceRecord, remoteClusterName),
				},
			},
		},
		{
			name:      "OptOutServiceBG",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"blueGreen\":{\"activeService\":\"bg-service\"}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			existingService: &optOutServiceBG,
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationNotManaged, primaryClusterName),
				},
			},
		},
		{
			name:      "OptOutServiceCanary",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"canary\":{\"stableService\":\"canary-service\"}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			existingService: &optOutServiceCanary,
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
				{
					name:  metrics.RolloutAdmissionSuccessMetric,
					value: 1,
				},
				{
					name:   metrics.RolloutTotalEventsSkipped,
					value:  1,
					labels: getSkipMutationLabel(SkipMutationNotManaged, primaryClusterName),
				},
			},
		},
		{
			name:      "MissingPrePromotionAnalysis",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"blueGreen\":{\"activeService\":\"bg-service\"}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			existingService:  &optInServiceBG,
			expectedResponse: getRolloutPatchResponse(StrategyLabelBG),
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
			},
		},
		{
			name:      "MissingPrePromotionAnalysisTemplates",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"blueGreen\":{\"activeService\":\"bg-service\",\"prePromotionAnalysis\":{}}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			existingService:  &optInServiceBG,
			expectedResponse: getRolloutPatchResponse(StrategyLabelBG),
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
			},
		},
		{
			name:      "CanaryMutationSuccess",
			isPrimary: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"canary\":{\"stableService\":\"canary-service\"}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			existingService:  &optInServiceCanary,
			expectedResponse: getRolloutPatchResponse(StrategyLabelCanary),
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
			},
		},
		{
			name:      "CanaryMutationSuccess - Remote Cluster",
			isPrimary: false,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"canary\":{\"stableService\":\"canary-service\"}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			existingService:  &optInServiceCanary,
			expectedResponse: getRolloutPatchResponse(StrategyLabelCanary),
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
			},
		},
		{
			name:                 "OcmManagedRolloutSkipped",
			isPrimary:            true,
			enableOcmIntegration: true,
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\n  \"spec\":{\"strategy\":{\"canary\":{\"stableService\":\"canary-service\"}}}, \"metadata\": {\n    \"labels\": {\n      \"open-cluster-management.io/managed-by\": \"ocm\"\n    }\n  }\n}"),
					},
					Namespace: "test-namespace",
				},
			},
			existingService:  &optInServiceCanary,
			expectedResponse: getOcmRolloutPatchResponse(StrategyLabelCanary),
			expectedMetrics: []metricStruct{
				{
					name:  metrics.RolloutAdmissionRequestMetric,
					value: 1,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.EnableOcmIntegration = tc.enableOcmIntegration
			defer func() {
				features.EnableOcmIntegration = false
			}()
			//test setup
			logger := zaptest.NewLogger(t).Sugar()
			mutationTemplatePaths := []string{"projects/services/servicemesh/mesh-operator/pkg/testdata/mutation-templates/"}
			stopCh := make(chan struct{})
			clusterName := primaryClusterName
			if !tc.isPrimary {
				clusterName = remoteClusterName
			}

			k8sClient := k8sfake.NewSimpleClientset()
			remoteK8sClient := k8sfake.NewSimpleClientset()

			if tc.isPrimary {
				if tc.existingService != nil {
					k8sClient = k8sfake.NewSimpleClientset(tc.existingService)
				}
			} else {
				if tc.existingService != nil {
					remoteK8sClient = k8sfake.NewSimpleClientset(tc.existingService)
				}
			}

			alias.Manager = alias.NewAliasManager(nil, nil)

			templateManager := createTemplatesManager(logger, stopCh, mutationTemplatePaths)

			registry := prometheus.NewRegistry()

			primaryFakeClient := &secretdiscovery.FakeClient{KubeClient: k8sClient}
			remoteFakeClient := &secretdiscovery.FakeClient{KubeClient: remoteK8sClient}

			primaryCluster := secretdiscovery.NewFakeCluster(primaryClusterName, true, primaryFakeClient)
			remoteCluster := secretdiscovery.NewFakeCluster(remoteClusterName, false, remoteFakeClient)

			remoteClusters := []secretdiscovery.DynamicCluster{remoteCluster}

			discovery := secretdiscovery.NewFakeDynamicDiscovery(primaryCluster, remoteClusters)

			rolloutAdmittor, _ := NewRolloutAdmittor(k8sClient, templateManager, templateManager,
				zaptest.NewLogger(t).Sugar(), registry, tc.admitOnFailure, discovery, primaryClusterName)
			response := rolloutAdmittor.Admit(tc.admissionReview, clusterName)
			// First assert patch as string to view a human readable difference.
			assert.Equal(t, string(tc.expectedResponse.Patch), string(response.Patch))
			assert.Equal(t, tc.expectedResponse, response)
			for _, metric := range tc.expectedMetrics {
				if metric.labels == nil {
					metricstesting.AssertEqualsCounterValue(t, registry, metric.name, metric.value)
				} else {
					metricstesting.AssertEqualsCounterValueWithLabel(t, registry, metric.name, metric.labels, metric.value)
				}
			}
		})
	}
}

func TestAdmitForRolloutWithTemplateMetadata(t *testing.T) {
	originalValue := features.EnableTemplateMetadata
	features.EnableTemplateMetadata = true
	defer func() { features.EnableTemplateMetadata = originalValue }()

	primaryClusterName := "primary"

	serviceWithMetadata := kube_test.NewServiceBuilder("bg-service", "test-namespace").
		SetPorts([]corev1.ServicePort{{Name: "http", Port: 7442}}).
		SetAnnotations(map[string]string{templateOverrideAnnotationAsStr: "default/bg-stateless"}).Build()

	serviceWithoutMetadata := kube_test.NewServiceBuilder("bg-service", "test-namespace").
		SetPorts([]corev1.ServicePort{{Name: "http", Port: 7442}}).
		SetAnnotations(map[string]string{templateOverrideAnnotationAsStr: "unknown/template"}).Build()

	testMetadata := map[string]*templating.TemplateMetadata{
		"default_bg-stateless": {
			Rollout: &templating.RolloutMetadata{
				MutationTemplate:   "blueGreen",
				ReMutationTemplate: "reMutateBlueGreen",
			},
		},
	}

	testCases := []struct {
		name             string
		admissionReview  *v1.AdmissionReview
		existingService  *corev1.Service
		expectedResponse *v1.AdmissionResponse
	}{
		{
			name: "MetadataBasedMutationSuccess",
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"blueGreen\":{\"activeService\":\"bg-service\"}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			existingService: &serviceWithMetadata,
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
				Patch:   []byte(DummyTemplate),
				PatchType: func() *v1.PatchType {
					pt := v1.PatchTypeJSONPatch
					return &pt
				}(),
			},
		},
		{
			name: "MetadataNotFoundSkipsMutation",
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.RolloutKind,
					Object: runtime.RawExtension{
						Raw: []byte("{\"spec\":{\"strategy\":{\"blueGreen\":{\"activeService\":\"bg-service\"}}}}"),
					},
					Namespace: "test-namespace",
				},
			},
			existingService: &serviceWithoutMetadata,
			expectedResponse: &v1.AdmissionResponse{
				Allowed: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			k8sClient := k8sfake.NewSimpleClientset()
			if tc.existingService != nil {
				k8sClient = k8sfake.NewSimpleClientset(tc.existingService)
			}

			alias.Manager = alias.NewAliasManager(nil, nil)

			templateManager := &TestableTemplateManager{
				templateMetadata: testMetadata,
				templateExists:   true,
				templateContent:  DummyTemplate,
			}

			registry := prometheus.NewRegistry()

			primaryFakeClient := &secretdiscovery.FakeClient{KubeClient: k8sClient}
			primaryCluster := secretdiscovery.NewFakeCluster(primaryClusterName, true, primaryFakeClient)
			discovery := secretdiscovery.NewFakeDynamicDiscovery(primaryCluster, []secretdiscovery.DynamicCluster{})

			rolloutAdmittor, _ := NewRolloutAdmittor(k8sClient, templateManager, templateManager,
				zaptest.NewLogger(t).Sugar(), registry, false, discovery, primaryClusterName)
			response := rolloutAdmittor.Admit(tc.admissionReview, primaryClusterName)

			assert.Equal(t, tc.expectedResponse, response)
		})
	}
}

func TestCreatePatches(t *testing.T) {
	service := createService("", "", "portName", 0)
	metadata := make(map[string]string)
	testCases := []struct {
		name            string
		existingService *corev1.Service
		admissionReview *v1.AdmissionReview
		strategy        string
		templateFound   bool
		templateContent string
		expectedError   error
		expectedPatches string
	}{
		{
			name: "MutationTemplateFound",
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.ServiceKind,
				},
			},
			strategy:        "bg-stateless",
			templateFound:   true,
			templateContent: DummyTemplate,
			expectedPatches: "[\n   {\n      \"op\": \"replace\",\n      \"path\": \"/spec/strategy\",\n      \"value\": \"test\"\n   }\n]\n",
		},
		{
			name: "MutationTemplateNotFound",
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.ServiceKind,
				},
			},
			strategy:        "canary-stateless",
			templateFound:   false,
			expectedPatches: "",
		},
		{
			name: "EmptyTemplate",
			admissionReview: &v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					Kind: constants.ServiceKind,
				},
			},
			strategy:        "bg-stateless",
			templateFound:   true,
			templateContent: `if 1 == 2 then {}`,
			expectedPatches: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t).Sugar()
			mutationTemplatePaths := []string{"../testdata/mutation-templates/"}
			tc.existingService = service

			templateManager := TestableTemplateManager{mutationTemplatePaths, tc.templateContent, tc.templateFound, nil}
			rolloutAdmittor := rolloutAdmittor{logger: logger, mutationTemplatesManager: &templateManager, serviceTemplatesManager: &templateManager}

			actualPatches, error := rolloutAdmittor.createPatches(tc.existingService, tc.admissionReview, tc.strategy, metadata)

			if tc.expectedError != nil {
				assert.Equal(t, tc.expectedError, error)
			}
			assert.True(t, reflect.DeepEqual(tc.expectedPatches, actualPatches))
		})
	}
}

func createTemplatesManager(logger *zap.SugaredLogger, stopCh <-chan struct{}, templatePaths []string) templating.TemplatesManager {
	templateManager, err := templating.NewTemplatesManager(logger, stopCh, templatePaths)
	if err != nil {
		logger.Fatalw("Can't create templates manager", "error", err)
	}
	return templateManager
}

func createService(namespace string, name string, portName string, portNumber int32) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    serviceLabelsBG,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: portName, Protocol: corev1.ProtocolTCP, Port: portNumber, TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: 30002}},
			},
		},
	}
}

type TestableTemplateManager struct {
	mutationTemplatePaths []string
	templateContent       string
	templateExists        bool
	templateMetadata      map[string]*templating.TemplateMetadata
}

func (m *TestableTemplateManager) Exists(_ string) bool {
	return true
}

func (m *TestableTemplateManager) GetTemplatePaths() []string {
	return []string{}
}

func (m *TestableTemplateManager) GetTemplatesByPrefix(_ string) map[string]string {
	foundTemplates := make(map[string]string)
	if m.templateExists {
		foundTemplates["dummy"] = m.templateContent
	}
	return foundTemplates
}

func (m *TestableTemplateManager) GetTemplateMetadata(templateKey string) *templating.TemplateMetadata {

	internalKey := strings.ReplaceAll(templateKey, constants.TemplateNameSeparator, templating.TemplateKeyDelimiter)
	if m.templateMetadata != nil {
		return m.templateMetadata[internalKey]
	}
	return nil
}
