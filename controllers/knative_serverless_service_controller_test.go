package controllers

import (
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

const serverlessServiceTemplateType = "serverless-service"

func TestMulticlusterKnativeServerlessServiceController_onNewClusterAdded(t *testing.T) {
	testServerlessService := kube_test.NewKnativeServerlessServiceBuilder("test-sls", "test-ns", "test-rv-1").Build()

	fakeDiscoveryClient := &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{}}
	fakeDiscoveryClient.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: schema.GroupVersion{Group: constants.KnativeGroup, Version: constants.KnativeVersion}.String(),
			APIResources: []metav1.APIResource{
				{
					Name: "serverlessservices",
					Kind: constants.KnativeServerlessServiceKind.Kind,
				},
			},
		},
	}

	fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), testServerlessService)
	kubeClient := &kube_test.FakeClient{
		DynamicClient:   fakeDynamicClient,
		DiscoveryClient: fakeDiscoveryClient,
	}

	kubeClient.InitFactory()
	kubeClient.DynamicInformerFactory().ForResource(constants.KnativeServerlessServiceResource).Informer().GetIndexer().Add(testServerlessService)

	logger := zaptest.NewLogger(t).Sugar()
	testCluster := &Cluster{ID: cluster.ID("cluster-1"), Client: kubeClient, namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{}}

	clusterStore := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
	clusterStore.SetPrimaryCluster(testCluster)

	controller := KnativeServerlessServiceController{
		logger:         logger,
		clusterManager: clusterStore,
	}

	err := controller.OnNewClusterAdded(testCluster)
	assert.Nil(t, err)
}

func TestMulticlusterKnativeServerlessServiceController_reconcile(t *testing.T) {

	testCases := []struct {
		name     string
		item     QueueItem
		isErrNil bool
	}{
		{
			name: "successful add/update",
			item: NewQueueItem(
				"cluster-1",
				"test-ns/test-sls",
				"test-uid",
				controllers_api.EventAdd),
			isErrNil: true,
		},
		{
			name: "successful delete",
			item: NewQueueItem(
				"cluster-1",
				"test-ns/test-sls",
				"test-uid",
				controllers_api.EventDelete),
			isErrNil: true,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testServerlessService := kube_test.NewKnativeServerlessServiceBuilder("test-sls", "test-ns", "test-rv-1").Build()

			fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), testServerlessService)
			kubeClient := &kube_test.FakeClient{
				DynamicClient: fakeDynamicClient,
			}
			kubeClient.InitFactory()
			kubeClient.DynamicInformerFactory().ForResource(constants.KnativeServerlessServiceResource).Informer().GetIndexer().Add(testServerlessService)
			testCluster := &Cluster{ID: cluster.ID(tc.item.cluster), Client: kubeClient}

			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			clusterManager.SetPrimaryCluster(testCluster)

			serverlessServiceQueue := createWorkQueue("knative-serverless-service-multicluster")

			renderer := &templating_test.FixedResultRenderer{
				ResultToReturn: templating.GeneratedConfig{
					Config: map[string][]*unstructured.Unstructured{
						serverlessServiceTemplateType: {
							{
								Object: map[string]interface{}{
									"apiVersion": "networking.istio.io/v1alpha3",
									"kind":       "DestinationRule",
								},
							},
						},
					},
					TemplateType: serverlessServiceTemplateType,
				},
			}

			applicator := &templating_test.TestableApplicator{
				AppliedResults: []templating_test.GeneratedConfigMetadata{},
			}

			metricsRegistry := prometheus.NewRegistry()

			configGenerator := NewConfigGenerator(renderer, []templating.Mutator{}, nil, applicator, templating.DontApplyOverlays, logger, metricsRegistry)

			controller := &KnativeServerlessServiceController{
				logger:                       logger,
				configGenerator:              configGenerator,
				serverlessServiceEnqueuer:    NewSingleQueueEnqueuer(serverlessServiceQueue),
				clusterManager:               clusterManager,
				resourceManager:              resources_test.NewFakeResourceManager(),
				metricsRegistry:              metricsRegistry,
				multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
			}

			err := controller.reconcile(tc.item)

			assert.Nil(t, err)
		})
	}
}

func TestServerlessServiceEventHandler_onAdd(t *testing.T) {
	clusterName := "cluster-1"
	queue := &TestableQueue{}
	serverlessServiceEnqueuer := NewSingleQueueEnqueuer(queue)
	logger := zaptest.NewLogger(t).Sugar()
	eventHandler := NewServerlessServiceControllerEventHandler(serverlessServiceEnqueuer, clusterName, logger)

	sls := kube_test.NewKnativeServerlessServiceBuilder("test-sls", "test-ns", "test-rv-1").Build()

	eventHandler.OnAdd(sls, false)

	expectedQueueItem := NewQueueItem(
		clusterName,
		"test-ns/test-sls",
		"test-uid",
		controllers_api.EventAdd)

	assert.Equal(t, 1, len(queue.recordedItems))
	assert.Equal(t, expectedQueueItem, queue.recordedItems[0])
}

func TestServerlessServiceEventHandler_onUpdate(t *testing.T) {
	clusterName := "cluster-1"
	queue := &TestableQueue{}
	serverlessServiceEnqueuer := NewSingleQueueEnqueuer(queue)
	logger := zaptest.NewLogger(t).Sugar()
	eventHandler := NewServerlessServiceControllerEventHandler(serverlessServiceEnqueuer, clusterName, logger)

	oldSls := kube_test.NewKnativeServerlessServiceBuilder("test-sls", "test-ns", "test-rv-1").Build()
	newSls := kube_test.NewKnativeServerlessServiceBuilder("test-sls", "test-ns", "test-rv-2").Build()

	eventHandler.OnUpdate(oldSls, newSls)

	expectedQueueItem := NewQueueItem(
		clusterName,
		"test-ns/test-sls",
		"test-uid",
		controllers_api.EventUpdate)

	assert.Equal(t, 1, len(queue.recordedItems))
	assert.Equal(t, expectedQueueItem, queue.recordedItems[0])
}

func TestServerlessServiceEventHandler_onDelete(t *testing.T) {
	clusterName := "cluster-1"
	queue := &TestableQueue{}
	serverlessServiceEnqueuer := NewSingleQueueEnqueuer(queue)
	logger := zaptest.NewLogger(t).Sugar()
	eventHandler := NewServerlessServiceControllerEventHandler(serverlessServiceEnqueuer, clusterName, logger)

	sls := kube_test.NewKnativeServerlessServiceBuilder("test-sls", "test-ns", "test-rv-1").Build()

	eventHandler.OnDelete(sls)

	expectedQueueItem := NewQueueItem(
		clusterName,
		"test-ns/test-sls",
		"test-uid",
		controllers_api.EventDelete)

	assert.Equal(t, 1, len(queue.recordedItems))
	assert.Equal(t, expectedQueueItem, queue.recordedItems[0])
}

func TestServerlessServiceResourcePresentInCluster(t *testing.T) {
	// Test with a properly configured discovery client that has ServerlessService resources
	fakeDiscoveryClient := &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{}}
	fakeDiscoveryClient.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: schema.GroupVersion{Group: constants.KnativeGroup, Version: constants.KnativeVersion}.String(),
			APIResources: []metav1.APIResource{
				{
					Name: "serverlessservices",
					Kind: constants.KnativeServerlessServiceKind.Kind,
				},
			},
		},
	}

	kubeClient := &kube_test.FakeClient{
		DiscoveryClient: fakeDiscoveryClient,
	}
	testCluster := &Cluster{ID: cluster.ID("cluster-1"), Client: kubeClient}

	result := serverlessServiceResourcePresentInCluster(testCluster)
	assert.True(t, result, "ServerlessService resource should be present in cluster")
}
