package controllers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	"k8s.io/client-go/informers"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	meshOpErrors "github.com/istio-ecosystem/mesh-operator/pkg/errors"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/multicluster"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/istio-ecosystem/mesh-operator/pkg/controller_test"

	"k8s.io/client-go/tools/cache"

	"go.uber.org/zap/zaptest"

	"time"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"

	"github.com/istio-ecosystem/mesh-operator/pkg/cluster"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func TestMulticlusterServiceController_readServiceObject_SingleCluster(t *testing.T) {
	stopCh := make(chan struct{})
	defer close(stopCh)

	testSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").Build()
	testCluster := createClusterWithObjects("cluster-1", stopCh, nil, &testSvc)

	clusterStore := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
	clusterStore.SetPrimaryCluster(testCluster)

	controller := MulticlusterServiceController{
		clusterManager: clusterStore,
	}

	actualResult, err := controller.readServicesFromKnownClusters("test-ns", "test-svc")

	expectedResult := map[string]*corev1.Service{
		"cluster-1": &testSvc,
	}
	assert.Nil(t, err)
	assert.Equal(t, expectedResult, actualResult)
}

func TestMulticlusterServiceController_readServiceObject_MultiCluster(t *testing.T) {
	stopCh := make(chan struct{})
	defer close(stopCh)

	testSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").SetUID("some-uid").Build()
	otherTestSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").SetUID("other-uid").Build()

	clusterWithSvc := createClusterWithObjects("cluster-1", stopCh, nil, &testSvc)
	otherClusterWithSvc := createClusterWithObjects("cluster-2", stopCh, nil, &otherTestSvc)
	clusterWithNoSvc := createClusterWithObjects("cluster-3", stopCh, nil)

	clusterStore := &testableClusterManager{
		cluster: clusterWithSvc,
		remoteClusters: map[cluster.ID]*Cluster{
			otherClusterWithSvc.ID: otherClusterWithSvc,
			clusterWithNoSvc.ID:    clusterWithNoSvc},
	}

	multiClusterSvcController := MulticlusterServiceController{
		clusterManager: clusterStore,
		logger:         zaptest.NewLogger(t).Sugar(),
	}

	testCases := []struct {
		name                      string
		clusterWithCacheSyncIssue cluster.ID
		expectedResult            map[string]*corev1.Service
	}{
		{
			name:                      "Happy Path",
			clusterWithCacheSyncIssue: "",
			expectedResult: map[string]*corev1.Service{
				"cluster-1": &testSvc,
				"cluster-2": &otherTestSvc,
			},
		},
		{
			name:                      "cluster unavailable",
			clusterWithCacheSyncIssue: "cluster-2",
			expectedResult: map[string]*corev1.Service{
				"cluster-1": &testSvc,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clusterStore.unhealthyCluster = tc.clusterWithCacheSyncIssue
			actualResult, err := multiClusterSvcController.readServicesFromKnownClusters("test-ns", "test-svc")

			assert.Nil(t, err)
			assert.Equal(t, tc.expectedResult, actualResult)
		})
	}
}

func TestMulticlusterServiceController_RecordEventForServices(t *testing.T) {
	testSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").SetUID("some-uid").Build()
	otherSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").SetUID("other-uid").Build()

	recorder := &TestableEventRecorder{}
	someCluster := &Cluster{
		ID:            cluster.ID("cluster-1"),
		eventRecorder: recorder,
	}

	otherRecorder := &TestableEventRecorder{}
	otherCluster := &Cluster{
		ID:            cluster.ID("cluster-2"),
		eventRecorder: otherRecorder,
	}

	yetAnotherRecorder := &TestableEventRecorder{}
	yetAnotherCluster := &Cluster{
		ID:            cluster.ID("cluster-3"),
		eventRecorder: yetAnotherRecorder,
	}

	clusterStore := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
	clusterStore.SetPrimaryCluster(someCluster)
	clusterStore.AddClusterForKey("secret", otherCluster)
	clusterStore.AddClusterForKey("secret", yetAnotherCluster)

	controller := MulticlusterServiceController{
		clusterManager: clusterStore,
	}

	// Method under test
	controller.recordEventForServices(map[string]*corev1.Service{
		"cluster-1": &testSvc,
		"cluster-3": &otherSvc,
	}, corev1.EventTypeWarning, "test-reason", "test-message")

	cluster1ObjectRef := &corev1.ObjectReference{
		Kind:            constants.ServiceKind.Kind,
		APIVersion:      constants.ServiceResource.GroupVersion().String(),
		ResourceVersion: constants.ServiceResource.Version,
		Name:            "test-svc",
		Namespace:       "test-ns",
		UID:             "some-uid",
	}

	cluster3ObjectRef := &corev1.ObjectReference{
		Kind:            constants.ServiceKind.Kind,
		APIVersion:      constants.ServiceResource.GroupVersion().String(),
		ResourceVersion: constants.ServiceResource.Version,
		Name:            "test-svc",
		Namespace:       "test-ns",
		UID:             "other-uid",
	}

	// Cluster-1 asserts
	assert.Equal(t, cluster1ObjectRef, recorder.recordedObject)
	assert.Equal(t, corev1.EventTypeWarning, recorder.eventtype)
	assert.Equal(t, "test-reason", recorder.reason)
	assert.Equal(t, "test-message", recorder.message)
	// Cluster-2 asserts
	assert.Nil(t, otherRecorder.recordedObject, "No events on cluster-2 expected")
	// Cluster-3 asserts
	assert.Equal(t, cluster3ObjectRef, yetAnotherRecorder.recordedObject)
	assert.Equal(t, corev1.EventTypeWarning, yetAnotherRecorder.eventtype)
	assert.Equal(t, "test-reason", yetAnotherRecorder.reason)
	assert.Equal(t, "test-message", yetAnotherRecorder.message)
}

func TestMulticlusterServiceController_updateMopStatusAcrossClusters(t *testing.T) {
	cluster1Name := "cluster1"
	cluster2Name := "cluster2"

	testNamespace := "test-ns"
	testSvc := kube_test.NewServiceBuilder("test-svc", testNamespace).SetUID("some-uid").Build()

	mop := kube_test.NewMopBuilder(testNamespace, "some-mop").Build()
	otherClusterMop := kube_test.NewMopBuilder(testNamespace, "some-mop").
		SetPhaseService("other-service", PhaseSucceeded).
		Build()

	mopSuccessStatus := kube_test.NewMopBuilder(testNamespace, "some-mop").
		SetPhaseService("test-svc", PhaseSucceeded).
		SetPhase(PhaseSucceeded).
		Build().Status

	mopFailedStatus := kube_test.NewMopBuilder(testNamespace, "some-mop").
		SetPhaseService("test-svc", PhaseFailed).
		SetServiceStatusMessage("test-svc", "error encountered when overlaying: mop some-mop, overlay <0>: test-error").
		SetPhase(PhaseFailed).
		SetStatusMessage("issues with applying overlay").
		Build().Status

	otherClusterMopSuccessStatus := otherClusterMop.Status.DeepCopy()
	otherClusterMopSuccessStatus.Phase = PhaseSucceeded
	otherClusterMopSuccessStatus.Services["test-svc"] = &v1alpha1.ServiceStatus{
		Phase: PhaseSucceeded,
	}

	otherClusterMopFailedStatus := otherClusterMop.Status.DeepCopy()
	otherClusterMopFailedStatus.Phase = PhaseFailed
	otherClusterMopFailedStatus.Message = "issues with applying overlay"
	otherClusterMopFailedStatus.Services["test-svc"] = &v1alpha1.ServiceStatus{
		Phase:   PhaseFailed,
		Message: "error encountered when overlaying: mop some-mop, overlay <0>: test-error",
	}

	testCases := []struct {
		name string
		// merged mop to update
		mergedMop *v1alpha1.MeshOperator
		// clusterName -> mops in cluster
		knownMops map[string][]*v1alpha1.MeshOperator
		errInfo   *meshOpErrors.OverlayErrorInfo
		// clusterName -> mop-name -> expected status
		expectedMopStatuses map[string]map[string]*v1alpha1.MeshOperatorStatus
	}{
		{
			name:      "service/mop in a single cluster (Success)",
			mergedMop: mop,
			knownMops: map[string][]*v1alpha1.MeshOperator{
				cluster1Name: {mop},
			},
			expectedMopStatuses: map[string]map[string]*v1alpha1.MeshOperatorStatus{
				cluster1Name: {
					"some-mop": &mopSuccessStatus,
				},
			},
		},
		{
			name:      "service/mop in a single cluster (Error)",
			mergedMop: mop,
			errInfo: &meshOpErrors.OverlayErrorInfo{
				OverlayIndex: 0,
				Message:      "test-error",
			},
			knownMops: map[string][]*v1alpha1.MeshOperator{
				cluster1Name: {mop},
			},
			expectedMopStatuses: map[string]map[string]*v1alpha1.MeshOperatorStatus{
				cluster1Name: {
					"some-mop": &mopFailedStatus,
				},
			},
		},
		{
			// Note: here it's important that only relevant service-status is updated in "otherClusterMop"
			name:      "service/mop in multiple clusters (Success)",
			mergedMop: mop,
			knownMops: map[string][]*v1alpha1.MeshOperator{
				cluster1Name: {mop},
				cluster2Name: {otherClusterMop},
			},
			expectedMopStatuses: map[string]map[string]*v1alpha1.MeshOperatorStatus{
				cluster1Name: {
					"some-mop": &mopSuccessStatus,
				},
				cluster2Name: {
					"some-mop": otherClusterMopSuccessStatus,
				},
			},
		},
		{
			name:      "service/mop in multiple clusters (Error)",
			mergedMop: mop,
			errInfo: &meshOpErrors.OverlayErrorInfo{
				OverlayIndex: 0,
				Message:      "test-error",
			},
			knownMops: map[string][]*v1alpha1.MeshOperator{
				cluster1Name: {mop},
				cluster2Name: {otherClusterMop},
			},
			expectedMopStatuses: map[string]map[string]*v1alpha1.MeshOperatorStatus{
				cluster1Name: {
					"some-mop": &mopFailedStatus,
				},
				cluster2Name: {
					"some-mop": otherClusterMopFailedStatus,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stopCh := make(chan struct{})
			defer close(stopCh)

			cluster1 := createClusterWithObjects(cluster1Name, stopCh, toRuntimeObjectList(tc.knownMops[cluster1Name]))
			cluster2 := createClusterWithObjects(cluster2Name, stopCh, toRuntimeObjectList(tc.knownMops[cluster2Name]))

			clusterStore := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
			clusterStore.SetPrimaryCluster(cluster1)
			clusterStore.AddClusterForKey("secret", cluster2)

			controller := MulticlusterServiceController{
				clusterManager:  clusterStore,
				metricsRegistry: prometheus.NewRegistry(),
			}

			err := controller.updateMopStatusAcrossClusters(
				zaptest.NewLogger(t).Sugar(),
				&testSvc,
				nil,
				tc.errInfo,
				tc.mergedMop,
				tc.knownMops)

			assert.Nil(t, err)

			for clusterName, mopStatuses := range tc.expectedMopStatuses {
				for mopName, expectedStatus := range mopStatuses {
					_, cluster := clusterStore.GetClusterById(clusterName)
					mopClient := cluster.GetKubeClient().MopApiClient().MeshV1alpha1()
					mopInCluster, err := mopClient.MeshOperators(testNamespace).Get(context.TODO(), mopName, metav1.GetOptions{})
					assert.Nil(t, err)
					assert.Equal(t, expectedStatus, &mopInCluster.Status)
				}
			}
		})
	}
}

func TestMulticlusterServiceController_updateMopStatus_RemoteClusterApiDown(t *testing.T) {
	cluster1Name := "primary"
	cluster2Name := "remote-1"

	testNamespace := "test-ns"
	testSvc := kube_test.NewServiceBuilder("test-svc", testNamespace).SetUID("some-uid").Build()

	mop := kube_test.NewMopBuilder(testNamespace, "some-mop").Build()

	stopCh := make(chan struct{})
	defer close(stopCh)

	cluster1 := createClusterWithObjects(cluster1Name, stopCh, toRuntimeObjectList([]*v1alpha1.MeshOperator{mop}))
	cluster2 := createClusterWithObjects(cluster2Name, stopCh, toRuntimeObjectList([]*v1alpha1.MeshOperator{mop}))

	cluster2FakeClient := cluster2.Client.(*kube_test.FakeClient)
	cluster2FakeClient.MopClient.PrependReactor("update", "meshoperators",
		func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, fmt.Errorf("remote cluster unreachable")
		},
	)

	clusterStore := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
	clusterStore.SetPrimaryCluster(cluster1)
	clusterStore.AddClusterForKey("secret", cluster2)

	controller := MulticlusterServiceController{
		clusterManager:  clusterStore,
		metricsRegistry: prometheus.NewRegistry(),
	}

	err := controller.updateMopStatusAcrossClusters(
		zaptest.NewLogger(t).Sugar(),
		&testSvc,
		nil,
		nil,
		mop,
		map[string][]*v1alpha1.MeshOperator{
			cluster1Name: {mop},
			cluster2Name: {mop},
		})

	assert.NotNil(t, err, "expected error when remote cluster kube-api is down")
	assert.Contains(t, err.Error(), "remote cluster unreachable")

	_, primaryCluster := clusterStore.GetClusterById(cluster1Name)
	mopInPrimary, getErr := primaryCluster.GetKubeClient().MopApiClient().MeshV1alpha1().MeshOperators(testNamespace).Get(context.TODO(), "some-mop", metav1.GetOptions{})
	assert.Nil(t, getErr)
	assert.Equal(t, PhaseSucceeded, mopInPrimary.Status.Phase, "primary cluster MOP status should still be updated")
}

func TestMulticlusterServiceController_updateCTPStatus(t *testing.T) {
	cluster1Name := "primary"
	testNamespace := "example-coreapp"
	testSvc := kube_test.NewServiceBuilder("ora2-casam-app", testNamespace).SetUID("some-uid").Build()

	testCTP := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mesh.io/v1alpha1",
			"kind":       "ClusterTrafficPolicy",
			"metadata": map[string]string{
				"name":      "ora2-casam-app",
				"namespace": "example-coreapp",
			},
		},
	}

	ctpObj := v1alpha1.ClusterTrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ora2-casam-app",
			Namespace: "example-coreapp",
		},
	}

	reconciledTime := metav1.Time{time.Now()}

	testReconcileError := "reconcile error - test"
	testCases := []struct {
		name              string
		reconcileErr      error
		expectedCTPStatus v1alpha1.ClusterTrafficPolicyStatus
	}{
		{
			name:         "no reconcile error",
			reconcileErr: nil,
			expectedCTPStatus: v1alpha1.ClusterTrafficPolicyStatus{
				State:              "Success",
				LastReconciledTime: &reconciledTime,
			},
		},
		{
			name:         "reconcile error",
			reconcileErr: errors.New(testReconcileError),
			expectedCTPStatus: v1alpha1.ClusterTrafficPolicyStatus{
				State:              "Failure",
				Message:            testReconcileError,
				LastReconciledTime: &reconciledTime,
			},
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			cluster1 := createClusterWithObjects(cluster1Name, stopCh, []runtime.Object{&ctpObj})

			clusterStore := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
			clusterStore.SetPrimaryCluster(cluster1)

			multiController := MulticlusterServiceController{
				clusterManager:  clusterStore,
				metricsRegistry: prometheus.NewRegistry(),
				primaryClient:   cluster1.Client,
			}

			err := multiController.updateCTPStatusOnReconcile(tc.reconcileErr, testCTP, reconciledTime)
			assert.Nil(t, err)

			ctpInCluster, err := multiController.primaryClient.MopApiClient().MeshV1alpha1().ClusterTrafficPolicies(testSvc.Namespace).Get(context.TODO(), testSvc.Name, metav1.GetOptions{})
			assert.Nil(t, err)

			assert.Equal(t, tc.expectedCTPStatus, ctpInCluster.Status)
		})
	}
}

func TestMulticlusterServiceController_extractStsMetadata_no_service(t *testing.T) {
	controller := MulticlusterServiceController{}

	actualMetadata, err := controller.extractStsMetadata(zaptest.NewLogger(t).Sugar(), map[string]*corev1.Service{})

	assert.Nil(t, err)
	assert.Equal(t, map[string]string{}, actualMetadata)
}

func TestMulticlusterServiceController_extractStsMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dateInThePast := metav1.Time{Time: time.Now().Add(-100 * time.Hour)}
	dateInTheFuture := metav1.Time{Time: time.Now().Add(100 * time.Hour)}

	olderSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").SetUID("older-svc-uid").Build()
	olderSvc.CreationTimestamp = dateInThePast

	newerSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").SetUID("newer-svc-uid").Build()
	newerSvc.CreationTimestamp = dateInTheFuture

	olderSvcSts := kube_test.CreateStatefulSet("test-ns", "olderSts", "test-svc", 3)
	newerSvcSts := kube_test.CreateStatefulSet("test-ns", "newerSts", "test-svc", 5)

	client1 := kube_test.NewKubeClientBuilder().AddK8sObjects(newerSvcSts).Build()
	cluster1 := &Cluster{ID: cluster.ID("cluster-1"), Client: client1, namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{}}

	client2 := kube_test.NewKubeClientBuilder().AddK8sObjects(olderSvcSts).Build()
	cluster2 := &Cluster{ID: cluster.ID("cluster-2"), Client: client2, namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{}}

	clusterManager := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
	clusterManager.SetPrimaryCluster(cluster1)
	clusterManager.AddClusterForKey("secret", cluster2)

	controller := MulticlusterServiceController{
		logger:         zaptest.NewLogger(t).Sugar(),
		clusterManager: clusterManager,
	}
	_ = controller.OnNewClusterAdded(cluster1)
	_ = controller.OnNewClusterAdded(cluster2)

	_ = client1.RunAndWait(ctx.Done(), true, nil)
	_ = client2.RunAndWait(ctx.Done(), true, nil)

	stsInformer1 := cluster1.Client.KubeInformerFactory().Apps().V1().StatefulSets().Informer()
	go stsInformer1.Run(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), stsInformer1.HasSynced)

	stsInformer2 := cluster2.Client.KubeInformerFactory().Apps().V1().StatefulSets().Informer()
	go stsInformer2.Run(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), stsInformer2.HasSynced)

	actualMetadata, err := controller.extractStsMetadata(
		zaptest.NewLogger(t).Sugar(),
		map[string]*corev1.Service{
			"cluster-1": &newerSvc,
			"cluster-2": &olderSvc,
		})

	expectedMetadata := map[string]string{
		"statefulSetReplicaName":   "olderSts",
		"statefulSetReplicas":      "3",
		"statefulSetOrdinalsStart": "0",
		"statefulSets":             "[{\"statefulSetReplicaName\":\"olderSts\",\"statefulSetReplicas\":\"3\"}]",
	}
	assert.Nil(t, err)
	assert.Equal(t, expectedMetadata, actualMetadata)
}

func TestMulticlusterServiceController_extractDynamicRoutingMetadata_no_service(t *testing.T) {
	controller := MulticlusterServiceController{}

	actualMetadata, err := controller.extractDynamicRoutingMetadata(zaptest.NewLogger(t).Sugar(), map[string]*corev1.Service{}, nil)

	assert.Nil(t, err)
	assert.Equal(t, map[string]string{}, actualMetadata)
}

func TestMulticlusterServiceController_extractDynamicRoutingMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	features.EnableDynamicRouting = true
	defer func() {
		features.EnableDynamicRouting = false
		cancel()
	}()

	dateInThePast := metav1.Time{Time: time.Now().Add(-100 * time.Hour)}
	dateInTheFuture := metav1.Time{Time: time.Now().Add(100 * time.Hour)}

	olderDynamicRoutingSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").
		SetAnnotations(map[string]string{
			string(constants.DynamicRoutingLabels): "version,app",
		}).Build()
	olderDynamicRoutingSvc.CreationTimestamp = dateInThePast

	newerDynamicRoutingSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").
		SetAnnotations(map[string]string{
			string(constants.DynamicRoutingLabels): "app",
		}).Build()
	newerDynamicRoutingSvc.CreationTimestamp = dateInTheFuture

	matchingDeployment := kube_test.NewDeploymentBuilder("test-ns", "deployment1").
		SetLabels(map[string]string{string(constants.DynamicRoutingServiceLabel): "test-svc"}).
		SetTemplateLabels(map[string]string{
			"version": "v1",
			"app":     "my-app",
		}).
		Build()

	otherMatchingDeployment := kube_test.NewDeploymentBuilder("test-ns", "deployment2").
		SetLabels(map[string]string{string(constants.DynamicRoutingServiceLabel): "test-svc"}).
		SetTemplateLabels(map[string]string{
			"version": "v2",
			"app":     "my-app",
		}).
		Build()

	expectedMetadata := map[string]string{
		"dynamicRoutingCombinations": "{\"Deployment\":{\"v1-my-app\":{\"app\":\"my-app\",\"version\":\"v1\"},\"v2-my-app\":{\"app\":\"my-app\",\"version\":\"v2\"}}}",
	}

	client1 := kube_test.NewKubeClientBuilder().Build()
	cluster1 := &Cluster{ID: cluster.ID("cluster-1"), Client: client1, namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{}}

	client2 := kube_test.NewKubeClientBuilder().AddK8sObjects(matchingDeployment, otherMatchingDeployment).Build()
	cluster2 := &Cluster{ID: cluster.ID("cluster-2"), Client: client2, namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{}}

	clusterManager := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
	clusterManager.SetPrimaryCluster(cluster1)
	clusterManager.AddClusterForKey("secret", cluster2)

	controller := MulticlusterServiceController{
		logger:          zaptest.NewLogger(t).Sugar(),
		clusterManager:  clusterManager,
		metricsRegistry: prometheus.NewRegistry(),
	}
	_ = controller.OnNewClusterAdded(cluster1)
	_ = controller.OnNewClusterAdded(cluster2)

	_ = client1.RunAndWait(ctx.Done(), true, nil)
	_ = client2.RunAndWait(ctx.Done(), true, nil)

	deploymentsInformer1 := cluster1.Client.KubeInformerFactory().Apps().V1().Deployments().Informer()
	go deploymentsInformer1.Run(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), deploymentsInformer1.HasSynced)

	deploymentsInformer2 := cluster2.Client.KubeInformerFactory().Apps().V1().Deployments().Informer()
	go deploymentsInformer2.Run(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), deploymentsInformer2.HasSynced)

	activeServices := map[string]*corev1.Service{
		"cluster-1": &newerDynamicRoutingSvc,
		"cluster-2": &olderDynamicRoutingSvc,
	}

	mergedService := multicluster.MergeServices(GetServices(activeServices), "1234567")
	actualMetadata, err := controller.extractDynamicRoutingMetadata(zaptest.NewLogger(t).Sugar(), activeServices, mergedService)

	assert.Nil(t, err)
	assert.Equal(t, expectedMetadata, actualMetadata)
}

func TestMulticlusterServiceController_extractDynamicRoutingMetadataStriped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	features.EnableDynamicRouting = true
	defer func() {
		features.EnableDynamicRouting = false
		cancel()
	}()

	dateInThePast := metav1.Time{Time: time.Now().Add(-100 * time.Hour)}
	dateInTheFuture := metav1.Time{Time: time.Now().Add(100 * time.Hour)}

	olderDynamicRoutingSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").
		SetAnnotations(map[string]string{
			string(constants.DynamicRoutingLabels):             "version,app",
			string(constants.DynamicRoutingDefaultCoordinates): "version=v1,app=my-app",
		}).Build()
	olderDynamicRoutingSvc.CreationTimestamp = dateInThePast

	newerDynamicRoutingSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").
		SetAnnotations(map[string]string{
			string(constants.DynamicRoutingLabels):             "app",
			string(constants.DynamicRoutingDefaultCoordinates): "version=v2,app=my-app",
		}).Build()
	newerDynamicRoutingSvc.CreationTimestamp = dateInTheFuture

	cluster2matchingDeployment := kube_test.NewDeploymentBuilder("test-ns", "deployment1").
		SetLabels(map[string]string{string(constants.DynamicRoutingServiceLabel): "test-svc"}).
		SetTemplateLabels(map[string]string{
			"version": "v1",
			"app":     "my-app",
		}).
		Build()

	cluster2otherMatchingDeployment := kube_test.NewDeploymentBuilder("test-ns", "deployment2").
		SetLabels(map[string]string{string(constants.DynamicRoutingServiceLabel): "test-svc"}).
		SetTemplateLabels(map[string]string{
			"version": "v2",
			"app":     "my-app",
		}).
		Build()

	cluster1matchingDeployment := kube_test.NewDeploymentBuilder("test-ns", "deployment1").
		SetLabels(map[string]string{string(constants.DynamicRoutingServiceLabel): "test-svc"}).
		SetTemplateLabels(map[string]string{
			"version": "v3",
			"app":     "my-app",
		}).
		Build()

	cluster1otherMatchingDeployment := kube_test.NewDeploymentBuilder("test-ns", "deployment2").
		SetLabels(map[string]string{string(constants.DynamicRoutingServiceLabel): "test-svc"}).
		SetTemplateLabels(map[string]string{
			"version": "v4",
			"app":     "my-app",
		}).
		Build()

	expectedMetadata := map[string]string{
		"dynamicRoutingCombinations":  "{\"Deployment\":{\"v1-my-app\":{\"app\":\"my-app\",\"version\":\"v1\"},\"v2-my-app\":{\"app\":\"my-app\",\"version\":\"v2\"},\"v3-my-app\":{\"app\":\"my-app\",\"version\":\"v3\"},\"v4-my-app\":{\"app\":\"my-app\",\"version\":\"v4\"}}}",
		"dynamicRoutingDefaultSubset": "v1-my-app",
	}

	client1 := kube_test.NewKubeClientBuilder().AddK8sObjects(cluster1matchingDeployment, cluster1otherMatchingDeployment).Build()
	cluster1 := &Cluster{ID: cluster.ID("cluster-1"), Client: client1, namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{}}

	client2 := kube_test.NewKubeClientBuilder().AddK8sObjects(cluster2matchingDeployment, cluster2otherMatchingDeployment).Build()
	cluster2 := &Cluster{ID: cluster.ID("cluster-2"), Client: client2, namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{}}

	clusterManager := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
	clusterManager.SetPrimaryCluster(cluster1)
	clusterManager.AddClusterForKey("secret", cluster2)

	controller := MulticlusterServiceController{
		logger:          zaptest.NewLogger(t).Sugar(),
		clusterManager:  clusterManager,
		metricsRegistry: prometheus.NewRegistry(),
	}
	_ = controller.OnNewClusterAdded(cluster1)
	_ = controller.OnNewClusterAdded(cluster2)

	_ = client1.RunAndWait(ctx.Done(), true, nil)
	_ = client2.RunAndWait(ctx.Done(), true, nil)

	deploymentsInformer1 := cluster1.Client.KubeInformerFactory().Apps().V1().Deployments().Informer()
	go deploymentsInformer1.Run(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), deploymentsInformer1.HasSynced)

	deploymentsInformer2 := cluster2.Client.KubeInformerFactory().Apps().V1().Deployments().Informer()
	go deploymentsInformer2.Run(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), deploymentsInformer2.HasSynced)

	activeServices := map[string]*corev1.Service{
		"cluster-1": &newerDynamicRoutingSvc,
		"cluster-2": &olderDynamicRoutingSvc,
	}

	mergedService := multicluster.MergeServices(GetServices(activeServices), "1234567")
	actualMetadata, err := controller.extractDynamicRoutingMetadata(zaptest.NewLogger(t).Sugar(), activeServices, mergedService)

	assert.Nil(t, err)
	assert.Equal(t, expectedMetadata, actualMetadata)
}

func TestMulticlusterServiceController_extractDynamicRoutingMetadataStriped_RemoteClusterUnhealthy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	features.EnableDynamicRouting = true
	defer func() {
		features.EnableDynamicRouting = false
		cancel()
	}()

	dynamicRoutingSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").
		SetAnnotations(map[string]string{
			string(constants.DynamicRoutingLabels):             "version,app",
			string(constants.DynamicRoutingDefaultCoordinates): "version=v3,app=my-app",
		}).Build()

	// Cluster1 has deployments with versions v3, v4
	cluster1Deployment1 := kube_test.NewDeploymentBuilder("test-ns", "deployment1").
		SetLabels(map[string]string{string(constants.DynamicRoutingServiceLabel): "test-svc"}).
		SetTemplateLabels(map[string]string{"version": "v3", "app": "my-app"}).
		Build()
	cluster1Deployment2 := kube_test.NewDeploymentBuilder("test-ns", "deployment2").
		SetLabels(map[string]string{string(constants.DynamicRoutingServiceLabel): "test-svc"}).
		SetTemplateLabels(map[string]string{"version": "v4", "app": "my-app"}).
		Build()

	client1 := kube_test.NewKubeClientBuilder().AddK8sObjects(cluster1Deployment1, cluster1Deployment2).Build()
	cluster1 := &Cluster{ID: cluster.ID("cluster-1"), Client: client1, namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{}}

	clusterManager := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
	clusterManager.SetPrimaryCluster(cluster1)

	controller := MulticlusterServiceController{
		logger:          zaptest.NewLogger(t).Sugar(),
		clusterManager:  clusterManager,
		metricsRegistry: prometheus.NewRegistry(),
	}
	_ = controller.OnNewClusterAdded(cluster1)
	_ = client1.RunAndWait(ctx.Done(), true, nil)

	deploymentsInformer1 := cluster1.Client.KubeInformerFactory().Apps().V1().Deployments().Informer()
	go deploymentsInformer1.Run(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), deploymentsInformer1.HasSynced)

	// Simulate cluster-2 being unhealthy: only cluster-1 in activeServices
	// In real scenario, cluster-2 would have deployments with v1, v2 but is excluded
	activeServices := map[string]*corev1.Service{
		"cluster-1": &dynamicRoutingSvc,
	}

	// Only v3, v4 expected (v1, v2 from unhealthy cluster-2 are MISSING)
	expectedMetadata := map[string]string{
		"dynamicRoutingCombinations":  "{\"Deployment\":{\"v3-my-app\":{\"app\":\"my-app\",\"version\":\"v3\"},\"v4-my-app\":{\"app\":\"my-app\",\"version\":\"v4\"}}}",
		"dynamicRoutingDefaultSubset": "v3-my-app",
	}

	mergedService := multicluster.MergeServices(GetServices(activeServices), "1234567")
	actualMetadata, err := controller.extractDynamicRoutingMetadata(zaptest.NewLogger(t).Sugar(), activeServices, mergedService)

	assert.Nil(t, err)
	assert.Equal(t, expectedMetadata, actualMetadata)
}

func TestMulticlusterServiceController_ReadServiceObjects(t *testing.T) {
	stopCh := make(chan struct{})
	defer close(stopCh)

	service := kube_test.NewServiceBuilder("test-svc", "test-ns").SetUID("some-uid").Build()
	otherService := kube_test.NewServiceBuilder("test-svc", "test-ns").SetUID("other-uid").Build()

	cluster1 := createClusterWithObjects("cluster-1", stopCh, nil, &service)
	cluster2 := createClusterWithObjects("cluster-2", stopCh, nil, &otherService)
	cluster3 := createClusterWithObjects("cluster-3", stopCh, nil)

	clusterStore := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
	clusterStore.SetPrimaryCluster(cluster1)
	clusterStore.AddClusterForKey("secret", cluster2)
	clusterStore.AddClusterForKey("secret", cluster3)

	controller := MulticlusterServiceController{
		clusterManager: clusterStore,
	}

	actualServices, err := controller.readServicesFromKnownClusters("test-ns", "test-svc")

	expectedServices := map[string]*corev1.Service{
		"cluster-1": &service,
		"cluster-2": &otherService,
	}
	assert.Nil(t, err)
	assert.Equal(t, expectedServices, actualServices)
}

func TestMulticlusterServiceController_GetMergedMopsForSerice(t *testing.T) {
	stopCh := make(chan struct{})
	defer close(stopCh)

	dateInThePast := metav1.Time{Time: time.Now().Add(-100 * time.Hour)}
	dateInTheFuture := metav1.Time{Time: time.Now().Add(100 * time.Hour)}

	testSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").
		SetLabels(map[string]string{
			"app": "test-svc-label",
		}).Build()

	otherSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").
		SetLabels(map[string]string{
			"app": "test-svc-label",
		}).Build()

	yetAnotherSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").
		SetLabels(map[string]string{
			"app": "yet-another-svc-label",
		}).Build()

	nonMatchingMop := kube_test.NewMopBuilder("test-ns", "non-matching-mop").
		AddSelector("app", "non-matching-label").
		Build()

	nonConflictingMop := kube_test.NewMopBuilder("test-ns", "non-conflicting-mop").
		AddSelector("app", "test-svc-label").
		Build()

	conflictingNewerMop := kube_test.NewMopBuilder("test-ns", "conflicting-mop").
		AddSelector("app", "test-svc-label").
		SetUID("newer-mop-uid").
		Build()
	conflictingNewerMop.CreationTimestamp = dateInTheFuture

	otherNonConflictingMop := kube_test.NewMopBuilder("test-ns", "other-non-conflicting-mop").
		AddSelector("app", "test-svc-label").
		Build()

	conflictingOlderMop := kube_test.NewMopBuilder("test-ns", "conflicting-mop").
		AddSelector("app", "test-svc-label").
		SetUID("older-mop-uid").
		Build()
	conflictingOlderMop.CreationTimestamp = dateInThePast

	cluster1 := createClusterWithObjects("cluster-1", stopCh, []runtime.Object{nonMatchingMop, nonConflictingMop, conflictingNewerMop})
	cluster2 := createClusterWithObjects("cluster-2", stopCh, []runtime.Object{})
	cluster3 := createClusterWithObjects("cluster-3", stopCh, []runtime.Object{otherNonConflictingMop, conflictingOlderMop})

	clusterStore := controllers_api.NewClustersStore(zaptest.NewLogger(t).Sugar(), nil, time.Second, time.Second)
	clusterStore.SetPrimaryCluster(cluster1)
	clusterStore.AddClusterForKey("secret", cluster2)
	clusterStore.AddClusterForKey("secret", cluster3)

	controller := MulticlusterServiceController{
		clusterManager: clusterStore,
	}

	testCases := []struct {
		name         string
		services     map[string]*corev1.Service
		expectedMops []*v1alpha1.MeshOperator
	}{
		{
			name: "single service passed - no matching mops",
			services: map[string]*corev1.Service{
				"cluster-2": &yetAnotherSvc,
			},
			expectedMops: []*v1alpha1.MeshOperator{},
		},
		{
			name: "single service passed - there are matching mops",
			services: map[string]*corev1.Service{
				"cluster-1": &testSvc,
			},
			expectedMops: []*v1alpha1.MeshOperator{nonConflictingMop, conflictingNewerMop},
		},
		{
			name: "multiple services - mix of matching mops",
			services: map[string]*corev1.Service{
				"cluster-1": &testSvc,
				"cluster-2": &yetAnotherSvc,
				"cluster-3": &otherSvc,
			},
			expectedMops: []*v1alpha1.MeshOperator{nonConflictingMop, otherNonConflictingMop, conflictingOlderMop},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mopsPerCluster, err := controller.getMopsForService(tc.services)
			assert.Nil(t, err)

			actualMops := multicluster.MergeMeshOperators(mopsPerCluster)

			assert.ElementsMatch(t, tc.expectedMops, actualMops)
		})
	}
}

func TestGetPoliciesForService(t *testing.T) {
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"}}
	ctp := &unstructured.Unstructured{Object: map[string]interface{}{"kind": "ClusterTrafficPolicy"}}
	tsp := &unstructured.Unstructured{Object: map[string]interface{}{"kind": "TrafficShardingPolicy"}}

	testCases := []struct {
		name                    string
		ctpEnabled              bool
		tspEnabled              bool
		additionalObjectManager AdditionalObjectManager
		expectedPolicies        map[string]*unstructured.Unstructured
		expectError             bool
		expectedErrorMessage    string
	}{
		{
			name:       "both policies enabled and found",
			ctpEnabled: true,
			tspEnabled: true,
			additionalObjectManager: &mockAdditionalObjectManager{
				ctp: ctp,
				tsp: tsp,
			},
			expectedPolicies: map[string]*unstructured.Unstructured{
				constants.ClusterTrafficPolicyName: ctp,
				// TSP is now handled by dedicated TrafficShardController, not returned here
			},
			expectError: false,
		},
		{
			name:       "only ctp enabled and found",
			ctpEnabled: true,
			tspEnabled: false,
			additionalObjectManager: &mockAdditionalObjectManager{
				ctp: ctp,
			},
			expectedPolicies: map[string]*unstructured.Unstructured{
				constants.ClusterTrafficPolicyName: ctp,
			},
			expectError: false,
		},
		{
			name:       "only tsp enabled and found",
			ctpEnabled: false,
			tspEnabled: true,
			additionalObjectManager: &mockAdditionalObjectManager{
				tsp: tsp,
			},
			expectedPolicies: map[string]*unstructured.Unstructured{
				// TSP is now handled by dedicated TrafficShardController, not returned here
			},
			expectError: false,
		},
		{
			name:                    "no policies found",
			ctpEnabled:              true,
			tspEnabled:              true,
			additionalObjectManager: &mockAdditionalObjectManager{},
			expectedPolicies:        map[string]*unstructured.Unstructured{},
			expectError:             false,
		},
		{
			name:       "error fetching policies",
			ctpEnabled: true,
			tspEnabled: true,
			additionalObjectManager: &mockAdditionalObjectManager{
				err: fmt.Errorf("test error"),
			},
			expectedPolicies: map[string]*unstructured.Unstructured{},
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	primaryCluster := createClusterWithObjects("testCluster", stopCh, []runtime.Object{})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.EnableMulticlusterCTP = tc.ctpEnabled
			features.EnableTrafficShardingPolicyController = tc.tspEnabled
			defer func() {
				features.EnableMulticlusterCTP = false
				features.EnableTrafficShardingPolicyController = false
			}()

			c := &MulticlusterServiceController{
				additionalObjectManager: tc.additionalObjectManager,
				logger:                  zaptest.NewLogger(t).Sugar(),
				primaryClient:           primaryCluster.Client,
			}

			err, policies := c.getPoliciesForService(service)

			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErrorMessage)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedPolicies, policies)
			}
		})
	}
}

func createClusterWithObjects(clusterName string, stop <-chan struct{}, mops []runtime.Object, k8sObjects ...runtime.Object) *Cluster {
	client := kube_test.NewKubeClientBuilder().
		AddK8sObjects(k8sObjects...).
		AddMopClientObjects(mops...).
		Build()

	newCluster := &Cluster{
		ID:     cluster.ID(clusterName),
		Client: client,
	}

	_ = client.RunAndWait(stop, true, nil)
	return newCluster
}

func toRuntimeObjectList(mops []*v1alpha1.MeshOperator) []runtime.Object {
	var runtimeObjects []runtime.Object
	for _, mop := range mops {
		runtimeObjects = append(runtimeObjects, mop)
	}

	return runtimeObjects
}

type mockAdditionalObjectManager struct {
	ctp *unstructured.Unstructured
	tsp *unstructured.Unstructured
	err error
}

func (m *mockAdditionalObjectManager) AddIndexersToSvcInformer(_ kube.Client) error {
	return nil
}

func (m *mockAdditionalObjectManager) GetInformers() []informers.GenericInformer {
	return nil
}

func (m *mockAdditionalObjectManager) CreateInformersForAddObjs(_ kube.Client, _ controllers_api.ClusterManager, _ *zap.SugaredLogger) error {
	return nil
}

func (m *mockAdditionalObjectManager) GetAdditionalObjectsForExtension(_ *v1alpha1.MeshOperator, _ *unstructured.Unstructured) (map[string]*templating.AdditionalObjects, error) {
	return nil, nil
}

func (m *mockAdditionalObjectManager) GetAdditionalObjectsForService(policyName string, _ *corev1.Service) ([]*unstructured.Unstructured, error) {
	if m.err != nil {
		return nil, m.err
	}
	if policyName == constants.ClusterTrafficPolicyName && m.ctp != nil {
		return []*unstructured.Unstructured{m.ctp}, nil
	}
	if policyName == constants.TrafficShardingPolicyName && m.tsp != nil {
		return []*unstructured.Unstructured{m.tsp}, nil
	}

	return nil, fmt.Errorf("additional object missing")
}
