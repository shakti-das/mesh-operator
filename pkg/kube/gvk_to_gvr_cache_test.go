package kube

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestConvertGvkToGvr(t *testing.T) {
	testCases := []struct {
		name        string
		resources   []*metav1.APIResourceList
		gvk         schema.GroupVersionKind
		expectedGvr schema.GroupVersionResource
		expectedErr string
	}{
		{
			name:        "MissingResourceGroupVersion",
			resources:   []*metav1.APIResourceList{},
			gvk:         schema.GroupVersionKind{Group: "test.networking.istio.io", Version: "v1alpha3", Kind: "EnvoyFilter"},
			expectedGvr: schema.GroupVersionResource{},
			expectedErr: "failed to get kubernetes resources for GroupVersion = \"test.networking.istio.io/v1alpha3\": the server could not find the requested resource, GroupVersion \"test.networking.istio.io/v1alpha3\" not found",
		},
		{
			name: "MissingResourceKind",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: schema.GroupVersion{Group: "test.networking.istio.io", Version: "v1alpha3"}.String(),
				},
			},
			gvk:         schema.GroupVersionKind{Group: "test.networking.istio.io", Version: "v1alpha3", Kind: "EnvoyFilter"},
			expectedGvr: schema.GroupVersionResource{},
			expectedErr: "failed to get kubernetes resource for GroupVersionKind = \"test.networking.istio.io/v1alpha3, Kind=EnvoyFilter\": no matching resource found for given kind",
		},
		{
			name: "AllResourcesExist",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: schema.GroupVersion{Group: "test.networking.istio.io", Version: "v1alpha3"}.String(),
					APIResources: []metav1.APIResource{
						{Name: "envoyfilter", Kind: "EnvoyFilter"},
					},
				},
			},
			gvk:         schema.GroupVersionKind{Group: "test.networking.istio.io", Version: "v1alpha3", Kind: "EnvoyFilter"},
			expectedGvr: schema.GroupVersionResource{Group: "test.networking.istio.io", Version: "v1alpha3", Resource: "envoyfilter"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			discovery := &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{}}

			discovery.Resources = tc.resources

			actualGvr, err := ConvertGvkToGvr("test-cluster", discovery, tc.gvk)

			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedErr)
			}

			assert.Equal(t, tc.expectedGvr, actualGvr)
		})
	}
}

func TestGvkToGvrCache(t *testing.T) {

	fakeDiscoveryClient := &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{}}
	fakeDiscoveryClient.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: schema.GroupVersion{Group: "test.networking.istio.io", Version: "v1alpha3"}.String(),
			APIResources: []metav1.APIResource{
				{Name: "envoyfilter", Kind: "EnvoyFilter"},
			},
		},
	}

	otherDiscoveryClient := &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{}}
	otherDiscoveryClient.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: schema.GroupVersion{Group: "test.networking.istio.io", Version: "v1alpha3"}.String(),
			APIResources: []metav1.APIResource{
				{Name: "other-envoyfilter", Kind: "EnvoyFilter"},
			},
		},
	}

	clusterName := "cluster1"
	otherClusterName := "other-cluster1"
	gvk := schema.GroupVersionKind{Group: "test.networking.istio.io", Version: "v1alpha3", Kind: "EnvoyFilter"}
	expectedGvr := schema.GroupVersionResource{Group: "test.networking.istio.io", Version: "v1alpha3", Resource: "envoyfilter"}
	otherExpectedGvr := schema.GroupVersionResource{Group: "test.networking.istio.io", Version: "v1alpha3", Resource: "other-envoyfilter"}

	actualGvr, err := ConvertGvkToGvr(clusterName, fakeDiscoveryClient, gvk) // Stores gvr in cache
	assert.NoError(t, err)
	assert.Equal(t, expectedGvr, actualGvr)

	cachedGvr, err := ConvertGvkToGvr(clusterName, nil, gvk) // Fetches gvr from cache
	assert.NoError(t, err)
	assert.Equal(t, expectedGvr, cachedGvr)

	otherGvr, err := ConvertGvkToGvr(otherClusterName, otherDiscoveryClient, gvk) // Stores gvr in cache (other cluster cache)
	assert.NoError(t, err)
	assert.Equal(t, otherExpectedGvr, otherGvr)

	otherCachedGvr, err := ConvertGvkToGvr(otherClusterName, nil, gvk) // Fetches gvr from cache (other cluster cache)
	assert.NoError(t, err)
	assert.Equal(t, otherExpectedGvr, otherCachedGvr)
}
