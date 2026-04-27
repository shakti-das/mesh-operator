package patch

import (
	"encoding/json"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestParsePort(t *testing.T) {
	testCases := []struct {
		name          string
		portToParse   interface{}
		expectedValue int
	}{
		{
			name:          "Parse an int port",
			portToParse:   1,
			expectedValue: 1,
		},
		{
			name:          "Parse nil",
			portToParse:   nil,
			expectedValue: invalidPort,
		},
		{
			name:          "Parse float",
			portToParse:   3.14,
			expectedValue: 3,
		},
		{
			name:          "Parse valid string",
			portToParse:   "123",
			expectedValue: 123,
		},
		{
			name:          "Parse invalid string",
			portToParse:   "something",
			expectedValue: invalidPort,
		},
		{
			name:          "Parse int64",
			portToParse:   int64(7),
			expectedValue: 7,
		},
		{
			name:          "Parse unsupported type",
			portToParse:   []int{1},
			expectedValue: invalidPort,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := parsePort(tc.portToParse)
			r := recover()
			assert.Nil(t, r)
			assert.Equal(t, tc.expectedValue, actual)
		})
	}
}

func TestVirtualServiceValidationStrategy_Validate_EmptyMatch(t *testing.T) {
	testCases := []struct {
		name          string
		overlay       map[string]interface{}
		errorExpected bool
		expectedError string
	}{
		{
			name: "Route with empty match array should not panic",
			overlay: map[string]interface{}{
				"spec": map[string]interface{}{
					"http": []interface{}{
						map[string]interface{}{
							"match": []interface{}{},
							"route": []interface{}{
								map[string]interface{}{
									"destination": map[string]interface{}{
										"host": "test-service",
									},
								},
							},
						},
					},
				},
			},
			errorExpected: true,
			expectedError: "only exactly one route match allowed per route override. route: 0",
		},
		{
			name: "Route with valid match should pass",
			overlay: map[string]interface{}{
				"spec": map[string]interface{}{
					"http": []interface{}{
						map[string]interface{}{
							"match": []interface{}{
								map[string]interface{}{
									"uri": map[string]interface{}{
										"prefix": "/api",
									},
								},
							},
							"route": []interface{}{
								map[string]interface{}{
									"destination": map[string]interface{}{
										"host": "test-service",
									},
								},
							},
						},
					},
				},
			},
			errorExpected: false,
		},
	}

	vsValidationStrategy := virtualServiceValidationStrategy{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			overlayBytes, err := json.Marshal(tc.overlay)
			assert.NoError(t, err)

			overlay := v1alpha1.Overlay{
				StrategicMergePatch: runtime.RawExtension{
					Raw: overlayBytes,
				},
			}

			err = vsValidationStrategy.Validate(&overlay)

			if tc.errorExpected {
				assert.NotNil(t, err)
				assert.Equal(t, tc.expectedError, err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
