package k8s

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/util/retry"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	machinerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestConvertKindToResource(t *testing.T) {
	testCases := []struct {
		name             string
		resources        []*metav1.APIResourceList
		kind             schema.GroupVersionKind
		expectedResource schema.GroupVersionResource
		expectedErr      string
	}{
		{
			name: "FoundResource",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: schema.GroupVersion{Group: "networking.istio.io", Version: "v1alpha3"}.String(),
					APIResources: []metav1.APIResource{
						{Name: "destinationrules", Kind: "DestinationRule"},
					},
				},
			},
			kind:             schema.GroupVersionKind{Group: "networking.istio.io", Version: "v1alpha3", Kind: "DestinationRule"},
			expectedResource: schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1alpha3", Resource: "destinationrules"},
		},
		{
			name:             "MissingResourceGroupVersion",
			resources:        []*metav1.APIResourceList{},
			kind:             schema.GroupVersionKind{Group: "test.networking.istio.io", Version: "v1alpha3", Kind: "DestinationRule"},
			expectedResource: schema.GroupVersionResource{},
			expectedErr:      "failed to get kubernetes resources for GroupVersion = \"test.networking.istio.io/v1alpha3\": the server could not find the requested resource, GroupVersion \"test.networking.istio.io/v1alpha3\" not found",
		},
		{
			name: "MissingResourceKind",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: schema.GroupVersion{Group: "test.networking.istio.io", Version: "v1alpha3"}.String(),
				},
			},
			kind:             schema.GroupVersionKind{Group: "test.networking.istio.io", Version: "v1alpha3", Kind: "DestinationRule"},
			expectedResource: schema.GroupVersionResource{},
			expectedErr:      "failed to get kubernetes resource for GroupVersionKind = \"test.networking.istio.io/v1alpha3, Kind=DestinationRule\": no matching resource found for given kind",
		},
		{
			name: "EnvoyFilterResource",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: schema.GroupVersion{Group: "test.networking.istio.io/v1alpha3", Version: "v1alpha3"}.String(),
					APIResources: []metav1.APIResource{
						{Name: "envoyfilter", Kind: "EnvoyFilter"},
					},
				},
			},
			kind:             schema.GroupVersionKind{Group: "test.networking.istio.io/v1alpha3", Version: "v1alpha3", Kind: "EnvoyFilter"},
			expectedResource: schema.GroupVersionResource{Group: "test.networking.istio.io/v1alpha3", Version: "v1alpha3", Resource: "envoyfilter"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			discovery := &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{}}

			discovery.Resources = tc.resources

			resource, err := ConvertKindToResource(discovery, tc.kind)

			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedErr)
			}

			assert.Equal(t, tc.expectedResource, resource)
		})
	}
}

func TestAreObjsEqual(t *testing.T) {
	testCases := []struct {
		name string

		objExisting *unstructured.Unstructured
		obj         *unstructured.Unstructured

		expectedEqual bool
		expectedErr   string
	}{
		{
			name: "AllExpectedFieldsSetAndEqual",

			objExisting: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),
			obj: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),

			expectedEqual: true,
		},
		{
			name: "SpecMismatch",

			objExisting: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test1"}, "spec")
				}),
			obj: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test2"}, "spec")
				}),

			expectedEqual: false,
		},
		{
			name: "NamespaceMismatch",

			objExisting: createTestObj(
				"test",
				"testnamespace1",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),
			obj: createTestObj(
				"test",
				"testnamespace2",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),

			expectedEqual: false,
		},
		{
			name: "NameMismatch",

			objExisting: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),
			obj: createTestObj(
				"test2",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),

			expectedEqual: false,
		},
		{
			name: "AnnotationMismatch",

			objExisting: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue1"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),
			obj: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue2"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),

			expectedEqual: false,
		},
		{
			name: "LabelMismatch",

			objExisting: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue1"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),
			obj: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue2"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),

			expectedEqual: false,
		},
		{
			name: "OwnerReferenceMismatch",

			objExisting: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test1"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),
			obj: createTestObj(
				"test1",
				"testnamespace",
				func(o *unstructured.Unstructured) {
					o.SetAnnotations(map[string]string{"testannotation": "testannotationvalue"})
					o.SetLabels(map[string]string{"testlabel": "testlabelvalue"})
					o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "test2"}})
					_ = unstructured.SetNestedField(o.Object, map[string]interface{}{"speccontents": "test"}, "spec")
				}),

			expectedEqual: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			equal, err := areObjsEqual(tc.objExisting, tc.obj)

			assert.Equal(t, tc.expectedEqual, equal)

			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedErr)
			}
		})
	}
}

func TestCreateOrUpdateObjectShouldRetryOn429(t *testing.T) {
	// Arrange
	var counter int = 0

	reaction := func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		counter++
		if counter >= 2 {
			// Return success if this is 2nd call
			return true, nil, nil
		} else {
			// Return 429 error if this is the first call
			return true, nil, &machinerr.StatusError{ErrStatus: metav1.Status{Code: http.StatusTooManyRequests}}
		}
	}

	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	client.PrependReactor("create", "*", reaction)
	gvr := schema.GroupVersionResource{Group: "networking.istio.io", Version: "v1alpha3", Resource: "EnvoyFilter"}

	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	logger, _ := config.Build()
	sugar := logger.Sugar()

	obj := createTestObj("test1", "testnamespace", func(o *unstructured.Unstructured) {})

	// Act
	err := CreateOrUpdateObject(client, obj, gvr, sugar)

	// Assert
	require.NoError(t, err)

	performedActions := []string{}
	for _, action := range client.Actions() {
		var actionName string
		var actionObj runtime.Object

		switch t := action.(type) {
		case k8stesting.GetActionImpl:
			// Skip Get Actions
			continue
		case k8stesting.CreateActionImpl:
			actionObj = t.GetObject()
			actionName = t.GetVerb()
		case k8stesting.UpdateActionImpl:
			actionObj = t.GetObject()
			actionName = t.GetVerb()
		case k8stesting.DeleteActionImpl:
			actionObj = nil
			actionName = t.GetVerb()
		}

		a := actionObj.(*unstructured.Unstructured)
		actionName = fmt.Sprintf("%s-%s.%s", actionName, a.GetNamespace(), a.GetName())
		performedActions = append(performedActions, actionName)
	}

	require.Equal(t, []string{"create-testnamespace.test1", "create-testnamespace.test1"}, performedActions, "Expected to see two Create calls due to 429 error")
}

func createTestObj(name string, namespace string, f func(o *unstructured.Unstructured)) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}

	o.SetName(name)
	o.SetNamespace(namespace)

	f(o)

	return o
}

// Mock operation function for testing
type MockOperation struct {
	attempts     int
	failCount    int
	nonRetryable bool
}

func (m *MockOperation) Run() (bool, error) {
	m.attempts++
	if m.attempts <= m.failCount {
		if m.nonRetryable {
			return false, errors.New("non-retryable error")
		}
		// Simulate "Too Many Requests" error
		return false, machinerr.NewTooManyRequests("too many requests", 1)
	}
	// Succeeds after `failCount` attempts
	return true, nil
}

func TestRetryWithBackoffOnTooManyRequests(t *testing.T) {
	testCases := []struct {
		name             string
		failCount        int
		nonRetryable     bool
		expectedError    string
		expectedAttempts int
	}{
		{
			name:             "SuccessOnFirstAttempt",
			failCount:        0,
			nonRetryable:     false,
			expectedError:    "",
			expectedAttempts: 1,
		},
		{
			name:             "RetriesOnTooManyRequestsAndSucceeds",
			failCount:        2,
			nonRetryable:     false,
			expectedError:    "",
			expectedAttempts: 3,
		},
		{
			name:             "FailsOnNonRetryableError",
			failCount:        1,
			nonRetryable:     true,
			expectedError:    "non-retryable error",
			expectedAttempts: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zap.NewExample().Sugar()
			// Initialize the mock operation with test case parameters
			mockOp := &MockOperation{
				failCount:    tc.failCount,
				nonRetryable: tc.nonRetryable,
			}

			// Execute RetryWithBackoffOnTooManyRequests with DefaultBackoff
			err := RetryWithBackoffOnTooManyRequests(retry.DefaultBackoff, mockOp.Run, logger)

			// Check expected error
			if tc.expectedError != "" {
				assert.EqualError(t, err, tc.expectedError)
			} else {
				assert.NoError(t, err)
			}

			// Verify the number of attempts matches the expected count
			assert.Equal(t, tc.expectedAttempts, mockOp.attempts)
		})
	}
}
