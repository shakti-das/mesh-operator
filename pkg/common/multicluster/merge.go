package multicluster

import (
	"maps"
	"slices"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// MergeServices - a key function in handling mesh-config for striped services.
// Given service records for a striped service from multiple clusters, produces a single service record that's used to generate config.
// Conflict resolution inspired by: https://github.com/kubernetes/enhancements/tree/master/keps/sig-multicluster/1645-multi-cluster-services-api#constraints-and-conflict-resolution
// Conflict resolution: conflict in values that can't be merged are resolved in the favor of the older Service object
// * service ports are unioned, conflicts in name/protocol resolved according to the conflict resolution
// * service headlessness - resolved according to the conflict resolution
// * service annotations/labels are merged, in case of a conflict - resolved according to the conflict resolution
func MergeServices(services []*corev1.Service, serviceInEventResourceVersion string) *corev1.Service {
	// Early exit
	if len(services) == 1 {
		return services[0]
	}

	// Sort services by age in ascending order (older to the right)
	var servicesSortedByAge []*corev1.Service
	for _, service := range services {
		servicesSortedByAge = append(servicesSortedByAge, service)
	}
	slices.SortFunc(servicesSortedByAge, func(svcLeft, svcRight *corev1.Service) int {
		res := svcLeft.CreationTimestamp.Compare(svcRight.CreationTimestamp.Time)
		return -res
	})

	// Take the oldest service as the base (this also determines the headlessness)
	resultService := servicesSortedByAge[len(servicesSortedByAge)-1].DeepCopy()

	// Merge ports
	resultService.Spec.Ports = mergePorts(servicesSortedByAge)

	// Merge annotations
	mergedAnnotations := mergeMap(servicesSortedByAge, func(service *corev1.Service) map[string]string {
		return service.GetAnnotations()
	})
	resultService.SetAnnotations(mergedAnnotations)

	// Merge labels
	mergedLabels := mergeMap(servicesSortedByAge, func(service *corev1.Service) map[string]string {
		return service.GetLabels()
	})
	resultService.SetLabels(mergedLabels)
	resultService.SetResourceVersion(serviceInEventResourceVersion)
	return resultService
}

// MergeMeshOperators - another key function in the handling of the mesh-config generation for striped services.
// It is presented a map of cluster -> []*MeshOperator which contains overlaying MOPs targeting a service across different clusters.
// Result is built in the following fashion:
// * if a MeshOperator.name is unique across other clusters, it is added to the final result
// * if a given MeshOperator.name is not unique, an older record wins
func MergeMeshOperators(mopsPerCluster map[string][]*v1alpha1.MeshOperator) []*v1alpha1.MeshOperator {
	meshOpsByName := map[string]*v1alpha1.MeshOperator{}

	for _, mops := range mopsPerCluster {
		for _, mop := range mops {
			existingMop, alreadyExists := meshOpsByName[mop.Name]
			if alreadyExists {
				// Replace or keep the older one
				mopToUse := existingMop
				if mop.CreationTimestamp.Before(&existingMop.CreationTimestamp) {
					mopToUse = mop
				}
				meshOpsByName[mop.Name] = mopToUse
			} else {
				meshOpsByName[mop.Name] = mop
			}
		}
	}

	// Collect results and return (order is non-deterministic)
	var mopsToUse []*v1alpha1.MeshOperator
	for _, mop := range meshOpsByName {
		mopsToUse = append(mopsToUse, mop)
	}
	return mopsToUse
}

func GetOldestService(services map[string]*corev1.Service) (string, *corev1.Service) {
	if len(services) == 0 {
		return "", nil
	}

	var oldestServiceCluster string
	var oldestService *corev1.Service

	for cluster, service := range services {
		if oldestService == nil || oldestService.CreationTimestamp.After(service.CreationTimestamp.Time) {
			oldestService = service
			oldestServiceCluster = cluster
		}
	}

	return oldestServiceCluster, oldestService
}

func mergeMap(services []*corev1.Service, getMap func(*corev1.Service) map[string]string) map[string]string {
	var resultMap = map[string]string{}
	// Iterate in the order to make sure that the older service wins
	for _, service := range services {
		maps.Copy(resultMap, getMap(service))
	}
	return resultMap
}

func mergePorts(services []*corev1.Service) []corev1.ServicePort {
	// portNumber -> port
	portsMap := map[int32]corev1.ServicePort{}

	// Traverse in the newer to older order and build ports map, to make sure that older service ports win
	for _, service := range services {
		for _, port := range service.Spec.Ports {
			portsMap[port.Port] = port
		}
	}

	// Collect and sort keys in consistent order, ordered by the port value
	sortedPorts := []int32{}
	for port := range portsMap {
		sortedPorts = append(sortedPorts, port)
	}
	slices.Sort(sortedPorts)

	// Collect result using the sorted jeys
	resultPorts := []corev1.ServicePort{}
	for _, port := range sortedPorts {
		resultPorts = append(resultPorts, portsMap[port])
	}

	return resultPorts
}
