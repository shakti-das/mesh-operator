package patch

import (
	"encoding/json"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
)

func TestDefaultListStrategy(t *testing.T) {
	testCases := []struct {
		name             string
		currentNode      interface{}
		expectedStrategy strategicpatch.PatchMeta
	}{
		{
			name:             "currentNode is nil",
			expectedStrategy: mergeByNameMeta,
		},
		{
			name:             "empty slice",
			currentNode:      []interface{}{},
			expectedStrategy: defaultMerge,
		},
		{
			name:             "currentNode is a slice of primitive types",
			currentNode:      []interface{}{"a", "b", "c"},
			expectedStrategy: defaultMerge,
		},
		{
			name:             "currentNode is a slice of objects not containing name",
			currentNode:      []interface{}{map[string]interface{}{"attr": "abc"}, map[string]interface{}{"attr": struct{}{}}},
			expectedStrategy: defaultReplace,
		},
		{
			name:             "currentNode is a slice of objects that include name",
			currentNode:      []interface{}{map[string]interface{}{"name": "abc"}, map[string]interface{}{"name": struct{}{}}},
			expectedStrategy: mergeByNameMeta,
		},
		{
			name: "currentNode is not a slice",
			currentNode: struct {
				name  string
				index int
			}{name: "blah", index: 1},
			expectedStrategy: mergeByNameMeta,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := DefaultListStrategy(tc.currentNode, "unused")
			assert.Equal(t, tc.expectedStrategy, actual)
		})
	}
}

func TestGetSubNodeValueIfExists(t *testing.T) {
	var (
		routeObj = []interface{}{
			map[string]interface{}{
				"destination": map[string]interface{}{
					"host": "route.1.host",
				},
			},
		}
		httpObj = []interface{}{
			map[string]interface{}{
				"name":  "test-route-1",
				"route": routeObj,
			},
		}
		specObj = map[string]interface{}{
			"http": httpObj,
		}
		overlayObj = map[string]interface{}{
			"spec": specObj,
		}
		overlayBytes, _ = json.Marshal(overlayObj)
		overlay, _      = OverlayToObject(&v1alpha1.Overlay{
			Kind: "VirtualService",
			Name: "test-overlay",
			StrategicMergePatch: runtime.RawExtension{
				Raw: overlayBytes,
			},
		})
	)

	testCases := []struct {
		name            string
		key             string
		currentNode     interface{}
		expectedSubNode interface{}
	}{
		{
			name:            "path is empty",
			key:             "spec",
			currentNode:     overlay,
			expectedSubNode: specObj,
		},
		{
			name:            "path is spec/http",
			key:             "http",
			currentNode:     specObj,
			expectedSubNode: httpObj,
		},
		{
			name:            "path is spec/http/route",
			key:             "route",
			currentNode:     httpObj,
			expectedSubNode: routeObj,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &dynamicSchemaMetaAdapter{
				path:           "",
				currentNode:    tc.currentNode,
				objectStrategy: DefaultObjectStrategy,
				listStrategy:   virtualServiceListStrategy,
			}

			actual := adapter.getSubNodeValueIfExists(tc.key)
			assert.Equal(t, tc.expectedSubNode, actual)
		})
	}
}
