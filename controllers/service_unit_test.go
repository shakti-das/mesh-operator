package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	"github.com/istio-ecosystem/mesh-operator/pkg/deployment"

	"github.com/istio-ecosystem/mesh-operator/pkg/statefulset"

	"github.com/istio-ecosystem/mesh-operator/pkg/rollout"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	kubetest "github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	appsv1 "k8s.io/api/apps/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/client-go/tools/cache"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
)

var (
	dynamicRoutingLabelsAsStr             = common.GetStringForAttribute(constants.DynamicRoutingLabels)
	dynamicRoutingDefaultCoordinatesAsStr = common.GetStringForAttribute(constants.DynamicRoutingDefaultCoordinates)
)

func TestEnqueueMopByService(t *testing.T) {
	clusterName := "test-cluster"
	q1 := &TestableQueue{}
	q1.AddRateLimited(NewQueueItem(clusterName, fmt.Sprintf("%s/%s", "test-namespace", "mop1"), "uid-1", controllers_api.EventUpdate))

	testCases := []struct {
		name                 string
		service              *corev1.Service
		mopInCluster         []runtime.Object
		enqueueExpected      bool
		expectedMopWorkQueue TestableQueue
	}{
		{
			name:    "EnqueueMop",
			service: kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "ordering"}),
			mopInCluster: []runtime.Object{
				kube_test.NewMopBuilder("test-namespace", "mop1").SetUID("uid-1").AddSelector("app", "ordering").Build(),
				kube_test.NewMopBuilder("test-namespace", "mop2").SetUID("uid-2").Build(),
				kube_test.NewMopBuilder("other-namespace", "mop3").SetUID("uid-3").AddSelector("app", "ordering").Build(),
			},
			enqueueExpected:      true,
			expectedMopWorkQueue: *q1,
		},
		{
			name:    "NoMopToEnqueue",
			service: kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "ordering"}),
			mopInCluster: []runtime.Object{
				kube_test.NewMopBuilder("test-namespace", "mop1").SetUID("uid-1").AddSelector("app", "ordering-bg").Build(),
				kube_test.NewMopBuilder("other-namespace", "mop2").SetUID("uid-2").AddSelector("app", "ordering-bg").Build(),
			},
			enqueueExpected: false,
		},
		{
			name:            "MopDontExistInCluster",
			service:         kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "ordering"}),
			mopInCluster:    []runtime.Object{},
			enqueueExpected: false,
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var mopWorkQueue = &TestableQueue{}

			fakeClient := kube_test.NewKubeClientBuilder().
				AddMopClientObjects(tc.mopInCluster...).
				Build()
			fakeClient.MopInformerFactory().Mesh().V1alpha1().MeshOperators().Lister()

			fakeClient.MopInformerFactory().Start(stopCh)
			fakeClient.MopInformerFactory().WaitForCacheSync(stopCh)

			EnqueueMopReferencingGivenService(
				zaptest.NewLogger(t).Sugar(),
				clusterName,
				fakeClient,
				NewSingleQueueEnqueuer(mopWorkQueue),
				tc.service)

			if tc.enqueueExpected {
				assert.True(t, compareQueue(tc.expectedMopWorkQueue, *mopWorkQueue))
			} else {
				assert.Equal(t, 0, len(mopWorkQueue.recordedItems))
			}
		})
	}
}

func TestExtractStsMetadata(t *testing.T) {
	stsNameList := []string{"stsA", "stsB"}

	headlessSvc := kube_test.CreateService("nameSpace5", "serviceName5")
	headlessSvc.Spec.ClusterIP = "None"

	testCases := []struct {
		name                    string
		statefulSet             *appsv1.StatefulSet
		secondStatefulSet       *appsv1.StatefulSet
		service                 *corev1.Service
		multiStsWarningExpected bool
		expectedMetadata        map[string]string
	}{
		{
			name:        "SuccessfulDequeue",
			service:     kube_test.CreateService("nameSpace1", "serviceName1"),
			statefulSet: kube_test.CreateStatefulSet("nameSpace1", "testSts", "serviceName1", testReplicaNumber),
			expectedMetadata: map[string]string{
				"statefulSetReplicaName":   "testSts",
				"statefulSetReplicas":      "1",
				"statefulSetOrdinalsStart": "0",
				"statefulSets":             "[{\"statefulSetReplicaName\":\"testSts\",\"statefulSetReplicas\":\"1\"}]",
			},
		},
		{
			name:             "SuccessfulDequeue_noSTSFound",
			service:          kube_test.CreateService("nameSpace2", "serviceName2"),
			statefulSet:      kube_test.CreateStatefulSet("nameSpace2", "name", "wrongServiceName", testReplicaNumber),
			expectedMetadata: map[string]string{},
		},
		{
			name:                    "SuccessfulDequeue_multipleSTSFound",
			service:                 kube_test.CreateService("nameSpace3", "serviceName3"),
			statefulSet:             kube_test.CreateStatefulSet("nameSpace3", "stsA", "serviceName3", testReplicaNumber),
			secondStatefulSet:       kube_test.CreateStatefulSet("nameSpace3", "stsB", "serviceName3", testReplicaNumber),
			multiStsWarningExpected: true,
			expectedMetadata: map[string]string{
				"statefulSetReplicaName":   "stsA",
				"statefulSetReplicas":      "1",
				"statefulSetOrdinalsStart": "0",
				"statefulSets":             "[{\"statefulSetReplicaName\":\"stsA\",\"statefulSetReplicas\":\"1\"}]",
			},
		},
		{
			name:              "SuccessfulDequeue_multipleSTSFound_headlessService_noWarning",
			service:           headlessSvc,
			statefulSet:       kube_test.CreateStatefulSet("nameSpace5", "stsA", "serviceName5", testReplicaNumber),
			secondStatefulSet: kube_test.CreateStatefulSet("nameSpace5", "stsB", "serviceName5", testReplicaNumber),
		},
		{
			name:        "SuccessfulDequeue_withOrdinalsStart",
			service:     kube_test.CreateService("nameSpace4", "serviceName4"),
			statefulSet: kube_test.NewStatefulSetBuilder("nameSpace4", "testStsWithOrdinals", "serviceName4", testReplicaNumber).SetOrdinalsStart(5).Build(),
			expectedMetadata: map[string]string{
				"statefulSetReplicaName":   "testStsWithOrdinals",
				"statefulSetReplicas":      "1",
				"statefulSetOrdinalsStart": "5",
				"statefulSets":             "[{\"statefulSetReplicaName\":\"testStsWithOrdinals\",\"statefulSetReplicas\":\"1\"}]",
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {

		clientBuilder := kube_test.NewKubeClientBuilder()

		if tc.secondStatefulSet != nil {
			clientBuilder.AddK8sObjects(tc.statefulSet, tc.secondStatefulSet)
		} else {
			clientBuilder.AddK8sObjects(tc.statefulSet)
		}

		client := clientBuilder.Build()

		stsInformer := client.KubeInformerFactory().Apps().V1().StatefulSets()

		var recorder = &kubetest.TestableEventRecorder{}

		err := statefulset.AddStsIndexerIfNotExists(stsInformer.Informer())
		assert.Nil(t, err)

		go stsInformer.Informer().Run(ctx.Done())
		cache.WaitForCacheSync(ctx.Done(), stsInformer.Informer().HasSynced)

		meta, err := ExtractStsMetadata(zaptest.NewLogger(t).Sugar(), tc.service, []kube.Client{client}, recorder)
		assert.Nil(t, err)

		if tc.multiStsWarningExpected {
			expectedServiceReference := &corev1.ObjectReference{
				Kind:            constants.ServiceKind.Kind,
				APIVersion:      constants.ServiceResource.GroupVersion().String(),
				ResourceVersion: constants.ServiceResource.Version,
				Name:            tc.service.Name,
				Namespace:       tc.service.Namespace,
				UID:             tc.service.UID,
			}
			assert.Equal(t, expectedServiceReference, recorder.RecordedObject)
			assert.Equal(t, corev1.EventTypeWarning, recorder.Eventtype)
			assert.Equal(t, constants.MeshConfigError, recorder.Reason)
			assert.Equal(t, "service is used by multiple statefulsets", recorder.Message)

			// since the metadata returned can map to any one of the multiple sts referencing the given svc, so check sts name belongs in the expected sts name list
			assert.Equal(t, common.SliceContains(stsNameList, meta[constants.ContextStsReplicaName]), true)
			assert.Equal(t, meta[constants.ContextStsReplicas], "1")
			assert.Equal(t, meta[constants.ContextStsOrdinalsStart], "0")
			stss := []map[string]string{}
			err = json.Unmarshal([]byte(meta[constants.ContextStatefulSets]), &stss)
			assert.Nil(t, err)
			for _, stsName := range stsNameList {
				assert.Contains(t, stss, map[string]string{
					constants.ContextStsReplicaName: stsName,
					constants.ContextStsReplicas:    "1",
				})
			}
		} else if tc.secondStatefulSet != nil {
			// Headless service with multiple STS: metadata should be populated but no warning event emitted
			assert.Nil(t, recorder.RecordedObject, "no event should be recorded for headless service with multiple STS")
			assert.Equal(t, common.SliceContains(stsNameList, meta[constants.ContextStsReplicaName]), true)
		} else {
			assert.Equal(t, tc.expectedMetadata, meta)
		}
	}
}

func TestExtractDynamicRoutingMetadata(t *testing.T) {
	svcNoDynamicRouting := kube_test.CreateServiceAsUnstructuredObject(namespace, serviceName)

	svcNoDynamicRoutingLabels := kube_test.CreateServiceAsUnstructuredObject(namespace, serviceName)

	dynamicRoutingSvc := kube_test.CreateServiceAsUnstructuredObject(namespace, serviceName)
	dynamicRoutingSvc.SetAnnotations(map[string]string{
		dynamicRoutingLabelsAsStr: "version,app",
	})

	dynamicRoutingSvcWithDefault := kube_test.CreateServiceAsUnstructuredObject(namespace, serviceName)
	dynamicRoutingSvcWithDefault.SetAnnotations(map[string]string{
		dynamicRoutingLabelsAsStr:             "version,app",
		dynamicRoutingDefaultCoordinatesAsStr: "version=v1,app=my-app",
	})

	dynamicRoutingSvcWithRolloutAsDefault := kube_test.CreateServiceAsUnstructuredObject(namespace, serviceName)
	dynamicRoutingSvcWithRolloutAsDefault.SetAnnotations(map[string]string{
		dynamicRoutingLabelsAsStr:             "version,app",
		dynamicRoutingDefaultCoordinatesAsStr: "version=v4,app=rollout",
	})

	matchingDeployment := kube_test.NewDeploymentBuilder(namespace, "deployment1").
		SetLabels(map[string]string{dynamicRoutingServiceLabelAsStr: serviceName}).
		SetTemplateLabels(map[string]string{
			"version": "v1",
			"app":     "my-app",
		}).
		Build()

	otherMatchingDeployment := kube_test.NewDeploymentBuilder(namespace, "deployment2").
		SetLabels(map[string]string{dynamicRoutingServiceLabelAsStr: serviceName}).
		SetTemplateLabels(map[string]string{
			"version": "v2",
			"app":     "my-app",
		}).
		Build()

	nonMatchingDeployment := kube_test.NewDeploymentBuilder(namespace, "deployment1").
		SetLabels(
			map[string]string{
				dynamicRoutingServiceLabelAsStr: "other-service",
			}).Build()

	matchingRollout := kube_test.NewRolloutBuilder("rollout1", namespace).SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: dynamicRoutingSvc.GetName()}).SetTemplateLabel(map[string]string{
		"version": "v3",
		"app":     "rollout",
	}).Build()
	otherMatchingRollout := kube_test.NewRolloutBuilder("rollout2", namespace).SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: dynamicRoutingSvc.GetName()}).SetTemplateLabel(map[string]string{
		"version": "v4",
		"app":     "rollout",
	}).Build()

	nonMatchingRollout := kube_test.NewRolloutBuilder("rollout2", namespace).SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: "some-service"}).SetTemplateLabel(map[string]string{
		"version": "v4",
		"app":     "rollout",
	}).Build()

	testCases := []struct {
		name                                       string
		enableDynamicRouting                       bool
		enableDynamicRoutingForBGAndCanaryServices bool
		service                                    *unstructured.Unstructured
		deploymentsInCluster                       []runtime.Object
		rolloutObjsInCluster                       []runtime.Object
		expectedMetadata                           map[string]string
	}{
		{
			name:                 "Extract subsets",
			enableDynamicRouting: true,
			service:              dynamicRoutingSvc,
			deploymentsInCluster: []runtime.Object{matchingDeployment, otherMatchingDeployment},
			expectedMetadata: map[string]string{
				"dynamicRoutingCombinations": "{\"Deployment\":{\"v1-my-app\":{\"app\":\"my-app\",\"version\":\"v1\"},\"v2-my-app\":{\"app\":\"my-app\",\"version\":\"v2\"}}}",
			},
		},
		{
			name:                 "Extract subsets with default subset defined",
			enableDynamicRouting: true,
			service:              dynamicRoutingSvcWithDefault,
			deploymentsInCluster: []runtime.Object{matchingDeployment, otherMatchingDeployment},
			expectedMetadata: map[string]string{
				"dynamicRoutingCombinations":  "{\"Deployment\":{\"v1-my-app\":{\"app\":\"my-app\",\"version\":\"v1\"},\"v2-my-app\":{\"app\":\"my-app\",\"version\":\"v2\"}}}",
				"dynamicRoutingDefaultSubset": "v1-my-app",
			},
		},
		{
			name:                 "Dynamic routing disabled",
			enableDynamicRouting: false,
			expectedMetadata:     map[string]string{},
		},
		{
			name:                 "Service with no dynamic routing",
			enableDynamicRouting: true,
			service:              svcNoDynamicRouting,
			expectedMetadata:     map[string]string{},
		},
		{
			name:                 "Service with no dynamic routing labels",
			enableDynamicRouting: true,
			service:              svcNoDynamicRoutingLabels,
			expectedMetadata:     map[string]string{},
		},
		{
			name:                 "No related deployments",
			enableDynamicRouting: true,
			service:              dynamicRoutingSvc,
			expectedMetadata:     map[string]string{},
		},
		{
			name:                 "No matching deployments",
			enableDynamicRouting: true,
			service:              dynamicRoutingSvc,
			deploymentsInCluster: []runtime.Object{nonMatchingDeployment},
			expectedMetadata:     map[string]string{},
		},
		{
			name:                 "No matching (deployments + rollouts)",
			enableDynamicRouting: true,
			service:              dynamicRoutingSvc,
			deploymentsInCluster: []runtime.Object{nonMatchingDeployment, nonMatchingRollout},
			expectedMetadata:     map[string]string{},
		},
		{
			name:                 "Extract subsets for rollout object",
			enableDynamicRouting: true,
			enableDynamicRoutingForBGAndCanaryServices: true,
			service:              dynamicRoutingSvc,
			rolloutObjsInCluster: []runtime.Object{matchingRollout, otherMatchingRollout},
			expectedMetadata: map[string]string{
				"dynamicRoutingCombinations": "{\"Rollout\":{\"v3-rollout\":{\"app\":\"rollout\",\"version\":\"v3\"},\"v4-rollout\":{\"app\":\"rollout\",\"version\":\"v4\"}}}",
			},
		},
		{
			name:                 "Extract subsets for deployment & rollout object",
			enableDynamicRouting: true,
			enableDynamicRoutingForBGAndCanaryServices: true,
			service:              dynamicRoutingSvc,
			deploymentsInCluster: []runtime.Object{matchingDeployment, otherMatchingDeployment},
			rolloutObjsInCluster: []runtime.Object{matchingRollout, otherMatchingRollout},
			expectedMetadata: map[string]string{
				"dynamicRoutingCombinations": "{" +
					"\"Deployment\":{\"v1-my-app\":{\"app\":\"my-app\",\"version\":\"v1\"},\"v2-my-app\":{\"app\":\"my-app\",\"version\":\"v2\"}}," +
					"\"Rollout\":{\"v3-rollout\":{\"app\":\"rollout\",\"version\":\"v3\"},\"v4-rollout\":{\"app\":\"rollout\",\"version\":\"v4\"}}" +
					"}",
			},
		},
		{
			name:                 "Extract subsets with default subset from Deployment",
			enableDynamicRouting: true,
			enableDynamicRoutingForBGAndCanaryServices: true,
			service:              dynamicRoutingSvcWithDefault,
			deploymentsInCluster: []runtime.Object{matchingDeployment, otherMatchingDeployment},
			rolloutObjsInCluster: []runtime.Object{matchingRollout, otherMatchingRollout},
			expectedMetadata: map[string]string{
				"dynamicRoutingCombinations": "{\"Deployment\":{\"v1-my-app\":{\"app\":\"my-app\",\"version\":\"v1\"},\"v2-my-app\":{\"app\":\"my-app\",\"version\":\"v2\"}}," +
					"\"Rollout\":{\"v3-rollout\":{\"app\":\"rollout\",\"version\":\"v3\"},\"v4-rollout\":{\"app\":\"rollout\",\"version\":\"v4\"}}}",
				"dynamicRoutingDefaultSubset": "v1-my-app",
			},
		},
		{
			name:                 "Extract subsets with default subset from Rollout",
			enableDynamicRouting: true,
			enableDynamicRoutingForBGAndCanaryServices: true,
			service:              dynamicRoutingSvcWithRolloutAsDefault,
			deploymentsInCluster: []runtime.Object{matchingDeployment, otherMatchingDeployment},
			rolloutObjsInCluster: []runtime.Object{matchingRollout, otherMatchingRollout},
			expectedMetadata: map[string]string{
				"dynamicRoutingCombinations": "{\"Deployment\":{\"v1-my-app\":{\"app\":\"my-app\",\"version\":\"v1\"},\"v2-my-app\":{\"app\":\"my-app\",\"version\":\"v2\"}}," +
					"\"Rollout\":{\"v3-rollout\":{\"app\":\"rollout\",\"version\":\"v3\"},\"v4-rollout\":{\"app\":\"rollout\",\"version\":\"v4\"}}}",
				"dynamicRoutingDefaultSubset": "v4-rollout",
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.EnableDynamicRouting = tc.enableDynamicRouting
			features.EnableDynamicRoutingForBGAndCanaryServices = tc.enableDynamicRoutingForBGAndCanaryServices
			features.EnableArgoIntegration = true
			restore := func() {
				features.EnableDynamicRouting = false
				features.EnableDynamicRoutingForBGAndCanaryServices = false
				features.EnableArgoIntegration = false
			}
			defer restore()

			fakeClient := kube_test.NewKubeClientBuilder().
				AddK8sObjects(tc.deploymentsInCluster...).
				AddDynamicClientObjects(tc.rolloutObjsInCluster...).
				Build()
			deploymentInformer := fakeClient.KubeInformerFactory().Apps().V1().Deployments()
			stsInformer := fakeClient.KubeInformerFactory().Apps().V1().StatefulSets()
			rolloutInformer := fakeClient.DynamicInformerFactory().ForResource(constants.RolloutResource).Informer()
			_ = rollout.AddServiceNameToRolloutIndexer(rolloutInformer)

			err := deployment.AddDeploymentIndexerIfNotExists(deploymentInformer.Informer())
			assert.Nil(t, err)
			err = statefulset.AddStsIndexerIfNotExists(stsInformer.Informer())
			assert.Nil(t, err)

			go deploymentInformer.Informer().Run(ctx.Done())
			cache.WaitForCacheSync(ctx.Done(), deploymentInformer.Informer().HasSynced)
			go stsInformer.Informer().Run(ctx.Done())
			cache.WaitForCacheSync(ctx.Done(), stsInformer.Informer().HasSynced)

			go rolloutInformer.Run(ctx.Done())
			cache.WaitForCacheSync(ctx.Done(), rolloutInformer.HasSynced)

			metadata, err := ExtractDynamicRoutingMetadata(zaptest.NewLogger(t).Sugar(), tc.service, prometheus.NewRegistry(), []kube.Client{fakeClient})

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedMetadata, metadata)
		})
	}
}

func TestExtractDynamicRoutingMetadataFromStatefulSet(t *testing.T) {
	svcNoDynamicRouting := kube_test.CreateServiceAsUnstructuredObject(namespace, serviceName)

	svcNoDynamicRoutingLabels := kube_test.CreateServiceAsUnstructuredObject(namespace, serviceName)

	dynamicRoutingSvc := kube_test.CreateServiceAsUnstructuredObject(namespace, serviceName)
	dynamicRoutingSvc.SetAnnotations(map[string]string{
		dynamicRoutingLabelsAsStr: "version,app",
	})

	dynamicRoutingSvcWithDefault := kube_test.CreateServiceAsUnstructuredObject(namespace, serviceName)
	dynamicRoutingSvcWithDefault.SetAnnotations(map[string]string{
		dynamicRoutingLabelsAsStr:             "version,app",
		dynamicRoutingDefaultCoordinatesAsStr: "version=v1,app=my-app",
	})
	matchingStatefulSet := kube_test.NewStatefulSetBuilder(namespace, "sts1", serviceName, 2).
		SetTemplateLabels(map[string]string{
			"version": "v1",
			"app":     "my-app",
		}).
		Build()

	otherMatchingStatefulSet := kube_test.NewStatefulSetBuilder(namespace, "sts2", serviceName, 2).
		SetTemplateLabels(map[string]string{
			"version": "v2",
			"app":     "my-app",
		}).
		Build()

	nonMatchingStatefulSet := kube_test.NewStatefulSetBuilder(namespace, "sts1", "other-service", 2).Build()

	testCases := []struct {
		name                  string
		featureEnabled        bool
		service               *unstructured.Unstructured
		statefulSetsInCluster []runtime.Object
		expectedMetadata      map[string]string
	}{
		{
			name:                  "Extract subsets",
			featureEnabled:        true,
			service:               dynamicRoutingSvc,
			statefulSetsInCluster: []runtime.Object{matchingStatefulSet, otherMatchingStatefulSet},
			expectedMetadata: map[string]string{
				"dynamicRoutingCombinations": "{\"Statefulset\":{\"v1-my-app\":{\"app\":\"my-app\",\"version\":\"v1\"},\"v2-my-app\":{\"app\":\"my-app\",\"version\":\"v2\"}}}",
			},
		},
		{
			name:                  "Extract subsets with default subset defined",
			featureEnabled:        true,
			service:               dynamicRoutingSvcWithDefault,
			statefulSetsInCluster: []runtime.Object{matchingStatefulSet, otherMatchingStatefulSet},
			expectedMetadata: map[string]string{
				"dynamicRoutingCombinations":  "{\"Statefulset\":{\"v1-my-app\":{\"app\":\"my-app\",\"version\":\"v1\"},\"v2-my-app\":{\"app\":\"my-app\",\"version\":\"v2\"}}}",
				"dynamicRoutingDefaultSubset": "v1-my-app",
			},
		},
		{
			name:             "Dynamic routing disabled",
			featureEnabled:   false,
			expectedMetadata: map[string]string{},
		},
		{
			name:             "Service with no dynamic routing",
			featureEnabled:   true,
			service:          svcNoDynamicRouting,
			expectedMetadata: map[string]string{},
		},
		{
			name:             "Service with no dynamic routing labels",
			featureEnabled:   true,
			service:          svcNoDynamicRoutingLabels,
			expectedMetadata: map[string]string{},
		},
		{
			name:             "No related statefulsets",
			featureEnabled:   true,
			service:          dynamicRoutingSvc,
			expectedMetadata: map[string]string{},
		},
		{
			name:                  "No matching statefulsets",
			featureEnabled:        true,
			service:               dynamicRoutingSvc,
			statefulSetsInCluster: []runtime.Object{nonMatchingStatefulSet},
			expectedMetadata:      map[string]string{},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.EnableDynamicRouting = tc.featureEnabled
			restore := func() {
				features.EnableDynamicRouting = false
			}
			defer restore()

			fakeClient := kube_test.NewKubeClientBuilder().
				AddK8sObjects(tc.statefulSetsInCluster...).
				Build()
			deploymentInformer := fakeClient.KubeInformerFactory().Apps().V1().Deployments()
			stsInformer := fakeClient.KubeInformerFactory().Apps().V1().StatefulSets()

			err := deployment.AddDeploymentIndexerIfNotExists(deploymentInformer.Informer())
			assert.Nil(t, err)
			err = statefulset.AddStsIndexerIfNotExists(stsInformer.Informer())
			assert.Nil(t, err)

			go deploymentInformer.Informer().Run(ctx.Done())
			cache.WaitForCacheSync(ctx.Done(), deploymentInformer.Informer().HasSynced)
			go stsInformer.Informer().Run(ctx.Done())
			cache.WaitForCacheSync(ctx.Done(), stsInformer.Informer().HasSynced)

			metadata, err := ExtractDynamicRoutingMetadata(zaptest.NewLogger(t).Sugar(), tc.service, prometheus.NewRegistry(), []kube.Client{fakeClient})

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedMetadata, metadata)
		})
	}
}

func compareQueue(q1 TestableQueue, q2 TestableQueue) bool {
	if len(q1.recordedItems) != len(q2.recordedItems) {
		return false
	}
	l := len(q1.recordedItems)
	for i := 0; i < l; i++ {
		if !assert.ObjectsAreEqual(q1.recordedItems[i], q2.recordedItems[i]) {
			return false
		}
	}
	return true
}
