package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	"github.com/istio-ecosystem/mesh-operator/pkg/reconcilemetadata"

	"github.com/istio-ecosystem/mesh-operator/pkg/cluster"

	kubeinformers "k8s.io/client-go/informers"

	"go.uber.org/zap"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	mopv1alpha1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8stesting "k8s.io/client-go/testing"

	kubetest "github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"

	"k8s.io/apimachinery/pkg/types"

	"github.com/istio-ecosystem/mesh-operator/pkg/templating_test"

	"github.com/istio-ecosystem/mesh-operator/pkg/resources_test"

	"k8s.io/client-go/util/workqueue"

	"github.com/istio-ecosystem/mesh-operator/pkg/controller_test"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/client-go/tools/cache"

	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	meshOpErrors "github.com/istio-ecosystem/mesh-operator/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	testReplicaNumber         = int32(1)
	genericTestError          = fmt.Errorf("TestError")
	targetPortError           = &meshOpErrors.UserConfigError{Message: fmt.Sprintf("unable to generate mesh routing config, please provide exact integer port as targetPort")}
	nonCriticalReconcileError = &meshOpErrors.NonCriticalReconcileError{Message: "Some non-critical error"}

	serviceNamespace = "test-namespace"
	serviceName      = "good-object"
	serviceUid       = types.UID("uid-1")

	serviceObject = corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   serviceNamespace,
			Name:        serviceName,
			UID:         serviceUid,
			Annotations: map[string]string{},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
	}

	// Object reference is used to record events
	serviceObjectReference = corev1.ObjectReference{
		Kind:            "Service",
		APIVersion:      "v1",
		ResourceVersion: "v1",
		Namespace:       serviceNamespace,
		Name:            serviceName,
		UID:             serviceUid,
	}

	svcWithTagretPortAsStringContext = corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: serviceNamespace,
			Name:      serviceName,
			UID:       serviceUid,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Name: "http", Port: 7442, TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "whatever"}}},
		},
	}
	serviceKey = "test-namespace/good-object"

	dynamicRoutingServiceLabelAsStr = common.GetStringForAttribute(constants.DynamicRoutingServiceLabel)
)

func TestEnqueueService(t *testing.T) {
	testCluster := "cluster-name"

	testCases := []struct {
		name            string
		object          *corev1.Service
		event           controllers_api.Event
		enqueueExpected bool
	}{
		{
			name:            "EnqueueAddEvent",
			object:          &serviceObject,
			event:           controllers_api.EventAdd,
			enqueueExpected: true,
		},
		{
			name:            "EnqueueUpdateEvent",
			object:          &serviceObject,
			event:           controllers_api.EventUpdate,
			enqueueExpected: true,
		},
		{
			name:            "EnqueueDeleteEvent",
			object:          &serviceObject,
			event:           controllers_api.EventDelete,
			enqueueExpected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var queue = &TestableQueue{}
			serviceEnqueuer := NewSingleQueueEnqueuer(queue)

			serviceEnqueuer.Enqueue(testCluster, tc.object, tc.event)

			q := NewQueueItem(
				testCluster,
				fmt.Sprintf("%s/%s", serviceNamespace, serviceName),
				serviceUid,
				tc.event)
			if tc.enqueueExpected {
				assert.Equal(t, 1, len(queue.recordedItems))
				assert.Equal(t, q, queue.recordedItems[0])
			} else {
				assert.Equal(t, 0, len(queue.recordedItems))
			}
		})
	}
}

func TestEnqueueStatefulSetService(t *testing.T) {
	clusterName := " test-cluster"

	testCases := []struct {
		name             string
		statefulSet      interface{}
		serviceInCluster *corev1.Service
		stsEvent         controllers_api.Event
		enqueueExpected  bool
	}{
		{
			name:             "EnqueueSvcUpdateEvent",
			statefulSet:      kube_test.CreateStatefulSet(serviceNamespace, "sts1", serviceName, testReplicaNumber),
			serviceInCluster: &serviceObject,
			stsEvent:         controllers_api.EventUpdate,
			enqueueExpected:  true,
		},
		{
			name:             "EnqueueSvcDeleteEvent",
			statefulSet:      kube_test.CreateStatefulSet(serviceNamespace, "sts2", serviceName, testReplicaNumber),
			serviceInCluster: &serviceObject,
			stsEvent:         controllers_api.EventDelete,
			enqueueExpected:  true,
		},
		{
			name: "TombstoneReceivedInsteadOfStatefulSet",
			statefulSet: cache.DeletedFinalStateUnknown{
				Key: "namespace3/sts3",
				Obj: kube_test.CreateStatefulSet(serviceNamespace, "sts3", serviceName, testReplicaNumber),
			},
			serviceInCluster: &serviceObject,
			stsEvent:         controllers_api.EventUpdate,
			enqueueExpected:  true,
		},
		{
			name:            "ServiceNotPresent",
			statefulSet:     kube_test.CreateStatefulSet(serviceNamespace, "sts1", serviceName, testReplicaNumber),
			stsEvent:        controllers_api.EventUpdate,
			enqueueExpected: false,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := kube_test.NewKubeClientBuilder().
				AddK8sObjects(tc.serviceInCluster).
				Build()
			// This is needed to register the informer
			fakeClient.KubeInformerFactory().Core().V1().Services().Informer()
			fakeClient.RunAndWait(ctx.Done(), false, nil)

			var queue = &TestableQueue{}
			logger := zaptest.NewLogger(t).Sugar()
			optimizer := NewServiceReconcileOptimizer(fakeClient.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas(), reconcilemetadata.NewReconcileHashManager())

			EnqueueServicesForStatefulSet(logger, clusterName, NewSingleQueueEnqueuer(queue), fakeClient, tc.stsEvent, optimizer, tc.statefulSet)

			if tc.enqueueExpected {
				// We expect EventUpdate enqueued for a service, regardless of the type of event for STS.
				q := NewQueueItem(clusterName, "test-namespace/good-object", serviceUid, controllers_api.EventUpdate)
				assert.Equal(t, 1, len(queue.recordedItems))
				assert.Equal(t, q, queue.recordedItems[0])
			} else {
				assert.Equal(t, 0, len(queue.recordedItems))
			}
		})
	}
}

func TestEnqueueServiceForDeployments(t *testing.T) {
	testCluster := "cluster-name"

	testCases := []struct {
		name              string
		relatedObject     interface{}
		referencedService string
		serviceInCluster  *corev1.Service
		event             controllers_api.Event
		enqueueExpected   bool
	}{
		{
			name:              "Deployment: EnqueueSuccessful",
			referencedService: serviceName,
			relatedObject: kube_test.NewDeploymentBuilder(serviceNamespace, "test").
				SetLabels(map[string]string{dynamicRoutingServiceLabelAsStr: serviceName}).
				Build(),
			serviceInCluster: &serviceObject,
			event:            controllers_api.EventUpdate,
			enqueueExpected:  true,
		},
		{
			name: "Deployment: MissingDynamicRoutingServiceLabel",
			relatedObject: kube_test.NewDeploymentBuilder(serviceNamespace, "test").
				SetLabels(map[string]string{dynamicRoutingServiceLabelAsStr: serviceName}).
				Build(),
			enqueueExpected: false,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := kube_test.NewKubeClientBuilder().
				AddK8sObjects(tc.serviceInCluster).
				Build()
			// This is needed to register the informer
			fakeClient.KubeInformerFactory().Core().V1().Services().Informer()
			fakeClient.RunAndWait(ctx.Done(), false, nil)

			var queue = &TestableQueue{}

			logger := zaptest.NewLogger(t).Sugar()
			optimizer := NewServiceReconcileOptimizer(fakeClient.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas(), reconcilemetadata.NewReconcileHashManager())

			EnqueueServicesForDeployments(logger, testCluster, NewSingleQueueEnqueuer(queue), fakeClient, tc.event, optimizer, tc.relatedObject)

			q := NewQueueItem(
				testCluster,
				fmt.Sprintf("%s/%s", serviceNamespace, tc.referencedService),
				serviceUid,
				tc.event)
			if tc.enqueueExpected {
				assert.Equal(t, 1, len(queue.recordedItems))
				assert.Equal(t, q, queue.recordedItems[0])
			} else {
				assert.Equal(t, 0, len(queue.recordedItems))
			}
		})
	}
}

func TestEnqueueServiceForRolloutObj(t *testing.T) {
	testNamespace := "mesh"
	activeServiceName := "activeSvc"
	testClusterName := "testCluster"

	dynamicRoutingEnabledService := kubetest.CreateService(testNamespace, activeServiceName)
	dynamicRoutingEnabledService.SetAnnotations(map[string]string{string(constants.DynamicRoutingLabels): "version,app"})
	dynamicRoutingEnabledService.SetUID(serviceUid)

	dynamicRoutingDisabledService := kubetest.CreateService(testNamespace, activeServiceName)
	dynamicRoutingDisabledService.SetUID(serviceUid)

	someService := kubetest.CreateService(testNamespace, "testService")
	someService.SetUID(serviceUid)

	testCases := []struct {
		name              string
		rolloutObject     *unstructured.Unstructured
		serviceInCluster  *corev1.Service
		activeServiceName string
		rolloutEvent      controllers_api.Event
		enqueueExpected   bool
	}{
		{
			name:              "EnqueueSuccessful(For dynamic routing scenario)",
			rolloutObject:     kube_test.NewRolloutBuilder("test", testNamespace).SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: activeServiceName}).SetTemplateLabel(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build(),
			serviceInCluster:  dynamicRoutingEnabledService,
			activeServiceName: activeServiceName,
			rolloutEvent:      controllers_api.EventUpdate,
			enqueueExpected:   true,
		},
		{
			name:              "No Enqueue (Non dynamic routing case)",
			rolloutObject:     kube_test.NewRolloutBuilder("test", testNamespace).SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: activeServiceName}).SetTemplateLabel(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build(),
			serviceInCluster:  dynamicRoutingDisabledService,
			activeServiceName: activeServiceName,
			rolloutEvent:      controllers_api.EventUpdate,
			enqueueExpected:   false,
		},
		{
			name:              "No Enqueue - Service Missing In Cluster",
			rolloutObject:     kube_test.NewRolloutBuilder("test", testNamespace).SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: activeServiceName}).SetTemplateLabel(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build(),
			serviceInCluster:  someService,
			activeServiceName: activeServiceName,
			rolloutEvent:      controllers_api.EventUpdate,
			enqueueExpected:   false,
		},
		{
			name:             "No Enqueue - Missing Active Svc Annotation",
			rolloutObject:    kube_test.NewRolloutBuilder("test", namespace).SetTemplateLabel(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build(),
			serviceInCluster: dynamicRoutingEnabledService,
			rolloutEvent:     controllers_api.EventUpdate,
			enqueueExpected:  false,
		},
		{
			name:             "No Enqueue - Empty Active Service Annotation",
			rolloutObject:    kube_test.NewRolloutBuilder("test", namespace).SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: ""}).SetTemplateLabel(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build(),
			serviceInCluster: dynamicRoutingEnabledService,
			rolloutEvent:     controllers_api.EventUpdate,
			enqueueExpected:  false,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		var queue = &TestableQueue{}

		fakeClient := kube_test.NewKubeClientBuilder().AddK8sObjects(tc.serviceInCluster).AddDynamicClientObjects(tc.rolloutObject).Build()
		fakeClient.KubeInformerFactory().Core().V1().Services().Informer()
		fakeClient.RunAndWait(ctx.Done(), false, nil)

		cluster := &Cluster{ID: cluster.ID(testClusterName), Client: fakeClient, namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{}}

		multiClusterSvcController := &MulticlusterServiceController{
			serviceEnqueuer:    NewSingleQueueEnqueuer(queue),
			logger:             zaptest.NewLogger(t).Sugar(),
			reconcileOptimizer: NewServiceReconcileOptimizer(fakeClient.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas(), reconcilemetadata.NewReconcileHashManager()),
		}

		multiClusterSvcController.enqueueServicesForRollout(cluster, controllers_api.EventUpdate, tc.rolloutObject)

		q := NewQueueItem(testClusterName, fmt.Sprintf("%s/%s", testNamespace, tc.activeServiceName), serviceUid, tc.rolloutEvent)

		if tc.enqueueExpected {
			assert.Equal(t, 1, len(queue.recordedItems))
			assert.Equal(t, q, queue.recordedItems[0])
		} else {
			assert.Equal(t, 0, len(queue.recordedItems))
		}

	}
}

func TestEnqueueServiceOnDeploymentChange(t *testing.T) {
	var clusterName = "test-cluster"

	deploymentObjWithDynamicRoutingSvcLabel := kube_test.NewDeploymentBuilder(namespace, "deployment1").
		SetLabels(map[string]string{dynamicRoutingServiceLabelAsStr: serviceName}).
		SetTemplateLabels(map[string]string{
			"version": "v1",
			"app":     "my-app",
		}).
		Build()

	deploymentObjWithoutDynamicRoutingSvcLabel := kube_test.NewDeploymentBuilder(namespace, "deployment2").SetTemplateLabels(map[string]string{
		"version": "v2",
		"app":     "my-app",
	}).
		Build()

	testCases := []struct {
		name                      string
		featureEnabled            bool
		service                   *unstructured.Unstructured
		deploymentsInCluster      []runtime.Object
		objectsInCluster          []runtime.Object
		serviceEnqueueExpected    bool
		expectedServiceQueueItems []QueueItem
	}{
		{
			name:                   "EnqueueService - DynamicRoutingServiceLabel Present",
			featureEnabled:         true,
			objectsInCluster:       []runtime.Object{deploymentObjWithDynamicRoutingSvcLabel, &serviceObject},
			serviceEnqueueExpected: true,
			expectedServiceQueueItems: []QueueItem{
				NewQueueItem(clusterName, fmt.Sprintf("%s/%s", serviceNamespace, serviceName), serviceUid, controllers_api.EventAdd),
				// Any Deployment event results in an EventUpdate for a service
				NewQueueItem(clusterName, fmt.Sprintf("%s/%s", serviceNamespace, serviceName), serviceUid, controllers_api.EventUpdate),
			},
		},
		{
			name:                   "ServiceNotEnqueued - DynamicRoutingServiceLabel Absent",
			featureEnabled:         true,
			objectsInCluster:       []runtime.Object{deploymentObjWithoutDynamicRoutingSvcLabel, &serviceObject},
			serviceEnqueueExpected: false,
			expectedServiceQueueItems: []QueueItem{
				NewQueueItem(clusterName, fmt.Sprintf("%s/%s", serviceNamespace, serviceName), serviceUid, controllers_api.EventAdd),
			},
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.EnableDynamicRouting = tc.featureEnabled
			restore := func() {
				features.EnableDynamicRouting = false
			}
			defer restore()

			client := kube_test.NewKubeClientBuilder().AddK8sObjects(tc.objectsInCluster...).Build()

			var recorder = &kubetest.TestableEventRecorder{}
			var queue = &TestableQueue{}

			var registry = prometheus.NewRegistry()

			cluster := &Cluster{
				ID:              cluster.ID(clusterName),
				Client:          client,
				primary:         true,
				eventRecorder:   recorder,
				namespaceFilter: &controller_test.FakeNamespacesNamespaceFilter{},
			}

			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			clusterManager.SetPrimaryCluster(cluster)

			svcController := &MulticlusterServiceController{
				logger:                       logger,
				serviceEnqueuer:              NewSingleQueueEnqueuer(queue),
				clusterManager:               clusterManager,
				primaryClient:                client,
				resourceManager:              resources_test.NewFakeResourceManager(),
				metricsRegistry:              registry,
				multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
				reconcileOptimizer:           &fakeReconcileOptimizer{},
			}

			svcController.OnNewClusterAdded(cluster)

			serviceInformer := client.KubeInformerFactory().Core().V1().Services()
			go serviceInformer.Informer().Run(ctx.Done())
			cache.WaitForCacheSync(ctx.Done(), serviceInformer.Informer().HasSynced)

			deploymentInformer := client.KubeInformerFactory().Apps().V1().Deployments()
			go deploymentInformer.Informer().Run(ctx.Done())
			cache.WaitForCacheSync(ctx.Done(), deploymentInformer.Informer().HasSynced)

			waitForEnqueue(queue, len(tc.expectedServiceQueueItems))

			assert.Equal(t, len(tc.expectedServiceQueueItems), len(queue.recordedItems))
			assert.ElementsMatch(t, tc.expectedServiceQueueItems, queue.recordedItems)

		})
	}
}

func waitForEnqueue(queue *TestableQueue, expectedQueueLength int) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	for {
		if len(queue.recordedItems) >= expectedQueueLength || ctx.Err() != nil {
			break
		}
	}
}

func TestReconcileOnRaceCondition(t *testing.T) {
	serviceKey = serviceNamespace + "/" + serviceName
	var clusterName = "primary"

	testCases := []struct {
		name                            string
		item                            QueueItem
		servicesInCluster               []runtime.Object
		remoteClusterReconcilingService bool
		expectedError                   error
	}{
		{
			name: "Race condition - Reconcile Error",
			item: NewQueueItem(
				clusterName,
				serviceKey,
				"uid-1",
				controllers_api.EventUpdate),
			servicesInCluster:               []runtime.Object{&serviceObject},
			remoteClusterReconcilingService: true,
			expectedError:                   &meshOpErrors.NonCriticalReconcileError{Message: fmt.Sprintf("race condition detected for service key: %v", serviceKey)},
		},
		{
			name: "No race condition - Reconcile Successful",
			item: NewQueueItem(
				clusterName,
				serviceKey,
				"uid-1",
				controllers_api.EventUpdate),
			servicesInCluster: []runtime.Object{&serviceObject},
			expectedError:     nil,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			serviceKeyMapper := make(map[string]struct{})

			// track service if remote cluster is reconciling serviceKey
			if tc.remoteClusterReconcilingService {
				serviceKeyMapper[serviceKey] = struct{}{}
			}

			multiClusterReconcileTracker := multiClusterReconcileManager{
				logger:    logger,
				keyMapper: serviceKeyMapper,
			}

			client := kube_test.NewKubeClientBuilder().AddK8sObjects(tc.servicesInCluster...).AddDynamicClientObjects().Build()

			renderer := &templating_test.TestRendererForMop{
				TemplateType: templateType,
			}

			var recorder = &kubetest.TestableEventRecorder{}
			var registry = prometheus.NewRegistry()

			serviceQueue := workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(0, 0), "Services")

			cluster := &Cluster{
				ID:            cluster.ID(clusterName),
				Client:        client,
				primary:       true,
				eventRecorder: recorder,
			}

			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			clusterManager.SetPrimaryCluster(cluster)

			configGenerator := NewConfigGenerator(
				renderer,
				[]templating.Mutator{},
				nil,
				&templating_test.TestableApplicator{},
				templating.ApplyOverlays,
				zaptest.NewLogger(t).Sugar(),
				registry)

			svcController := &MulticlusterServiceController{
				logger:                       logger,
				serviceEnqueuer:              NewSingleQueueEnqueuer(serviceQueue),
				configGenerator:              configGenerator,
				clusterManager:               clusterManager,
				primaryClient:                client,
				resourceManager:              resources_test.NewFakeResourceManager(),
				metricsRegistry:              registry,
				multiClusterReconcileTracker: &multiClusterReconcileTracker,
			}

			client.RunAndWait(ctx.Done(), true, []kubeinformers.GenericInformer{})

			err := svcController.reconcile(tc.item)
			if tc.expectedError != nil {
				assert.ErrorAs(t, err, &tc.expectedError)
			} else {
				assert.Nil(t, err)

				// validates serviceKey was removed from Tracker
				_, serviceKeyFoundInTracker := multiClusterReconcileTracker.keyMapper[serviceKey]
				assert.False(t, serviceKeyFoundInTracker)
			}
		})
	}
}

func TestCreateMeshConfig(t *testing.T) {
	var clusterName = "cluster-1"

	nsCreationError := fmt.Errorf("failed to create namespace %s", serviceObject.Namespace)

	service := kube_test.CreateServiceWithLabels("test-namespace", "good-object", map[string]string{"key-1": "val-2"})
	service.UID = serviceUid
	service.CreationTimestamp = metav1.Time{Time: time.Now()}
	stripedServiceInPrimary := kube_test.CreateServiceWithLabels("test-namespace", "good-object", map[string]string{"key-1": "val-2"})
	stripedServiceInPrimary.UID = "primary-svc-uid"
	stripedServiceInPrimary.CreationTimestamp = metav1.Time{Time: time.Now().Add(-100 * time.Hour)}

	remoteNs := kubetest.CreateNamespace(namespace, map[string]string{"operator.mesh.io/enabled": "true"})
	primaryNs := kubetest.CreateNamespace("random", map[string]string{"operator.mesh.io/enabled": "true"})

	nsInPrimaryCluster1 := []runtime.Object{remoteNs, primaryNs}
	nsInPrimaryCluster2 := []runtime.Object{primaryNs}

	applicatorResult := []*templating.AppliedConfigObject{
		{
			Object: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": constants.VirtualServiceResource.GroupVersion().String(),
					"kind":       constants.VirtualServiceKind.Kind,
				},
			},
		},
	}

	testCases := []struct {
		name                  string
		stripedService        bool
		dryRun                bool
		errorNsCreation       error
		errorOnTrack          error
		errorOnTrackChange    error
		errorOnRender         error
		errorOnApply          error
		k8sApplyError         error
		mops                  []*mopv1alpha1.MeshOperator
		expectedMops          []*mopv1alpha1.MeshOperator
		trackOnChangeExpected bool
		allowOverlays         bool
	}{
		{
			name: "ErrorCreatingRemoteNamespace",
			errorNsCreation: fmt.Errorf("failed while creating the namespace %s in primary cluster for %s:%s."+
				" Error: %w", serviceObject.Namespace, "service", serviceObject.Name, nsCreationError),
		},
		{
			name:         "Track service error",
			errorOnTrack: fmt.Errorf("on-track-error"),
		},
		{
			name:          "Render error",
			errorOnRender: fmt.Errorf("render-error"),
		},
		{
			name:               "On service change error",
			errorOnTrackChange: fmt.Errorf("on-track-change-error"),
		},
		{
			name:                  "no MOPs, plain reconcile",
			trackOnChangeExpected: true,
		},
		{
			name:                  "no MOPs, plain reconcile (striped)",
			stripedService:        true,
			trackOnChangeExpected: true,
		},
		{
			name:                  "single Mop overlay applied",
			mops:                  []*mopv1alpha1.MeshOperator{mopWithRetriesOverlay},
			expectedMops:          []*mopv1alpha1.MeshOperator{mopWithRetriesOverlayWithSuccessStatus},
			trackOnChangeExpected: true,
			allowOverlays:         true,
		},
		{
			name:                  "multiple Mop overlays applied",
			mops:                  []*mopv1alpha1.MeshOperator{mopWithRetriesOverlay, mopWithOverlay2},
			expectedMops:          []*mopv1alpha1.MeshOperator{mopWithRetriesOverlayWithSuccessStatus, mopWithOverlay2WithStatus},
			trackOnChangeExpected: true,
			allowOverlays:         true,
		},
		{
			name:                  "multiple Mop overlays applied - only MOP with overlays is getting updated",
			mops:                  []*mopv1alpha1.MeshOperator{mopWithRetriesOverlay, mopWithNoOverlay},
			expectedMops:          []*mopv1alpha1.MeshOperator{mopWithRetriesOverlayWithSuccessStatus, mopWithNoOverlay},
			trackOnChangeExpected: true,
			allowOverlays:         true,
		},
		{
			name:                  "Mop overlay applied - does not update root status if any of the svc status is in pending",
			mops:                  []*mopv1alpha1.MeshOperator{mopWithOverlay1PendingSvcStatus},
			expectedMops:          []*mopv1alpha1.MeshOperator{mopWithOverlay1RootStatusPending},
			trackOnChangeExpected: true,
			allowOverlays:         true,
		},
		{
			name:                  "Resources tracked on overlaying error",
			errorOnApply:          getOverlayingError("test-mop", 0, "test-overlaying-error"),
			trackOnChangeExpected: true,
			allowOverlays:         true,
		},
		{
			name:                  "Resources not tracked on non-overlaying error",
			errorOnApply:          fmt.Errorf("test-apply-error"),
			trackOnChangeExpected: false,
		},
		{
			name:                  "Error if application failed",
			k8sApplyError:         fmt.Errorf("test-k8s-apply-error"),
			trackOnChangeExpected: false,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		test := func(t *testing.T) {
			metadata := map[string]string{"key-1": "val-2"}
			manager := resources_test.NewFakeResourceManager()
			manager.ErrorOnTrack = tc.errorOnTrack
			manager.ErrorOnChange = tc.errorOnTrackChange
			manager.MsmOnTrack = mopv1alpha1.MeshServiceMetadata{
				TypeMeta: metav1.TypeMeta{
					APIVersion: mopv1alpha1.ApiVersion,
					Kind:       mopv1alpha1.MsmKind.Kind,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "good-object",
					Namespace: namespace,
					Labels:    metadata,
				},
			}

			renderer := &templating_test.FixedResultRenderer{
				ResultToReturn: templating.GeneratedConfig{
					Config: map[string][]*unstructured.Unstructured{
						templateType: {
							{
								Object: map[string]interface{}{
									"apiVersion": "networking.istio.io/v1alpha3",
									"kind":       "VirtualService",
								},
							},
						},
					},
					TemplateType: templateType,
				},
			}
			renderer.ErrorToThrow = tc.errorOnRender

			applicator := &templating_test.TestableApplicator{
				ApplicatorError:    tc.errorOnApply,
				AppliedResults:     []templating_test.GeneratedConfigMetadata{},
				K8sPatchErrorToAdd: tc.k8sApplyError,
			}

			metricsRegistry := prometheus.NewRegistry()

			mopObjects := []runtime.Object{}
			for _, mopObject := range tc.mops {
				mopObjects = append(mopObjects, mopObject)
			}

			client := kubetest.NewKubeClientBuilder().AddDynamicClientObjects(remoteNs).
				AddK8sObjects(service).
				AddMopClientObjects(mopObjects...).Build()

			primaryClientBuilder := kubetest.NewKubeClientBuilder().
				AddDynamicClientObjects(nsInPrimaryCluster1...)
			if tc.stripedService {
				primaryClientBuilder.AddK8sObjects(stripedServiceInPrimary)
			}
			primaryClient := primaryClientBuilder.Build()

			mopInformerFactory := client.MopInformerFactory()
			mopInformerFactory.Start(stopCh)
			mopInformerFactory.WaitForCacheSync(stopCh)

			primaryInformerFactory := primaryClient.KubeInformerFactory()
			primaryInformerFactory.Start(stopCh)
			primaryInformerFactory.WaitForCacheSync(stopCh)

			kubeInformerFactory := client.KubeInformerFactory()
			kubeInformerFactory.Start(stopCh)
			kubeInformerFactory.WaitForCacheSync(stopCh)

			if tc.errorNsCreation != nil {
				primaryClient = kubetest.NewKubeClientBuilder().AddDynamicClientObjects(nsInPrimaryCluster2...).Build()
				primaryFakeClient := (primaryClient).(*kube_test.FakeClient)
				primaryFakeClient.KubeClient.PrependReactor("create", constants.NamespaceResource.Resource, func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, nsCreationError
				})
			}

			features.EnableServiceConfigOverlays = tc.allowOverlays
			defer func() { features.EnableServiceConfigOverlays = false }()

			configGenerator := NewConfigGenerator(renderer, []templating.Mutator{}, nil, applicator, templating.DontApplyOverlays, logger, metricsRegistry)

			eventRecorder := &TestableEventRecorder{}
			queueItem := NewQueueItem(clusterName, "test-namespace/good-object", serviceUid, controllers_api.EventUpdate)

			remoteCluster := &Cluster{
				Client:        client,
				ID:            cluster.ID(clusterName),
				eventRecorder: eventRecorder,
			}
			primaryCluster := &Cluster{
				Client:        primaryClient,
				eventRecorder: &TestableEventRecorder{},
			}

			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			clusterManager.SetPrimaryCluster(primaryCluster)
			clusterManager.AddClusterForKey("key", remoteCluster)

			ctrl := MulticlusterServiceController{
				logger:                       logger,
				clusterManager:               clusterManager,
				primaryClient:                primaryClient,
				resourceManager:              manager,
				configGenerator:              configGenerator,
				metricsRegistry:              metricsRegistry,
				multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
			}
			err := ctrl.reconcile(queueItem)

			expectedConfigMetadata := templating_test.GeneratedConfigMetadata{
				ObjectKey: "test-namespace/good-object",
				Metadata:  map[string]string{},
				Owners: []metav1.OwnerReference{
					{
						APIVersion: "mesh.io/v1alpha1",
						Kind:       "MeshServiceMetadata",
						Name:       "good-object",
					},
				},
				TemplatesRendered: []string{templateType},
				ApplicatorResult:  applicatorResult,
			}
			expectedConfigMetadataWithoutOwnerReference := templating_test.GeneratedConfigMetadata{
				ObjectKey:         "test-namespace/good-object",
				Metadata:          map[string]string{},
				TemplatesRendered: []string{templateType},
				ApplicatorResult:  applicatorResult,
			}
			expectedTrackRequests := []resources_test.TrackRequest{
				{
					ClusterName: clusterName,
					Namespace:   serviceNamespace,
					Kind:        constants.ServiceKind.Kind,
					Name:        serviceName,
					UID:         serviceUid,
				},
			}
			expectedOnChangeRequests := []resources_test.OnChangeRequest{
				{
					ClusterName: clusterName,
					ServiceKey:  "test-namespace/good-object",
					Resources: []*unstructured.Unstructured{
						{
							Object: map[string]interface{}{
								"apiVersion": "networking.istio.io/v1alpha3",
								"kind":       "VirtualService",
							},
						},
					},
				},
			}

			if tc.trackOnChangeExpected {
				assert.Equal(t, 1, len(manager.RecordedOnChangeRequests))
			} else {
				assert.Equal(t, 0, len(manager.RecordedOnChangeRequests))
			}

			if tc.errorNsCreation != nil {
				assert.NotNil(t, err)
				assert.Error(t, tc.errorNsCreation, err.Error())   // assert ns creation failed with given error
				assert.Len(t, applicator.AppliedResults, 0)        // assert no object being patched
				assert.Len(t, manager.RecordedTrackRequests, 0)    // no track request recorded
				assert.Len(t, manager.RecordedOnChangeRequests, 0) // no change request recorded
			} else if tc.errorOnTrack != nil {
				// mesh config created successfully
				assert.Nil(t, err) // assert no mesh config creation error
				applicator.AssertResults(t, []templating_test.GeneratedConfigMetadata{expectedConfigMetadataWithoutOwnerReference})
				assert.Len(t, manager.RecordedTrackRequests, 0)    // no track request recorded
				assert.Len(t, manager.RecordedOnChangeRequests, 0) // no change request recorded
			} else if tc.errorOnRender != nil {
				assert.NotNil(t, err)
				assert.ErrorAs(t, err, &tc.errorOnRender)
				assert.ElementsMatch(t, expectedTrackRequests, manager.RecordedTrackRequests)
			} else if tc.errorOnApply != nil && meshOpErrors.IsOverlayingError(tc.errorOnApply) {
				assert.Nil(t, err)
				assert.ElementsMatch(t, expectedTrackRequests, manager.RecordedTrackRequests)
				assert.Equal(t, corev1.EventTypeWarning, eventRecorder.eventtype)
				assert.Equal(t, constants.UnableToGenerateMeshConfig, eventRecorder.reason)
				assert.Equal(t, tc.errorOnApply.Error(), eventRecorder.message)
			} else if tc.errorOnApply != nil {
				assert.NotNil(t, err)
				assert.ErrorAs(t, err, &tc.errorOnApply)
				assert.ElementsMatch(t, expectedTrackRequests, manager.RecordedTrackRequests)
			} else if tc.k8sApplyError != nil {
				assert.NotNil(t, err)
				assert.ErrorAs(t, err, &tc.k8sApplyError)
				assert.ElementsMatch(t, expectedTrackRequests, manager.RecordedTrackRequests)
			} else if tc.errorOnTrackChange != nil {
				assert.NotNil(t, err)
				assert.ErrorAs(t, err, &tc.errorOnTrackChange)
				applicator.AssertResults(t, []templating_test.GeneratedConfigMetadata{expectedConfigMetadata})
			} else {
				// Success case
				assert.Nil(t, err)
				applicator.AssertResults(t, []templating_test.GeneratedConfigMetadata{expectedConfigMetadata})
				assert.ElementsMatch(t, expectedTrackRequests, manager.RecordedTrackRequests)
				assert.ElementsMatch(t, expectedOnChangeRequests, manager.RecordedOnChangeRequests)

				// verify mop status update
				for _, expectedMop := range tc.expectedMops {
					mop, err := client.MopApiClient().MeshV1alpha1().MeshOperators("test-namespace").Get(context.TODO(), expectedMop.Name, metav1.GetOptions{})
					assert.NoError(t, err)
					assert.Equal(t, expectedMop.Status, mop.Status)
				}
			}

			features.EnableServiceConfigOverlays = false
		}

		t.Run(tc.name, test)
	}
}

func TestDeleteMeshConfig(t *testing.T) {
	var primaryClusterName = "primary-cluster"
	var clusterName = "test-cluster"
	var namespace = "test-namespace"
	var kind = "Service"
	var serviceName = "svc-1"
	var uid = types.UID("uid-1")

	stripedService := kube_test.NewServiceBuilder(serviceName, namespace).
		SetUID("striped-uid-1").Build()

	stripedServiceRoutingDisabled := kube_test.NewServiceBuilder(serviceName, namespace).
		SetUID("striped-uid-1").
		SetAnnotations(map[string]string{string(constants.RoutingConfigEnabledAnnotation): "false"}).
		Build()

	testCases := []struct {
		name                     string
		stripedService           *corev1.Service
		expectedTrackRequest     resources_test.TrackRequest
		errorOnUntrack           error
		enqueueOfStripedExpected bool
	}{
		{
			name:                 "Success",
			expectedTrackRequest: resources_test.TrackRequest{ClusterName: clusterName, Namespace: namespace, Kind: kind, Name: name, UID: uid},
		},
		{
			name:           "Error",
			errorOnUntrack: fmt.Errorf("test-error"),
		},
		{
			name:                     "Striped service: Update enqueued if other services present on delete",
			expectedTrackRequest:     resources_test.TrackRequest{ClusterName: clusterName, Namespace: namespace, Kind: kind, Name: name, UID: uid},
			stripedService:           &stripedService,
			enqueueOfStripedExpected: true,
		},
		{
			name:                     "Striped service with routing disabled: no update enqueued",
			expectedTrackRequest:     resources_test.TrackRequest{ClusterName: clusterName, Namespace: namespace, Kind: kind, Name: name, UID: uid},
			stripedService:           &stripedServiceRoutingDisabled,
			enqueueOfStripedExpected: false,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		test := func(t *testing.T) {
			stopCh := make(chan struct{})
			defer close(stopCh)

			manager := resources_test.NewFakeResourceManager()
			manager.ErrorOnUntrack = tc.errorOnUntrack

			registry := prometheus.NewRegistry()

			client := kube_test.NewKubeClientBuilder().Build()
			primaryClientBuilder := kube_test.NewKubeClientBuilder()
			if tc.stripedService != nil {
				primaryClientBuilder.AddK8sObjects(tc.stripedService)
			}
			primaryClient := primaryClientBuilder.Build()

			informerFactory := primaryClient.KubeInformerFactory()
			informerFactory.Start(stopCh)
			informerFactory.WaitForCacheSync(stopCh)

			queueItem := NewQueueItem(clusterName, namespace+"/"+serviceName, uid, controllers_api.EventDelete)

			queue := &TestableQueue{}
			serviceEnqueuer := NewSingleQueueEnqueuer(queue)
			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			primaryCluster := &Cluster{
				Client: primaryClient,
				ID:     cluster.ID(primaryClusterName),
			}
			remoteCluster := &Cluster{
				Client: client,
			}

			clusterManager.SetPrimaryCluster(primaryCluster)
			clusterManager.AddClusterForKey("key", remoteCluster)

			ctrl := MulticlusterServiceController{
				logger:                       logger,
				clusterManager:               clusterManager,
				resourceManager:              manager,
				metricsRegistry:              registry,
				multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
				serviceEnqueuer:              serviceEnqueuer,
			}

			err := ctrl.reconcile(queueItem)

			if tc.errorOnUntrack != nil {
				assert.NotNil(t, err)
				assert.ErrorAs(t, err, &tc.errorOnUntrack)
				assert.Equal(t, 0, len(manager.RecordedUnTrackRequests))
			} else {
				assert.Nil(t, err)
				assert.Equal(t, 1, len(manager.RecordedUnTrackRequests))
				assert.Equal(t,
					resources_test.TrackRequest{ClusterName: clusterName, Namespace: namespace, Kind: kind, Name: serviceName, UID: uid},
					manager.RecordedUnTrackRequests[0])
			}
			if tc.stripedService != nil && tc.enqueueOfStripedExpected {
				expectedQueueItem := NewQueueItem(primaryClusterName, "test-namespace/svc-1", "striped-uid-1", controllers_api.EventUpdate)
				assert.Equal(t, 1, len(queue.recordedItems))
				assert.Equal(t, expectedQueueItem, queue.recordedItems[0])
			} else {
				assert.Equal(t, 0, len(queue.recordedItems))
			}
		}

		t.Run(tc.name, test)
	}
}

func TestUpdateOverlayingMopStatus_updateStatusError(t *testing.T) {
	var clusterName = "test-cluster"
	testsvc := kube_test.CreateServiceWithLabels("test-namespace", "testsvc", map[string]string{"key-1": "val-2"})
	mopWithRetriesOverlay = kube_test.NewMopBuilder(serviceNamespace, "mop1").AddSelector("key-1", "val-2").
		AddOverlay(retriesOverlay).Build()
	mopWithTimeoutOverlay = kube_test.NewMopBuilder(serviceNamespace, "mop1.1").AddSelector("key-1", "val-2").
		AddOverlay(timeoutOverlay).Build()
	testCases := []struct {
		name                            string
		service                         *corev1.Service
		mopsInCluster                   []runtime.Object
		mops                            []*mopv1alpha1.MeshOperator
		mopStatusUpdateErrorMessageList []string
		expectedStatusUpdateError       error
	}{
		{
			name:                            "Multiple MOPs (MOP1 status update fails + MOP2 status update succeeded)",
			service:                         testsvc,
			mopsInCluster:                   []runtime.Object{mopWithRetriesOverlay, mopWithTimeoutOverlay},
			mops:                            []*mopv1alpha1.MeshOperator{mopWithRetriesOverlay, mopWithTimeoutOverlay},
			mopStatusUpdateErrorMessageList: []string{"resource version conflict", ""},
			expectedStatusUpdateError:       fmt.Errorf("resource version conflict"),
		},
		{
			name:                            "Multiple MOPs (MOP1 status update succeeded + MOP2 status update fails)",
			service:                         testsvc,
			mopsInCluster:                   []runtime.Object{mopWithRetriesOverlay, mopWithTimeoutOverlay},
			mops:                            []*mopv1alpha1.MeshOperator{mopWithRetriesOverlay, mopWithTimeoutOverlay},
			mopStatusUpdateErrorMessageList: []string{"", "resource version conflict"},
			expectedStatusUpdateError:       fmt.Errorf("resource version conflict"),
		},
		{
			name:                            "Multiple MOPs (Both the MOP status update - success)",
			service:                         testsvc,
			mopsInCluster:                   []runtime.Object{mopWithRetriesOverlay, mopWithTimeoutOverlay},
			mops:                            []*mopv1alpha1.MeshOperator{mopWithRetriesOverlay, mopWithTimeoutOverlay},
			mopStatusUpdateErrorMessageList: []string{"", ""},
		},
		{
			name:                            "Multiple MOPs (Both the MOP status update - fails)",
			service:                         testsvc,
			mopsInCluster:                   []runtime.Object{mopWithRetriesOverlay, mopWithTimeoutOverlay},
			mops:                            []*mopv1alpha1.MeshOperator{mopWithRetriesOverlay, mopWithTimeoutOverlay},
			mopStatusUpdateErrorMessageList: []string{"resource version conflict", "resource version conflict error"},
			expectedStatusUpdateError:       fmt.Errorf("resource version conflict error"),
		},
		{
			name:                            "Single MOP (Status update success)",
			service:                         testsvc,
			mopsInCluster:                   []runtime.Object{mopWithRetriesOverlay},
			mops:                            []*mopv1alpha1.MeshOperator{mopWithRetriesOverlay},
			mopStatusUpdateErrorMessageList: []string{""},
		},
		{
			name:                            "Single MOP (Status update fails)",
			service:                         testsvc,
			mopsInCluster:                   []runtime.Object{mopWithRetriesOverlay},
			mops:                            []*mopv1alpha1.MeshOperator{mopWithRetriesOverlay},
			mopStatusUpdateErrorMessageList: []string{"resource version conflict"},
			expectedStatusUpdateError:       fmt.Errorf("resource version conflict"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			client := kube_test.NewKubeClientBuilder().AddMopClientObjects(tc.mopsInCluster...).Build()
			fakeClient := (client).(*kube_test.FakeClient)

			metricsRegistry := prometheus.NewRegistry()

			fakeClient.MopClient.PrependReactor("update", "meshoperators", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
				ret = action.(k8stesting.CreateAction).GetObject()
				meta, ok := ret.(metav1.Object)
				if !ok {
					return
				}
				for idx, mop := range tc.mops {
					if mop.Name == meta.GetName() {
						if tc.mopStatusUpdateErrorMessageList[idx] != "" {
							return true, nil, fmt.Errorf("%s", tc.mopStatusUpdateErrorMessageList[idx])
						}
						return true, nil, nil
					}
				}
				return true, nil, nil
			})

			logger := zaptest.NewLogger(t).Sugar()

			err := updateOverlayingMopStatusForService(
				logger,
				tc.service,
				tc.mops,
				nil,
				nil,
				func(logger *zap.SugaredLogger, service *corev1.Service, validationError error, errInfo *meshOpErrors.OverlayErrorInfo, mop *mopv1alpha1.MeshOperator) error {
					return UpdateOverlayMopStatus(logger, client.MopApiClient(), clusterName, service, validationError, errInfo, mop, metricsRegistry)
				})

			if tc.expectedStatusUpdateError != nil {
				assert.EqualError(t, err, tc.expectedStatusUpdateError.Error())
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestUpdateOverlayingMopStatusForService_applicationAndOverlayErrors(t *testing.T) {
	var clusterName = "test-cluster"
	var mopName = "mop1"
	var serviceName = "testsvc"
	var testsvc = kube_test.CreateServiceWithLabels("test-namespace", serviceName, map[string]string{"key-1": "val-2"})
	var mopWithOverlay = kube_test.NewMopBuilder(serviceNamespace, mopName).
		AddSelector("key-1", "val-2").
		AddOverlay(retriesOverlay).Build()

	testCases := []struct {
		name             string
		generationError  error
		applicationError error
		expectedStatus   *mopv1alpha1.MeshOperatorStatus
	}{
		{
			name: "Overlaying error",
			generationError: &meshOpErrors.OverlayingErrorImpl{
				ErrorMap: map[string]*meshOpErrors.OverlayErrorInfo{
					mopName: {
						OverlayIndex: 0,
						Message:      "test overlay error",
					},
				},
			},
			expectedStatus: &mopv1alpha1.MeshOperatorStatus{
				Message: "issues with applying overlay",
				Phase:   PhaseFailed,
				Services: map[string]*mopv1alpha1.ServiceStatus{
					serviceName: {
						Phase:   PhaseFailed,
						Message: "error encountered when overlaying: mop mop1, overlay <0>: test overlay error",
					},
				},
			},
		},
		{
			name:             "Application error",
			applicationError: fmt.Errorf("application-error"),
			expectedStatus: &mopv1alpha1.MeshOperatorStatus{
				Message: "issues with applying overlay",
				Phase:   PhaseFailed,
				Services: map[string]*mopv1alpha1.ServiceStatus{
					serviceName: {
						Phase:   PhaseFailed,
						Message: "error encountered when overlaying: mop mop1, overlay <0>: application-error",
					},
				},
			},
		},
		{
			name: "No error",
			expectedStatus: &mopv1alpha1.MeshOperatorStatus{
				Phase: PhaseSucceeded,
				Services: map[string]*mopv1alpha1.ServiceStatus{
					serviceName: {
						Phase: PhaseSucceeded,
					},
				},
			},
		},
		{
			name:            "Non-overlaying generation error",
			generationError: fmt.Errorf("other generation error"),
			expectedStatus: &mopv1alpha1.MeshOperatorStatus{
				Phase: PhaseSucceeded,
				Services: map[string]*mopv1alpha1.ServiceStatus{
					serviceName: {
						Phase: PhaseSucceeded,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		client := kube_test.NewKubeClientBuilder().AddMopClientObjects(mopWithOverlay).Build()

		metricsRegistry := prometheus.NewRegistry()
		t.Run(tc.name, func(t *testing.T) {
			updateStatusError := updateOverlayingMopStatusForService(
				zaptest.NewLogger(t).Sugar(),
				testsvc,
				[]*mopv1alpha1.MeshOperator{mopWithOverlay},
				tc.generationError,
				tc.applicationError,
				func(logger *zap.SugaredLogger, service *corev1.Service, validationError error, errInfo *meshOpErrors.OverlayErrorInfo, mop *mopv1alpha1.MeshOperator) error {
					return UpdateOverlayMopStatus(logger, client.MopApiClient(), clusterName, service, validationError, errInfo, mop, metricsRegistry)
				})

			assert.NoError(t, updateStatusError)

			mopInCluster, _ := client.MopApiClient().MeshV1alpha1().MeshOperators(mopWithOverlay.Namespace).Get(context.TODO(), mopName, metav1.GetOptions{})
			assert.Equal(t, tc.expectedStatus, &mopInCluster.Status)
		})
	}
}

func TestSvcControllerEventHandling(t *testing.T) {
	testNamespace := "test-ns"
	clusterName := "test-cluster"

	svc := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   testNamespace,
			Name:        serviceName,
			UID:         serviceUid,
			Annotations: map[string]string{},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
	}

	testCases := []struct {
		name string
		item QueueItem

		objectReadFromCluster *corev1.Service
		expectedMetricLabel   map[string]string
	}{
		{
			name: "Successful Add in svc controller",
			item: NewQueueItem(
				clusterName,
				"test-ns/item-name",
				"uid-1",
				controllers_api.EventAdd),
			objectReadFromCluster: &svc,
			expectedMetricLabel: map[string]string{
				commonmetrics.ClusterLabel:      "test-cluster",
				commonmetrics.NamespaceLabel:    "test-ns",
				commonmetrics.ResourceNameLabel: commonmetrics.DeprecatedLabel,
				commonmetrics.EventTypeLabel:    "add",
			},
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var registry = prometheus.NewRegistry()

			client := kube_test.NewKubeClientBuilder().AddDynamicClientObjects().Build()
			cluster := &Cluster{
				ID:     "cluster-1",
				Client: client,
			}
			clusterManager := controllers_api.NewClustersStore(logger, nil, time.Second, time.Second)
			clusterManager.SetPrimaryCluster(cluster)
			serviceQueue := workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(0, 0), "Services")

			clientMulti := kube_test.NewKubeClientBuilder().AddDynamicClientObjects().Build()

			multiclusterSvcController := &MulticlusterServiceController{
				logger:                       logger,
				configGenerator:              nil,
				serviceEnqueuer:              NewSingleQueueEnqueuer(serviceQueue),
				clusterManager:               clusterManager,
				resourceManager:              resources_test.NewFakeResourceManager(),
				metricsRegistry:              registry,
				multiClusterReconcileTracker: NewMultiClusterReconcileTracker(logger),
			}

			clientMulti.RunAndWait(ctx.Done(), true, []kubeinformers.GenericInformer{})

			err := multiclusterSvcController.reconcile(tc.item)
			assert.NoError(t, err)

			logger.Warnf("Test: %s, %v", commonmetrics.ServicesReconciledTotal, tc.expectedMetricLabel)
			assertCounterWithLabels(t, registry, commonmetrics.ServicesReconciledTotal, tc.expectedMetricLabel, 1)
		})
	}
}

type fakeReconcileOptimizer struct{}

func (o *fakeReconcileOptimizer) ShouldReconcile(_ *zap.SugaredLogger, _ interface{}, _ string, _ string) bool {
	return true
}

func (o *fakeReconcileOptimizer) ShouldEnqueueServiceForRelatedObjects(_ *zap.SugaredLogger, _ controllers_api.Event, _ interface{}, _ interface{}, clusterName string, kind string) bool {
	return true
}
