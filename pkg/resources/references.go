package resources

import (
	"fmt"

	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func ServiceToReference(clusterName string, service *corev1.Service) *v1alpha1.Owner {
	return &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: constants.ServiceResource.GroupVersion().String(),
		Kind:       constants.ServiceKind.Kind,
		Name:       service.Name,
		UID:        service.UID,
	}
}

func ServiceEntryToReference(clusterName string, serviceEntry *istiov1alpha3.ServiceEntry) *v1alpha1.Owner {
	return &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: constants.ServiceEntryResource.GroupVersion().String(),
		Kind:       constants.ServiceEntryKind.Kind,
		Name:       serviceEntry.Name,
		UID:        serviceEntry.UID,
	}
}

func IngressToReference(clusterName string, ingress *unstructured.Unstructured) *v1alpha1.Owner {
	return &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: constants.KnativeIngressResource.GroupVersion().String(),
		Kind:       constants.KnativeIngressKind.Kind,
		Name:       ingress.GetName(),
		UID:        ingress.GetUID(),
	}
}

func ServerlessServiceToReference(clusterName string, serverlessService *unstructured.Unstructured) *v1alpha1.Owner {
	return &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: constants.KnativeServerlessServiceResource.GroupVersion().String(),
		Kind:       constants.KnativeServerlessServiceKind.Kind,
		Name:       serverlessService.GetName(),
		UID:        serverlessService.GetUID(),
	}
}

func TspToReference(clusterName string, tsp *v1alpha1.TrafficShardingPolicy) *v1alpha1.Owner {
	return &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: constants.TrafficShardingPolicyResource.GroupVersion().String(),
		Kind:       constants.TrafficShardingPolicyKind.Kind,
		Name:       tsp.Name,
		UID:        tsp.UID,
	}
}

func removeOwnerFromOwnersList(clusterName string, kind string, ownerName string, ownerUid types.UID, owners []v1alpha1.Owner,
) []v1alpha1.Owner {
	newOwnerList := []v1alpha1.Owner{}
	for _, ref := range owners {
		if !isReferenceToTheOwner(clusterName, kind, ownerName, ownerUid, ref) {
			newOwnerList = append(newOwnerList, ref)
		}
	}
	return newOwnerList
}

func resourceToReference(resources ...*unstructured.Unstructured) []v1alpha1.OwnedResource {
	var references = make([]v1alpha1.OwnedResource, len(resources))
	for idx, res := range resources {
		references[idx] = v1alpha1.OwnedResource{
			ApiVersion: res.GetAPIVersion(),
			Kind:       res.GetKind(),
			Name:       res.GetName(),
			Namespace:  res.GetNamespace(),
			UID:        res.GetUID(),
		}
	}
	return references
}

// difference - returns a slice of references that exist in the "existing" slice, but don't exist in "incoming" one.
// We're using kind/namespace/name to compare the references.
// TODO: Include UID into comparison as well.
func markStaleResources(existing []v1alpha1.OwnedResource, incoming []v1alpha1.OwnedResource) (combinedResources, stale []v1alpha1.OwnedResource) {
	incomingMap := make(map[string]struct{}, len(incoming))
	for _, ref := range incoming {
		incomingMap[fmt.Sprintf("%s/%s/%s", ref.Kind, ref.Namespace, ref.Name)] = struct{}{}
	}

	var diff []v1alpha1.OwnedResource
	for _, ref := range existing {
		_, found := incomingMap[fmt.Sprintf("%s/%s/%s", ref.Kind, ref.Namespace, ref.Name)]
		if !found {
			ref.Stale = true
			diff = append(diff, ref)
		}
	}

	combined := append(incoming, diff...)
	return combined, diff
}

func isOwnerTracked(msm *v1alpha1.MeshServiceMetadata, clusterName string, kind string, ownerName string, ownerUid types.UID) bool {
	for _, ref := range msm.Spec.Owners {
		if isReferenceToTheOwner(clusterName, kind, ownerName, ownerUid, ref) {
			return true
		}
	}
	return false
}

func isReferenceToTheOwner(clusterName string, kind string, ownerName string, ownerUid types.UID,
	owner v1alpha1.Owner) bool {
	return owner.Cluster == clusterName &&
		owner.Kind == kind &&
		owner.Name == ownerName &&
		owner.UID == ownerUid
}
