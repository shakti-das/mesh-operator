package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/resources"

	cluster2 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/cluster"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	metricstesting "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics/testing"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controller_test"
	meshOpErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	kubetest "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
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
	mopSvcStatusSuccess = v1alpha1.MeshOperatorStatus{
		Phase: PhaseSucceeded,
		Services: map[string]*v1alpha1.ServiceStatus{
			serviceName: {
				Phase: PhaseSucceeded,
			},
		},
	}

	mopSvcStatusPending = v1alpha1.MeshOperatorStatus{
		Phase: PhasePending,
		Services: map[string]*v1alpha1.ServiceStatus{
			serviceName: {
				Phase: PhasePending,
			},
			"some-other-svc": {
				Phase: PhasePending,
			},
		},
	}

	mopStatusFailed = v1alpha1.MeshOperatorStatus{
		Phase:   PhaseFailed,
		Message: "test-msg",
	}

	mopSvcStatusRootPending = v1alpha1.MeshOperatorStatus{
		Phase: PhasePending,
		Services: map[string]*v1alpha1.ServiceStatus{
			serviceName: {
				Phase: PhaseSucceeded,
			},
			"some-other-svc": {
				Phase: PhasePending,
			},
		},
	}

	validationFailedStatus = v1alpha1.MeshOperatorStatus{
		Phase:   PhaseFailed,
		Message: "overlay 0 is invalid: invalid match object, please make sure it follows Istio VirtualService spec",
	}

	retriesOverlay = v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: retriesOverlayBytes,
		},
	}

	timeoutOverlay = v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: timeoutOverlayBytes,
		},
	}

	badOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				{
					"name": "no-match",
				},
			},
		},
	})

	anotherBadOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				{
					"name": "still-no-match",
				},
			},
		},
	})

	badOverlay = v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: badOverlayBytes,
		},
	}

	anotherBadOverlay = v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: anotherBadOverlayBytes,
		},
	}

	invalidOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				{
					"match": []string{
						"port 123",
					},
				},
			},
		},
	})

	invalidOverlay = v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: invalidOverlayBytes,
		},
	}

	mopWithRetriesOverlay = kube_test.NewMopBuilder(serviceNamespace, "mop1").AddSelector("key-1", "val-2").
				AddOverlay(retriesOverlay).Build()
	mopWithRetriesOverlayWithSuccessStatus = kube_test.NewMopBuilderFrom(mopWithRetriesOverlay).SetStatus(mopSvcStatusSuccess).Build()
	mopWithRetriesOverlayWithFailStatus    = kube_test.NewMopBuilderFrom(mopWithRetriesOverlay).
						SetStatus(getMopSvcStatusFailed(mopWithRetriesOverlay.Name, 0)).Build()

	mopWithTimeoutOverlay = kube_test.NewMopBuilder(serviceNamespace, "mop1.1").AddSelector("key-1", "val-2").
				AddOverlay(timeoutOverlay).Build()

	mopWithOverlay2 = kube_test.NewMopBuilder(serviceNamespace, "mop2").AddSelector("key-1", "val-2").
			AddOverlay(retriesOverlay).Build()
	mopWithOverlay2WithStatus = kube_test.NewMopBuilderFrom(mopWithOverlay2).SetStatus(mopSvcStatusSuccess).Build()

	mopWithBadOverlay = kube_test.NewMopBuilder(serviceNamespace, "mop3").AddSelector("key-1", "val-2").
				AddOverlay(badOverlay).Build()
	mopWithBadOverlayWithStatus = kube_test.NewMopBuilderFrom(mopWithBadOverlay).
					SetStatus(getMopSvcStatusFailed(mopWithBadOverlay.Name, 0)).Build()

	mopWithAnotherBadOverlay = kube_test.NewMopBuilder(serviceNamespace, "mop3.1").AddSelector("key-1", "val-2").
					AddOverlay(anotherBadOverlay).Build()

	mopWithNoOverlay = kube_test.NewMopBuilder(serviceNamespace, "mop0").AddSelector("key-1", "val-2").SetStatus(mopStatusFailed).Build()

	mopWithOverlay1PendingSvcStatus = kube_test.NewMopBuilder(serviceNamespace, "mop4").AddSelector("key-1", "val-2").
					SetStatus(mopSvcStatusPending).AddOverlay(retriesOverlay).SetStatus(mopSvcStatusPending).Build()

	mopWithOverlay1RootStatusPending = kubetest.NewMopBuilderFrom(mopWithOverlay1PendingSvcStatus).SetStatus(mopSvcStatusRootPending).Build()

	mopWithInvalidOverlay = kube_test.NewMopBuilder(serviceNamespace, "mop5").AddSelector("key-1", "val-2").
				AddOverlay(invalidOverlay).Build()

	mopWithInvalidOverlayWithStatus = kubetest.NewMopBuilderFrom(mopWithInvalidOverlay).SetStatus(validationFailedStatus).Build()

	testService = kube_test.CreateServiceWithLabels("test-namespace", "good-object", map[string]string{"key-1": "val-2"})

	testVs = unstructured.Unstructured{
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

	retriesOverlaidVS = unstructured.Unstructured{
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

	timeoutOverlaidVS = unstructured.Unstructured{
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
						"timeout": "7s",
					},
				},
			},
		},
	}

	timeoutAndRetriesOverlaidVS = unstructured.Unstructured{
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
)

func TestConfigAfterOverlayAdds(t *testing.T) {
	mopWithGoodThenBadOverlays := kube_test.NewMopBuilder(serviceNamespace, "mop5").AddSelector("key-1", "val-2").
		AddOverlay(retriesOverlay).AddOverlay(badOverlay).Build()

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
					Object: &testVs,
				},
			},
		},
		{
			name: "Overlaying - validation error",
			mops: []*v1alpha1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop5").SetOverlays([]v1alpha1.Overlay{
					invalidOverlay,
				}).AddSelector("app", "ordering").Build(),
			},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{"mop5": validationFailedStatus},
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &testVs,
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
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{"mop1": getMopSvcStatusFailed(mopWithRetriesOverlay.Name, 0)},
			expectedError:        getOverlayingError(mopWithRetriesOverlayWithFailStatus.Name, 0, "no matching route found to apply overlay"),
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &testVs,
				},
			},
		},
		{
			name:                 "Good Overlay + Invalid Overlay - overlay applied",
			mops:                 []*v1alpha1.MeshOperator{mopWithInvalidOverlay, mopWithRetriesOverlay},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithRetriesOverlay.Name: mopSvcStatusSuccess, mopWithInvalidOverlay.Name: validationFailedStatus},
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &retriesOverlaidVS,
				},
			},
		},
		{
			name:                 "Bad Overlay + Invalid Overlay - assert base config, MOP status updates",
			mops:                 []*v1alpha1.MeshOperator{mopWithBadOverlay, mopWithInvalidOverlay},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithBadOverlay.Name: getMopSvcStatusFailed(mopWithBadOverlay.Name, 0), mopWithInvalidOverlay.Name: validationFailedStatus},
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
					Object: &testVs,
				},
			},
		},
		{
			name:                 "Overlay successfully generated - single mop",
			mops:                 []*v1alpha1.MeshOperator{mopWithRetriesOverlay},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithRetriesOverlay.Name: mopSvcStatusSuccess},
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &retriesOverlaidVS,
				},
			},
		},
		{
			name:                 "One mop with a good then bad overlay",
			mops:                 []*v1alpha1.MeshOperator{mopWithGoodThenBadOverlays},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithGoodThenBadOverlays.Name: getMopSvcStatusFailed(mopWithGoodThenBadOverlays.Name, 1)},
			expectedError:        getOverlayingError(mopWithGoodThenBadOverlays.Name, 1, "no matching route found to apply overlay"),
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &testVs,
				},
			},
		},
		{
			name:                 "Overlay successfully generated - multiple mops",
			mops:                 []*v1alpha1.MeshOperator{mopWithRetriesOverlay, mopWithOverlay2},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithRetriesOverlay.Name: mopSvcStatusSuccess, mopWithOverlay2.Name: mopSvcStatusSuccess},
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &retriesOverlaidVS,
				},
			},
		},
		{
			name:                 "Bad mop then good mop - good overlay generated, MOP statuses updated",
			mops:                 []*v1alpha1.MeshOperator{mopWithBadOverlay, mopWithRetriesOverlay},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithBadOverlay.Name: getMopSvcStatusFailed(mopWithBadOverlay.Name, 0), mopWithRetriesOverlay.Name: mopSvcStatusSuccess},
			expectedError:        getOverlayingError(mopWithBadOverlay.Name, 0, "no matching route found to apply overlay"),
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &retriesOverlaidVS,
				},
			},
		},
		{
			name:                 "Good mop then bad mop - good overlay generated, MOP statuses updated",
			mops:                 []*v1alpha1.MeshOperator{mopWithRetriesOverlay, mopWithBadOverlay},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{mopWithRetriesOverlay.Name: mopSvcStatusSuccess, mopWithBadOverlay.Name: getMopSvcStatusFailed(mopWithBadOverlay.Name, 0)},
			expectedError:        getOverlayingError(mopWithBadOverlay.Name, 0, "no matching route found to apply overlay"),
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &retriesOverlaidVS,
				},
			},
		},
		{
			name: "Differently bad mops - no overlaying, MOP statuses updated",
			mops: []*v1alpha1.MeshOperator{mopWithBadOverlay, mopWithAnotherBadOverlay},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{
				mopWithBadOverlay.Name:        getMopSvcStatusFailed(mopWithBadOverlay.Name, 0),
				mopWithAnotherBadOverlay.Name: getMopSvcStatusFailed(mopWithAnotherBadOverlay.Name, 0),
			},
			expectedError: &meshOpErrors.OverlayingErrorImpl{
				ErrorMap: map[string]*meshOpErrors.OverlayErrorInfo{
					mopWithBadOverlay.Name: {
						OverlayIndex: 0,
						Message:      "no matching route found to apply overlay",
					},
					mopWithAnotherBadOverlay.Name: {
						OverlayIndex: 0,
						Message:      "no matching route found to apply overlay",
					},
				},
			},
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &testVs,
				},
			},
		},
		{
			name: "Good then bad then another good mop",
			mops: []*v1alpha1.MeshOperator{mopWithRetriesOverlay, mopWithBadOverlay, mopWithTimeoutOverlay},
			expectedMopStatusMap: map[string]v1alpha1.MeshOperatorStatus{
				mopWithRetriesOverlay.Name: mopSvcStatusSuccess,
				mopWithBadOverlay.Name:     getMopSvcStatusFailed(mopWithBadOverlay.Name, 0),
				mopWithTimeoutOverlay.Name: mopSvcStatusSuccess,
			},
			expectedError: getOverlayingError(mopWithBadOverlay.Name, 0, "no matching route found to apply overlay"),
			expectedResults: []*templating.AppliedConfigObject{
				{
					Object: &timeoutAndRetriesOverlaidVS,
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
						&testVs,
					},
				},
				TemplateType: templateType,
			}

			serviceObjects := []runtime.Object{
				testService,
			}

			mopObjects := []runtime.Object{}
			for _, mopObject := range tc.mops {
				mopObjects = append(mopObjects, mopObject)
			}

			client := kube_test.NewKubeClientBuilder().AddDynamicClientObjects(serviceObjects...).AddMopClientObjects(mopObjects...).Build()
			registry := prometheus.NewRegistry()
			applicator := &templating_test.TestableApplicator{
				AppliedResults: []templating_test.GeneratedConfigMetadata{},
			}

			mopQueue := createWorkQueue("MeshOperator")
			serviceQueue := createWorkQueue("Services")

			cluster := &Cluster{
				ID:              cluster2.ID("primary"),
				Client:          client,
				primary:         true,
				namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{},
				mopEnqueuer:     NewSingleQueueEnqueuer(mopQueue),
				eventRecorder:   &kubetest.TestableEventRecorder{},
			}

			configGenerator := NewConfigGenerator(
				renderer,
				[]templating.Mutator{&templating.ManagedByMutator{}},
				nil,
				applicator,
				templating.ApplyOverlays,
				zaptest.NewLogger(t).Sugar(),
				registry)

			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			clusterManager.SetPrimaryCluster(cluster)

			svcController := &MulticlusterServiceController{
				logger:                       logger,
				configGenerator:              configGenerator,
				serviceEnqueuer:              NewSingleQueueEnqueuer(serviceQueue),
				clusterManager:               clusterManager,
				primaryClient:                client,
				resourceManager:              resources_test.NewFakeResourceManager(),
				metricsRegistry:              registry,
				multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
			}

			features.EnableServiceConfigOverlays = !tc.overlayDisabled

			results, err := generateConfig(
				logger,
				"primary",
				testService,
				nil,
				nil,
				tc.mops,
				func(logger *zap.SugaredLogger, service *corev1.Service, validationError error, errInfo *meshOpErrors.OverlayErrorInfo, mop *v1alpha1.MeshOperator) error {
					return svcController.updateMopStatusAcrossClusters(logger, service, validationError, errInfo, mop, map[string][]*v1alpha1.MeshOperator{"primary": {mop}})
				},
				svcController.configGenerator, nil)
			if tc.expectedError != nil {
				assert.Equal(t, tc.expectedError, err)
			} else {
				assert.NoError(t, err)
			}

			// verify mop status update
			for mopName, expectedMopStatus := range tc.expectedMopStatusMap {
				mop, err := client.MopApiClient().MeshV1alpha1().MeshOperators(serviceNamespace).
					Get(context.TODO(), mopName, metav1.GetOptions{})
				assert.NoError(t, err)
				assert.Equal(t, expectedMopStatus, mop.Status)

				assertOverlayMopMetrics(t, expectedMopStatus, mopName, registry, mop, "Service")
			}

			assert.ElementsMatch(t, tc.expectedResults, results)
			features.EnableServiceConfigOverlays = false
		})
	}
}

func TestConfigAfterDeletingOverlayingMops(t *testing.T) {
	serviceObjects := []runtime.Object{testService}
	mopWithRetriesDeleted := kube_test.NewMopBuilderFrom(mopWithRetriesOverlay).
		SetFinalizers("mesh.io/mesh-operator", "test-finalizer").
		SetDeletionTimestamp().Build()
	mopWithTimeoutDeleted := kube_test.NewMopBuilderFrom(mopWithTimeoutOverlay).
		SetFinalizers("mesh.io/mesh-operator", "test-finalizer").
		SetDeletionTimestamp().Build()

	testCases := []struct {
		name                       string
		preexistingOverlaidConfigs []runtime.Object
		mopsInCluster              []*v1alpha1.MeshOperator
		configAfterMopDeletion     *unstructured.Unstructured
	}{
		{
			name:                       "Apply then delete one good mop",
			preexistingOverlaidConfigs: []runtime.Object{&retriesOverlaidVS}, // testVs + mopWithRetriesOverlay
			mopsInCluster:              []*v1alpha1.MeshOperator{mopWithRetriesDeleted},
			configAfterMopDeletion:     &testVs,
		},
		{
			name:                       "Apply two good mops, delete one",
			preexistingOverlaidConfigs: []runtime.Object{&timeoutAndRetriesOverlaidVS}, // testVs + mopWithRetriesOverlay + mopWithTimeoutOverlay
			mopsInCluster:              []*v1alpha1.MeshOperator{mopWithTimeoutOverlay, mopWithRetriesDeleted},
			configAfterMopDeletion:     &timeoutOverlaidVS,
		},
		{
			name:                       "Apply two good mops, delete both",
			preexistingOverlaidConfigs: []runtime.Object{&timeoutAndRetriesOverlaidVS}, // testVs + mopWithRetriesOverlay + mopWithTimeoutOverlay
			mopsInCluster:              []*v1alpha1.MeshOperator{mopWithRetriesDeleted, mopWithTimeoutDeleted},
			configAfterMopDeletion:     &testVs,
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
						&testVs,
					},
				},
				TemplateType: templateType,
			}

			clusterName := "primary"
			var mopRuntimeObjs []runtime.Object
			for _, mop := range tc.mopsInCluster {
				mopRuntimeObjs = append(mopRuntimeObjs, mop)
			}

			client := kube_test.NewKubeClientBuilder().
				AddDynamicClientObjects(serviceObjects...).
				AddMopClientObjects(mopRuntimeObjs...).
				AddIstioObjects(tc.preexistingOverlaidConfigs...).Build()

			registry := prometheus.NewRegistry()
			applicator := &templating_test.TestableApplicator{
				AppliedResults: []templating_test.GeneratedConfigMetadata{},
			}

			manager := resources_test.NewFakeResourceManager()
			manager.MsmOnTrack = v1alpha1.MeshServiceMetadata{
				TypeMeta: metav1.TypeMeta{
					APIVersion: v1alpha1.ApiVersion,
					Kind:       v1alpha1.MsmKind.Kind,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "good-object",
					Namespace: namespace,
					Labels:    map[string]string{},
				},
			}

			serviceQueue := createWorkQueue("Services")
			mopQueue := createWorkQueue("MeshOperator")

			cluster := &Cluster{
				ID:              cluster2.ID(clusterName),
				Client:          client,
				primary:         true,
				eventRecorder:   &kubetest.TestableEventRecorder{},
				namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{},
				mopEnqueuer:     NewSingleQueueEnqueuer(mopQueue),
			}

			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			clusterManager.SetPrimaryCluster(cluster)

			configGenerator := NewConfigGenerator(
				renderer,
				[]templating.Mutator{&templating.ManagedByMutator{}},
				nil,
				applicator,
				templating.ApplyOverlays,
				zaptest.NewLogger(t).Sugar(),
				registry)

			svcController := &MulticlusterServiceController{
				logger:                       logger,
				configGenerator:              configGenerator,
				serviceEnqueuer:              NewSingleQueueEnqueuer(serviceQueue),
				clusterManager:               clusterManager,
				primaryClient:                client,
				resourceManager:              resources_test.NewFakeResourceManager(),
				metricsRegistry:              registry,
				multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
			}

			err := client.RunAndWait(stopCh, true, nil)
			if err != nil {
				t.Fatalf("unexpected error while running client: %s", err.Error())
			}

			features.EnableServiceConfigOverlays = true
			defer func() { features.EnableServiceConfigOverlays = false }()

			// verify overlay deletes
			err = createMeshConfig(
				logger,
				false,
				clusterName,
				client,
				true,
				testService,
				resources.ServiceToReference(clusterName, testService),
				map[string]string{},
				func(service *corev1.Service) ([]*v1alpha1.MeshOperator, error) {
					perClusterMap, err := svcController.getMopsForService(map[string]*corev1.Service{clusterName: service})
					return perClusterMap[clusterName], err
				},
				func(logger *zap.SugaredLogger, service *corev1.Service, validationError error, errInfo *meshOpErrors.OverlayErrorInfo, mop *v1alpha1.MeshOperator) error {
					return svcController.updateMopStatusAcrossClusters(logger, service, validationError, errInfo, mop, map[string][]*v1alpha1.MeshOperator{clusterName: {mop}})
				},
				client,
				configGenerator,
				resources_test.NewFakeResourceManager(),
				registry,
				nil)

			if err != nil {
				t.Fatalf("unexpected error while running test case %s: %s", tc.name, err.Error())
			}
			actualConfig := applicator.AppliedResults[0].ApplicatorResult[0].Object
			assert.Equal(t, tc.configAfterMopDeletion, actualConfig)
		})
	}
}

func getOverlayingError(mopName string, overlayIdx int, message string) meshOpErrors.OverlayingError {
	return &meshOpErrors.OverlayingErrorImpl{
		ErrorMap: map[string]*meshOpErrors.OverlayErrorInfo{
			mopName: {
				OverlayIndex: overlayIdx,
				Message:      message,
			},
		},
	}
}

func getMopSvcStatusFailed(mopName string, overlayIdx int) v1alpha1.MeshOperatorStatus {
	return v1alpha1.MeshOperatorStatus{
		Phase:   PhaseFailed,
		Message: "issues with applying overlay",
		Services: map[string]*v1alpha1.ServiceStatus{
			serviceName: {
				Phase:   PhaseFailed,
				Message: fmt.Sprintf("error encountered when overlaying: mop %s, overlay <%d>: no matching route found to apply overlay", mopName, overlayIdx),
			},
		},
	}
}

func assertOverlayMopMetrics(t *testing.T, expectedMopStatus v1alpha1.MeshOperatorStatus, mopName string, registry *prometheus.Registry, mop *v1alpha1.MeshOperator, targetKind string) {
	expectedTargetStatus := expectedMopStatus.Services
	if targetKind == "ServiceEntry" {
		expectedTargetStatus = expectedMopStatus.ServiceEntries
	}
	if len(expectedTargetStatus) == 0 {
		labels := map[string]string{
			commonmetrics.ResourceKind:            "MeshOperator",
			commonmetrics.ClusterLabel:            "primary",
			commonmetrics.NamespaceLabel:          serviceNamespace,
			commonmetrics.ResourceNameLabel:       mopName,
			commonmetrics.TargetResourceNameLabel: commonmetrics.TargetAll,
			commonmetrics.TargetKindLabel:         targetKind,
			commonmetrics.TemplateTypeLabel:       commonmetrics.OverlayMopType,
		}

		metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.ObjectsConfiguredTotal, labels, 1)
	} else {
		for svcName, serviceStatus := range mop.Status.Services {
			totalLabels := map[string]string{
				commonmetrics.ResourceKind:            "MeshOperator",
				commonmetrics.ClusterLabel:            "primary",
				commonmetrics.NamespaceLabel:          serviceNamespace,
				commonmetrics.ResourceNameLabel:       mopName,
				commonmetrics.TargetResourceNameLabel: svcName,
				commonmetrics.TargetKindLabel:         targetKind,
				commonmetrics.TemplateTypeLabel:       commonmetrics.OverlayMopType,
			}
			failureLabels := map[string]string{
				commonmetrics.ResourceKind:            "MeshOperator",
				commonmetrics.ClusterLabel:            "primary",
				commonmetrics.NamespaceLabel:          serviceNamespace,
				commonmetrics.ResourceNameLabel:       mopName,
				commonmetrics.TargetResourceNameLabel: svcName,
				commonmetrics.TargetKindLabel:         targetKind,
				commonmetrics.MopType:                 commonmetrics.OverlayMopType,
			}
			expectedFailure := 0.0
			if serviceStatus.Phase != PhaseSucceeded {
				expectedFailure = 1
			}

			metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.ObjectsConfiguredTotal, totalLabels, 1)
			metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, commonmetrics.MeshOperatorFailed, failureLabels, expectedFailure)
		}
	}

	reconcileFailureLabels := map[string]string{
		commonmetrics.ClusterLabel:      "primary",
		commonmetrics.NamespaceLabel:    serviceNamespace,
		commonmetrics.ResourceNameLabel: mopName,
		commonmetrics.EventTypeLabel:    controllers_api.UnknownEvent,
	}

	if expectedMopStatus.Phase != PhaseSucceeded {
		if noMatchingRouteFoundError(mop, targetKind) {
			metricstesting.AssertEqualsCounterValueWithLabel(t, registry, commonmetrics.MeshOperatorReconcileUserError, reconcileFailureLabels, 1)
		} else {
			metricstesting.AssertEqualsCounterValueWithLabel(t, registry, commonmetrics.MeshOperatorReconcileFailed, reconcileFailureLabels, 1)
		}
	} else {
		metricstesting.AssertEqualsCounterValueWithLabel(t, registry, commonmetrics.MeshOperatorReconcileFailed, reconcileFailureLabels, 0)
		metricstesting.AssertEqualsCounterValueWithLabel(t, registry, commonmetrics.MeshOperatorReconcileUserError, reconcileFailureLabels, 0)

	}
}
