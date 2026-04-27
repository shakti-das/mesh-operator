package ocm

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	ManagedByLabel = "open-cluster-management.io/managed-by"
	ManagedByValue = "ocm"
)

func IsOcmManagedObject(object *unstructured.Unstructured) bool {
	if object == nil {
		return false
	}
	labels := object.GetLabels()
	if labels == nil {
		return false
	}
	return labels[ManagedByLabel] == ManagedByValue
}
