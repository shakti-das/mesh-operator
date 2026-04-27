package templating

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/ocm"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	k8stesting "k8s.io/client-go/testing"

	kubetest "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestPatchServiceRenderedResources(t *testing.T) {
	svc1 := "svc1"
	filter1 := kubetest.CreateEnvoyFilter("filter1", "test", svc1)

	testCases := []struct {
		name                string
		objectsInCluster    []runtime.Object
		patchObjects        []*unstructured.Unstructured
		upsertError         error
		expectedErrorPrefix string
	}{
		{
			name:         "PatchSvcRenderedResource",
			patchObjects: []*unstructured.Unstructured{filter1},
		},
		{
			name: "UpdateSameError",
			objectsInCluster: []runtime.Object{
				filter1,
			},
			patchObjects: []*unstructured.Unstructured{filter1},
		},
		{
			name:                "UpsertError",
			patchObjects:        []*unstructured.Unstructured{filter1},
			upsertError:         errors.New("object creation failed"),
			expectedErrorPrefix: "failed to upsert object",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t).Sugar()

			client := kubetest.NewKubeClientBuilder().AddDynamicClientObjects(tc.objectsInCluster...).Build()
			primaryClient := client

			fakeClient := (client).(*kubetest.FakeClient)

			if tc.upsertError != nil {
				fakeClient.DynamicClient.PrependReactor("create", constants.EnvoyFilterResource.Resource,
					func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, errors.New("failed to upsert object: object creation failed")
					})
			}

			fieldManager := &kube.ArgoFieldManager{}
			k8sApplicator := k8sApplicator{logger: logger, primaryClient: primaryClient, retainFieldManager: fieldManager}
			results, err := k8sApplicator.patchResources(tc.patchObjects, logger, fieldManager)

			// assert error
			if tc.expectedErrorPrefix != "" {
				assert.Nil(t, err)
				assert.NotNil(t, results[0].Error)
				assert.Equal(t, strings.Contains(results[0].Error.Error(), tc.expectedErrorPrefix), true)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestAddGeneratedByWithObjects(t *testing.T) {
	filter := kubetest.CreateEnvoyFilter("filter1", "test", "some-svc")
	result := addGeneratedBy([]*unstructured.Unstructured{filter}, "template_name")
	resultAsArray, ok := result.([]*unstructured.Unstructured)

	assert.True(t, ok)
	assert.Equal(t, 1, len(resultAsArray))

	generatedByLabel := resultAsArray[0].GetLabels()["$generatedBy"]
	assert.Equal(t, "template_name", generatedByLabel)
}

func TestAddGeneratedByEmpty(t *testing.T) {
	result := addGeneratedBy([]*unstructured.Unstructured{}, "template_name")
	resultAsStruct, ok := result.([]emptyElement)

	assert.True(t, ok)
	assert.Equal(t, 1, len(resultAsStruct))
	assert.Equal(t, emptyElement{GeneratedBy: "Empty output from: template_name"}, resultAsStruct[0])
}

func TestPatchVSWithArgoFields(t *testing.T) {
	testNS := "test-ns"
	testVS := "test-vs"
	ocmManagedSvc := kubetest.CreateService("test-ns", "ocm-managed-svc")
	ocmManagedSvc.SetLabels(map[string]string{
		ocm.ManagedByLabel: ocm.ManagedByValue,
	})
	ocmManagedSvcUnstructured, _ := kube.ObjectToUnstructured(ocmManagedSvc)

	routesWithWeights := []interface{}{
		map[string]interface{}{
			"name": "route-1",
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v2",
					},
					"weight": int64(25),
				},
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v1",
					},
					"weight": int64(75),
				},
			},
		},
		map[string]interface{}{
			"name": "route-2",
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "previews.prod.svc.cluster.local",
						"subset": "v2",
					},
					"weight": int64(0),
				},
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "previews.prod.svc.cluster.local",
						"subset": "v1",
					},
					"weight": int64(100),
				},
			},
		},
	}
	singleRouteWithWeights := []interface{}{
		map[string]interface{}{
			"name": "route-1",
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v2",
					},
					"weight": int64(100),
				},
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v1",
					},
					"weight": int64(0),
				},
			},
		},
	}

	routesNoWeights := []interface{}{
		map[string]interface{}{
			"name": "route-1",
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v2",
					},
				},
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v1",
					},
				},
			},
		},
		map[string]interface{}{
			"name": "route-2",
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "previews.prod.svc.cluster.local",
						"subset": "v2",
					},
				},
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "previews.prod.svc.cluster.local",
						"subset": "v1",
					},
				},
			},
		},
	}

	mixedResultsRoute := []interface{}{
		map[string]interface{}{
			"name": "route-1",
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v2",
					},
					"weight": int64(100),
				},
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v1",
					},
					"weight": int64(0),
				},
			},
		},
		map[string]interface{}{
			"name": "route-2",
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "previews.prod.svc.cluster.local",
						"subset": "v2",
					},
				},
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "previews.prod.svc.cluster.local",
						"subset": "v1",
					},
				},
			},
		},
	}

	routesWithNoName := []interface{}{
		map[string]interface{}{
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v2",
					},
					"weight": int64(25),
				},
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v1",
					},
					"weight": int64(75),
				},
			},
		},
	}

	brokenRoutes := []interface{}{
		map[string]interface{}{
			"name": "route-1",
			"route": []interface{}{
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v2",
					},
					"weight": int64(25),
				},
				map[string]interface{}{
					"destination": map[string]interface{}{
						"host":   "reviews.prod.svc.cluster.local",
						"subset": "v1",
					},
					"weight": int64(75),
				},
			},
		},
		map[string]interface{}{
			"name":  "route-2",
			"route": "route",
		},
	}

	originalArgoVS := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      testVS,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "argo-controller",
				},
			},
			"spec": map[string]interface{}{
				"hosts": []interface{}{"reviews.prod.svc.cluster.local"},
				"http":  routesWithWeights,
			},
		},
	}

	vsNoRouteWeight1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      testVS,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"hosts": []interface{}{"reviews.prod.svc.cluster.local"},
				"http":  routesNoWeights,
			},
		},
	}

	vsSingleRouteWithWeight := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      testVS,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "argo-controller",
				},
			},
			"spec": map[string]interface{}{
				"hosts": []interface{}{"reviews.prod.svc.cluster.local"},
				"http":  singleRouteWithWeights,
			},
		},
	}

	resultVS := originalArgoVS.DeepCopy()
	meshOpLabel := map[string]interface{}{
		"mesh.io/managed-by": "mesh-operator",
	}
	resultVS.Object["metadata"].(map[string]interface{})["labels"] = meshOpLabel

	vsNoRouteWeight2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      testVS,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"hosts": []interface{}{"something-else.prod.svc.cluster.local"},
				"http":  routesNoWeights,
			},
		},
	}

	vsNoRouteWeightResult1 := vsNoRouteWeight1.DeepCopy()
	vsNoRouteWeightResult2 := vsNoRouteWeight2.DeepCopy()
	vsNoRouteWeight3 := vsNoRouteWeight2.DeepCopy()
	vsNoRouteWeight := vsNoRouteWeight1.DeepCopy()
	vsNoRouteWeight4 := vsNoRouteWeight1.DeepCopy()
	vsNoRouteWeight5 := vsNoRouteWeight1.DeepCopy()

	vsRouteWithNoName := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      testVS,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"hosts": []interface{}{"reviews.prod.svc.cluster.local"},
				"http":  routesWithNoName,
			},
		},
	}

	vsBrokenRoute := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      testVS,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"hosts": []interface{}{"reviews.prod.svc.cluster.local"},
				"http":  brokenRoutes,
			},
		},
	}

	vsRouteMixedResult := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      testVS,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"hosts": []interface{}{"reviews.prod.svc.cluster.local"},
				"http":  mixedResultsRoute,
			},
		},
	}

	testCases := []struct {
		name                 string
		enableOcmIntegration bool
		contextObject        *unstructured.Unstructured
		objectsInCluster     []runtime.Object
		patchObjects         []*unstructured.Unstructured
		upsertError          error
		results              []*AppliedConfigObject
	}{
		{
			name: "VS - With Route Weights",
			objectsInCluster: []runtime.Object{
				originalArgoVS,
			},
			patchObjects: []*unstructured.Unstructured{vsNoRouteWeight1},
			results: []*AppliedConfigObject{
				{
					Object: resultVS,
					Error:  nil,
				},
			},
		},
		{
			name: "VS - No Route Weight",
			objectsInCluster: []runtime.Object{
				vsNoRouteWeight,
			},
			patchObjects: []*unstructured.Unstructured{vsNoRouteWeight2},
			results: []*AppliedConfigObject{
				{
					Object: vsNoRouteWeightResult2,
					Error:  nil,
				},
			},
		},
		{
			name: "VS - Skip retaining fields, route name not found",
			objectsInCluster: []runtime.Object{
				vsRouteWithNoName,
			},
			patchObjects: []*unstructured.Unstructured{vsNoRouteWeight3},
			results: []*AppliedConfigObject{
				{
					Object: vsNoRouteWeightResult2,
					Error:  nil,
				},
			},
		},
		{
			name: "VS - Extra route found in patch object",
			objectsInCluster: []runtime.Object{
				vsSingleRouteWithWeight,
			},
			patchObjects: []*unstructured.Unstructured{vsNoRouteWeight4},
			results: []*AppliedConfigObject{
				{
					Object: vsRouteMixedResult,
					Error:  nil,
				},
			},
		},
		{
			name: "VS - Error when extracting fields",
			objectsInCluster: []runtime.Object{
				vsBrokenRoute,
			},
			patchObjects: []*unstructured.Unstructured{vsNoRouteWeight5},
			results: []*AppliedConfigObject{
				{
					Object: vsNoRouteWeightResult1,
					Error:  fmt.Errorf(".route accessor error: route is of the type string, expected []interface{}"),
				},
			},
		},
		{
			name:                 "VS for OCM managed service",
			enableOcmIntegration: true,
			contextObject:        ocmManagedSvcUnstructured,
			objectsInCluster: []runtime.Object{
				originalArgoVS,
			},
			patchObjects: []*unstructured.Unstructured{vsNoRouteWeight1},
			results: []*AppliedConfigObject{
				{
					Object: vsNoRouteWeight1,
					Error:  nil,
				},
			},
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := kubetest.NewKubeClientBuilder().AddDynamicClientObjects(tc.objectsInCluster...).Build()

			k8sApplicator := NewK8sApplicator(logger, client)

			features.RetainArgoManagedFields = true
			features.EnableOcmIntegration = tc.enableOcmIntegration
			defer func() {
				features.RetainArgoManagedFields = false
				features.EnableOcmIntegration = false
			}()

			err := client.RunAndWait(stopCh, true, nil)
			if err != nil {
				t.Fatalf("unexpected error while running client: %s", err.Error())
			}

			renderCtx := RenderRequestContext{
				Object: tc.contextObject,
			}

			// fake a generated config
			generatedConfig := GeneratedConfig{
				Config: map[string][]*unstructured.Unstructured{"test-config": tc.patchObjects},
			}

			results, _ := k8sApplicator.ApplyConfig(&renderCtx, &generatedConfig, logger)

			assert.ElementsMatch(t, results, tc.results)
		})
	}
}

// TestPatchDRWithArgoFields tests DestinationRule patching with field management
func TestPatchDRWithArgoFields(t *testing.T) {
	testNS := "test-ns"
	testDR := "test-dr"

	ocmManagedSvc := kubetest.CreateService("test-ns", "ocm-managed-svc")
	ocmManagedSvc.SetLabels(map[string]string{
		ocm.ManagedByLabel: ocm.ManagedByValue,
	})
	ocmManagedSvcUnstructured, _ := kube.ObjectToUnstructured(ocmManagedSvc)

	drSubsetsWithHash := []interface{}{
		map[string]interface{}{
			"name": "v1",
			"labels": map[string]interface{}{
				"app":                        "reviews",
				"rollouts-pod-template-hash": "67b988fbf6",
			},
		},
		map[string]interface{}{
			"name": "v2",
			"labels": map[string]interface{}{
				"app":                        "reviews",
				"rollouts-pod-template-hash": "89ab1234cd",
			},
		},
	}

	drSingleSubsetWithHash := []interface{}{
		map[string]interface{}{
			"name": "v1",
			"labels": map[string]interface{}{
				"app":                        "reviews",
				"rollouts-pod-template-hash": "67b988fbf6",
			},
		},
	}

	drSubsetsMixedResult := []interface{}{
		map[string]interface{}{
			"name": "v1",
			"labels": map[string]interface{}{
				"app":                        "reviews",
				"rollouts-pod-template-hash": "67b988fbf6",
			},
		},
		map[string]interface{}{
			"name": "v2",
			"labels": map[string]interface{}{
				"app": "reviews",
			},
		},
	}

	drSubsetsNoHash := []interface{}{
		map[string]interface{}{
			"name": "v1",
			"labels": map[string]interface{}{
				"app": "reviews",
			},
		},
		map[string]interface{}{
			"name": "v2",
			"labels": map[string]interface{}{
				"app": "reviews",
			},
		},
	}

	brokenSubset := []interface{}{
		map[string]interface{}{
			"name":   "v1",
			"labels": "string",
		},
	}

	drSubsetsHashNoName := []interface{}{
		map[string]interface{}{
			"name": "v1",
			"labels": map[string]interface{}{
				"app":                        "reviews",
				"rollouts-pod-template-hash": "67b988fbf6",
			},
		},
		map[string]interface{}{
			// name missing in subset
			"labels": map[string]interface{}{
				"app":                        "reviews",
				"rollouts-pod-template-hash": "89ab1234cd",
			},
		},
	}

	originalArgoDR := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "DestinationRule",
			"metadata": map[string]interface{}{
				"name":      testDR,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "argo-controller",
				},
			},
			"spec": map[string]interface{}{
				"host":    "reviews.prod.svc.cluster.local",
				"subsets": drSubsetsWithHash,
			},
		},
	}

	originalArgoDRSingleSubset := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "DestinationRule",
			"metadata": map[string]interface{}{
				"name":      testDR,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "argo-controller",
				},
			},
			"spec": map[string]interface{}{
				"host":    "reviews.prod.svc.cluster.local",
				"subsets": drSingleSubsetWithHash,
			},
		},
	}

	drNoSubsetHash1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "DestinationRule",
			"metadata": map[string]interface{}{
				"name":      testDR,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"host":    "reviews.prod.svc.cluster.local",
				"subsets": drSubsetsNoHash,
			},
		},
	}

	drNoSubsetHash2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "DestinationRule",
			"metadata": map[string]interface{}{
				"name":      testDR,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"host":    "something-else.prod.svc.cluster.local",
				"subsets": drSubsetsNoHash,
			},
		},
	}

	drSubsetMixedResult := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "DestinationRule",
			"metadata": map[string]interface{}{
				"name":      testDR,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"host":    "reviews.prod.svc.cluster.local",
				"subsets": drSubsetsMixedResult,
			},
		},
	}

	drSubsetHashNoName := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "DestinationRule",
			"metadata": map[string]interface{}{
				"name":      testDR,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"host":    "reviews.prod.svc.cluster.local",
				"subsets": drSubsetsHashNoName,
			},
		},
	}

	drSubsetBroken := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "DestinationRule",
			"metadata": map[string]interface{}{
				"name":      testDR,
				"namespace": testNS,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": "mesh-operator",
				},
			},
			"spec": map[string]interface{}{
				"host":    "reviews.prod.svc.cluster.local",
				"subsets": brokenSubset,
			},
		},
	}

	resultDR := originalArgoDR.DeepCopy()
	resultDR.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{
		"mesh.io/managed-by": "mesh-operator",
	}

	drNoSubsetHashResult1 := drNoSubsetHash1.DeepCopy()
	drNoSubsetHashResult2 := drNoSubsetHash2.DeepCopy()

	drNoSubsetHash3 := drNoSubsetHash1.DeepCopy()
	drNoSubsetHash := drNoSubsetHash1.DeepCopy()
	drNoSubsetHash4 := drNoSubsetHash2.DeepCopy()
	drNoSubsetHash5 := drNoSubsetHash1.DeepCopy()
	drNoSubsetHash6 := drNoSubsetHash1.DeepCopy()

	envoyFilterOriginal := kubetest.CreateEnvoyFilter("test-envoyfilter", testNS, "")

	envoyFilterOriginal.SetKind(constants.EnvoyFilterKind.Kind)

	envoyFilterKind := envoyFilterOriginal.DeepCopy()
	envoyFilterKind.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{
		"label": "original-test-envoyfilter",
	}

	testCases := []struct {
		name                 string
		enableOcmIntegration bool
		contextObject        *unstructured.Unstructured
		objectsInCluster     []runtime.Object
		patchObjects         []*unstructured.Unstructured
		expectedResults      []*AppliedConfigObject
	}{
		{
			name:             "Update Envoyfilter - no existing object of VS/DR found",
			objectsInCluster: []runtime.Object{envoyFilterOriginal},
			patchObjects:     []*unstructured.Unstructured{envoyFilterKind},
			expectedResults: []*AppliedConfigObject{
				{
					Object: envoyFilterKind,
					Error:  nil,
				},
			},
		},
		{
			name: "Update DR - extra subset found in patch object",
			objectsInCluster: []runtime.Object{
				originalArgoDRSingleSubset,
			},
			patchObjects: []*unstructured.Unstructured{drNoSubsetHash1},
			expectedResults: []*AppliedConfigObject{
				{
					Object: drSubsetMixedResult,
					Error:  nil,
				},
			},
		},
		{
			name: "Update DR - With Subset Hash",
			objectsInCluster: []runtime.Object{
				originalArgoDR,
			},
			patchObjects: []*unstructured.Unstructured{drNoSubsetHash3},
			expectedResults: []*AppliedConfigObject{
				{
					Object: resultDR,
					Error:  nil,
				},
			},
		},
		{
			name: "Update DR - No Subset Hash",
			objectsInCluster: []runtime.Object{
				drNoSubsetHash,
			},
			patchObjects: []*unstructured.Unstructured{drNoSubsetHash4},
			expectedResults: []*AppliedConfigObject{
				{
					Object: drNoSubsetHashResult2,
					Error:  nil,
				},
			},
		},
		{
			name: "Update DR - Subset has hash, yet no name found in subset",
			objectsInCluster: []runtime.Object{
				drSubsetHashNoName,
			},
			patchObjects: []*unstructured.Unstructured{drNoSubsetHash5},
			expectedResults: []*AppliedConfigObject{
				{
					Object: drSubsetMixedResult,
					Error:  nil,
				},
			},
		},
		{
			name: "Update DR - Error when subtracting fields in broken Subset",
			objectsInCluster: []runtime.Object{
				drSubsetBroken,
			},
			patchObjects: []*unstructured.Unstructured{drNoSubsetHash6},
			expectedResults: []*AppliedConfigObject{
				{
					Object: drNoSubsetHashResult1,
					Error:  fmt.Errorf(".labels accessor error: string is of the type string, expected map[string]interface{}"),
				},
			},
		},
		{
			name:                 "DR - for OCM managed service",
			enableOcmIntegration: true,
			contextObject:        ocmManagedSvcUnstructured,
			objectsInCluster: []runtime.Object{
				originalArgoDR,
			},
			patchObjects: []*unstructured.Unstructured{drNoSubsetHash3},
			expectedResults: []*AppliedConfigObject{
				{
					Object: drNoSubsetHash3,
					Error:  nil,
				},
			},
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := kubetest.NewKubeClientBuilder().AddDynamicClientObjects(tc.objectsInCluster...).Build()
			k8sApplicator := NewK8sApplicator(logger, client)

			features.RetainArgoManagedFields = true
			features.EnableOcmIntegration = tc.enableOcmIntegration
			defer func() {
				features.RetainArgoManagedFields = false
				features.EnableOcmIntegration = false
			}()

			renderCtx := RenderRequestContext{
				Object: tc.contextObject,
			}

			generatedConfig := GeneratedConfig{
				Config: map[string][]*unstructured.Unstructured{"test-config": tc.patchObjects},
			}

			results, _ := k8sApplicator.ApplyConfig(&renderCtx, &generatedConfig, logger)
			assert.ElementsMatch(t, results, tc.expectedResults)
		})
	}
}

func TestFormatPrinterApplicatorWhenInputIsNull(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	frmtPrinterApplicator := NewFormatPrinterApplicator(logger)
	renderCtx := RenderRequestContext{}
	appliedConfigObjects, err := frmtPrinterApplicator.ApplyConfig(&renderCtx, nil, logger)
	assert.Nil(t, appliedConfigObjects)
	assert.Nil(t, err)
}

func TestCompareConfigObjects(t *testing.T) {
	conf := unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "config-1",
				"namespace": "test-namespace",
			},
		},
	}

	otherConfig := unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "zzz-config",
				"namespace": "test-namespace",
			},
		},
	}

	assert.True(t, compareConfigObjects(&conf, &otherConfig))
	assert.False(t, compareConfigObjects(&otherConfig, &conf))
	assert.False(t, compareConfigObjects(&conf, &conf))
}
