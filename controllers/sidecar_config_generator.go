package controllers

import (
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	meshv1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
)

// generateSidecar generates an Istio Sidecar resource based on a workload-level SidecarConfig.
// It merges egress hosts from three levels:
//  1. Root SidecarConfig (if exists)
//  2. Namespace SidecarConfig (if exists)
//  3. Workload SidecarConfig (required)
//
// The generated Istio Sidecar will have:
//   - metadata.name: Derived from SidecarConfig name (e.g., my-service-my-cell-my-instance-sidecar)
//   - metadata.labels: Propagated from SidecarConfig labels (p_servicename, p_cell, p_serviceinstance)
//   - workloadSelector: From workload SidecarConfig.Spec.WorkloadSelector; if renderMode is Shadow,
//     a dummy label (sidecar.mesh.io/shadow=true) is added so the Sidecar does not match any workload (shadow mode).
//   - egress: Merged entries from all three levels (grouped by port, deduplicated)
//   - outboundTrafficPolicy: Hardcoded to ALLOW_ANY (enables DFP discovery)
func (c *SidecarConfigController) generateSidecar(
	sc *meshv1alpha1.SidecarConfig,
	rootConfig *meshv1alpha1.SidecarConfig,
	namespaceConfig *meshv1alpha1.SidecarConfig,
	renderMode SidecarRenderMode,
) (*templating.AppliedConfigObject, error) {
	ctxLogger := c.logger.With("namespace", sc.Namespace, "name", sc.Name)

	// Defensive check: EgressHosts and WorkloadSelector should be validated earlier, but add safety check
	if sc.Spec.EgressHosts == nil {
		return nil, fmt.Errorf("SidecarConfig %s/%s: spec.egressHosts cannot be nil", sc.Namespace, sc.Name)
	}
	if sc.Spec.WorkloadSelector == nil {
		return nil, fmt.Errorf("SidecarConfig %s/%s: spec.workloadSelector cannot be nil", sc.Namespace, sc.Name)
	}

	// Gather egress hosts from all three levels
	// Merge Runtime + Config hosts (Static hosts not yet supported)
	var allHosts []meshv1alpha1.Host

	// 1. Root-level hosts (if exists)
	if rootConfig != nil && rootConfig.Spec.EgressHosts != nil {
		if rootConfig.Spec.EgressHosts.Discovered != nil {
			allHosts = append(allHosts, rootConfig.Spec.EgressHosts.Discovered.Runtime...)
			allHosts = append(allHosts, rootConfig.Spec.EgressHosts.Discovered.Config...)
		}
		ctxLogger.Debugf("root-level: %d total hosts", len(allHosts))
	}

	// 2. Namespace-level hosts (if exists)
	if namespaceConfig != nil && namespaceConfig.Spec.EgressHosts != nil {
		if namespaceConfig.Spec.EgressHosts.Discovered != nil {
			allHosts = append(allHosts, namespaceConfig.Spec.EgressHosts.Discovered.Runtime...)
			allHosts = append(allHosts, namespaceConfig.Spec.EgressHosts.Discovered.Config...)
		}
	}

	// 3. Workload-level hosts (required)
	if sc.Spec.EgressHosts.Discovered != nil {
		allHosts = append(allHosts, sc.Spec.EgressHosts.Discovered.Runtime...)
		allHosts = append(allHosts, sc.Spec.EgressHosts.Discovered.Config...)
	}

	// Merge and deduplicate
	mergedHostEntries := c.mergeEgressHosts(allHosts)

	ctxLogger.Infof("merged egress hosts: %d total (deduplicated)", len(mergedHostEntries))

	// Build egress section grouped by port
	egressSection := buildEgressSection(mergedHostEntries)

	sidecarName := deriveSidecarName(sc.Name)

	workloadSelectorLabels := copyLabels(sc.Spec.WorkloadSelector.Labels)
	if renderMode == SidecarRenderModeShadow {
		// Dummy label ensures the Sidecar does not match any workload (shadow mode; no actual injection).
		workloadSelectorLabels[constants.SidecarShadowLabel] = "true"
	}

	sidecar := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1beta1",
			"kind":       "Sidecar",
			"metadata": map[string]interface{}{
				"name":      sidecarName,
				"namespace": sc.Namespace,
				"labels":    copyLabels(sc.Labels),
			},
			"spec": map[string]interface{}{
				"workloadSelector": map[string]interface{}{
					"labels": workloadSelectorLabels,
				},
				"egress": egressSection,
				"outboundTrafficPolicy": map[string]interface{}{
					"mode": "ALLOW_ANY",
				},
			},
		},
	}

	// Apply mutators (adds mesh.io/managed-by label and ownerReferences)
	scUnstructured, err := convertToUnstructured(sc)
	if err != nil {
		return nil, fmt.Errorf("failed to convert SidecarConfig to unstructured: %w", err)
	}

	ownerRef := sidecarConfigToOwnerRef(sc)
	renderCtx := &templating.RenderRequestContext{
		Object:   scUnstructured,
		OwnerRef: &ownerRef,
	}

	mutators := []templating.Mutator{
		&templating.ManagedByMutator{},
		&templating.OwnerRefMutator{},
	}

	for _, mutator := range mutators {
		sidecar, err = mutator.Mutate(renderCtx, sidecar)
		if err != nil {
			return nil, fmt.Errorf("failed to apply mutator: %w", err)
		}
	}

	ctxLogger.Infof("successfully generated Sidecar: %s/%s", sidecar.GetNamespace(), sidecar.GetName())

	return &templating.AppliedConfigObject{
		Object: sidecar,
		Error:  nil,
	}, nil
}

// mergeEgressHosts deduplicates and merges egress hosts from static and discovered sources.
// Deduplication is based on (hostname, port) tuple. Returns sorted by hostname, then port.
func (c *SidecarConfigController) mergeEgressHosts(hosts []meshv1alpha1.Host) []meshv1alpha1.Host {
	seen := make(map[string]meshv1alpha1.Host)
	getKey := func(entry meshv1alpha1.Host) string {
		return fmt.Sprintf("%s:%d", entry.Hostname, entry.Port)
	}

	for _, entry := range hosts {
		key := getKey(entry)
		if _, exists := seen[key]; !exists {
			seen[key] = entry
		}
	}

	result := make([]meshv1alpha1.Host, 0, len(seen))
	for _, entry := range seen {
		result = append(result, entry)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Hostname != result[j].Hostname {
			return result[i].Hostname < result[j].Hostname
		}
		return result[i].Port < result[j].Port
	})

	return result
}

// buildEgressSection groups egress entries by port and builds the Istio Sidecar egress structure.
func buildEgressSection(entries []meshv1alpha1.Host) []interface{} {
	// Group entries by port
	type portGroup struct {
		port  int32
		hosts []string
	}

	portMap := make(map[int32]*portGroup)

	for _, entry := range entries {
		port := entry.Port // 0 means no port specified

		// Add "*/" prefix for cross-namespace routing
		hostname := fmt.Sprintf("*/%s", entry.Hostname)

		if group, exists := portMap[port]; exists {
			group.hosts = append(group.hosts, hostname)
		} else {
			portMap[port] = &portGroup{
				port:  port,
				hosts: []string{hostname},
			}
		}
	}

	var egressEntries []interface{}
	for _, group := range portMap {
		// Sort hosts for consistent output
		sort.Strings(group.hosts)

		egressEntry := map[string]interface{}{
			"hosts": group.hosts,
		}

		// Only add port field if port is specified (non-zero)
		if group.port != 0 {
			egressEntry["port"] = map[string]interface{}{
				"number":   group.port,
				"protocol": "HTTP",
			}
		}

		egressEntries = append(egressEntries, egressEntry)
	}

	// Sort egress entries by port for consistent output
	sort.Slice(egressEntries, func(i, j int) bool {
		entryI := egressEntries[i].(map[string]interface{})
		entryJ := egressEntries[j].(map[string]interface{})

		portI, hasPortI := entryI["port"]
		portJ, hasPortJ := entryJ["port"]

		// Entries without port come last
		if !hasPortI && !hasPortJ {
			return false
		}
		if !hasPortI {
			return false
		}
		if !hasPortJ {
			return true
		}

		portNumI := portI.(map[string]interface{})["number"].(int32)
		portNumJ := portJ.(map[string]interface{})["number"].(int32)
		return portNumI < portNumJ
	})

	return egressEntries
}

// deriveSidecarName derives the Istio Sidecar name from the SidecarConfig name.
func deriveSidecarName(sidecarConfigName string) string {
	if strings.HasSuffix(sidecarConfigName, "-sidecarconfig") {
		return strings.TrimSuffix(sidecarConfigName, "-sidecarconfig") + "-sidecar"
	}
	if strings.HasSuffix(sidecarConfigName, "sidecarconfig") {
		return strings.TrimSuffix(sidecarConfigName, "sidecarconfig") + "sidecar"
	}
	return sidecarConfigName + "-sidecar"
}

// copyLabels converts map[string]string to map[string]interface{} for unstructured.
func copyLabels(labels map[string]string) map[string]interface{} {
	if labels == nil {
		return make(map[string]interface{})
	}

	result := make(map[string]interface{}, len(labels))
	for k, v := range labels {
		result[k] = v
	}
	return result
}

// sidecarConfigToOwnerRef creates an OwnerReference for automatic garbage collection.
func sidecarConfigToOwnerRef(sc *meshv1alpha1.SidecarConfig) metav1.OwnerReference {
	controller := true
	return metav1.OwnerReference{
		APIVersion:         meshv1alpha1.SchemeGroupVersion.String(),
		Kind:               "SidecarConfig",
		Name:               sc.Name,
		UID:                sc.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &controller,
	}
}

// convertToUnstructured converts a SidecarConfig to unstructured for mutation.
func convertToUnstructured(sc *meshv1alpha1.SidecarConfig) (*unstructured.Unstructured, error) {
	unstructuredObj := &unstructured.Unstructured{}
	unstructuredObj.SetGroupVersionKind(meshv1alpha1.SchemeGroupVersion.WithKind("SidecarConfig"))
	unstructuredObj.SetName(sc.Name)
	unstructuredObj.SetNamespace(sc.Namespace)
	unstructuredObj.SetUID(sc.UID)
	unstructuredObj.SetLabels(sc.Labels)
	unstructuredObj.SetAnnotations(sc.Annotations)
	return unstructuredObj, nil
}
