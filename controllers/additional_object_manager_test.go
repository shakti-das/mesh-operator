package controllers

import (
	"context"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	constants2 "github.com/istio-ecosystem/mesh-operator/common/pkg/k8s/constants"

	"github.com/istio-ecosystem/mesh-operator/pkg/cluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
	kubeinformers "k8s.io/client-go/informers"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestAdditionalObjectIndexName(t *testing.T) {
	assert.Equal(t, "label1", getAdditionalObjectIndexName([]string{"label1"}))
	assert.Equal(t, "label1/label2", getAdditionalObjectIndexName([]string{"label1", "label2"}))
}

func TestGetServiceObjectIndexString(t *testing.T) {
	assert.Equal(t, "value1", getServiceObjectIndexString([]string{"value1"}))
	assert.Equal(t, "value1/value2", getAdditionalObjectIndexName([]string{"value1", "value2"}))
}

func TestCreateInformersForAddObjs(t *testing.T) {

	renderingConfig := RenderingConfig{
		Extensions: map[string]Extension{
			"extension": {
				AdditionalObjects: []AdditionalObject{
					{
						Group:     "networking.istio.io",
						Version:   "v1alpha3",
						Resource:  "virtualservices",
						Namespace: "additional-obj-ns",
						Lookup:    Lookup{MatchByServiceLabels: []string{"label1", "label2"}},
					},
				},
			},
		},
		ServiceConfig: map[string]AdditionalObject{
			"clusterTrafficPolicy": {
				Group:     "mesh.io",
				Version:   "v1alpha1",
				Resource:  "clustertrafficpolicies",
				Namespace: "example-coreapp",
				Lookup:    Lookup{BySvcNameAndNamespace: true},
			},
			"trafficShardingPolicy": {
				Group:     "mesh.io",
				Version:   "v1alpha1",
				Resource:  "trafficshardingpolicies",
				Namespace: "example-coreapp",
				Lookup:    Lookup{BySvcNameAndNamespaceArray: []string{"spec", "services"}},
			},
		},
	}

	testCTP := &v1alpha1.ClusterTrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ora2-casam-app",
			Namespace: "example-coreapp",
		},
	}
	client := kube_test.NewKubeClientBuilder().
		AddMopClientObjects([]runtime.Object{testCTP}...).Build()

	newCluster := &Cluster{
		ID:     cluster.ID("fake-cluster"),
		Client: client,
	}

	clusterManager := &testableClusterManager{cluster: newCluster}

	ac := &AdditionalObjectManagerImpl{
		renderingConfig: &renderingConfig,
		informers:       map[string]kubeinformers.GenericInformer{}}

	err := ac.CreateInformersForAddObjs(client,
		clusterManager,
		zaptest.NewLogger(t).Sugar(),
	)

	assert.Nil(t, err)

	informerName := schema.GroupVersionResource{
		Group:    "networking.istio.io",
		Version:  "v1alpha3",
		Resource: "virtualservices",
	}.String()

	informer := ac.informers[informerName]
	assert.NotNil(t, informer)

	ctpInformerName := schema.GroupVersionResource{
		Group:    "mesh.io",
		Version:  "v1alpha1",
		Resource: "clustertrafficpolicies",
	}.String()

	ctpInformer := ac.informers[ctpInformerName]
	assert.NotNil(t, ctpInformer)

	tspInformerName := schema.GroupVersionResource{
		Group:    "mesh.io",
		Version:  "v1alpha1",
		Resource: "trafficshardingpolicies",
	}.String()

	tspInformer := ac.informers[tspInformerName]
	assert.NotNil(t, tspInformer)

	// Mock object to add to the indexer
	mockObject := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      "mockObject",
				"namespace": "additional-obj-ns",
				"labels":    map[string]interface{}{"label1": "value1", "label2": "value2"},
			},
		},
	}

	err = informer.Informer().GetIndexer().Add(mockObject)
	assert.Nil(t, err)

	objects, err := informer.Informer().GetIndexer().ByIndex("label1/label2", "additional-obj-ns/value1/value2")
	assert.NotNil(t, objects)
	assert.Equal(t, 1, len(objects))

	// Mock TCP object to add to the indexer
	ctpMockObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mesh.io/v1alpha1",
			"kind":       "ClusterTrafficPolicy",
			"metadata": map[string]interface{}{
				"name":      "test-ctp",
				"namespace": "test-ctp-ns",
			},
		},
	}
	err = informer.Informer().GetIndexer().Add(ctpMockObj)
	assert.Nil(t, err)

	ctpInCluster, err := informer.Lister().ByNamespace("test-ctp-ns").Get("test-ctp")
	assert.NotNil(t, ctpInCluster)

	// Mock TSP object to add to the indexer
	mockTsp := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mesh.io/v1alpha1",
			"kind":       "TrafficShardingPolicy",
			"metadata": map[string]interface{}{
				"name":      "ora2-casam-app",
				"namespace": "example-coreapp",
			},
		},
	}
	err = tspInformer.Informer().GetIndexer().Add(mockTsp)
	assert.NoError(t, err)
	tspInCluster, err := tspInformer.Informer().GetIndexer().ByIndex("spec/services", "example-coreapp/ora2-casam-app")
	assert.NotNil(t, tspInCluster)
}

func TestCreateInformersForAddObjs_noResourceInCluster(t *testing.T) {
	renderingConfig := RenderingConfig{
		Extensions: map[string]Extension{
			"extension": {
				AdditionalObjects: []AdditionalObject{
					{
						Group:     "some-group",
						Version:   "v1alpha3",
						Resource:  "non-existent-resource",
						Namespace: "additional-obj-ns",
						Lookup:    Lookup{MatchByServiceLabels: []string{"label1", "label2"}},
					},
				},
			},
		},
	}

	fakeClient := kube_test.NewKubeClientBuilder().Build()

	fakeCluster := &Cluster{
		ID:     "fake-cluster",
		Client: fakeClient,
	}
	clusterManager := &testableClusterManager{cluster: fakeCluster}

	ac := &AdditionalObjectManagerImpl{
		renderingConfig: &renderingConfig,
		informers:       map[string]kubeinformers.GenericInformer{}}

	err := ac.CreateInformersForAddObjs(fakeClient,
		clusterManager,
		zaptest.NewLogger(t).Sugar(),
	)

	assert.Nil(t, err)

	informerName := schema.GroupVersionResource{
		Group:    "some-group",
		Version:  "v1alpha3",
		Resource: "non-existent-resource",
	}.String()

	informer := ac.informers[informerName]
	assert.Nil(t, informer)
}

func TestEnqueueServiceMopsFromAdditionalObject(t *testing.T) {

	addObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "VirtualService",
			"apiVersion": "networking.istio.io/v1alpha3",
			"metadata": map[string]interface{}{
				"name":      "additional-obj1",
				"namespace": "additional-obj-ns",
				"labels": map[string]interface{}{
					"app": "ordering",
				},
			},
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	matchLabelNames := []string{"app"}

	service := kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "ordering"})
	mopInCluster := []runtime.Object{
		kube_test.NewMopBuilder("test-namespace", "mop1").SetUID("uid-1").AddSelector("app", "ordering").Build(),
		kube_test.NewMopBuilder("test-namespace", "mop2").SetUID("uid-2").Build(),
		kube_test.NewMopBuilder("other-namespace", "mop3").SetUID("uid-3").AddSelector("app", "ordering").Build(),
	}
	expectEnqueueCount := 1

	objects := []runtime.Object{service}

	logger := zaptest.NewLogger(t).Sugar()
	queue := &TestableQueue{}
	fakeClient := kube_test.NewKubeClientBuilder().
		AddMopClientObjects(mopInCluster...).
		AddK8sObjects(objects...).
		Build()
	serviceInformer := fakeClient.KubeInformerFactory().Core().V1().Services()
	_ = fakeClient.MopInformerFactory().Mesh().V1alpha1().MeshOperators().Lister()

	indexName := getAdditionalObjectIndexName(matchLabelNames)

	serviceInformer.Informer().AddIndexers(cache.Indexers{
		indexName: func(obj interface{}) (strings []string, e error) {
			svc := obj.(*corev1.Service)
			if svc == nil {
				return nil, e
			}
			var labelValues []string
			for _, labelKey := range matchLabelNames {
				labelValue := svc.Labels[labelKey]
				labelValues = append(labelValues, labelValue)
			}
			index := getServiceObjectIndexString(labelValues)
			return []string{index}, nil
		},
	})

	fakeCluster := &Cluster{
		ID:          "fake-cluster",
		Client:      fakeClient,
		mopEnqueuer: NewSingleQueueEnqueuer(queue),
	}
	clusterManager := &testableClusterManager{cluster: fakeCluster}

	go serviceInformer.Informer().Run(stopCh)
	cache.WaitForCacheSync(stopCh, serviceInformer.Informer().HasSynced)

	fakeClient.MopInformerFactory().Start(stopCh)
	fakeClient.MopInformerFactory().WaitForCacheSync(stopCh)

	enqueueServiceMopsFromAdditionalObject(addObj, matchLabelNames, clusterManager, logger)

	assert.Equal(t, expectEnqueueCount, len(queue.recordedItems))
}

func TestCreateEventHandlerForNameLookup(t *testing.T) {
	expectedQueueItem := NewQueueItem(
		"fake-cluster",
		"test-namespace/mop1",
		"uid-1",
		controllers_api.EventUpdate)

	testCases := []struct {
		name              string
		oldObjRv          string
		newObjRv          string
		oldObjGeneration  int64
		newObjGeneration  int64
		expectedItemCount int
		expectedQueueItem QueueItem
	}{
		{
			name:              "Add",
			expectedQueueItem: expectedQueueItem,
			expectedItemCount: 1,
		},
		{
			name:              "Update_With_Same_RV",
			expectedItemCount: 0,
			oldObjRv:          "1",
			newObjRv:          "1",
			oldObjGeneration:  int64(1),
			newObjGeneration:  int64(2),
		},
		{
			name:              "Update_With_Same_Generation",
			oldObjRv:          "1",
			newObjRv:          "2",
			oldObjGeneration:  int64(1),
			newObjGeneration:  int64(1),
			expectedItemCount: 0,
		},
		{
			name:              "Update_With_Diff_Generation",
			oldObjRv:          "1",
			newObjRv:          "2",
			oldObjGeneration:  int64(1),
			newObjGeneration:  int64(2),
			expectedQueueItem: expectedQueueItem,
			expectedItemCount: 1,
		},
		{
			name:              "Update_No_Generation",
			oldObjRv:          "1",
			newObjRv:          "2",
			oldObjGeneration:  int64(0),
			newObjGeneration:  int64(0),
			expectedQueueItem: expectedQueueItem,
			expectedItemCount: 1,
		},
		{
			name:              "Delete",
			expectedQueueItem: expectedQueueItem,
			expectedItemCount: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			stopCh := make(chan struct{})
			defer close(stopCh)

			extensionName := "faultInjection"

			mopsInCluster := []runtime.Object{
				kube_test.NewMopBuilder("test-namespace", "mop1").SetUID("uid-1").AddSelector("app", "ordering").AddFilter(testFaultFilter).Build(),
				kube_test.NewMopBuilder("test-namespace", "mop2").SetUID("uid-2").Build(),
				kube_test.NewMopBuilder("other-namespace", "mop3").SetUID("uid-3").AddSelector("app", "ordering").Build(),
			}

			logger := zaptest.NewLogger(t).Sugar()
			queue := &TestableQueue{}
			fakeClient := kube_test.NewKubeClientBuilder().
				AddMopClientObjects(mopsInCluster...).
				Build()

			fakeCluster := &Cluster{
				ID:          "fake-cluster",
				Client:      fakeClient,
				mopEnqueuer: NewSingleQueueEnqueuer(queue),
			}
			clusterManager := &testableClusterManager{cluster: fakeCluster}

			eventHandler := createEventHandlerForNameLookup(extensionName, clusterManager, logger)

			addObj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name":      "mockObject1",
						"namespace": "additional-obj-ns",
						"labels":    map[string]interface{}{"psn": "shipping", "psi": "shipping1"},
					},
				},
			}

			mopInformer := fakeClient.MopInformerFactory().Mesh().V1alpha1().MeshOperators().Informer()

			addMopExtensionIndexer(mopInformer)

			fakeClient.MopInformerFactory().Start(stopCh)
			fakeClient.MopInformerFactory().WaitForCacheSync(stopCh)

			if tc.name == "Add" {
				eventHandler.OnAdd(addObj, false)
			} else if tc.name == "Delete" {
				eventHandler.OnDelete(addObj)
			} else {
				oldObj := addObj.DeepCopy()
				oldObj.SetResourceVersion(tc.oldObjRv)
				if tc.oldObjGeneration > 0 {
					oldObj.SetGeneration(tc.oldObjGeneration)
				}

				newObj := addObj.DeepCopy()
				newObj.SetResourceVersion(tc.newObjRv)

				if tc.newObjGeneration > 0 {
					newObj.SetGeneration(tc.newObjGeneration)
				}

				eventHandler.OnUpdate(oldObj, newObj)
			}

			assert.Equal(t, tc.expectedItemCount, len(queue.recordedItems))
			if tc.expectedItemCount > 0 {
				assert.Equal(t, tc.expectedQueueItem, queue.recordedItems[0])
			}
		})
	}
}

func TestGetAdditionalObjectsForExtension_SVC(t *testing.T) {

	testAuthorityFilter := v1alpha1.ExtensionElement{
		Authority: &v1alpha1.AuthorityFilter{},
	}

	renderingConfig := RenderingConfig{
		Extensions: map[string]Extension{
			"faultInjection": {
				AdditionalObjects: []AdditionalObject{
					{
						Group:      "networking.istio.io",
						Version:    "v1alpha3",
						Resource:   "virtualservices",
						Namespace:  "additional-obj-ns",
						Lookup:     Lookup{MatchByServiceLabels: []string{"psn", "psi"}},
						ContextKey: "deploy",
						Singleton:  true,
					},
				},
			},
			"authority": {
				AdditionalObjects: []AdditionalObject{
					{
						Group:      "networking.istio.io",
						Version:    "v1alpha3",
						Resource:   "virtualservices",
						Namespace:  "additional-obj-ns",
						Lookup:     Lookup{MatchByName: "name-lookup-addObj"},
						ContextKey: "deploy",
						Singleton:  true,
					},
				},
			},
		},
	}

	testMop := kube_test.
		NewMopBuilder(namespace, "mop1").
		AddSelector("psn", "shipping").
		AddFilter(testFaultFilter).
		Build()

	mopForNameLookup := kube_test.
		NewMopBuilder(namespace, "mop1").
		AddSelector("psn", "shipping").
		AddFilter(testAuthorityFilter).
		Build()

	testservice := kube_test.NewServiceBuilder("svc1", namespace).SetLabels(map[string]string{"psn": "shipping", "psi": "shipping1"}).GetServiceAsUnstructuredObject()
	svcPartialMatch := kube_test.NewServiceBuilder("svc1", namespace).SetLabels(map[string]string{"psn": "shipping"}).GetServiceAsUnstructuredObject()
	svcNoMatch := kube_test.NewServiceBuilder("svc1", namespace).GetServiceAsUnstructuredObject()

	addObj1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      "mockObject1",
				"namespace": "additional-obj-ns",
				"labels":    map[string]interface{}{"psn": "shipping", "psi": "shipping1"},
			},
		},
	}

	addObj2 := addObj1.DeepCopy()
	addObj2.SetName("mockObject2")

	addObjDifferentNs := addObj1.DeepCopy()
	addObjDifferentNs.SetNamespace("other-additional-obj-ns")

	addObjPartialMatch := addObj1.DeepCopy()
	addObjPartialMatch.SetLabels(map[string]string{"psn": "shipping"})

	addObjNoMatch := addObj1.DeepCopy()
	addObjNoMatch.SetLabels(map[string]string{})

	addObjForNameLookup := addObj1.DeepCopy()
	addObjForNameLookup.SetName("name-lookup-addObj")

	expectedAdditionalObjects := map[string]*templating.AdditionalObjects{
		"faultInjection": {
			Singletons: map[string]interface{}{
				"deploy": addObj1,
			},
		},
	}

	expectedAdditionalObjectsForNameLookup := map[string]*templating.AdditionalObjects{
		"authority": {
			Singletons: map[string]interface{}{
				"deploy": addObjForNameLookup,
			},
		},
	}

	testCases := []struct {
		name                       string
		object                     *unstructured.Unstructured
		mop                        *v1alpha1.MeshOperator
		additionalObjectsInCluster []interface{}
		expectedAdditionalObjects  map[string]*templating.AdditionalObjects
		expectedError              string
	}{
		{
			name:                      "MOP_WithNoExtensions",
			object:                    testservice,
			mop:                       kube_test.NewMopBuilder(namespace, "testMop").SetPhase(PhaseSucceeded).Build(),
			expectedAdditionalObjects: map[string]*templating.AdditionalObjects{},
		},
		{
			name:   "No_AdditionalObjectConfigured_ForExtension",
			object: testservice,
			mop: kube_test.NewMopBuilder(namespace, "mop1").
				AddSelector("psn", "shipping").
				AddFilter(v1alpha1.ExtensionElement{ActiveHealthCheckFilter: &v1alpha1.ActiveHealthCheck{}}).
				Build(),
			expectedAdditionalObjects: map[string]*templating.AdditionalObjects{},
		},
		{
			name:                      "No_AdditionalObjectsFound_ForExtension",
			object:                    testservice,
			mop:                       testMop,
			expectedAdditionalObjects: map[string]*templating.AdditionalObjects{},
			expectedError:             "additional object: additional-obj-ns/shipping/shipping1 is missing",
		},
		{
			name:                       "More than one additional Object Found",
			object:                     testservice,
			mop:                        testMop,
			additionalObjectsInCluster: []interface{}{addObj1, addObj2},
			expectedError:              "more than one additional object found",
		},
		{
			name:                       "Additional object exists",
			object:                     testservice,
			mop:                        testMop,
			additionalObjectsInCluster: []interface{}{addObj1},
			expectedAdditionalObjects:  expectedAdditionalObjects,
		},
		{
			name:                       "Additional object exists in different namespace",
			object:                     testservice,
			mop:                        testMop,
			additionalObjectsInCluster: []interface{}{addObjDifferentNs},
			expectedAdditionalObjects:  map[string]*templating.AdditionalObjects{},
			expectedError:              "additional object: additional-obj-ns/shipping/shipping1 is missing",
		},
		{
			name:          "Svc has no match labels",
			object:        svcNoMatch,
			mop:           testMop,
			expectedError: "service is missing labels to lookup an additional object: [psn psi]",
		},
		{
			name:                       "Svc partial labels match",
			object:                     svcPartialMatch,
			mop:                        testMop,
			additionalObjectsInCluster: []interface{}{addObjPartialMatch},
			expectedError:              "service is missing labels to lookup an additional object: [psn psi]",
		},
		{
			name:                       "By Name Lookup",
			object:                     testservice,
			mop:                        mopForNameLookup,
			additionalObjectsInCluster: []interface{}{addObjForNameLookup},
			expectedAdditionalObjects:  expectedAdditionalObjectsForNameLookup,
		},
		{
			name:                       "By Name Lookup - additional object missing",
			object:                     testservice,
			mop:                        mopForNameLookup,
			additionalObjectsInCluster: []interface{}{},
			expectedError:              "additional object: additional-obj-ns/name-lookup-addObj is missing",
			expectedAdditionalObjects:  map[string]*templating.AdditionalObjects{},
		},
	}

	resource := schema.GroupVersionResource{
		Group:    "networking.istio.io",
		Version:  "v1alpha3",
		Resource: "virtualservices",
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Configure client and cluster
			fakeClient := kube_test.NewKubeClientBuilder().Build()
			fakeCluster := &Cluster{
				ID:     "fake-cluster",
				Client: fakeClient,
			}
			clusterManager := &testableClusterManager{cluster: fakeCluster}

			// Configure object manager and additional-object informers and indexers
			addObjMgr := &AdditionalObjectManagerImpl{
				renderingConfig: &renderingConfig,
				informers:       map[string]kubeinformers.GenericInformer{}}
			err := addObjMgr.CreateInformersForAddObjs(fakeClient, clusterManager, zaptest.NewLogger(t).Sugar())
			assert.NoError(t, err)

			// Add objects to the informer
			addObjInformer := fakeClient.DynamicInformerFactory().ForResource(resource)
			for _, addObj := range tc.additionalObjectsInCluster {
				err = addObjInformer.Informer().GetIndexer().Add(addObj)

			}

			actualAdditionalObjects, err := addObjMgr.GetAdditionalObjectsForExtension(tc.mop, tc.object)

			if tc.expectedError != "" {
				assert.ErrorContains(t, err, tc.expectedError)
				assert.Nil(t, actualAdditionalObjects)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tc.expectedAdditionalObjects, actualAdditionalObjects)
			}
		})
	}
}

func TestGetAdditionalObjectsForExtension_SE(t *testing.T) {

	testAuthorityFilter := v1alpha1.ExtensionElement{
		Authority: &v1alpha1.AuthorityFilter{},
	}

	renderingConfig := RenderingConfig{
		Extensions: map[string]Extension{
			"faultInjection": {
				AdditionalObjects: []AdditionalObject{
					{
						Group:      "networking.istio.io",
						Version:    "v1alpha3",
						Resource:   "virtualservices",
						Namespace:  "additional-obj-ns",
						Lookup:     Lookup{MatchByServiceLabels: []string{"psn", "psi"}},
						ContextKey: "deploy",
						Singleton:  true,
					},
				},
			},
			"authority": {
				AdditionalObjects: []AdditionalObject{
					{
						Group:      "networking.istio.io",
						Version:    "v1alpha3",
						Resource:   "virtualservices",
						Namespace:  "additional-obj-ns",
						Lookup:     Lookup{MatchByName: "name-lookup-addObj"},
						ContextKey: "deploy",
						Singleton:  true,
					},
				},
			},
		},
	}

	testMop := kube_test.
		NewMopBuilder(namespace, "mop1").
		AddSelector("psn", "shipping").
		AddFilter(testFaultFilter).
		Build()

	mopForNameLookup := kube_test.
		NewMopBuilder(namespace, "mop1").
		AddSelector("psn", "shipping").
		AddFilter(testAuthorityFilter).
		Build()

	testserviceentry := kube_test.NewServiceEntryBuilder("svc1", namespace).SetLabels(map[string]string{"psn": "shipping", "psi": "shipping1"}).GetServiceEntryAsUnstructuredObject()
	svcEntryPartialMatch := kube_test.NewServiceEntryBuilder("svc1", namespace).SetLabels(map[string]string{"psn": "shipping"}).GetServiceEntryAsUnstructuredObject()
	svcEntryNoMatch := kube_test.NewServiceEntryBuilder("svc1", namespace).GetServiceEntryAsUnstructuredObject()

	addObj1 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name":      "mockObject1",
				"namespace": "additional-obj-ns",
				"labels":    map[string]interface{}{"psn": "shipping", "psi": "shipping1"},
			},
		},
	}

	addObj2 := addObj1.DeepCopy()
	addObj2.SetName("mockObject2")

	addObjDifferentNs := addObj1.DeepCopy()
	addObjDifferentNs.SetNamespace("other-additional-obj-ns")

	addObjPartialMatch := addObj1.DeepCopy()
	addObjPartialMatch.SetLabels(map[string]string{"psn": "shipping"})

	addObjNoMatch := addObj1.DeepCopy()
	addObjNoMatch.SetLabels(map[string]string{})

	addObjForNameLookup := addObj1.DeepCopy()
	addObjForNameLookup.SetName("name-lookup-addObj")

	expectedAdditionalObjects := map[string]*templating.AdditionalObjects{
		"faultInjection": {
			Singletons: map[string]interface{}{
				"deploy": addObj1,
			},
		},
	}

	expectedAdditionalObjectsForNameLookup := map[string]*templating.AdditionalObjects{
		"authority": {
			Singletons: map[string]interface{}{
				"deploy": addObjForNameLookup,
			},
		},
	}

	testCases := []struct {
		name                       string
		object                     *unstructured.Unstructured
		mop                        *v1alpha1.MeshOperator
		additionalObjectsInCluster []interface{}
		expectedAdditionalObjects  map[string]*templating.AdditionalObjects
		expectedError              string
	}{
		{
			name:                      "MOP_WithNoExtensions",
			object:                    testserviceentry,
			mop:                       kube_test.NewMopBuilder(namespace, "testMop").SetPhase(PhaseSucceeded).Build(),
			expectedAdditionalObjects: map[string]*templating.AdditionalObjects{},
		},
		{
			name:   "No_AdditionalObjectConfigured_ForExtension",
			object: testserviceentry,
			mop: kube_test.NewMopBuilder(namespace, "mop1").
				AddSelector("psn", "shipping").
				AddFilter(v1alpha1.ExtensionElement{ActiveHealthCheckFilter: &v1alpha1.ActiveHealthCheck{}}).
				Build(),
			expectedAdditionalObjects: map[string]*templating.AdditionalObjects{},
		},
		{
			name:                      "No_AdditionalObjectsFound_ForExtension",
			object:                    testserviceentry,
			mop:                       testMop,
			expectedAdditionalObjects: map[string]*templating.AdditionalObjects{},
			expectedError:             "additional object: additional-obj-ns/shipping/shipping1 is missing",
		},
		{
			name:                       "More than one additional Object Found",
			object:                     testserviceentry,
			mop:                        testMop,
			additionalObjectsInCluster: []interface{}{addObj1, addObj2},
			expectedError:              "more than one additional object found",
		},
		{
			name:                       "Additional object exists",
			object:                     testserviceentry,
			mop:                        testMop,
			additionalObjectsInCluster: []interface{}{addObj1},
			expectedAdditionalObjects:  expectedAdditionalObjects,
		},
		{
			name:                       "Additional object exists in different namespace",
			object:                     testserviceentry,
			mop:                        testMop,
			additionalObjectsInCluster: []interface{}{addObjDifferentNs},
			expectedAdditionalObjects:  map[string]*templating.AdditionalObjects{},
			expectedError:              "additional object: additional-obj-ns/shipping/shipping1 is missing",
		},
		{
			name:          "Svc entry has no match labels",
			object:        svcEntryNoMatch,
			mop:           testMop,
			expectedError: "serviceentry is missing labels to lookup an additional object: [psn psi]",
		},
		{
			name:                       "Svc partial labels match",
			object:                     svcEntryPartialMatch,
			mop:                        testMop,
			additionalObjectsInCluster: []interface{}{addObjPartialMatch},
			expectedError:              "serviceentry is missing labels to lookup an additional object: [psn psi]",
		},
		{
			name:                       "By Name Lookup",
			object:                     testserviceentry,
			mop:                        mopForNameLookup,
			additionalObjectsInCluster: []interface{}{addObjForNameLookup},
			expectedAdditionalObjects:  expectedAdditionalObjectsForNameLookup,
		},
		{
			name:                       "By Name Lookup - additional object missing",
			object:                     testserviceentry,
			mop:                        mopForNameLookup,
			additionalObjectsInCluster: []interface{}{},
			expectedError:              "additional object: additional-obj-ns/name-lookup-addObj is missing",
			expectedAdditionalObjects:  map[string]*templating.AdditionalObjects{},
		},
	}

	resource := schema.GroupVersionResource{
		Group:    "networking.istio.io",
		Version:  "v1alpha3",
		Resource: "virtualservices",
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Configure client and cluster
			fakeClient := kube_test.NewKubeClientBuilder().Build()
			fakeCluster := &Cluster{
				ID:     "fake-cluster",
				Client: fakeClient,
			}
			clusterManager := &testableClusterManager{cluster: fakeCluster}

			// Configure object manager and additional-object informers and indexers
			addObjMgr := &AdditionalObjectManagerImpl{
				renderingConfig: &renderingConfig,
				informers:       map[string]kubeinformers.GenericInformer{}}
			err := addObjMgr.CreateInformersForAddObjs(fakeClient, clusterManager, zaptest.NewLogger(t).Sugar())
			assert.NoError(t, err)

			// Add objects to the informer
			addObjInformer := fakeClient.DynamicInformerFactory().ForResource(resource)
			for _, addObj := range tc.additionalObjectsInCluster {
				err = addObjInformer.Informer().GetIndexer().Add(addObj)

			}

			actualAdditionalObjects, err := addObjMgr.GetAdditionalObjectsForExtension(tc.mop, tc.object)

			if tc.expectedError != "" {
				assert.ErrorContains(t, err, tc.expectedError)
				assert.Nil(t, actualAdditionalObjects)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tc.expectedAdditionalObjects, actualAdditionalObjects)
			}
		})
	}
}

func TestGetCtpObjectsForService(t *testing.T) {
	serviceConfig := map[string]AdditionalObject{
		"test-service-policy": {
			Group:     "mesh.io",
			Version:   "v1alpha1",
			Resource:  "clustertrafficpolicies",
			Namespace: "example-coreapp",
			Lookup:    Lookup{BySvcNameAndNamespace: true},
		},
	}

	matchingCTPObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mesh.io/v1alpha1",
			"kind":       "ClusterTrafficPolicy",
			"metadata": map[string]interface{}{
				"name":      "ora2-casam-app",
				"namespace": "example-coreapp",
			},
		},
	}

	matchingCTPObjs := []*unstructured.Unstructured{
		matchingCTPObj,
	}

	differentNsCTPObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mesh.io/v1alpha1",
			"kind":       "ClusterTrafficPolicy",
			"metadata": map[string]interface{}{
				"name":      "ora2-casam-app",
				"namespace": "other-namespace",
			},
		},
	}

	differentNameCTPObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mesh.io/v1alpha1",
			"kind":       "ClusterTrafficPolicy",
			"metadata": map[string]interface{}{
				"name":      "other-name",
				"namespace": "example-coreapp",
			},
		},
	}
	testSvc := kube_test.CreateServiceWithLabels("example-coreapp", "ora2-casam-app", map[string]string{})

	testCases := []struct {
		name                       string
		svc                        *corev1.Service
		additionalObjectsInCluster []runtime.Object
		expectedAdditionalObject   []*unstructured.Unstructured
		expectedError              string
	}{
		{
			name:                       "Additional object exists",
			svc:                        testSvc,
			additionalObjectsInCluster: []runtime.Object{matchingCTPObj},
			expectedAdditionalObject:   matchingCTPObjs,
		},
		{
			name:                     "No_AdditionalObjectsFound_ForSvc",
			svc:                      testSvc,
			expectedAdditionalObject: nil,
			expectedError:            "additional object: example-coreapp/ora2-casam-app is missing",
		},
		{
			name:                       "Additional object exists in different namespace",
			svc:                        testSvc,
			additionalObjectsInCluster: []runtime.Object{differentNsCTPObj},
			expectedAdditionalObject:   nil,
			expectedError:              "additional object: example-coreapp/ora2-casam-app is missing",
		},
		{
			name:                       "Additional object exists in the namespace, no matching name",
			svc:                        testSvc,
			additionalObjectsInCluster: []runtime.Object{differentNameCTPObj},
			expectedAdditionalObject:   nil,
			expectedError:              "additional object: example-coreapp/ora2-casam-app is missing",
		},
	}

	ctpResource := schema.GroupVersionResource{
		Group:    "mesh.io",
		Version:  "v1alpha1",
		Resource: "clustertrafficpolicies",
	}

	renderingConfig := RenderingConfig{ServiceConfig: serviceConfig}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := kube_test.NewKubeClientBuilder().AddMopClientObjects(tc.additionalObjectsInCluster...).Build()
			fakeCluster := &Cluster{
				ID:     "fake-cluster",
				Client: fakeClient,
			}
			clusterManager := &testableClusterManager{cluster: fakeCluster}

			// Configure object manager and additional-object informers and indexers
			addObjMgr := &AdditionalObjectManagerImpl{
				renderingConfig: &renderingConfig,
				informers:       map[string]kubeinformers.GenericInformer{}}
			err := addObjMgr.CreateInformersForAddObjs(fakeClient, clusterManager, zaptest.NewLogger(t).Sugar())
			assert.NoError(t, err)

			// Add objects to the informer
			ctpInformer := addObjMgr.informers[ctpResource.String()]
			assert.NotNil(t, ctpInformer)

			for _, addObj := range tc.additionalObjectsInCluster {
				err = ctpInformer.Informer().GetIndexer().Add(addObj)
				assert.NoError(t, err)
			}

			actualAdditionalObjects, err := addObjMgr.GetAdditionalObjectsForService("test-service-policy", tc.svc)

			if tc.expectedError != "" {
				assert.ErrorContains(t, err, tc.expectedError)
				assert.Nil(t, actualAdditionalObjects)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tc.expectedAdditionalObject, actualAdditionalObjects)
			}
		})
	}
}

func TestGetTspObjectForService(t *testing.T) {
	serviceConfig := map[string]AdditionalObject{
		"test-service-policy": {
			Group:    "mesh.io",
			Version:  "v1alpha1",
			Resource: "trafficshardingpolicies",
			Lookup:   Lookup{BySvcNameAndNamespaceArray: []string{"spec", "services"}},
		},
	}

	matchingTspObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mesh.io/v1alpha1",
			"kind":       "TrafficShardingPolicy",
			"metadata": map[string]interface{}{
				"name":      "test-tsp",
				"namespace": "example-coreapp",
			},
			"spec": map[string]interface{}{
				"services": []interface{}{
					map[string]interface{}{
						"name":      "ora2-casam-app",
						"namespace": "example-coreapp",
					},
				},
			},
		},
	}

	matchingTspObjs := []*unstructured.Unstructured{
		matchingTspObj,
	}

	differentNsTspObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mesh.io/v1alpha1",
			"kind":       "ClusterTrafficPolicy",
			"metadata": map[string]interface{}{
				"name":      "test-tsp-other-ns",
				"namespace": "other-namespace",
			},
			"spec": map[string]interface{}{
				"services": []interface{}{
					map[string]interface{}{
						"name":      "ora2-casam-app",
						"namespace": "example-coreapp",
					},
				},
			},
		},
	}

	nonMatchTspObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mesh.io/v1alpha1",
			"kind":       "ClusterTrafficPolicy",
			"metadata": map[string]interface{}{
				"name":      "non-matching-tsp",
				"namespace": "example-coreapp",
			},
			"spec": map[string]interface{}{
				"services": []interface{}{
					map[string]interface{}{
						"name":      "ora2-casam-app-nonmatch",
						"namespace": "example-coreapp",
					},
				},
			},
		},
	}

	testSvc := kube_test.CreateServiceWithLabels("example-coreapp", "ora2-casam-app", map[string]string{})

	testCases := []struct {
		name                       string
		svc                        *corev1.Service
		additionalObjectsInCluster []runtime.Object
		expectedAdditionalObject   []*unstructured.Unstructured
		expectedError              string
	}{
		{
			name:                       "Additional object exists",
			svc:                        testSvc,
			additionalObjectsInCluster: []runtime.Object{matchingTspObj},
			expectedAdditionalObject:   matchingTspObjs,
		},
		{
			name:                     "No_AdditionalObjectsFound_ForSvc",
			svc:                      testSvc,
			expectedAdditionalObject: nil,
			expectedError:            "additional object: example-coreapp/ora2-casam-app is missing",
		},
		{
			name:                       "Additional object exists in different namespace",
			svc:                        testSvc,
			additionalObjectsInCluster: []runtime.Object{differentNsTspObj},
			expectedAdditionalObject:   nil,
			expectedError:              "additional object: example-coreapp/ora2-casam-app is missing",
		},
		{
			name:                       "Additional object exists in the namespace, no matching name",
			svc:                        testSvc,
			additionalObjectsInCluster: []runtime.Object{nonMatchTspObj},
			expectedAdditionalObject:   nil,
			expectedError:              "additional object: example-coreapp/ora2-casam-app is missing",
		},
	}

	renderingConfig := RenderingConfig{ServiceConfig: serviceConfig}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := kube_test.NewKubeClientBuilder().AddMopClientObjects(tc.additionalObjectsInCluster...).Build()
			fakeCluster := &Cluster{
				ID:     "fake-cluster",
				Client: fakeClient,
			}
			clusterManager := &testableClusterManager{cluster: fakeCluster}

			// Configure object manager and additional-object informers and indexers
			addObjMgr := &AdditionalObjectManagerImpl{
				renderingConfig: &renderingConfig,
				informers:       map[string]kubeinformers.GenericInformer{}}
			err := addObjMgr.CreateInformersForAddObjs(fakeClient, clusterManager, zaptest.NewLogger(t).Sugar())
			assert.NoError(t, err)

			// Add objects to the informer
			tspInformer := addObjMgr.informers[constants2.TrafficShardingPolicyResource.String()]
			assert.NotNil(t, tspInformer)

			for _, addObj := range tc.additionalObjectsInCluster {
				err = tspInformer.Informer().GetIndexer().Add(addObj)
				assert.NoError(t, err)
			}

			actualAdditionalObjects, err := addObjMgr.GetAdditionalObjectsForService("test-service-policy", tc.svc)

			if tc.expectedError != "" {
				assert.ErrorContains(t, err, tc.expectedError)
				assert.Nil(t, actualAdditionalObjects)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tc.expectedAdditionalObject, actualAdditionalObjects)
			}
		})
	}
}

func TestExtractLabelValuesOrNil(t *testing.T) {

	addObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":   "mockObject",
				"labels": map[string]interface{}{"psn": "shipping", "psi": "shipping1"},
			},
		},
	}

	testCases := []struct {
		name                string
		labels              []string
		expectedLabelValues []string
	}{
		{
			name:                "Obj has all labels",
			labels:              []string{"psn", "psi"},
			expectedLabelValues: []string{"shipping", "shipping1"},
		},
		{
			name:                "Obj missing some labels",
			labels:              []string{"app", "psi"},
			expectedLabelValues: nil,
		},
		{
			name:                "Obj missing all labels",
			labels:              []string{"app", "name"},
			expectedLabelValues: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			actualLabelValues := extractLabelValuesOrNil(tc.labels, addObj.GetLabels())
			assert.Equal(t, tc.expectedLabelValues, actualLabelValues)
		})
	}
}

func TestGetAdditionalObjectIndexString(t *testing.T) {

	testCases := []struct {
		name          string
		namespace     string
		labels        []string
		expectedIndex string
	}{
		{
			name:          "Object has all label values",
			namespace:     "additional-obj-ns",
			labels:        []string{"shipping", "shipping1", "shipping2"},
			expectedIndex: "additional-obj-ns/shipping/shipping1/shipping2",
		},
		{
			name:          "Object missing some labels",
			namespace:     "additional-obj-ns",
			labels:        []string{"shipping", ""},
			expectedIndex: "additional-obj-ns/shipping/",
		},
		{
			name:          "Object missing all labels",
			namespace:     "additional-obj-ns",
			labels:        []string{"", "", ""},
			expectedIndex: "additional-obj-ns///",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			actualIndex := getAdditionalObjectIndexString(tc.namespace, tc.labels)
			assert.Equal(t, tc.expectedIndex, actualIndex)
		})
	}
}

func TestIsResourcePresentInCluster(t *testing.T) {
	client := kube_test.NewKubeClientBuilder().Build()

	existingResource := schema.GroupVersionResource{
		Group:    "networking.istio.io",
		Version:  "v1alpha3",
		Resource: "virtualservices",
	}

	nonExistingResource := schema.GroupVersionResource{
		Group:    "non-existent-group",
		Version:  "v1alpha3",
		Resource: "non-existent-resource",
	}

	resourceExists, err := isResourcePresentInCluster(nonExistingResource, client)
	assert.Nil(t, err)
	assert.False(t, resourceExists)

	resourceExists, err = isResourcePresentInCluster(existingResource, client)
	assert.Nil(t, err)
	assert.True(t, resourceExists)
}

func TestEnqueueMop_OnAdditionalObjectUpdate(t *testing.T) {

	testServiceObject := kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"p_svc": "shipping", "p_si": "shipping1"})
	serviceInCluster := []runtime.Object{testServiceObject}

	testCases := []struct {
		name               string
		oldResourceVerion  string
		newResourceVersion string
		oldGeneration      int64
		newGeneration      int64
		oldLabels          map[string]string
		newLabels          map[string]string
		expectedQueueSize  int
	}{
		{
			name:               "No change in resource Version",
			oldResourceVerion:  "77527",
			newResourceVersion: "77527",
			oldGeneration:      1,
			newGeneration:      1,
			expectedQueueSize:  0,
		},
		{
			name:               "Generation changed",
			oldResourceVerion:  "77527",
			newResourceVersion: "77547",
			oldGeneration:      1,
			newGeneration:      2,
			expectedQueueSize:  2,
		},
		{
			name:               "No change in Generation but label changed",
			oldResourceVerion:  "77527",
			newResourceVersion: "77547",
			oldGeneration:      1,
			newGeneration:      1,
			oldLabels:          map[string]string{"p_svc": "shipping", "p_si": "shipping1", "key": "key1"},
			newLabels:          map[string]string{"p_svc": "shipping", "p_si": "shipping1", "key": "key2"},
			expectedQueueSize:  2,
		},
		{
			name:               "No change in generation + No change in labels",
			oldResourceVerion:  "77527",
			newResourceVersion: "77547",
			oldGeneration:      1,
			newGeneration:      1,
			oldLabels:          map[string]string{"p_svc": "shipping", "p_si": "shipping1", "key": "key1"},
			newLabels:          map[string]string{"p_svc": "shipping", "p_si": "shipping1", "key": "key1"},
			expectedQueueSize:  0,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			stopCh := make(chan struct{})
			defer close(stopCh)

			mopsInCluster := []runtime.Object{
				kube_test.NewMopBuilder(namespace, "mop1").SetUID("uid-1").AddSelector("p_svc", "shipping").Build(),
			}

			logger := zaptest.NewLogger(t).Sugar()
			queue := &TestableQueue{}
			fakeClient := kube_test.NewKubeClientBuilder().AddK8sObjects(serviceInCluster...).AddMopClientObjects(mopsInCluster...).Build()

			fakeCluster := &Cluster{
				ID:          "fake-cluster",
				Client:      fakeClient,
				mopEnqueuer: NewSingleQueueEnqueuer(queue),
			}
			clusterManager := &testableClusterManager{cluster: fakeCluster}

			matchLabelNames := []string{"p_svc", "p_si"}

			serviceInformer := fakeClient.KubeInformerFactory().Core().V1().Services().Informer()
			addServiceIndexer(serviceInformer, matchLabelNames)
			go serviceInformer.Run(ctx.Done())
			cache.WaitForCacheSync(ctx.Done(), serviceInformer.HasSynced)

			mopInformer := fakeClient.MopInformerFactory().Mesh().V1alpha1().MeshOperators().Informer()
			addMopExtensionIndexer(mopInformer)
			fakeClient.MopInformerFactory().Start(stopCh)
			fakeClient.MopInformerFactory().WaitForCacheSync(stopCh)

			eventHandler := createEventHandlerForLabelsLookup(matchLabelNames, clusterManager, logger)

			addObj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "ingress.mesh.io.example.com/v2",
					"kind":       "IngressGatewayConfig",
					"metadata": map[string]interface{}{
						"name":      "whatever",
						"namespace": "additional-obj-ns",
						"labels":    map[string]interface{}{"p_svc": "shipping", "p_si": "shipping1"},
					},
				},
			}

			oldObj := addObj.DeepCopy()
			oldObj.SetResourceVersion(tc.oldResourceVerion)
			oldObj.SetGeneration(tc.oldGeneration)
			if tc.oldLabels != nil {
				oldObj.SetLabels(tc.oldLabels)
			}

			newObj := addObj.DeepCopy()
			newObj.SetResourceVersion(tc.newResourceVersion)
			newObj.SetGeneration(tc.newGeneration)
			if tc.newLabels != nil {
				newObj.SetLabels(tc.newLabels)
			}

			eventHandler.OnUpdate(oldObj, newObj)

			assert.Equal(t, tc.expectedQueueSize, len(queue.recordedItems))
		})
	}
}

func TestEventHandlerByNameFilter(t *testing.T) {

	matchingNamespace := "matching-namespace"
	matchingName := "matching-name"

	testCases := []struct {
		name            string
		objectNamespace string
		objectName      string
		expectedResult  bool
	}{
		{
			name:            "namespace doesn't match, name doesn't match",
			objectNamespace: "non-match",
			objectName:      "non-match",
			expectedResult:  false,
		},
		{
			name:            "namespace doesn't match, name match",
			objectNamespace: "non-match",
			objectName:      matchingName,
			expectedResult:  false,
		},
		{
			name:            "namespace match, name doesn't match",
			objectNamespace: matchingNamespace,
			objectName:      "non-match",
			expectedResult:  false,
		},
		{
			name:            "namespace match, name match",
			objectNamespace: matchingNamespace,
			objectName:      matchingName,
			expectedResult:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Object Kind doesn't matter.
			object := kube_test.NewUnstructuredBuilder(constants.EnvoyFilterResource, constants.EnvoyFilterKind, tc.objectName, tc.objectNamespace).Build()

			actualResult := eventHandlerByNameFilter(matchingNamespace, matchingName)(object)

			assert.Equal(t, tc.expectedResult, actualResult)
		})
	}
}

func addServiceIndexer(informer cache.SharedIndexInformer, labelNames []string) error {
	indexName := getAdditionalObjectIndexName(labelNames)

	return informer.AddIndexers(cache.Indexers{
		indexName: func(obj interface{}) (strings []string, e error) {
			svc := obj.(*corev1.Service)
			if svc == nil {
				return nil, e
			}
			labelValues := extractLabelValuesOrNil(labelNames, svc.GetLabels())
			if labelValues == nil {
				// don't index objects if it is missing match labels
				return []string{}, nil
			}
			svcIndexString := getServiceObjectIndexString(labelValues)
			return []string{svcIndexString}, nil
		},
	})
}

func TestNoOpAdditionalObjectManager(t *testing.T) {
	manager := &NoOpAdditionalObjectManager{}
	logger := zaptest.NewLogger(t).Sugar()
	client := kube_test.NewKubeClientBuilder().Build()
	clusterManager := &testableClusterManager{}

	// Test CreateInformersForAddObjs
	err := manager.CreateInformersForAddObjs(client, clusterManager, logger)
	assert.NoError(t, err)

	// Test GetAdditionalObjectsForExtension
	objects, err := manager.GetAdditionalObjectsForExtension(nil, nil)
	assert.NoError(t, err)
	assert.Empty(t, objects)

	// Test GetAdditionalObjectsForService
	policies, err := manager.GetAdditionalObjectsForService("", nil)
	assert.NoError(t, err)
	assert.Empty(t, policies)
}

func TestInvokeForNamespaceAndNames(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	testCases := []struct {
		name              string
		obj               *unstructured.Unstructured
		lookup            []string
		expectedCallbacks []struct{ namespace, name string }
	}{
		{
			name: "valid object with single service",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-obj",
						"namespace": "test-ns",
					},
					"spec": map[string]interface{}{
						"services": []interface{}{
							map[string]interface{}{
								"name":      "service1",
								"namespace": "test-ns",
							},
						},
					},
				},
			},
			lookup: []string{"spec", "services"},
			expectedCallbacks: []struct{ namespace, name string }{
				{namespace: "test-ns", name: "service1"},
			},
		},
		{
			name: "valid object with multiple services",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-obj",
						"namespace": "test-ns",
					},
					"spec": map[string]interface{}{
						"targets": []interface{}{
							map[string]interface{}{
								"name":      "service1",
								"namespace": "test-ns",
							},
							map[string]interface{}{
								"name":      "service2",
								"namespace": "test-ns",
							},
						},
					},
				},
			},
			lookup: []string{"spec", "targets"},
			expectedCallbacks: []struct{ namespace, name string }{
				{namespace: "test-ns", name: "service1"},
				{namespace: "test-ns", name: "service2"},
			},
		},
		{
			name: "cross-namespace service reference",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-obj",
						"namespace": "test-ns",
					},
					"spec": map[string]interface{}{
						"services": []interface{}{
							map[string]interface{}{
								"name":      "service1",
								"namespace": "other-ns",
							},
						},
					},
				},
			},
			lookup:            []string{"spec", "services"},
			expectedCallbacks: nil,
		},
		{
			name: "missing name field",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-obj",
						"namespace": "test-ns",
					},
					"spec": map[string]interface{}{
						"services": []interface{}{
							map[string]interface{}{
								"namespace": "test-ns",
							},
						},
					},
				},
			},
			lookup:            []string{"spec", "services"},
			expectedCallbacks: nil,
		},
		{
			name: "missing namespace field",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-obj",
						"namespace": "test-ns",
					},
					"spec": map[string]interface{}{
						"services": []interface{}{
							map[string]interface{}{
								"name": "service1",
							},
						},
					},
				},
			},
			lookup:            []string{"spec", "services"},
			expectedCallbacks: nil,
		},
		{
			name: "invalid format - not a map",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-obj",
						"namespace": "test-ns",
					},
					"spec": map[string]interface{}{
						"services": []interface{}{
							"invalid-string-instead-of-map",
						},
					},
				},
			},
			lookup:            []string{"spec", "services"},
			expectedCallbacks: nil,
		},
		{
			name: "lookup path not found",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-obj",
						"namespace": "test-ns",
					},
					"spec": map[string]interface{}{},
				},
			},
			lookup:            []string{"spec", "nonexistent"},
			expectedCallbacks: nil,
		},
		{
			name: "empty services array",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name":      "test-obj",
						"namespace": "test-ns",
					},
					"spec": map[string]interface{}{
						"services": []interface{}{},
					},
				},
			},
			lookup:            []string{"spec", "services"},
			expectedCallbacks: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actualCallbacks []struct{ namespace, name string }

			callback := func(namespace, name string) {
				actualCallbacks = append(actualCallbacks, struct{ namespace, name string }{
					namespace: namespace,
					name:      name,
				})
			}

			invokeForNamespaceAndNames(logger, tc.obj, tc.lookup, callback)

			// Verify callbacks
			assert.Equal(t, tc.expectedCallbacks, actualCallbacks, "Callbacks should match expected values")
		})
	}
}

func TestGetAdditionalObjectIndexNameForNameAndNsArray(t *testing.T) {
	testCases := []struct {
		name           string
		lookup         []string
		expectedResult string
	}{
		{
			name:           "empty lookup array",
			lookup:         []string{},
			expectedResult: "",
		},
		{
			name:           "single element",
			lookup:         []string{"spec"},
			expectedResult: "spec",
		},
		{
			name:           "two elements",
			lookup:         []string{"spec", "services"},
			expectedResult: "spec/services",
		},
		{
			name:           "multiple elements",
			lookup:         []string{"spec", "targets", "services", "names"},
			expectedResult: "spec/targets/services/names",
		},
		{
			name:           "elements with special characters",
			lookup:         []string{"metadata", "annotations", "service.mesh/enabled"},
			expectedResult: "metadata/annotations/service.mesh/enabled",
		},
		{
			name:           "elements with empty strings",
			lookup:         []string{"spec", "", "services"},
			expectedResult: "spec//services",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := getAdditionalObjectIndexNameForNameAndNsArray(tc.lookup)
			assert.Equal(t, tc.expectedResult, result)
		})
	}
}

func TestGetAdditionalObjectIndexStringForNameAndNsArray(t *testing.T) {
	result := getAdditionalObjectIndexStringForNameAndNsArray("test-namespace", "test-service")
	assert.Equal(t, "test-namespace/test-service", result)
}

func TestAddEventHandlerForBySvcNameAndNsArray(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	policy := unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":            "test-policy",
				"namespace":       "test-ns",
				"resourceVersion": "1",
				"generation":      int64(1),
			},
			"spec": map[string]interface{}{
				"services": []interface{}{
					map[string]interface{}{
						"name":      "test-service",
						"namespace": "test-ns",
					},
				},
			},
		},
	}

	policyNewGeneration := policy.DeepCopy()
	policyNewGeneration.SetResourceVersion("2")
	policyNewGeneration.SetGeneration(int64(2))

	crossNsPolicy := unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "test-policy",
				"namespace": "test-ns",
			},
			"spec": map[string]interface{}{
				"services": []interface{}{
					map[string]interface{}{
						"name":      "test-service",
						"namespace": "other-ns", // Different namespace
					},
				},
			},
		},
	}

	invalidLookupPolicy := unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "test-policy",
				"namespace": "test-ns",
			},
			"spec": map[string]interface{}{},
		},
	}

	testCases := []struct {
		name                string
		eventType           string
		obj                 *unstructured.Unstructured
		oldObj              *unstructured.Unstructured
		lookup              []string
		expectEnqueueCalled int
	}{
		{
			name:                "Add event with valid service reference",
			eventType:           "Add",
			obj:                 &policy,
			lookup:              []string{"spec", "services"},
			expectEnqueueCalled: 1,
		},
		{
			name:                "Update event with generation change",
			eventType:           "Update",
			oldObj:              &policy,
			obj:                 policyNewGeneration,
			lookup:              []string{"spec", "services"},
			expectEnqueueCalled: 2,
		},
		{
			name:                "Update event with same resource version - should skip",
			eventType:           "Update",
			oldObj:              &policy,
			obj:                 &policy,
			lookup:              []string{"spec", "services"},
			expectEnqueueCalled: 0,
		},
		{
			name:                "Delete event",
			eventType:           "Delete",
			obj:                 &policy,
			lookup:              []string{"spec", "services"},
			expectEnqueueCalled: 1,
		},
		{
			name:                "Add event with cross-namespace reference",
			eventType:           "Add",
			obj:                 &crossNsPolicy,
			lookup:              []string{"spec", "services"},
			expectEnqueueCalled: 0,
		},
		{
			name:                "Add event with missing lookup path",
			eventType:           "Add",
			obj:                 &invalidLookupPolicy,
			lookup:              []string{"spec", "nonexistent"},
			expectEnqueueCalled: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a mock service to return from the lister
			testService := kube_test.CreateServiceWithLabels("test-ns", "test-service", map[string]string{})
			otherService := kube_test.CreateServiceWithLabels("other-test-ns", "other-test-service", map[string]string{})

			stopCh := make(chan struct{})
			defer close(stopCh)

			queue := &TestableQueue{}
			fakeClient := kube_test.NewKubeClientBuilder().
				AddK8sObjects(testService, otherService).
				Build()

			fakeCluster := &Cluster{
				ID:     "fake-cluster",
				Client: fakeClient,
				multiclusterServiceController: &testableMulticlusterServiceController{
					enqueuer: NewSingleQueueEnqueuer(queue),
				},
			}
			clusterManager := &testableClusterManager{cluster: fakeCluster}
			fakeInformer := fakeClient.DynamicInformerFactory().ForResource(constants2.TrafficShardingPolicyResource)
			serviceInformer := fakeClient.KubeInformerFactory().Core().V1().Services()

			addIndexerBySvcNameAndNsArray(fakeInformer.Informer(), logger, tc.lookup)
			eventHandler := createEventHandlerForBySvcNsArray(logger, tc.lookup, clusterManager)

			fakeClient.DynamicInformerFactory().Start(stopCh)
			fakeClient.DynamicInformerFactory().WaitForCacheSync(stopCh)
			go serviceInformer.Informer().Run(stopCh)
			cache.WaitForCacheSync(stopCh, serviceInformer.Informer().HasSynced)

			if tc.eventType == "Add" {
				eventHandler.OnAdd(tc.obj, false)
			} else if tc.eventType == "Update" {
				eventHandler.OnUpdate(tc.oldObj, tc.obj)
			} else if tc.eventType == "Delete" {
				eventHandler.OnDelete(tc.obj)
			}

			assert.Equal(t, tc.expectEnqueueCalled, len(queue.recordedItems))
			for _, item := range queue.recordedItems {
				assert.Equal(t, QueueItem(QueueItem{key: "test-ns/test-service", event: controllers_api.EventUpdate, cluster: "fake-cluster"}), item)
			}
		})
	}
}

// testableMulticlusterServiceController is a mock implementation for testing
type testableMulticlusterServiceController struct {
	enqueuer controllers_api.ObjectEnqueuer
}

func (t *testableMulticlusterServiceController) Run(_ int, _ <-chan struct{}) error {
	panic("implement me")
}

func (t *testableMulticlusterServiceController) OnNewClusterAdded(_ controllers_api.Cluster) error {
	panic("implement me")
}

func (t *testableMulticlusterServiceController) GetObjectEnqueuer() controllers_api.ObjectEnqueuer {
	return t.enqueuer
}
