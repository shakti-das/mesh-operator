package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating_test"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/istio-ecosystem/mesh-operator/pkg/cluster"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/controller_test"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/istio-ecosystem/mesh-operator/pkg/resources_test"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestMulticlusterKnativeIngressController_onNewClusterAdded(t *testing.T) {
	testIng := kube_test.NewKnativeIngressBuilder("test-ing", "test-ns", "test-rv-1").Build()

	fakeDiscoveryClient := &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{}}
	fakeDiscoveryClient.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: schema.GroupVersion{Group: constants.KnativeGroup, Version: constants.KnativeVersion}.String(),
			APIResources: []metav1.APIResource{
				{
					Name: "ingresses",
					Kind: constants.KnativeIngressKind.Kind,
				},
			},
		},
	}

	fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), testIng)
	kubeClient := &kube_test.FakeClient{
		DynamicClient:   fakeDynamicClient,
		DiscoveryClient: fakeDiscoveryClient,
	}

	kubeClient.InitFactory()
	kubeClient.DynamicInformerFactory().ForResource(constants.KnativeIngressResource).Informer().GetIndexer().Add(testIng)

	logger := zaptest.NewLogger(t).Sugar()
	testCluster := &Cluster{ID: cluster.ID("cluster-1"), Client: kubeClient, namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{}}

	clusterStore := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
	clusterStore.SetPrimaryCluster(testCluster)

	controller := KnativeIngressController{
		logger:         logger,
		clusterManager: clusterStore,
	}

	err := controller.OnNewClusterAdded(testCluster)
	assert.Nil(t, err)
}

func TestMulticlusterKnativeIngressController_reconcile(t *testing.T) {

	testCases := []struct {
		name           string
		item           QueueItem
		isErrNil       bool
		expectedStatus *IngressStatus
	}{
		{
			name: "successful add/update",
			item: NewQueueItem(
				"cluster-1",
				"test-ns/test-ing",
				"test-uid",
				controllers_api.EventAdd),
			isErrNil: true,
			expectedStatus: &IngressStatus{
				Status: Status{
					Conditions: []Condition{
						{
							Type:   LoadBalancerReady,
							Status: metav1.ConditionTrue,
						},
						{
							Type:   NetworkConfigured,
							Status: metav1.ConditionTrue,
						},
						{
							Type:   Ready,
							Status: metav1.ConditionTrue,
						},
					},
					PrivateLoadBalancer: LoadBalancerStatus{
						Ingress: []LoadBalancerIngressStatus{
							{
								MeshOnly: true,
							},
						},
					},
				},
			},
		},
		{
			name: "successful delete",
			item: NewQueueItem(
				"cluster-1",
				"test-ns/test-ing",
				"test-uid",
				controllers_api.EventDelete),
			isErrNil: true,
			expectedStatus: &IngressStatus{
				Status: Status{
					Conditions: nil,
				},
			},
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testIng := kube_test.NewKnativeIngressBuilder("test-ing", "test-ns", "test-rv-1").Build()

			fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), testIng)
			kubeClient := &kube_test.FakeClient{
				DynamicClient: fakeDynamicClient,
			}
			kubeClient.InitFactory()
			kubeClient.DynamicInformerFactory().ForResource(constants.KnativeIngressResource).Informer().GetIndexer().Add(testIng)
			testCluster := &Cluster{ID: cluster.ID(tc.item.cluster), Client: kubeClient}

			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			clusterManager.SetPrimaryCluster(testCluster)

			ingressQueue := createWorkQueue("knative-ingress-multicluster")

			renderer := &templating_test.FixedResultRenderer{
				ResultToReturn: templating.GeneratedConfig{
					Config: map[string][]*unstructured.Unstructured{
						templateType: {
							{
								Object: map[string]interface{}{
									"apiVersion": "networking.istio.io/v1alpha3",
									"kind":       "VirtualService",
								},
							},
						},
					},
					TemplateType: templateType,
				},
			}

			applicator := &templating_test.TestableApplicator{
				AppliedResults: []templating_test.GeneratedConfigMetadata{},
			}

			metricsRegistry := prometheus.NewRegistry()

			configGenerator := NewConfigGenerator(renderer, []templating.Mutator{}, nil, applicator, templating.DontApplyOverlays, logger, metricsRegistry)

			controller := &KnativeIngressController{
				logger:                       logger,
				configGenerator:              configGenerator,
				ingressEnqueuer:              NewSingleQueueEnqueuer(ingressQueue),
				clusterManager:               clusterManager,
				resourceManager:              resources_test.NewFakeResourceManager(),
				metricsRegistry:              metricsRegistry,
				multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
			}

			err := controller.reconcile(tc.item)

			assert.Nil(t, err)

			// Verify the status update
			updatedIngress, err := fakeDynamicClient.Resource(constants.KnativeIngressResource).Namespace("test-ns").Get(context.TODO(), "test-ing", metav1.GetOptions{})
			assert.NoError(t, err)

			actualStatus := &IngressStatus{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(updatedIngress.UnstructuredContent(), actualStatus)
			assert.NoError(t, err)

			if tc.item.event == controllers_api.EventAdd || tc.item.event == controllers_api.EventUpdate {
				for i := range len(actualStatus.Status.Conditions) {
					expectedCondition := tc.expectedStatus.Status.Conditions[i]
					actualCondition := actualStatus.Status.Conditions[i]
					assert.Equal(t, expectedCondition.Type, actualCondition.Type)
					assert.Equal(t, expectedCondition.Status, actualCondition.Status)
				}
				assert.NotNil(t, actualStatus.Status.PrivateLoadBalancer.Ingress)
				assert.True(t, actualStatus.Status.PrivateLoadBalancer.Ingress[0].MeshOnly)
			}

			if tc.item.event == controllers_api.EventDelete {
				assert.Nil(t, actualStatus.Status.Conditions)
			}
		})
	}
}

func TestEventHandler_onAdd(t *testing.T) {
	clusterName := "cluster-1"
	queue := &TestableQueue{}
	ingressEnqueuer := NewSingleQueueEnqueuer(queue)
	logger := zaptest.NewLogger(t).Sugar()
	eventHandler := NewKnativeControllerEventHandler(ingressEnqueuer, clusterName, logger)

	ing := kube_test.NewKnativeIngressBuilder("test-ing", "test-ns", "test-rv-1").Build()

	eventHandler.OnAdd(ing, false)

	expectedQueueItem := NewQueueItem(
		clusterName,
		"test-ns/test-ing",
		"test-uid",
		controllers_api.EventAdd)

	assert.Equal(t, 1, len(queue.recordedItems))
	assert.Equal(t, expectedQueueItem, queue.recordedItems[0])
}

func TestEventHandler_onUpdate(t *testing.T) {
	clusterName := "cluster-1"
	queue := &TestableQueue{}
	ingressEnqueuer := NewSingleQueueEnqueuer(queue)
	logger := zaptest.NewLogger(t).Sugar()
	eventHandler := NewKnativeControllerEventHandler(ingressEnqueuer, clusterName, logger)

	oldIng := kube_test.NewKnativeIngressBuilder("test-ing", "test-ns", "test-rv-1").Build()

	newIng := kube_test.NewKnativeIngressBuilder("test-ing", "test-ns", "test-rv-2").Build()

	eventHandler.OnUpdate(oldIng, newIng)

	expectedQueueItem := NewQueueItem(
		clusterName,
		"test-ns/test-ing",
		"test-uid",
		controllers_api.EventUpdate)

	assert.Equal(t, 1, len(queue.recordedItems))
	assert.Equal(t, expectedQueueItem, queue.recordedItems[0])
}

func TestEventHandler_onDelete(t *testing.T) {
	clusterName := "cluster-1"
	queue := &TestableQueue{}
	ingressEnqueuer := NewSingleQueueEnqueuer(queue)
	logger := zaptest.NewLogger(t).Sugar()
	eventHandler := NewKnativeControllerEventHandler(ingressEnqueuer, clusterName, logger)

	ing := kube_test.NewKnativeIngressBuilder("test-ing", "test-ns", "test-rv-1").Build()

	eventHandler.OnDelete(ing)

	expectedQueueItem := NewQueueItem(
		clusterName,
		"test-ns/test-ing",
		"test-uid",
		controllers_api.EventDelete)

	assert.Equal(t, 1, len(queue.recordedItems))
	assert.Equal(t, expectedQueueItem, queue.recordedItems[0])
}
