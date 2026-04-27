package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/cluster"

	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/resources_test"
	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"go.uber.org/zap/zaptest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	msmObject = &v1alpha1.MeshServiceMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "namespace",
			Name:      "test-msm",
			UID:       "some-uid",
		},
	}

	testOwner = &v1alpha1.Owner{
		Cluster: "test-cluster",
		Kind:    "Service",
		Name:    "object-name",
	}
)

func TestMsmEventHandler_OnAdd(t *testing.T) {
	queue := &TestableQueue{}
	handler := &msmEventHandler{queue: queue}

	handler.OnAdd(msmObject, false)

	assert.Equal(t, 1, len(queue.recordedItems))

	item := queue.recordedItems[0].(QueueItem)
	assert.Equal(t, "namespace/test-msm", item.key)
}

func TestMsmEventHandler_OnUpdate(t *testing.T) {
	queue := &TestableQueue{}
	handler := &msmEventHandler{queue: queue}

	handler.OnUpdate(msmObject, msmObject)

	assert.Equal(t, 0, len(queue.recordedItems))
}

func TestMsmEventHandler_OnDelete(t *testing.T) {
	queue := &TestableQueue{}
	handler := &msmEventHandler{queue: queue}

	handler.OnDelete(msmObject)

	assert.Equal(t, 0, len(queue.recordedItems))
}

func TestCheckOwnerMightExist(t *testing.T) {
	kubeClient := kube_test.NewKubeClientBuilder().
		AddDynamicClientObjects(
			&unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Rollout",
					"metadata": map[string]interface{}{
						"namespace": "test-namespace",
						"name":      "test-object",
					},
				},
			},
		).
		Build()

	fakeDynamicClient := kubeClient.Dynamic().(*dynamicfake.FakeDynamicClient)
	fakeDynamicClient.PrependReactor("get", "*", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		getAction := action.(k8stesting.GetAction)
		if getAction.GetName() == "erroneous-object" {
			return true, nil, fmt.Errorf("test-error")
		}
		return false, nil, nil
	})

	fakeCluster := &Cluster{
		ID:     "fake-cluster",
		Client: kubeClient,
	}

	testCases := []struct {
		name                  string
		cluster               *Cluster
		msmNamespace          string
		owner                 v1alpha1.Owner
		enableCacheOwnerCheck bool
		cacheCheckError       error
		cacheCheckRecord      interface{}

		expectedError  string
		expectedResult bool
	}{
		{
			name:         "Unknown cluster - consider owner exists",
			msmNamespace: "test-namespace",
			owner: v1alpha1.Owner{
				Cluster: "unknown-cluster",
			},
			cluster:        nil,
			expectedResult: true,
		},
		{
			name:         "Invalid api version",
			msmNamespace: "test-namespace",
			cluster:      fakeCluster,
			owner: v1alpha1.Owner{
				Cluster:    "test-cluster",
				ApiVersion: "bad/api/version",
			},
			expectedResult: true,
		},
		{
			name:         "Unknown kind",
			msmNamespace: "test-namespace",
			cluster:      fakeCluster,
			owner: v1alpha1.Owner{
				Cluster:    "test-cluster",
				ApiVersion: "some-api/v1",
				Kind:       "UnknownKind",
			},
			expectedResult: true,
		},
		{
			name:         "Error on retrieve",
			msmNamespace: "test-namespace",
			cluster:      fakeCluster,
			owner: v1alpha1.Owner{
				Cluster:    "test-cluster",
				ApiVersion: "argoproj.io/v1alpha1",
				Kind:       "Rollout",
				Name:       "erroneous-object",
			},
			expectedResult: true,
			expectedError:  "test-error",
		},
		{
			name:         "Owner not found",
			msmNamespace: "test-namespace",
			cluster:      fakeCluster,
			owner: v1alpha1.Owner{
				Cluster:    "test-cluster",
				ApiVersion: "argoproj.io/v1alpha1",
				Kind:       "Rollout",
				Name:       "not-found-object",
			},
			expectedResult: false,
		},
		{
			name:         "Owner found",
			msmNamespace: "test-namespace",
			cluster:      fakeCluster,
			owner: v1alpha1.Owner{
				Cluster:    "test-cluster",
				ApiVersion: "argoproj.io/v1alpha1",
				Kind:       "Rollout",
				Name:       "test-object",
			},
			expectedResult: true,
		},
		{
			name:                  "Cache check unknown, owner found",
			msmNamespace:          "test-namespace",
			cluster:               fakeCluster,
			enableCacheOwnerCheck: true,
			cacheCheckError:       fmt.Errorf("cache check error"),
			owner: v1alpha1.Owner{
				Cluster:    "test-cluster",
				ApiVersion: "argoproj.io/v1alpha1",
				Kind:       "Rollout",
				Name:       "test-object",
			},
			expectedResult: true,
		},
		{
			name:                  "Cache check unknown, Owner not found",
			msmNamespace:          "test-namespace",
			cluster:               fakeCluster,
			enableCacheOwnerCheck: true,
			cacheCheckError:       fmt.Errorf("cache check error"),
			owner: v1alpha1.Owner{
				Cluster:    "test-cluster",
				ApiVersion: "argoproj.io/v1alpha1",
				Kind:       "Rollout",
				Name:       "not-found-object",
			},
			expectedResult: false,
		},
		{
			name:                  "Cache check owner found",
			msmNamespace:          "test-namespace",
			cluster:               fakeCluster,
			enableCacheOwnerCheck: true,
			cacheCheckRecord:      kube_test.NewServiceBuilder("some-service", "some-ns"),
			owner: v1alpha1.Owner{
				Cluster:    "test-cluster",
				ApiVersion: "",
				Kind:       "Service",
				Name:       "some-service",
			},
			expectedResult: true,
		},
		{
			name:                  "Cache check owner not found",
			msmNamespace:          "test-namespace",
			cluster:               fakeCluster,
			enableCacheOwnerCheck: true,
			cacheCheckError:       &k8serrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound, Code: http.StatusNotFound}},
			owner: v1alpha1.Owner{
				Cluster:    "test-cluster",
				ApiVersion: "",
				Kind:       "Service",
				Name:       "some-service",
			},
			expectedResult: false,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			informerOwnerChecks := map[string]informerOwnerCheck{}

			if tc.enableCacheOwnerCheck {
				features.UseInformerInMsmOwnerCheck = true
				defer func() {
					features.UseInformerInMsmOwnerCheck = false
				}()

				informerOwnerChecks["Service"] = &testableInformerOwnerCheck{
					hasSynced:        true,
					readObjectResult: tc.cacheCheckRecord,
					readObjectError:  tc.cacheCheckError,
				}
			}
			clusterManager := &testableClusterManager{cluster: tc.cluster}
			msmController := msmController{
				logger:              logger,
				clusterManager:      clusterManager,
				informerOwnerChecks: informerOwnerChecks,
			}

			result, err := msmController.checkOwnerMightExist(tc.msmNamespace, tc.owner)

			assert.Equal(t, tc.expectedResult, result)
			if len(tc.expectedError) > 0 {
				assert.Error(t, err, tc.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMsmReconcile(t *testing.T) {

	kubeClient := kube_test.NewKubeClientBuilder().
		AddDynamicClientObjects(
			&unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Rollout",
					"metadata": map[string]interface{}{
						"namespace": "test-ns",
						"name":      "test-object",
					},
				},
			},
		).
		Build()

	fakeDynamicClient := kubeClient.Dynamic().(*dynamicfake.FakeDynamicClient)
	fakeDynamicClient.PrependReactor("get", "*", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		getAction := action.(k8stesting.GetAction)
		if getAction.GetName() == "erroneous-object" {
			return true, nil, fmt.Errorf("test-error")
		}
		return false, nil, nil
	})

	clusterName := "fake-cluster"
	fakeCluster := &Cluster{
		ID:     cluster.ID(clusterName),
		Client: kubeClient,
	}

	testCases := []struct {
		name              string
		cluster           *Cluster
		msm               interface{}
		raiseUntrackError string

		errorExpected        string
		expectedUntrackOwner bool
	}{
		{
			name:    "Object doesn't exist",
			cluster: fakeCluster,
			msm:     nil,
		},
		{
			name:    "Owner check error",
			cluster: fakeCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-msm",
					Namespace: "test-ns",
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{
						{
							Cluster:    "some-cluster",
							ApiVersion: "argoproj.io/v1alpha1",
							Kind:       "Rollout",
							Name:       "erroneous-object",
						},
					},
				},
			},
			errorExpected: "test-error",
		},
		{
			name:    "Object exists",
			cluster: fakeCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-msm",
					Namespace: "test-ns",
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{
						{
							Cluster:    "some-cluster",
							ApiVersion: "argoproj.io/v1alpha1",
							Kind:       "Rollout",
							Name:       "test-object",
						},
					},
				},
			},
		},
		{
			name:    "Untrack error",
			cluster: fakeCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-msm",
					Namespace: "test-ns",
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{
						{
							Cluster:    "some-cluster",
							ApiVersion: "argoproj.io/v1alpha1",
							Kind:       "Rollout",
							Name:       "non-existent-object",
						},
					},
				},
			},
			raiseUntrackError: "untrack-error",
			errorExpected:     "untrack-error",
		},
		{
			name:    "Untrack success",
			cluster: fakeCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-msm",
					Namespace: "test-ns",
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{
						{
							Cluster:    "some-cluster",
							ApiVersion: "argoproj.io/v1alpha1",
							Kind:       "Rollout",
							Name:       "non-existent-object",
						},
					},
				},
			},
			expectedUntrackOwner: true,
		},
		{
			name:    "StripedService_RemoteClusterDown",
			cluster: fakeCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-msm",
					Namespace: "test-ns",
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{
						{
							Cluster:    "some-cluster",
							ApiVersion: "argoproj.io/v1alpha1",
							Kind:       "Rollout",
							Name:       "test-object",
						},
						{
							Cluster:    "some-cluster",
							ApiVersion: "argoproj.io/v1alpha1",
							Kind:       "Rollout",
							Name:       "erroneous-object",
						},
					},
				},
			},
			errorExpected: "test-error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clusterManager := &testableClusterManager{cluster: tc.cluster}
			resourceManager := &resources_test.FakeResourceManager{}
			if len(tc.raiseUntrackError) > 0 {
				resourceManager = &resources_test.FakeResourceManager{
					ErrorOnUntrack: errors.New(tc.raiseUntrackError),
				}
			}
			registry := prometheus.NewRegistry()

			msmController := msmController{
				clusterName:     "test-cluster",
				logger:          zaptest.NewLogger(t).Sugar(),
				clusterManager:  clusterManager,
				resourceManager: resourceManager,
				metricsRegistry: registry,
			}

			msmController.objectReader = func(namespace string, name string) (interface{}, error) {
				return tc.msm, nil
			}

			queueItem := NewQueueItem(clusterName, "test-ns/test-msm", "", controllers_api.EventAdd)
			err := msmController.reconcile(queueItem)

			metricLabels := map[string]string{
				commonmetrics.ClusterLabel:      "test-cluster",
				commonmetrics.NamespaceLabel:    "test-ns",
				commonmetrics.ResourceNameLabel: commonmetrics.DeprecatedLabel,
				commonmetrics.EventTypeLabel:    "add",
			}
			assertCounterWithLabels(t, registry, commonmetrics.MeshServiceMetadatasReconciledTotal, metricLabels, 1)
			if len(tc.errorExpected) > 0 {
				assertCounterWithLabels(t, registry, commonmetrics.MeshServiceMetadatasReconcileFailed, metricLabels, 1)
				assert.ErrorContains(t, err, tc.errorExpected)
			} else {
				assert.NoError(t, err)
			}

			if tc.expectedUntrackOwner {
				assert.Equal(t, 1, len(resourceManager.RecordedUnTrackRequests))
				assertCounterWithLabels(t, registry, commonmetrics.MeshServiceMetadataOwnersUnTracked, metricLabels, 1)
			} else {
				assert.Equal(t, 0, len(resourceManager.RecordedUnTrackRequests))
			}
		})
	}
}

func TestReadObject(t *testing.T) {
	testCluster := "testCluster"
	msmNamespace := "namespace"
	msmName := "test-msm"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := kube_test.NewKubeClientBuilder().AddMopClientObjects(msmObject).Build()
	fakeClient := (client).(*kube_test.FakeClient)
	fakeCluster := &Cluster{
		ID:     cluster.ID(testCluster),
		Client: fakeClient,
	}
	clusterManager := &testableClusterManager{cluster: fakeCluster}
	resourceManager := &resources_test.FakeResourceManager{}
	registry := prometheus.NewRegistry()

	controllerObj := NewMeshServiceMetadataController(zaptest.NewLogger(t).Sugar(), fakeClient, testCluster, clusterManager, resourceManager, registry)
	client.MopInformerFactory().Start(ctx.Done())
	client.MopInformerFactory().WaitForCacheSync(ctx.Done())
	msmControllerObj := (controllerObj).(*msmController)

	testCases := []struct {
		name              string
		msmNamespace      string
		msmName           string
		expectedMsmObject *v1alpha1.MeshServiceMetadata
		expectedError     error
	}{
		{
			name:              "Object exist",
			msmNamespace:      msmNamespace,
			msmName:           msmName,
			expectedMsmObject: msmObject,
		},
		{
			name:          "Object don't exist in cluster",
			msmNamespace:  "whatever",
			msmName:       "whatever",
			expectedError: errors.New("meshservicemetadata.mesh.io whatever not found"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msmObj, readObjErr := msmControllerObj.readObject(tc.msmNamespace, tc.msmName)
			assert.Equal(t, tc.expectedMsmObject, msmObj)
			if tc.expectedError != nil {
				assert.Error(t, tc.expectedError, readObjErr.Error())
			} else {
				assert.Nil(t, readObjErr)
			}

		})
	}

}

func TestServiceInformerOwnerCheck(t *testing.T) {
	clusterName := "test-cluster"
	namespace := "test-ns"
	objectName := "test-object"
	testSvc := kube_test.NewServiceBuilder(objectName, namespace).Build()

	existingOwner := &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "",
		Kind:       "Service",
		Name:       objectName,
	}

	nonExistingOwner := &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "",
		Kind:       "Service",
		Name:       "non-existing-object",
	}

	client := kube_test.NewKubeClientBuilder().AddK8sObjects(&testSvc).Build()
	fakeClient := (client).(*kube_test.FakeClient)
	fakeCluster := &Cluster{
		ID:     cluster.ID(clusterName),
		Client: fakeClient,
	}

	// Start informer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.KubeInformerFactory().Core().V1().Services().Informer()
	client.KubeInformerFactory().Start(ctx.Done())
	client.KubeInformerFactory().WaitForCacheSync(ctx.Done())

	check := serviceInformerOwnerCheck{}

	assert.True(t, check.isInformerSynced(fakeCluster))

	record, err := check.readRecord(fakeCluster, namespace, existingOwner)
	assert.NoError(t, err)
	assert.NotNil(t, record)

	_, err = check.readRecord(fakeCluster, namespace, nonExistingOwner)
	assert.Error(t, err)
}

func TestServiceEntryInformerOwnerCheck(t *testing.T) {
	clusterName := "test-cluster"
	namespace := "test-ns"
	objectName := "test-object"

	testSe := kube_test.NewServiceEntryBuilder(objectName, namespace).Build()
	existingOwner := &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "",
		Kind:       "ServiceEntry",
		Name:       objectName,
	}

	nonExistingOwner := &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "",
		Kind:       "ServiceEntry",
		Name:       "non-existing-object",
	}

	client := kube_test.NewKubeClientBuilder().AddIstioObjects(&testSe).Build()
	fakeClient := (client).(*kube_test.FakeClient)
	fakeCluster := &Cluster{
		ID:     cluster.ID(clusterName),
		Client: fakeClient,
	}

	// Start informer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.IstioInformerFactory().Networking().V1alpha3().ServiceEntries().Informer()
	client.IstioInformerFactory().Start(ctx.Done())
	client.IstioInformerFactory().WaitForCacheSync(ctx.Done())

	check := serviceEntryInformerOwnerCheck{}

	assert.True(t, check.isInformerSynced(fakeCluster))

	record, err := check.readRecord(fakeCluster, namespace, existingOwner)
	assert.NoError(t, err)
	assert.NotNil(t, record)

	_, err = check.readRecord(fakeCluster, namespace, nonExistingOwner)
	assert.Error(t, err)
}

func TestKnativeInformerOwnerCheck(t *testing.T) {
	features.EnableKnativeControllers = true
	defer func() {
		features.EnableKnativeControllers = false
	}()

	clusterName := "test-cluster"
	namespace := "test-ns"
	objectName := "test-object"

	testIngress := kube_test.NewKnativeIngressBuilder(objectName, namespace, "1").Build()

	existingOwner := &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "",
		Kind:       "Ingress",
		Name:       objectName,
	}

	nonExistingOwner := &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "",
		Kind:       "Ingress",
		Name:       "non-existing-object",
	}

	client := kube_test.NewKubeClientBuilder().AddDynamicClientObjects(testIngress).Build()
	fakeClient := (client).(*kube_test.FakeClient)
	fakeCluster := &Cluster{
		ID:     cluster.ID(clusterName),
		Client: fakeClient,
	}

	// Start informer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.DynamicInformerFactory().ForResource(constants.KnativeIngressResource)
	client.DynamicInformerFactory().Start(ctx.Done())
	client.DynamicInformerFactory().WaitForCacheSync(ctx.Done())

	check := knativeIngressInformerOwnerCheck{}

	assert.True(t, check.isInformerSynced(fakeCluster))

	record, err := check.readRecord(fakeCluster, namespace, existingOwner)
	assert.NoError(t, err)
	assert.NotNil(t, record)

	_, err = check.readRecord(fakeCluster, namespace, nonExistingOwner)
	assert.Error(t, err)
}

func TestKnativeServerlessServiceInformerOwnerCheck(t *testing.T) {
	features.EnableKnativeControllers = true
	defer func() {
		features.EnableKnativeControllers = false
	}()

	clusterName := "test-cluster"
	namespace := "test-ns"
	objectName := "test-object"

	testServerlessService := kube_test.NewKnativeServerlessServiceBuilder(objectName, namespace, "1").Build()

	existingOwner := &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "",
		Kind:       "ServerlessService",
		Name:       objectName,
	}

	nonExistingOwner := &v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "",
		Kind:       "ServerlessService",
		Name:       "non-existing-object",
	}

	client := kube_test.NewKubeClientBuilder().AddDynamicClientObjects(testServerlessService).Build()
	fakeClient := (client).(*kube_test.FakeClient)
	fakeCluster := &Cluster{
		ID:     cluster.ID(clusterName),
		Client: fakeClient,
	}

	// Start informer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.DynamicInformerFactory().ForResource(constants.KnativeServerlessServiceResource)
	client.DynamicInformerFactory().Start(ctx.Done())
	client.DynamicInformerFactory().WaitForCacheSync(ctx.Done())

	check := knativeServerlessServiceInformerOwnerCheck{}

	assert.True(t, check.isInformerSynced(fakeCluster))

	record, err := check.readRecord(fakeCluster, namespace, existingOwner)
	assert.NoError(t, err)
	assert.NotNil(t, record)

	_, err = check.readRecord(fakeCluster, namespace, nonExistingOwner)
	assert.Error(t, err)
}

func TestCheckOwnerExistsInCache_featureNotEnabled(t *testing.T) {
	controller := msmController{
		informerOwnerChecks: map[string]informerOwnerCheck{"Service": &testableInformerOwnerCheck{}},
	}

	result := controller.checkOwnerExistsInCache(nil, "ns", testOwner)

	assert.Equal(t, Unknown, result)
}

func TestCheckOwnerExistsInCache_informerNotSynced(t *testing.T) {
	features.UseInformerInMsmOwnerCheck = true
	defer func() {
		features.UseInformerInMsmOwnerCheck = false
	}()
	controller := msmController{
		informerOwnerChecks: map[string]informerOwnerCheck{"Service": &testableInformerOwnerCheck{hasSynced: false}},
	}

	result := controller.checkOwnerExistsInCache(nil, "ns", testOwner)

	assert.Equal(t, Unknown, result)
}

func TestCheckOwnerExistsInCache_unsupportedObjectType(t *testing.T) {
	features.UseInformerInMsmOwnerCheck = true
	defer func() {
		features.UseInformerInMsmOwnerCheck = false
	}()
	controller := msmController{}

	result := controller.checkOwnerExistsInCache(nil, "ns", testOwner)

	assert.Equal(t, Unknown, result)
}

func TestCheckOwnerExistsInCache_recordNotFound(t *testing.T) {
	features.UseInformerInMsmOwnerCheck = true
	defer func() {
		features.UseInformerInMsmOwnerCheck = false
	}()
	controller := msmController{
		informerOwnerChecks: map[string]informerOwnerCheck{
			"Service": &testableInformerOwnerCheck{
				hasSynced:        true,
				readObjectError:  &k8serrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound, Code: http.StatusNotFound}},
				readObjectResult: nil,
			}},
	}

	result := controller.checkOwnerExistsInCache(nil, "ns", testOwner)

	assert.Equal(t, OwnerDoesntExist, result)
}

func TestCheckOwnerExistsInCache_unknownError(t *testing.T) {
	features.UseInformerInMsmOwnerCheck = true
	defer func() {
		features.UseInformerInMsmOwnerCheck = false
	}()
	controller := msmController{
		informerOwnerChecks: map[string]informerOwnerCheck{
			"Service": &testableInformerOwnerCheck{
				hasSynced:        true,
				readObjectError:  fmt.Errorf("unknown-error"),
				readObjectResult: nil,
			}},
	}

	result := controller.checkOwnerExistsInCache(nil, "ns", testOwner)

	assert.Equal(t, Unknown, result)
}

func TestCheckOwnerExistsInCache_recordFound(t *testing.T) {
	features.UseInformerInMsmOwnerCheck = true
	defer func() {
		features.UseInformerInMsmOwnerCheck = false
	}()
	controller := msmController{
		informerOwnerChecks: map[string]informerOwnerCheck{
			"Service": &testableInformerOwnerCheck{
				hasSynced:        true,
				readObjectError:  nil,
				readObjectResult: kube_test.NewServiceBuilder("test-svc", "test-ns").Build(),
			}},
	}

	result := controller.checkOwnerExistsInCache(nil, "ns", testOwner)

	assert.Equal(t, OwnerExists, result)
}

type testableClusterManager struct {
	cluster          *Cluster
	remoteClusters   map[cluster.ID]*Cluster
	unhealthyCluster cluster.ID
}

func (m *testableClusterManager) GetClusterById(_ string) (bool, controllers_api.Cluster) {
	return m.cluster != nil, m.cluster
}

func (m *testableClusterManager) GetExistingClusters() []controllers_api.Cluster {
	result := []controllers_api.Cluster{m.cluster}
	if m.remoteClusters != nil {
		for _, cls := range m.remoteClusters {
			result = append(result, cls)
		}
	}
	return result
}

func (m *testableClusterManager) GetExistingHealthyClusters() []controllers_api.Cluster {
	result := []controllers_api.Cluster{m.cluster}
	if m.remoteClusters != nil {
		for _, cls := range m.remoteClusters {
			if cls.ID != m.unhealthyCluster {
				result = append(result, cls)
			}
		}
	}
	return result
}

func (m *testableClusterManager) GetExistingClustersForKey(secretKey string) []controllers_api.Cluster {
	panic("implement me")
}

func (m *testableClusterManager) IsClusterExistsForOtherKey(secretKey string, clusterId cluster.ID) (string, bool) {
	panic("implement me")
}

func (m *testableClusterManager) AddClusterForKey(secretKey string, value controllers_api.Cluster) {
	panic("implement me")
}
func (m *testableClusterManager) SetPrimaryCluster(cluster controllers_api.Cluster)          {}
func (m *testableClusterManager) DeleteClustersForKey(secretKey string)                      {}
func (m *testableClusterManager) DeleteClusterForKey(secretKey string, clusterId cluster.ID) {}
func (m *testableClusterManager) StopAllClusters() {
	panic("implement me")
}
func (t *testableClusterManager) TestRemoteClustersOrRetry(<-chan struct{}) {
	panic("implement me")
}

func (t *testableClusterManager) IsClusterHealthy(id cluster.ID) bool {
	return t.unhealthyCluster != id
}

type testableInformerOwnerCheck struct {
	hasSynced        bool
	readObjectResult interface{}
	readObjectError  error
}

func (oc *testableInformerOwnerCheck) isInformerSynced(_ controllers_api.Cluster) bool {
	return oc.hasSynced
}

func (oc *testableInformerOwnerCheck) readRecord(_ controllers_api.Cluster, _ string, _ *v1alpha1.Owner) (interface{}, error) {
	return oc.readObjectResult, oc.readObjectError
}
