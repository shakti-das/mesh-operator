package patch

import (
	"fmt"
	"sort"
	"strconv"

	meshOpErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
)

const (
	invalidPort int = -1
)

var (
	RouteTypes = []string{"http", "tls", "tcp"}
)

type virtualServicePatchStrategy struct {
	objects []*unstructured.Unstructured
}

func (v virtualServicePatchStrategy) GetObjects() []*unstructured.Unstructured {
	return v.objects
}

type virtualServiceValidationStrategy struct{}

func (v virtualServicePatchStrategy) CanApply(patch *v1alpha1.Overlay) bool {
	return patch.Kind == constants.VirtualServiceKind.Kind
}

func sortVSObjects(objects []*unstructured.Unstructured) {
	sort.Slice(objects, func(i, j int) bool {
		for _, routeType := range RouteTypes {
			routes, found, _ := unstructured.NestedSlice(objects[i].Object, "spec", routeType)
			if !found {
				continue
			}
			for _, route := range routes {
				routeAsMap, valid := route.(map[string]interface{})
				if !valid {
					continue
				}
				_, delegateExists, _ := unstructured.NestedString(routeAsMap, "delegate", "name")
				if delegateExists {
					return true
				}
			}
		}
		return false
	})
}

func (v *virtualServicePatchStrategy) Patch(patch *v1alpha1.Overlay) (map[string]bool, error) {
	baseVirtualServices := v.GetObjects()

	overlay := patch.DeepCopy()
	uriBasedInjection, overlayUri := isUriBasedInjection(overlay)

	sortVSObjects(baseVirtualServices)

	if uriBasedInjection {
		return v.PatchByUri(overlayUri, overlay, baseVirtualServices)
	}
	return v.PatchByRouteNameOrPort(overlay, baseVirtualServices)
}

func (v *virtualServicePatchStrategy) PatchByUri(uri interface{}, overlay *v1alpha1.Overlay, baseVirtualServices []*unstructured.Unstructured) (map[string]bool, error) {
	var err error
	overlaidVS := make(map[string]bool)

	overlayMatchKeys, overlayKeyErr := buildOverlayKeys(overlay)
	if overlayKeyErr != nil {
		return nil, overlayKeyErr
	}

	vsMatchKeys, matchKeyErr := buildVsMatchKeys(overlay)
	if err != nil {
		return nil, matchKeyErr
	}

	uriHandlerObj := NewUriHandler(uri)

	for _, baseVS := range baseVirtualServices {
		rootVs, rootVsErr := isRootVs(baseVS)
		if rootVsErr != nil {
			return nil, rootVsErr
		}
		if rootVs {
			if len(vsMatchKeys) == 0 || containsMatchKey(vsMatchKeys, []string{"", fmt.Sprintf("%s", baseVS.GetName())}) {
				injectionApplied, delegateKeys, injectionErr := uriHandlerObj.applyUriInjectionInRootVs(baseVS, overlay, overlayMatchKeys, overlaidVS)
				if injectionErr != nil {
					return nil, injectionErr
				}
				if len(delegateKeys) > 0 {
					vsMatchKeys = append(vsMatchKeys, delegateKeys...)
				}
				if injectionApplied {
					overlaidVS[baseVS.GetName()] = true
				}
			}
		} else {
			// apply uri injection only to relevant port delegates
			if containsMatchKey(vsMatchKeys, []string{"", fmt.Sprintf("%s", baseVS.GetName())}) {
				injectionErr := uriHandlerObj.applyUriInjectionInDelegateVs(baseVS, overlay, overlaidVS)
				if injectionErr != nil {
					return nil, injectionErr
				}
				overlaidVS[baseVS.GetName()] = true
			}
		}
	}
	return overlaidVS, nil
}

func (v *virtualServicePatchStrategy) PatchByRouteNameOrPort(overlay *v1alpha1.Overlay, baseVirtualServices []*unstructured.Unstructured) (map[string]bool, error) {
	var err error
	overlaidVS := make(map[string]bool)

	overlayMatchKeys, overlayKeyErr := buildOverlayKeys(overlay)
	if overlayKeyErr != nil {
		return nil, overlayKeyErr
	}

	for _, baseVS := range baseVirtualServices {
		var overlaysToApply []map[string]interface{}
		if containsMatchKey(overlayMatchKeys, []string{baseVS.GetName(), ""}) {
			cleanedOverlay := getCleanedOverlay(overlay)
			overlaysToApply = append(overlaysToApply, cleanedOverlay)
		} else {
			for _, routeType := range RouteTypes {
				if routes, found, _ := unstructured.NestedSlice(baseVS.Object, "spec", routeType); found {
					for index, route := range routes {
						if routeAsMap, valid := route.(map[string]interface{}); valid {
							routeKeys, routeKeyErr := buildRouteKeys(routeAsMap, baseVS.GetName(), routeType, false)
							if routeKeyErr != nil {
								// TODO log this somewhere?
								continue
							}

							keyMatch := containsMatchKey(overlayMatchKeys, routeKeys)

							if keyMatch {
								delegateName, delegateExists, _ := unstructured.NestedFieldNoCopy(routeAsMap, "delegate", "name")
								if delegateExists {
									overlayMatchKeys = append(overlayMatchKeys, fmt.Sprintf("%s/%s//", delegateName, routeType))
								} else {
									syntheticKey := generateSyntheticKey(baseVS.GetName(), index)
									err = insertSyntheticKey(baseVS.Object, routeType, routes, routeAsMap, syntheticKey)

									cleanedOverlay := getCleanedOverlay(overlay)
									overlayToApply, _ := preProcessOverlay(cleanedOverlay, routeType, syntheticKey)
									overlaysToApply = append(overlaysToApply, overlayToApply)
								}
							} else {
								// Insert none matching key for partially matching overlay to some routes on baseVS.
								// Index at the end is needed to make sure that the k8s strategic patch sort function doesn't reorder elements with the same merge key.
								err = insertSyntheticKey(baseVS.Object, routeType, routes, routeAsMap, fmt.Sprintf("no-match-found-%d", index))
							}
						}
					}
				}
			}
		}
		if len(overlaysToApply) > 0 {
			overlaidVS, err = applyOverlays(overlaysToApply, baseVS, overlaidVS)
			if err != nil {
				return overlaidVS, err
			}
		}
		postProcess(baseVS.Object)
	}
	v.objects = baseVirtualServices
	return overlaidVS, nil
}

func isRootVs(vs *unstructured.Unstructured) (bool, error) {
	_, hasHosts, hostErr := unstructured.NestedSlice(vs.Object, "spec", "hosts")
	if hostErr != nil {
		return false, hostErr
	}
	return hasHosts, nil
}

// TODO we're eating errors here, at least log these?
func getCleanedOverlay(overlay *v1alpha1.Overlay) map[string]interface{} {
	overlayObj, _ := OverlayToObject(overlay)
	cleanedObj, _ := removeNameAndMatchFromOverlay(overlayObj)
	return cleanedObj
}

func buildOverlayKeys(overlay *v1alpha1.Overlay) ([]string, error) {
	overlayObj, _ := OverlayToObject(overlay)
	matchKey := overlay.Name
	for _, routeType := range RouteTypes {
		routes, found, _ := unstructured.NestedSlice(overlayObj, "spec", routeType)
		if !found {
			continue
		}
		routeAsMap, valid := routes[0].(map[string]interface{})
		if !valid {
			continue
		}
		routeName, _, _ := unstructured.NestedString(routeAsMap, "name")
		matchRequest, routeMatchRequestExists, _ := unstructured.NestedSlice(routeAsMap, "match")
		var port interface{}
		var hasUri bool
		if routeMatchRequestExists {
			port, _, _ = unstructured.NestedFieldNoCopy(matchRequest[0].(map[string]interface{}), "port")
			_, hasUri, _ = unstructured.NestedFieldNoCopy(matchRequest[0].(map[string]interface{}), "uri")
		}

		// for match by uri use case - ignore route name
		if hasUri {
			matchKey += fmt.Sprintf("/%s//", routeType)
		} else {
			matchKey += fmt.Sprintf("/%s/%s/", routeType, routeName)
		}

		if port != nil {
			p := parsePort(port)
			if invalidPort == p {
				return nil, fmt.Errorf("invalid port %v found in overlay", port)
			}
			matchKey += fmt.Sprintf("%d", p)
		}
	}
	return []string{matchKey}, nil
}

func removeNameAndMatchFromOverlay(overlay map[string]interface{}) (map[string]interface{}, error) {
	var err error

	for _, routeType := range RouteTypes {
		routes, found, _ := unstructured.NestedSlice(overlay, "spec", routeType)
		if found {
			routeAsMap, valid := routes[0].(map[string]interface{})
			if !valid {
				continue
			}
			// removes match to avoid overriding match request on baseVS
			unstructured.RemoveNestedField(routeAsMap, "match")
			// removes name to adding name to delegateVS
			unstructured.RemoveNestedField(routeAsMap, "name")
			err = unstructured.SetNestedSlice(overlay, routes, "spec", routeType)
		}
	}

	return overlay, err
}

func buildRouteKeys(routeAsMap map[string]interface{}, baseVSName string, routeType string, uriBasedInjection bool) ([]string, error) {
	var routeKeys []string

	// vs-name/http + /http
	routeKeys = append(routeKeys, fmt.Sprintf("/%s//", routeType))
	routeKeys = append(routeKeys, fmt.Sprintf("%s/%s//", baseVSName, routeType))

	routeName, routeNameExists, _ := unstructured.NestedString(routeAsMap, "name")
	// vs-name/http/name + /http/name
	if !uriBasedInjection && routeNameExists {
		routeKeys = append(routeKeys, fmt.Sprintf("/%s/%s/", routeType, routeName))
		routeKeys = append(routeKeys, fmt.Sprintf("%s/%s/%s/", baseVSName, routeType, routeName))
	}

	matchRequest, matchRequestExists, err := unstructured.NestedSlice(routeAsMap, "match")
	if err != nil {
		return routeKeys, err
	}

	if matchRequestExists {
		for _, matchAttr := range matchRequest {
			if port, portExists, _ := unstructured.NestedFieldNoCopy(matchAttr.(map[string]interface{}), "port"); portExists {
				parsedPort := parsePort(port)
				if parsedPort == invalidPort {
					return nil, fmt.Errorf("invalid port %v found in overlay", port)
				}

				// vs-name/http/port + /http/port
				routeKeys = append(routeKeys, fmt.Sprintf("/%s//%d", routeType, parsedPort))
				routeKeys = append(routeKeys, fmt.Sprintf("%s/%s//%d", baseVSName, routeType, parsedPort))

				// vs-name/http/name&&port + /http/name&&port
				if !uriBasedInjection && routeNameExists {
					routeKeys = append(routeKeys, fmt.Sprintf("/%s/%s/%d", routeType, routeName, parsedPort))
					routeKeys = append(routeKeys, fmt.Sprintf("%s/%s/%s/%d", baseVSName, routeType, routeName, parsedPort))
				}
			}
		}
	}

	return routeKeys, nil
}

func containsMatchKey(keyList1 []string, keyList2 []string) bool {
	for _, key := range keyList1 {
		for _, matchKey := range keyList2 {
			if key == matchKey {
				return true
			}
		}
	}
	return false
}

func preProcessOverlay(obj map[string]interface{}, routeType string, syntheticKey string) (map[string]interface{}, error) {
	var err error
	overlayRoutes, routeExists, _ := unstructured.NestedSlice(obj, "spec", routeType)
	if routeExists {
		overlayRoute, _ := overlayRoutes[0].(map[string]interface{})
		err = insertSyntheticKey(obj, routeType, overlayRoutes, overlayRoute, syntheticKey)
	}
	return obj, err
}

func applyOverlays(overlaysToApply []map[string]interface{}, baseVS *unstructured.Unstructured, overlaidVS map[string]bool) (map[string]bool, error) {
	for _, overlayToApply := range overlaysToApply {
		_, err := apply(baseVS, overlayToApply)
		if err != nil {
			return nil, err
		}
		overlaidVS[baseVS.GetName()] = true
	}
	return overlaidVS, nil
}

func apply(baseVS *unstructured.Unstructured, overlay map[string]interface{}) (strategicpatch.JSONMap, error) {
	result, err := strategicpatch.StrategicMergeMapPatchUsingLookupPatchMeta(
		baseVS.Object,
		overlay,
		newVirtualServiceMetaAdapter(overlay))

	return result, err
}

func postProcess(object map[string]interface{}) (*unstructured.Unstructured, error) {
	for _, routeType := range RouteTypes {
		routes, exists, err := unstructured.NestedSlice(object, "spec", routeType)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		for _, route := range routes {
			var routeAsMap = route.(map[string]interface{})
			unstructured.RemoveNestedField(routeAsMap, SyntheticMergeKey)
		}
		err = unstructured.SetNestedSlice(object, routes, "spec", routeType)
		if err != nil {
			return nil, err
		}
	}
	return &unstructured.Unstructured{Object: object}, nil
}

func insertSyntheticKey(objToProcess map[string]interface{}, routeType string, routes []interface{}, currentRoute map[string]interface{}, keyValue string) error {
	err := unstructured.SetNestedField(currentRoute, keyValue, SyntheticMergeKey)
	if err != nil {
		return err
	}
	err = unstructured.SetNestedSlice(objToProcess, routes, "spec", routeType)
	return err
}

func (v *virtualServiceValidationStrategy) Validate(patch *v1alpha1.Overlay) error {
	overlay, err := OverlayToObject(patch)
	if err != nil {
		return err
	}
	spec, specExists, err := unstructured.NestedMap(overlay, "spec")
	if err != nil {
		return err
	}

	// Check if overlay is trying to patch a mix of routes or properties
	if specExists {
		if len(overlay) > 1 {
			// mixing spec and non-spec overrides
			return &meshOpErrors.UserConfigError{Message: "mixing metadata and spec overrides not allowed in the same overlay"}
		}
		if len(spec) > 1 {
			// mixing gateway/route-type overrides
			return &meshOpErrors.UserConfigError{Message: "mixing route types and/or non-route overrides not allowed in the same overlay"}
		}
		for _, routeType := range RouteTypes {
			routes, routeTypeExists, err := unstructured.NestedSlice(spec, routeType)
			if err != nil {
				return err
			}
			if !routeTypeExists {
				continue
			}
			if len(routes) > 1 {
				// one route per overlay
				return &meshOpErrors.UserConfigError{Message: "only one route can be overridden per overlay"}
			}
			for routeIdx, route := range routes {
				routeAsMap, valid := route.(map[string]interface{})
				if !valid {
					return &meshOpErrors.UserConfigError{Message: fmt.Sprintf("route %d is not valid", routeIdx)}
				}
				routeMatch, matchExists, err := unstructured.NestedSlice(routeAsMap, "match")
				if err != nil {
					return err
				}
				if !matchExists {
					continue
				}
				if len(routeMatch) != 1 {
					// one match per route
					return &meshOpErrors.UserConfigError{Message: fmt.Sprintf("only exactly one route match allowed per route override. route: %d", routeIdx)}
				}
				routeMatchObject, validRouteMatchObject := routeMatch[0].(map[string]interface{})
				if !validRouteMatchObject {
					return &meshOpErrors.UserConfigError{Message: "invalid match object, please make sure it follows Istio VirtualService spec"}
				}
				_, portExists := routeMatchObject["port"]
				_, uriExists := routeMatchObject["uri"]

				// eligible match condition: 1. port + uri; 2. either port OR uri present
				if (portExists && uriExists && len(routeMatchObject) > 2) ||
					(len(routeMatchObject) > 1 && !(portExists && uriExists)) ||
					(len(routeMatchObject) == 1 && !(portExists || uriExists)) {
					return &meshOpErrors.UserConfigError{Message: fmt.Sprintf("only port/uri match allowed in route override. route: %d", routeIdx)}
				}
			}
		}
	}
	return nil
}

func newVirtualServiceMetaAdapter(patchMap map[string]interface{}) strategicpatch.LookupPatchMeta {
	return &dynamicSchemaMetaAdapter{
		path:           "",
		currentNode:    patchMap,
		objectStrategy: DefaultObjectStrategy,
		listStrategy:   virtualServiceListStrategy,
	}
}

func virtualServiceListStrategy(currentNode interface{}, path string) strategicpatch.PatchMeta {
	if isPathToARoute(path) {
		return mergeBySyntheticKeyMeta
	}
	return DefaultListStrategy(currentNode, path)
}

func generateSyntheticKey(baseVSName string, ind int) string {
	return fmt.Sprintf("%s-%d", baseVSName, ind)
}

func parsePort(port interface{}) int {
	switch port.(type) {
	case int:
		return port.(int)
	case int64:
		return int(port.(int64))
	case float64:
		return int(port.(float64))
	case string:
		intVal, err := strconv.Atoi(port.(string))
		if err != nil {
			return invalidPort
		}
		return intVal
	default:
		return invalidPort
	}
}

func isPathToARoute(path string) bool {
	if path == "/spec/http" || path == "/spec/tls" || path == "/spec/tcp" {
		return true
	}
	return false
}
