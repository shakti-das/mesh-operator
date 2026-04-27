package controllers

import (
	"encoding/json"
	"fmt"
	"testing"

	"go.uber.org/zap"

	"k8s.io/apimachinery/pkg/api/errors"

	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	error2 "github.com/istio-ecosystem/mesh-operator/pkg/errors"

	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	metricstesting "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics/testing"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/controller_test"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/istio-ecosystem/mesh-operator/pkg/resources_test"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating_test"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
)

var (
	retriesOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				{
					"name":    "test-route-1",
					"retries": "100",
				},
			},
		},
	})
	timeoutOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				{
					"name":    "test-route-1",
					"timeout": "7s",
				},
			},
		},
	})
	invalidDrOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				{
					"name":    "test-route-1",
					"retries": "100",
				},
			},
		},
	})
	testSvc = kube_test.NewServiceBuilder("svc1", namespace).GetServiceAsUnstructuredObject()
	testSE  = kube_test.NewServiceEntryBuilder("testSE", namespace).GetServiceEntryAsUnstructuredObject()
	testMop = kube_test.NewMopBuilder(namespace, "test-mop").
		AddOverlay(v1alpha1.Overlay{
			Name: "test-vs-1",
			Kind: "VirtualService",
			StrategicMergePatch: runtime.RawExtension{
				Raw: retriesOverlayBytes,
			}}).
		Build()
)

func TestRecordRenderingStats(t *testing.T) {
	clusterName := "test-cluster"

	testCases := []struct {
		name                    string
		mop                     *v1alpha1.MeshOperator
		isOverlayMop            bool
		expectedMopFailure      float64
		expectedResourceFailure float64
		expectedResourceTotal   float64
	}{
		{
			name:                    "MopSuccess",
			mop:                     kube_test.NewMopBuilder(namespace, name).SetPhase(PhaseSucceeded).Build(),
			expectedMopFailure:      0,
			expectedResourceFailure: 0,
			expectedResourceTotal:   0,
		},
		{
			name:                    "MopFailure",
			mop:                     kube_test.NewMopBuilder(namespace, name).SetPhase(PhaseFailed).Build(),
			expectedMopFailure:      1,
			expectedResourceFailure: 0,
			expectedResourceTotal:   0,
		},
		{
			name: "ResourcesSuccessful",
			mop: kube_test.NewMopBuilder(namespace, name).SetPhase(PhaseSucceeded).
				SetObjectRelatedResourcesPhasesForKind(constants.ServiceKind.Kind, "test", PhaseSucceeded, PhaseSucceeded).Build(),
			expectedMopFailure:      0,
			expectedResourceFailure: 0,
			expectedResourceTotal:   2,
		},
		{
			name: "ResourcesFailed",
			mop: kube_test.NewMopBuilder(namespace, name).SetPhase(PhaseFailed).
				SetObjectRelatedResourcesPhasesForKind(constants.ServiceKind.Kind, "test", PhaseFailed).Build(),
			expectedMopFailure:      1,
			expectedResourceFailure: 1,
			expectedResourceTotal:   1,
		},
		{
			name: "FailedMop (overlay)",
			mop: kube_test.NewMopBuilder(namespace, name).
				SetOverlays([]v1alpha1.Overlay{{Name: "some-vs-name"}}).
				SetPhase(PhaseFailed).
				SetObjectRelatedResourcesPhasesForKind(constants.ServiceKind.Kind, "test", PhaseFailed).Build(),
			isOverlayMop:            true,
			expectedMopFailure:      1,
			expectedResourceFailure: 1,
			expectedResourceTotal:   1,
		},
		{
			name: "ResourcesPartlySuccessful",
			mop: kube_test.NewMopBuilder(namespace, name).SetPhase(PhaseFailed).
				SetObjectRelatedResourcesPhasesForKind(constants.ServiceKind.Kind, "test", PhaseSucceeded, PhaseFailed).Build(),
			expectedMopFailure:      1,
			expectedResourceFailure: 1,
			expectedResourceTotal:   2,
		},
		{
			name:                    "NoStatus",
			mop:                     kube_test.NewMopBuilder(namespace, name).Build(),
			expectedMopFailure:      1,
			expectedResourceFailure: 0,
			expectedResourceTotal:   0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()

			RecordMopRenderingStats(registry, clusterName, tc.mop, constants.ServiceKind.Kind)

			labels := map[string]string{
				"k8s_cluster":       clusterName,
				"k8s_namespace":     namespace,
				"k8s_resource_name": name,
				"kind":              "MeshOperator",
			}

			metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.MeshOperatorResourcesFailed, labels, tc.expectedResourceFailure)
			metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.MeshOperatorResourcesTotal, labels, tc.expectedResourceTotal)

			expectedMopType := "extension"
			if tc.isOverlayMop {
				expectedMopType = "overlay"
			}

			if len(tc.mop.Status.Services) == 0 { // root level failure
				labels := map[string]string{
					"k8s_cluster":              clusterName,
					"k8s_namespace":            namespace,
					"k8s_resource_name":        name,
					"k8s_target_resource_name": "all",
					"mop_type":                 expectedMopType,
					"kind":                     "MeshOperator",
					"k8s_target_kind":          "Service",
				}
				metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.MeshOperatorFailed, labels, tc.expectedMopFailure)
			} else {
				for serviceName, status := range tc.mop.Status.Services {
					labels := map[string]string{
						"k8s_cluster":              clusterName,
						"k8s_namespace":            namespace,
						"k8s_resource_name":        name,
						"k8s_target_resource_name": serviceName,
						"mop_type":                 expectedMopType,
						"kind":                     "MeshOperator",
						"k8s_target_kind":          "Service",
					}
					expectedFailure := 0.0
					if status.Phase != PhaseSucceeded {
						expectedFailure = 1
					}
					metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.MeshOperatorFailed, labels, expectedFailure)
				}
			}
		})
	}
}

func TestEraseRenderingStats(t *testing.T) {
	clusterName := "test-cluster"
	labels := map[string]string{
		"k8s_cluster":       clusterName,
		"k8s_namespace":     namespace,
		"k8s_resource_name": name,
		"kind":              "MeshOperator",
	}
	registry := prometheus.NewRegistry()

	// Fake metrics
	commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.MeshOperatorFailed, labels, registry).Set(1)
	commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.MeshOperatorResourcesTotal, labels, registry).Set(10)
	commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.MeshOperatorResourcesFailed, labels, registry).Set(3)

	EraseMopRenderingStats(registry, clusterName, namespace, name)

	metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.MeshOperatorFailed, labels, 0)
	metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.MeshOperatorResourcesTotal, labels, 0)
	metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.MeshOperatorResourcesFailed, labels, 0)
}

func TestStaleRelatedResources(t *testing.T) {
	testCases := []struct {
		name         string
		oldResources []*v1alpha1.ResourceStatus
		newResources []*v1alpha1.ResourceStatus
		expected     []*v1alpha1.ResourceStatus
	}{
		{
			name:         "ZeroOld_OneNew_OneDiff_ZeroStale",
			newResources: []*v1alpha1.ResourceStatus{{Name: "r1"}},
		},
		{
			name:         "OneOld_OneNew_ZeroDiff_ZeroStale",
			oldResources: []*v1alpha1.ResourceStatus{{Name: "r1"}},
			newResources: []*v1alpha1.ResourceStatus{{Name: "r1"}},
		},
		{
			name:         "OneOld_ZeroNew_OneDiff_OneStale",
			oldResources: []*v1alpha1.ResourceStatus{{Name: "r1"}},
			expected:     []*v1alpha1.ResourceStatus{{Name: "r1"}},
		},
		{
			name:         "OneOld_OneNew_OneDiff_OneStale",
			oldResources: []*v1alpha1.ResourceStatus{{Name: "r1"}},
			newResources: []*v1alpha1.ResourceStatus{{Name: "r2"}},
			expected:     []*v1alpha1.ResourceStatus{{Name: "r1"}},
		},
		{
			name:         "ZeroOld_MultipleNew_MultipleDiff_ZeroStale",
			newResources: []*v1alpha1.ResourceStatus{{Name: "r1"}, {Name: "r2"}},
		},
		{
			name:         "MultipleOld_MultipleNew_ZeroDiff_ZeroStale",
			oldResources: []*v1alpha1.ResourceStatus{{Name: "r1"}, {Name: "r2"}},
			newResources: []*v1alpha1.ResourceStatus{{Name: "r1"}, {Name: "r2"}},
		},
		{
			name:         "MultipleOld_ZeroNew_ZeroDiff_MultipleStale",
			oldResources: []*v1alpha1.ResourceStatus{{Name: "r1"}, {Name: "r2"}},
			expected:     []*v1alpha1.ResourceStatus{{Name: "r1"}, {Name: "r2"}},
		},
		{
			name:         "MultipleOld_MultipleNew_OneDiff_OneStale",
			oldResources: []*v1alpha1.ResourceStatus{{Name: "r1"}, {Name: "r2"}},
			newResources: []*v1alpha1.ResourceStatus{{Name: "r2"}, {Name: "r3"}},
			expected:     []*v1alpha1.ResourceStatus{{Name: "r1"}},
		},
		{
			name:         "MultipleOld_MultipleNew_MultipleDiff_MultipleStale",
			oldResources: []*v1alpha1.ResourceStatus{{Name: "r1"}, {Name: "r2"}},
			newResources: []*v1alpha1.ResourceStatus{{Name: "r3"}, {Name: "r4"}},
			expected:     []*v1alpha1.ResourceStatus{{Name: "r1"}, {Name: "r2"}},
		},
		{
			name:         "Stale resource in different ns",
			oldResources: []*v1alpha1.ResourceStatus{{Namespace: "ns-1", Name: "r1"}},
			newResources: []*v1alpha1.ResourceStatus{{Namespace: "ns-2", Name: "r1"}},
			expected:     []*v1alpha1.ResourceStatus{{Namespace: "ns-1", Name: "r1"}},
		},
		{
			name:         "Multiple stale resources in different ns",
			oldResources: []*v1alpha1.ResourceStatus{{Namespace: "ns-1", Name: "r1"}, {Namespace: "ns-1", Name: "r2"}, {Namespace: "ns-1", Name: "r3"}},
			newResources: []*v1alpha1.ResourceStatus{{Namespace: "ns-1", Name: "r1"}, {Namespace: "ns-2", Name: "r2"}},
			expected:     []*v1alpha1.ResourceStatus{{Namespace: "ns-1", Name: "r2"}, {Namespace: "ns-1", Name: "r3"}},
		},
		{
			name:         "Stale resource of different kind, same name",
			oldResources: []*v1alpha1.ResourceStatus{{Kind: "kind-1", Name: "r1"}},
			newResources: []*v1alpha1.ResourceStatus{{Kind: "kind-2", Name: "r1"}},
			expected:     []*v1alpha1.ResourceStatus{{Kind: "kind-1", Name: "r1"}},
		},
		{
			name:         "Empty namespace reference",
			oldResources: []*v1alpha1.ResourceStatus{{Kind: "kind-1", Name: "r1"}},
			newResources: []*v1alpha1.ResourceStatus{{Kind: "kind-1", Namespace: namespace, Name: "r1"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			oldMop := kube_test.
				NewMopBuilder(namespace, "oldMop").SetRelatedResourcesInStatus(PhaseSucceeded, tc.oldResources).
				Build()
			newMop := kube_test.
				NewMopBuilder(namespace, "newMop").SetRelatedResourcesInStatus(PhaseSucceeded, tc.newResources).
				Build()
			actualResources := identifyStaleRelatedResources(oldMop, newMop)
			assert.Equal(t, tc.expected, actualResources)
		})
	}
}

// TestIsMopValid checks various length constraints on mesh operator fields.
func TestIsMopValid(t *testing.T) {
	testOverlay := v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: retriesOverlayBytes,
		}}

	mopWithNameLenViolation := generateString(constants.MaxMopNameLenAllowed + 1)
	mopWithFilterLenViolation := createMopWithGivenFilterLen(constants.MaxAllowedFilters + 1)
	mopWithMaxNameLength := generateString(constants.MaxMopNameLenAllowed)
	mopWithMaxNumberOfAllowedFilters := createMopWithGivenFilterLen(constants.MaxAllowedFilters)

	extensionMopWithInvalidServiceSelector := kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("invalid", "istio-ordering-").AddFilter(testFaultFilter).Build()
	overlayMopWithInvalidServiceSelector := kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("invalid", "istio-ordering-").AddOverlay(testOverlay).Build()
	extensionMopWithValidServiceSelector := kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("valid", "istio-ordering").AddFilter(testFaultFilter).Build()
	overlayMopWithValidServiceSelector := kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("valid", "istio-ordering").AddOverlay(testOverlay).Build()

	testCases := []struct {
		name          string
		mop           *v1alpha1.MeshOperator
		expectedError string
	}{
		{
			name:          "Invalid_MOP_NameLengthViolation",
			mop:           kube_test.NewMopBuilder(namespace, mopWithNameLenViolation).SetPhase(PhaseSucceeded).Build(),
			expectedError: "MOP metadata.name should not be more than 238 characters",
		},
		{
			name:          "Invalid_MOP_FilterLengthViolation",
			mop:           mopWithFilterLenViolation,
			expectedError: "mop.spec.extensions length should not be more than 1000",
		},
		{
			name:          "Invalid_Extension_MOP_ServiceSelectorViolation",
			mop:           extensionMopWithInvalidServiceSelector,
			expectedError: "invalid selector value: `istio-ordering-`, 'a valid label must be an empty string or consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyValue',  or 'my_value',  or '12345', regex used for validation is '(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?')'",
		},
		{
			name:          "Invalid_Overlays_MOP_ServiceSelectorViolation",
			mop:           overlayMopWithInvalidServiceSelector,
			expectedError: "invalid selector value: `istio-ordering-`, 'a valid label must be an empty string or consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyValue',  or 'my_value',  or '12345', regex used for validation is '(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?')'",
		},
		{
			name:          "Valid_Extension_MOP_ServiceSelector",
			mop:           extensionMopWithValidServiceSelector,
			expectedError: "",
		},
		{
			name:          "Valid_Overlays_MOP_ServiceSelector",
			mop:           overlayMopWithValidServiceSelector,
			expectedError: "",
		},
		{
			name:          "Valid_MOP_WithMaxAllowedNameLength",
			mop:           kube_test.NewMopBuilder(namespace, mopWithMaxNameLength).SetPhase(PhaseSucceeded).Build(),
			expectedError: "",
		},
		{
			name:          "Valid_MOP_WithMaxNumberOfAllowedFilters",
			mop:           mopWithMaxNumberOfAllowedFilters,
			expectedError: "",
		},
		{
			name:          "Valid_MOP",
			mop:           kube_test.NewMopBuilder(namespace, name).SetPhase(PhaseSucceeded).Build(),
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateExtensionsMop(tc.mop)
			if tc.expectedError != "" {
				assert.Equal(t, tc.expectedError, err.Error())
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

// TestGenerateConfig stubs actual config generation and validates that all config-generation scenarios are handled.
func TestGenerateConfig(t *testing.T) {
	features.EnableServiceConfigOverlays = true

	renderer := &templating_test.FixedResultRenderer{}
	recorder := &record.FakeRecorder{}
	registry := prometheus.NewRegistry()
	testableQueue := &TestableQueue{}

	testableResourceManager := &resources_test.FakeResourceManager{}
	applicator := &templating_test.TestableApplicator{}
	logger := zaptest.NewLogger(t).Sugar()

	controller := NewMeshOperatorController(
		logger,
		&controller_test.FakeNamespacesNamespaceFilter{},
		recorder,
		applicator,
		renderer,
		registry,
		"primaryCluster",
		kube_test.NewKubeClientBuilder().Build(),
		kube_test.NewKubeClientBuilder().Build(),
		NewSingleQueueEnqueuer(testableQueue),
		&NoOpEnqueuer{},
		&NoOpEnqueuer{},
		testableResourceManager,
		false,
		true,
		&NoOpReconcileManager{},
		nil,
		&kube_test.FakeTimeProvider{}).(interface{}).(*meshOperatorController)

	testCases := []struct {
		name            string
		object          *unstructured.Unstructured
		mop             *v1alpha1.MeshOperator
		doSvcOverlays   bool
		featureDisabled bool
		mopError        error
		svcOverlayError error
	}{
		{
			name:   "Mop and service present",
			object: testSvc,
			mop:    testMop,
		},
		{
			name:   "Mop and SE present",
			object: testSE,
			mop:    testMop,
		},
		{
			name: "No service/SE present",
			mop:  testMop,
		},
		{
			name:     "Error while generating mop config",
			mop:      testMop,
			mopError: fmt.Errorf("test error"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.EnableServiceConfigOverlays = !tc.featureDisabled

			mopConfigGenerator := &fakeConfigGenerator{}
			if tc.mopError != nil {
				mopConfigGenerator.errorToThrow = tc.mopError
			}
			controller.configGenerator = mopConfigGenerator

			_, _, err := controller.generateConfig(tc.mop, tc.object, logger)
			assert.True(t, mopConfigGenerator.wasCalled)

			if tc.mopError != nil {
				assert.Equal(t, tc.mopError, err)
			} else if tc.svcOverlayError != nil {
				assert.Equal(t, tc.svcOverlayError, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}

}

func TestValidateMopRecord(t *testing.T) {
	// Type of violation doesn't matter. We're testing behavior of the method
	invalidVsOverlayBytes, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]string{},
		},
		"spec": map[string]interface{}{
			"something": "something",
		},
	})

	invalidMop := kube_test.NewMopBuilder(namespace, "test-mop").
		AddOverlay(v1alpha1.Overlay{
			Name: "test-vs-1",
			Kind: "VirtualService",
			StrategicMergePatch: runtime.RawExtension{
				Raw: invalidVsOverlayBytes,
			}}).
		Build()

	testCases := []struct {
		name          string
		mop           *v1alpha1.MeshOperator
		errorExpected bool
	}{
		{
			name:          "Invalid MOP",
			mop:           invalidMop,
			errorExpected: true,
		},
		{
			name:          "Valid MOP",
			mop:           testMop,
			errorExpected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOverlayingMop(tc.mop)

			if tc.errorExpected {
				assert.Error(t, err)
				assert.True(t, error2.IsUserConfigError(err))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetListOfServicesNoLongerSelectedByNewMop(t *testing.T) {
	testOverlay := v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: retriesOverlayBytes,
		}}

	testCases := []struct {
		name                  string
		mopWithOldStatus      *v1alpha1.MeshOperator
		mopWithNewStatus      *v1alpha1.MeshOperator
		expectedListOfService []string
	}{
		{
			name:                  "MultipleServiceSelector_To_SingleServiceSelector(Old - [svc1, svc2, svc3],  New - [svc1]) ",
			mopWithOldStatus:      kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app_instance", "istio-shipping").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).SetPhaseService("svc2", PhaseSucceeded).SetPhaseService("svc3", PhaseSucceeded).Build(),
			mopWithNewStatus:      kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app_instance", "istio-shipping1").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).Build(),
			expectedListOfService: []string{"svc2", "svc3"},
		},
		{
			name:                  "ServiceSelectorUpdate(Old - [svc2], New - [svc1])",
			mopWithOldStatus:      kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app_instance", "istio-shipping2").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc2", PhaseSucceeded).Build(),
			mopWithNewStatus:      kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app_instance", "istio-shipping1").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).Build(),
			expectedListOfService: []string{"svc2"},
		},
		{
			name:                  "MultipleServiceSelector_To_NoService_Selector",
			mopWithOldStatus:      kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app_instance", "istio-shipping").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).SetPhaseService("svc2", PhaseSucceeded).Build(),
			mopWithNewStatus:      kube_test.NewMopBuilder("test-namespace", "test-mop").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).Build(),
			expectedListOfService: []string{"svc1", "svc2"},
		},
		{
			name:                  "NoServiceSelector_ToMultipleServiceSelector",
			mopWithOldStatus:      kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app", "istio-shipping").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).Build(),
			mopWithNewStatus:      kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app", "istio-shipping").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).SetPhaseService("svc2", PhaseSucceeded).Build(),
			expectedListOfService: nil,
		},
		{
			name:                  "ServiceSelectorUnchanged",
			mopWithOldStatus:      kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app", "istio-shipping1").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).Build(),
			mopWithNewStatus:      kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app", "istio-shipping1").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).Build(),
			expectedListOfService: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			serviceNoLongerSelectedByMop := getServicesNotSelectedByNewMop(tc.mopWithOldStatus, tc.mopWithNewStatus)
			assert.ElementsMatch(t, tc.expectedListOfService, serviceNoLongerSelectedByMop)
		})
	}
}

func TestMopStatusForRelatedResources(t *testing.T) {
	mopNamespace := "mop-namespace"
	resourceName := mutatorPrefix + filterName
	message := "all resources generated successfully"

	testCases := []struct {
		name           string
		object         *unstructured.Unstructured
		appliedResults []*templating.AppliedConfigObject
		expectedStatus v1alpha1.MeshOperatorStatus
		expectedErr    error
	}{
		{
			name:           "Related resource for object from same namespace",
			object:         testSvc,
			appliedResults: getAppliedResultWithNs(testSvc.GetNamespace(), nil),
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Services: map[string]*v1alpha1.ServiceStatus{
					testSvc.GetName(): {
						Phase:   PhaseSucceeded,
						Message: message,
						RelatedResources: []*v1alpha1.ResourceStatus{
							{
								Phase:     PhaseSucceeded,
								Name:      resourceName,
								Namespace: testSvc.GetNamespace(),
							},
						},
					},
				},
			},
		},
		{
			name:           "Related resource for object from different namespace",
			object:         testSvc,
			appliedResults: getAppliedResultWithNs("some-other-ns", nil),
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Services: map[string]*v1alpha1.ServiceStatus{
					testSvc.GetName(): {
						Phase:   PhaseSucceeded,
						Message: message,
						RelatedResources: []*v1alpha1.ResourceStatus{
							{
								Phase:     PhaseSucceeded,
								Name:      resourceName,
								Namespace: "some-other-ns",
							},
						},
					},
				},
			},
		},
		{
			name:           "Related resource for object from with no namespace set",
			object:         testSvc,
			appliedResults: getAppliedResultWithNs("", nil),
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Services: map[string]*v1alpha1.ServiceStatus{
					testSvc.GetName(): {
						Phase:   PhaseSucceeded,
						Message: message,
						RelatedResources: []*v1alpha1.ResourceStatus{
							{
								Phase:     PhaseSucceeded,
								Name:      resourceName,
								Namespace: mopNamespace,
							},
						},
					},
				},
			},
		},
		{
			name:           "Related resource for mop with no service with namespace set",
			appliedResults: getAppliedResultWithNs("some-ns", nil),
			object:         nil,
			expectedStatus: v1alpha1.MeshOperatorStatus{
				RelatedResources: []*v1alpha1.ResourceStatus{
					{
						Phase:     PhaseSucceeded,
						Name:      resourceName,
						Namespace: "some-ns",
					},
				},
			},
		},
		{
			name:           "Related resource for mop with no service with no namespace set",
			appliedResults: getAppliedResultWithNs("", nil),
			object:         nil,
			expectedStatus: v1alpha1.MeshOperatorStatus{
				RelatedResources: []*v1alpha1.ResourceStatus{
					{
						Phase:     PhaseSucceeded,
						Name:      resourceName,
						Namespace: mopNamespace,
					},
				},
			},
		},
		{
			name:           "Conflict error",
			object:         testSvc,
			appliedResults: getAppliedResultWithNs(testSvc.GetNamespace(), &errors.StatusError{ErrStatus: metav1.Status{Reason: "Conflict"}}),
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Phase:   PhaseFailed,
				Message: "Conflict error while updating resources",
				Services: map[string]*v1alpha1.ServiceStatus{
					testSvc.GetName(): {
						Phase:   PhaseFailed,
						Message: "Conflict error while updating resources",
						RelatedResources: []*v1alpha1.ResourceStatus{
							{
								Phase:     PhaseFailed,
								Name:      resourceName,
								Namespace: testSvc.GetNamespace(),
							},
						},
					},
				},
			},
			expectedErr: &error2.NonCriticalReconcileError{Message: "Conflict error while updating resources"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mop := kube_test.NewMopBuilder(mopNamespace, name).Build()
			updatedMop, err := updateStatusForRelatedResources(mop, tc.object, tc.appliedResults)
			if tc.expectedErr != nil {
				assert.Equal(t, tc.expectedErr, err)
			} else {
				assert.Nil(t, err)
			}
			assert.Equal(t, tc.expectedStatus, updatedMop.Status)
		})
	}

}

func TestAppendPendingResources(t *testing.T) {
	existingResource := &v1alpha1.ResourceStatus{
		Phase:      PhaseSucceeded,
		Name:       "existing-resource",
		Namespace:  "some-ns",
		ApiVersion: "networking.istio.io/v1alpha3",
		Kind:       "EnvoyFilter",
	}
	newResource := &v1alpha1.ResourceStatus{
		Phase:      PhasePending,
		Name:       "new-resource",
		Namespace:  "new-resource-namespace",
		ApiVersion: "networking.istio.io/v1alpha3",
		Kind:       "EnvoyFilter",
	}

	newResourceObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "EnvoyFilter",
			"metadata": map[string]interface{}{
				"name":      "new-resource",
				"namespace": "new-resource-namespace",
			},
		},
	}

	noResourcesMop := &v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mop1",
			Namespace: "some-ns",
		},
		Status: v1alpha1.MeshOperatorStatus{
			Phase: PhasePending,
		},
	}

	mopWithResources := &v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mop1",
			Namespace: "some-ns",
		},
		Status: v1alpha1.MeshOperatorStatus{
			Phase:            PhasePending,
			RelatedResources: []*v1alpha1.ResourceStatus{existingResource},
		},
	}

	mopWithTheSameResources := &v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mop1",
			Namespace: "some-ns",
		},
		Status: v1alpha1.MeshOperatorStatus{
			Phase:            PhasePending,
			RelatedResources: []*v1alpha1.ResourceStatus{newResource},
		},
	}

	svcMopWithResources := &v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mop1",
			Namespace: "some-ns",
		},
		Status: v1alpha1.MeshOperatorStatus{
			Phase: PhasePending,
			Services: map[string]*v1alpha1.ServiceStatus{
				"svc1": {
					Phase:            PhaseSucceeded,
					RelatedResources: []*v1alpha1.ResourceStatus{existingResource},
				},
			},
		},
	}

	svcMopWithSameResources := &v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mop1",
			Namespace: "some-ns",
		},
		Status: v1alpha1.MeshOperatorStatus{
			Phase: PhasePending,
			Services: map[string]*v1alpha1.ServiceStatus{
				"svc1": {
					Phase:            PhaseSucceeded,
					RelatedResources: []*v1alpha1.ResourceStatus{newResource},
				},
			},
		},
	}

	testCases := []struct {
		name           string
		mop            *v1alpha1.MeshOperator
		object         *unstructured.Unstructured
		config         *templating.GeneratedConfig
		expectedStatus *v1alpha1.MeshOperatorStatus
	}{
		{
			name:   "NS level MOP. No resources.",
			mop:    noResourcesMop,
			object: nil,
			config: templating.UnflattenConfig([]*unstructured.Unstructured{newResourceObj}, "some-template"),
			expectedStatus: &v1alpha1.MeshOperatorStatus{
				Phase:            PhasePending,
				RelatedResources: []*v1alpha1.ResourceStatus{newResource},
			},
		},
		{
			name:   "NS level MOP. With existing resources.",
			mop:    mopWithResources,
			object: nil,
			config: templating.UnflattenConfig([]*unstructured.Unstructured{newResourceObj}, "some-template"),
			expectedStatus: &v1alpha1.MeshOperatorStatus{
				Phase:            PhasePending,
				RelatedResources: []*v1alpha1.ResourceStatus{existingResource, newResource},
			},
		},
		{
			name:   "NS level MOP. With the same existing resources.",
			mop:    mopWithTheSameResources,
			object: nil,
			config: templating.UnflattenConfig([]*unstructured.Unstructured{newResourceObj}, "some-template"),
			expectedStatus: &v1alpha1.MeshOperatorStatus{
				Phase:            PhasePending,
				RelatedResources: []*v1alpha1.ResourceStatus{newResource},
			},
		},
		{
			name:   "Svc level MOP. No resources.",
			mop:    noResourcesMop,
			object: testSvc,
			config: templating.UnflattenConfig([]*unstructured.Unstructured{newResourceObj}, "some-template"),
			expectedStatus: &v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				Services: map[string]*v1alpha1.ServiceStatus{
					"svc1": {
						Phase:            PhasePending,
						RelatedResources: []*v1alpha1.ResourceStatus{newResource},
					},
				},
			},
		},
		{
			name:   "Svc level MOP. With existing resources.",
			mop:    svcMopWithResources,
			object: testSvc,
			config: templating.UnflattenConfig([]*unstructured.Unstructured{newResourceObj}, "some-template"),
			expectedStatus: &v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				Services: map[string]*v1alpha1.ServiceStatus{
					"svc1": {
						Phase:            PhasePending,
						RelatedResources: []*v1alpha1.ResourceStatus{existingResource, newResource},
					},
				},
			},
		},
		{
			name:   "Svc level MOP. With the same existing resources.",
			mop:    svcMopWithSameResources,
			object: testSvc,
			config: templating.UnflattenConfig([]*unstructured.Unstructured{newResourceObj}, "some-template"),
			expectedStatus: &v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				Services: map[string]*v1alpha1.ServiceStatus{
					"svc1": {
						Phase:            PhasePending,
						RelatedResources: []*v1alpha1.ResourceStatus{newResource},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mop := tc.mop.DeepCopy()
			appendPendingResources(mop, tc.object, tc.config)

			assert.Equal(t, tc.expectedStatus, &mop.Status)
		})
	}
}

func TestMopInitStatus(t *testing.T) {
	testOverlay := v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: retriesOverlayBytes,
		}}

	testFaultFilter = v1alpha1.ExtensionElement{
		FaultFilter: &v1alpha1.HttpFaultFilter{Abort: &v1alpha1.HttpFaultFilterAbort{HttpStatus: kube_test.DefaultAbortHttpStatus}},
	}

	mopNamespace := "mop-namespace"

	testSvc := kube_test.CreateServiceWithLabels(mopNamespace, "svc1", map[string]string{})

	seRoutingDisabled := kube_test.CreateServiceEntryWithLabels("some-namespace", "se0", nil)
	seRoutingDisabled.SetAnnotations(map[string]string{"routing.mesh.io/enabled": "false"})

	seRoutingEnabled := kube_test.CreateServiceEntryWithLabels("some-namespace", "se", nil)

	testCases := []struct {
		name           string
		services       []*corev1.Service
		serviceEntries []*istiov1alpha3.ServiceEntry
		mop            *v1alpha1.MeshOperator
		expectedStatus v1alpha1.MeshOperatorStatus
	}{
		{
			name: "No service/SE selected",
			mop:  kube_test.NewMopBuilder(namespace, "testmop").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).Build(),
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Phase:   PhaseSucceeded,
				Message: noServicesForSelectorMessage,
			},
		},
		{
			name:     "MOP selects service",
			services: []*corev1.Service{testSvc},
			mop:      kube_test.NewMopBuilder(namespace, "testmop").SetPhase(PhaseSucceeded).Build(),
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				Services: map[string]*v1alpha1.ServiceStatus{
					testSvc.Name: {
						Phase: PhasePending,
					},
				},
			},
		},
		{
			name:           "MOP selects SE",
			mop:            kube_test.NewMopBuilder(namespace, "testmop").SetPhase(PhaseSucceeded).Build(),
			serviceEntries: []*istiov1alpha3.ServiceEntry{seRoutingEnabled},
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				ServiceEntries: map[string]*v1alpha1.ServiceStatus{
					seRoutingEnabled.Name: {
						Phase: PhasePending,
					},
				},
			},
		},
		{
			name:           "Overlay MOP selects (Service + Routing Disabled SE) - Ignore routing-disabled SE",
			services:       []*corev1.Service{testSvc},
			mop:            kube_test.NewMopBuilder(namespace, "testmop").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).Build(),
			serviceEntries: []*istiov1alpha3.ServiceEntry{seRoutingDisabled},
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				Services: map[string]*v1alpha1.ServiceStatus{
					testSvc.Name: {
						Phase: PhasePending,
					},
				},
			},
		},
		{
			name:           "Extension MOP selects (Service + Routing Disabled SE) - routing disabled SE shows on MOP status",
			services:       []*corev1.Service{testSvc},
			mop:            kube_test.NewMopBuilder(namespace, "testmop").AddFilter(testFaultFilter).SetPhase(PhaseSucceeded).Build(),
			serviceEntries: []*istiov1alpha3.ServiceEntry{seRoutingDisabled},
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				Services: map[string]*v1alpha1.ServiceStatus{
					testSvc.Name: {
						Phase: PhasePending,
					},
				},
				ServiceEntries: map[string]*v1alpha1.ServiceStatus{
					seRoutingDisabled.Name: {
						Phase: PhasePending,
					},
				},
			},
		},
		{
			name:           "Overlay MOP selects  (Service + Routing Enabled SE)",
			services:       []*corev1.Service{testSvc},
			mop:            kube_test.NewMopBuilder(namespace, "testmop").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).Build(),
			serviceEntries: []*istiov1alpha3.ServiceEntry{seRoutingEnabled},
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				Services: map[string]*v1alpha1.ServiceStatus{
					testSvc.Name: {
						Phase: PhasePending,
					},
				},
				ServiceEntries: map[string]*v1alpha1.ServiceStatus{
					seRoutingEnabled.Name: {
						Phase: PhasePending,
					},
				},
			},
		},
		{
			name:           "Extension MOP selects  (Service + Routing Enabled SE)",
			services:       []*corev1.Service{testSvc},
			mop:            kube_test.NewMopBuilder(namespace, "testmop").AddFilter(testFaultFilter).SetPhase(PhaseSucceeded).Build(),
			serviceEntries: []*istiov1alpha3.ServiceEntry{seRoutingEnabled},
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				Services: map[string]*v1alpha1.ServiceStatus{
					testSvc.Name: {
						Phase: PhasePending,
					},
				},
				ServiceEntries: map[string]*v1alpha1.ServiceStatus{
					seRoutingEnabled.Name: {
						Phase: PhasePending,
					},
				},
			},
		},
		{
			name:           "Overlay MOP selects (Routing Enabled SE + Routing disabled SE)",
			mop:            kube_test.NewMopBuilder(namespace, "testmop").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).Build(),
			serviceEntries: []*istiov1alpha3.ServiceEntry{seRoutingEnabled, seRoutingDisabled},
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				ServiceEntries: map[string]*v1alpha1.ServiceStatus{
					seRoutingEnabled.Name: {
						Phase: PhasePending,
					},
				},
			},
		},
		{
			name:           "Extension MOP selects (Routing Enabled SE + Routing disabled SE)",
			mop:            kube_test.NewMopBuilder(namespace, "testmop").AddFilter(testFaultFilter).SetPhase(PhaseSucceeded).Build(),
			serviceEntries: []*istiov1alpha3.ServiceEntry{seRoutingEnabled, seRoutingDisabled},
			expectedStatus: v1alpha1.MeshOperatorStatus{
				Phase: PhasePending,
				ServiceEntries: map[string]*v1alpha1.ServiceStatus{
					seRoutingEnabled.Name: {
						Phase: PhasePending,
					},
					seRoutingDisabled.Name: {
						Phase: PhasePending,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			updatedMop := initStatus(tc.mop, tc.services, tc.serviceEntries)
			assert.Equal(t, tc.expectedStatus, updatedMop.Status)
		})
	}

}

func getAppliedResultWithNs(ns string, err error) []*templating.AppliedConfigObject {
	return []*templating.AppliedConfigObject{
		{
			Object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      mutatorPrefix + filterName,
						"namespace": ns,
					},
				},
			},
			Error: err,
		},
	}
}

func TestIsConfigNamespaceExtension(t *testing.T) {
	testMeshConfigNamespace := "testMeshConfigNamespace"
	testIngressConfigNamespace := "testIngresConfigNamespace"

	testCases := []struct {
		name                     string
		extensionFieldName       string
		isConfigNsEnabledPresent bool
		expectedConfigNamespace  string
	}{
		{
			name:                     "extension type has no ConfigNamespaceEnabled present",
			extensionFieldName:       "ServerMaxOutboundFrames",
			isConfigNsEnabledPresent: false,
			expectedConfigNamespace:  "",
		},
		{
			name:                     "MeshConfigNamespaceEnabledExtension",
			extensionFieldName:       "ActiveHealthCheckFilter",
			isConfigNsEnabledPresent: true,
			expectedConfigNamespace:  testMeshConfigNamespace,
		},
		{
			name:                     "IngressConfigNamespaceEnabledExtension",
			extensionFieldName:       "IngressRequestHeaders",
			isConfigNsEnabledPresent: true,
			expectedConfigNamespace:  testIngressConfigNamespace,
		},
		{
			name:                     "empty extension",
			isConfigNsEnabledPresent: false,
			expectedConfigNamespace:  "",
		},
	}

	defer func() {
		features.ConfigNamespace = constants.DefaultConfigNamespace
		features.IngressConfigNamespace = constants.DefaultConfigNamespace
	}()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.ConfigNamespace = testMeshConfigNamespace
			features.IngressConfigNamespace = testIngressConfigNamespace

			isConfigNsEnabledExtension, configNamespace := templating.IsConfigNamespaceExtension(tc.extensionFieldName)
			assert.Equal(t, isConfigNsEnabledExtension, tc.isConfigNsEnabledPresent)
			assert.Equal(t, tc.expectedConfigNamespace, configNamespace)
		})
	}
}

func TestAddMopExtensionIndexer(t *testing.T) {

	mopWithExtension := kube_test.
		NewMopBuilder(namespace, "mop1").
		AddSelector("psn", "shipping").
		AddFilter(testFaultFilter).
		Build()

	mopWithNoExtensions := kube_test.
		NewMopBuilder(namespace, "mop1").
		AddSelector("psn", "shipping").
		Build()

	testCases := []struct {
		name                  string
		mop                   *v1alpha1.MeshOperator
		extensionName         string
		expectedObjectsLength int
		expectedObjects       []interface{}
	}{
		{
			name:                  "mop with no extensions",
			mop:                   mopWithNoExtensions,
			extensionName:         "faultInjection",
			expectedObjectsLength: 0,
			expectedObjects:       []interface{}{},
		},
		{
			name:                  "mop with extension",
			mop:                   mopWithExtension,
			extensionName:         "faultInjection",
			expectedObjectsLength: 1,
			expectedObjects:       []interface{}{mopWithExtension},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			fakeClient := &kube_test.FakeClient{}
			fakeClient.InitFactory()

			mopInformer := fakeClient.MopInformerFactory().Mesh().V1alpha1().MeshOperators().Informer()

			err := addMopExtensionIndexer(mopInformer)
			assert.Nil(t, err)

			err = mopInformer.GetIndexer().Add(tc.mop)
			assert.Nil(t, err)

			objects, err := mopInformer.GetIndexer().ByIndex(constants.ExtensionIndexName, tc.extensionName)
			assert.Nil(t, err)
			assert.Equal(t, tc.expectedObjectsLength, len(objects))
			assert.Equal(t, tc.expectedObjects, objects)

		})
	}
}

type fakeConfigGenerator struct {
	wasCalled    bool
	errorToThrow error
}

func (g *fakeConfigGenerator) GenerateConfig(_ *templating.RenderRequestContext, _ OnBeforeApply, _ *zap.SugaredLogger) (
	[]*templating.AppliedConfigObject, error) {
	g.wasCalled = true
	if g.errorToThrow != nil {
		return nil, g.errorToThrow
	}
	return nil, nil
}

func generateString(n int) string {
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		b[i] = 'a'
	}
	return string(b)
}

func createMopWithGivenFilterLen(n int) *v1alpha1.MeshOperator {
	var filters []v1alpha1.ExtensionElement
	for i := 0; i < n; i++ {
		filters = append(filters, v1alpha1.ExtensionElement{})
	}
	mop := &v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: v1alpha1.MeshOperatorSpec{
			Extensions: filters,
		},
	}
	return mop
}
