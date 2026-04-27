package patch

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/stretchr/testify/assert"
)

func TestInsertUriInRoute(t *testing.T) {
	u := &uriHandler{
		overlayUri: "test",
	}

	testCases := []struct {
		name              string
		newRouteData      map[string]interface{}
		portNumber        int
		expectedRouteData map[string]interface{}
		expectedError     error
	}{
		{
			name:         "InsertUriWithPort",
			newRouteData: map[string]interface{}{},
			portNumber:   8080,
			expectedRouteData: map[string]interface{}{
				"match": []interface{}{
					map[string]interface{}{
						"uri":  "test",
						"port": int64(8080),
					},
				},
			},
			expectedError: nil,
		},
		{
			name:         "InsertUriWithoutPort",
			newRouteData: map[string]interface{}{},
			portNumber:   -1,
			expectedRouteData: map[string]interface{}{
				"match": []interface{}{
					map[string]interface{}{
						"uri": "test",
					},
				},
			},
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := u.insertUriInRoute(tc.newRouteData, tc.portNumber)

			assert.Nil(t, err)

			if !reflect.DeepEqual(tc.newRouteData, tc.expectedRouteData) {
				t.Errorf("Unexpected result. Expected: %#v, Got: %#v", tc.expectedRouteData, tc.newRouteData)
			}
		})
	}
}

func TestUriMatchExistInDelegateVS(t *testing.T) {
	u := &uriHandler{
		overlayUri: "test",
	}

	testCases := []struct {
		name           string
		vsData         map[string]interface{}
		expectedResult bool
		expectedError  error
	}{
		{
			name: "UriMatchExists",
			vsData: map[string]interface{}{
				"spec": map[string]interface{}{
					"http": []interface{}{
						map[string]interface{}{
							"match": []interface{}{
								map[string]interface{}{
									"uri": "test",
								},
							},
						},
					},
				},
			},
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name: "UriMatchDoesNotExist",
			vsData: map[string]interface{}{
				"spec": map[string]interface{}{
					"http": []interface{}{
						map[string]interface{}{
							"match": []interface{}{
								map[string]interface{}{
									"uri": "example",
								},
							},
						},
					},
				},
			},
			expectedResult: false,
			expectedError:  nil,
		},
		{
			name: "UriMatchDoesNotExist (No match block in base route)",
			vsData: map[string]interface{}{
				"spec": map[string]interface{}{
					"http": []interface{}{
						map[string]interface{}{
							"name": "some-route",
						},
					},
				},
			},
			expectedResult: false,
			expectedError:  nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vs := &unstructured.Unstructured{Object: tc.vsData}

			result, err := u.uriMatchExistInDelegateVS(vs)

			assert.Nil(t, err)
			assert.Equal(t, tc.expectedResult, result)
		})
	}
}

func TestCheckUriMatchExists(t *testing.T) {
	u := &uriHandler{
		overlayUri: "test",
	}

	testCases := []struct {
		name          string
		rootVS        bool
		baseRouteData map[string]interface{}
		expected      bool
		expectedError error
	}{
		{
			name:   "RootVS - NoUriMatch",
			rootVS: true,
			baseRouteData: map[string]interface{}{
				"match": []interface{}{
					map[string]interface{}{
						"uri":  "something else",
						"port": "80",
					},
				},
			},
			expected:      false,
			expectedError: nil,
		},
		{
			name:   "RootVSRouteWithPort - UriMatchExist",
			rootVS: true,
			baseRouteData: map[string]interface{}{
				"match": []interface{}{
					map[string]interface{}{
						"uri":  "test",
						"port": "80",
					},
				},
			},
			expected:      true,
			expectedError: nil,
		},
		{
			name:   "RootVSMatchWithoutPort - UriMatchExist",
			rootVS: true,
			baseRouteData: map[string]interface{}{
				"match": []interface{}{
					map[string]interface{}{
						"uri": "test",
					},
				},
			},
			expected:      true,
			expectedError: nil,
		},
		{
			name:   "DelegateVsUriMatch",
			rootVS: false,
			baseRouteData: map[string]interface{}{
				"match": []interface{}{
					map[string]interface{}{
						"uri": "test",
					},
				},
			},
			expected:      true,
			expectedError: nil,
		},
		{
			name:   "DelegateVsUriMismatch",
			rootVS: false,
			baseRouteData: map[string]interface{}{
				"match": []interface{}{
					map[string]interface{}{
						"uri": "something",
					},
				},
			},
			expected:      false,
			expectedError: nil,
		},
		{
			name:   "DelegateVsUriMismatch - UriWithAnotherField",
			rootVS: false,
			baseRouteData: map[string]interface{}{
				"match": []interface{}{
					map[string]interface{}{
						"uri":  "test",
						"test": "test1",
					},
				},
			},
			expected:      false,
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := u.checkUriMatchExists(tc.rootVS, tc.baseRouteData)
			if (err != nil && tc.expectedError == nil) || (err == nil && tc.expectedError != nil) {
				t.Errorf("Unexpected error. Expected: %v, Got: %v", tc.expectedError, err)
			}

			assert.Equal(t, tc.expected, result)
		})
	}
}
