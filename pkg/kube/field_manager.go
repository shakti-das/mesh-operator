package kube

import (
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	route                   = "route"
	drSubsets               = "subsets"
	labels                  = "labels"
	weight                  = "weight"
	subset                  = "subset"
	destination             = "destination"
	rolloutsPodHashTemplate = "rollouts-pod-template-hash"
)

type RetainFieldManager interface {
	RetainUnmanaged(*unstructured.Unstructured, *unstructured.Unstructured) error
}

// OcmFieldManager - field manager applied when we're dealing with OCM managed resources
type OcmFieldManager struct {
}

func (fm *OcmFieldManager) RetainUnmanaged(*unstructured.Unstructured, *unstructured.Unstructured) error {
	// We don't want to interfere with pod-hash and weights in VS/DR, when resource is managed by OCM.
	return nil
}

type ArgoFieldManager struct{}

func (a *ArgoFieldManager) RetainUnmanaged(existingConfig *unstructured.Unstructured, generatedConfig *unstructured.Unstructured) error {
	if !features.RetainArgoManagedFields || existingConfig == nil || generatedConfig == nil {
		return nil
	}

	fieldsManager := getFieldManager(existingConfig, generatedConfig)

	// skip restoring if there is no fieldsManager of the corresponding Kind
	if fieldsManager == nil {
		return nil
	}

	err := fieldsManager.ExtractFieldValues()
	if err != nil {
		return err
	}

	err = fieldsManager.PersistFieldValues()
	if err != nil {
		return err
	}

	return nil
}

// SidecarConfigStatusRetainer copies status from the existing SidecarConfig into the generated object before update.
// ProxyAccess updates only spec (egressHosts); SidecarConfigController writes status (phase, etc.).
type SidecarConfigStatusRetainer struct{}

func (s *SidecarConfigStatusRetainer) RetainUnmanaged(existing *unstructured.Unstructured, generated *unstructured.Unstructured) error {
	if existing == nil || generated == nil || existing.GetKind() != constants.SidecarConfigKind.Kind {
		return nil
	}
	if status, found, err := unstructured.NestedFieldCopy(existing.Object, "status"); err == nil && found && status != nil {
		return unstructured.SetNestedField(generated.Object, status, "status")
	}
	return nil
}

func getFieldManager(existingConfig *unstructured.Unstructured, generatedConfig *unstructured.Unstructured) RetainConfigField {
	kind := existingConfig.GetKind()
	if kind == constants.VirtualServiceKind.Kind {
		return &VirtualServiceFieldManager{
			existingVs: existingConfig,
			targetVs:   generatedConfig,
		}
	}

	if kind == constants.DestinationRuleKind.Kind {
		return &DestinationRuleFieldManager{
			existingDr: existingConfig,
			targetDr:   generatedConfig,
		}
	}

	return nil
}

type RetainConfigField interface {
	ExtractFieldValues() error
	PersistFieldValues() error
}

type VirtualServiceFieldManager struct {
	existingVs      *unstructured.Unstructured
	targetVs        *unstructured.Unstructured
	routeWeightsMap map[string]int64
}

// ExtractFieldValues extracts weights from http routes in VS
// route weights map - [httpRouteName/subset: weight]
func (v *VirtualServiceFieldManager) ExtractFieldValues() error {
	weights := make(map[string]int64)

	httpRoutes, found, err := unstructured.NestedSlice(v.existingVs.Object, "spec", "http")
	if err != nil {
		return err
	}
	if !found {
		return nil // No "http" routes found
	}

	for _, httpRoute := range httpRoutes {
		if httpRouteMap, ok := httpRoute.(map[string]interface{}); ok {
			routeEntries, found, err := unstructured.NestedSlice(httpRouteMap, route)
			if err != nil {
				return err
			}
			if !found {
				continue // Skip if no "route" entries are found
			}

			routeName, routeNameFound, err := unstructured.NestedString(httpRouteMap, "name")
			if err != nil {
				return err
			}
			if !routeNameFound {
				// not argo managed, since all argo managed http routes have name
				continue // Skip if no "route" name found
			}

			for _, route := range routeEntries {
				if routeMap, ok := route.(map[string]interface{}); ok {
					if weight, found, _ := unstructured.NestedInt64(routeMap, weight); found {
						// Use a unique identifier for the route (host and subset) as the key
						if dest, ok := routeMap[destination].(map[string]interface{}); ok {
							subset, foundSubset := dest[subset].(string)
							if !foundSubset {
								// not argo managed, since all argo managed routes have subsets
								continue
							}
							routeID := fmt.Sprintf("%s/%s", routeName, subset)
							weights[routeID] = weight
						}
					}
				}
			}
		}
	}
	v.routeWeightsMap = weights
	return nil
}

// PersistFieldValues applies weights in http routes in VS
func (v *VirtualServiceFieldManager) PersistFieldValues() error {
	httpRoutes, found, err := unstructured.NestedSlice(v.targetVs.Object, "spec", "http")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	for httpRouteInd, httpRoute := range httpRoutes {
		if httpRouteMap, ok := httpRoute.(map[string]interface{}); ok {
			routeEntries, found, err := unstructured.NestedSlice(httpRouteMap, route)
			if err != nil {
				return err
			}
			if !found {
				continue
			}

			routeName, routeNameFound, err := unstructured.NestedString(httpRouteMap, "name")
			if err != nil {
				return err
			}
			if !routeNameFound {
				// not argo managed, since all argo managed http routes have name
				continue // Skip if no "route" name found
			}

			for routeInd, route := range routeEntries {
				if routeMap, ok := route.(map[string]interface{}); ok {
					if dest, ok := routeMap[destination].(map[string]interface{}); ok {
						subset, foundSubset := dest[subset].(string)
						if !foundSubset {
							// not argo managed, since all argo managed routes have subsets
							continue
						}

						routeID := fmt.Sprintf("%s/%s", routeName, subset)

						// If there's a weight in the map for this route, apply it
						if weightAsInt, exists := v.routeWeightsMap[routeID]; exists {
							routeMap[weight] = weightAsInt
							routeEntries[routeInd] = routeMap
						}
					}
				}

			}
			// Update the httpRoute with modified routes
			httpRouteMap[route] = routeEntries
			httpRoutes[httpRouteInd] = httpRouteMap
		}
	}

	// Update obj's spec.http with the modified routes
	return unstructured.SetNestedSlice(v.targetVs.Object, httpRoutes, "spec", "http")
}

type DestinationRuleFieldManager struct {
	existingDr *unstructured.Unstructured
	targetDr   *unstructured.Unstructured
	podHashMap map[string]string
}

// ExtractFieldValues extracts pod template hashes from subsets in DR
// hashes map - [subsetName: podHash]
func (d *DestinationRuleFieldManager) ExtractFieldValues() error {
	hashes := make(map[string]string)

	subsets, found, err := unstructured.NestedSlice(d.existingDr.Object, "spec", drSubsets)
	if err != nil {
		return err
	}
	if !found {
		return nil // No "subsets" found or error occurred
	}

	for _, subset := range subsets {
		if subsetMap, ok := subset.(map[string]interface{}); ok {
			name, found, err := unstructured.NestedString(subsetMap, "name")
			if err != nil {
				return err
			}
			if !found {
				continue // Skip if no "name" field found
			}

			labelsAsMap, found, err := unstructured.NestedMap(subsetMap, labels)
			if err != nil {
				return err
			}
			if found {
				if hash, found := labelsAsMap[rolloutsPodHashTemplate].(string); found {
					hashes[name] = hash // Store the hash with the subset name as the key
				}
			}
		}
	}

	d.podHashMap = hashes
	return nil

}

// PersistFieldValues applies pod template hashes in subsets in DR
func (d *DestinationRuleFieldManager) PersistFieldValues() error {
	subsets, found, err := unstructured.NestedSlice(d.targetDr.Object, "spec", drSubsets)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	for subsetInd, subset := range subsets {
		if subsetMap, ok := subset.(map[string]interface{}); ok {
			name, found, err := unstructured.NestedString(subsetMap, "name")
			if err != nil {
				return err
			}
			if !found {
				continue // Skip if no "name" field found
			}

			// If there is a hash for this subset name, apply it to the labels
			if hash, exists := d.podHashMap[name]; exists {
				// Access or create the labels map
				labelsAsMap, found, err := unstructured.NestedMap(subsetMap, labels)
				if err != nil {
					return err
				}
				if !found {
					labelsAsMap = make(map[string]interface{})
				}
				labelsAsMap[rolloutsPodHashTemplate] = hash
				subsetMap[labels] = labelsAsMap
			}
			subsets[subsetInd] = subsetMap
		}
	}

	// Update obj's spec.subsets with the modified subsets
	return unstructured.SetNestedSlice(d.targetDr.Object, subsets, "spec", drSubsets)
}
