package patch

import (
	"fmt"
	"reflect"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type uriHandler struct {
	overlayUri interface{}
}

func NewUriHandler(overlayUri interface{}) *uriHandler {
	return &uriHandler{
		overlayUri: overlayUri,
	}
}

func (u *uriHandler) applyUriInjectionInDelegateVs(baseDelegateVS *unstructured.Unstructured, overlay *v1alpha1.Overlay, overlaidVsIndex map[string]bool) error {

	var overlaysToApply []map[string]interface{}
	var finalHttpRoutes []interface{}

	var err error

	// 0. Check uri match exist in delegate VS
	uriMatchExists, uriMatchErr := u.uriMatchExistInDelegateVS(baseDelegateVS)
	if uriMatchErr != nil {
		return uriMatchErr
	}

	if uriMatchExists {
		routes, _, _ := unstructured.NestedSlice(baseDelegateVS.Object, "spec", "http")
		for idx, route := range routes {
			if routeAsMap, valid := route.(map[string]interface{}); valid {
				uriMatchFound, matchErr := u.checkUriMatchExists(false, routeAsMap)
				if matchErr != nil {
					return matchErr
				}
				if uriMatchFound {
					syntheticKey := generateSyntheticKey(baseDelegateVS.GetName(), idx)
					err = insertSyntheticKey(baseDelegateVS.Object, "http", routes, routeAsMap, syntheticKey)

					cleanedOverlay := getCleanedOverlay(overlay)
					overlayToApply, _ := preProcessOverlay(cleanedOverlay, "http", syntheticKey)
					overlaysToApply = append(overlaysToApply, overlayToApply)
					finalHttpRoutes = append(finalHttpRoutes, routeAsMap)
				} else {
					// Insert none matching key for partially matching overlay to some routes on baseVS.
					// Index at the end is needed to make sure that the k8s strategic patch sort function doesn't reorder elements with the same merge key.
					insertErr := unstructured.SetNestedField(routeAsMap, fmt.Sprintf("no-match-found-%d", idx), SyntheticMergeKey)
					if insertErr != nil {
						return insertErr
					}
					finalHttpRoutes = append(finalHttpRoutes, routeAsMap)
				}
			}
		}
	} else {
		routes, _, _ := unstructured.NestedSlice(baseDelegateVS.Object, "spec", "http")
		for idx, route := range routes {
			routeAsMap, _ := route.(map[string]interface{})
			if idx != len(routes)-1 {
				syntheticKey := generateSyntheticKey(baseDelegateVS.GetName(), idx)
				_ = unstructured.SetNestedField(routeAsMap, syntheticKey, SyntheticMergeKey)
				finalHttpRoutes = append(finalHttpRoutes, routeAsMap)
			} else {
				clonedRoute, overlayToApply, cloneRouteErr := u.getUriRouteWithMatchingOverlay(routeAsMap, idx, -1, overlay, baseDelegateVS.GetName())
				if cloneRouteErr != nil {
					return err
				}

				finalHttpRoutes = append(finalHttpRoutes, clonedRoute)
				overlaysToApply = append(overlaysToApply, overlayToApply)

				// append default to final route list
				defaultRouteSyntheticKey := generateSyntheticKey(baseDelegateVS.GetName(), idx+1)
				_ = unstructured.SetNestedField(routeAsMap, defaultRouteSyntheticKey, SyntheticMergeKey)
				finalHttpRoutes = append(finalHttpRoutes, routeAsMap)
			}
		}
	}

	// update http routes
	err = unstructured.SetNestedSlice(baseDelegateVS.Object, finalHttpRoutes, "spec", "http")
	if err != nil {
		return err
	}

	if len(overlaysToApply) > 0 {
		_, err = applyOverlays(overlaysToApply, baseDelegateVS, overlaidVsIndex)
		if err != nil {
			return err
		}
	}
	postProcess(baseDelegateVS.Object)
	return nil
}

func (u *uriHandler) applyUriInjectionInRootVs(baseVs *unstructured.Unstructured, overlay *v1alpha1.Overlay, overlayMatchKeys []string, overlaidVsIndex map[string]bool) (bool, []string, error) {
	var overlaysToApply []map[string]interface{}
	var finalHttpRoutes []interface{}

	var err error

	var matchingDelegateKeys []string

	if routes, found, _ := unstructured.NestedSlice(baseVs.Object, "spec", "http"); found {
		routeCnt := -1
		for _, route := range routes {
			routeCnt += 1
			if routeAsMap, valid := route.(map[string]interface{}); valid {
				routeKeys, routeKeyErr := buildRouteKeys(routeAsMap, baseVs.GetName(), "http", true)
				if routeKeyErr != nil {
					continue
				}
				keyMatch := containsMatchKey(overlayMatchKeys, routeKeys)
				if keyMatch {
					delegateName, delegateExists, _ := unstructured.NestedFieldNoCopy(routeAsMap, "delegate", "name")
					if delegateExists {
						matchingDelegateKeys = append(matchingDelegateKeys, fmt.Sprintf("%s", delegateName))
						_ = unstructured.SetNestedField(routeAsMap, fmt.Sprintf("no-match-found-%d", routeCnt), SyntheticMergeKey)
						finalHttpRoutes = append(finalHttpRoutes, routeAsMap)
					} else {
						// 0. check if uri match exists or not
						uriMatchExists, uriMatchErr := u.checkUriMatchExists(true, routeAsMap)
						if uriMatchErr != nil {
							return false, matchingDelegateKeys, uriMatchErr
						}

						if uriMatchExists {
							syntheticKey := generateSyntheticKey(baseVs.GetName(), routeCnt)
							err = insertSyntheticKey(baseVs.Object, "http", routes, routeAsMap, syntheticKey)
							cleanedOverlay := getCleanedOverlay(overlay)
							overlayToApply, _ := preProcessOverlay(cleanedOverlay, "http", syntheticKey)
							overlaysToApply = append(overlaysToApply, overlayToApply)
							finalHttpRoutes = append(finalHttpRoutes, routeAsMap)
						} else {
							portNumber := getPortNumber(routeAsMap)
							clonedRoute, overlayToApply, cloneRouteErr := u.getUriRouteWithMatchingOverlay(routeAsMap, routeCnt, portNumber, overlay, baseVs.GetName())
							if cloneRouteErr != nil {
								return false, matchingDelegateKeys, cloneRouteErr
							}
							overlaysToApply = append(overlaysToApply, overlayToApply)
							routeCnt += 1
							_ = unstructured.SetNestedField(routeAsMap, fmt.Sprintf("no-match-found-%d", routeCnt), SyntheticMergeKey)
							finalHttpRoutes = append(finalHttpRoutes, clonedRoute, routeAsMap)
						}
					}
				} else {
					// Insert none matching key for partially matching overlay to some routes on baseVS.
					// Index at the end is needed to make sure that the k8s strategic patch sort function doesn't reorder elements with the same merge key.
					_ = unstructured.SetNestedField(routeAsMap, fmt.Sprintf("no-match-found-%d", routeCnt), SyntheticMergeKey)
					finalHttpRoutes = append(finalHttpRoutes, routeAsMap)
				}
			}
		}

		// update http routes
		err = unstructured.SetNestedSlice(baseVs.Object, finalHttpRoutes, "spec", "http")
		if err != nil {
			return false, matchingDelegateKeys, err
		}

		if len(overlaysToApply) > 0 {
			overlaidVsIndex, err = applyOverlays(overlaysToApply, baseVs, overlaidVsIndex)
			if err != nil {
				return false, matchingDelegateKeys, err
			}
		}
		postProcess(baseVs.Object)
		return len(overlaysToApply) > 0, matchingDelegateKeys, nil
	}
	return false, matchingDelegateKeys, nil
}

func (u *uriHandler) getUriRouteWithMatchingOverlay(route map[string]interface{}, routeIdx int, portNumber int, overlay *v1alpha1.Overlay, baseVsName string) (map[string]interface{}, map[string]interface{}, error) {
	// clone default route
	newRouteAsMap := copyMap(route)

	// cloned route pre-processing
	updateRouteName(newRouteAsMap)
	unstructured.RemoveNestedField(newRouteAsMap, "match")
	_ = u.insertUriInRoute(newRouteAsMap, portNumber)
	newRouteSyntheticKey := generateSyntheticKey(baseVsName, routeIdx)
	_ = unstructured.SetNestedField(newRouteAsMap, newRouteSyntheticKey, SyntheticMergeKey)

	// overlay processing for the cloned route
	cleanedOverlay := getCleanedOverlay(overlay)
	overlayToApply, _ := preProcessOverlay(cleanedOverlay, "http", newRouteSyntheticKey)
	return newRouteAsMap, overlayToApply, nil
}

func (u *uriHandler) insertUriInRoute(newRouteAsMap map[string]interface{}, portNumber int) error {

	var matchRequests []interface{}
	matchRequest := make(map[string]interface{})
	err := unstructured.SetNestedField(matchRequest, u.overlayUri, "uri")
	if err != nil {
		return err
	}
	if portNumber != -1 {
		err = unstructured.SetNestedField(matchRequest, int64(portNumber), "port")
		if err != nil {
			return err
		}
	}
	matchRequests = append(matchRequests, matchRequest)
	insertMatchErr := unstructured.SetNestedSlice(newRouteAsMap, matchRequests, "match")
	return insertMatchErr
}

func (u *uriHandler) uriMatchExistInDelegateVS(vs *unstructured.Unstructured) (bool, error) {
	if routes, found, _ := unstructured.NestedSlice(vs.Object, "spec", "http"); found {
		for _, route := range routes {
			if routeAsMap, valid := route.(map[string]interface{}); valid {
				uriMatchExists, uriMatchErr := u.checkUriMatchExists(false, routeAsMap)
				if uriMatchErr != nil {
					return false, uriMatchErr
				}
				if uriMatchExists {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (u *uriHandler) checkUriMatchExists(rootVS bool, baseRouteAsMap map[string]interface{}) (bool, error) {

	matchRequest, matchRequestExists, err := unstructured.NestedSlice(baseRouteAsMap, "match")
	if !matchRequestExists || err != nil {
		return false, err
	}

	if matchRequestExists {
		for _, matchAttr := range matchRequest {
			matchAttrAsMap := matchAttr.(map[string]interface{})

			baseRouteUri, hasUri, _ := unstructured.NestedFieldNoCopy(matchAttrAsMap, "uri")
			if hasUri {
				if rootVS {
					_, hasPort, portErr := unstructured.NestedFieldNoCopy(matchAttrAsMap, "port")
					if portErr != nil {
						return false, portErr
					}
					if hasPort {
						if len(matchAttrAsMap) == 2 && reflect.DeepEqual(baseRouteUri, u.overlayUri) {
							return true, nil
						}
					} else {
						if len(matchAttrAsMap) == 1 && reflect.DeepEqual(baseRouteUri, u.overlayUri) {
							return true, nil
						}
					}
				} else {
					if len(matchAttrAsMap) == 1 && reflect.DeepEqual(baseRouteUri, u.overlayUri) {
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}
