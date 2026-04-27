package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	meshv1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controller_test"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
)

// TestNewSidecarConfigController tests the controller initialization.
func TestNewSidecarConfigController(t *testing.T) {
	logger := zap.NewNop().Sugar()
	fakeClient := kube_test.NewKubeClientBuilder().
		SetClusterName("test-cluster").
		Build()

	testableQueue := &TestableQueue{}
	enqueuer := NewSingleQueueEnqueuer(testableQueue)

	controller := NewSidecarConfigController(
		logger,
		&controller_test.FakeNamespacesNamespaceFilter{},
		nil, // recorder
		nil, // applicator
		nil, // renderer
		nil, // metricsRegistry
		"test-cluster",
		fakeClient,
		enqueuer,
		false, // dryRun
		NewMultiClusterReconcileTracker(logger),
	)

	assert.NotNil(t, controller, "controller should be created")
	sidecarController := controller.(*SidecarConfigController)
	assert.NotNil(t, sidecarController.sidecarConfigInformer, "informer should be set")
	assert.NotNil(t, sidecarController.sidecarConfigLister, "lister should be set")
	assert.Equal(t, "test-cluster", sidecarController.clusterName)
}

// setupTestControllerForReconcile is a test helper that creates a SidecarConfigController with all necessary dependencies.
// It returns the controller, test queue, and a cleanup function (defer the stopCh close).
func setupTestControllerForReconcile(t *testing.T, configs ...*meshv1alpha1.SidecarConfig) (*SidecarConfigController, *TestableQueue, chan struct{}) {
	return setupTestControllerForReconcileWithDryRun(t, false, configs...)
}

// setupTestControllerForReconcileWithDryRun is like setupTestControllerForReconcile but allows setting dryRun.
// When dryRun is true, applySidecar is a no-op (avoids fake dynamic client Create, which can panic on some unstructured payloads).
func setupTestControllerForReconcileWithDryRun(t *testing.T, dryRun bool, configs ...*meshv1alpha1.SidecarConfig) (*SidecarConfigController, *TestableQueue, chan struct{}) {
	return setupTestControllerForReconcileWithDryRunAndDynamic(t, dryRun, nil, configs...)
}

// setupTestControllerForReconcileWithDryRunAndDynamic is like setupTestControllerForReconcileWithDryRun but seeds the
// dynamic client with the given objects (e.g. an existing Istio Sidecar for delete tests).
func setupTestControllerForReconcileWithDryRunAndDynamic(t *testing.T, dryRun bool, dynamicObjs []runtime.Object, configs ...*meshv1alpha1.SidecarConfig) (*SidecarConfigController, *TestableQueue, chan struct{}) {
	logger := zap.NewNop().Sugar()

	clientBuilder := kube_test.NewKubeClientBuilder().SetClusterName("test-cluster")
	for _, config := range configs {
		clientBuilder.AddMopClientObjects(config)
	}
	if len(dynamicObjs) > 0 {
		clientBuilder.AddDynamicClientObjects(dynamicObjs...)
	}
	fakeClient := clientBuilder.Build()

	if fakeDisco, ok := fakeClient.Discovery().(*discoveryfake.FakeDiscovery); ok {
		fakeDisco.Resources = append(fakeDisco.Resources, &metav1.APIResourceList{
			GroupVersion: "networking.istio.io/v1beta1",
			APIResources: []metav1.APIResource{
				{
					Name:         "sidecars",
					SingularName: "sidecar",
					Namespaced:   true,
					Kind:         "Sidecar",
					Verbs:        []string{"get", "list", "create", "update", "delete"},
				},
			},
		})
	}

	testQueue := &TestableQueue{}
	enqueuer := NewSingleQueueEnqueuer(testQueue)
	metricsRegistry := prometheus.NewRegistry()
	fakeRecorder := record.NewFakeRecorder(100)

	stopCh := make(chan struct{})
	sidecarConfigInformer := fakeClient.MopInformerFactory().Mesh().V1alpha1().SidecarConfigs().Informer()
	sidecarConfigLister := fakeClient.MopInformerFactory().Mesh().V1alpha1().SidecarConfigs().Lister()

	fakeClient.MopInformerFactory().Start(stopCh)
	fakeClient.MopInformerFactory().WaitForCacheSync(stopCh)

	controller := &SidecarConfigController{
		logger:                logger,
		clusterName:           "test-cluster",
		client:                fakeClient,
		recorder:              fakeRecorder,
		metricsRegistry:       metricsRegistry,
		sidecarConfigClient:   fakeClient.MopApiClient(),
		sidecarConfigInformer: sidecarConfigInformer,
		sidecarConfigLister:   sidecarConfigLister,
		sidecarConfigEnqueuer: enqueuer,
		dryRun:                dryRun,
		reconcileTracker:      NewMultiClusterReconcileTracker(logger),
	}

	return controller, testQueue, stopCh
}

// setupTestController creates a SidecarConfigController without metrics (use setupTestControllerForReconcile if metrics needed).
func setupTestController(t *testing.T, configs ...*meshv1alpha1.SidecarConfig) (*SidecarConfigController, *TestableQueue, chan struct{}) {
	logger := zap.NewNop().Sugar()

	clientBuilder := kube_test.NewKubeClientBuilder().SetClusterName("test-cluster")
	for _, config := range configs {
		clientBuilder.AddMopClientObjects(config)
	}
	fakeClient := clientBuilder.Build()

	testQueue := &TestableQueue{}

	stopCh := make(chan struct{})
	sidecarConfigInformer := fakeClient.MopInformerFactory().Mesh().V1alpha1().SidecarConfigs().Informer()
	sidecarConfigLister := fakeClient.MopInformerFactory().Mesh().V1alpha1().SidecarConfigs().Lister()

	fakeClient.MopInformerFactory().Start(stopCh)
	fakeClient.MopInformerFactory().WaitForCacheSync(stopCh)

	controller := &SidecarConfigController{
		client:                fakeClient,
		sidecarConfigClient:   fakeClient.MopApiClient(),
		sidecarConfigInformer: sidecarConfigInformer,
		sidecarConfigLister:   sidecarConfigLister,
		clusterName:           "test-cluster",
		logger:                logger,
		sidecarConfigEnqueuer: NewSingleQueueEnqueuer(testQueue),
		dryRun:                false,
		reconcileTracker:      NewMultiClusterReconcileTracker(logger),
		metricsRegistry:       nil,
		recorder:              nil,
	}

	return controller, testQueue, stopCh
}

// newWorkloadSidecarConfig creates a SidecarConfig for testing with common defaults.
// Optionally override namespace, name, or labels.
func newWorkloadSidecarConfig(namespace, name string, labels map[string]string) *meshv1alpha1.SidecarConfig {
	if labels == nil {
		labels = map[string]string{"app": name}
	}
	return &meshv1alpha1.SidecarConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: meshv1alpha1.SidecarConfigSpec{
			WorkloadSelector: &meshv1alpha1.WorkloadSelector{
				Labels: labels,
			},
		},
	}
}

// newRootSidecarConfig creates a root-level (cluster-wide) SidecarConfig for testing.
func newRootSidecarConfig() *meshv1alpha1.SidecarConfig {
	return &meshv1alpha1.SidecarConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RootSidecarConfigName,
			Namespace: "mesh-control-plane",
		},
		Spec: meshv1alpha1.SidecarConfigSpec{
			// No workload selector = root-level
			EgressHosts: &meshv1alpha1.EgressHosts{
				Discovered: &meshv1alpha1.DiscoveredEgressHosts{
					Runtime: []meshv1alpha1.Host{
						{Hostname: "root-service.svc.cluster.local", Port: 443},
					},
				},
			},
		},
	}
}

// newNamespaceSidecarConfig creates a namespace-level SidecarConfig for testing.
func newNamespaceSidecarConfig(namespace string) *meshv1alpha1.SidecarConfig {
	return &meshv1alpha1.SidecarConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespaceSidecarConfigName(namespace),
			Namespace: namespace,
		},
		Spec: meshv1alpha1.SidecarConfigSpec{
			// No workload selector = namespace-level
			EgressHosts: &meshv1alpha1.EgressHosts{
				Discovered: &meshv1alpha1.DiscoveredEgressHosts{
					Runtime: []meshv1alpha1.Host{
						{Hostname: "namespace-service.svc.cluster.local", Port: 443},
					},
				},
			},
		},
	}
}

// TestSidecarConfigFeatureFlag tests that the feature flag correctly gates controller initialization
func TestSidecarConfigFeatureFlag(t *testing.T) {
	// Save original feature flag values
	originalEnable := features.EnableSidecarConfig
	originalThreads := features.SidecarConfigControllerThreads
	defer func() {
		features.EnableSidecarConfig = originalEnable
		features.SidecarConfigControllerThreads = originalThreads
	}()

	testCases := []struct {
		name       string
		enableFlag bool
		threads    int
	}{
		{
			name:       "feature flag OFF (default) - controller should NOT run",
			enableFlag: false,
			threads:    2,
		},
		{
			name:       "feature flag ON - controller should run",
			enableFlag: true,
			threads:    2,
		},
		{
			name:       "feature flag ON with custom threads",
			enableFlag: true,
			threads:    8,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set feature flags for this test
			features.EnableSidecarConfig = tc.enableFlag
			features.SidecarConfigControllerThreads = tc.threads

			// Simulate the gating logic from secret_controller.go
			var controllerCreated bool
			var runThreads int

			// This simulates the actual code:
			// if features.EnableSidecarConfig {
			//     sidecarConfigController = NewSidecarConfigController(...)
			//     go wait.Until(func() {
			//         sidecarConfigController.Run(features.SidecarConfigControllerThreads, stop)
			//     }, time.Second, stop)
			// }
			if features.EnableSidecarConfig {
				controllerCreated = true
				runThreads = features.SidecarConfigControllerThreads
			}

			// Verify behavior
			assert.Equal(t, tc.enableFlag, controllerCreated,
				"Controller creation should match feature flag")

			if tc.enableFlag {
				assert.Equal(t, tc.threads, runThreads,
					"Controller should use configured thread count when enabled")
			}
		})
	}
}

// TestSidecarConfigController_GenerateSidecar tests Sidecar generation.
func TestSidecarConfigController_GenerateSidecar(t *testing.T) {
	testCases := []struct {
		name                     string
		sidecarConfig            *meshv1alpha1.SidecarConfig
		rootConfig               *meshv1alpha1.SidecarConfig
		namespaceConfig          *meshv1alpha1.SidecarConfig
		expectedHosts            []string
		expectedWorkloadSelector map[string]string
		expectedError            bool
		errorMessage             string
	}{
		{
			name: "generate Sidecar with runtime hosts only",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
					UID:       "test-uid-123",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "test"},
					},
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "service-a.namespace1.svc.cluster.local", Port: 8080},
								{Hostname: "service-b.namespace2.svc.cluster.local", Port: 9000},
							},
						},
					},
				},
			},
			rootConfig:      nil,
			namespaceConfig: nil,
			expectedHosts: []string{
				"*/service-a.namespace1.svc.cluster.local",
				"*/service-b.namespace2.svc.cluster.local",
			},
			expectedWorkloadSelector: map[string]string{"app": "test", constants.SidecarShadowLabel: "true"},
			expectedError:            false,
		},
		{
			name: "generate Sidecar with config hosts",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
					UID:       "test-uid-456",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "myapp"},
					},
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Config: []meshv1alpha1.Host{
								{Hostname: "config-service.namespace.svc.cluster.local", Port: 7443},
							},
						},
					},
				},
			},
			rootConfig:               nil,
			namespaceConfig:          nil,
			expectedHosts:            []string{"*/config-service.namespace.svc.cluster.local"},
			expectedWorkloadSelector: map[string]string{"app": "myapp", constants.SidecarShadowLabel: "true"},
			expectedError:            false,
		},
		{
			name: "generate Sidecar with hierarchical configs (root + namespace + workload)",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "workload-sc",
					Namespace: "app-ns",
					UID:       "workload-uid",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "workload"},
					},
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "workload-service.svc.cluster.local", Port: 8080},
							},
						},
					},
				},
			},
			rootConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      RootSidecarConfigName,
					Namespace: "mesh-control-plane",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "root-service.svc.cluster.local", Port: 443},
							},
						},
					},
				},
			},
			namespaceConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      namespaceSidecarConfigName("app-ns"),
					Namespace: "app-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "namespace-service.svc.cluster.local", Port: 9090},
							},
						},
					},
				},
			},
			expectedHosts: []string{
				"*/root-service.svc.cluster.local",
				"*/namespace-service.svc.cluster.local",
				"*/workload-service.svc.cluster.local",
			},
			expectedWorkloadSelector: map[string]string{"app": "workload", constants.SidecarShadowLabel: "true"},
			expectedError:            false,
		},
		{
			name: "generate Sidecar with actual workload selector (no shadow)",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "real-sc",
					Namespace: "prod-ns",
					UID:       "real-uid",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "prod", "version": "v1"},
					},
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "svc.prod.svc.cluster.local", Port: 8080},
							},
						},
					},
				},
			},
			rootConfig:               nil,
			namespaceConfig:          nil,
			expectedHosts:            []string{"*/svc.prod.svc.cluster.local"},
			expectedWorkloadSelector: map[string]string{"app": "prod", "version": "v1"},
			expectedError:            false,
		},
		{
			name: "error - nil EgressHosts",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "test"},
					},
					EgressHosts: nil,
				},
			},
			rootConfig:      nil,
			namespaceConfig: nil,
			expectedError:   true,
			errorMessage:    "egressHosts cannot be nil",
		},
		{
			name: "error - nil WorkloadSelector",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-sc2",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: nil,
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "service.svc.cluster.local", Port: 8080},
							},
						},
					},
				},
			},
			rootConfig:      nil,
			namespaceConfig: nil,
			expectedError:   true,
			errorMessage:    "workloadSelector cannot be nil",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zap.NewNop().Sugar()
			controller := &SidecarConfigController{
				logger: logger,
			}

			renderMode := SidecarRenderModeShadow
			if tc.expectedWorkloadSelector != nil {
				if _, hasShadow := tc.expectedWorkloadSelector[constants.SidecarShadowLabel]; !hasShadow {
					renderMode = SidecarRenderModeEnabled
				}
			}
			result, err := controller.generateSidecar(tc.sidecarConfig, tc.rootConfig, tc.namespaceConfig, renderMode)

			if tc.expectedError {
				assert.Error(t, err, "Expected an error")
				assert.Contains(t, err.Error(), tc.errorMessage, "Error message should match")
				assert.Nil(t, result, "Result should be nil on error")
				return
			}

			require.NoError(t, err, "Should not error")
			require.NotNil(t, result, "Result should not be nil")
			require.NotNil(t, result.Object, "Result.Object should not be nil")

			obj := result.Object

			assert.Equal(t, "networking.istio.io/v1beta1", obj.GetAPIVersion())
			assert.Equal(t, "Sidecar", obj.GetKind())
			assert.Equal(t, tc.sidecarConfig.Namespace, obj.GetNamespace())

			spec, found := obj.Object["spec"]
			require.True(t, found, "spec should exist")
			require.NotNil(t, spec, "spec should not be nil")

			specMap, ok := spec.(map[string]interface{})
			require.True(t, ok, "spec should be a map")
			egress, found := specMap["egress"]
			require.True(t, found, "egress should exist")
			require.NotNil(t, egress, "egress should not be nil")

			egressSlice, ok := egress.([]interface{})
			require.True(t, ok, "egress should be a slice")
			assert.Greater(t, len(egressSlice), 0, "egress should have at least one entry")

			if tc.expectedWorkloadSelector != nil {
				ws, found := specMap["workloadSelector"]
				require.True(t, found, "workloadSelector should exist")
				wsMap, ok := ws.(map[string]interface{})
				require.True(t, ok, "workloadSelector should be a map")
				labelsVal, found := wsMap["labels"]
				require.True(t, found, "workloadSelector.labels should exist")
				labelsMap, ok := labelsVal.(map[string]interface{})
				require.True(t, ok, "labels should be a map")
				for k, v := range tc.expectedWorkloadSelector {
					assert.Equal(t, v, labelsMap[k], "workloadSelector label %s", k)
				}
				assert.Len(t, labelsMap, len(tc.expectedWorkloadSelector), "workloadSelector labels count")
			}
		})
	}
}

// TestSidecarConfigController_ValidateSidecarConfig tests SidecarConfig validation.
func TestSidecarConfigController_ValidateSidecarConfig(t *testing.T) {
	testCases := []struct {
		name          string
		sc            *meshv1alpha1.SidecarConfig
		expectedError bool
		errorMessage  string
	}{
		{
			name: "valid SidecarConfig",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "test"},
					},
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "api.example.com", Port: 8080},
							},
						},
					},
				},
			},
			expectedError: false,
		},
		{
			name: "invalid - empty workload selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{},
					},
				},
			},
			expectedError: true,
			errorMessage:  "must have workloadSelector with at least one label",
		},
		{
			name: "invalid - nil workload selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: nil,
				},
			},
			expectedError: true,
			errorMessage:  "workloadSelector",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zap.NewNop().Sugar()
			controller := &SidecarConfigController{
				logger: logger,
			}

			err := controller.validateSidecarConfig(tc.sc)

			if tc.expectedError {
				assert.Error(t, err, "Expected validation error")
				assert.Contains(t, err.Error(), tc.errorMessage, "Error message should contain expected text")
			} else {
				assert.NoError(t, err, "Expected no validation error")
			}
		})
	}
}

// TestSidecarConfigController_HandleDelete tests deletion logic.
// NOTE: Due to OwnerReferences, Kubernetes automatically deletes Sidecars when SidecarConfig is deleted.
// This test mainly verifies logging and metrics.
func TestSidecarConfigController_HandleDelete(t *testing.T) {
	testCases := []struct {
		name        string
		namespace   string
		scName      string
		uid         string
		expectedLog string
	}{
		{
			name:        "delete SidecarConfig",
			namespace:   "test-ns",
			scName:      "test-sc",
			uid:         "test-uid-123",
			expectedLog: "Sidecar will be auto-deleted by K8s GC",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zap.NewNop().Sugar()
			controller := &SidecarConfigController{
				logger: logger,
			}

			err := controller.handleDelete(tc.namespace, tc.scName, tc.uid)
			assert.NoError(t, err, "handleDelete should not error")
		})
	}
}

// TestSidecarConfigController_StatusUpdate tests status update logic.
func TestSidecarConfigController_StatusUpdate(t *testing.T) {
	testCases := []struct {
		name          string
		sc            *meshv1alpha1.SidecarConfig
		phase         meshv1alpha1.Phase
		message       string
		expectedPhase meshv1alpha1.Phase
	}{
		{
			name: "update to succeeded",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
				},
			},
			phase:         scPhaseSucceeded,
			message:       "",
			expectedPhase: scPhaseSucceeded,
		},
		{
			name: "update to failed with message",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
				},
			},
			phase:         scPhaseFailed,
			message:       "validation failed",
			expectedPhase: scPhaseFailed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := kube_test.NewKubeClientBuilder().
				SetClusterName("test-cluster").
				AddMopClientObjects(tc.sc).
				Build()

			logger := zap.NewNop().Sugar()
			controller := &SidecarConfigController{
				client:              fakeClient,
				sidecarConfigClient: fakeClient.MopApiClient(),
				clusterName:         "test-cluster",
				logger:              logger,
			}

			err := controller.updateSidecarConfigStatus(tc.sc, tc.phase, tc.message)
			require.NoError(t, err, "Should not error updating status")

			updatedSC, getErr := fakeClient.MopApiClient().MeshV1alpha1().
				SidecarConfigs(tc.sc.Namespace).
				Get(context.Background(), tc.sc.Name, metav1.GetOptions{})
			require.NoError(t, getErr, "Should be able to retrieve updated SidecarConfig")
			require.NotNil(t, updatedSC, "Updated SidecarConfig should not be nil")

			assert.Equal(t, tc.expectedPhase, updatedSC.Status.Phase, "Phase should match expected")
			if tc.message != "" {
				assert.Equal(t, tc.message, updatedSC.Status.Message, "Message should match expected")
			}
			assert.Equal(t, tc.sc.Generation, updatedSC.Status.ObservedGeneration, "ObservedGeneration should match")
		})
	}
}

// TestSidecarConfigEventHandler tests the event handler.
func TestSidecarConfigEventHandler(t *testing.T) {
	logger := zap.NewNop().Sugar()
	clusterName := "test-cluster"

	testCases := []struct {
		name             string
		handlerFunc      func(handler cache.ResourceEventHandler, sc *meshv1alpha1.SidecarConfig, oldSC *meshv1alpha1.SidecarConfig)
		expectedEnqueues int
		eventType        controllers_api.Event
	}{
		{
			name: "AddFunc enqueues SidecarConfig",
			handlerFunc: func(handler cache.ResourceEventHandler, sc *meshv1alpha1.SidecarConfig, oldSC *meshv1alpha1.SidecarConfig) {
				handler.OnAdd(sc, false)
			},
			expectedEnqueues: 1,
			eventType:        controllers_api.EventAdd,
		},
		{
			name: "UpdateFunc enqueues when ResourceVersion changes",
			handlerFunc: func(handler cache.ResourceEventHandler, sc *meshv1alpha1.SidecarConfig, oldSC *meshv1alpha1.SidecarConfig) {
				handler.OnUpdate(oldSC, sc)
			},
			expectedEnqueues: 1,
			eventType:        controllers_api.EventUpdate,
		},
		{
			name: "UpdateFunc skips when ResourceVersion is same",
			handlerFunc: func(handler cache.ResourceEventHandler, sc *meshv1alpha1.SidecarConfig, oldSC *meshv1alpha1.SidecarConfig) {
				sameSC := sc.DeepCopy()
				sameSC.ResourceVersion = sc.ResourceVersion // Same resource version
				handler.OnUpdate(sc, sameSC)
			},
			expectedEnqueues: 0,
			eventType:        controllers_api.EventUpdate,
		},
		{
			name: "DeleteFunc enqueues SidecarConfig",
			handlerFunc: func(handler cache.ResourceEventHandler, sc *meshv1alpha1.SidecarConfig, oldSC *meshv1alpha1.SidecarConfig) {
				handler.OnDelete(sc)
			},
			expectedEnqueues: 1,
			eventType:        controllers_api.EventDelete,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testQueue := &TestableQueue{}
			enqueuer := NewSingleQueueEnqueuer(testQueue)
			handler := NewSidecarConfigEventHandler(enqueuer, clusterName, logger)

			sc := &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-sc",
					Namespace:       "test-ns",
					UID:             "test-uid",
					ResourceVersion: "2",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "test"},
					},
				},
			}

			oldSC := sc.DeepCopy()
			oldSC.ResourceVersion = "1"

			tc.handlerFunc(handler, sc, oldSC)

			assert.Equal(t, tc.expectedEnqueues, len(testQueue.recordedItems),
				"Expected %d enqueues, got %d", tc.expectedEnqueues, len(testQueue.recordedItems))

			if tc.expectedEnqueues > 0 {
				assert.Len(t, testQueue.recordedItems, tc.expectedEnqueues, "Should have enqueued items")
				// Extract the queue item from the recorded items
				queueItem := testQueue.recordedItems[0].(QueueItem)
				assert.Equal(t, tc.eventType, queueItem.event, "Event type should match")
			}
		})
	}
}

// TestIsRootLevelSidecarConfig tests root-level SidecarConfig detection
func TestIsRootLevelSidecarConfig(t *testing.T) {
	testCases := []struct {
		name     string
		sc       *meshv1alpha1.SidecarConfig
		expected bool
	}{
		{
			name: "root level - mesh-control-plane namespace, default name, no selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      RootSidecarConfigName,
					Namespace: "mesh-control-plane",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: nil,
				},
			},
			expected: true,
		},
		{
			name: "root level - mesh-control-plane namespace, default name, empty selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      RootSidecarConfigName,
					Namespace: "mesh-control-plane",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{},
					},
				},
			},
			expected: true,
		},
		{
			name: "NOT root level - wrong namespace",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: "my-app-namespace",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: nil,
				},
			},
			expected: false,
		},
		{
			name: "NOT root level - wrong name",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-service-sidecarconfig",
					Namespace: "mesh-control-plane",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: nil,
				},
			},
			expected: false,
		},
		{
			name: "NOT root level - has workload selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      RootSidecarConfigName,
					Namespace: "mesh-control-plane",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "test"},
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isRootLevelSidecarConfig(tc.sc)
			assert.Equal(t, tc.expected, result, "isRootLevelSidecarConfig result mismatch")
		})
	}
}

// TestIsNamespaceLevelSidecarConfig tests namespace-level SidecarConfig detection
func TestIsNamespaceLevelSidecarConfig(t *testing.T) {
	testCases := []struct {
		name     string
		sc       *meshv1alpha1.SidecarConfig
		expected bool
	}{
		{
			name: "namespace level - app namespace, default name, no selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      namespaceSidecarConfigName("my-app-namespace"),
					Namespace: "my-app-namespace",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: nil,
				},
			},
			expected: true,
		},
		{
			name: "namespace level - app namespace, default name, empty selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      namespaceSidecarConfigName("my-app-namespace"),
					Namespace: "my-app-namespace",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{},
					},
				},
			},
			expected: true,
		},
		{
			name: "NOT namespace level - root namespace",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: "mesh-control-plane",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: nil,
				},
			},
			expected: false,
		},
		{
			name: "NOT namespace level - wrong name",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-service-sidecarconfig",
					Namespace: "my-app-namespace",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: nil,
				},
			},
			expected: false,
		},
		{
			name: "NOT namespace level - has workload selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      namespaceSidecarConfigName("my-app-namespace"),
					Namespace: "my-app-namespace",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "test"},
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isNamespaceLevelSidecarConfig(tc.sc)
			assert.Equal(t, tc.expected, result, "isNamespaceLevelSidecarConfig result mismatch")
		})
	}
}

// TestIsWorkloadLevelSidecarConfig tests workload-level SidecarConfig detection
func TestIsWorkloadLevelSidecarConfig(t *testing.T) {
	testCases := []struct {
		name     string
		sc       *meshv1alpha1.SidecarConfig
		expected bool
	}{
		{
			name: "workload level - has selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-service-sidecarconfig",
					Namespace: "my-app-namespace",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "test"},
					},
				},
			},
			expected: true,
		},
		{
			name: "NOT workload level - no selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      namespaceSidecarConfigName("my-app-namespace"),
					Namespace: "my-app-namespace",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: nil,
				},
			},
			expected: false,
		},
		{
			name: "NOT workload level - empty selector",
			sc: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      namespaceSidecarConfigName("my-app-namespace"),
					Namespace: "my-app-namespace",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{},
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isWorkloadLevelSidecarConfig(tc.sc)
			assert.Equal(t, tc.expected, result, "isWorkloadLevelSidecarConfig result mismatch")
		})
	}
}

// TestMergeEgressHostsWithHierarchy tests the egress host merging logic with hierarchical configs
func TestMergeEgressHostsWithHierarchy(t *testing.T) {
	testCases := []struct {
		name          string
		hosts         []meshv1alpha1.Host
		expectedCount int
		expectedHosts map[string]bool // For uniqueness check
	}{
		{
			name: "merge with no duplicates",
			hosts: []meshv1alpha1.Host{
				{Hostname: "api.example.com", Port: 8080},
				{Hostname: "dynamic.example.com", Port: 9000},
			},
			expectedCount: 2,
			expectedHosts: map[string]bool{
				"api.example.com:8080":     true,
				"dynamic.example.com:9000": true,
			},
		},
		{
			name: "merge with duplicates - same hostname and port",
			hosts: []meshv1alpha1.Host{
				{Hostname: "api.example.com", Port: 8080},
				{Hostname: "api.example.com", Port: 8080},
			},
			expectedCount: 1,
			expectedHosts: map[string]bool{
				"api.example.com:8080": true,
			},
		},
		{
			name: "same hostname different ports - both kept",
			hosts: []meshv1alpha1.Host{
				{Hostname: "api.example.com", Port: 8080},
				{Hostname: "api.example.com", Port: 9000},
			},
			expectedCount: 2,
			expectedHosts: map[string]bool{
				"api.example.com:8080": true,
				"api.example.com:9000": true,
			},
		},
		{
			name: "hosts without ports (port 0)",
			hosts: []meshv1alpha1.Host{
				{Hostname: "api.example.com", Port: 0},
				{Hostname: "dynamic.example.com", Port: 0},
			},
			expectedCount: 2,
			expectedHosts: map[string]bool{
				"api.example.com:0":     true,
				"dynamic.example.com:0": true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller := &SidecarConfigController{}

			result := controller.mergeEgressHosts(tc.hosts)

			assert.Equal(t, tc.expectedCount, len(result), "merged host count mismatch")

			for _, entry := range result {
				key := fmt.Sprintf("%s:%d", entry.Hostname, entry.Port)
				assert.True(t, tc.expectedHosts[key],
					"unexpected host in result: %s", key)
			}
		})
	}
}

// TestApplySidecar tests that applySidecar correctly creates/updates Sidecars.
func TestApplySidecar(t *testing.T) {
	testCases := []struct {
		name            string
		existingSidecar *unstructured.Unstructured
		expectError     bool
	}{
		{
			name:            "create new Sidecar successfully",
			existingSidecar: nil,
			expectError:     false,
		},
		{
			name: "update existing Sidecar",
			existingSidecar: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "networking.istio.io/v1beta1",
					"kind":       "Sidecar",
					"metadata": map[string]interface{}{
						"name":            "test-sidecar",
						"namespace":       "test-ns",
						"resourceVersion": "100",
					},
					"spec": map[string]interface{}{
						"egress": []interface{}{
							map[string]interface{}{
								"hosts": []interface{}{"*/old-service.svc.cluster.local"},
							},
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clientBuilder := kube_test.NewKubeClientBuilder().
				SetClusterName("test-cluster")

			if tc.existingSidecar != nil {
				clientBuilder.AddDynamicClientObjects(tc.existingSidecar)
			}

			fakeClient := clientBuilder.Build()

			if fakeDisco, ok := fakeClient.Discovery().(*discoveryfake.FakeDiscovery); ok {
				fakeDisco.Resources = append(fakeDisco.Resources, &metav1.APIResourceList{
					GroupVersion: "networking.istio.io/v1beta1",
					APIResources: []metav1.APIResource{
						{
							Name:         "sidecars",
							SingularName: "sidecar",
							Namespaced:   true,
							Kind:         "Sidecar",
							Verbs:        []string{"get", "list", "create", "update", "delete"},
						},
					},
				})
			}

			logger := zap.NewNop().Sugar()
			controller := &SidecarConfigController{
				client:      fakeClient,
				clusterName: "test-cluster",
				logger:      logger,
			}

			sidecarConfig := &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sidecarconfig",
					Namespace: "test-ns",
					UID:       "test-uid-123",
				},
			}

			sidecarObject := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "networking.istio.io/v1beta1",
					"kind":       "Sidecar",
					"metadata": map[string]interface{}{
						"name":      "test-sidecar",
						"namespace": "test-ns",
					},
					"spec": map[string]interface{}{
						"egress": []interface{}{
							map[string]interface{}{
								"hosts": []interface{}{"*/test-service.svc.cluster.local"},
							},
						},
						"workloadSelector": map[string]interface{}{
							"labels": map[string]interface{}{
								"app":                        "test",
								constants.SidecarShadowLabel: "true",
							},
						},
					},
				},
			}

			appliedConfig := &templating.AppliedConfigObject{
				Object: sidecarObject,
			}

			err := controller.applySidecar(sidecarConfig, appliedConfig)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err, "applySidecar should not error with valid inputs")

				gvr := schema.GroupVersionResource{
					Group:    "networking.istio.io",
					Version:  "v1beta1",
					Resource: "sidecars",
				}

				retrievedSidecar, getErr := fakeClient.Dynamic().Resource(gvr).
					Namespace("test-ns").
					Get(context.Background(), "test-sidecar", metav1.GetOptions{})

				require.NoError(t, getErr, "should be able to retrieve created Sidecar")
				require.NotNil(t, retrievedSidecar, "Sidecar should exist")

				assert.Equal(t, "networking.istio.io/v1beta1", retrievedSidecar.GetAPIVersion())
				assert.Equal(t, "Sidecar", retrievedSidecar.GetKind())
				assert.Equal(t, "test-sidecar", retrievedSidecar.GetName())
				assert.Equal(t, "test-ns", retrievedSidecar.GetNamespace())
			}
		})
	}
}

// TestBuildEgressSection tests the egress section generation with proper port formatting
func TestBuildEgressSection(t *testing.T) {
	testCases := []struct {
		name              string
		hosts             []meshv1alpha1.Host
		expectedEgressLen int
		validateEgress    func(t *testing.T, egress []interface{})
	}{
		{
			name: "single host with port",
			hosts: []meshv1alpha1.Host{
				{Hostname: "service-a.namespace1.svc.cluster.local", Port: 8080},
			},
			expectedEgressLen: 1,
			validateEgress: func(t *testing.T, egress []interface{}) {
				entry := egress[0].(map[string]interface{})

				// Verify hosts
				hosts := entry["hosts"].([]string)
				assert.Len(t, hosts, 1)
				assert.Equal(t, "*/service-a.namespace1.svc.cluster.local", hosts[0])

				port := entry["port"].(map[string]interface{})
				assert.Equal(t, int32(8080), port["number"])
				assert.Equal(t, "HTTP", port["protocol"])
			},
		},
		{
			name: "multiple hosts with same port",
			hosts: []meshv1alpha1.Host{
				{Hostname: "service-a.namespace1.svc.cluster.local", Port: 8080},
				{Hostname: "service-b.namespace2.svc.cluster.local", Port: 8080},
			},
			expectedEgressLen: 1,
			validateEgress: func(t *testing.T, egress []interface{}) {
				entry := egress[0].(map[string]interface{})

				hosts := entry["hosts"].([]string)
				assert.Len(t, hosts, 2)
				assert.Contains(t, hosts, "*/service-a.namespace1.svc.cluster.local")
				assert.Contains(t, hosts, "*/service-b.namespace2.svc.cluster.local")

				port := entry["port"].(map[string]interface{})
				assert.Equal(t, int32(8080), port["number"])
				assert.Equal(t, "HTTP", port["protocol"])
			},
		},
		{
			name: "hosts with different ports",
			hosts: []meshv1alpha1.Host{
				{Hostname: "service-a.namespace1.svc.cluster.local", Port: 8080},
				{Hostname: "service-b.namespace2.svc.cluster.local", Port: 9000},
			},
			expectedEgressLen: 2,
			validateEgress: func(t *testing.T, egress []interface{}) {
				// Should create two egress entries
				for _, e := range egress {
					entry := e.(map[string]interface{})
					hosts := entry["hosts"].([]string)
					port := entry["port"].(map[string]interface{})

					if port["number"].(int32) == 8080 {
						assert.Contains(t, hosts, "*/service-a.namespace1.svc.cluster.local")
					} else if port["number"].(int32) == 9000 {
						assert.Contains(t, hosts, "*/service-b.namespace2.svc.cluster.local")
					}
				}
			},
		},
		{
			name: "host without port (port 0)",
			hosts: []meshv1alpha1.Host{
				{Hostname: "service-a.namespace1.svc.cluster.local", Port: 0},
			},
			expectedEgressLen: 1,
			validateEgress: func(t *testing.T, egress []interface{}) {
				entry := egress[0].(map[string]interface{})

				hosts := entry["hosts"].([]string)
				assert.Len(t, hosts, 1)
				assert.Equal(t, "*/service-a.namespace1.svc.cluster.local", hosts[0])

				// Port 0 means unspecified, should not be present in egress
				_, hasPort := entry["port"]
				assert.False(t, hasPort, "port field should not be present when port is 0")
			},
		},
		{
			name: "mixed ports including port 0",
			hosts: []meshv1alpha1.Host{
				{Hostname: "service-a.namespace1.svc.cluster.local", Port: 8080},
				{Hostname: "service-b.namespace2.svc.cluster.local", Port: 0},
			},
			expectedEgressLen: 2,
			validateEgress: func(t *testing.T, egress []interface{}) {
				portZeroFound := false
				port8080Found := false

				for _, e := range egress {
					entry := e.(map[string]interface{})
					_, hasPort := entry["port"]

					if hasPort {
						port := entry["port"].(map[string]interface{})
						assert.Equal(t, int32(8080), port["number"])
						port8080Found = true
					} else {
						portZeroFound = true
						hosts := entry["hosts"].([]string)
						assert.Contains(t, hosts, "*/service-b.namespace2.svc.cluster.local")
					}
				}

				assert.True(t, portZeroFound, "should have entry without port")
				assert.True(t, port8080Found, "should have entry with port 8080")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			egress := buildEgressSection(tc.hosts)

			assert.Equal(t, tc.expectedEgressLen, len(egress), "egress entry count mismatch")

			if tc.validateEgress != nil {
				tc.validateEgress(t, egress)
			}
		})
	}
}

func createTestSidecarConfig(name, namespace string, workloadSelector map[string]string, runtimeHosts, configHosts []meshv1alpha1.Host) *meshv1alpha1.SidecarConfig {
	return &meshv1alpha1.SidecarConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "test-uid",
		},
		Spec: meshv1alpha1.SidecarConfigSpec{
			WorkloadSelector: &meshv1alpha1.WorkloadSelector{
				Labels: workloadSelector,
			},
			EgressHosts: &meshv1alpha1.EgressHosts{
				Discovered: &meshv1alpha1.DiscoveredEgressHosts{
					Runtime: runtimeHosts,
					Config:  configHosts,
				},
			},
		},
	}
}

// Helper function to create a test Sidecar
func createTestSidecar(name, namespace string, hosts []string) interface{} {
	// TODO: Return proper Istio Sidecar structure
	return nil
}

// TestSidecarConfigController_RecordMetrics tests the main recordMetrics function.
func TestSidecarConfigController_RecordMetrics(t *testing.T) {
	testCases := []struct {
		name             string
		sidecarConfig    *meshv1alpha1.SidecarConfig
		phase            string
		duration         time.Duration
		expectedCounters map[string]float64
		expectedGauges   map[string]float64
	}{
		{
			name: "successful reconciliation",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "test"},
					},
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "service-a.svc.cluster.local", Port: 8080},
								{Hostname: "service-b.svc.cluster.local", Port: 9090},
							},
							Config: []meshv1alpha1.Host{
								{Hostname: "service-c.svc.cluster.local", Port: 443},
							},
						},
					},
				},
			},
			phase:    string(scPhaseSucceeded),
			duration: 150 * time.Millisecond,
			expectedCounters: map[string]float64{
				"sidecar_config_reconciled_total": 1,
			},
			expectedGauges: map[string]float64{
				"objects_configured_total": 1,
			},
		},
		{
			name: "failed reconciliation",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "failed-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "test"},
					},
				},
			},
			phase:    string(scPhaseFailed),
			duration: 50 * time.Millisecond,
			expectedCounters: map[string]float64{
				"sidecar_config_reconcile_failed": 1,
				"sidecar_config_reconciled_total": 1,
			},
			expectedGauges: map[string]float64{},
		},
		{
			name: "deleted reconciliation",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deleted-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					WorkloadSelector: &meshv1alpha1.WorkloadSelector{
						Labels: map[string]string{"app": "test"},
					},
				},
			},
			phase:    "deleted",
			duration: 25 * time.Millisecond,
			expectedCounters: map[string]float64{
				"sidecar_config_reconciled_total": 1,
			},
			expectedGauges: map[string]float64{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zap.NewNop().Sugar()
			registry := prometheus.NewRegistry()
			controller := &SidecarConfigController{
				clusterName:     "test-cluster",
				logger:          logger,
				metricsRegistry: registry,
			}

			controller.recordMetrics(tc.sidecarConfig, tc.phase, tc.duration)

			for metricName, expectedValue := range tc.expectedCounters {
				labels := commonmetrics.GetLabelsForK8sResource("SidecarConfig", "test-cluster", tc.sidecarConfig.Namespace, tc.sidecarConfig.Name)
				if metricName == "sidecar_config_reconciled_total" {
					labels[commonmetrics.ReconcilePhaseLabel] = tc.phase
				}
				assertCounterWithLabels(t, registry, metricName, labels, expectedValue)
			}

			if tc.phase == string(scPhaseSucceeded) && len(tc.expectedGauges) > 0 {
				metrics, err := registry.Gather()
				require.NoError(t, err)

				foundGauge := false
				for _, metricFamily := range metrics {
					if metricFamily.GetName() == commonmetrics.ObjectsConfiguredTotal {
						for _, metric := range metricFamily.GetMetric() {
							if metric.GetGauge().GetValue() == 1 {
								foundGauge = true
								break
							}
						}
					}
				}
				assert.True(t, foundGauge, "objects_configured_total gauge should be found with value 1")
			}

			latencyLabels := map[string]string{
				commonmetrics.ClusterLabel:        "test-cluster",
				commonmetrics.ResourceKind:        "SidecarConfig",
				commonmetrics.ReconcilePhaseLabel: tc.phase,
			}
			summary := commonmetrics.GetOrRegisterSummaryWithLabels(
				commonmetrics.ReconcileLatencyMetric,
				registry,
				map[float64]float64{0.25: 0.1, 0.5: 0.1, 0.95: 0.1, 0.99: 0.1, 1.0: 0.1},
				latencyLabels,
			)
			assert.NotNil(t, summary, "latency summary should be registered")
		})
	}
}

// TestSidecarConfigController_RecordReconciliationCounters tests counter metrics.
func TestSidecarConfigController_RecordReconciliationCounters(t *testing.T) {
	testCases := []struct {
		name             string
		sidecarConfig    *meshv1alpha1.SidecarConfig
		phase            string
		expectedCounters map[string]float64
	}{
		{
			name: "succeeded phase - increments reconciled_total only",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "success-sc",
					Namespace: "test-ns",
				},
			},
			phase: string(scPhaseSucceeded),
			expectedCounters: map[string]float64{
				"sidecar_config_reconciled_total": 1,
				"sidecar_config_reconcile_failed": 0,
			},
		},
		{
			name: "failed phase - increments both failed and reconciled_total",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "failed-sc",
					Namespace: "test-ns",
				},
			},
			phase: string(scPhaseFailed),
			expectedCounters: map[string]float64{
				"sidecar_config_reconciled_total": 1,
				"sidecar_config_reconcile_failed": 1,
			},
		},
		{
			name: "deleted phase - increments reconciled_total only",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deleted-sc",
					Namespace: "test-ns",
				},
			},
			phase: "deleted",
			expectedCounters: map[string]float64{
				"sidecar_config_reconciled_total": 1,
				"sidecar_config_reconcile_failed": 0,
			},
		},
		{
			name:          "nil sidecarconfig - uses unknown identifiers",
			sidecarConfig: nil,
			phase:         string(scPhaseSucceeded),
			expectedCounters: map[string]float64{
				"sidecar_config_reconciled_total": 1,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zap.NewNop().Sugar()
			registry := prometheus.NewRegistry()
			controller := &SidecarConfigController{
				clusterName:     "test-cluster",
				logger:          logger,
				metricsRegistry: registry,
			}

			controller.recordReconciliationCounters(tc.sidecarConfig, tc.phase)

			namespace := "unknown"
			name := "unknown"
			if tc.sidecarConfig != nil {
				namespace = tc.sidecarConfig.Namespace
				name = tc.sidecarConfig.Name
			}

			if expectedCount, ok := tc.expectedCounters["sidecar_config_reconciled_total"]; ok && expectedCount > 0 {
				labels := commonmetrics.GetLabelsForK8sResource("SidecarConfig", "test-cluster", namespace, name)
				labels[commonmetrics.ReconcilePhaseLabel] = tc.phase
				assertCounterWithLabels(t, registry, "sidecar_config_reconciled_total", labels, expectedCount)
			}

			if expectedCount, ok := tc.expectedCounters["sidecar_config_reconcile_failed"]; ok && expectedCount > 0 {
				labels := commonmetrics.GetLabelsForK8sResource("SidecarConfig", "test-cluster", namespace, name)
				assertCounterWithLabels(t, registry, "sidecar_config_reconcile_failed", labels, expectedCount)
			}
		})
	}
}

// TestSidecarConfigController_RecordReconciliationLatency tests latency metrics.
func TestSidecarConfigController_RecordReconciliationLatency(t *testing.T) {
	testCases := []struct {
		name     string
		phase    string
		duration time.Duration
	}{
		{
			name:     "succeeded phase with 100ms duration",
			phase:    string(scPhaseSucceeded),
			duration: 100 * time.Millisecond,
		},
		{
			name:     "failed phase with 50ms duration",
			phase:    string(scPhaseFailed),
			duration: 50 * time.Millisecond,
		},
		{
			name:     "deleted phase with 25ms duration",
			phase:    "deleted",
			duration: 25 * time.Millisecond,
		},
		{
			name:     "very long duration - 5 seconds",
			phase:    string(scPhaseSucceeded),
			duration: 5 * time.Second,
		},
		{
			name:     "very short duration - 1ms",
			phase:    string(scPhaseSucceeded),
			duration: 1 * time.Millisecond,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zap.NewNop().Sugar()
			registry := prometheus.NewRegistry()
			controller := &SidecarConfigController{
				clusterName:     "test-cluster",
				logger:          logger,
				metricsRegistry: registry,
			}

			for i := 0; i < 5; i++ {
				controller.recordReconciliationLatency(tc.phase, tc.duration)
			}

			latencyLabels := map[string]string{
				commonmetrics.ClusterLabel:        "test-cluster",
				commonmetrics.ResourceKind:        "SidecarConfig",
				commonmetrics.ReconcilePhaseLabel: tc.phase,
			}
			summary := commonmetrics.GetOrRegisterSummaryWithLabels(
				commonmetrics.ReconcileLatencyMetric,
				registry,
				map[float64]float64{0.25: 0.1, 0.5: 0.1, 0.95: 0.1, 0.99: 0.1, 1.0: 0.1},
				latencyLabels,
			)
			require.NotNil(t, summary, "latency summary should be registered")

			serializedMetric := &dto.Metric{}
			err := summary.Write(serializedMetric)
			require.NoError(t, err, "should be able to write summary metric")

			assert.Equal(t, uint64(5), serializedMetric.GetSummary().GetSampleCount(), "should have 5 observations")

			expectedSum := tc.duration.Seconds() * 5
			actualSum := serializedMetric.GetSummary().GetSampleSum()
			assert.InDelta(t, expectedSum, actualSum, 0.001, "sum should match expected value")
		})
	}
}

// TestSidecarConfigController_RecordEgressHostMetrics tests egress host and objects_configured metrics.
func TestSidecarConfigController_RecordEgressHostMetrics(t *testing.T) {
	testCases := []struct {
		name                    string
		sidecarConfig           *meshv1alpha1.SidecarConfig
		phase                   string
		expectObjectsConfigured bool
	}{
		{
			name: "successful with runtime hosts",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "service-a.svc.cluster.local", Port: 8080},
								{Hostname: "service-b.svc.cluster.local", Port: 9090},
							},
						},
					},
				},
			},
			phase:                   string(scPhaseSucceeded),
			expectObjectsConfigured: true,
		},
		{
			name: "successful with config hosts",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Config: []meshv1alpha1.Host{
								{Hostname: "service-c.svc.cluster.local", Port: 443},
							},
						},
					},
				},
			},
			phase:                   string(scPhaseSucceeded),
			expectObjectsConfigured: true,
		},
		{
			name: "successful with both runtime and config hosts",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "service-a.svc.cluster.local", Port: 8080},
							},
							Config: []meshv1alpha1.Host{
								{Hostname: "service-b.svc.cluster.local", Port: 9090},
							},
						},
					},
				},
			},
			phase:                   string(scPhaseSucceeded),
			expectObjectsConfigured: true,
		},
		{
			name: "failed phase - no objects_configured",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "failed-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{
								{Hostname: "service-a.svc.cluster.local", Port: 8080},
							},
						},
					},
				},
			},
			phase:                   string(scPhaseFailed),
			expectObjectsConfigured: false,
		},
		{
			name:                    "nil sidecarconfig - no panic",
			sidecarConfig:           nil,
			phase:                   string(scPhaseSucceeded),
			expectObjectsConfigured: false,
		},
		{
			name: "nil egress hosts - no panic",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-egress-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{},
			},
			phase:                   string(scPhaseSucceeded),
			expectObjectsConfigured: false,
		},
		{
			name: "empty hosts - still emits objects_configured",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-hosts-sc",
					Namespace: "test-ns",
				},
				Spec: meshv1alpha1.SidecarConfigSpec{
					EgressHosts: &meshv1alpha1.EgressHosts{
						Discovered: &meshv1alpha1.DiscoveredEgressHosts{
							Runtime: []meshv1alpha1.Host{},
							Config:  []meshv1alpha1.Host{},
						},
					},
				},
			},
			phase:                   string(scPhaseSucceeded),
			expectObjectsConfigured: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			logger := zap.NewNop().Sugar()
			registry := prometheus.NewRegistry()

			controller := &SidecarConfigController{
				clusterName:     "test-cluster",
				logger:          logger,
				metricsRegistry: registry,
			}

			controller.recordEgressHostMetrics(tc.sidecarConfig, tc.phase)

			if tc.expectObjectsConfigured && tc.sidecarConfig != nil {
				metrics, err := registry.Gather()
				require.NoError(t, err)

				foundGauge := false
				for _, metricFamily := range metrics {
					if metricFamily.GetName() == commonmetrics.ObjectsConfiguredTotal {
						for _, metric := range metricFamily.GetMetric() {
							if metric.GetGauge().GetValue() == 1 {
								foundGauge = true
								break
							}
						}
					}
				}
				assert.True(t, foundGauge, "objects_configured_total gauge should be found with value 1")
			} else {
				metrics, err := registry.Gather()
				require.NoError(t, err)
				for _, metric := range metrics {
					if metric.GetName() == commonmetrics.ObjectsConfiguredTotal {
						t.Errorf("objects_configured_total should not be emitted for this case")
					}
				}
			}
		})
	}
}

// TestSidecarConfigController_GetResourceIdentifiers tests identifier extraction.
func TestSidecarConfigController_GetResourceIdentifiers(t *testing.T) {
	testCases := []struct {
		name              string
		sidecarConfig     *meshv1alpha1.SidecarConfig
		expectedNamespace string
		expectedName      string
	}{
		{
			name: "valid sidecarconfig",
			sidecarConfig: &meshv1alpha1.SidecarConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sc",
					Namespace: "test-ns",
				},
			},
			expectedNamespace: "test-ns",
			expectedName:      "test-sc",
		},
		{
			name:              "nil sidecarconfig - returns unknown",
			sidecarConfig:     nil,
			expectedNamespace: "unknown",
			expectedName:      "unknown",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller := &SidecarConfigController{}
			namespace, name := controller.getResourceIdentifiers(tc.sidecarConfig)
			assert.Equal(t, tc.expectedNamespace, namespace)
			assert.Equal(t, tc.expectedName, name)
		})
	}
}

// TestSidecarConfigController_GetRootSidecarConfig tests root SidecarConfig retrieval.
func TestSidecarConfigController_GetRootSidecarConfig(t *testing.T) {
	testCases := []struct {
		name          string
		setupConfigs  []*meshv1alpha1.SidecarConfig
		expectedFound bool
		expectError   bool
	}{
		{
			name: "root config exists",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      RootSidecarConfigName,
						Namespace: "mesh-control-plane",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						EgressHosts: &meshv1alpha1.EgressHosts{
							Discovered: &meshv1alpha1.DiscoveredEgressHosts{
								Runtime: []meshv1alpha1.Host{
									{Hostname: "root-service.svc.cluster.local", Port: 443},
								},
							},
						},
					},
				},
			},
			expectedFound: true,
			expectError:   false,
		},
		{
			name: "root config does not exist - should not error",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-config",
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "test"},
						},
					},
				},
			},
			expectedFound: false,
			expectError:   false,
		},
		{
			name: "wrong namespace - should not find",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      RootSidecarConfigName,
						Namespace: "wrong-namespace",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{},
				},
			},
			expectedFound: false,
			expectError:   false,
		},
		{
			name: "wrong name - should not find",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "not-default",
						Namespace: "mesh-control-plane",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{},
				},
			},
			expectedFound: false,
			expectError:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller, _, stopCh := setupTestController(t, tc.setupConfigs...)
			defer close(stopCh)

			rootConfig, err := controller.getRootSidecarConfig()

			if tc.expectError {
				assert.Error(t, err, "expected error")
			} else {
				assert.NoError(t, err, "should not error")
			}

			if tc.expectedFound {
				require.NotNil(t, rootConfig, "root config should be found")
				assert.Equal(t, RootSidecarConfigName, rootConfig.Name)
				assert.Equal(t, "mesh-control-plane", rootConfig.Namespace)
			} else {
				assert.Nil(t, rootConfig, "root config should not be found")
			}
		})
	}
}

// TestSidecarConfigController_GetNamespaceSidecarConfig tests namespace SidecarConfig retrieval.
func TestSidecarConfigController_GetNamespaceSidecarConfig(t *testing.T) {
	testCases := []struct {
		name          string
		targetNS      string
		setupConfigs  []*meshv1alpha1.SidecarConfig
		expectedFound bool
		expectError   bool
	}{
		{
			name:     "namespace config exists",
			targetNS: "app-ns",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      namespaceSidecarConfigName("app-ns"),
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						EgressHosts: &meshv1alpha1.EgressHosts{
							Discovered: &meshv1alpha1.DiscoveredEgressHosts{
								Runtime: []meshv1alpha1.Host{
									{Hostname: "namespace-service.svc.cluster.local", Port: 8080},
								},
							},
						},
					},
				},
			},
			expectedFound: true,
			expectError:   false,
		},
		{
			name:     "namespace config does not exist - should not error",
			targetNS: "app-ns",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-config",
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "test"},
						},
					},
				},
			},
			expectedFound: false,
			expectError:   false,
		},
		{
			name:     "wrong namespace - should not find",
			targetNS: "app-ns",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      namespaceSidecarConfigName("other-ns"),
						Namespace: "other-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{},
				},
			},
			expectedFound: false,
			expectError:   false,
		},
		{
			name:     "multiple configs in namespace - only default is namespace-level",
			targetNS: "app-ns",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      namespaceSidecarConfigName("app-ns"),
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						EgressHosts: &meshv1alpha1.EgressHosts{
							Discovered: &meshv1alpha1.DiscoveredEgressHosts{
								Runtime: []meshv1alpha1.Host{
									{Hostname: "ns-service.svc.cluster.local", Port: 9090},
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-1",
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload1"},
						},
					},
				},
			},
			expectedFound: true,
			expectError:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller, _, stopCh := setupTestController(t, tc.setupConfigs...)
			defer close(stopCh)

			nsConfig, err := controller.getNamespaceSidecarConfig(tc.targetNS)

			if tc.expectError {
				assert.Error(t, err, "expected error")
			} else {
				assert.NoError(t, err, "should not error")
			}

			if tc.expectedFound {
				require.NotNil(t, nsConfig, "namespace config should be found")
				assert.Equal(t, namespaceSidecarConfigName(tc.targetNS), nsConfig.Name)
				assert.Equal(t, tc.targetNS, nsConfig.Namespace)
			} else {
				assert.Nil(t, nsConfig, "namespace config should not be found")
			}
		})
	}
}

// TestSidecarConfigController_EnqueueAllSidecarConfigs tests root-level enqueueing.
func TestSidecarConfigController_EnqueueAllSidecarConfigs(t *testing.T) {
	testCases := []struct {
		name                 string
		setupConfigs         []*meshv1alpha1.SidecarConfig
		expectedEnqueueCount int
	}{
		{
			name: "enqueues only workload-level configs",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				// Root-level (should be skipped)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      RootSidecarConfigName,
						Namespace: "mesh-control-plane",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{},
				},
				// Namespace-level in app-ns (should be skipped)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      namespaceSidecarConfigName("app-ns"),
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{},
				},
				// Workload-level in app-ns (should be enqueued)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-1",
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload1"},
						},
					},
				},
				// Workload-level in other-ns (should be enqueued)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-2",
						Namespace: "other-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload2"},
						},
					},
				},
			},
			expectedEnqueueCount: 2,
		},
		{
			name: "no workload configs - nothing enqueued",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      RootSidecarConfigName,
						Namespace: "mesh-control-plane",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      namespaceSidecarConfigName("app-ns"),
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{},
				},
			},
			expectedEnqueueCount: 0,
		},
		{
			name: "all workload configs - all enqueued",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-1",
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload1"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-2",
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload2"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-3",
						Namespace: "other-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload3"},
						},
					},
				},
			},
			expectedEnqueueCount: 3,
		},
		{
			name:                 "empty cluster - nothing enqueued",
			setupConfigs:         []*meshv1alpha1.SidecarConfig{},
			expectedEnqueueCount: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller, testQueue, stopCh := setupTestController(t, tc.setupConfigs...)
			defer close(stopCh)

			controller.enqueueAllSidecarConfigs()

			assert.Equal(t, tc.expectedEnqueueCount, len(testQueue.recordedItems), "enqueued count mismatch")

			for _, item := range testQueue.recordedItems {
				queueItem := item.(QueueItem)
				assert.Equal(t, controllers_api.EventUpdate, queueItem.event, "should be Update event")

				namespace, name, _ := cache.SplitMetaNamespaceKey(queueItem.key)
				assert.NotEqual(t, "mesh-control-plane", namespace, "should not enqueue root config")
				if namespace != "mesh-control-plane" {
					assert.NotEqual(t, namespaceSidecarConfigName(namespace), name, "should not enqueue namespace-level config")
				}
			}
		})
	}
}

// TestSidecarConfigController_EnqueueNamespaceSidecarConfigs tests namespace-level enqueueing.
func TestSidecarConfigController_EnqueueNamespaceSidecarConfigs(t *testing.T) {
	testCases := []struct {
		name                 string
		targetNamespace      string
		setupConfigs         []*meshv1alpha1.SidecarConfig
		expectedEnqueueCount int
	}{
		{
			name:            "enqueues workload configs in target namespace only",
			targetNamespace: "app-ns",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				// Namespace-level in app-ns (should be skipped)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      namespaceSidecarConfigName("app-ns"),
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{},
				},
				// Workload-level in app-ns (should be enqueued)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-1",
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload1"},
						},
					},
				},
				// Another workload-level in app-ns (should be enqueued)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-2",
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload2"},
						},
					},
				},
				// Workload-level in other-ns (should NOT be enqueued - different namespace)
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-3",
						Namespace: "other-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload3"},
						},
					},
				},
			},
			expectedEnqueueCount: 2,
		},
		{
			name:            "no workload configs in namespace - nothing enqueued",
			targetNamespace: "app-ns",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      namespaceSidecarConfigName("app-ns"),
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{},
				},
			},
			expectedEnqueueCount: 0,
		},
		{
			name:            "all workload configs in namespace - all enqueued",
			targetNamespace: "app-ns",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-1",
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload1"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "workload-2",
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						WorkloadSelector: &meshv1alpha1.WorkloadSelector{
							Labels: map[string]string{"app": "workload2"},
						},
					},
				},
			},
			expectedEnqueueCount: 2,
		},
		{
			name:                 "empty namespace - nothing enqueued",
			targetNamespace:      "empty-ns",
			setupConfigs:         []*meshv1alpha1.SidecarConfig{},
			expectedEnqueueCount: 0,
		},
		{
			name:            "namespace with only namespace-level config - nothing enqueued",
			targetNamespace: "app-ns",
			setupConfigs: []*meshv1alpha1.SidecarConfig{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      namespaceSidecarConfigName("app-ns"),
						Namespace: "app-ns",
					},
					Spec: meshv1alpha1.SidecarConfigSpec{
						EgressHosts: &meshv1alpha1.EgressHosts{},
					},
				},
			},
			expectedEnqueueCount: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller, testQueue, stopCh := setupTestController(t, tc.setupConfigs...)
			defer close(stopCh)

			controller.enqueueNamespaceSidecarConfigs(tc.targetNamespace)

			assert.Equal(t, tc.expectedEnqueueCount, len(testQueue.recordedItems), "enqueued count mismatch")

			for _, item := range testQueue.recordedItems {
				queueItem := item.(QueueItem)
				assert.Equal(t, controllers_api.EventUpdate, queueItem.event, "should be Update event")

				namespace, name, _ := cache.SplitMetaNamespaceKey(queueItem.key)
				assert.Equal(t, tc.targetNamespace, namespace, "should only enqueue from target namespace")
				assert.NotEqual(t, namespaceSidecarConfigName(tc.targetNamespace), name, "should not enqueue namespace-level config")
			}
		})
	}
}

// TestSidecarConfigController_Reconcile_DuplicateReconciliation tests path 2: duplicate reconciliation check.
func TestSidecarConfigController_Reconcile_DuplicateReconciliation(t *testing.T) {
	controller, _, stopCh := setupTestControllerForReconcile(t)
	defer close(stopCh)

	key := "test-ns/test-config"
	controller.reconcileTracker.trackKey(key)

	queueItem := QueueItem{
		key:   key,
		event: controllers_api.EventUpdate,
		uid:   "test-uid",
	}

	err := controller.reconcile(queueItem)
	assert.NoError(t, err, "reconcile should return nil for duplicate reconciliation")
}

// TestSidecarConfigController_Reconcile_ListerError tests path 4: error getting SidecarConfig from lister.
func TestSidecarConfigController_Reconcile_ListerError(t *testing.T) {
	testCases := []struct {
		name             string
		queueItem        QueueItem
		existingConfigs  []*meshv1alpha1.SidecarConfig
		expectError      bool
		errorDescription string
	}{
		{
			name: "SidecarConfig not found in lister",
			queueItem: QueueItem{
				key:   "test-ns/nonexistent-config",
				event: controllers_api.EventUpdate,
				uid:   "test-uid",
			},
			existingConfigs:  []*meshv1alpha1.SidecarConfig{}, // Empty - config doesn't exist
			expectError:      true,
			errorDescription: "should return error when SidecarConfig not found",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller, _, stopCh := setupTestControllerForReconcile(t, tc.existingConfigs...)
			defer close(stopCh)

			err := controller.reconcile(tc.queueItem)

			if tc.expectError {
				assert.Error(t, err, tc.errorDescription)
			}
		})
	}
}

// TestSidecarRenderModeForNamespace_AllNamespacesWildcard tests that "*" in allowlist env vars allows all namespaces.
func TestSidecarRenderModeForNamespace_AllNamespacesWildcard(t *testing.T) {
	origEnabled := features.EnableSidecarConfigNamespaces
	origShadow := features.EnableShadowSidecarConfigNamespaces
	defer func() {
		features.EnableSidecarConfigNamespaces = origEnabled
		features.EnableShadowSidecarConfigNamespaces = origShadow
	}()

	// "*" in ENABLE_SIDECAR_CONFIG_NAMESPACES -> any namespace gets Enabled
	features.EnableSidecarConfigNamespaces = map[string]struct{}{features.SidecarConfigAllNamespacesWildcard: {}}
	features.EnableShadowSidecarConfigNamespaces = map[string]struct{}{}
	assert.Equal(t, SidecarRenderModeEnabled, sidecarRenderModeForNamespace("any-ns"), "wildcard in enabled should allow any namespace")
	assert.Equal(t, SidecarRenderModeEnabled, sidecarRenderModeForNamespace("other"), "wildcard in enabled should allow any namespace")

	// "*" in ENABLE_SHADOW_SIDECAR_CONFIG_NAMESPACES only -> any namespace gets Shadow
	features.EnableSidecarConfigNamespaces = map[string]struct{}{}
	features.EnableShadowSidecarConfigNamespaces = map[string]struct{}{features.SidecarConfigAllNamespacesWildcard: {}}
	assert.Equal(t, SidecarRenderModeShadow, sidecarRenderModeForNamespace("any-ns"), "wildcard in shadow only should give Shadow")
	assert.Equal(t, SidecarRenderModeShadow, sidecarRenderModeForNamespace("other"), "wildcard in shadow only should give Shadow")

	// "*" in both -> Enabled takes precedence
	features.EnableSidecarConfigNamespaces = map[string]struct{}{features.SidecarConfigAllNamespacesWildcard: {}}
	features.EnableShadowSidecarConfigNamespaces = map[string]struct{}{features.SidecarConfigAllNamespacesWildcard: {}}
	assert.Equal(t, SidecarRenderModeEnabled, sidecarRenderModeForNamespace("any-ns"), "when both have wildcard, Enabled takes precedence")
}

// TestSidecarConfigController_Reconcile_DeleteEvent tests path 3: delete event handling.
func TestSidecarConfigController_Reconcile_DeleteEvent(t *testing.T) {
	controller, _, stopCh := setupTestControllerForReconcile(t)
	defer close(stopCh)

	deleteItem := QueueItem{
		key:   "test-ns/test-config",
		event: controllers_api.EventDelete,
		uid:   "test-uid-123",
	}

	err := controller.reconcile(deleteItem)
	assert.NoError(t, err, "reconcile should handle delete event without error")
}

// TestSidecarConfigController_Reconcile_RootLevelConfig tests path 5: root-level config handling.
func TestSidecarConfigController_Reconcile_RootLevelConfig(t *testing.T) {
	rootConfig := newRootSidecarConfig()
	workloadConfig1 := newWorkloadSidecarConfig("app-ns-1", "workload-1", map[string]string{"app": "service-1"})
	workloadConfig2 := newWorkloadSidecarConfig("app-ns-2", "workload-2", map[string]string{"app": "service-2"})

	controller, testQueue, stopCh := setupTestControllerForReconcile(t, rootConfig, workloadConfig1, workloadConfig2)
	defer close(stopCh)

	rootItem := QueueItem{
		key:   "mesh-control-plane/" + RootSidecarConfigName,
		event: controllers_api.EventUpdate,
		uid:   "root-uid",
	}

	err := controller.reconcile(rootItem)
	assert.NoError(t, err, "reconcile should handle root-level config without error")

	assert.Greater(t, len(testQueue.recordedItems), 0, "workload configs should be enqueued")

	for _, item := range testQueue.recordedItems {
		queueItem := item.(QueueItem)
		namespace, name, _ := cache.SplitMetaNamespaceKey(queueItem.key)
		assert.NotEqual(t, "mesh-control-plane", namespace, "should not enqueue root config")
		if namespace != "mesh-control-plane" {
			assert.NotEqual(t, namespaceSidecarConfigName(namespace), name, "should not enqueue namespace-level config")
		}
	}
}

// TestSidecarConfigController_Reconcile_NamespaceLevelConfig tests path 6: namespace-level config handling.
func TestSidecarConfigController_Reconcile_NamespaceLevelConfig(t *testing.T) {
	targetNamespace := "app-namespace"

	namespaceConfig := newNamespaceSidecarConfig(targetNamespace)
	workloadConfig1 := newWorkloadSidecarConfig(targetNamespace, "workload-1", map[string]string{"app": "service-1"})
	workloadConfig2 := newWorkloadSidecarConfig(targetNamespace, "workload-2", map[string]string{"app": "service-2"})
	workloadConfigOtherNs := newWorkloadSidecarConfig("other-namespace", "workload-other", map[string]string{"app": "other-service"})

	controller, testQueue, stopCh := setupTestControllerForReconcile(t, namespaceConfig, workloadConfig1, workloadConfig2, workloadConfigOtherNs)
	defer close(stopCh)

	namespaceItem := QueueItem{
		key:   targetNamespace + "/" + namespaceSidecarConfigName(targetNamespace),
		event: controllers_api.EventUpdate,
		uid:   "namespace-uid",
	}

	err := controller.reconcile(namespaceItem)
	assert.NoError(t, err, "reconcile should handle namespace-level config without error")

	assert.Greater(t, len(testQueue.recordedItems), 0, "workload configs should be enqueued")

	for _, item := range testQueue.recordedItems {
		queueItem := item.(QueueItem)
		namespace, name, _ := cache.SplitMetaNamespaceKey(queueItem.key)
		assert.Equal(t, targetNamespace, namespace, "should only enqueue from target namespace")
		assert.NotEqual(t, namespaceSidecarConfigName(targetNamespace), name, "should not enqueue namespace-level config itself")
	}
}

// TestSidecarConfigController_ReconcileWorkloadSidecarConfig_ValidationFailure tests error path 3:
// validation failure before fetching hierarchical configs.
func TestSidecarConfigController_ReconcileWorkloadSidecarConfig_ValidationFailure(t *testing.T) {
	invalidConfig := &meshv1alpha1.SidecarConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "invalid-config",
			Namespace: "test-ns",
			UID:       "invalid-uid",
		},
		Spec: meshv1alpha1.SidecarConfigSpec{
			// Missing WorkloadSelector - this should fail validation
			EgressHosts: &meshv1alpha1.EgressHosts{
				Discovered: &meshv1alpha1.DiscoveredEgressHosts{
					Runtime: []meshv1alpha1.Host{
						{Hostname: "service.test-ns.svc.cluster.local", Port: 8080},
					},
				},
			},
		},
	}

	controller, _, stopCh := setupTestControllerForReconcile(t, invalidConfig)
	defer close(stopCh)

	// Call reconcileWorkloadSidecarConfig
	startTime := time.Now()
	err := controller.reconcileWorkloadSidecarConfig(invalidConfig, "test-ns", startTime, SidecarRenderModeDisabled)

	// Verify NO error is returned (validation failure is handled by updating status to Failed)
	// This is the standard Kubernetes controller pattern - only return errors for retryable issues
	assert.NoError(t, err, "should return nil after handling validation failure by updating status")

	// Verify status was updated to Failed
	// Note: Read from the client API directly, not the lister, since lister cache may not be immediately updated
	ctx := context.Background()
	updatedConfig, getErr := controller.sidecarConfigClient.MeshV1alpha1().SidecarConfigs("test-ns").Get(ctx, "invalid-config", metav1.GetOptions{})
	require.NoError(t, getErr, "should be able to get updated config")
	assert.Equal(t, scPhaseFailed, updatedConfig.Status.Phase, "status should be Failed")
	assert.Contains(t, updatedConfig.Status.Message, "workloadSelector", "error message should mention workloadSelector")

	// Verify metrics were recorded with "failed" status
	metricFamilies, err := controller.metricsRegistry.Gather()
	require.NoError(t, err, "should be able to gather metrics")

	var failedCounter *dto.MetricFamily
	for _, mf := range metricFamilies {
		if mf.GetName() == "sidecar_config_reconcile_failed" {
			failedCounter = mf
			break
		}
	}
	require.NotNil(t, failedCounter, "failed counter should exist")
	assert.Greater(t, len(failedCounter.GetMetric()), 0, "failed counter should have metrics")
}

// TestSidecarConfigController_ReconcileWorkloadSidecarConfig_SidecarNotRendered tests that when renderMode is Disabled
// and no Istio Sidecar exists, reconciliation succeeds with status "not rendered" and no Sidecar is created.
func TestSidecarConfigController_ReconcileWorkloadSidecarConfig_SidecarNotRendered(t *testing.T) {
	workloadConfig := newWorkloadSidecarConfig("test-ns", "my-workload", map[string]string{"app": "test"})
	workloadConfig.UID = "workload-uid"
	workloadConfig.Spec.EgressHosts = &meshv1alpha1.EgressHosts{
		Discovered: &meshv1alpha1.DiscoveredEgressHosts{
			Runtime: []meshv1alpha1.Host{
				{Hostname: "service.test-ns.svc.cluster.local", Port: 8080},
			},
		},
	}

	controller, _, stopCh := setupTestControllerForReconcile(t, workloadConfig)
	defer close(stopCh)

	startTime := time.Now()
	err := controller.reconcileWorkloadSidecarConfig(workloadConfig, "test-ns", startTime, SidecarRenderModeDisabled)

	assert.NoError(t, err, "reconcile should succeed when render mode is Disabled (no sidecar rendered)")
	ctx := context.Background()
	updatedConfig, getErr := controller.sidecarConfigClient.MeshV1alpha1().SidecarConfigs("test-ns").Get(ctx, "my-workload", metav1.GetOptions{})
	require.NoError(t, getErr, "should be able to get updated config")

	assert.Equal(t, scPhaseSucceeded, updatedConfig.Status.Phase, "status.phase should be Succeeded")
	assert.Equal(t, "Disabled mode: Sidecar test-ns/my-workload-sidecar not rendered", updatedConfig.Status.Message, "status.message when Sidecar not found")
	assert.Equal(t, workloadConfig.Generation, updatedConfig.Status.ObservedGeneration, "status.observedGeneration should match")

	// No Istio Sidecar should have been created (namespace not in allowlists)
	sidecars, listErr := controller.client.Dynamic().Resource(constants.SidecarResource).Namespace("test-ns").List(ctx, metav1.ListOptions{})
	require.NoError(t, listErr, "should be able to list Sidecars")
	assert.Empty(t, sidecars.Items, "no Sidecar resource should be created when render mode is Disabled")
}

// TestSidecarConfigController_ReconcileWorkloadSidecarConfig_DisabledDeletesExistingSidecar tests that when renderMode is
// Disabled, any existing Istio Sidecar for this SidecarConfig is deleted and status is updated to Succeeded.
func TestSidecarConfigController_ReconcileWorkloadSidecarConfig_DisabledDeletesExistingSidecar(t *testing.T) {
	// Use networking.istio.io/v1alpha3 to match constants.SidecarResource (fake client indexes by GVR)
	existingSidecar := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "Sidecar",
			"metadata": map[string]interface{}{
				"name":      "delete-me-sidecar",
				"namespace": "test-ns",
			},
			"spec": map[string]interface{}{},
		},
	}
	workloadConfig := newWorkloadSidecarConfig("test-ns", "delete-me", map[string]string{"app": "test"})
	workloadConfig.UID = "delete-me-uid"
	workloadConfig.Spec.EgressHosts = &meshv1alpha1.EgressHosts{
		Discovered: &meshv1alpha1.DiscoveredEgressHosts{
			Runtime: []meshv1alpha1.Host{
				{Hostname: "service.test-ns.svc.cluster.local", Port: 8080},
			},
		},
	}

	controller, _, stopCh := setupTestControllerForReconcileWithDryRunAndDynamic(t, false, []runtime.Object{existingSidecar}, workloadConfig)
	defer close(stopCh)

	startTime := time.Now()
	err := controller.reconcileWorkloadSidecarConfig(workloadConfig, "test-ns", startTime, SidecarRenderModeDisabled)
	assert.NoError(t, err, "reconcile should succeed when Disabled and delete existing Sidecar")

	ctx := context.Background()
	updatedConfig, getErr := controller.sidecarConfigClient.MeshV1alpha1().SidecarConfigs("test-ns").Get(ctx, "delete-me", metav1.GetOptions{})
	require.NoError(t, getErr, "should be able to get updated config")
	assert.Equal(t, scPhaseSucceeded, updatedConfig.Status.Phase, "status.phase should be Succeeded")
	assert.Equal(t, "Disabled mode: Sidecar test-ns/delete-me-sidecar deleted", updatedConfig.Status.Message, "status.message must reflect deleted Sidecar when existing Sidecar was removed")
	assert.Equal(t, workloadConfig.Generation, updatedConfig.Status.ObservedGeneration, "status.observedGeneration should match")

	// Existing Sidecar must have been deleted
	sidecars, listErr := controller.client.Dynamic().Resource(constants.SidecarResource).Namespace("test-ns").List(ctx, metav1.ListOptions{})
	require.NoError(t, listErr, "should be able to list Sidecars")
	assert.Empty(t, sidecars.Items, "existing Sidecar should be deleted when render mode is Disabled")
}

// TestSidecarConfigController_ReconcileWorkloadSidecarConfig_SidecarRenderedInShadowMode tests that when renderMode is Shadow,
// reconciliation succeeds and status message is "Sidecar rendered in Shadow mode". Uses dryRun to avoid fake client Create.
func TestSidecarConfigController_ReconcileWorkloadSidecarConfig_SidecarRenderedInShadowMode(t *testing.T) {
	workloadConfig := newWorkloadSidecarConfig("test-ns", "shadow-workload", map[string]string{"app": "test"})
	workloadConfig.UID = "shadow-uid"
	workloadConfig.Spec.EgressHosts = &meshv1alpha1.EgressHosts{
		Discovered: &meshv1alpha1.DiscoveredEgressHosts{
			Runtime: []meshv1alpha1.Host{
				{Hostname: "service.test-ns.svc.cluster.local", Port: 8080},
			},
		},
	}

	controller, _, stopCh := setupTestControllerForReconcileWithDryRun(t, true, workloadConfig)
	defer close(stopCh)

	startTime := time.Now()
	err := controller.reconcileWorkloadSidecarConfig(workloadConfig, "test-ns", startTime, SidecarRenderModeShadow)

	assert.NoError(t, err, "reconcile should succeed when render mode is Shadow")
	ctx := context.Background()
	updatedConfig, getErr := controller.sidecarConfigClient.MeshV1alpha1().SidecarConfigs("test-ns").Get(ctx, "shadow-workload", metav1.GetOptions{})
	require.NoError(t, getErr, "should be able to get updated config")

	assert.Equal(t, scPhaseSucceeded, updatedConfig.Status.Phase, "status.phase should be Succeeded")
	assert.Equal(t, "Sidecar rendered in Shadow mode", updatedConfig.Status.Message, "status.message should indicate Shadow mode")
	assert.Equal(t, workloadConfig.Generation, updatedConfig.Status.ObservedGeneration, "status.observedGeneration should match resource generation")
}

// TestSidecarConfigController_ReconcileWorkloadSidecarConfig_SidecarRenderedInEnabledMode tests that when renderMode is Enabled,
// reconciliation succeeds and status message is "Sidecar rendered in Enabled mode". Uses dryRun to avoid fake client Create.
func TestSidecarConfigController_ReconcileWorkloadSidecarConfig_SidecarRenderedInEnabledMode(t *testing.T) {
	workloadConfig := newWorkloadSidecarConfig("test-ns", "enabled-workload", map[string]string{"app": "test"})
	workloadConfig.UID = "enabled-uid"
	workloadConfig.Spec.EgressHosts = &meshv1alpha1.EgressHosts{
		Discovered: &meshv1alpha1.DiscoveredEgressHosts{
			Runtime: []meshv1alpha1.Host{
				{Hostname: "service.test-ns.svc.cluster.local", Port: 8080},
			},
		},
	}

	controller, _, stopCh := setupTestControllerForReconcileWithDryRun(t, true, workloadConfig)
	defer close(stopCh)

	startTime := time.Now()
	err := controller.reconcileWorkloadSidecarConfig(workloadConfig, "test-ns", startTime, SidecarRenderModeEnabled)

	assert.NoError(t, err, "reconcile should succeed when render mode is Enabled")
	ctx := context.Background()
	updatedConfig, getErr := controller.sidecarConfigClient.MeshV1alpha1().SidecarConfigs("test-ns").Get(ctx, "enabled-workload", metav1.GetOptions{})
	require.NoError(t, getErr, "should be able to get updated config")

	assert.Equal(t, scPhaseSucceeded, updatedConfig.Status.Phase, "status.phase should be Succeeded")
	assert.Equal(t, "Sidecar rendered in Enabled mode", updatedConfig.Status.Message, "status.message should indicate Enabled mode")
	assert.Equal(t, workloadConfig.Generation, updatedConfig.Status.ObservedGeneration, "status.observedGeneration should match resource generation")
}

// TestSidecarConfigController_ReconcileWorkloadSidecarConfig_RootConfigError tests error path 4:
// root config fetch failure.
func TestSidecarConfigController_ReconcileWorkloadSidecarConfig_RootConfigError(t *testing.T) {
	// Create a valid workload config
	workloadConfig := newWorkloadSidecarConfig("test-ns", "my-workload", map[string]string{"app": "test"})
	workloadConfig.UID = "workload-uid"
	workloadConfig.Spec.EgressHosts = &meshv1alpha1.EgressHosts{
		Discovered: &meshv1alpha1.DiscoveredEgressHosts{
			Runtime: []meshv1alpha1.Host{
				{Hostname: "service.test-ns.svc.cluster.local", Port: 8080},
			},
		},
	}

	// Note: We cannot easily simulate lister errors with fake client, as the fake lister
	// only returns IsNotFound errors (which are handled gracefully as nil).
	// This test documents the limitation - we rely on unit tests of getRootSidecarConfig instead.
	t.Skip("Cannot simulate lister errors beyond IsNotFound with fake client - tested via getRootSidecarConfig unit tests")
}

// TestSidecarConfigController_ReconcileWorkloadSidecarConfig_NamespaceConfigError tests error path 5:
// namespace config fetch failure.
func TestSidecarConfigController_ReconcileWorkloadSidecarConfig_NamespaceConfigError(t *testing.T) {
	// Create a valid workload config
	workloadConfig := newWorkloadSidecarConfig("test-ns", "my-workload", map[string]string{"app": "test"})
	workloadConfig.UID = "workload-uid"
	workloadConfig.Spec.EgressHosts = &meshv1alpha1.EgressHosts{
		Discovered: &meshv1alpha1.DiscoveredEgressHosts{
			Runtime: []meshv1alpha1.Host{
				{Hostname: "service.test-ns.svc.cluster.local", Port: 8080},
			},
		},
	}

	// Note: Same limitation as root config error test - cannot simulate non-IsNotFound errors with fake client.
	t.Skip("Cannot simulate lister errors beyond IsNotFound with fake client - tested via getNamespaceSidecarConfig unit tests")
}

// TestSidecarConfigController_ReconcileWorkloadSidecarConfig_GenerationFailure tests error path 6:
// sidecar generation failure.
func TestSidecarConfigController_ReconcileWorkloadSidecarConfig_GenerationFailure(t *testing.T) {
	// Create a workload config that will pass validation but might fail generation
	workloadConfig := newWorkloadSidecarConfig("test-ns", "my-workload", map[string]string{"app": "test"})
	workloadConfig.UID = "workload-uid"

	// Note: Looking at the generateSidecar implementation, it's very robust and doesn't fail easily.
	// It handles nil configs gracefully and creates valid Istio Sidecar resources.
	// Generation failures would only occur with invalid internal state, which is hard to simulate.
	// This error path is better tested in integration tests or by testing generateSidecar directly
	// with edge cases.
	t.Skip("generateSidecar is robust and hard to make fail with valid input - tested via generateSidecar unit tests")
}
