package controllers

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	mopFake "github.com/istio-ecosystem/mesh-operator/pkg/generated/clientset/versioned/fake"
	"github.com/istio-ecosystem/mesh-operator/pkg/generated/informers/externalversions"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"k8s.io/apimachinery/pkg/types"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	mopv1alpha1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/resources_test"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/istio-ecosystem/mesh-operator/pkg/templating_test"

	"github.com/istio-ecosystem/mesh-operator/pkg/controller_test"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	networkingv1alpha3 "istio.io/api/networking/v1alpha3"
	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	seApiVersion           = constants.ServiceEntryKind.Group + "/" + constants.ServiceEntryResource.Version
	mopEnabledServiceEntry = istiov1alpha3.ServiceEntry{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceEntryKind.Kind,
			APIVersion: seApiVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mop-enabled-service-entry",
			Namespace: "test-namespace",
			UID:       uid,
		},
		Spec: networkingv1alpha3.ServiceEntry{
			Hosts:    []string{"host-1.com"},
			Location: networkingv1alpha3.ServiceEntry_MESH_INTERNAL,
		},
	}

	mopDisabledServiceEntry = istiov1alpha3.ServiceEntry{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceEntryKind.Kind,
			APIVersion: seApiVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "mop-disabled-service-entry",
			Namespace:   "test-namespace",
			Annotations: map[string]string{routingConfigEnabledAnnotationAsStr: "false"},
		},
		Spec: networkingv1alpha3.ServiceEntry{
			Hosts:    []string{"host-1.com"},
			Location: networkingv1alpha3.ServiceEntry_MESH_INTERNAL,
		},
	}
)

func TestReconcileServiceEntry(t *testing.T) {

	var (
		clusterName    = "whatever"
		seName         = "mop-enabled-service-entry"
		seNamespace    = "test-namespace"
		mopEnabledKey  = strings.Join([]string{seNamespace, seName}, "/")
		mopDisabledKey = strings.Join([]string{seNamespace, "mop-disabled-service-entry"}, "/")
		msmOwnerRefs   = []metav1.OwnerReference{
			{
				APIVersion: "mesh.io/v1alpha1",
				Kind:       "MeshServiceMetadata",
				Name:       seName,
			},
		}

		enabledSeTrackedRequest  = resources_test.TrackRequest{ClusterName: clusterName, Kind: "ServiceEntry", Namespace: seNamespace, Name: "mop-enabled-service-entry", UID: uid}
		disabledSeTrackedRequest = resources_test.TrackRequest{ClusterName: clusterName, Kind: "ServiceEntry", Namespace: seNamespace, Name: "mop-disabled-service-entry", UID: uid}
	)
	testCases := []struct {
		name                      string
		isRemoteClusterEvent      bool
		queueItem                 QueueItem
		objectsInCluster          []runtime.Object
		cleanupDisabledFeature    bool
		upsertCallExpected        bool
		deleteCallExpected        bool
		expectedConfigMetadata    []templating_test.GeneratedConfigMetadata
		expectedTrackRequest      resources_test.TrackRequest
		expectedUnTrackRequest    resources_test.TrackRequest
		expectedError             error
		expectedObjectWithWarning interface{}
		trackOwnerError           error
		onOwnerChangeError        error
	}{
		{
			name: "ObjectWithInvalidKey",
			queueItem: NewQueueItem(
				clusterName,
				"an/invalid/key",
				"uid-1",
				controllers_api.EventAdd),
		},
		{
			name: "ObjectDoesntExistUpsert",
			queueItem: NewQueueItem(
				clusterName,
				mopEnabledKey,
				"uid-1",
				controllers_api.EventAdd),
		},
		{
			name: "MopDisabledForObject (disabled deletion off)",
			queueItem: NewQueueItem(
				clusterName,
				mopDisabledKey,
				"uid-1",
				controllers_api.EventAdd),
			objectsInCluster: []runtime.Object{&mopDisabledServiceEntry},
		},
		{
			name: "MopDisabledForObject (disabled deletion on)",
			queueItem: NewQueueItem(
				clusterName,
				mopDisabledKey,
				"uid-1",
				controllers_api.EventAdd),
			objectsInCluster:       []runtime.Object{&mopDisabledServiceEntry},
			cleanupDisabledFeature: true,
			deleteCallExpected:     true,
			expectedUnTrackRequest: disabledSeTrackedRequest,
		},
		{
			name: "UnsupportedEvent",
			queueItem: NewQueueItem(
				clusterName,
				mopEnabledKey,
				"uid-1",
				999),
			objectsInCluster: []runtime.Object{&mopEnabledServiceEntry},
		},
		{
			name: "SuccessfulAdd",
			queueItem: NewQueueItem(
				clusterName,
				mopEnabledKey,
				"uid-1",
				controllers_api.EventAdd),
			objectsInCluster:     []runtime.Object{&mopEnabledServiceEntry},
			upsertCallExpected:   true,
			expectedTrackRequest: enabledSeTrackedRequest,
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey: mopEnabledKey,
					Metadata:  map[string]string{},
					Owners:    msmOwnerRefs,
				},
			},
		},
		{
			name: "FailedAdd",
			queueItem: NewQueueItem(
				clusterName,
				mopEnabledKey,
				"uid-1",
				controllers_api.EventAdd),
			objectsInCluster:          []runtime.Object{&mopEnabledServiceEntry},
			upsertCallExpected:        true,
			expectedTrackRequest:      enabledSeTrackedRequest,
			expectedError:             fmt.Errorf("failed Add"),
			expectedObjectWithWarning: &mopEnabledServiceEntry,
		},
		{
			name: "SuccessfulUpdate",
			queueItem: NewQueueItem(
				clusterName,
				mopEnabledKey,
				"uid-1",
				controllers_api.EventUpdate),
			objectsInCluster:     []runtime.Object{&mopEnabledServiceEntry},
			upsertCallExpected:   true,
			expectedTrackRequest: enabledSeTrackedRequest,
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey: mopEnabledKey,
					Metadata:  map[string]string{},
					Owners:    msmOwnerRefs,
				},
			},
		},
		{
			name:                 "SkipRenderingForRemoteSeEvents",
			isRemoteClusterEvent: true,
			queueItem: NewQueueItem(
				clusterName,
				mopEnabledKey,
				"uid-1",
				controllers_api.EventUpdate),
			objectsInCluster: []runtime.Object{&mopEnabledServiceEntry},
		},
		{
			name: "FailedUpdate",
			queueItem: NewQueueItem(
				clusterName,
				mopEnabledKey,
				"uid-1",
				controllers_api.EventUpdate),
			objectsInCluster:          []runtime.Object{&mopEnabledServiceEntry},
			upsertCallExpected:        true,
			expectedTrackRequest:      enabledSeTrackedRequest,
			expectedError:             fmt.Errorf("failed Update"),
			expectedObjectWithWarning: &mopEnabledServiceEntry,
		},
		{
			name:                   "SuccessfulDelete",
			queueItem:              NewQueueItem(clusterName, mopEnabledKey, uid, controllers_api.EventDelete),
			objectsInCluster:       []runtime.Object{&mopEnabledServiceEntry},
			deleteCallExpected:     true,
			expectedUnTrackRequest: enabledSeTrackedRequest,
		},
		{
			name:               "FailedDeletion",
			queueItem:          NewQueueItem(clusterName, mopEnabledKey, uid, controllers_api.EventDelete),
			objectsInCluster:   []runtime.Object{&mopEnabledServiceEntry},
			deleteCallExpected: true,
			expectedError:      fmt.Errorf("failed to delete"),
		},
		{
			name: "TrackOwnerFailure_GracefulDegradation",
			queueItem: NewQueueItem(
				clusterName,
				mopEnabledKey,
				"uid-1",
				controllers_api.EventAdd),
			objectsInCluster:   []runtime.Object{&mopEnabledServiceEntry},
			upsertCallExpected: true,
			trackOwnerError:    fmt.Errorf("primary kube-api unavailable"),
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey: mopEnabledKey,
					Metadata:  map[string]string{},
				},
			},
		},
		{
			name: "OnOwnerChangeFailure_ErrorPropagated",
			queueItem: NewQueueItem(
				clusterName,
				mopEnabledKey,
				"uid-1",
				controllers_api.EventAdd),
			objectsInCluster:   []runtime.Object{&mopEnabledServiceEntry},
			upsertCallExpected: true,
			onOwnerChangeError: fmt.Errorf("primary kube-api unavailable"),
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey: mopEnabledKey,
					Metadata:  map[string]string{},
					Owners:    msmOwnerRefs,
				},
			},
			expectedTrackRequest:      enabledSeTrackedRequest,
			expectedError:             fmt.Errorf("primary kube-api unavailable"),
			expectedObjectWithWarning: &mopEnabledServiceEntry,
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			applicatorErr := tc.expectedError
			if tc.trackOwnerError != nil || tc.onOwnerChangeError != nil {
				applicatorErr = nil
			}
			var applicator = &templating_test.TestableApplicator{ApplicatorError: applicatorErr}
			var recorder = &TestableEventRecorder{}
			renderer := &templating_test.TestRendererForMop{}

			manager := resources_test.NewFakeResourceManager()

			if tc.deleteCallExpected {
				manager.ErrorOnUntrack = tc.expectedError
			}
			if tc.trackOwnerError != nil {
				manager.ErrorOnTrack = tc.trackOwnerError
			}
			if tc.onOwnerChangeError != nil {
				manager.ErrorOnChange = tc.onOwnerChangeError
			}

			manager.MsmOnTrack = mopv1alpha1.MeshServiceMetadata{
				TypeMeta: metav1.TypeMeta{
					APIVersion: mopv1alpha1.ApiVersion,
					Kind:       mopv1alpha1.MsmKind.Kind,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: seName,
				},
			}

			registry := prometheus.NewRegistry()

			client := kube_test.NewKubeClientBuilder().AddIstioObjects(tc.objectsInCluster...).Build()
			primaryClient := client

			if tc.isRemoteClusterEvent {
				primaryClient = kube_test.NewKubeClientBuilder().Build()
			}

			if tc.cleanupDisabledFeature {
				features.CleanupDisabledConfig = true
				defer func() { features.CleanupDisabledConfig = false }()
			}

			mopQueue := createWorkQueue("MeshOperator")
			seQueue := createWorkQueue("SEs")
			controller := NewServiceEntryController(
				zaptest.NewLogger(t).Sugar(),
				applicator,
				renderer,
				manager,
				recorder,
				&controller_test.FakeNamespacesNamespaceFilter{},
				registry,
				NewSingleQueueEnqueuer(seQueue),
				NewSingleQueueEnqueuer(mopQueue),
				clusterName,
				client,
				primaryClient,
				false).(interface{}).(*serviceEntryController)

			_ = client.RunAndWait(stopCh, !tc.isRemoteClusterEvent, nil)

			err := controller.reconcile(tc.queueItem)

			if tc.expectedError != nil {
				assert.NotNil(t, err)
				assert.Error(t, tc.expectedError, err.Error())
			} else {
				assert.Nil(t, err)
			}

			// assert Render flow
			if tc.upsertCallExpected {
				applicator.AssertResults(t, tc.expectedConfigMetadata)
				if tc.trackOwnerError != nil {
					assert.Equal(t, 0, len(manager.RecordedTrackRequests))
				} else {
					assert.Equal(t, 1, len(manager.RecordedTrackRequests))
					assert.Equal(t, tc.expectedTrackRequest, manager.RecordedTrackRequests[0])
				}
				if tc.expectedError != nil {
					assert.Equal(t, tc.expectedObjectWithWarning, recorder.recordedObject)
					assert.Equal(t, corev1.EventTypeWarning, recorder.eventtype)
					assert.Equal(t, constants.MeshConfigError, recorder.reason)
					assert.Equal(t, tc.expectedError.Error(), recorder.message)
				}
			}

			if tc.deleteCallExpected && tc.expectedError == nil {
				// assert UnTrack Requests
				assert.Equal(t, 1, len(manager.RecordedUnTrackRequests))
				assert.Equal(t, tc.expectedUnTrackRequest, manager.RecordedUnTrackRequests[0])
			}
		})

	}
}

func TestCreateMeshConfigForServiceEntryWrongObjectType(t *testing.T) {
	controller := serviceEntryController{}

	logger := zaptest.NewLogger(t).Sugar()

	err := controller.createMeshConfig("WrongObject", logger)

	assert.NotNil(t, err)
	assert.Equal(t, "object of a wrong type received string", err.Error())
}

func TestDeleteSeRenderedMeshConfig(t *testing.T) {
	var clusterName = "test-cluster"
	var namespace = "test-namespace"
	var name = "test-se"
	var uid = types.UID("uid-1")

	testCases := []struct {
		name                 string
		expectedTrackRequest resources_test.TrackRequest
		errorOnUntrack       error
	}{
		{
			name:                 "Success",
			expectedTrackRequest: resources_test.TrackRequest{ClusterName: clusterName, Namespace: namespace, Kind: "ServiceEntry", Name: name, UID: uid},
		},
		{
			name:           "Error",
			errorOnUntrack: fmt.Errorf("test-error"),
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := resources_test.NewFakeResourceManager()
			manager.ErrorOnUntrack = tc.errorOnUntrack

			serviceEntryController := serviceEntryController{
				logger:          zaptest.NewLogger(t).Sugar(),
				clusterName:     clusterName,
				resourceManager: manager,
				metricsRegistry: prometheus.NewRegistry(),
			}

			err := serviceEntryController.deleteMeshConfig(clusterName, namespace, name, uid, logger)

			if tc.errorOnUntrack != nil {
				assert.NotNil(t, err)
				assert.ErrorAs(t, err, &tc.errorOnUntrack)
				assert.Equal(t, 0, len(manager.RecordedUnTrackRequests))
			} else {
				assert.Nil(t, err)
				assert.Equal(t, 1, len(manager.RecordedUnTrackRequests))
				assert.Equal(t, tc.expectedTrackRequest, manager.RecordedUnTrackRequests[0])
			}
		})
	}
}

func TestEnqueueMopByServiceEntry(t *testing.T) {
	clusterName := "test-cluster"
	q1 := &TestableQueue{}
	q1.AddRateLimited(NewQueueItem(clusterName, fmt.Sprintf("%s/%s", "test-namespace", "mop1"), "uid-1", controllers_api.EventUpdate))

	seRoutingDisabled := kube_test.CreateServiceEntryWithLabels("test-namespace", "svc1", map[string]string{"app": "ordering"})
	seRoutingDisabled.SetAnnotations(map[string]string{"routing.mesh.io/enabled": "false"})

	testOverlay := v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: retriesOverlayBytes,
		}}

	testCases := []struct {
		name                 string
		serviceEntry         *istiov1alpha3.ServiceEntry
		mopInCluster         []runtime.Object
		enqueueExpected      bool
		expectedMopWorkQueue TestableQueue
	}{
		{
			name:         "EnqueueMop",
			serviceEntry: kube_test.CreateServiceEntryWithLabels("test-namespace", "svc1", map[string]string{"app": "ordering"}),
			mopInCluster: []runtime.Object{
				kube_test.NewMopBuilder("test-namespace", "mop1").SetUID("uid-1").AddSelector("app", "ordering").Build(),
				kube_test.NewMopBuilder("test-namespace", "mop2").SetUID("uid-2").Build(),
				kube_test.NewMopBuilder("other-namespace", "mop3").SetUID("uid-3").AddSelector("app", "ordering").Build(),
			},
			enqueueExpected:      true,
			expectedMopWorkQueue: *q1,
		},
		{
			name:         "NoMopToEnqueue",
			serviceEntry: kube_test.CreateServiceEntryWithLabels("test-namespace", "svc1", map[string]string{"app": "ordering"}),
			mopInCluster: []runtime.Object{
				kube_test.NewMopBuilder("test-namespace", "mop1").SetUID("uid-1").AddSelector("app", "ordering-bg").Build(),
				kube_test.NewMopBuilder("other-namespace", "mop2").SetUID("uid-2").AddSelector("app", "ordering-bg").Build(),
			},
			enqueueExpected: false,
		},
		{
			name:            "MopDontExistInCluster",
			serviceEntry:    kube_test.CreateServiceEntryWithLabels("test-namespace", "svc1", map[string]string{"app": "ordering"}),
			mopInCluster:    []runtime.Object{},
			enqueueExpected: false,
		},
		{
			name:         "MopOverlayServiceEntryRoutingDisabled",
			serviceEntry: seRoutingDisabled,
			mopInCluster: []runtime.Object{
				kube_test.NewMopBuilder("test-namespace", "mop1").SetUID("uid-1").AddSelector("app", "ordering").AddOverlay(testOverlay).Build(),
			},
			enqueueExpected: false,
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var mopWorkQueue = &TestableQueue{}
			var recorder = &kube_test.TestableEventRecorder{}

			mopClient := mopFake.NewSimpleClientset(tc.mopInCluster...)
			mopInformerFactory := externalversions.NewSharedInformerFactory(mopClient, 1*time.Second)
			mopLister := mopInformerFactory.Mesh().V1alpha1().MeshOperators().Lister()

			mopInformerFactory.Start(stopCh)
			mopInformerFactory.WaitForCacheSync(stopCh)

			seController := &serviceEntryController{
				logger:      zaptest.NewLogger(t).Sugar(),
				recorder:    recorder,
				mopLister:   mopLister,
				mopEnqueuer: NewSingleQueueEnqueuer(mopWorkQueue),
				clusterName: clusterName,
			}
			seController.enqueueMopReferencingGivenServiceEntry(tc.serviceEntry)

			if tc.enqueueExpected {
				assert.True(t, compareQueue(tc.expectedMopWorkQueue, *mopWorkQueue))
			} else {
				assert.Equal(t, 0, len(mopWorkQueue.recordedItems))
			}
		})
	}
}
