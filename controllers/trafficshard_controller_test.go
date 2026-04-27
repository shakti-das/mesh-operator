package controllers

import (
	"errors"
	"testing"
	"time"

	meshv1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/cluster"
	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controller_test"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/resources_test"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating_test"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestTrafficShardController_OnNewClusterAdded(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	tsp := &meshv1alpha1.TrafficShardingPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tsp",
			Namespace: "test-ns",
			UID:       "test-uid",
		},
		Spec: meshv1alpha1.TrafficShardingPolicySpec{
			ServiceSelector: meshv1alpha1.ServiceSelector{
				Labels: map[string]string{"app": "test"},
			},
		},
	}

	kubeClient := kube_test.NewKubeClientBuilder().
		AddMopClientObjects(tsp).
		Build()

	testCluster := &Cluster{
		ID:              cluster.ID("cluster-1"),
		Client:          kubeClient,
		namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{},
	}

	clusterStore := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
	clusterStore.SetPrimaryCluster(testCluster)

	metricsRegistry := prometheus.NewRegistry()
	tspQueue := createWorkQueue("tsp-multicluster")

	controller := &TrafficShardController{
		logger:          logger,
		clusterManager:  clusterStore,
		tspEnqueuer:     NewSingleQueueEnqueuer(tspQueue),
		metricsRegistry: metricsRegistry,
		labelConfig:     TSPLabelConfig{CellTypeLabel: "p_cell_type", ServiceInstanceLabel: "p_service_instance", CellLabel: "p_cell", ServiceNameLabel: "p_servicename"},
	}

	err := controller.OnNewClusterAdded(testCluster)
	assert.Nil(t, err)
}

func TestTrafficShardController_Reconcile(t *testing.T) {
	testCases := []struct {
		name     string
		item     QueueItem
		isErrNil bool
	}{
		{
			name: "successful add",
			item: NewQueueItem(
				"cluster-1",
				"test-ns/test-tsp",
				"test-uid",
				controllers_api.EventAdd),
			isErrNil: true,
		},
		{
			name: "successful update",
			item: NewQueueItem(
				"cluster-1",
				"test-ns/test-tsp",
				"test-uid",
				controllers_api.EventUpdate),
			isErrNil: true,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tsp := &meshv1alpha1.TrafficShardingPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tsp",
					Namespace: "test-ns",
					UID:       types.UID("test-uid"),
				},
				Spec: meshv1alpha1.TrafficShardingPolicySpec{
					ServiceSelector: meshv1alpha1.ServiceSelector{
						Labels: map[string]string{"app": "test"},
					},
				},
			}

			// Build fake client with TSP object
			kubeClient := kube_test.NewKubeClientBuilder().
				AddMopClientObjects(tsp).
				Build()

			// Add TSP to the informer's indexer so it can be found by the lister
			fakeClient := kubeClient.(*kube_test.FakeClient)
			fakeClient.MopInformerFactory().Mesh().V1alpha1().TrafficShardingPolicies().Informer().GetIndexer().Add(tsp)

			testCluster := &Cluster{
				ID:              cluster.ID(tc.item.cluster),
				Client:          kubeClient,
				namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{},
				primary:         true,
			}

			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			clusterManager.SetPrimaryCluster(testCluster)

			tspQueue := createWorkQueue("tsp-multicluster")

			renderer := &templating_test.FixedResultRenderer{
				ResultToReturn: templating.GeneratedConfig{
					Config: map[string][]*unstructured.Unstructured{
						"tsp": {
							{
								Object: map[string]interface{}{
									"apiVersion": "networking.istio.io/v1alpha3",
									"kind":       "EnvoyFilter",
									"metadata": map[string]interface{}{
										"name":      "test-filter",
										"namespace": "test-ns",
									},
								},
							},
						},
					},
					TemplateType: "tsp",
				},
			}

			applicator := &templating_test.TestableApplicator{
				AppliedResults: []templating_test.GeneratedConfigMetadata{},
			}

			metricsRegistry := prometheus.NewRegistry()

			configGenerator := NewConfigGenerator(renderer, []templating.Mutator{}, nil, applicator, templating.DontApplyOverlays, logger, metricsRegistry)

			controller := &TrafficShardController{
				logger:                       logger,
				configGenerator:              configGenerator,
				tspEnqueuer:                  NewSingleQueueEnqueuer(tspQueue),
				clusterManager:               clusterManager,
				resourceManager:              resources_test.NewFakeResourceManager(),
				metricsRegistry:              metricsRegistry,
				multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
				labelConfig:                  TSPLabelConfig{CellTypeLabel: "p_cell_type", ServiceInstanceLabel: "p_service_instance", CellLabel: "p_cell"},
			}

			err := controller.reconcile(tc.item)

			if tc.isErrNil {
				assert.Nil(t, err)
			} else {
				assert.NotNil(t, err)
			}
		})
	}
}

func TestTrafficShardController_ReconcileValidationFailure(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	// TSP with a route targeting cellType "core"
	tsp := &meshv1alpha1.TrafficShardingPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tsp",
			Namespace: "test-ns",
			UID:       types.UID("test-uid"),
		},
		Spec: meshv1alpha1.TrafficShardingPolicySpec{
			Routes: []meshv1alpha1.TSPRoute{
				{Name: "route-core", Destination: meshv1alpha1.TSPRouteDestination{CellType: "core"}},
			},
			ServiceSelector: meshv1alpha1.ServiceSelector{
				Labels: map[string]string{"app": "test"},
			},
		},
	}

	// Service matches the selector but does NOT have p_cell_type=core,
	// so route validation will fail with "destination does not match any service"
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "svc-1",
			Namespace: "test-ns",
			Labels:    map[string]string{"app": "test", "p_cell_type": "sdb"},
		},
	}

	kubeClient := kube_test.NewKubeClientBuilder().
		AddMopClientObjects(tsp).
		AddK8sObjects(svc).
		Build()

	fakeClient := kubeClient.(*kube_test.FakeClient)
	fakeClient.MopInformerFactory().Mesh().V1alpha1().TrafficShardingPolicies().Informer().GetIndexer().Add(tsp)
	fakeClient.KubeInformerFactory().Core().V1().Services().Informer().GetIndexer().Add(svc)

	testCluster := &Cluster{
		ID:              cluster.ID("cluster-1"),
		Client:          kubeClient,
		namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{},
		primary:         true,
	}

	clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
	clusterManager.SetPrimaryCluster(testCluster)

	metricsRegistry := prometheus.NewRegistry()
	tspQueue := createWorkQueue("tsp-multicluster")

	controller := &TrafficShardController{
		logger:                       logger,
		tspEnqueuer:                  NewSingleQueueEnqueuer(tspQueue),
		clusterManager:               clusterManager,
		resourceManager:              resources_test.NewFakeResourceManager(),
		metricsRegistry:              metricsRegistry,
		multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
		labelConfig:                  TSPLabelConfig{CellTypeLabel: "p_cell_type", ServiceInstanceLabel: "p_service_instance", CellLabel: "p_cell", ServiceNameLabel: "p_servicename"},
	}

	item := NewQueueItem("cluster-1", "test-ns/test-tsp", "test-uid", controllers_api.EventAdd)
	err := controller.reconcile(item)

	// UserConfigError is caught at reconcile level and returns nil (no retry)
	assert.Nil(t, err)

	failedLatencyLabels := map[string]string{
		commonmetrics.ClusterLabel:        "cluster-1",
		commonmetrics.ResourceKind:        "TrafficShardingPolicy",
		commonmetrics.ReconcilePhaseLabel: "Failed",
	}
	assertSummarySampleCountWithLabels(t, metricsRegistry, commonmetrics.ReconcileLatencyMetric, failedLatencyLabels, 1)
}

func TestTrafficShardController_ServicesToUnstructured(t *testing.T) {
	tests := []struct {
		name           string
		services       []*corev1.Service
		expectedLength int
	}{
		{
			name: "multiple services",
			services: []*corev1.Service{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-1",
						Namespace: "test-namespace",
						Labels:    map[string]string{"p_servicename": "test-service", "p_cell": "cell-1"},
					},
					Spec: corev1.ServiceSpec{
						Ports: []corev1.ServicePort{{Name: "http", Port: 8080}},
					},
				},
				{
					TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-2",
						Namespace: "test-namespace",
						Labels:    map[string]string{"p_servicename": "test-service", "p_cell": "cell-2"},
					},
					Spec: corev1.ServiceSpec{
						Ports: []corev1.ServicePort{{Name: "http", Port: 8080}},
					},
				},
			},
			expectedLength: 2,
		},
		{
			name:           "empty services",
			services:       []*corev1.Service{},
			expectedLength: 0,
		},
		{
			name: "single service",
			services: []*corev1.Service{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-1",
						Namespace: "test-namespace",
					},
				},
			},
			expectedLength: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &TrafficShardController{
				logger: zaptest.NewLogger(t).Sugar(),
			}

			result := c.servicesToUnstructured(tt.services)

			assert.Len(t, result, tt.expectedLength)

			if tt.expectedLength > 0 {
				metadata := result[0]["metadata"].(map[string]interface{})
				assert.Equal(t, tt.services[0].Name, metadata["name"])
				assert.Equal(t, tt.services[0].Namespace, metadata["namespace"])
			}
		})
	}
}

func TestTrafficShardController_ValidateRoutes(t *testing.T) {
	labelConfig := TSPLabelConfig{
		CellTypeLabel:        "p_cell_type",
		ServiceInstanceLabel: "p_service_instance",
		CellLabel:            "p_cell",
		ServiceNameLabel:     "p_servicename",
	}

	tests := []struct {
		name         string
		tsp          *meshv1alpha1.TrafficShardingPolicy
		services     []*corev1.Service
		expectMsg    string
		expectErr    bool
		expectErrMsg string
	}{
		{
			name: "all routes resolve",
			tsp: &meshv1alpha1.TrafficShardingPolicy{
				Spec: meshv1alpha1.TrafficShardingPolicySpec{
					Routes: []meshv1alpha1.TSPRoute{
						{Name: "route-core", Destination: meshv1alpha1.TSPRouteDestination{CellType: "core"}},
						{Name: "default", Destination: meshv1alpha1.TSPRouteDestination{Cell: "cell-1"}},
					},
					ServiceSelector: meshv1alpha1.ServiceSelector{Labels: map[string]string{"p_servicename": "my-svc"}},
				},
			},
			services: []*corev1.Service{
				{ObjectMeta: metav1.ObjectMeta{Name: "svc-core-1", Labels: map[string]string{"p_cell_type": "core", "p_servicename": "my-svc"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "svc-cell-1", Labels: map[string]string{"p_cell": "cell-1", "p_servicename": "my-svc"}}},
			},
			expectMsg: "Successfully reconciled",
		},
		{
			name: "no services matched - succeeds with info message",
			tsp: &meshv1alpha1.TrafficShardingPolicy{
				Spec: meshv1alpha1.TrafficShardingPolicySpec{
					Routes:          []meshv1alpha1.TSPRoute{{Name: "default", Destination: meshv1alpha1.TSPRouteDestination{Cell: "cell-1"}}},
					ServiceSelector: meshv1alpha1.ServiceSelector{Labels: map[string]string{"p_servicename": "missing"}},
				},
			},
			services:  []*corev1.Service{},
			expectMsg: "No services matched the serviceSelector; ensure target services exist with the correct labels",
		},
		{
			name: "p_servicename matches k8s service name - fails",
			tsp: &meshv1alpha1.TrafficShardingPolicy{
				Spec: meshv1alpha1.TrafficShardingPolicySpec{
					Routes:          []meshv1alpha1.TSPRoute{{Name: "default", Destination: meshv1alpha1.TSPRouteDestination{CellType: "core"}}},
					ServiceSelector: meshv1alpha1.ServiceSelector{Labels: map[string]string{"p_servicename": "my-svc"}},
				},
			},
			services: []*corev1.Service{
				{ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Labels: map[string]string{"p_cell_type": "core", "p_servicename": "my-svc"}}},
			},
			expectErr:    true,
			expectErrMsg: "selecting services by name is not currently supported",
		},
		{
			name: "unresolved route destination - fails",
			tsp: &meshv1alpha1.TrafficShardingPolicy{
				Spec: meshv1alpha1.TrafficShardingPolicySpec{
					Routes:          []meshv1alpha1.TSPRoute{{Name: "route-missing", Destination: meshv1alpha1.TSPRouteDestination{ServiceInstance: "nonexistent"}}},
					ServiceSelector: meshv1alpha1.ServiceSelector{Labels: map[string]string{"p_servicename": "my-svc"}},
				},
			},
			services: []*corev1.Service{
				{ObjectMeta: metav1.ObjectMeta{Name: "svc-abc", Labels: map[string]string{"p_servicename": "my-svc"}}},
			},
			expectErr:    true,
			expectErrMsg: "destination does not match any service",
		},
		{
			name: "service instance matches",
			tsp: &meshv1alpha1.TrafficShardingPolicy{
				Spec: meshv1alpha1.TrafficShardingPolicySpec{
					Routes:          []meshv1alpha1.TSPRoute{{Name: "route-si", Destination: meshv1alpha1.TSPRouteDestination{ServiceInstance: "si-1"}}},
					ServiceSelector: meshv1alpha1.ServiceSelector{Labels: map[string]string{"p_servicename": "my-svc"}},
				},
			},
			services: []*corev1.Service{
				{ObjectMeta: metav1.ObjectMeta{Name: "svc-si-1", Labels: map[string]string{"p_service_instance": "si-1", "p_servicename": "my-svc"}}},
			},
			expectMsg: "Successfully reconciled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &TrafficShardController{
				logger:      zaptest.NewLogger(t).Sugar(),
				labelConfig: labelConfig,
			}
			msg, err := c.validateRoutes(tt.tsp, tt.services)
			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErrMsg)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectMsg, msg)
			}
		})
	}
}

func TestTrafficShardController_ReconcileMetrics(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	tsp := &meshv1alpha1.TrafficShardingPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tsp",
			Namespace: "test-ns",
			UID:       types.UID("test-uid"),
		},
		Spec: meshv1alpha1.TrafficShardingPolicySpec{
			ServiceSelector: meshv1alpha1.ServiceSelector{
				Labels: map[string]string{"app": "test"},
			},
		},
	}

	kubeClient := kube_test.NewKubeClientBuilder().
		AddMopClientObjects(tsp).
		Build()

	fakeClient := kubeClient.(*kube_test.FakeClient)
	fakeClient.MopInformerFactory().Mesh().V1alpha1().TrafficShardingPolicies().Informer().GetIndexer().Add(tsp)

	testCluster := &Cluster{
		ID:              cluster.ID("cluster-1"),
		Client:          kubeClient,
		namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{},
		primary:         true,
	}

	clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
	clusterManager.SetPrimaryCluster(testCluster)

	metricsRegistry := prometheus.NewRegistry()
	tspQueue := createWorkQueue("tsp-multicluster")

	renderer := &templating_test.FixedResultRenderer{
		ResultToReturn: templating.GeneratedConfig{
			Config: map[string][]*unstructured.Unstructured{
				"tsp": {{Object: map[string]interface{}{
					"apiVersion": "networking.istio.io/v1alpha3",
					"kind":       "EnvoyFilter",
					"metadata":   map[string]interface{}{"name": "test-filter", "namespace": "test-ns"},
				}}},
			},
			TemplateType: "tsp",
		},
	}

	applicator := &templating_test.TestableApplicator{AppliedResults: []templating_test.GeneratedConfigMetadata{}}
	configGenerator := NewConfigGenerator(renderer, []templating.Mutator{}, nil, applicator, templating.DontApplyOverlays, logger, metricsRegistry)

	controller := &TrafficShardController{
		logger:                       logger,
		configGenerator:              configGenerator,
		tspEnqueuer:                  NewSingleQueueEnqueuer(tspQueue),
		clusterManager:               clusterManager,
		resourceManager:              resources_test.NewFakeResourceManager(),
		metricsRegistry:              metricsRegistry,
		multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
		labelConfig:                  TSPLabelConfig{CellTypeLabel: "p_cell_type", ServiceInstanceLabel: "p_service_instance", CellLabel: "p_cell"},
	}

	item := NewQueueItem("cluster-1", "test-ns/test-tsp", "test-uid", controllers_api.EventAdd)
	err := controller.reconcile(item)
	assert.Nil(t, err)

	// Verify reconciled total counter was incremented
	reconciledLabels := map[string]string{
		commonmetrics.ClusterLabel:      "cluster-1",
		commonmetrics.NamespaceLabel:    "test-ns",
		commonmetrics.ResourceNameLabel: "deprecated",
		commonmetrics.EventTypeLabel:    "add",
	}
	assertCounterWithLabels(t, metricsRegistry, commonmetrics.TSPReconciledTotal, reconciledLabels, 1)

	// Verify matched services gauge was set (0 services matched since no services in cluster)
	matchedLabels := map[string]string{
		commonmetrics.ClusterLabel:      "cluster-1",
		commonmetrics.NamespaceLabel:    "test-ns",
		commonmetrics.ResourceNameLabel: "test-tsp",
	}
	assertGaugeWithLabels(t, metricsRegistry, commonmetrics.TSPMatchedServicesCount, matchedLabels, 0)

	// Verify latency summary was registered
	latencyLabels := map[string]string{
		commonmetrics.ClusterLabel:        "cluster-1",
		commonmetrics.ResourceKind:        "TrafficShardingPolicy",
		commonmetrics.ReconcilePhaseLabel: "Succeeded",
	}
	assertSummarySampleCountWithLabels(t, metricsRegistry, commonmetrics.ReconcileLatencyMetric, latencyLabels, 1)
}

func TestTrafficShardController_ConfigGenerationFailedMetric(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	metricsRegistry := prometheus.NewRegistry()

	tsp := &meshv1alpha1.TrafficShardingPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tsp",
			Namespace: "test-ns",
			UID:       types.UID("test-uid"),
		},
		Spec: meshv1alpha1.TrafficShardingPolicySpec{
			ServiceSelector: meshv1alpha1.ServiceSelector{
				Labels: map[string]string{"app": "test"},
			},
		},
	}

	kubeClient := kube_test.NewKubeClientBuilder().AddMopClientObjects(tsp).Build()
	fakeClient := kubeClient.(*kube_test.FakeClient)
	fakeClient.MopInformerFactory().Mesh().V1alpha1().TrafficShardingPolicies().Informer().GetIndexer().Add(tsp)

	testCluster := &Cluster{
		ID:              cluster.ID("cluster-1"),
		Client:          kubeClient,
		namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{},
		primary:         true,
	}

	clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
	clusterManager.SetPrimaryCluster(testCluster)
	tspQueue := createWorkQueue("tsp-multicluster")

	renderer := &templating_test.FixedResultRenderer{
		ErrorToThrow: errors.New("template render failed"),
	}
	applicator := &templating_test.TestableApplicator{AppliedResults: []templating_test.GeneratedConfigMetadata{}}
	configGenerator := NewConfigGenerator(renderer, []templating.Mutator{}, nil, applicator, templating.DontApplyOverlays, logger, metricsRegistry)

	controller := &TrafficShardController{
		logger:                       logger,
		configGenerator:              configGenerator,
		tspEnqueuer:                  NewSingleQueueEnqueuer(tspQueue),
		clusterManager:               clusterManager,
		resourceManager:              resources_test.NewFakeResourceManager(),
		metricsRegistry:              metricsRegistry,
		multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
		labelConfig:                  TSPLabelConfig{CellTypeLabel: "p_cell_type", ServiceInstanceLabel: "p_service_instance", CellLabel: "p_cell"},
	}

	item := NewQueueItem("cluster-1", "test-ns/test-tsp", "test-uid", controllers_api.EventAdd)
	err := controller.reconcile(item)
	assert.Error(t, err)

	labels := map[string]string{
		commonmetrics.ClusterLabel:      "cluster-1",
		commonmetrics.NamespaceLabel:    "test-ns",
		commonmetrics.ResourceNameLabel: "test-tsp",
		commonmetrics.EventTypeLabel:    "generate-config",
	}
	assertCounterWithLabels(t, metricsRegistry, commonmetrics.TSPConfigGenerationFailed, labels, 1)
}

func TestTrafficShardController_NonCriticalErrorMetric(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	metricsRegistry := prometheus.NewRegistry()

	tsp := &meshv1alpha1.TrafficShardingPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tsp",
			Namespace: "test-ns",
			UID:       types.UID("test-uid"),
		},
		Spec: meshv1alpha1.TrafficShardingPolicySpec{
			ServiceSelector: meshv1alpha1.ServiceSelector{
				Labels: map[string]string{"app": "test"},
			},
		},
	}

	kubeClient := kube_test.NewKubeClientBuilder().AddMopClientObjects(tsp).Build()
	fakeClient := kubeClient.(*kube_test.FakeClient)
	fakeClient.MopInformerFactory().Mesh().V1alpha1().TrafficShardingPolicies().Informer().GetIndexer().Add(tsp)

	testCluster := &Cluster{
		ID:              cluster.ID("cluster-1"),
		Client:          kubeClient,
		namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{},
		primary:         true,
	}

	clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
	clusterManager.SetPrimaryCluster(testCluster)

	tspQueue := createWorkQueue("tsp-multicluster")
	renderer := &templating_test.FixedResultRenderer{
		ResultToReturn: templating.GeneratedConfig{
			Config:       map[string][]*unstructured.Unstructured{"tsp": {{Object: map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "t", "namespace": "t"}}}}},
			TemplateType: "tsp",
		},
	}
	applicator := &templating_test.TestableApplicator{AppliedResults: []templating_test.GeneratedConfigMetadata{}}
	configGenerator := NewConfigGenerator(renderer, []templating.Mutator{}, nil, applicator, templating.DontApplyOverlays, logger, metricsRegistry)

	controller := &TrafficShardController{
		logger:                       logger,
		configGenerator:              configGenerator,
		tspEnqueuer:                  NewSingleQueueEnqueuer(tspQueue),
		clusterManager:               clusterManager,
		resourceManager:              resources_test.NewFakeResourceManager(),
		metricsRegistry:              metricsRegistry,
		multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
		labelConfig:                  TSPLabelConfig{CellTypeLabel: "p_cell_type", ServiceInstanceLabel: "p_service_instance", CellLabel: "p_cell"},
	}

	// First reconcile succeeds and tracks the key
	item := NewQueueItem("cluster-1", "test-ns/test-tsp", "test-uid", controllers_api.EventAdd)
	_ = controller.reconcile(item)

	// Second concurrent reconcile should get NonCriticalReconcileError and increment metric
	// Simulate by tracking the key manually
	controller.multiClusterReconcileTracker.trackKey("test-ns", "test-tsp")
	err := controller.reconcile(item)
	assert.NotNil(t, err)

	nonCritLabels := map[string]string{
		commonmetrics.ClusterLabel:      "cluster-1",
		commonmetrics.NamespaceLabel:    "test-ns",
		commonmetrics.ResourceNameLabel: "test-tsp",
		commonmetrics.EventTypeLabel:    "concurrent-reconcile",
	}
	assertCounterWithLabels(t, metricsRegistry, commonmetrics.TSPReconcileNonCriticalError, nonCritLabels, 1)
}

func assertSummarySampleCountWithLabels(t *testing.T, registry *prometheus.Registry, metricName string, labels map[string]string, expectedSampleCount uint64) {
	metricFamilies, err := registry.Gather()
	assert.NoError(t, err)

	for _, mf := range metricFamilies {
		if mf.GetName() != metricName {
			continue
		}
		for _, metric := range mf.GetMetric() {
			labelsMatch := true
			matchedLabelsCount := 0
			for _, label := range metric.GetLabel() {
				expectedValue, found := labels[label.GetName()]
				if found {
					if expectedValue != label.GetValue() {
						labelsMatch = false
						break
					}
					matchedLabelsCount++
				}
			}
			if labelsMatch && matchedLabelsCount == len(labels) {
				assert.Equal(t, expectedSampleCount, metric.GetSummary().GetSampleCount())
				return
			}
		}
	}

	assert.Failf(t, "summary metric not found", "metric=%s labels=%v", metricName, labels)
}
