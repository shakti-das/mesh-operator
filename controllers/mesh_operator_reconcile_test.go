package controllers

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	cluster2 "github.com/istio-ecosystem/mesh-operator/pkg/cluster"

	corev1 "k8s.io/api/core/v1"
	kubeinformers "k8s.io/client-go/informers"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	meshOpErrors "github.com/istio-ecosystem/mesh-operator/pkg/errors"

	"github.com/istio-ecosystem/mesh-operator/pkg/resources_test"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/istio-ecosystem/mesh-operator/pkg/templating"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/diff"

	meshv1alpha1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"

	"github.com/prometheus/client_golang/prometheus"

	kubetest "github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating_test"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	"github.com/istio-ecosystem/mesh-operator/pkg/controller_test"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
)

const (
	namespace     = "test-namespace"
	name          = "mop1"
	uid           = "uid-1"
	TestError     = "error generating one or more config"
	key           = namespace + "/" + name
	templateType  = "rendererOutput"
	mutatorPrefix = "mutated-"
	filterName    = "filter-1"
)

// TestReconcileMopComponentInteractions tests whether all the config generation components (render, mutator, applicator
// etc) were called and what the final output is.
func TestReconcileMopComponentInteractions(t *testing.T) {
	features.EnableServiceEntryOverlays = true
	features.EnableServiceEntryExtension = true
	defer func() {
		features.EnableServiceEntryOverlays = false
		features.EnableServiceEntryExtension = false
	}()
	clusterName := "whatever"
	mopOwner := []metav1.OwnerReference{
		{
			APIVersion: "mesh.io/v1alpha1",
			Kind:       "MeshOperator",
			Name:       name,
			UID:        uid,
		},
	}
	applicatorResult := []*templating.AppliedConfigObject{
		{
			Object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": mutatorPrefix + filterName,
					},
				},
			},
			Error: nil,
		},
	}
	// A manager pre-populated with a specific NS/MOP
	reconcileManager := NewMultiClusterReconcileTracker(nil)
	reconcileManager.trackKey("test-namespace", "mop1")

	testCases := []struct {
		name                    string
		queueItem               QueueItem
		servicesInCluster       []runtime.Object
		serviceEntriesInCluster []runtime.Object
		mopInCluster            []runtime.Object
		reconcileTracker        ReconcileManager
		expectedConfigMetadata  []templating_test.GeneratedConfigMetadata
		renderError             error
		mutatorError            error
		applicatorError         error
		otherExpectedError      error
	}{
		{
			name:      "ObjectNotInCluster",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventAdd),
		},
		{
			name:         "NoSelectorMopRender",
			queueItem:    NewQueueItem(clusterName, "test-namespace/mop1", "uid-1", controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{getDefaultMopBuilder().Build()},
			servicesInCluster: []runtime.Object{
				kube_test.CreateService("test-namespace", "svc1"),
				kube_test.CreateService("test-namespace", "svc2"),
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         "",
					MopKey:            "test-namespace/mop1",
					TemplatesRendered: []string{templateType},
					Owners:            mopOwner,
					ApplicatorResult:  applicatorResult,
				},
			},
		},
		{
			name:      "NoSelectorMopRenderError",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				getDefaultMopBuilder().Build(),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateService("test-namespace", "svc1"),
			},
			renderError: fmt.Errorf(TestError),
		},
		{
			name:      "MopRender",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				getDefaultMopBuilder().AddSelector("app", "ordering").Build(),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "ordering"}),
				kube_test.CreateServiceWithLabels(namespace, "svc2", map[string]string{"app": "ordering"}),
				kube_test.CreateServiceWithLabels(namespace, "svc3", map[string]string{"app": "shipping"}),
				kube_test.CreateServiceWithLabels("other-namespace", "svc4", map[string]string{"app": "ordering"}),
			},
			serviceEntriesInCluster: []runtime.Object{
				kube_test.CreateServiceEntryWithLabels(namespace, "se1", map[string]string{"app": "ordering"}),
				kube_test.CreateServiceEntryWithLabels(namespace, "se2", map[string]string{"app": "shipping"}),
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         "test-namespace/svc1",
					MopKey:            key,
					Owners:            mopOwner,
					TemplatesRendered: []string{templateType},
					ApplicatorResult:  applicatorResult,
				},
				{
					ObjectKey:         "test-namespace/svc2",
					MopKey:            key,
					Owners:            mopOwner,
					TemplatesRendered: []string{templateType},
					ApplicatorResult:  applicatorResult,
				},
				{
					ObjectKey:         "test-namespace/se1",
					MopKey:            key,
					Owners:            mopOwner,
					TemplatesRendered: []string{templateType},
					ApplicatorResult:  applicatorResult,
				},
			},
		},
		{
			name:      "MopRenderError",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				getDefaultMopBuilder().AddSelector("app", "ordering").Build(),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "ordering"}),
			},
			renderError: fmt.Errorf("error creating mesh config: test error"),
		},
		{
			name:      "MopMutatorError",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				getDefaultMopBuilder().AddSelector("app", "ordering").Build(),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "ordering"}),
			},
			mutatorError: fmt.Errorf("error mutating mesh config: test error"),
		},
		{
			name:      "MopApplicatorError",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				getDefaultMopBuilder().AddSelector("app", "ordering").Build(),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "ordering"}),
			},
			applicatorError: fmt.Errorf("error applying mesh config: test error"),
		},
		{
			name:      "MopDelete",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventDelete),
			mopInCluster: []runtime.Object{
				kube_test.NewMopBuilder(namespace, name).SetPhase(PhaseTerminating).Build(),
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{},
		},
		{
			name:                   "UnsupportedOperation",
			queueItem:              NewQueueItem(clusterName, key, uid, 100),
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{},
		},
		{
			name:      "UserConfigError",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				getDefaultMopBuilder().AddSelector("app", "ordering").Build(),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "ordering"}),
			},
			applicatorError: &meshOpErrors.UserConfigError{Message: fmt.Sprintf("user-config error")},
		},
		{
			name:               "ConcurrentReconcileInProgress",
			queueItem:          NewQueueItem(clusterName, "test-namespace/mop1", "uid-1", controllers_api.EventUpdate),
			mopInCluster:       []runtime.Object{getDefaultMopBuilder().Build()},
			reconcileTracker:   reconcileManager,
			otherExpectedError: &meshOpErrors.NonCriticalReconcileError{Message: "MOP being reconciled already, will retry: test-namespace/mop1"},
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &TestableEventRecorder{}

			renderer := &templating_test.TestRendererForMop{
				RenderErrorToThrow: tc.renderError,
				TemplateType:       templateType,
				FilterName:         filterName,
			}
			mutator := &templating_test.FakeMutator{MutatorErrorToThrow: tc.mutatorError, MutatorPrefix: mutatorPrefix}
			applicator := &templating_test.TestableApplicator{ApplicatorError: tc.applicatorError}

			primaryClient := kubetest.NewKubeClientBuilder().
				AddK8sObjects(tc.servicesInCluster...).
				AddIstioObjects(tc.serviceEntriesInCluster...).
				AddMopClientObjects(tc.mopInCluster...).Build()
			client := primaryClient

			reconcileTracker := tc.reconcileTracker
			if reconcileTracker == nil {
				reconcileTracker = &NoOpReconcileManager{}
			}

			registry := prometheus.NewRegistry()
			logger := zaptest.NewLogger(t).Sugar()
			mopQueue := createWorkQueue("MeshOperators")
			mopEnqueuer := NewSingleQueueEnqueuer(mopQueue)
			controller := NewMeshOperatorController(
				logger,
				&controller_test.FakeNamespacesNamespaceFilter{},
				recorder,
				applicator,
				renderer,
				registry,
				clusterName,
				client,
				primaryClient,
				mopEnqueuer,
				&NoOpEnqueuer{},
				&NoOpEnqueuer{},
				&resources_test.FakeResourceManager{},
				true,
				true,
				reconcileTracker,
				nil,
				&kubetest.FakeTimeProvider{}).(interface{}).(*meshOperatorController)

			controller.configGenerator = NewConfigGenerator(
				renderer,
				[]templating.Mutator{mutator},
				nil,
				applicator,
				templating.DontApplyOverlays,
				logger,
				registry)

			client.RunAndWait(stopCh, false, nil)

			err := controller.reconcile(tc.queueItem)

			if tc.renderError != nil {
				testError(t, registry, clusterName, tc.queueItem, tc.renderError, err)
				return
			}

			if tc.mutatorError != nil {
				testError(t, registry, clusterName, tc.queueItem, tc.mutatorError, err)
				return
			}

			if tc.applicatorError != nil {
				testError(t, registry, clusterName, tc.queueItem, tc.applicatorError, err)
				return
			}

			if tc.otherExpectedError != nil {
				testError(t, registry, clusterName, tc.queueItem, tc.otherExpectedError, err)
			} else {
				assert.Nil(t, err)
				applicator.AssertResults(t, tc.expectedConfigMetadata)
				assertCounterValue(t, registry, commonmetrics.MeshOperatorRecordsReconciledTotal, clusterName, tc.queueItem, 1)
			}
		})
	}
}

func getDefaultMopBuilder() *kube_test.MopBuilder {
	return kube_test.NewMopBuilder(namespace, name).SetUID(uid).SetPhase(PhasePending)
}

func testError(t *testing.T, registry *prometheus.Registry, clusterName string, qi QueueItem, expectedErr error, actualErr error) {
	if expectedErr != nil {
		if meshOpErrors.IsUserConfigError(expectedErr) || meshOpErrors.IsOverlayingError(expectedErr) {
			assert.NoError(t, actualErr)
			assertCounterValue(t, registry, commonmetrics.MeshOperatorReconcileFailed, clusterName, qi, 0)
			assertCounterValue(t, registry, commonmetrics.MeshOperatorReconcileNonCriticalError, clusterName, qi, 0)
			assertCounterValue(t, registry, commonmetrics.MeshOperatorReconcileUserError, clusterName, qi, 1)

		} else if meshOpErrors.IsNonCriticalReconcileError(expectedErr) {
			assert.NotNil(t, actualErr)
			assert.True(t, assert.Contains(t, actualErr.Error(), expectedErr.Error()))
			assertCounterValue(t, registry, commonmetrics.MeshOperatorReconcileNonCriticalError, clusterName, qi, 1)
			assertCounterValue(t, registry, commonmetrics.MeshOperatorReconcileFailed, clusterName, qi, 0)
			assertCounterValue(t, registry, commonmetrics.MeshOperatorReconcileUserError, clusterName, qi, 0)

		} else {
			assert.NotNil(t, actualErr)
			assert.True(t, assert.Contains(t, actualErr.Error(), expectedErr.Error()))
			assertCounterValue(t, registry, commonmetrics.MeshOperatorReconcileFailed, clusterName, qi, 1)
			assertCounterValue(t, registry, commonmetrics.MeshOperatorReconcileNonCriticalError, clusterName, qi, 0)
			assertCounterValue(t, registry, commonmetrics.MeshOperatorReconcileUserError, clusterName, qi, 0)
		}
		assertCounterValue(t, registry, commonmetrics.MeshOperatorRecordsReconciledTotal, clusterName, qi, 1)
	} else {
		assert.NoError(t, actualErr)
	}
}

func TestExtensionMopStatusAfterReconcile(t *testing.T) {
	clusterName := "whatever"

	mopWithSelector := getDefaultMopBuilder().AddSelector("app", "ordering").Build()
	mopOwner := []metav1.OwnerReference{
		{
			APIVersion: "mesh.io/v1alpha1",
			Kind:       "MeshOperator",
			Name:       name,
			UID:        uid,
		},
	}
	applicatorResult := []*templating.AppliedConfigObject{
		{
			Object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": mutatorPrefix + filterName,
					},
				},
			},
			Error: nil,
		},
	}
	serviceStatus1 := map[string]*meshv1alpha1.ServiceStatus{
		"svc1": {
			Phase:   PhaseFailed,
			Message: "error generating one or more config",
		},
	}
	serviceStatus2 := map[string]*meshv1alpha1.ServiceStatus{
		"svc1": {
			Phase:   PhaseFailed,
			Message: "error generating one or more config",
		},
		"svc2": {
			Phase:   PhaseFailed,
			Message: "error generating one or more config",
		},
	}
	serviceStatusWithResources := map[string]*meshv1alpha1.ServiceStatus{
		"svc1": {
			Phase:   PhaseFailed,
			Message: "error generating one or more config",
			RelatedResources: []*meshv1alpha1.ResourceStatus{
				{
					Phase:      PhaseFailed,
					Message:    TestError,
					ApiVersion: "mesh.io/v1alpha1",
					Kind:       "ApplicatorError",
					Name:       name,
				},
			},
		},
	}
	resourceStatus := meshv1alpha1.ResourceStatus{
		ApiVersion: "networking.istio.io/v1alpha3",
		Kind:       "EnvoyFilter",
		Phase:      PhaseSucceeded,
	}
	resourceStatusFailed := meshv1alpha1.ResourceStatus{
		ApiVersion: "networking.istio.io/v1alpha3",
		Kind:       "EnvoyFilter",
		Phase:      PhaseFailed,
		Message:    TestError,
	}
	testCases := []struct {
		name                    string
		queueItem               QueueItem
		servicesInCluster       []runtime.Object
		mopInCluster            []runtime.Object
		renderError             error
		resourceError           error
		expectedServiceStatuses map[string]*meshv1alpha1.ServiceStatus
		expectedStatusMessage   string
		expectedConfigMetadata  []templating_test.GeneratedConfigMetadata
	}{
		{
			name:      "NoSelectorMopRenderError",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				getDefaultMopBuilder().Build(),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "ordering"}),
			},
			expectedServiceStatuses: nil,
			renderError:             fmt.Errorf(TestError),
		},
		{
			name:      "NoSelectorMopRenderWithResourceError",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				getDefaultMopBuilder().Build(),
			},
			resourceError:           fmt.Errorf(TestError),
			expectedServiceStatuses: nil,
		},
		{
			name:      "SelectorMopRenderError",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				mopWithSelector,
			},
			expectedStatusMessage: noServicesForSelectorMessage,
		},
		{
			name:      "MopRenderError",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				mopWithSelector,
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "ordering"}),
			},
			expectedServiceStatuses: serviceStatus1,
			renderError:             fmt.Errorf(TestError),
		},
		{
			name:      "MopRenderWithMultipleErrors",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				mopWithSelector,
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "ordering"}),
				kube_test.CreateServiceWithLabels(namespace, "svc2", map[string]string{"app": "ordering"}),
			},
			expectedServiceStatuses: serviceStatus2,
			renderError:             fmt.Errorf(TestError),
		},
		{
			name:      "MopRenderWithResourceError",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				mopWithSelector,
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "ordering"}),
			},
			resourceError:           fmt.Errorf(TestError),
			expectedServiceStatuses: serviceStatusWithResources,
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         "test-namespace/svc1",
					MopKey:            key,
					Owners:            mopOwner,
					TemplatesRendered: []string{templateType},
					ApplicatorResult:  applicatorResult,
				},
			},
		},
		{
			name:      "MopRender",
			queueItem: NewQueueItem(clusterName, key, uid, controllers_api.EventUpdate),
			mopInCluster: []runtime.Object{
				getDefaultMopBuilder().AddSelector("app", "ordering").Build(),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "ordering"}),
				kube_test.CreateServiceWithLabels(namespace, "svc2", map[string]string{"app": "ordering"}),
				kube_test.CreateServiceWithLabels(namespace, "svc3", map[string]string{"app": "shipping"}),
				kube_test.CreateServiceWithLabels("other-namespace", "svc4", map[string]string{"app": "ordering"}),
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         "test-namespace/svc1",
					MopKey:            key,
					Owners:            mopOwner,
					TemplatesRendered: []string{templateType},
					ApplicatorResult:  applicatorResult,
				},
				{
					ObjectKey:         "test-namespace/svc2",
					MopKey:            key,
					Owners:            mopOwner,
					TemplatesRendered: []string{templateType},
					ApplicatorResult:  applicatorResult,
				},
			},
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &TestableEventRecorder{}

			applicator := &templating_test.TestableApplicator{K8sPatchErrorToAdd: tc.resourceError}
			renderer := &templating_test.TestRendererForMop{
				RenderErrorToThrow: tc.renderError,
				TemplateType:       templateType,
				FilterName:         filterName,
			}

			primaryClient := kubetest.NewKubeClientBuilder().AddK8sObjects(tc.servicesInCluster...).AddMopClientObjects(tc.mopInCluster...).Build()
			client := primaryClient

			mopQueue := createWorkQueue("MeshOperators")
			mopEnqueuer := NewSingleQueueEnqueuer(mopQueue)

			registry := prometheus.NewRegistry()
			controller := NewMeshOperatorController(
				zaptest.NewLogger(t).Sugar(),
				&controller_test.FakeNamespacesNamespaceFilter{},
				recorder,
				applicator,
				renderer,
				registry,
				clusterName,
				client,
				primaryClient,
				mopEnqueuer,
				&NoOpEnqueuer{},
				&NoOpEnqueuer{},
				&resources_test.FakeResourceManager{},
				true,
				true,
				&NoOpReconcileManager{},
				nil,
				&kubetest.FakeTimeProvider{}).(interface{}).(*meshOperatorController)

			client.RunAndWait(stopCh, false, nil)

			err := controller.reconcile(tc.queueItem)
			if tc.renderError != nil || tc.resourceError != nil {
				assert.NotNil(t, err)
			} else {
				require.NoError(t, err)
			}

			fakeClient := (client).(*kubetest.FakeClient)

			mopRecord, err := fakeClient.MopClient.MeshV1alpha1().MeshOperators(namespace).Get(context.TODO(), name, metav1.GetOptions{})
			require.NoError(t, err)

			mopPhase := mopRecord.Status.Phase
			mopMsg := mopRecord.Status.Message

			if tc.renderError != nil {
				if !reflect.DeepEqual(tc.expectedServiceStatuses, mopRecord.Status.Services) {
					t.Errorf("Status mismatch. \nDiff: %s", diff.ObjectGoPrintSideBySide(tc.expectedServiceStatuses, mopRecord.Status.Services))
				}
				if mopRecord.Spec.ServiceSelector == nil {
					assert.Equal(t, PhaseFailed, mopPhase)
					assert.Equal(t, TestError, mopMsg)
				}
			} else if tc.resourceError != nil {
				assertServicesStatus(t, mopRecord, resourceStatusFailed, "error generating one or more config")
			} else {
				assert.Equal(t, PhaseSucceeded, mopPhase)
				assert.Equal(t, tc.expectedStatusMessage, mopMsg)
				assertServicesStatus(t, mopRecord, resourceStatus, "")
			}

			if tc.resourceError == nil {
				assertServicesStatus(t, mopRecord, resourceStatus, "")
				applicator.AssertResults(t, tc.expectedConfigMetadata)
			}
		})
	}
}

func TestOverlayMopStatusAfterReconcile(t *testing.T) {
	clusterName := "whatever"
	renderResult = templating.GeneratedConfig{
		TemplateType: "templateName",
		Config: map[string][]*unstructured.Unstructured{
			"templateName": {
				{
					Object: map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "config-name",
						},
						"spec": map[string]interface{}{
							"http": []map[string]interface{}{
								{
									"name": "test-route-1",
								},
							},
						},
					},
				},
			},
		},
	}

	serviceStatusFailed := map[string]*meshv1alpha1.ServiceStatus{
		"good-object": {
			Phase:   PhaseFailed,
			Message: "error encountered when overlaying: mop mop3, overlay <0>: no matching route found to apply overlay",
		},
	}
	serviceStatusSucceed := map[string]*meshv1alpha1.ServiceStatus{
		"good-object": {
			Phase: PhaseSucceeded,
		},
	}
	serviceStatusSomePending := map[string]*meshv1alpha1.ServiceStatus{
		"good-object": {
			Phase: PhaseSucceeded,
		},
		"some-other-svc": {
			Phase: PhasePending,
		},
	}

	renderResult = templating.GeneratedConfig{
		TemplateType: "templateName",
		Config: map[string][]*unstructured.Unstructured{
			"templateName": {
				{
					Object: map[string]interface{}{
						"kind": "VirtualService",
						"metadata": map[string]interface{}{
							"name": "test-vs-1",
						},
						"spec": map[string]interface{}{
							"http": []interface{}{
								map[string]interface{}{
									"name": "test-route-1",
								},
							},
						},
					},
				},
			},
		},
	}

	testSvcKey := fmt.Sprintf("%s/%s", serviceObject.Namespace, serviceObject.Name)
	testCases := []struct {
		name                    string
		svcQueueItem            QueueItem
		mopQueueItem            QueueItem
		mop                     *meshv1alpha1.MeshOperator
		expectedMop             *meshv1alpha1.MeshOperator
		overlayError            error
		validationError         error
		expectedServiceStatuses map[string]*meshv1alpha1.ServiceStatus
		rootStatus              meshv1alpha1.Phase
	}{
		{
			name:                    "Overlay Validation Error - Status set to Failed",
			svcQueueItem:            NewQueueItem(clusterName, testSvcKey, uid, controllers_api.EventUpdate),
			mopQueueItem:            NewQueueItem(clusterName, "mop5", uid, controllers_api.EventUpdate),
			mop:                     mopWithInvalidOverlay,
			expectedMop:             mopWithInvalidOverlayWithStatus,
			expectedServiceStatuses: nil,
			validationError:         fmt.Errorf("overlay 0 is invalid: invalid match object, please make sure it follows Istio VirtualService spec"),
			overlayError:            nil,
			rootStatus:              PhaseFailed,
		},
		{
			name:                    "OverlayingError - Status set to Failed",
			svcQueueItem:            NewQueueItem(clusterName, testSvcKey, uid, controllers_api.EventUpdate),
			mopQueueItem:            NewQueueItem(clusterName, "mop3", uid, controllers_api.EventUpdate),
			mop:                     mopWithBadOverlay,
			expectedMop:             mopWithBadOverlayWithStatus,
			expectedServiceStatuses: serviceStatusFailed,
			overlayError:            fmt.Errorf("issues with applying overlay"),
			rootStatus:              PhaseFailed,
		},
		{
			name:                    "Overlay Succeed - Status set to Succeed",
			svcQueueItem:            NewQueueItem(clusterName, testSvcKey, uid, controllers_api.EventUpdate),
			mopQueueItem:            NewQueueItem(clusterName, "mop1", uid, controllers_api.EventUpdate),
			mop:                     mopWithRetriesOverlay,
			expectedMop:             mopWithRetriesOverlayWithSuccessStatus,
			expectedServiceStatuses: serviceStatusSucceed,
			overlayError:            nil,
			rootStatus:              PhaseSucceeded,
		},
		{
			name:                    "Overlay Succeed - Some object status in pending, no root status update",
			svcQueueItem:            NewQueueItem(clusterName, testSvcKey, uid, controllers_api.EventUpdate),
			mopQueueItem:            NewQueueItem(clusterName, "mop4", uid, controllers_api.EventUpdate),
			mop:                     mopWithOverlay1PendingSvcStatus,
			expectedMop:             mopWithOverlay1RootStatusPending,
			expectedServiceStatuses: serviceStatusSomePending,
			overlayError:            nil,
			rootStatus:              PhasePending,
		},
	}

	serviceObject = corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: serviceNamespace,
			Name:      serviceName,
			UID:       serviceUid,
			Labels: map[string]string{
				"key-1": "val-2",
			},
			Annotations: map[string]string{},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &TestableEventRecorder{}

			applicator := &templating_test.TestableApplicator{K8sPatchErrorToAdd: tc.overlayError}
			renderer := &templating_test.FixedResultRenderer{ResultToReturn: renderResult}
			mutator := &templating_test.FakeMutator{MutatorPrefix: templateType}

			serviceInCluster := []runtime.Object{
				&serviceObject,
			}

			mopInCluster := []runtime.Object{
				tc.mop,
			}

			primaryClient := kubetest.NewKubeClientBuilder().AddK8sObjects(serviceInCluster...).AddMopClientObjects(mopInCluster...).Build()
			client := primaryClient
			cluster := &Cluster{
				ID:            cluster2.ID(clusterName),
				Client:        client,
				primary:       true,
				eventRecorder: recorder,
			}

			mopObjReader := func(string, string) (interface{}, error) {
				return tc.expectedMop, nil
			}

			mopQueue := createWorkQueue("MeshOperators")
			mopEnqueuer := NewSingleQueueEnqueuer(mopQueue)

			registry := prometheus.NewRegistry()
			mopController := NewMeshOperatorController(
				zaptest.NewLogger(t).Sugar(),
				&controller_test.FakeNamespacesNamespaceFilter{},
				recorder,
				applicator,
				renderer,
				registry,
				clusterName,
				client,
				primaryClient,
				mopEnqueuer,
				&NoOpEnqueuer{},
				&NoOpEnqueuer{},
				&resources_test.FakeResourceManager{},
				true,
				true,
				&NoOpReconcileManager{},
				nil,
				&kubetest.FakeTimeProvider{}).(interface{}).(*meshOperatorController)
			mopController.objectReader = mopObjReader

			serviceQueue := createWorkQueue("Services")

			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			clusterManager.SetPrimaryCluster(cluster)

			configGenerator := NewConfigGenerator(
				renderer,
				[]templating.Mutator{mutator},
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

			features.EnableServiceConfigOverlays = true
			restore := func() {
				features.EnableServiceConfigOverlays = false
			}
			defer restore()

			client.RunAndWait(ctx.Done(), true, []kubeinformers.GenericInformer{})

			err := svcController.reconcile(tc.svcQueueItem)

			require.NoError(t, err)

			fakeClient := (client).(*kubetest.FakeClient)

			mopRecord, err := fakeClient.MopClient.MeshV1alpha1().MeshOperators(namespace).Get(context.TODO(), tc.mopQueueItem.key, metav1.GetOptions{})
			require.NoError(t, err)

			mopPhase := mopRecord.Status.Phase
			mopMsg := mopRecord.Status.Message

			// assert root level phase
			assert.Equal(t, tc.rootStatus, mopPhase)

			// assert root level message
			if tc.overlayError != nil {
				assert.Equal(t, tc.overlayError.Error(), mopMsg)
			} else if tc.validationError != nil {
				assert.Equal(t, tc.validationError.Error(), mopMsg)
			} else {
				assert.Equal(t, "", mopMsg)
			}

			// assert service level status
			if !reflect.DeepEqual(tc.expectedServiceStatuses, mopRecord.Status.Services) {
				t.Errorf("Status mismatch. \nDiff: %s", diff.ObjectGoPrintSideBySide(tc.expectedServiceStatuses, mopRecord.Status.Services))
			}
		})
	}
}

func assertServicesStatus(t *testing.T, mopRecord *meshv1alpha1.MeshOperator, resourceStatus meshv1alpha1.ResourceStatus, errorMsg string) {
	for i := range mopRecord.Status.Services {
		if errorMsg != "" {
			assert.Equal(t, PhaseFailed, mopRecord.Status.Services[i].Phase)
			assert.Equal(t, errorMsg, mopRecord.Status.Services[i].Message)
		}
		for j := range mopRecord.Status.Services[i].RelatedResources {
			assert.Equal(t, resourceStatus.Phase, mopRecord.Status.Services[i].RelatedResources[j].Phase)
			assert.Equal(t, resourceStatus.Message, mopRecord.Status.Services[i].RelatedResources[j].Message)
		}
	}
	for i := range mopRecord.Status.RelatedResources {
		if errorMsg != "" {
			assert.Equal(t, PhaseFailed, mopRecord.Status.RelatedResources[i].Phase)
			assert.Equal(t, errorMsg, mopRecord.Status.RelatedResources[i].Message)
		}
	}
}

// - Start cluster with 1 MOP and 1 matching service
// - Create another MOP matching the service
// - Wait for render to happen
// - Update the MOP.Selector to match another service
// - Wait for render call to complete and assert
/*
 * TODO: comment out this test due to SFCI unit test failure:
 * Details: https://salesforce-internal.slack.com/archives/G021Y2E6AAF/p1645490993498959
 *
func TestRunController(t *testing.T) {
	servicesInCluster := []runtime.Object{
		kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "ordering"}),
		kube_test.CreateServiceWithLabels("test-namespace", "svc2", map[string]string{"app": "shipping"}),
	}
	mopInCluster := []runtime.Object{
		kube_test.NewMopBuilder("test-namespace", "mop1").AddSelector("app", "ordering").Build(),
	}
	kubeClient := kubefake.NewSimpleClientset(servicesInCluster...)
	mopClient := fake.NewSimpleClientset(mopInCluster...)
	mopInformerFactory := externalversions.NewSharedInformerFactory(mopClient, 1*time.Second)
	clusterName := "whatever"
	var recorder = &TestableEventRecorder{}
	var applicator = &templating_test.TestableApplicator{AppliedResults: []templating_test.GeneratedConfigMetadata{}}
	controller := NewMeshOperatorController(
		zaptest.NewLogger(t).Sugar(),
		&controller_test.FakeNamespacesNamespaceFilter{},
		mopClient,
		mopInformerFactory.Mesh().V1alpha1().MeshOperators().Informer(),
		mopInformerFactory.Mesh().V1alpha1().MeshOperators().Lister(),
		kubeClient.CoreV1(),
		recorder,
		applicator,
		prometheus.NewRegistry(),
		clusterName).(interface{}).(*meshOperatorController)
	stopCh := make(chan struct{})
	defer close(stopCh)
	mopInformerFactory.Start(stopCh)
	go wait.Until(func() { _ = controller.Run(1, stopCh) }, time.Second, stopCh)
	// First render on cluster start
	applicator.WaitForRender(1)
	newMop := kube_test.NewMopBuilder("test-namespace", "mop1").
		AddSelector("app", "shipping").Build()
	newMop.SetResourceVersion("1")
	newMop, err := mopClient.MeshV1alpha1().MeshOperators("test-namespace").Update(context.TODO(), newMop, v1.UpdateOptions{})
	require.NoError(t, err)
	// Second render on MOP update
	applicator.WaitForRender(2)
	expectedMopOwner := []metav1.OwnerReference{
		{
			APIVersion: "mesh.io/v1alpha1",
			Kind:       "MeshOperator",
			Name:       "mop1",
			UID:        newMop.GetUID(),
		},
	}
	applicator.AssertResults(t, []templating_test.GeneratedConfigMetadata{
		{
			ObjectKey: "test-namespace/svc1",
			MopKey:    "test-namespace/mop1",
			Owners:    expectedMopOwner,
		},
		{
			ObjectKey: "test-namespace/svc2",
			MopKey:    "test-namespace/mop1",
			Owners:    expectedMopOwner,
		},
	})
}
*/

func assertCounterValue(t *testing.T, registry *prometheus.Registry, metricName string, cluster string, item QueueItem, expectedValue float64) {
	labels := map[string]string{
		commonmetrics.ClusterLabel:      cluster,
		commonmetrics.NamespaceLabel:    "test-namespace",
		commonmetrics.ResourceNameLabel: commonmetrics.DeprecatedLabel,
		commonmetrics.EventTypeLabel:    item.GetEventName(),
	}
	assertCounterWithLabels(t, registry, metricName, labels, expectedValue)
}
