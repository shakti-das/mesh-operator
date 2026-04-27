package metrics

import (
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestGetLabelsForK8sResource(t *testing.T) {
	actualLabels := GetLabelsForK8sResource("MeshOperator", "cluster-1", "namespace-1", "resource-1")
	expectedLabels := map[string]string{
		"k8s_cluster":       "cluster-1",
		"k8s_namespace":     "namespace-1",
		"k8s_resource_name": "resource-1",
		"kind":              "MeshOperator",
	}
	assert.Equal(t, expectedLabels, actualLabels)
}

func TestEraseMetricsForK8sResource(t *testing.T) {
	svc1Labels := GetLabelsForK8sResource("Service", "cluster-1", "namespace-1", "svc-1")
	svc2Labels := GetLabelsForK8sResource("Service", "cluster-1", "namespace-1", "svc-2")
	svc3Labels := GetLabelsForK8sResource("Service", "cluster-1", "namespace-2", "svc-1")
	svc4Labels := GetLabelsForK8sResource("Service", "cluster-2", "namespace-1", "svc-1")

	testCases := []struct {
		name string

		metricsToMatch   []string
		clusterToMatch   string
		namespaceToMatch string
		resourceToMatch  string

		// matchExpected - if match expected, tests will verify that metrics for the cluster-1/namespace-1/svc-1 are removed
		matchExpected bool
	}{
		{
			name:             "Erase by matching labels",
			clusterToMatch:   "cluster-1",
			namespaceToMatch: "namespace-1",
			resourceToMatch:  "svc-1",
			metricsToMatch:   []string{"metric1", "metric2"},
			matchExpected:    true,
		},
		{
			name:             "No match by metric",
			clusterToMatch:   "cluster-1",
			namespaceToMatch: "namespace-1",
			resourceToMatch:  "svc-1",
			metricsToMatch:   []string{"otherMetric"},
			matchExpected:    false,
		},
		{
			name:             "No match by cluster",
			clusterToMatch:   "otherCluster",
			namespaceToMatch: "namespace-1",
			resourceToMatch:  "svc-1",
			metricsToMatch:   []string{"metric1", "metric2"},
			matchExpected:    false,
		},
		{
			name:             "No match by namespace",
			clusterToMatch:   "cluster-1",
			namespaceToMatch: "otherNamespace",
			resourceToMatch:  "svc-1",
			metricsToMatch:   []string{"metric1", "metric2"},
			matchExpected:    false,
		},
		{
			name:             "No match by resource",
			clusterToMatch:   "cluster-1",
			namespaceToMatch: "namespace-1",
			resourceToMatch:  "otherSvc",
			metricsToMatch:   []string{"metric1", "metric2"},
			matchExpected:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()

			registerMetricsForService(registry, svc1Labels)
			registerMetricsForService(registry, svc2Labels)
			registerMetricsForService(registry, svc3Labels)
			registerMetricsForService(registry, svc4Labels)

			expectedRegistry := prometheus.NewRegistry()
			registerMetricsForService(expectedRegistry, svc2Labels)
			registerMetricsForService(expectedRegistry, svc3Labels)
			registerMetricsForService(expectedRegistry, svc4Labels)
			if !tc.matchExpected {
				registerMetricsForService(expectedRegistry, svc1Labels)
			}

			EraseMetricsForK8sResource(
				registry,
				tc.metricsToMatch,
				map[string]string{
					ClusterLabel:      tc.clusterToMatch,
					NamespaceLabel:    tc.namespaceToMatch,
					ResourceNameLabel: tc.resourceToMatch,
				})

			actualIndex := metricsToIndex(registry)
			expectedIndex := metricsToIndex(expectedRegistry)
			assert.ElementsMatchf(t, expectedIndex, actualIndex, "")
		})
	}
}

func TestGetObjectIdentity(t *testing.T) {
	testCases := []struct {
		name             string
		objectName       string
		objectLabels     map[string]string
		identityLabelSet bool
		expectedIdentity string
	}{
		{
			name:             "NoIdentity",
			identityLabelSet: true,
			objectName:       "svc-1",
			expectedIdentity: "svc-1",
		},
		{
			name:             "IdentityProvided",
			identityLabelSet: true,
			objectName:       "svc-1",
			objectLabels:     map[string]string{"my_identity_label": "test-svc-identity"},
			expectedIdentity: "test-svc-identity",
		},
		{
			name:             "IdentityLabelNotSet",
			identityLabelSet: false,
			objectName:       "svc-1",
			objectLabels:     map[string]string{"my_identity_label": "test-svc-identity"},
			expectedIdentity: "svc-1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.identityLabelSet {
				features.IdentityLabel = "my_identity_label"
				defer func() {
					features.IdentityLabel = ""
				}()
			}

			identity := GetObjectIdentity(tc.objectName, tc.objectLabels)

			assert.Equal(t, tc.expectedIdentity, identity)
		})
	}
}

func registerMetricsForService(registry *prometheus.Registry, resourceLabels map[string]string) {
	// Metric with resource labels
	GetOrRegisterGaugeWithLabels("metric1", resourceLabels, registry).Set(1)

	// Metric with additional labels
	additionalLabels := map[string]string{"additionalLabel": "value"}
	for k, v := range resourceLabels {
		additionalLabels[k] = v
	}
	GetOrRegisterGaugeWithLabels("metric2", additionalLabels, registry).Set(1)
}

func metricsToIndex(registry *prometheus.Registry) []string {
	result := []string{}

	metricsInRegistry, _ := registry.Gather()
	for _, metric := range metricsInRegistry {
		for _, metricWithLabels := range metric.GetMetric() {
			key := metric.GetName()
			for _, label := range metricWithLabels.GetLabel() {
				key += "," + label.GetName() + "=" + label.GetValue()
			}
			result = append(result, key)
		}
	}
	return result
}
