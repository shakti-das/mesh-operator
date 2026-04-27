package controllers

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (

	//Condition Types
	LoadBalancerReady = "LoadBalancerReady"
	NetworkConfigured = "NetworkConfigured"
	Ready             = "Ready"
	MeshConfigured    = "MeshConfigured"

	//Reasons
	ReconcileVirtualServiceFailed = "ReconcileVirtualServiceFailed"
	ReconcileMeshConfigFailed     = "ReconcileMeshConfigFailed"
)

type IngressStatus struct {
	Status Status `json:"status"`
}

type Status struct {
	ObservedGeneration  int64              `json:"observedGeneration,omitempty"`
	Conditions          []Condition        `json:"conditions"`
	PrivateLoadBalancer LoadBalancerStatus `json:"privateLoadBalancer"`
}

type Condition struct {
	Type               string                 `json:"type"`
	Status             metav1.ConditionStatus `json:"status"`
	Reason             string                 `json:"reason,omitempty"`
	Message            string                 `json:"message,omitempty"`
	LastTransitionTime metav1.Time            `json:"lastTransitionTime,omitempty"`
}

type LoadBalancerStatus struct {
	Ingress []LoadBalancerIngressStatus `json:"ingress"`
}

type LoadBalancerIngressStatus struct {
	MeshOnly bool `json:"meshOnly,omitempty"`
}

type IngStatusBuilder struct {
	ingressStatus *IngressStatus
}

func NewIngressStatusBuilder() *IngStatusBuilder {
	return &IngStatusBuilder{
		ingressStatus: &IngressStatus{},
	}
}

func (ingStatusBuilder *IngStatusBuilder) SetIngressStatus(ingress *unstructured.Unstructured) *IngStatusBuilder {
	ingStatusBuilder.ingressStatus.Status.ObservedGeneration = ingress.GetGeneration()
	ingStatusBuilder.ingressStatus.Status.PrivateLoadBalancer = LoadBalancerStatus{
		Ingress: []LoadBalancerIngressStatus{
			{
				MeshOnly: true,
			},
		},
	}
	return ingStatusBuilder
}

func (ingStatusBuilder *IngStatusBuilder) AddCondition(conditionType string, conditionStatus metav1.ConditionStatus, reason, err string) *IngStatusBuilder {
	condition := Condition{}
	condition.Type = conditionType
	condition.Status = conditionStatus
	condition.Reason = reason
	condition.Message = err
	condition.LastTransitionTime = metav1.Now()

	ingStatusBuilder.ingressStatus.Status.Conditions = append(ingStatusBuilder.ingressStatus.Status.Conditions, condition)
	return ingStatusBuilder
}

func (ingStatusBuilder *IngStatusBuilder) Build() *IngressStatus {
	return ingStatusBuilder.ingressStatus
}
