package matching

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"istio.io/client-go/pkg/apis/networking/v1alpha3"
	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

type MopListFunc func(namespace string) ([]*v1alpha1.MeshOperator, error)
type ServiceListFunc func(namespace string, labelSelector labels.Selector) ([]*v1.Service, error)
type ServiceEntryListFunc func(namespace string, labelSelector labels.Selector) ([]*v1alpha3.ServiceEntry, error)

// ListMopsForService - obtains a list of MOP records that match given service
// Note that it doesn't account for MOP records that don't have Selector specified
func ListMopsForService(service *v1.Service, listFunc MopListFunc) ([]*v1alpha1.MeshOperator, error) {
	meshOperators, err := listFunc(service.Namespace)
	if err != nil {
		return nil, fmt.Errorf("error obtaining MOP records: %w", err)
	}

	var matches []*v1alpha1.MeshOperator
	for _, mop := range meshOperators {
		if !HasServiceSelector(mop) || !mop.GetObjectMeta().GetDeletionTimestamp().IsZero() {
			// Ignore MOP records without selector or ones being deleted
			continue
		}
		selector := mopToServiceSelector(mop)
		if selector.Matches(labels.Set(service.GetLabels())) {
			matches = append(matches, mop)
		}
	}

	return matches, nil
}

// ListMopsForServiceEntry - obtains a list of MOP records that match given serviceEntry
// Note that it doesn't account for MOP records that don't have Selector specified
func ListMopsForServiceEntry(serviceEntry *istiov1alpha3.ServiceEntry, listFunc MopListFunc) ([]*v1alpha1.MeshOperator, error) {
	meshOperators, err := listFunc(serviceEntry.Namespace)
	if err != nil {
		return nil, fmt.Errorf("error obtaining MOP records: %w", err)
	}

	var matches []*v1alpha1.MeshOperator
	for _, mop := range meshOperators {
		if !HasServiceSelector(mop) || !mop.GetObjectMeta().GetDeletionTimestamp().IsZero() {
			// Ignore MOP records without selector or ones being deleted
			continue
		}
		selector := mopToServiceSelector(mop)
		if selector.Matches(labels.Set(serviceEntry.GetLabels())) {
			matches = append(matches, mop)
		}
	}

	return matches, nil
}

// ListServicesForMop - obtains a list of Service records that match given MOP's serviceSelector
// Returns an empty list if MOP record doesn't have serviceSelector specified.
func ListServicesForMop(mop *v1alpha1.MeshOperator, listFunc ServiceListFunc, hasServiceSelectorFunc func(*v1alpha1.MeshOperator) bool) ([]*v1.Service, error) {
	var services []*v1.Service
	if hasServiceSelectorFunc(mop) {
		svcList, err := listFunc(mop.Namespace, mopToServiceSelector(mop))
		if err != nil {
			return nil, fmt.Errorf("error obtaining Service records: %w", err)
		}
		// filter out service with routing disabled annotation
		for _, svc := range svcList {
			if !common.IsOperatorDisabled(svc) {
				// GVK needs to be set explicitly in order to make sure service.TypeMetadata field is set (TypeMetadata is used futher while converting object to unstructured object type)
				svc.SetGroupVersionKind(schema.GroupVersionKind{Group: constants.ServiceKind.Group, Version: constants.ServiceKind.Version, Kind: constants.ServiceKind.Kind})
				services = append(services, svc)
			}
		}
		return services, nil
	}

	return services, nil
}

func ListServiceEntriesForMop(mop *v1alpha1.MeshOperator, listFunc ServiceEntryListFunc) ([]*v1alpha3.ServiceEntry, error) {
	var serviceEntries []*v1alpha3.ServiceEntry
	if HasServiceSelector(mop) {
		seList, err := listFunc(mop.Namespace, mopToServiceSelector(mop))
		if err != nil {
			return nil, fmt.Errorf("error listing ServiceEntry records: %w", err)
		}
		for _, se := range seList {
			// GVK needs to be set explicitly in order to make sure SE.TypeMetadata field is set (TypeMetadata is used further while converting SE to unstructured object type)
			se.SetGroupVersionKind(schema.GroupVersionKind{Group: constants.ServiceEntryKind.Group, Version: constants.ServiceEntryKind.Version, Kind: constants.ServiceEntryKind.Kind})
		}
		return seList, nil
	}
	return serviceEntries, nil
}

func mopToServiceSelector(mop *v1alpha1.MeshOperator) labels.Selector {
	requirements := make([]labels.Requirement, 0, len(mop.Spec.ServiceSelector))
	for label, value := range mop.Spec.ServiceSelector {
		requirement, _ := labels.NewRequirement(label, selection.Equals, []string{value})
		requirements = append(requirements, *requirement)
	}

	selector := labels.NewSelector()
	return selector.Add(requirements...)
}

func HasServiceSelector(mop *v1alpha1.MeshOperator) bool {
	return len(mop.Spec.ServiceSelector) > 0
}

type TspListFunc func(namespace string) ([]*v1alpha1.TrafficShardingPolicy, error)

// tspToServiceSelector converts TSP's serviceSelector to k8s labels.Selector
func tspToServiceSelector(tsp *v1alpha1.TrafficShardingPolicy) labels.Selector {
	requirements := make([]labels.Requirement, 0, len(tsp.Spec.ServiceSelector.Labels))
	for label, value := range tsp.Spec.ServiceSelector.Labels {
		requirement, _ := labels.NewRequirement(label, selection.Equals, []string{value})
		requirements = append(requirements, *requirement)
	}

	selector := labels.NewSelector()
	return selector.Add(requirements...)
}

// ListTspsForService obtains a list of TSP records that match given service based on labels
func ListTspsForService(service *v1.Service, listFunc TspListFunc) ([]*v1alpha1.TrafficShardingPolicy, error) {
	tsps, err := listFunc(service.Namespace)
	if err != nil {
		return nil, fmt.Errorf("error obtaining TSP records: %w", err)
	}

	var matches []*v1alpha1.TrafficShardingPolicy
	for _, tsp := range tsps {
		if !tsp.GetObjectMeta().GetDeletionTimestamp().IsZero() {
			// Ignore TSPs being deleted
			continue
		}
		selector := tspToServiceSelector(tsp)
		if selector.Matches(labels.Set(service.GetLabels())) {
			matches = append(matches, tsp)
		}
	}

	return matches, nil
}

// ListServicesForTsp obtains a list of Service records that match given TSP's serviceSelector
func ListServicesForTsp(tsp *v1alpha1.TrafficShardingPolicy, listFunc ServiceListFunc) ([]*v1.Service, error) {
	var services []*v1.Service
	svcList, err := listFunc(tsp.Namespace, tspToServiceSelector(tsp))
	if err != nil {
		return nil, fmt.Errorf("error obtaining Service records for TSP: %w", err)
	}
	// filter out service with routing disabled annotation
	for _, svc := range svcList {
		if !common.IsOperatorDisabled(svc) {
			// GVK needs to be set explicitly in order to make sure service.TypeMetadata field is set
			svc.SetGroupVersionKind(schema.GroupVersionKind{Group: constants.ServiceKind.Group, Version: constants.ServiceKind.Version, Kind: constants.ServiceKind.Kind})
			services = append(services, svc)
		}
	}
	return services, nil
}
