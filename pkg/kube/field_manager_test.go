package kube

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestOcmFieldManager_RetainUnmanaged(t *testing.T) {
	// Create test objects
	existingObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      "test-vs",
				"namespace": "test-ns",
			},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"name": "route-1",
						"route": []interface{}{
							map[string]interface{}{
								"destination": map[string]interface{}{
									"host":   "service-a",
									"subset": "v1",
								},
								"weight": int64(90),
							},
						},
					},
				},
			},
		},
	}

	// Set different route weight in the generated object
	generatedObject := existingObj.DeepCopy()
	_ = unstructured.SetNestedField(generatedObject.Object, int64(90), "spec", "http", "0", "route", "0", "weight")

	ocmManager := &OcmFieldManager{}

	originalGeneratedObject := generatedObject.DeepCopy()
	err := ocmManager.RetainUnmanaged(existingObj, generatedObject.DeepCopy())

	assert.NoError(t, err)
	assert.Equal(t, originalGeneratedObject, generatedObject)
}
