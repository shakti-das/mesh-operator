package hash

import (
	"errors"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"go.uber.org/zap/zaptest"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func TestCollisionExists(t *testing.T) {
	mopName := "mop"
	svcNamespace := "test"
	svc1 := "svc1"
	svc2 := "svc2"
	svc1Hash := ComputeHashForName(nil, svc1)
	svc1FilterName := mopName + "-" + svc1Hash + "-" + "1"
	svc2FilterName := mopName + "-" + svc1Hash + "-" + "1" // svc1 Hash is used to test collision use case
	svc1Filter := kube_test.CreateEnvoyFilter(svc1FilterName, svcNamespace, "cluster1"+"/"+svc1)
	svc1FilterWithMissingAnnotation := kube_test.CreateEnvoyFilter(svc1FilterName, svcNamespace, "")
	svc1FilterWithEmptyAnnotation := kube_test.NewUnstructuredBuilder(constants.EnvoyFilterResource, constants.EnvoyFilterKind, svc1FilterName, "test").
		SetAnnotations(map[string]string{constants.ExtensionSourceAnnotation: ""}).Build()
	gvr := svc1Filter.GroupVersionKind()

	testCases := []struct {
		name                     string
		objectsInCluster         []runtime.Object
		objectKind               string
		objectNamespace          string
		objectName               string
		objectLookupNamespace    string
		objectLookUpName         string
		collisionExpected        bool
		conflictingErrorExpected bool
		expectedErrorMessage     string
	}{
		{
			name: "CollisionExists_WhenSvc2FilterCollidesWithSvc1Filter",
			objectsInCluster: []runtime.Object{
				svc1Filter,
			},
			objectKind:            "Service",
			objectNamespace:       svcNamespace,
			objectName:            svc2,
			objectLookupNamespace: "test",
			objectLookUpName:      svc2FilterName,
			collisionExpected:     true,
		},
		{
			name:                  "NoCollision_FilterDoesNotExistInCluster",
			objectKind:            "Service",
			objectNamespace:       svcNamespace,
			objectName:            svc1,
			objectLookupNamespace: "test",
			objectLookUpName:      svc1FilterName,
			collisionExpected:     false,
		},
		{
			name: "NoCollision_SameSvcFilterExistInCluster",
			objectsInCluster: []runtime.Object{
				svc1Filter,
			},
			objectKind:            "Service",
			objectNamespace:       svcNamespace,
			objectName:            svc1,
			objectLookupNamespace: "test",
			objectLookUpName:      svc1FilterName,
			collisionExpected:     false,
		},
		{
			name: "NoCollision_NotFoundError",
			objectsInCluster: []runtime.Object{
				svc1Filter,
			},
			objectKind:            "Service",
			objectLookupNamespace: "test",
			objectLookUpName:      "random",
			collisionExpected:     false,
		},
		{
			name: "ConflictingError_MissingAnnotationInCluster",
			objectsInCluster: []runtime.Object{
				svc1FilterWithMissingAnnotation,
			},
			objectKind:               "Service",
			objectNamespace:          svcNamespace,
			objectName:               svc1,
			objectLookupNamespace:    "test",
			objectLookUpName:         svc1FilterName,
			collisionExpected:        false,
			conflictingErrorExpected: true,
			expectedErrorMessage:     "conflicting filter in cluster test/" + svc1FilterName,
		},
		{
			name: "ConflictingError_EmptyAnnotationInCluster",
			objectsInCluster: []runtime.Object{
				svc1FilterWithEmptyAnnotation,
			},
			objectKind:               "Service",
			objectNamespace:          svcNamespace,
			objectName:               svc1,
			objectLookupNamespace:    "test",
			objectLookUpName:         svc1FilterName,
			collisionExpected:        false,
			conflictingErrorExpected: true,
			expectedErrorMessage:     "conflicting filter in cluster test/" + svc1FilterName,
		},
		{
			name: "Error_UnableToAccessFilter",
			objectsInCluster: []runtime.Object{
				svc1Filter,
			},
			objectKind:            "Service",
			objectNamespace:       svcNamespace,
			objectName:            "randomsvc",
			objectLookupNamespace: "test",
			objectLookUpName:      "random",
			collisionExpected:     false,
			expectedErrorMessage:  "unable to access existing filter: test/random, error: get operation failed",
		},
		{
			name: "CollisionDetected_WhenSEFilterCollidesWithServiceFilter",
			objectsInCluster: []runtime.Object{
				svc1Filter,
			},
			objectKind:            "ServiceEntry",
			objectNamespace:       svcNamespace,
			objectName:            svc1,
			objectLookupNamespace: "test",
			objectLookUpName:      svc1FilterName,
			collisionExpected:     true,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var kubeClient kube.Client
			if tc.objectsInCluster != nil {
				kubeClient = kube_test.NewKubeClientBuilder().AddDynamicClientObjects(tc.objectsInCluster...).Build()
			} else {
				kubeClient = kube_test.NewKubeClientBuilder().AddDynamicClientObjects().Build()
			}

			fakeClient := (kubeClient).(*kube_test.FakeClient)

			if tc.expectedErrorMessage != "" && !tc.conflictingErrorExpected {
				fakeClient.DynamicClient.PrependReactor("get", constants.EnvoyFilterResource.Resource, func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.New("get operation failed")
				})
			}

			actualCollision, err := collisionExists(logger, false, tc.objectKind, tc.objectNamespace, tc.objectName, tc.objectLookupNamespace, tc.objectLookUpName, kubeClient, gvr)

			assert.Equal(t, tc.collisionExpected, actualCollision)
			if tc.expectedErrorMessage == "" {
				assert.Nil(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedErrorMessage)
			}
		})
	}
}

func TestKindToResourceConversionError(t *testing.T) {
	kube.ClearGvkToGvrCache()
	expectedErrorMessage := "unable to access existing filter: test/random, error: failed to get kubernetes resources for GroupVersion = \"networking.istio.io/v1alpha3\": the server could not find the requested resource, GroupVersion \"networking.istio.io/v1alpha3\" not found"

	renderedFilterObject := kube_test.CreateEnvoyFilter("random", "test", "cluster1/randomsvc")
	gvr := renderedFilterObject.GroupVersionKind()

	objectLookUpName := "random"
	objectLookUpNamespace := "test"

	svcName := "service"

	discoveryClient := &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{}}
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	logger := zaptest.NewLogger(t).Sugar()

	kubeClient := kube_test.FakeClient{DynamicClient: dynamicClient, DiscoveryClient: discoveryClient} // can't use kube builder for this test cases as envoyfilter gets added by default in discovery client

	actualCollision, err := collisionExists(logger, false, "Service", "svc-namespace", svcName, objectLookUpNamespace, objectLookUpName, &kubeClient, gvr)
	assert.Equal(t, false, actualCollision)
	assert.EqualError(t, err, expectedErrorMessage)
}
