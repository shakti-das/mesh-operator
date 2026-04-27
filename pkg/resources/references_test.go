package resources

import (
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	networkingv1alpha3 "istio.io/api/networking/v1alpha3"
	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestIsReferenceToService(t *testing.T) {
	var clusterName = "cluster-1"
	var serviceName = "svc1"
	var kind = "Service"
	var uid types.UID = "abcd-123"

	// Match
	assert.True(t, isReferenceToTheOwner(clusterName, kind, serviceName, uid, v1alpha1.Owner{
		Cluster: clusterName,
		Kind:    kind,
		Name:    serviceName,
		UID:     uid,
	}))

	// Different cluster
	assert.False(t, isReferenceToTheOwner(clusterName, kind, serviceName, uid, v1alpha1.Owner{
		Cluster: "other-cluster",
		Kind:    kind,
		Name:    serviceName,
		UID:     uid,
	}))

	// Different kind
	assert.False(t, isReferenceToTheOwner(clusterName, kind, serviceName, uid, v1alpha1.Owner{
		Cluster: clusterName,
		Kind:    "Rollout",
		Name:    serviceName,
		UID:     uid,
	}))

	// Different name
	assert.False(t, isReferenceToTheOwner(clusterName, kind, serviceName, uid, v1alpha1.Owner{
		Cluster: clusterName,
		Kind:    kind,
		Name:    "svc2",
		UID:     uid,
	}))

	// Differend uid
	assert.False(t, isReferenceToTheOwner(clusterName, kind, serviceName, uid, v1alpha1.Owner{
		Cluster: clusterName,
		Kind:    kind,
		Name:    serviceName,
		UID:     "other-uid1",
	}))
}

func TestIsServiceTracked(t *testing.T) {
	var clusterName = "cluster-1"
	var serviceName = "svc1"
	var kind = "Service"
	var uid types.UID = "abcd-123"

	testCases := []struct {
		name           string
		references     []v1alpha1.Owner
		expectedResult bool
	}{
		{
			name: "Tracked: single reference",
			references: []v1alpha1.Owner{
				{
					Cluster: clusterName,
					Kind:    kind,
					Name:    serviceName,
					UID:     uid,
				},
			},
			expectedResult: true,
		},
		{
			name: "Tracked: multiple references",
			references: []v1alpha1.Owner{
				{
					Cluster: "other-cluster",
					Kind:    kind,
					Name:    serviceName,
					UID:     uid,
				},
				{
					Cluster: clusterName,
					Kind:    kind,
					Name:    serviceName,
					UID:     uid,
				},
			},
			expectedResult: true,
		},
		{
			name: "Not tracked",
			references: []v1alpha1.Owner{
				{
					Cluster: "other-cluster",
					Kind:    kind,
					Name:    serviceName,
					UID:     uid,
				},
			},
		},
		{
			name:       "Empty references",
			references: []v1alpha1.Owner{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msm := &v1alpha1.MeshServiceMetadata{
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: tc.references,
				},
			}

			assert.Equal(t, tc.expectedResult, isOwnerTracked(msm, clusterName, kind, serviceName, uid))
		})
	}
}

func TestMarkStaleResources(t *testing.T) {
	ref1 := v1alpha1.OwnedResource{
		Kind:      "EnvoyFilter",
		Namespace: "ns-1",
		Name:      "filter-1",
	}
	ref1Stale := *ref1.DeepCopy()
	ref1Stale.Stale = true

	ref2 := v1alpha1.OwnedResource{
		Kind:      "EnvoyFilter",
		Namespace: "ns-1",
		Name:      "filter-2",
	}
	ref2Stale := *ref2.DeepCopy()
	ref2Stale.Stale = true

	ref3 := v1alpha1.OwnedResource{
		Kind:      "VirtualService",
		Namespace: "ns-2",
		Name:      "vs-1",
	}

	// Empty existing
	combined, stale := markStaleResources([]v1alpha1.OwnedResource{}, []v1alpha1.OwnedResource{ref1, ref2})
	assert.ElementsMatch(t, []v1alpha1.OwnedResource{}, stale)
	assert.ElementsMatch(t, []v1alpha1.OwnedResource{ref1, ref2}, combined)

	// Empty incoming
	combined, stale = markStaleResources([]v1alpha1.OwnedResource{ref1, ref2}, []v1alpha1.OwnedResource{})
	assert.ElementsMatch(t, []v1alpha1.OwnedResource{ref1Stale, ref2Stale}, stale)
	assert.ElementsMatch(t, []v1alpha1.OwnedResource{ref1Stale, ref2Stale}, combined)

	// Difference present
	combined, stale = markStaleResources([]v1alpha1.OwnedResource{ref1, ref2}, []v1alpha1.OwnedResource{ref1, ref3})
	assert.ElementsMatch(t, []v1alpha1.OwnedResource{ref2Stale}, stale)
	assert.ElementsMatch(t, []v1alpha1.OwnedResource{ref1, ref2Stale, ref3}, combined)

	// No difference
	combined, stale = markStaleResources([]v1alpha1.OwnedResource{ref1, ref2}, []v1alpha1.OwnedResource{ref1, ref2})
	assert.ElementsMatch(t, []v1alpha1.OwnedResource{}, stale)
	assert.ElementsMatch(t, []v1alpha1.OwnedResource{ref1, ref2}, combined)
}

func TestResourceToReference(t *testing.T) {
	// empty
	assert.ElementsMatchf(t,
		[]v1alpha1.OwnedResource{},
		resourceToReference(),
		"")

	// one
	object1 := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "EnvoyFilter",
			"metadata": map[string]interface{}{
				"name":      "filter-1",
				"namespace": "test-ns1",
				"uid":       "uid-1",
			},
		},
	}
	reference1 := v1alpha1.OwnedResource{
		ApiVersion: "networking.istio.io/v1alpha3",
		Kind:       "EnvoyFilter",
		Name:       "filter-1",
		Namespace:  "test-ns1",
		UID:        "uid-1",
	}
	assert.ElementsMatchf(t,
		[]v1alpha1.OwnedResource{reference1},
		resourceToReference(&object1),
		"")

	// many
	object2 := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      "vs-2",
				"namespace": "test-ns2",
				"uid":       "uid-2",
			},
		},
	}
	reference2 := v1alpha1.OwnedResource{
		ApiVersion: "networking.istio.io/v1alpha3",
		Kind:       "VirtualService",
		Name:       "vs-2",
		Namespace:  "test-ns2",
		UID:        "uid-2",
	}
	assert.ElementsMatchf(t,
		[]v1alpha1.OwnedResource{reference1, reference2},
		resourceToReference(&object1, &object2),
		"")
}

func TestServiceToReference(t *testing.T) {
	var clusterName = "cluster-1"
	var service = kube_test.CreateService("test-namespace", "svc1")
	service.UID = "abcd-123"

	assert.Equal(t,
		&v1alpha1.Owner{
			Cluster:    clusterName,
			ApiVersion: "v1",
			Kind:       "Service",
			Name:       "svc1",
			UID:        "abcd-123",
		},
		ServiceToReference(clusterName, service))
}

func TestServiceEntryToReference(t *testing.T) {
	var clusterName = "cluster1"
	var serviceEntry = istiov1alpha3.ServiceEntry{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "se1",
			Namespace: "test-namespace",
		},
		Spec: networkingv1alpha3.ServiceEntry{
			Hosts:    []string{"host-1.com"},
			Location: networkingv1alpha3.ServiceEntry_MESH_INTERNAL,
		},
	}
	serviceEntry.UID = "test-uid-123"

	assert.Equal(t,
		&v1alpha1.Owner{
			Cluster:    clusterName,
			ApiVersion: "networking.istio.io/v1alpha3",
			Kind:       constants.ServiceEntryKind.Kind,
			Name:       "se1",
			UID:        "test-uid-123",
		},
		ServiceEntryToReference(clusterName, &serviceEntry))
}

func TestRemoveServiceFromOwnersList(t *testing.T) {
	var clusterName = "cluster-1"
	var serviceName = "svc1"
	var uid = types.UID("abcd-123")
	var kind = "Service"

	matchingRef := v1alpha1.Owner{
		Cluster: clusterName,
		Kind:    kind,
		Name:    serviceName,
		UID:     uid,
	}
	nonMatchingRef := v1alpha1.Owner{
		Cluster: "other-cluster",
		Kind:    kind,
		Name:    "svc1",
		UID:     "xyz-123",
	}
	testCases := []struct {
		name           string
		references     []v1alpha1.Owner
		expectedResult []v1alpha1.Owner
	}{
		{
			name:           "Reference removed",
			references:     []v1alpha1.Owner{matchingRef, nonMatchingRef},
			expectedResult: []v1alpha1.Owner{nonMatchingRef},
		},
		{
			name:           "Reference to service not present",
			references:     []v1alpha1.Owner{nonMatchingRef},
			expectedResult: []v1alpha1.Owner{nonMatchingRef},
		},
		{
			name:           "Empty references list",
			references:     []v1alpha1.Owner{},
			expectedResult: []v1alpha1.Owner{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualResult := removeOwnerFromOwnersList(clusterName, kind, serviceName, uid, tc.references)

			assert.Equal(t, tc.expectedResult, actualResult)
		})
	}
}

func TestRemoveServiceEntryFromOwnersList(t *testing.T) {
	var clusterName = "cluster-1"
	var seName = "se1"
	var uid = types.UID("abcd-123")
	var kind = "ServiceEntry"

	matchingRef := v1alpha1.Owner{
		Cluster: clusterName,
		Kind:    kind,
		Name:    seName,
		UID:     uid,
	}
	nonMatchingRef := v1alpha1.Owner{
		Cluster: clusterName,
		Kind:    kind,
		Name:    "whatever",
		UID:     "xyz-123",
	}
	testCases := []struct {
		name           string
		references     []v1alpha1.Owner
		expectedResult []v1alpha1.Owner
	}{
		{
			name:           "Reference removed",
			references:     []v1alpha1.Owner{matchingRef, nonMatchingRef},
			expectedResult: []v1alpha1.Owner{nonMatchingRef},
		},
		{
			name:           "Reference to service entry not present",
			references:     []v1alpha1.Owner{nonMatchingRef},
			expectedResult: []v1alpha1.Owner{nonMatchingRef},
		},
		{
			name:           "Empty references list",
			references:     []v1alpha1.Owner{},
			expectedResult: []v1alpha1.Owner{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualResult := removeOwnerFromOwnersList(clusterName, kind, seName, uid, tc.references)
			assert.Equal(t, tc.expectedResult, actualResult)
		})
	}
}

func TestIsReferenceToTheOwner(t *testing.T) {
	var (
		uid         types.UID = "uid1"
		name                  = "name1"
		clusterName           = "clusterName"
	)
	testCases := []struct {
		name        string
		clusterName string
		kind        string
		ownerName   string
		ownerUid    types.UID
		reference   v1alpha1.Owner
		expected    bool
	}{
		{
			name:        "SameOwnerReference - Service kind with matching fields",
			clusterName: clusterName,
			kind:        constants.ServiceKind.Kind,
			ownerName:   name,
			ownerUid:    uid,
			reference: v1alpha1.Owner{
				Cluster: clusterName,
				Kind:    constants.ServiceKind.Kind,
				Name:    name,
				UID:     uid,
			},
			expected: true,
		},
		{
			name:        "SameOwnerReference - Service entry kind with matching fields",
			clusterName: clusterName,
			kind:        "ServiceEntry",
			ownerName:   name,
			ownerUid:    uid,
			reference: v1alpha1.Owner{
				Cluster: "clusterName",
				Kind:    constants.ServiceEntryKind.Kind,
				Name:    name,
				UID:     uid,
			},
			expected: true,
		},
		{
			name:        "OwnerReferenceMismatch - Kind mismatch",
			clusterName: clusterName,
			kind:        "ServiceEntry",
			ownerName:   name,
			ownerUid:    uid,
			reference: v1alpha1.Owner{
				Cluster: clusterName,
				Kind:    "Service",
				Name:    name,
				UID:     uid,
			},
		},
		{
			name:        "OwnerReferenceMismatch - cluster field mismatch",
			clusterName: clusterName,
			kind:        constants.ServiceKind.Kind,
			ownerName:   name,
			ownerUid:    uid,
			reference: v1alpha1.Owner{
				Cluster: "some-other-cluster",
				Kind:    constants.ServiceKind.Kind,
				Name:    name,
				UID:     uid,
			},
		},
		{
			name:        "OwnerReferenceMismatch - name field mismatch",
			clusterName: clusterName,
			kind:        constants.ServiceKind.Kind,
			ownerName:   name,
			ownerUid:    uid,
			reference: v1alpha1.Owner{
				Cluster: clusterName,
				Kind:    constants.ServiceKind.Kind,
				Name:    "some-other-name",
				UID:     uid,
			},
		},
		{
			name:        "OwnerReferenceMismatch - uid field mismatch",
			clusterName: clusterName,
			kind:        constants.ServiceKind.Kind,
			ownerName:   name,
			ownerUid:    uid,
			reference: v1alpha1.Owner{
				Cluster: clusterName,
				Kind:    constants.ServiceKind.Kind,
				Name:    name,
				UID:     "some-other-uid",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := isReferenceToTheOwner(tc.clusterName, tc.kind, tc.ownerName, tc.ownerUid, tc.reference)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
