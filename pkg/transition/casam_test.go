package transition

import (
	"context"
	"os"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	metricstesting "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics/testing"
	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"github.com/joeyb/goldenfiles"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func TestComposeCasamOverlays(t *testing.T) {
	testFilesDir := "projects/services/servicemesh/mesh-operator/pkg/testdata/TestComposeCasamOverlays/"
	workspacePath := os.Getenv("WORKSPACE_PATH")
	goldenfiles.GoldenFilePath = workspacePath + testFilesDir

	testCases := []struct {
		name           string
		routingContext *unstructured.Unstructured
		namespace      string
		serviceName    string
		expectedError  string
	}{
		{
			name:           "Live: blue, Test: blue",
			routingContext: createRoutingContext("blue", "blue"),
			namespace:      "core-on-sam",
			serviceName:    "ora1-casam-app",
		},
		{
			name:           "Live: blue, Test: green",
			routingContext: createRoutingContext("blue", "green"),
			namespace:      "core-on-sam",
			serviceName:    "ora1-casam-app",
		},
		{
			name:           "Live: green, Test: blue",
			routingContext: createRoutingContext("green", "blue"),
			namespace:      "core-on-sam",
			serviceName:    "ora1-casam-app",
		},
		{
			name:           "Live: green, Test: green",
			routingContext: createRoutingContext("green", "green"),
			namespace:      "core-on-sam",
			serviceName:    "ora1-casam-app",
		},
		{
			name:           "Live: blue, Test: blue (HSR)",
			routingContext: createRoutingContext("blue", "blue"),
			namespace:      "hsr",
			serviceName:    "ora1-hsr-app",
		},
		{
			name:           "Live: blue, Test: green (HSR)",
			routingContext: createRoutingContext("blue", "green"),
			namespace:      "hsr",
			serviceName:    "ora1-hsr-app",
		},
		{
			name:           "Live: green, Test: blue (HSR)",
			routingContext: createRoutingContext("green", "blue"),
			namespace:      "hsr",
			serviceName:    "ora1-hsr-app",
		},
		{
			name:           "Live: green, Test: green (HSR)",
			routingContext: createRoutingContext("green", "green"),
			namespace:      "hsr",
			serviceName:    "ora1-hsr-app",
		},
		{
			name: "Invalid RC: missing live version",
			routingContext: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"testTrafficAppVersion": "test",
					},
				},
			},
			expectedError: "live version not in RC",
		},
		{
			name: "Invalid RC: missing test version",
			routingContext: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"liveTrafficAppVersion": "test",
					},
				},
			},
			expectedError: "test version not in RC",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			overlays, err := composeOverlays(tc.namespace, tc.serviceName, tc.routingContext)

			if len(tc.expectedError) > 0 {
				assert.Error(t, err)
				assert.ErrorContains(t, err, tc.expectedError)
				return
			}

			assert.NoError(t, err)
			yamlBytes, err := yaml.Marshal(overlays)
			assert.NoError(t, err)
			goldenfiles.EqualString(t, string(yamlBytes), goldenfiles.Config{Suffix: ".yaml"})
		})
	}
}

func TestComposeServiceSelector(t *testing.T) {
	kube_test.NewServiceBuilder("test-namespace", "test-svc")

	testCases := []struct {
		name             string
		service          corev1.Service
		expectedSelector map[string]string
		expectedError    string
	}{
		{
			name: "All components",
			service: kube_test.NewServiceBuilder("test-namespace", "test-svc").
				SetLabels(map[string]string{
					"p_cell":             "cell",
					"p_servicename":      "service",
					"p_service_instance": "service01",
					"p_other_label":      "some-value",
				}).
				Build(),
			expectedSelector: map[string]string{
				"p_cell":             "cell",
				"p_servicename":      "service",
				"p_service_instance": "service01",
			},
		},
		{
			name: "Cell and Service",
			service: kube_test.NewServiceBuilder("test-namespace", "test-svc").
				SetLabels(map[string]string{
					"p_cell":        "cell",
					"p_servicename": "service",
					"p_other_label": "some_value",
				}).
				Build(),
			expectedSelector: map[string]string{
				"p_cell":        "cell",
				"p_servicename": "service",
			},
		},
		{
			name: "Missing Cell",
			service: kube_test.NewServiceBuilder("test-namespace", "test-svc").
				SetLabels(map[string]string{
					"p_servicename":      "service",
					"p_service_instance": "service01",
					"p_other_label":      "some-value",
				}).
				Build(),
			expectedError: "missing p_cell label",
		},
		{
			name: "Missing Service",
			service: kube_test.NewServiceBuilder("test-namespace", "test-svc").
				SetLabels(map[string]string{
					"p_cell":        "cell",
					"p_other_label": "some-value",
				}).
				Build(),
			expectedError: "missing p_servicename label",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			selector, err := composeServiceSelector(&tc.service)

			if len(tc.expectedError) > 0 {
				assert.Error(t, err)
				assert.ErrorContains(t, err, tc.expectedError)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedSelector, selector)
		})
	}
}

func TestInitCasamOverlays(t *testing.T) {
	var serviceName = "ora1-casam-app"
	var clusterName = "sam-processing00001"
	var mopName = "rus--ora1-casam-app"

	var platformLabels = map[string]string{
		"p_cell":        "cell",
		"p_servicename": "service",
	}

	var casamService = kube_test.NewServiceBuilder(serviceName, "core-on-sam").
		SetLabels(platformLabels).
		Build()
	casamService.SetUID("casam-uid")

	existingMop := v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mopName,
			Namespace: "core-on-sam",
		},
	}

	routingContext := createRoutingContext("blue", "green")
	routingContext.SetName(serviceName)
	routingContext.SetNamespace("core-on-sam")

	testCases := []struct {
		name                string
		service             corev1.Service
		routingContext      *unstructured.Unstructured
		mopInCluster        *v1alpha1.MeshOperator
		featureEnabled      bool
		isMulticluster      bool
		expectNewMopCreated bool
		expectInitFailure   bool
	}{
		{
			name:                "Feature disabled",
			featureEnabled:      false,
			service:             casamService,
			routingContext:      routingContext,
			expectNewMopCreated: false,
		},
		{
			name:           "Non casam namespace",
			featureEnabled: true,
			service: kube_test.NewServiceBuilder("test-service", "non-casam-namespace").
				SetLabels(platformLabels).
				Build(),
			routingContext:      createRoutingContext("blue", "green"),
			expectNewMopCreated: false,
		},
		{
			name:                "RC doesn't exist",
			featureEnabled:      true,
			service:             casamService,
			expectNewMopCreated: false,
		},
		{
			name:                "Overlay MOP already exists",
			featureEnabled:      true,
			service:             casamService,
			mopInCluster:        &existingMop,
			expectNewMopCreated: false,
		},
		{
			name:                "Successful init",
			featureEnabled:      true,
			service:             casamService,
			routingContext:      routingContext,
			expectNewMopCreated: true,
		},
		{
			name:           "Failed init (missing live/test attributes)",
			featureEnabled: true,
			service:        casamService,
			routingContext: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "mesh.sfdc.net/v1",
					"kind":       "RoutingContext",
					"metadata": map[string]interface{}{
						"namespace": "core-on-sam",
						"name":      serviceName,
					},
					"spec": map[string]interface{}{},
				},
			},
			expectNewMopCreated: false,
			expectInitFailure:   true,
		},
		{
			name:                "Successful init (multicluster)",
			featureEnabled:      true,
			isMulticluster:      true,
			service:             casamService,
			routingContext:      routingContext,
			expectNewMopCreated: true,
		},
		{
			name:                "RC doesn't exist (multicluster)",
			featureEnabled:      true,
			isMulticluster:      true,
			service:             casamService,
			expectNewMopCreated: false,
		},
		{
			name:                "Overlay MOP already exists (multicluster)",
			featureEnabled:      true,
			isMulticluster:      true,
			service:             casamService,
			mopInCluster:        &existingMop,
			expectNewMopCreated: false,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			previousValue := EnableInitCasamMops
			EnableInitCasamMops = tc.featureEnabled
			defer func() { EnableInitCasamMops = previousValue }()

			var client, primaryClient kube.Client

			if tc.isMulticluster {
				primaryClient = kube_test.NewKubeClientBuilder().
					AddDynamicClientObjects(tc.routingContext).
					Build()

				client = kube_test.NewKubeClientBuilder().
					AddMopClientObjects(tc.mopInCluster).
					Build()
			} else {
				primaryClient = kube_test.NewKubeClientBuilder().
					AddDynamicClientObjects(tc.routingContext).
					AddMopClientObjects(tc.mopInCluster).
					Build()
				client = primaryClient
			}

			registry := prometheus.NewRegistry()

			InitCasamOverlays(logger, primaryClient, client, registry, clusterName, &tc.service)

			var metricLabels = map[string]string{
				"k8s_cluster":       clusterName,
				"kind":              "Service",
				"k8s_namespace":     tc.service.Namespace,
				"k8s_resource_name": tc.service.Name,
			}

			mopInCluster, err := client.MopApiClient().MeshV1alpha1().MeshOperators(tc.service.Namespace).Get(context.TODO(), mopName, metav1.GetOptions{})
			if tc.expectNewMopCreated {
				assert.NoError(t, err)
				assert.NotNil(t, mopInCluster)
				assert.Equal(t, mopInCluster.Spec.ServiceSelector, platformLabels)
				assert.Equal(t, mopInCluster.Labels[constants.MeshIoManagedByLabel], "mesh-operator")
				assert.ElementsMatch(t, mopInCluster.GetOwnerReferences(), []metav1.OwnerReference{{
					APIVersion: constants.ServiceResource.GroupVersion().String(),
					Kind:       constants.ServiceKind.Kind,
					Name:       tc.service.GetName(),
					UID:        tc.service.UID,
				}})
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, CasamInitOverlaysMopError, metricLabels, 0)
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, CasamInitOverlaysMopSuccess, metricLabels, 1)
				return
			}
			if tc.mopInCluster != nil {
				assert.NoError(t, err)
				assert.Equal(t, tc.mopInCluster, mopInCluster)
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, CasamInitOverlaysMopError, metricLabels, 0)
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, CasamInitOverlaysMopSuccess, metricLabels, 0)
				return
			}
			if tc.expectInitFailure {
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, CasamInitOverlaysMopError, metricLabels, 1)
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, CasamInitOverlaysMopSuccess, metricLabels, 0)
				return
			}

			assert.Error(t, err)
			assert.True(t, k8serrors.IsNotFound(err))
			assert.Nil(t, mopInCluster)
			metricstesting.AssertEqualsCounterValueWithLabel(t, registry, CasamInitOverlaysMopError, metricLabels, 0)
			metricstesting.AssertEqualsCounterValueWithLabel(t, registry, CasamInitOverlaysMopSuccess, metricLabels, 0)

			return

		})
	}
}

func createRoutingContext(liveColor, testColor string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mesh.sfdc.net/v1",
			"kind":       "RoutingContext",
			"spec": map[string]interface{}{
				"liveTrafficAppVersion": liveColor,
				"testTrafficAppVersion": testColor,
			},
		},
	}
}
