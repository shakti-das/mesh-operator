package patch

import (
	"fmt"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
)

const SyntheticMergeKey = "$syntheticKey"

type PatchStrategy interface {
	CanApply(patch *v1alpha1.Overlay) bool
	Patch(patch *v1alpha1.Overlay) (map[string]bool, error)
	GetObjects() []*unstructured.Unstructured
}

type ValidationStrategy interface {
	Validate(patch *v1alpha1.Overlay) error
}

type patchStrategyFactory func([]*unstructured.Unstructured) PatchStrategy

type defaultValidator struct{}

var (
	patchStrategyByKind = map[string]patchStrategyFactory{
		"VirtualService": func(o []*unstructured.Unstructured) PatchStrategy {
			return &virtualServicePatchStrategy{objects: o}
		},
		"DestinationRule": func(o []*unstructured.Unstructured) PatchStrategy {
			return &destinationRulePatchStrategy{objects: o}
		},
	}
	defaultStrategy = func(o []*unstructured.Unstructured) PatchStrategy {
		return defaultPatchStrategy{objects: o}
	}

	validationStrategyByKind = map[string]ValidationStrategy{
		"VirtualService":  &virtualServiceValidationStrategy{},
		"DestinationRule": &destinationRuleValidationStrategy{},
	}
	defaultValidationStrategy = &defaultValidator{}
)

func GetStrategyForKindOrDefault(kind string, objects []*unstructured.Unstructured) PatchStrategy {
	patchStrategy, exists := patchStrategyByKind[kind]
	if !exists {
		patchStrategy = defaultStrategy
	}
	var objectCopies []*unstructured.Unstructured
	for _, obj := range objects {
		objectCopies = append(objectCopies, obj.DeepCopy())
	}
	return patchStrategy(objectCopies)
}

func GetValidationStrategyForKindOrDefault(kind string) ValidationStrategy {
	validationStrategy, exists := validationStrategyByKind[kind]
	if !exists {
		validationStrategy = defaultValidationStrategy
	}
	return validationStrategy
}

// --- Meta adapters

type defaultPatchStrategy struct {
	objects []*unstructured.Unstructured
}

func (d defaultPatchStrategy) CanApply(_ *v1alpha1.Overlay) bool {
	return true
}

func (d defaultPatchStrategy) Patch(patch *v1alpha1.Overlay) (map[string]bool, error) {
	resultObjects := []*unstructured.Unstructured{}
	appliedTo := map[string]bool{}
	for _, obj := range d.objects {
		if (patch.Kind == "" || obj.GetKind() == patch.Kind) &&
			(patch.Name == "" || obj.GetName() == patch.Name) {
			patchedObject, err := d.applyPatchToObject(obj, patch)
			if err != nil {
				return nil, fmt.Errorf("failed to apply patch to object: %s, %v", obj.GetName(), err)
			}
			resultObjects = append(resultObjects, patchedObject)
			appliedTo[fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName())] = true
		} else {
			resultObjects = append(resultObjects, obj)
		}
	}

	d.objects = resultObjects
	return appliedTo, nil
}

func (d defaultPatchStrategy) GetObjects() []*unstructured.Unstructured {
	return d.objects
}

func (d defaultPatchStrategy) applyPatchToObject(original *unstructured.Unstructured, patch *v1alpha1.Overlay) (*unstructured.Unstructured, error) {
	overlayMap, err := OverlayToObject(patch)
	defaultMetaAdapter := &dynamicSchemaMetaAdapter{
		path:           "",
		currentNode:    patch,
		objectStrategy: DefaultObjectStrategy,
		listStrategy:   DefaultListStrategy,
	}
	result, err := strategicpatch.StrategicMergeMapPatchUsingLookupPatchMeta(original.Object, overlayMap, defaultMetaAdapter)

	return &unstructured.Unstructured{Object: result}, err

}

func (v *defaultValidator) Validate(_ *v1alpha1.Overlay) error {
	return nil
}

func DefaultObjectStrategy(interface{}, string) strategicpatch.PatchMeta {
	return defaultMerge
}

func DefaultListStrategy(currentNode interface{}, _ string) strategicpatch.PatchMeta {
	// We don't know the value, so fallback to default: merge-by-name
	if currentNode == nil {
		return mergeByNameMeta
	}

	// It's a slice, we need to check one of its values
	slice, isSlice := currentNode.([]interface{})

	if isSlice {

		if len(slice) == 0 {
			return defaultMerge
		}

		_, isObject := slice[0].(map[string]interface{})

		// currentNode is a primitive slice, use normal merge
		if !isObject {
			return defaultMerge
		}

		// If current node slice has objects, use replace unless name field is present
		for _, obj := range slice {
			sliceElemObj, isObj := obj.(map[string]interface{})
			if !isObj {
				// this shouldn't happen; we're expecting all members of the slice to be the same kind.
				return mergeByNameMeta
			}
			if _, nameExists := sliceElemObj["name"]; !nameExists {
				// if any slice element object doesn't contain name, use replace strategy
				return defaultReplace
			}
		}
	}

	// Default: merge-by-name
	return mergeByNameMeta
}

type StrategyHandler func(currentNode interface{}, path string) strategicpatch.PatchMeta

// hackSpecs - an internal struct that we use as a source of the field metadata for various merge strategies.
type hackSpecs struct {
	DefaultMerge        []interface{} `patchStrategy:"merge"`
	MergeByName         []interface{} `patchStrategy:"merge" patchMergeKey:"name"`
	MergeBySyntheticKey []interface{} `patchStrategy:"merge" patchMergeKey:"$syntheticKey"`
	DefaultReplace      []interface{} `patchStrategy:"replace"`
}

var (
	structMeta, _ = strategicpatch.NewPatchMetaFromStruct(new(hackSpecs))

	// defaultMerge - a piece of metadata representing merge strategy. Cached to avoid making extra reflection calls.
	_, defaultMerge, _ = structMeta.LookupPatchMetadataForStruct("DefaultMerge")
	// defaultReplace - a piece of metadata representing replace strategy. Cached to avoid making extra reflection calls.
	_, defaultReplace, _ = structMeta.LookupPatchMetadataForStruct("DefaultReplace")
	// mergeByNameStrategy - a piece of metadata representing merge-by-name strategy. Cached to avoid making extra reflection calls.
	_, mergeByNameMeta, _ = structMeta.LookupPatchMetadataForStruct("MergeByName")
	// mergeBySyntheticKeyMeta - a piece of metadata representing merge-by-synthetic-key strategy. Cached to avoid making extra reflection calls.
	_, mergeBySyntheticKeyMeta, _ = structMeta.LookupPatchMetadataForStruct("MergeBySyntheticKey")
)

// dynamicSchemaMetaAdapter - an internal class that represents a specific node during merge process
type dynamicSchemaMetaAdapter struct {
	// path - path of this node, for example /spec/gateways or /spec/hosts
	path string
	// currentNode - value at which current node is pointing to.
	// This is needed to dynamically determine the patch strategy, such as merge-by-name for object lists and merge for scalar lists.
	currentNode interface{}
	// objectStrategy - kind specific delegate to determine patch strategy for a object property
	objectStrategy StrategyHandler
	// objectStrategy - kind specific delegate to determine patch strategy for a list property
	listStrategy StrategyHandler
}

// getSubNode - create an adapter for a sub-node in a merge patch.
// Carries over the kind and cached strategies from the parent.
// Maintains the path of the node.
func (me *dynamicSchemaMetaAdapter) getSubNode(key string) dynamicSchemaMetaAdapter {
	return dynamicSchemaMetaAdapter{
		path:           me.path + "/" + key,
		currentNode:    me.getSubNodeValueIfExists(key),
		objectStrategy: me.objectStrategy,
		listStrategy:   me.listStrategy,
	}
}

func (me *dynamicSchemaMetaAdapter) getSubNodeValueIfExists(key string) interface{} {
	var currNodeMap map[string]interface{}

	currentNodeAsMap, ok := me.currentNode.(map[string]interface{})
	if ok {
		currNodeMap = currentNodeAsMap
	} else {
		currentNodeAsSlice, isSlice := me.currentNode.([]interface{})
		if isSlice && len(currentNodeAsSlice) > 0 {
			elemAsMap, elemOk := currentNodeAsSlice[0].(map[string]interface{})
			if !elemOk {
				return nil
			}
			currNodeMap = elemAsMap
		}
	}

	if currNodeMap != nil {
		nodeByName, found, err := unstructured.NestedFieldNoCopy(currNodeMap, key)
		if err != nil || !found {
			return nil
		}
		return nodeByName
	}

	return nil
}

// getObjectStrategy - return a strategy used for merging objects in the path. Merge - by default.
func (me *dynamicSchemaMetaAdapter) getObjectStrategy() strategicpatch.PatchMeta {
	return me.objectStrategy(me.currentNode, me.path)
}

// getListStrategy - return a strategy used for merging lists. Merge-by-name - by default.
func (me *dynamicSchemaMetaAdapter) getListStrategy() strategicpatch.PatchMeta {
	return me.listStrategy(me.currentNode, me.path)
}

func (me *dynamicSchemaMetaAdapter) LookupPatchMetadataForStruct(key string) (strategicpatch.LookupPatchMeta, strategicpatch.PatchMeta, error) {
	subPatchMeta := me.getSubNode(key)
	return &subPatchMeta, subPatchMeta.getObjectStrategy(), nil
}

func (me *dynamicSchemaMetaAdapter) LookupPatchMetadataForSlice(key string) (strategicpatch.LookupPatchMeta, strategicpatch.PatchMeta, error) {
	subPatchMeta := me.getSubNode(key)
	return &subPatchMeta, subPatchMeta.getListStrategy(), nil
}

func (me *dynamicSchemaMetaAdapter) Name() string {
	return me.path
}

func OverlayToObject(overlay *v1alpha1.Overlay) (map[string]interface{}, error) {
	var err error

	overlayObj := &unstructured.Unstructured{}

	if err = overlayObj.UnmarshalJSON(overlay.StrategicMergePatch.Raw); err != nil {
		if !runtime.IsMissingKind(err) {
			return nil, err
		}
	}

	return overlayObj.Object, nil
}
