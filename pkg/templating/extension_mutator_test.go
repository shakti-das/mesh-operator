package templating

import (
	"reflect"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/hash"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"

	kubetest "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestFilterHashConfigMutator(t *testing.T) {
	var one int32 = 1
	mopName := "mop-1"
	svc1Namespace := "test"
	svc1Name := "svc1"
	knownSvcHash := hash.ComputeHashForName(nil, svc1Name)
	knownConfigNsSvcHash := hash.ComputeHashForName(nil, svc1Namespace, svc1Name)
	knownCollisionBypassSvcHash := hash.ComputeHashForName(&one, svc1Name)
	knownConfigNsCollisionBypassSvcHash := hash.ComputeHashForName(&one, svc1Namespace, svc1Name)
	knownNsWideConfigNsHash := hash.ComputeHashForName(nil, "test-namespace", "")
	knownNsWideConfigNsBypassHash := hash.ComputeHashForName(&one, "test-namespace", "")

	knownFilterName := mopName + "-" + knownSvcHash + "-0"
	knownConfigNsFilterName := mopName + "-" + knownConfigNsSvcHash + "-0"
	knownNsWideConfigNsFilterName := mopName + "-" + knownNsWideConfigNsHash + "-0"
	svc := kubetest.CreateServiceAsUnstructuredObject(svc1Namespace, svc1Name)

	testCases := []struct {
		name                    string
		configObjectKind        string
		mop                     *v1alpha1.MeshOperator
		service                 *unstructured.Unstructured
		objectsInCluster        []runtime.Object
		isConfigNamespaceObject bool

		expectedServiceHash   string
		expectedNamespaceHash string
	}{
		{
			name:                "Non filter config object accepted",
			configObjectKind:    "VirtualService",
			mop:                 kubetest.NewMopBuilder("test", mopName).Build(),
			service:             svc,
			expectedServiceHash: knownSvcHash,
		},
		{
			name:                "Namespace level filter",
			configObjectKind:    constants.EnvoyFilterKind.Kind,
			mop:                 kubetest.NewMopBuilder("test", mopName).Build(),
			expectedServiceHash: "",
		},
		{
			name:                "ServiceHash is known",
			configObjectKind:    constants.EnvoyFilterKind.Kind,
			mop:                 kubetest.NewMopBuilder("test", "mop-1").SetServiceHash("svc1", "known-hash").Build(),
			service:             svc,
			expectedServiceHash: "known-hash",
		},
		{
			name:                  "NamespaceHash is known",
			configObjectKind:      constants.EnvoyFilterKind.Kind,
			mop:                   kubetest.NewMopBuilder("test", "mop-1").SetNamespaceHash("known-namespace-hash").Build(),
			expectedNamespaceHash: "known-namespace-hash",
		},
		{
			name:                "No collision - no filters exist",
			configObjectKind:    constants.EnvoyFilterKind.Kind,
			mop:                 kubetest.NewMopBuilder("test", mopName).Build(),
			service:             svc,
			expectedServiceHash: knownSvcHash,
		},
		{
			name:             "No collision - filter for same cluster service exists",
			configObjectKind: constants.EnvoyFilterKind.Kind,
			mop:              kubetest.NewMopBuilder("test", mopName).Build(),
			service:          svc,
			objectsInCluster: []runtime.Object{
				kubetest.CreateEnvoyFilter(knownFilterName, "test", "primary-1/svc1"),
			},
			expectedServiceHash: knownSvcHash,
		},
		{
			name:             "No collision - filter for other cluster service exists",
			configObjectKind: constants.EnvoyFilterKind.Kind,
			mop:              kubetest.NewMopBuilder("test", mopName).Build(),
			service:          svc,
			objectsInCluster: []runtime.Object{
				kubetest.CreateEnvoyFilter(knownFilterName, "test", "remote-1/svc1"),
			},
			expectedServiceHash: knownSvcHash,
		},
		{
			name:             "Collision",
			configObjectKind: constants.EnvoyFilterKind.Kind,
			mop:              kubetest.NewMopBuilder("test", mopName).Build(),
			service:          svc,
			objectsInCluster: []runtime.Object{
				kubetest.CreateEnvoyFilter(knownFilterName, "test", "primary-1/svc2"),
			},
			expectedServiceHash: knownCollisionBypassSvcHash,
		},
		{
			name:                    "Config namespace extension - No Collision, no filter exists",
			configObjectKind:        constants.EnvoyFilterKind.Kind,
			mop:                     kubetest.NewMopBuilder("test", mopName).Build(),
			service:                 svc,
			objectsInCluster:        []runtime.Object{},
			isConfigNamespaceObject: true,
			expectedServiceHash:     knownConfigNsSvcHash,
		},
		{
			name:             "Config namespace extension - No collision - filter for same cluster service exists",
			configObjectKind: constants.EnvoyFilterKind.Kind,
			mop:              kubetest.NewMopBuilder("test", mopName).Build(),
			service:          svc,
			objectsInCluster: []runtime.Object{
				kubetest.CreateEnvoyFilter(knownConfigNsFilterName, constants.DefaultConfigNamespace, "primary-1/test/svc1"),
			},
			isConfigNamespaceObject: true,
			expectedServiceHash:     knownConfigNsSvcHash,
		},
		{
			name:             "Config namespace extension - Collision",
			configObjectKind: constants.EnvoyFilterKind.Kind,
			mop:              kubetest.NewMopBuilder("test", mopName).Build(),
			service:          svc,
			objectsInCluster: []runtime.Object{
				kubetest.CreateEnvoyFilter(knownConfigNsFilterName, constants.DefaultConfigNamespace, "primary-1/test/svc2"),
			},
			isConfigNamespaceObject: true,
			expectedServiceHash:     knownConfigNsCollisionBypassSvcHash,
		},
		{
			name:                    "Config namespace - Ns-wide, no collision, no filter exists",
			configObjectKind:        constants.EnvoyFilterKind.Kind,
			mop:                     kubetest.NewMopBuilder("test-namespace", mopName).Build(),
			objectsInCluster:        []runtime.Object{},
			isConfigNamespaceObject: true,
			expectedNamespaceHash:   knownNsWideConfigNsHash,
		},
		{
			name:             "Config namespace - Ns-wide, no collision, filter exists",
			configObjectKind: constants.EnvoyFilterKind.Kind,
			mop:              kubetest.NewMopBuilder("test-namespace", mopName).Build(),
			objectsInCluster: []runtime.Object{
				kubetest.CreateEnvoyFilter(knownNsWideConfigNsFilterName, constants.DefaultConfigNamespace, "primary-1/test-namespace"),
			},
			isConfigNamespaceObject: true,
			expectedNamespaceHash:   knownNsWideConfigNsHash,
		},
		{
			name:             "Config namespace - Ns-wide, collistion",
			configObjectKind: constants.EnvoyFilterKind.Kind,
			mop:              kubetest.NewMopBuilder("test-namespace", mopName).Build(),
			objectsInCluster: []runtime.Object{
				kubetest.CreateEnvoyFilter(knownNsWideConfigNsFilterName, constants.DefaultConfigNamespace, "primary-1/other-namespace"),
			},
			isConfigNamespaceObject: true,
			expectedNamespaceHash:   knownNsWideConfigNsBypassHash,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t).Sugar()
			client := kubetest.NewKubeClientBuilder().AddDynamicClientObjects(tc.objectsInCluster...).Build()
			mutator := ExtensionHashMutator{Logger: logger, KubeClient: client}

			configObject := unstructured.Unstructured{}
			gvk := schema.GroupVersionKind{
				Group:   constants.EnvoyFilterResource.Group,
				Version: constants.EnvoyFilterResource.Version,
				Kind:    tc.configObjectKind,
			}
			configObject.SetGroupVersionKind(gvk)
			configObject.SetName("filter-0")
			if tc.isConfigNamespaceObject {
				configObject.SetAnnotations(map[string]string{constants.ConfigNamespaceExtensionAnnotation: constants.DefaultConfigNamespace})
			}

			ctx := RenderRequestContext{
				Object:       tc.service,
				MeshOperator: tc.mop,
				ClusterName:  "primary-1",
			}

			mutatedConfigObject, err := mutator.Mutate(&ctx, &configObject)
			assert.NoError(t, err)
			assert.True(t, reflect.DeepEqual(&configObject, mutatedConfigObject))

			if tc.expectedServiceHash == "" {
				assert.Nil(t, tc.mop.Status.Services)
			} else {
				assert.Equal(t, tc.expectedServiceHash, tc.mop.Status.Services[tc.service.GetName()].Hash)
			}
			assert.Equal(t, tc.expectedNamespaceHash, tc.mop.Status.NamespaceHash)
		})
	}
}

func TestFilterNameConfigMutator(t *testing.T) {
	namespace := "test-namespace"
	mopName := "mop-1"
	svcName := "svc1"

	testCases := []struct {
		name                    string
		configObjectKind        string
		service                 *unstructured.Unstructured
		hashInMop               string
		renderedConfigtName     string
		renderedConfigNamespace string
		isConfigNamespaceConfig bool
		expectedFilterName      string
		expectedFilterNamespace string
	}{
		{
			name:                    "Non filter config accepted",
			configObjectKind:        "VirtualService",
			renderedConfigtName:     "filter-0",
			renderedConfigNamespace: "test-ns",
			expectedFilterName:      "mop-1-0",
			expectedFilterNamespace: namespace,
		},
		{
			name:                    "Namespace level filter",
			configObjectKind:        constants.EnvoyFilterKind.Kind,
			renderedConfigtName:     "filter-0",
			renderedConfigNamespace: "test-ns",
			service:                 nil,
			hashInMop:               "",
			expectedFilterName:      "mop-1-0",
			expectedFilterNamespace: namespace,
		},
		{
			name:                    "Service level filter",
			configObjectKind:        constants.EnvoyFilterKind.Kind,
			service:                 kubetest.CreateServiceAsUnstructuredObject("test", svcName),
			renderedConfigtName:     "filter-0",
			renderedConfigNamespace: "test-ns",
			hashInMop:               "precalculated-hash",
			expectedFilterName:      "mop-1-precalculated-hash-0",
			expectedFilterNamespace: namespace,
		},
		{
			name:                    "Namespace from template overidden with MOP namespace",
			configObjectKind:        constants.EnvoyFilterKind.Kind,
			renderedConfigtName:     "filter-0",
			renderedConfigNamespace: "test-ns",
			service:                 kubetest.CreateServiceAsUnstructuredObject("test", svcName),
			hashInMop:               "service-hash",
			expectedFilterName:      "mop-1-service-hash-0",
			expectedFilterNamespace: namespace,
		},
		{
			name:                    "Config-ns extension namespace overidden",
			configObjectKind:        constants.EnvoyFilterKind.Kind,
			renderedConfigtName:     "FaultFilter-0",
			renderedConfigNamespace: "test-ns",
			isConfigNamespaceConfig: true,
			service:                 kubetest.CreateServiceAsUnstructuredObject("test", svcName),
			hashInMop:               "service-hash",
			expectedFilterName:      "mop-1-service-hash-0",
			expectedFilterNamespace: constants.DefaultConfigNamespace,
		},
		{
			name:                    "Ns-wide mop with config-ns extension",
			configObjectKind:        constants.EnvoyFilterKind.Kind,
			renderedConfigtName:     "FaultFilter-0",
			renderedConfigNamespace: "test-ns",
			isConfigNamespaceConfig: true,
			service:                 nil,
			hashInMop:               "namespace-hash",
			expectedFilterName:      "mop-1-namespace-hash-0",
			expectedFilterNamespace: constants.DefaultConfigNamespace,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mop := kubetest.NewMopBuilder(namespace, mopName).SetServiceHash(svcName, tc.hashInMop).Build()
			ctx := RenderRequestContext{Object: tc.service, MeshOperator: mop}
			configObject := unstructured.Unstructured{}
			configObject.SetKind(tc.configObjectKind)
			configObject.SetName(tc.renderedConfigtName)
			configObject.SetNamespace(tc.renderedConfigNamespace)
			if tc.isConfigNamespaceConfig {
				configObject.SetAnnotations(map[string]string{constants.ConfigNamespaceExtensionAnnotation: constants.DefaultConfigNamespace})
				mop.Status.NamespaceHash = tc.hashInMop
			}

			mutator := ExtensionNameMutator{}
			mutatedConfigObject, err := mutator.Mutate(&ctx, &configObject)

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedFilterName, mutatedConfigObject.GetName())
			assert.Equal(t, tc.expectedFilterNamespace, mutatedConfigObject.GetNamespace())
		})
	}
}

func TestFilterSourceConfigMutator(t *testing.T) {
	primaryClusterName := "primary-1"
	mopName := "mop-1"
	svcName := "svc1"
	filterName := "mop-1-svc-hash-0"
	svc := kubetest.CreateServiceAsUnstructuredObject("test", svcName)

	testCases := []struct {
		name                   string
		configObjectApiVersion string
		configObjectKind       string
		objectsInCluster       []runtime.Object
		expectedFilterSource   string
	}{
		{
			name:                   "Non filter config accepted",
			configObjectApiVersion: constants.VirtualServiceResource.GroupVersion().String(),
			configObjectKind:       constants.VirtualServiceKind.Kind,
			objectsInCluster:       []runtime.Object{},
			expectedFilterSource:   "primary-1/svc1",
		},
		{
			name:                   "New filter added",
			configObjectApiVersion: constants.EnvoyFilterResource.GroupVersion().String(),
			configObjectKind:       constants.EnvoyFilterKind.Kind,
			objectsInCluster:       []runtime.Object{},
			expectedFilterSource:   "primary-1/svc1",
		},
		{
			name:                   "Filter updated",
			configObjectApiVersion: constants.EnvoyFilterResource.GroupVersion().String(),
			configObjectKind:       constants.EnvoyFilterKind.Kind,
			objectsInCluster: []runtime.Object{
				kubetest.CreateEnvoyFilter(filterName, "test", "primary-1/svc1"),
			},
			expectedFilterSource: "primary-1/svc1",
		},
		{
			name:                   "Filter added to another cluster",
			configObjectApiVersion: constants.EnvoyFilterResource.GroupVersion().String(),
			configObjectKind:       constants.EnvoyFilterKind.Kind,
			objectsInCluster: []runtime.Object{
				kubetest.CreateEnvoyFilter(filterName, "test", "remote-1/svc1"),
			},
			expectedFilterSource: "remote-1/svc1,primary-1/svc1",
		},
		{
			configObjectApiVersion: constants.EnvoyFilterResource.GroupVersion().String(),
			configObjectKind:       constants.EnvoyFilterKind.Kind,
			objectsInCluster: []runtime.Object{
				kubetest.CreateEnvoyFilter(filterName, "test", "remote-1/svc1,primary-1/svc1"),
			},
			expectedFilterSource: "remote-1/svc1,primary-1/svc1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := kubetest.NewKubeClientBuilder().AddDynamicClientObjects(tc.objectsInCluster...).Build()
			mop := kubetest.NewMopBuilder("test", mopName).Build()
			ctx := RenderRequestContext{Object: svc, MeshOperator: mop, ClusterName: primaryClusterName}

			configObject := unstructured.Unstructured{}
			configObject.SetAPIVersion(tc.configObjectApiVersion)
			configObject.SetKind(tc.configObjectKind)
			configObject.SetName(filterName)
			mutator := ExtensionSourceMutator{KubeClient: client}

			mutatedConfigObject, err := mutator.Mutate(&ctx, &configObject)

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedFilterSource, mutatedConfigObject.GetAnnotations()[constants.ExtensionSourceAnnotation])
		})
	}
}
