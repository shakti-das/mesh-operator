package templating

import (
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	parentClusterName = "clusterName"
	parentNamespace   = "test-namespace"
)

func TestConfigResourceParentMutation(t *testing.T) {
	svc := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.ServiceResource.GroupVersion().String(),
			"kind":       constants.ServiceKind.Kind,
			"metadata": map[string]interface{}{
				"name":      "svc-name",
				"namespace": parentNamespace,
			},
			"spec": map[string]interface{}{},
		},
	}

	se := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.ServiceEntryResource.GroupVersion().String(),
			"kind":       constants.ServiceEntryKind.Kind,
			"metadata": map[string]interface{}{
				"name":      "se-name",
				"namespace": parentNamespace,
			},
			"spec": map[string]interface{}{},
		},
	}

	mop := &v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: parentNamespace,
			Name:      "mop-name",
		},
	}

	testCases := []struct {
		name                string
		configObj           *unstructured.Unstructured
		contextObject       *unstructured.Unstructured
		contextMeshOperator *v1alpha1.MeshOperator
		expectedParent      string
		expectedError       error
	}{
		{
			name:           "Single service rendered with no mop",
			configObj:      createConfigObjWithAnnotations(nil),
			contextObject:  svc,
			expectedParent: getParentStr(svc.GetName(), "Service"),
		},
		{
			name:                "Single service rendered with mop",
			configObj:           createConfigObjWithAnnotations(nil),
			contextObject:       svc,
			contextMeshOperator: mop,
			expectedParent:      getParentStr(svc.GetName(), "Service"),
		},
		{
			name:           "Service entry as parent",
			configObj:      createConfigObjWithAnnotations(nil),
			contextObject:  se,
			expectedParent: getParentStr(se.GetName(), "ServiceEntry"),
		},
		{
			name:           "Multiple owners generating same config resource",
			configObj:      createConfigObjWithAnnotations(map[string]string{constants.ResourceParent: getParentStr(svc.GetName(), "Service")}),
			contextObject:  svc,
			expectedParent: getParentStr(svc.GetName(), "Service"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mutator := &ConfigResourceParentMutator{}

			context := &RenderRequestContext{
				Object:       tc.contextObject,
				MeshOperator: tc.contextMeshOperator,
				ClusterName:  parentClusterName,
			}

			mutatedConfig, err := mutator.Mutate(context, tc.configObj)
			if tc.expectedError == nil {
				assert.Nil(t, err)
				annots := mutatedConfig.GetAnnotations()
				assert.True(t, len(annots) > 0)
				assert.Equal(t, tc.expectedParent, annots[constants.ResourceParent])
			} else {
				assert.NotNil(t, err)
				assert.Equal(t, tc.expectedError, err)
				assert.Nil(t, mutatedConfig)
			}
		})
	}
}

func createConfigObjWithAnnotations(annotations map[string]string) *unstructured.Unstructured {
	filter := kube_test.CreateEnvoyFilter("config-object", "config-namespace", "clusterName1/svc1")
	if annotations == nil {
		annotations = make(map[string]string, 0)
	}
	filter.SetAnnotations(annotations)
	return filter
}

func getParentStr(name string, kind string) string {
	return common.GetResourceParent(parentNamespace, name, kind)
}
