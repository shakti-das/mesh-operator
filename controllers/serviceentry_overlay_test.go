package controllers

import (
	"context"
	"fmt"
	"testing"

	meshOpErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controller_test"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/resources_test"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating_test"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	testSeName         = "good-object"
	testNsName         = "test-namespace"
	testServiceEntry   = kube_test.CreateServiceEntryWithLabels(testNsName, testSeName, map[string]string{"key-1": "val-2"})
	mopSeStatusSuccess = v1alpha1.MeshOperatorStatus{
		Phase: PhaseSucceeded,
		ServiceEntries: map[string]*v1alpha1.ServiceStatus{
			testSeName: {
				Phase: PhaseSucceeded,
			},
		},
	}
)

func TestGenerateConfigWithOverlay(t *testing.T) {

	mopWithRetries := kube_test.NewMopBuilder(serviceNamespace, "mop1").AddSelector("key-1", "val-2").AddOverlay(retriesOverlay).Build()
	mopWithTimeout := kube_test.NewMopBuilder(serviceNamespace, "mop1.1").AddSelector("key-1", "val-2").AddOverlay(timeoutOverlay).Build()
	invalidMopOverlay := kube_test.NewMopBuilder(serviceNamespace, "mop5").AddSelector("key-1", "val-2").AddOverlay(invalidOverlay).Build()
	badMopOverlay := kube_test.NewMopBuilder(serviceNamespace, "mop3").AddSelector("key-1", "val-2").AddOverlay(badOverlay).Build()
	mopWithGoodThenBadOverlays := kube_test.NewMopBuilder(serviceNamespace, "mop5").AddSelector("key-1", "val-2").AddOverlay(retriesOverlay).AddOverlay(badOverlay).Build()

	renderedVs := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      "test-vs-1",
				"namespace": serviceNamespace,
			},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"name": "test-route-1",
					},
				},
			},
		},
	}

	overlaidVSWithTimeoutAndRetries := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      "test-vs-1",
				"namespace": serviceNamespace,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"name":    "test-route-1",
						"retries": "100",
						"timeout": "7s",
					},
				},
			},
		},
	}

	overlaidVSWithRetries := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      "test-vs-1",
				"namespace": serviceNamespace,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"name":    "test-route-1",
						"retries": "100",
					},
				},
			},
		},
	}

	testCases := []struct {
		name                 string
		mops                 []*v1alpha1.MeshOperator
		expectedMopStatusMap map[string]v1alpha1.MeshOperatorStatus
		overlayDisabled      bool
		expectedResults      []*templating.AppliedConfigObject
		expectedError        error
	}{
		{
			name: "Overlay not enabled",
			mops: []*v1alpha1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop1").AddSelector("app", "ordering").Build(),
			},
			overlayDisabled: true,
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &renderedVs,
				},
			},
		},
		{
			name: "Overlaying - validation error",
			mops: []*v1alpha1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop5").SetOverlays([]v1alpha1.Overlay{
					invalidOverlay}).AddSelector("app", "ordering").Build(),
			},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{"mop5": validationFailedStatus},
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &renderedVs,
				},
			},
		},
		{
			name: "Bad Overlay (no match route)",
			mops: []*v1alpha1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop1").SetOverlays([]v1alpha1.Overlay{
					{
						StrategicMergePatch: runtime.RawExtension{
							Raw: invalidDrOverlayBytes,
						},
					},
				}).AddSelector("app", "ordering").Build(),
			},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{"mop1": getMopSeStatusFailed(mopWithRetries.Name, 0)},
			expectedError:        getOverlayingError("mop1", 0, "no matching route found to apply overlay"),
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &renderedVs,
				},
			},
		},
		{
			name:                 "Good Overlay + Invalid Overlay - overlay applied",
			mops:                 []*v1alpha1.MeshOperator{invalidMopOverlay, mopWithRetries},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithRetries.Name: mopSeStatusSuccess, invalidMopOverlay.Name: validationFailedStatus},
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &retriesOverlaidVS,
				},
			},
		},
		{
			name:                 "Bad Overlay + Invalid Overlay - assert base config, MOP status updates",
			mops:                 []*v1alpha1.MeshOperator{badMopOverlay, invalidMopOverlay},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{badMopOverlay.Name: getMopSeStatusFailed(badMopOverlay.Name, 0), invalidMopOverlay.Name: validationFailedStatus},
			expectedError: &meshOpErrors.OverlayingErrorImpl{
				ErrorMap: map[string]*meshOpErrors.OverlayErrorInfo{
					mopWithBadOverlay.Name: {
						OverlayIndex: 0,
						Message:      "no matching route found to apply overlay",
					},
				},
			},
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &renderedVs,
				},
			},
		},
		{
			name:                 "Overlay successfully generated - single mop",
			mops:                 []*v1alpha1.MeshOperator{mopWithRetries},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithRetries.Name: mopSeStatusSuccess},
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &retriesOverlaidVS,
				},
			},
		},
		{
			name:                 "One mop with a good then bad overlay",
			mops:                 []*v1alpha1.MeshOperator{mopWithGoodThenBadOverlays},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithGoodThenBadOverlays.Name: getMopSeStatusFailed(mopWithGoodThenBadOverlays.Name, 1)},
			expectedError:        getOverlayingError(mopWithGoodThenBadOverlays.Name, 1, "no matching route found to apply overlay"),
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &renderedVs,
				},
			},
		},
		{
			name:                 "Multiple mops with overlay applied successfully",
			mops:                 []*v1alpha1.MeshOperator{mopWithRetries, mopWithTimeout},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithRetries.Name: mopSeStatusSuccess, mopWithTimeout.Name: mopSeStatusSuccess},
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &overlaidVSWithTimeoutAndRetries,
				},
			},
		},
		{
			name:                 "Bad mop then good mop - good overlay generated, MOP statuses updated",
			mops:                 []*v1alpha1.MeshOperator{badMopOverlay, mopWithRetries},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{badMopOverlay.Name: getMopSeStatusFailed(badMopOverlay.Name, 0), mopWithRetries.Name: mopSeStatusSuccess},
			expectedError:        getOverlayingError(badMopOverlay.Name, 0, "no matching route found to apply overlay"),
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &overlaidVSWithRetries,
				},
			},
		},
		{
			name:                 "Good mop then bad mop - good overlay generated, MOP statuses updated",
			mops:                 []*v1alpha1.MeshOperator{mopWithRetries, badMopOverlay},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithRetries.Name: mopSeStatusSuccess, badMopOverlay.Name: getMopSeStatusFailed(badMopOverlay.Name, 0)},
			expectedError:        getOverlayingError(badMopOverlay.Name, 0, "no matching route found to apply overlay"),
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &overlaidVSWithRetries,
				},
			},
		},
		{
			name: "Good then bad then another good mop",
			mops: []*v1alpha1.MeshOperator{mopWithRetries, badMopOverlay, mopWithTimeout},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{
				mopWithRetries.Name: mopSeStatusSuccess,
				badMopOverlay.Name:  getMopSeStatusFailed(badMopOverlay.Name, 0),
				mopWithTimeout.Name: mopSeStatusSuccess},
			expectedError: getOverlayingError(badMopOverlay.Name, 0, "no matching route found to apply overlay"),
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &overlaidVSWithTimeoutAndRetries,
				},
			},
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			renderer := &templating_test.FixedResultRenderer{}
			renderer.ResultToReturn = templating.GeneratedConfig{
				Config: map[string][]*unstructured.Unstructured{
					templateType: {
						&renderedVs,
					},
				},
				TemplateType: templateType,
			}

			seObjects := []runtime.Object{
				testServiceEntry,
			}

			mopObjects := []runtime.Object{}
			for _, mopObject := range tc.mops {
				mopObjects = append(mopObjects, mopObject)
			}

			client := kube_test.NewKubeClientBuilder().AddDynamicClientObjects(seObjects...).AddMopClientObjects(mopObjects...).Build()
			registry := prometheus.NewRegistry()
			applicator := &templating_test.TestableApplicator{
				AppliedResults: []templating_test.GeneratedConfigMetadata{},
			}

			mopQueue := createWorkQueue("MeshOperator")
			seQueue := createWorkQueue("SEs")
			seController := NewServiceEntryController(
				logger,
				applicator,
				renderer,
				resources_test.NewFakeResourceManager(),
				&kube_test.TestableEventRecorder{},
				&controller_test.FakeNamespacesNamespaceFilter{},
				registry,
				NewSingleQueueEnqueuer(seQueue),
				NewSingleQueueEnqueuer(mopQueue),
				"primary",
				client,
				client,
				false).(interface{}).(*serviceEntryController)

			features.EnableServiceEntryOverlays = !tc.overlayDisabled

			results, err := seController.generateConfig(testServiceEntry, nil, tc.mops, logger)
			if tc.expectedError != nil {
				assert.Equal(t, tc.expectedError, err)
			} else {
				assert.NoError(t, err)
			}

			// verify mop status update
			for mopName, expectedMopStatus := range tc.expectedMopStatusMap {
				mop, err := seController.client.MopApiClient().MeshV1alpha1().MeshOperators(serviceNamespace).
					Get(context.TODO(), mopName, metav1.GetOptions{})
				assert.NoError(t, err)
				assert.Equal(t, expectedMopStatus, mop.Status)

				assertOverlayMopMetrics(t, expectedMopStatus, mopName, registry, mop, "ServiceEntry")
			}

			assert.ElementsMatch(t, tc.expectedResults, results)
			features.EnableServiceEntryOverlays = false
		})
	}
}

func getMopSeStatusFailed(mopName string, overlayIdx int) v1alpha1.MeshOperatorStatus {
	return v1alpha1.MeshOperatorStatus{
		Phase:   PhaseFailed,
		Message: "issues with applying overlay",
		ServiceEntries: map[string]*v1alpha1.ServiceStatus{
			testSeName: {
				Phase:   PhaseFailed,
				Message: fmt.Sprintf("error encountered when overlaying: mop %s, overlay <%d>: no matching route found to apply overlay", mopName, overlayIdx),
			},
		},
	}
}
