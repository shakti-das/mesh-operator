package matching

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	networkingv1alpha3 "istio.io/api/networking/v1alpha3"

	"k8s.io/apimachinery/pkg/labels"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"istio.io/client-go/pkg/apis/networking/v1alpha3"

	mopv1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHasServiceSelector(t *testing.T) {
	mop := kube_test.NewMopBuilder("ns", "name").Build()
	assert.False(t, HasServiceSelector(mop))

	mop = kube_test.NewMopBuilder("ns", "name").Build()
	mop.Spec.ServiceSelector = map[string]string{}
	assert.False(t, HasServiceSelector(mop))

	mop = kube_test.NewMopBuilder("ns", "name").AddSelector("key", "value").Build()
	assert.True(t, HasServiceSelector(mop))
}

func TestMopToServiceSelector(t *testing.T) {
	testCases := []struct {
		name             string
		meshOperator     *mopv1.MeshOperator
		expectedSelector string
	}{
		{
			name: "MOP with selector (single label match)",
			meshOperator: kube_test.NewMopBuilder("ns", "mop1").
				AddSelector("app", "ordering").
				Build(),
			expectedSelector: "app=ordering",
		},
		{
			name: "MOP with selector (multiple label match)",
			meshOperator: kube_test.NewMopBuilder("ns", "mop1").
				AddSelector("app", "ordering").
				AddSelector("app_instance", "ordering-bg").
				AddSelector("other_app", "test-app").
				Build(),
			expectedSelector: "app=ordering,app_instance=ordering-bg,other_app=test-app",
		},
		{
			name:             "MOP no selector",
			meshOperator:     kube_test.NewMopBuilder("ns", "mop1").Build(),
			expectedSelector: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			selector := mopToServiceSelector(tc.meshOperator)

			assert.Equal(t, tc.expectedSelector, selector.String())
		})
	}
}

func TestListServiceEntriesForMop(t *testing.T) {
	orderingServiceEntry := *kube_test.CreateServiceEntryWithLabels("test-namespace", "se1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping-se1"})
	serviceEntryNoGvk := v1alpha3.ServiceEntry{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "se1",
			Namespace: "test-namespace",
			Labels:    map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping-se1"},
		},
		Spec: networkingv1alpha3.ServiceEntry{
			Hosts:    []string{"some-host.com"},
			Location: networkingv1alpha3.ServiceEntry_MESH_INTERNAL,
		},
	}

	serviceEntryWithGvk := serviceEntryNoGvk.DeepCopy()
	serviceEntryWithGvk.SetGroupVersionKind(schema.GroupVersionKind{Group: constants.ServiceEntryKind.Group, Version: constants.ServiceEntryKind.Version, Kind: constants.ServiceEntryKind.Kind})

	testCases := []struct {
		name                 string
		meshOperator         *mopv1.MeshOperator
		selectorExpected     string
		errorOnList          error
		listExpected         bool
		listFunctionOutput   []*v1alpha3.ServiceEntry
		expectedSeList       []*v1alpha3.ServiceEntry
		expectedErrorMessage string
	}{
		{
			name:         "MOP with selector, no matching SE exist(serviceEntry list is nil)",
			meshOperator: kube_test.NewMopBuilder("test-namespace", "mop1").AddSelector("app", "ordering").Build(),
		},
		{
			name:               "MOP with selector, no matching SE exists(serviceEntry list is empty)",
			meshOperator:       kube_test.NewMopBuilder("test-namespace", "mop1").AddSelector("app", "ordering").Build(),
			listFunctionOutput: []*v1alpha3.ServiceEntry{},
			expectedSeList:     []*v1alpha3.ServiceEntry{},
		},
		{
			name:               "MOP with selector, found matching SEs",
			meshOperator:       kube_test.NewMopBuilder("test-namespace", "mop1").AddSelector("app", "ordering").Build(),
			listFunctionOutput: []*v1alpha3.ServiceEntry{&orderingServiceEntry},
			expectedSeList:     []*v1alpha3.ServiceEntry{&orderingServiceEntry},
		},
		// This test makes sure that GVK is populated into SE objects, otherwise, it's going to break unmarshalling of these objects into Unstructured
		{
			name:               "MOP with selector, found matching SEs (no gvk)",
			meshOperator:       kube_test.NewMopBuilder("test-namespace", "mop1").AddSelector("app", "ordering").Build(),
			listFunctionOutput: []*v1alpha3.ServiceEntry{&serviceEntryNoGvk},
			expectedSeList:     []*v1alpha3.ServiceEntry{serviceEntryWithGvk},
		},
		{
			name:         "MOP without serviceSelector",
			meshOperator: kube_test.NewMopBuilder("test-namespace", "mop1").Build(),
		},
		{
			name:                 "Error while listing serviceEntries",
			meshOperator:         kube_test.NewMopBuilder("test-namespace", "mop1").AddSelector("app", "ordering").AddSelector("other_app", "test-app").Build(),
			errorOnList:          fmt.Errorf("test error"),
			expectedErrorMessage: "error listing ServiceEntry records: test error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			seList, err := ListServiceEntriesForMop(tc.meshOperator, func(namespace string, selector labels.Selector) ([]*v1alpha3.ServiceEntry, error) {
				if tc.errorOnList != nil {
					return nil, tc.errorOnList
				}
				return tc.listFunctionOutput, nil
			})

			assert.ElementsMatch(t, tc.expectedSeList, seList)

			if tc.errorOnList != nil {
				assert.NotNil(t, err)
				assert.Error(t, err, tc.expectedErrorMessage)
			}
		})
	}
}

func TestListMopsForService(t *testing.T) {
	testCases := []struct {
		name                     string
		service                  *v1.Service
		meshOperatorsInCluster   []*mopv1.MeshOperator
		expectedMeshOperatorKeys []string
		listError                error
	}{
		{
			name: "Single selector match",
			service: kube_test.CreateServiceWithLabels(
				"test-namespace",
				"svc1",
				map[string]string{
					"app": "ordering"}),
			meshOperatorsInCluster: []*mopv1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop1").
					AddSelector("app", "ordering").
					Build(),
			},
			expectedMeshOperatorKeys: []string{"test-namespace/mop1"},
		},
		{
			name: "Single selector mismatch",
			service: kube_test.CreateServiceWithLabels(
				"test-namespace",
				"svc1",
				map[string]string{
					"app": "ordering"}),
			meshOperatorsInCluster: []*mopv1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop1").
					AddSelector("app", "shipping").
					Build(),
			},
			expectedMeshOperatorKeys: []string{},
		},
		{
			name: "Multi selector match",
			service: kube_test.CreateServiceWithLabels(
				"test-namespace",
				"svc1",
				map[string]string{
					"app":          "ordering",
					"app_instance": "ordering-bg1"}),
			meshOperatorsInCluster: []*mopv1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop1").
					AddSelector("app", "ordering").
					AddSelector("app_instance", "ordering-bg1").
					Build(),
			},
			expectedMeshOperatorKeys: []string{"test-namespace/mop1"},
		},
		{
			name: "Multi selector mismatch",
			service: kube_test.CreateServiceWithLabels(
				"test-namespace",
				"svc1",
				map[string]string{
					"app":          "ordering",
					"app_instance": "ordering-bg1"}),
			meshOperatorsInCluster: []*mopv1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop1").
					AddSelector("app", "shipping").
					AddSelector("app_instance", "shipping-bg1").
					Build(),
			},
			expectedMeshOperatorKeys: []string{},
		},
		{
			name: "Strict multi selector mismatch",
			service: kube_test.CreateServiceWithLabels(
				"test-namespace",
				"svc1",
				map[string]string{
					"app":          "ordering",
					"app_instance": "ordering-bg1"}),
			meshOperatorsInCluster: []*mopv1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop1").
					AddSelector("app", "ordering").
					AddSelector("app_instance", "ordering").
					AddSelector("app", "ordering").
					Build(),
			},
			expectedMeshOperatorKeys: []string{},
		},
		{
			name: "Service has more labels than needed for match",
			service: kube_test.CreateServiceWithLabels(
				"test-namespace",
				"svc1",
				map[string]string{
					"app":          "ordering",
					"app_instance": "ordering-bg1"}),
			meshOperatorsInCluster: []*mopv1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop1").
					AddSelector("app", "ordering").
					AddSelector("app_instance", "ordering-bg1").
					Build(),
			},
			expectedMeshOperatorKeys: []string{"test-namespace/mop1"},
		},
		{
			name: "Service doesn't match MOPs with no selector",
			service: kube_test.CreateServiceWithLabels(
				"test-namespace",
				"svc1",
				map[string]string{
					"app": "ordering"}),
			meshOperatorsInCluster: []*mopv1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop1").
					Build(),
			},
			expectedMeshOperatorKeys: []string{},
		},
		{
			name: "Service matches mixed MOPs",
			service: kube_test.CreateServiceWithLabels(
				"test-namespace",
				"svc1",
				map[string]string{
					"app":          "ordering",
					"app_instance": "ordering-bg1"}),
			meshOperatorsInCluster: []*mopv1.MeshOperator{
				// Matching MOPs
				kube_test.NewMopBuilder("test-namespace", "mop1").
					AddSelector("app", "ordering").
					Build(),
				kube_test.NewMopBuilder("test-namespace", "mop2").
					AddSelector("app", "ordering").
					AddSelector("app_instance", "ordering-bg1").
					Build(),
				// Non matching MOPs
				kube_test.NewMopBuilder("test-namespace", "mop4").
					AddSelector("app", "shipping").
					Build(),
				kube_test.NewMopBuilder("test-namespace", "mop5").
					AddSelector("app", "ordering").
					AddSelector("other_app", "ordering").
					Build(),
				kube_test.NewMopBuilder("test-namespace", "mop6").
					Build(),
			},
			expectedMeshOperatorKeys: []string{"test-namespace/mop1", "test-namespace/mop2"},
		},
		{
			name: "Service with no labels",
			service: kube_test.CreateService(
				"test-namespace",
				"svc1"),
			meshOperatorsInCluster: []*mopv1.MeshOperator{
				kube_test.NewMopBuilder("test-namespace", "mop1").
					AddSelector("app", "svc1").
					Build(),
				kube_test.NewMopBuilder("test-namespace", "mop6").
					Build(),
			},
			expectedMeshOperatorKeys: []string{},
		},
		{
			name: "MOP list error",
			service: kube_test.CreateService(
				"test-namespace",
				"svc1"),
			listError: fmt.Errorf("test error"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualNamespace := ""
			listCalled := false

			actualMops, err := ListMopsForService(tc.service, func(namespace string) ([]*mopv1.MeshOperator, error) {
				listCalled = true
				actualNamespace = namespace

				if tc.listError != nil {
					return nil, tc.listError
				}

				return tc.meshOperatorsInCluster, nil
			})

			assert.True(t, listCalled)
			assert.Equal(t, tc.service.Namespace, actualNamespace)

			if tc.listError != nil {
				assert.NotNil(t, err)
				assert.Nil(t, actualMops)
				return
			}

			var actualMopKeys []string
			for _, mop := range actualMops {
				actualMopKeys = append(actualMopKeys, fmt.Sprintf("%s/%s", mop.Namespace, mop.Name))
			}
			assert.ElementsMatchf(t, tc.expectedMeshOperatorKeys, actualMopKeys, "expected MOP records weren't selected")
		})
	}
}

func mockHasServiceSelector(m *mopv1.MeshOperator) bool {
	return m.Spec.ServiceSelector != nil
}

func mockListFunc(namespace string, _ labels.Selector) ([]*v1.Service, error) {
	services := []*v1.Service{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "service-1",
				Namespace: namespace,
				Annotations: map[string]string{
					string(constants.RoutingConfigEnabledAnnotation): "true",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "service-2",
				Namespace: namespace,
				Annotations: map[string]string{
					string(constants.RoutingConfigEnabledAnnotation): "false",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "service-3",
				Namespace: namespace,
				Annotations: map[string]string{
					"another-annotation": "value",
				},
			},
		},
	}

	return services, nil
}

func TestListServicesForMop(t *testing.T) {
	testCases := []struct {
		name             string
		meshOperator     *mopv1.MeshOperator
		selectorExpected string
		errorOnList      error
		expectedList     []*v1.Service
	}{
		{
			name: "MOP with selector",
			meshOperator: kube_test.NewMopBuilder("test-namespace", "mop1").
				AddSelector("app", "ordering").
				Build(),
			selectorExpected: "app=ordering",
			expectedList: []*v1.Service{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       constants.ServiceKind.Kind,
						APIVersion: constants.ServiceResource.GroupVersion().String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-1",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							string(constants.RoutingConfigEnabledAnnotation): "true",
						},
					},
				},
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       constants.ServiceKind.Kind,
						APIVersion: constants.ServiceResource.GroupVersion().String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-3",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							"another-annotation": "value",
						},
					},
				},
			},
		},
		{
			name:         "MOP no selector",
			meshOperator: kube_test.NewMopBuilder("test-namespace", "mop1").Build(),
			expectedList: []*v1.Service{},
		},
		{
			name: "List error",
			meshOperator: kube_test.NewMopBuilder("test-namespace", "mop1").
				AddSelector("app", "ordering").
				AddSelector("other_app", "test-app").
				Build(),
			selectorExpected: "app=ordering,other_app=test-app",
			errorOnList:      fmt.Errorf("test error"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockListFunc := func(namespace string, selector labels.Selector) ([]*v1.Service, error) {
				if tc.errorOnList != nil {
					return nil, tc.errorOnList
				}
				return mockListFunc(namespace, selector)
			}

			// Call the function being tested
			services, err := ListServicesForMop(tc.meshOperator, mockListFunc, mockHasServiceSelector)

			if tc.errorOnList != nil {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorOnList.Error())
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, tc.expectedList, services)
				assert.Equal(t, len(tc.expectedList), len(services))
			}
		})
	}
}
