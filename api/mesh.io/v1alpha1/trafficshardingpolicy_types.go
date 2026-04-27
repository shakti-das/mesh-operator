/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:XValidation:rule="self.routes.filter(r, !has(r.match.headers) || size(r.match.headers) == 0).size() == 1",message="exactly one default route (with empty headers) is required"
// +kubebuilder:validation:XValidation:rule="!has(self.routes[size(self.routes)-1].match.headers) || size(self.routes[size(self.routes)-1].match.headers) == 0",message="the default route (empty headers) must be the last route"
type TrafficShardingPolicySpec struct {
	// List of routing rules that are evaluated by priority (higher priority first).
	// The last route must be the default route (with empty headers match).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	// +kubebuilder:validation:MaxItems=50
	Routes []TSPRoute `json:"routes"`

	// Selector for identifying target services.
	// +kubebuilder:validation:Required
	ServiceSelector ServiceSelector `json:"serviceSelector"`
}

type TrafficShardingPolicyStatus struct {
	// State represents the current state of the TrafficShardingPolicy
	// +kubebuilder:validation:Enum=Pending;Succeeded;Failed;Unknown
	// +kubebuilder:default:=Pending
	State Phase `json:"state,omitempty"`

	// Message provides additional information about the current state
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// ObservedGeneration reflects the generation of the most recently observed TrafficShardingPolicy
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// MatchedServices contains the list of services that matched the TSP selector
	// +kubebuilder:validation:Optional
	MatchedServices []string `json:"matchedServices,omitempty"`
}

// TSPRoute defines a single routing rule with match conditions and destination for Traffic Sharding Policy.
type TSPRoute struct {
	// Name of the rule for identification and debugging purposes.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Match criteria for this rule.
	// +kubebuilder:validation:Required
	Match TSPRouteMatch `json:"match"`

	// Destination configuration for matching traffic.
	// +kubebuilder:validation:Required
	Destination TSPRouteDestination `json:"destination"`
}

// TSPRouteMatch defines the conditions that must be met for a rule to be selected.
// Supports multiple types of matching with extensible design.
type TSPRouteMatch struct {
	// Map of header name to match criteria. All specified headers must match.
	// +kubebuilder:validation:Optional
	Headers map[string]TSPStringMatch `json:"headers,omitempty"`
}

// TSPStringMatch specifies the match criteria for strings.
// Exactly one of exact, prefix, suffix, or regex must be set.
type TSPStringMatch struct {
	// Exact string match.
	// +kubebuilder:validation:Optional
	Exact string `json:"exact,omitempty"`

	// Prefix-based match.
	// +kubebuilder:validation:Optional
	Prefix string `json:"prefix,omitempty"`

	// Suffix-based match.
	// +kubebuilder:validation:Optional
	Suffix string `json:"suffix,omitempty"`

	// RE2 style regex-based match (https://github.com/google/re2/wiki/Syntax).
	// +kubebuilder:validation:Optional
	Regex string `json:"regex,omitempty"`
}

// TSPRouteDestination defines where traffic matching a rule should be routed and how
// it should be distributed across available targets.
// Exactly one of cellType, serviceInstance, or cell must be set.
type TSPRouteDestination struct {
	// Route to instances of a specific cell type (e.g., "core", "sdb").
	// +kubebuilder:validation:Optional
	CellType string `json:"cellType,omitempty"`

	// Route to specific service instance.
	// +kubebuilder:validation:Optional
	ServiceInstance string `json:"serviceInstance,omitempty"`

	// Route to a specific named cell.
	// +kubebuilder:validation:Optional
	Cell string `json:"cell,omitempty"`

	// Hash policy for hash-based distribution across cells.
	// Only applicable if the target is a cellType.
	// +kubebuilder:validation:Optional
	HashPolicy *TSPHashPolicy `json:"hashPolicy,omitempty"`

	// Shuffle shard policy for shuffle-sharding within a cell/service-instance.
	// Only applicable if the target is a cell or service-instance.
	// If specified for a cellType, it gets applied within a cell that gets selected post consistent hashing.
	// +kubebuilder:validation:Optional
	ShuffleShardingPolicy *TSPShuffleShardPolicy `json:"shuffleShardingPolicy,omitempty"`
}

// TSPHashPolicy defines parameters for consistent hash-based routing.
// Exactly one of headerName or xfccConfig must be set.
type TSPHashPolicy struct {
	// Use value from specified HTTP header.
	// +kubebuilder:validation:Optional
	HeaderName string `json:"headerName,omitempty"`

	// Extract components from XFCC header.
	// +kubebuilder:validation:Optional
	XFCCConfig *TSPXFCCHashConfig `json:"xfccConfig,omitempty"`
}

// TSPShuffleShardPolicy defines the configuration for shuffle-sharding, a
// technique that provides isolation by distributing traffic across a subset
// of available hosts based on header. Shuffle-sharding helps limit the
// blast radius of problematic clients (like poison requests) by ensuring
// that different clients are assigned to different combinations of hosts.
type TSPShuffleShardPolicy struct {
	// Replication factor defines how many shards each client should be assigned to.
	// For example, if there are 8 available hosts and replicationFactor is 2,
	// each client will be randomly load balanced to 2 out of the 8 available backends,
	// creating isolation between different clients while maintaining redundancy.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum:=1
	ReplicationFactor int32 `json:"replicationFactor"`

	// Header specifies the HTTP header name whose value will be used to
	// compute the hash for consistent shard assignment. This ensures that
	// requests with the same header value are consistently routed to the same
	// set of shards.
	// +kubebuilder:validation:Required
	Header string `json:"header"`
}

// TSPXFCCHashConfig defines how to extract hash components from XFCC header.
type TSPXFCCHashConfig struct {
	// List of XFCC components to use for hash computation.
	// Components are concatenated in order before hashing.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems:=1
	Keys []TSPXFCCKey `json:"keys"`
}

// TSPXFCCKey defines the components available in XFCC header for hashing.
// +kubebuilder:validation:Enum=XFCC_KEY_UNSPECIFIED;SERVICE;NAMESPACE;CELL;FUNCTIONAL_DOMAIN
type TSPXFCCKey string

const (
	// Default value, should not be used.
	TSPXFCCKeyUnspecified TSPXFCCKey = "XFCC_KEY_UNSPECIFIED"
	// Hash based on the service identifier.
	TSPXFCCKeyService TSPXFCCKey = "SERVICE"
	// Hash based on the namespace identifier.
	TSPXFCCKeyNamespace TSPXFCCKey = "NAMESPACE"
	// Hash based on the cell identifier.
	TSPXFCCKeyCell TSPXFCCKey = "CELL"
	// Hash based on the functional domain.
	TSPXFCCKeyFunctionalDomain TSPXFCCKey = "FUNCTIONAL_DOMAIN"
)

// ServiceSelector contains criteria for selecting target services.
// +kubebuilder:validation:XValidation:rule="'p_servicename' in self.labels && self.labels['p_servicename'] != ”",message="serviceSelector.labels must include 'p_servicename' key with a non-empty value"
type ServiceSelector struct {
	// Key-value pairs of labels used to identify and select specific services.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties:=1
	Labels map[string]string `json:"labels"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=tsp

// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type TrafficShardingPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TrafficShardingPolicySpec   `json:"spec,omitempty"`
	Status TrafficShardingPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type TrafficShardingPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TrafficShardingPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TrafficShardingPolicy{}, &TrafficShardingPolicyList{})
}
