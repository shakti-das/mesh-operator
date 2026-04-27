package hash

import (
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
)

func TestReadHash(t *testing.T) {
	testCases := []struct {
		name                     string
		mop                      *v1alpha1.MeshOperator
		service                  *unstructured.Unstructured
		configNamespaceExtension bool
		expectedHash             string
	}{
		{
			name:    "ServiceHashStoreIsNil",
			mop:     kube_test.NewMopBuilder("test", "mop").Build(),
			service: kube_test.CreateServiceAsUnstructuredObject("test", "svc1"),
		},
		{
			name:         "ContainsServiceHash",
			mop:          kube_test.NewMopBuilder("test", "mop").SetServiceHash("svc1", "123456789").Build(),
			service:      kube_test.CreateServiceAsUnstructuredObject("test", "svc1"),
			expectedHash: "123456789",
		},
		{
			name:         "DoesNotContainsServiceHash",
			mop:          kube_test.NewMopBuilder("test", "mop").SetServiceHash("svc1", "123456789").Build(),
			service:      kube_test.CreateServiceAsUnstructuredObject("test", "svc2"),
			expectedHash: "",
		},
		{
			name:         "Ns-wide",
			mop:          kube_test.NewMopBuilder("test", "mop").Build(),
			service:      nil,
			expectedHash: "",
		},
		{
			name:         "Ns-wide MOP, non config-ns extension",
			mop:          kube_test.NewMopBuilder("test", "mop").SetNamespaceHash("namespace-hash").Build(),
			service:      nil,
			expectedHash: "",
		},
		{
			name:                     "Ns-wide, config-ns extension",
			mop:                      kube_test.NewMopBuilder("test", "mop").SetNamespaceHash("namespace-hash").Build(),
			service:                  nil,
			configNamespaceExtension: true,
			expectedHash:             "namespace-hash",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			serviceHash := ReadHash(tc.configNamespaceExtension, tc.mop, tc.service)
			assert.Equal(t, tc.expectedHash, serviceHash)
		})
	}
}

func TestUpdateMopHashMap(t *testing.T) {

	testCases := []struct {
		name       string
		mop        *v1alpha1.MeshOperator
		kind       string
		objectName string
		objectHash string
	}{
		{
			name:       "UpdateHashStoreMap - for Service",
			mop:        kube_test.NewMopBuilder("test", "mop").Build(),
			kind:       constants.ServiceKind.Kind,
			objectName: "svc",
			objectHash: "1234",
		},
		{
			name:       "UpdateHashStoreMap with new hash - for Service",
			mop:        kube_test.NewMopBuilder("test", "mop").SetServiceHash("svc", "987654321").Build(),
			kind:       constants.ServiceKind.Kind,
			objectName: "svc",
			objectHash: "1234",
		},
		{
			name:       "UpdateHashStoreMap - for ServiceEntry",
			mop:        kube_test.NewMopBuilder("test", "mop").Build(),
			kind:       constants.ServiceEntryKind.Kind,
			objectName: "testSe",
			objectHash: "1234",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			UpdateMopHashMap(tc.mop, tc.kind, tc.objectName, tc.objectHash)
			if tc.kind == constants.ServiceKind.Kind {
				assert.Equal(t, tc.objectHash, tc.mop.Status.Services[tc.objectName].Hash)
			} else {
				assert.Equal(t, tc.objectHash, tc.mop.Status.ServiceEntries[tc.objectName].Hash)
			}
		})
	}
}

func TestGetExtensionSource(t *testing.T) {
	features.EnableServiceEntryExtension = true
	defer func() {
		features.EnableServiceEntryExtension = false
	}()

	svc := kube_test.CreateServiceAsUnstructuredObject("test-ns", "svc1")

	// assert for service object
	assert.Equal(t, "primary1/svc1", GetExtensionSource("primary1", "test-ns", svc, false))
	assert.Equal(t, "primary1", GetExtensionSource("primary1", "test-ns", nil, false))
	assert.Equal(t, "primary1/test-ns/svc1", GetExtensionSource("primary1", "test-ns", svc, true))
	assert.Equal(t, "primary1/test-ns", GetExtensionSource("primary1", "test-ns", nil, true))

	// for non-service object, assert extension source annotation
	se := kube_test.NewUnstructuredBuilder(constants.ServiceEntryResource, constants.ServiceEntryKind, "se1", "test-ns").Build()
	assert.Equal(t, "primary1/se1/ServiceEntry", GetExtensionSource("primary1", "test-ns", se, false))
	assert.Equal(t, "primary1/test-ns/se1/ServiceEntry", GetExtensionSource("primary1", "test-ns", se, true))
	assert.Equal(t, "primary1/test-ns", GetExtensionSource("primary1", "test-ns", nil, true))

}

func TestRemoveFilterSourceForCluster(t *testing.T) {
	testCases := []struct {
		name                        string
		isConfigNsResource          bool
		clusterName                 string
		existingExtensionSource     string
		expectedNewElements         string
		expectedExistingSourceFound bool
	}{
		{
			name:        "empty existing source",
			clusterName: "primary1",
		},
		{
			name:                        "cluster name in source",
			clusterName:                 "primary1",
			existingExtensionSource:     "primary1",
			expectedExistingSourceFound: true,
		},
		{
			name:                        "cluster + svc in source",
			clusterName:                 "primary1",
			existingExtensionSource:     "primary1/svc1",
			expectedExistingSourceFound: true,
		},
		{
			name:                        "cluster: first primary then remote in source",
			clusterName:                 "primary1",
			existingExtensionSource:     "primary1,remote1",
			expectedNewElements:         "remote1",
			expectedExistingSourceFound: true,
		},
		{
			name:                        "cluster: first remote then primary in source",
			clusterName:                 "primary1",
			existingExtensionSource:     "remote1,primary1",
			expectedNewElements:         "remote1",
			expectedExistingSourceFound: true,
		},
		{
			name:                        "cluster + svc: first primary then remote in source, remove primary",
			clusterName:                 "primary1",
			existingExtensionSource:     "primary1/svc1,remote1/svc1",
			expectedNewElements:         "remote1/svc1",
			expectedExistingSourceFound: true,
		},
		{
			name:                        "cluster + svc: first primary then remote in source, remove primary",
			clusterName:                 "primary1",
			existingExtensionSource:     "remote1/svc1,primary1/svc1",
			expectedNewElements:         "remote1/svc1",
			expectedExistingSourceFound: true,
		},
		{
			name:                        "cluster + svc: first primary then remote in source, remove remote",
			clusterName:                 "sam-processing1-2",
			existingExtensionSource:     "sam-processing1/svc1,sam-processing1-2/svc1",
			expectedNewElements:         "sam-processing1/svc1",
			expectedExistingSourceFound: true,
		},
		{
			name:                        "cluster + svc: first remote then primary in source, remove remote",
			clusterName:                 "sam-processing1-2",
			existingExtensionSource:     "sam-processing1-2/svc1,sam-processing1/svc1",
			expectedNewElements:         "sam-processing1/svc1",
			expectedExistingSourceFound: true,
		},
		{
			name:                    "no matches",
			clusterName:             "remote1",
			existingExtensionSource: "primary1/some-other-ns/svc1",
			expectedNewElements:     "primary1/some-other-ns/svc1",
		},
		{
			name:               "config ns: no existing source",
			isConfigNsResource: true,
			clusterName:        "primary1",
		},
		{
			name:                    "config ns: no matches",
			isConfigNsResource:      true,
			clusterName:             "primary1",
			existingExtensionSource: "primary1/some-other-ns/svc1",
			expectedNewElements:     "primary1/some-other-ns/svc1",
		},
		{
			name:                        "config ns: cluster/ns in sources",
			isConfigNsResource:          true,
			clusterName:                 "primary1",
			existingExtensionSource:     "primary1/test-svc-ns",
			expectedExistingSourceFound: true,
		},
		{
			name:                        "config ns: cluster/ns/svc in sources",
			isConfigNsResource:          true,
			clusterName:                 "primary1",
			existingExtensionSource:     "primary1/test-svc-ns/svc1",
			expectedExistingSourceFound: true,
		},
		{
			name:                        "config ns, cluster/ns: primary then remote in source, remove primary",
			isConfigNsResource:          true,
			clusterName:                 "primary1",
			existingExtensionSource:     "primary1/test-svc-ns,remote1/test-svc-ns",
			expectedExistingSourceFound: true,
			expectedNewElements:         "remote1/test-svc-ns",
		},
		{
			name:                        "config ns, cluster/ns: remote then primary in source, remove primary",
			isConfigNsResource:          true,
			clusterName:                 "primary1",
			existingExtensionSource:     "remote1/test-svc-ns,primary1/test-svc-ns",
			expectedExistingSourceFound: true,
			expectedNewElements:         "remote1/test-svc-ns",
		},
		{
			name:                        "config ns, cluster/ns/svc: primary then remote in source, remove primary",
			isConfigNsResource:          true,
			clusterName:                 "primary1",
			existingExtensionSource:     "primary1/test-svc-ns/svc1,remote1/test-svc-ns/svc1",
			expectedExistingSourceFound: true,
			expectedNewElements:         "remote1/test-svc-ns/svc1",
		},
		{
			name:                        "config ns, cluster/ns/svc: remote then primary in source, remove primary",
			isConfigNsResource:          true,
			clusterName:                 "primary1",
			existingExtensionSource:     "remote1/test-svc-ns/svc1,primary1/test-svc-ns/svc1",
			expectedExistingSourceFound: true,
			expectedNewElements:         "remote1/test-svc-ns/svc1",
		},
		{
			name:                        "config ns, cluster/ns/svc: multiple namespaces in sources",
			isConfigNsResource:          true,
			clusterName:                 "primary1",
			existingExtensionSource:     "remote1/test-svc-ns/svc1,primary/other-test-ns/svc1,primary1/test-svc-ns/svc1",
			expectedExistingSourceFound: true,
			expectedNewElements:         "remote1/test-svc-ns/svc1,primary/other-test-ns/svc1",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			newExtensionSource, found := RemoveExtensionSourceForCluster(tc.isConfigNsResource, tc.clusterName, "test-svc-ns", tc.existingExtensionSource)
			assert.Equal(t, tc.expectedNewElements, newExtensionSource)
			assert.Equal(t, tc.expectedExistingSourceFound, found)
		})
	}
}

func TestIsExtensionSourceConflicts(t *testing.T) {
	tests := []struct {
		name                     string
		configNamespaceExtension bool
		objectKind               string
		objectNamespace          string
		objectName               string
		extensionSourceInCluster string
		expected                 bool
	}{
		{
			name:                     "Two Service with conflicting hash - conflict detected",
			configNamespaceExtension: false,
			objectKind:               "Service",
			objectNamespace:          "default",
			objectName:               "somservice",
			extensionSourceInCluster: "cluster/testservice",
			expected:                 true,
		},
		{
			name:                     "Two SE with conflicting hash - conflict detected",
			configNamespaceExtension: false,
			objectKind:               "ServiceEntry",
			objectNamespace:          "default",
			objectName:               "se2",
			extensionSourceInCluster: "cluster/se1/ServiceEntry",
			expected:                 true,
		},
		{
			name:                     "Two SE with same name in different ns + configNamespace extension - conflict detected",
			configNamespaceExtension: true,
			objectKind:               "ServiceEntry",
			objectNamespace:          "ns2",
			objectName:               "se1",
			extensionSourceInCluster: "cluster/ns1/se1/ServiceEntry",
			expected:                 true,
		},
		{
			name:                     "SE extension enabled + configNamespace extension, same hash generated for SE and Service with same/namespace, SE extension exist in cluster - conflict detected",
			configNamespaceExtension: true,
			objectKind:               "Service",
			objectNamespace:          "default",
			objectName:               "testresource",
			extensionSourceInCluster: "cluster/default/testresource/ServiceEntry",
			expected:                 true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsExtensionSourceConflicts(tt.configNamespaceExtension, tt.objectKind, tt.objectNamespace, tt.objectName, tt.extensionSourceInCluster)
			assert.Equal(t, tt.expected, result)
		})
	}
}
