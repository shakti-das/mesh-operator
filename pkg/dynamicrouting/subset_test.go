package dynamicrouting

import (
	"reflect"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"go.uber.org/zap/zaptest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/istio-ecosystem/mesh-operator/common/pkg/dynamicrouting"

	"github.com/stretchr/testify/assert"
)

func TestGetUniqueLabelSubsets(t *testing.T) {
	testCases := []struct {
		name                 string
		subsetLabelKeys      []string
		deploymentLabels     []map[string]string
		expectedSubsetLabels map[string]map[string]string
	}{
		{
			"SubsetAdded",
			[]string{"version", "user"},
			[]map[string]string{{
				"version": "v1", "user": "u1",
			}, {
				"version": "v1", "user": "u2",
			}},
			map[string]map[string]string{
				"v1-u1": {"user": "u1", "version": "v1"},
				"v1-u2": {"user": "u2", "version": "v1"},
			},
		},
		{
			"PartialSubsetDiscarded",
			[]string{"version", "user"},
			[]map[string]string{{
				"version": "v1", "user": "u1",
			}, {
				"user": "u2",
			}, {
				"version": "v3",
			}, {
				"version": "v4", "user": "u4",
			}},
			map[string]map[string]string{
				"v1-u1": {"user": "u1", "version": "v1"},
				"v4-u4": {"user": "u4", "version": "v4"},
			},
		},
		{
			"EmptySubset",
			[]string{"version", "user"},
			[]map[string]string{{
				"vers": "v1", "user": "u1",
			}, {
				"ver": "v1", "user": "u2",
			}},
			map[string]map[string]string{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			subsetLabels := dynamicrouting.GetUniqueLabelSubsets(tc.subsetLabelKeys, tc.deploymentLabels)
			assert.Equal(t, tc.expectedSubsetLabels, subsetLabels)
		})
	}
}

func TestGetMetadataIfDynamicRoutingEnabled(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	dynamicRoutingDisabledService := kube_test.CreateServiceAsUnstructuredObject("test1", "test")
	dynamicRoutingEnabledSvc := kube_test.CreateServiceAsUnstructuredObject("test", "test")
	dynamicRoutingEnabledSvc.SetAnnotations(map[string]string{
		string(constants.DynamicRoutingLabels): "version,user",
	})
	dynamicRoutingSvcWithDefaultRoutingCoordinates := kube_test.CreateServiceAsUnstructuredObject("test", "test")
	dynamicRoutingSvcWithDefaultRoutingCoordinates.SetAnnotations(map[string]string{
		string(constants.DynamicRoutingLabels):             "version,user",
		string(constants.DynamicRoutingDefaultCoordinates): "version=v1,user=u1",
	})

	dynamicRoutingSvcWithMalformedDefaultRoutingCoordinates := kube_test.CreateServiceAsUnstructuredObject("test", "test")
	dynamicRoutingSvcWithMalformedDefaultRoutingCoordinates.SetAnnotations(map[string]string{
		string(constants.DynamicRoutingLabels):             "version,user",
		string(constants.DynamicRoutingDefaultCoordinates): "version",
	})
	tests := []struct {
		name             string
		object           *unstructured.Unstructured
		service          metav1.Object
		expectedMetadata map[string]string
	}{
		{
			name: "Nil metadata for dynamic routing disabled service",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{},
						},
					},
				},
			},
			service: dynamicRoutingDisabledService,
		},
		{
			name: "No labels on pod template",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{},
						},
					},
				},
			},
			service: dynamicRoutingEnabledSvc,
		},
		{
			name: "Empty pod template labels in object",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"labels": map[string]interface{}{},
							},
						},
					},
				},
			},
			service: dynamicRoutingEnabledSvc,
		},
		{
			name: "No matching subset",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"labels": map[string]interface{}{
									"app": "test",
								},
							},
						},
					},
				},
			},
			service: dynamicRoutingEnabledSvc,
		},
		{
			name: "Matching subset",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"labels": map[string]interface{}{
									"version": "v1",
									"user":    "u1",
									"app":     "test",
								},
							},
						},
					},
				},
			},
			service:          dynamicRoutingEnabledSvc,
			expectedMetadata: map[string]string{constants.ContextDynamicRoutingSubsetName: "v1-u1"},
		},
		{
			name: "Matching default coordinates",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"labels": map[string]interface{}{
									"version": "v1",
									"user":    "u1",
									"app":     "test",
								},
							},
						},
					},
				},
			},
			service:          dynamicRoutingSvcWithDefaultRoutingCoordinates,
			expectedMetadata: map[string]string{constants.ContextDynamicRoutingSubsetName: "v1-u1", constants.ContextDynamicRoutingDefaultSubset: "v1-u1"},
		},
		{
			name: "Non-matching default coordinates",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"labels": map[string]interface{}{
									"version": "v2",
									"user":    "u2",
								},
							},
						},
					},
				},
			},
			service:          dynamicRoutingSvcWithDefaultRoutingCoordinates,
			expectedMetadata: map[string]string{constants.ContextDynamicRoutingSubsetName: "v2-u2"},
		},
		{
			name: "Malformed default coordinates",
			object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"labels": map[string]interface{}{
									"version": "v2",
									"user":    "u2",
								},
							},
						},
					},
				},
			},
			service:          dynamicRoutingSvcWithMalformedDefaultRoutingCoordinates,
			expectedMetadata: map[string]string{constants.ContextDynamicRoutingSubsetName: "v2-u2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			metadata := GetMetadataIfDynamicRoutingEnabled(tc.object, tc.service, logger, prometheus.NewRegistry())
			if !reflect.DeepEqual(metadata, tc.expectedMetadata) {
				t.Errorf("expected %s, but got %s", tc.expectedMetadata, metadata)
			}
		})
	}
}
