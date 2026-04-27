package patch

import (
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func isUriBasedInjection(overlay *v1alpha1.Overlay) (bool, interface{}) {
	overlayObj, _ := OverlayToObject(overlay)
	routes, found, _ := unstructured.NestedSlice(overlayObj, "spec", "http")
	if !found {
		return false, nil
	}
	routeAsMap, valid := routes[0].(map[string]interface{})
	if !valid {
		return false, nil
	}
	matchRequest, routeMatchRequestExists, _ := unstructured.NestedSlice(routeAsMap, "match")
	if !routeMatchRequestExists {
		return false, nil
	}
	uri, hasUri, _ := unstructured.NestedFieldNoCopy(matchRequest[0].(map[string]interface{}), "uri")
	return hasUri, uri
}

func getPortNumber(route map[string]interface{}) int {
	matchRequest, matchRequestExists, _ := unstructured.NestedSlice(route, "match")

	if matchRequestExists {
		if port, portExists, _ := unstructured.NestedFieldNoCopy(matchRequest[0].(map[string]interface{}), "port"); portExists {
			parsedPort := parsePort(port)
			if parsedPort == invalidPort {
				return 0
			}
			return parsedPort
		}
	}
	return 0
}

// updateRouteName appends `uri-injected` to the route name (if present)
func updateRouteName(route map[string]interface{}) {
	routeName, hasRouteName, _ := unstructured.NestedString(route, "name")
	if hasRouteName {
		_ = unstructured.SetNestedField(route, routeName+"-uri-injected", "name")
	}
	return
}

func copyMap(original map[string]interface{}) map[string]interface{} {
	newMap := make(map[string]interface{})

	for key, value := range original {
		if subMap, ok := value.(map[string]interface{}); ok {
			// If the value is a map, recursively copy it
			newMap[key] = copyMap(subMap)
		} else {
			// Otherwise, assign the value directly
			newMap[key] = value
		}
	}

	return newMap
}

// getDefaultRoute returns the default route in a given vs delegate
func getDefaultRoute(vs *unstructured.Unstructured) map[string]interface{} {
	routes, found, _ := unstructured.NestedSlice(vs.Object, "spec", "http")
	if found && len(routes) > 0 {
		defaultRoute := routes[len(routes)-1]
		routeAsMap, _ := defaultRoute.(map[string]interface{})
		return routeAsMap
	}
	return nil
}

// buildVsMatchKeys constructs keys which is used to identify VS against which uri injection needs to be applied
func buildVsMatchKeys(overlay *v1alpha1.Overlay) ([]string, error) {
	// match Key = vsName
	vsMatchKeys := make([]string, 0)
	if overlay.Name != "" {
		vsMatchKeys = append(vsMatchKeys, overlay.Name)
		return vsMatchKeys, nil
	}
	overlayObj, _ := OverlayToObject(overlay)
	routes, _, _ := unstructured.NestedSlice(overlayObj, "spec", "http")
	routeAsMap, valid := routes[0].(map[string]interface{})
	if !valid {
		return vsMatchKeys, fmt.Errorf("http route referenced in overlay is invalid")
	}
	matchRequest, matchRequestExists, err := unstructured.NestedSlice(routeAsMap, "match")
	if err != nil {
		return vsMatchKeys, err
	}

	if matchRequestExists {
		for _, matchAttr := range matchRequest {
			_, portExists, _ := unstructured.NestedFieldNoCopy(matchAttr.(map[string]interface{}), "port")
			if !portExists {
				// target all the VS
				vsMatchKeys = append(vsMatchKeys, "")
				return vsMatchKeys, nil
			}
		}
	}
	return vsMatchKeys, nil
}
