package hash

import (
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestComputeObjectHash(t *testing.T) {
	collisionCount := int32(1)
	mopName := "mop"
	svc1Namespace := "test-ns-1"
	svc1 := "svc1"
	svc2Namespace := "test-ns-2"
	svc2 := "svc2"
	filterIndex := "0"

	serviceEntry1 := "svc1"
	serviceEntry1Namespace := "test-ns-1"
	se1FilterHash := ComputeHashForName(nil, "ServiceEntry", serviceEntry1)
	se1FilterName := mopName + "-" + se1FilterHash + "-" + filterIndex
	se1FilterNameCollision := kube_test.CreateEnvoyFilter(se1FilterName, serviceEntry1Namespace, "cluster1/se2")

	svc1Hash := ComputeHashForName(nil, svc1)
	svc1FilterName := mopName + "-" + svc1Hash + "-" + filterIndex

	svc2Hash := ComputeHashForName(nil, svc2)
	svc2FilterName := mopName + "-" + svc2Hash + "-" + filterIndex

	configNsSvc2Hash := ComputeHashForName(nil, svc2Namespace, svc2)
	configNsSvc2FilterName := mopName + "-" + configNsSvc2Hash + "-" + filterIndex

	svc1FilterCluster1 := kube_test.CreateEnvoyFilter(svc1FilterName, svc1Namespace, "cluster1/svc1")
	svc2FilterWithNameCollision := kube_test.CreateEnvoyFilter(svc2FilterName, svc2Namespace, "cluster1/svc1")
	svc1FilterWithNameCollisionMissingAnnotation := kube_test.CreateEnvoyFilter(svc2FilterName, svc2Namespace, "")

	configNsSvc1Filter := kube_test.CreateEnvoyFilter(svc1FilterName, constants.DefaultConfigNamespace, "cluster1/test-ns-1/svc1")
	configNsSvc2FilterWithNameCollision := kube_test.CreateEnvoyFilter(configNsSvc2FilterName, constants.DefaultConfigNamespace, "cluster1/svc1")
	configNssvc1FilterWithNameCollisionMissingAnnotation := kube_test.CreateEnvoyFilter(configNsSvc2FilterName, constants.DefaultConfigNamespace, "")

	testCases := []struct {
		name                     string
		objectsInCluster         []runtime.Object
		configNamespaceExtension bool
		configNamespace          string
		mopName                  string
		objectKind               string
		objectNamespace          string
		objectName               string
		filterIndex              string
		expectedObjectHash       string
		expectedError            string
	}{
		{
			name: "NoCollision",
			objectsInCluster: []runtime.Object{
				svc1FilterCluster1,
			},
			mopName:            mopName,
			objectKind:         "Service",
			objectNamespace:    svc2Namespace,
			objectName:         svc2,
			filterIndex:        filterIndex,
			expectedObjectHash: ComputeHashForName(nil, svc2),
		},
		{
			name: "CollisionExists",
			objectsInCluster: []runtime.Object{
				svc2FilterWithNameCollision,
			},
			mopName:            mopName,
			objectKind:         "Service",
			objectNamespace:    svc2Namespace,
			objectName:         svc2,
			filterIndex:        filterIndex,
			expectedObjectHash: ComputeHashForName(&collisionCount, svc2),
		},
		{
			name: "CollisionError",
			objectsInCluster: []runtime.Object{
				svc1FilterWithNameCollisionMissingAnnotation,
			},
			mopName:            mopName,
			objectKind:         "Service",
			objectNamespace:    svc2Namespace,
			objectName:         svc2,
			filterIndex:        filterIndex,
			expectedObjectHash: "",
			expectedError:      "conflicting filter in cluster test/" + svc1FilterWithNameCollisionMissingAnnotation.GetName(),
		},
		{
			name:                     "ConfigNamespace - NoCollision",
			objectsInCluster:         []runtime.Object{},
			mopName:                  mopName,
			objectKind:               "Service",
			objectNamespace:          svc2Namespace,
			objectName:               svc2,
			filterIndex:              filterIndex,
			configNamespaceExtension: true,
			configNamespace:          constants.DefaultConfigNamespace,
			expectedObjectHash:       ComputeHashForName(nil, svc2Namespace, svc2),
		},
		{
			name: "ConfigNamespace - CollisionExists",
			objectsInCluster: []runtime.Object{
				configNsSvc2FilterWithNameCollision,
			},
			mopName:                  mopName,
			objectKind:               "Service",
			objectNamespace:          svc2Namespace,
			objectName:               svc2,
			filterIndex:              filterIndex,
			configNamespaceExtension: true,
			configNamespace:          constants.DefaultConfigNamespace,
			expectedObjectHash:       ComputeHashForName(&collisionCount, svc2Namespace, svc2),
		},
		{
			name: "ConfigNamespace - CollisionError",
			objectsInCluster: []runtime.Object{
				configNssvc1FilterWithNameCollisionMissingAnnotation,
			},
			mopName:                  mopName,
			objectKind:               "Service",
			objectNamespace:          svc2Namespace,
			objectName:               svc2,
			filterIndex:              filterIndex,
			configNamespaceExtension: true,
			configNamespace:          constants.DefaultConfigNamespace,
			expectedObjectHash:       "",
			expectedError:            "conflicting filter in cluster test/" + configNssvc1FilterWithNameCollisionMissingAnnotation.GetName(),
		},
		{
			name: "NoCollision - MOP selects svc and SE with same name",
			objectsInCluster: []runtime.Object{
				svc1FilterCluster1,
			},
			mopName:            mopName,
			objectKind:         "ServiceEntry",
			objectNamespace:    svc1Namespace,
			objectName:         serviceEntry1,
			filterIndex:        filterIndex,
			expectedObjectHash: ComputeHashForName(nil, "ServiceEntry", serviceEntry1),
		},
		{
			name: "Collision b/w two SE in same namespace",
			objectsInCluster: []runtime.Object{
				se1FilterNameCollision,
			},
			mopName:            mopName,
			objectKind:         "ServiceEntry",
			objectNamespace:    serviceEntry1Namespace,
			objectName:         serviceEntry1,
			filterIndex:        filterIndex,
			expectedObjectHash: ComputeHashForName(&collisionCount, "ServiceEntry", serviceEntry1),
		},
		{
			name: "ConfigNamespace - NoCollision, scenario - MOP picks service and serviceEntry with same name",
			objectsInCluster: []runtime.Object{
				configNsSvc1Filter,
			},
			mopName:                  mopName,
			objectKind:               "ServiceEntry",
			objectNamespace:          serviceEntry1Namespace,
			objectName:               serviceEntry1,
			filterIndex:              filterIndex,
			configNamespaceExtension: true,
			configNamespace:          constants.DefaultConfigNamespace,
			expectedObjectHash:       ComputeHashForName(nil, "ServiceEntry", serviceEntry1Namespace, serviceEntry1),
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			kubeClient := kube_test.NewKubeClientBuilder().AddDynamicClientObjects(tc.objectsInCluster...).Build()
			primaryKubeClient := (kubeClient).(*kube_test.FakeClient)

			objectHash, err := ComputeObjectHash(logger, tc.configNamespaceExtension, tc.configNamespace, tc.mopName, tc.objectKind, tc.objectNamespace, tc.objectName, primaryKubeClient, svc1FilterCluster1.GroupVersionKind())
			assert.Equal(t, tc.expectedObjectHash, objectHash)
			if tc.expectedError == "" {
				assert.Nil(t, err)
			} else {
				assert.Error(t, err, tc.expectedError)
			}
		})
	}
}
