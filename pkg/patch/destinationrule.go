package patch

import (
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
)

type destinationRulePatchStrategy struct {
	objects []*unstructured.Unstructured
}

type destinationRuleValidationStrategy struct{}

func (v destinationRulePatchStrategy) CanApply(patch *v1alpha1.Overlay) bool {
	return patch.Kind == constants.DestinationRuleKind.Kind
}

func (v *destinationRulePatchStrategy) Patch(patch *v1alpha1.Overlay) (map[string]bool, error) {
	overlayObj, err := OverlayToObject(patch)
	if err != nil {
		return nil, err
	}

	// removes host field from patch if present
	unstructured.RemoveNestedField(overlayObj, "spec", "host")

	// if consistentHash is enabled, localityLbSetting and simple will be turned off automatically
	_, consistentHashEnabled, err := unstructured.NestedMap(overlayObj, "spec", "trafficPolicy", "loadBalancer", "consistentHash")
	if consistentHashEnabled {
		unstructured.SetNestedMap(overlayObj, map[string]interface{}{"enabled": false}, "spec", "trafficPolicy", "loadBalancer", "localityLbSetting")
		unstructured.SetNestedField(overlayObj, nil, "spec", "trafficPolicy", "loadBalancer", "simple")
	}

	resultObjects := []*unstructured.Unstructured{}
	appliedTo := map[string]bool{}
	for _, obj := range v.objects {
		if patch.Name == "" || patch.Name == obj.GetName() {
			_, originalHasHost, hostErr := unstructured.NestedString(obj.Object, "spec", "host")
			if hostErr != nil {
				return nil, hostErr
			}
			if originalHasHost {
				result, mergePatchErr := strategicpatch.StrategicMergeMapPatchUsingLookupPatchMeta(
					obj.Object,
					overlayObj,
					newDestinationRuleMetaAdapter(overlayObj))
				if mergePatchErr != nil {
					return nil, mergePatchErr
				}
				resultObjects = append(resultObjects, &unstructured.Unstructured{Object: result})
				appliedTo[fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName())] = true
			} else {
				resultObjects = append(resultObjects, obj)
			}
		} else {
			resultObjects = append(resultObjects, obj)
		}
	}
	v.objects = resultObjects
	return appliedTo, nil
}

func (v destinationRulePatchStrategy) GetObjects() []*unstructured.Unstructured {
	return v.objects
}

func (v *destinationRuleValidationStrategy) Validate(_ *v1alpha1.Overlay) error {
	return nil
}

func newDestinationRuleMetaAdapter(patchMap map[string]interface{}) strategicpatch.LookupPatchMeta {
	return &dynamicSchemaMetaAdapter{
		path:           "",
		currentNode:    patchMap,
		objectStrategy: DefaultObjectStrategy,
		listStrategy:   destinationRuleListStrategy,
	}
}

func destinationRuleListStrategy(currentNode interface{}, path string) strategicpatch.PatchMeta {
	// DestinationRule specifics should go there

	return DefaultListStrategy(currentNode, path)
}
