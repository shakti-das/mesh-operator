package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"
	error2 "github.com/istio-ecosystem/mesh-operator/pkg/errors"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	metricstesting "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics/testing"
	kubetest "github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coretesting "k8s.io/client-go/testing"
	k8stesting "k8s.io/client-go/testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"go.uber.org/zap/zaptest"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	"github.com/stretchr/testify/assert"
)

var (
	goodObject = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.ServiceResource.GroupVersion().String(),
			"kind":       constants.ServiceKind.Kind,
			"metadata": map[string]interface{}{
				"name":      "good-object",
				"namespace": "test-namespace",
			},
		},
	}

	emptyUidObject = goodObject.DeepCopy()

	routingConfigEnabledAnnotationAsStr = common.GetStringForAttribute(constants.RoutingConfigEnabledAnnotation)
)

func TestEnqueue(t *testing.T) {
	goodObject.SetUID("uid-1")
	testClusterName := "test-cluster"

	goodObjectAddQueueItem := NewQueueItem(
		testClusterName,
		"test-namespace/good-object",
		"uid-1",
		controllers_api.EventAdd)

	goodObjectUpdateQueueItem := NewQueueItem(
		testClusterName,
		"test-namespace/good-object",
		"uid-1",
		controllers_api.EventUpdate)
	emtypUidQueueItem := NewQueueItem(
		testClusterName,
		serviceKey,
		"",
		controllers_api.EventAdd,
	)
	testCases := []struct {
		name              string
		obj               interface{}
		event             controllers_api.Event
		expectedQueueItem *QueueItem
	}{
		{
			name:              "GoodObjectAdd",
			obj:               goodObject,
			event:             controllers_api.EventAdd,
			expectedQueueItem: &goodObjectAddQueueItem,
		},
		{
			name:              "GoodObjectUpdate",
			obj:               goodObject,
			event:             controllers_api.EventUpdate,
			expectedQueueItem: &goodObjectUpdateQueueItem,
		},
		{
			name: "PassEmptyObject",
		},
		{
			name:              "PassEmptyUid",
			obj:               emptyUidObject,
			expectedQueueItem: &emtypUidQueueItem,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := &TestableQueue{}

			enqueue(queue, testClusterName, tc.obj, tc.event)

			if tc.expectedQueueItem != nil {
				assert.Equal(t, 1, len(queue.recordedItems))
				assert.Equal(t, *tc.expectedQueueItem, queue.recordedItems[0])
			} else {
				assert.Equal(t, 0, len(queue.recordedItems))
			}
		})
	}
}

func TestNoProcessingOnShutdown(t *testing.T) {
	queue := &TestableQueue{shutdown: true}

	result := processNextWorkItem(zaptest.NewLogger(t).Sugar(), queue, nil, nil)

	assert.False(t, result)
}

func TestNonQueueItemOnTheQueue(t *testing.T) {
	queue := &TestableQueue{
		forgottenItems: []interface{}{},
		doneItems:      []interface{}{},
		objectOnQueue:  "WrongObject",
	}

	result := processNextWorkItem(zaptest.NewLogger(t).Sugar(), queue, nil, nil)

	assert.True(t, result)
	assert.Equal(t, 1, len(queue.forgottenItems))
	assert.Equal(t, "WrongObject", queue.forgottenItems[0])
	assert.Equal(t, 1, len(queue.doneItems))
	assert.Equal(t, "WrongObject", queue.doneItems[0])
}

func TestReconcileError(t *testing.T) {
	tests := []struct {
		name                  string
		reconcileError        error
		expectErrorLog        bool
		expectWarningLog      bool
		expectOnRetryExecuted bool
	}{
		{
			name:                  "regular error should be logged as error",
			reconcileError:        fmt.Errorf("test-error"),
			expectErrorLog:        true,
			expectWarningLog:      false,
			expectOnRetryExecuted: true,
		},
		{
			name:                  "UserConfigError should be logged as warning",
			reconcileError:        &error2.UserConfigError{Message: "user-config-error"},
			expectErrorLog:        false,
			expectWarningLog:      true,
			expectOnRetryExecuted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queueItem := NewQueueItem(
				"test-cluster",
				"test-namespace/good-object",
				"",
				controllers_api.EventAdd,
			)
			queue := &TestableQueue{
				forgottenItems: []interface{}{},
				doneItems:      []interface{}{},
				objectOnQueue:  queueItem,
			}
			reconciler := func(item QueueItem) error {
				return tt.reconcileError
			}
			onRetry := &testableOnRetryHandler{}

			observed, observedLogs := observer.New(zapcore.InfoLevel)
			testLogger := zap.New(zapcore.NewTee(zaptest.NewLogger(t).Core(), observed))

			result := processNextWorkItem(testLogger.Sugar(), queue, reconciler, onRetry.onRetry)

			assert.True(t, result)
			assert.Equal(t, 1, len(queue.doneItems))
			assert.Equal(t, queueItem, queue.doneItems[0])
			assert.Equal(t, 1, len(queue.recordedItems))
			assert.Equal(t, queueItem, queue.recordedItems[0])
			assert.Equal(t, tt.expectOnRetryExecuted, onRetry.onRetryExecuted)

			if tt.expectErrorLog {
				assert.Equal(t, 1, observedLogs.Len())
				entry := observedLogs.All()[0]
				assert.Equal(t, zapcore.ErrorLevel, entry.Level)
				assert.Equal(t, "reconcile error: error syncing key. Will requeue: test-namespace/good-object cause: test-error, \"test-error\"", entry.Message)
			}

			if tt.expectWarningLog {
				assert.Equal(t, 1, observedLogs.Len())
				entry := observedLogs.All()[0]
				assert.Equal(t, zapcore.WarnLevel, entry.Level)
				assert.Equal(t, "non critical error during reconcile: error syncing key. Will requeue: test-namespace/good-object cause: user-config-error", entry.Message)
			}
		})
	}
}

func TestReconcileSuccess(t *testing.T) {
	queueItem := NewQueueItem(
		"test-cluster",
		"test-namespace/good-object",
		"",
		controllers_api.EventAdd,
	)
	queue := &TestableQueue{
		forgottenItems: []interface{}{},
		doneItems:      []interface{}{},
		objectOnQueue:  queueItem,
	}
	reconciler := func(item QueueItem) error {
		return nil
	}
	onRetry := &testableOnRetryHandler{}

	result := processNextWorkItem(zaptest.NewLogger(t).Sugar(), queue, reconciler, onRetry.onRetry)

	assert.True(t, result)
	assert.Equal(t, 1, len(queue.forgottenItems))
	assert.Equal(t, queueItem, queue.forgottenItems[0])
	assert.Equal(t, 1, len(queue.doneItems))
	assert.Equal(t, queueItem, queue.doneItems[0])
	assert.False(t, onRetry.onRetryExecuted)
}

// This test makes sure that NewQueueItem returns comparable queue-items that can be then de-duped on the workqueue
func TestNewQueueItem(t *testing.T) {
	item1 := NewQueueItem("test-cluster", "test-key", "uid-1", controllers_api.EventAdd)
	item2 := NewQueueItem("test-cluster", "test-key", "uid-1", controllers_api.EventAdd)

	assert.True(t, item1 == item2)
}

func TestQueueBehaviourWithAndWithoutPriorityQueue(t *testing.T) {
	item1 := NewQueueItem("test-cluster", "test-namespace/test-key", "uid-1", controllers_api.EventAdd)
	item2 := NewQueueItem("test-cluster", "priority-namespace/test-key", "uid-2", controllers_api.EventAdd)
	object1 := goodObject.DeepCopy()
	object2 := goodObject.DeepCopy()
	object1.SetName("test-key")
	object1.SetUID("uid-1")
	object1.SetNamespace("test-namespace")
	object2.SetName("test-key")
	object2.SetUID("uid-2")
	object2.SetNamespace("priority-namespace")

	testCases := []struct {
		name                              string
		usePriorityQueue                  bool
		enqueueFirstItemWithRateLimiting  bool
		enqueueSecondItemWithRateLimiting bool
		expectedQueueItem                 *QueueItem
		description                       string
	}{
		{
			name:                              "Both elements with rate limiting with priority queue",
			usePriorityQueue:                  true,
			enqueueFirstItemWithRateLimiting:  true,
			enqueueSecondItemWithRateLimiting: true,
			expectedQueueItem:                 &item1,
			description:                       "When all elements are rate-limited, the one inserted first will be processed first, regardless of its priority.",
		},
		{
			name:                              "Both elements without rate limiting with priority queue",
			usePriorityQueue:                  true,
			enqueueFirstItemWithRateLimiting:  false,
			enqueueSecondItemWithRateLimiting: false,
			expectedQueueItem:                 &item1,
			description:                       "Without rate limiting, items with higher priority are processed first.",
		},
		{
			name:                              "Only First element with rate limiting with priority queue",
			usePriorityQueue:                  true,
			enqueueFirstItemWithRateLimiting:  true,
			enqueueSecondItemWithRateLimiting: false,
			expectedQueueItem:                 &item2,
			description:                       "When only a few items are inserted with rate limiting, the non–rate-limited items are processed first, regardless of priority.",
		},
		{
			name:                              "Only Second element with rate limiting with priority queue",
			usePriorityQueue:                  true,
			enqueueFirstItemWithRateLimiting:  false,
			enqueueSecondItemWithRateLimiting: true,
			expectedQueueItem:                 &item1,
			description:                       "When only a few items are inserted with rate limiting, the non–rate-limited items are processed first, regardless of priority.",
		},
		{
			name:                              "Both elements with rate limiting without priority queue",
			usePriorityQueue:                  false,
			enqueueFirstItemWithRateLimiting:  true,
			enqueueSecondItemWithRateLimiting: true,
			expectedQueueItem:                 &item1,
			description:                       "Order of the items is just based on when the items are ready",
		},
	}
	labels := map[string]string{
		"name": "test-queue",
	}
	registry := prometheus.NewRegistry()
	metrics.NewMetricsProvider(registry)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.UsePriorityQueue = tc.usePriorityQueue
			defer func() { features.UsePriorityQueue = false }()
			queue := createWorkQueue("test-queue")
			if tc.enqueueFirstItemWithRateLimiting {
				enqueue(queue, "test-cluster", object1, controllers_api.EventAdd)
			} else {
				enqueueNow(queue, "test-cluster", object1, controllers_api.EventAdd)
			}
			if tc.enqueueSecondItemWithRateLimiting {
				enqueue(queue, "test-cluster", object2, controllers_api.EventAdd)
			} else {
				enqueueNow(queue, "test-cluster", object2, controllers_api.EventAdd)
			}
			// sleeping 1 second to ensure all the items are ready before calling get
			time.Sleep(time.Second)

			// Check that both items are in the queue before processing
			assertGaugeWithLabels(t, registry, "queue_depth", labels, 2)
			actualValue, _ := queue.Get()
			assert.Equal(t, *tc.expectedQueueItem, actualValue)
			assertGaugeWithLabels(t, registry, "queue_depth", labels, 1)
			queue.Done(actualValue)

			// Drain all remaining items from the queue
			drainQueue(queue)
			queue.ShutDown()
		})
	}
}

func TestIncrementCounterForQueueItem(t *testing.T) {
	testCases := []struct {
		name           string
		queueItem      QueueItem
		cluster        string
		expectedLabels map[string]string
	}{
		{
			name:      "AddEvent",
			queueItem: NewQueueItem("test-cluster", "some-namespace/mop1", "uid-1", controllers_api.EventAdd),
			cluster:   "cluster-1",
			expectedLabels: map[string]string{
				metrics.ClusterLabel:      "cluster-1",
				metrics.NamespaceLabel:    "some-namespace",
				metrics.ResourceNameLabel: metrics.DeprecatedLabel,
				metrics.EventTypeLabel:    "add",
			},
		},
		{
			name:      "UpdateEvent",
			queueItem: NewQueueItem("test-cluster", "some-namespace/mop1", "uid-1", controllers_api.EventUpdate),
			cluster:   "cluster-1",
			expectedLabels: map[string]string{
				metrics.ClusterLabel:      "cluster-1",
				metrics.NamespaceLabel:    "some-namespace",
				metrics.ResourceNameLabel: metrics.DeprecatedLabel,
				metrics.EventTypeLabel:    "update",
			},
		},
		{
			name:      "DeleteEvent",
			queueItem: NewQueueItem("test-cluster", "some-namespace/mop1", "uid-1", controllers_api.EventDelete),
			cluster:   "cluster-1",
			expectedLabels: map[string]string{
				metrics.ClusterLabel:      "cluster-1",
				metrics.NamespaceLabel:    "some-namespace",
				metrics.ResourceNameLabel: metrics.DeprecatedLabel,
				metrics.EventTypeLabel:    "delete",
			},
		},
		{
			name:      "EmptyNamespace",
			queueItem: NewQueueItem("test-cluster", "bad-key", "uid-1", controllers_api.EventAdd),
			cluster:   "cluster-1",
			expectedLabels: map[string]string{
				metrics.ClusterLabel:      "cluster-1",
				metrics.NamespaceLabel:    "",
				metrics.ResourceNameLabel: metrics.DeprecatedLabel,
				metrics.EventTypeLabel:    "add",
			},
		},
		{
			name:      "InvalidKey",
			queueItem: NewQueueItem("test-cluster", "very/bad/key", "uid-1", controllers_api.EventAdd),
			cluster:   "cluster-1",
			expectedLabels: map[string]string{
				metrics.ClusterLabel:      "cluster-1",
				metrics.NamespaceLabel:    "unknown",
				metrics.ResourceNameLabel: metrics.DeprecatedLabel,
				metrics.EventTypeLabel:    "add",
			},
		},
	}

	var metricName = "test_counter"
	var registry = prometheus.NewRegistry()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			IncrementCounterForQueueItem(registry, "test_counter", tc.cluster, tc.queueItem)
			assertCounterWithLabels(t, registry, metricName, tc.expectedLabels, 1)

			IncrementCounterForQueueItem(registry, "test_counter", tc.cluster, tc.queueItem)
			assertCounterWithLabels(t, registry, metricName, tc.expectedLabels, 2)
		})
	}
}

// TestableQueue
type TestableQueue struct {
	workqueue.RateLimitingInterface

	recordedItems  []interface{}
	forgottenItems []interface{}
	doneItems      []interface{}

	objectOnQueue interface{}
	shutdown      bool

	// Counters for tracking enqueue operations
	enqueueNowCount int // Counter for items enqueued with Add (enqueueNow)
	enqueueCount    int // Counter for items enqueued with AddRateLimited (regular enqueue)
}

func (q *TestableQueue) AddRateLimited(item interface{}) {
	q.recordedItems = append(q.recordedItems, item)
	q.enqueueCount++ // Increment counter for regular enqueue operations
}

func (q *TestableQueue) Get() (item interface{}, shutdown bool) {
	return q.objectOnQueue, q.shutdown
}

func (q *TestableQueue) Forget(item interface{}) {
	q.forgottenItems = append(q.forgottenItems, item)
}

func (q *TestableQueue) Done(item interface{}) {
	q.doneItems = append(q.doneItems, item)
}

func (q *TestableQueue) Add(item interface{}) {
	q.recordedItems = append(q.recordedItems, item)
	q.enqueueNowCount++ // Increment counter for enqueueNow operations
}

// TestableEventRecorder
type TestableEventRecorder struct {
	record.EventRecorder

	recordedObject runtime.Object
	eventtype      string
	reason         string
	message        string
}

func (r *TestableEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	r.recordedObject = object
	r.eventtype = eventtype
	r.reason = reason
	r.message = message
}

func assertCounterWithLabels(t *testing.T, registry *prometheus.Registry, metricName string, labels map[string]string, expectedValue float64) {
	counter := commonmetrics.GetOrRegisterCounterWithLabels(metricName, labels, registry)
	assert.NotNil(t, counter)

	serializedMetric := &dto.Metric{}
	_ = counter.Write(serializedMetric)

	assert.Equal(t, expectedValue, serializedMetric.GetCounter().GetValue())
}

func assertGaugeWithLabels(t *testing.T, registry *prometheus.Registry, metricName string, labels map[string]string, expectedValue float64) {
	gauge := commonmetrics.GetOrRegisterGaugeWithLabels(metricName, labels, registry)
	assert.NotNil(t, gauge)

	serializedMetric := &dto.Metric{}
	_ = gauge.Write(serializedMetric)

	assert.Equal(t, expectedValue, serializedMetric.GetGauge().GetValue())
}

func drainQueue(queue workqueue.RateLimitingInterface) {
	for queue.Len() > 0 {
		item, shutdown := queue.Get()
		if shutdown {
			break
		}
		queue.Done(item)
	}
}

func TestNamespaceInPrimaryCluster(t *testing.T) {
	var (
		namespace        = "test-namespace"
		remoteNs         = kubetest.CreateNamespace(namespace, map[string]string{"operator.mesh.io/enabled": "true"})
		createdNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		service    = kubetest.CreateService(namespace, "test-service")
		objectMeta = &metav1.ObjectMeta{
			Name:      "test-service",
			Namespace: namespace,
		}
		logger        = zaptest.NewLogger(t).Sugar()
		registry      = prometheus.NewRegistry()
		metricsLabels = metrics.GetMetricsLabels(service.Kind, objectMeta)
	)

	testCases := []struct {
		name                      string
		isNsInitiallyInPrimary    bool
		primaryNsErrorVerb        string
		expectedErr               string
		expectedMetric            string
		expectedKubeClientActions []coretesting.Action
	}{
		{
			name:                   "NamespaceAlreadyExistInPrimaryCluster",
			isNsInitiallyInPrimary: true,
		},
		{
			name:           "CreateRemoteNamespaceInPrimaryCluster",
			expectedMetric: metrics.PrimaryNamespaceCreated,
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, "", createdNamespace),
			},
		},
		{
			name: "PrimaryNamespaceCreationFails",
			expectedErr: "failed while creating the namespace test-namespace in primary cluster for Service:test-service." +
				" Error: Internal error occurred: namespace create request failed",
			primaryNsErrorVerb: "create",
			expectedMetric:     metrics.PrimaryNamespaceCreationFailed,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var objectsInPrimaryCluster []runtime.Object

			if tc.isNsInitiallyInPrimary {
				objectsInPrimaryCluster = []runtime.Object{remoteNs}
			} else {
				objectsInPrimaryCluster = []runtime.Object{}
			}

			primaryClient := kubetest.NewKubeClientBuilder().AddK8sObjects(objectsInPrimaryCluster...).
				AddDynamicClientObjects(objectsInPrimaryCluster...).Build()

			primaryFakeClient := (primaryClient).(*kubetest.FakeClient)

			stopCh := make(chan struct{})
			defer close(stopCh)

			namespaceInformer := primaryFakeClient.KubeInformerFactory().Core().V1().Namespaces()

			go namespaceInformer.Informer().Run(stopCh)
			cache.WaitForCacheSync(stopCh, namespaceInformer.Informer().HasSynced)

			if tc.primaryNsErrorVerb != "" {
				primaryFakeClient.KubeClient.PrependReactor(tc.primaryNsErrorVerb,
					constants.NamespaceResource.Resource, func(action k8stesting.Action) (
						handled bool, ret runtime.Object, err error) {
						return true, nil, k8serrors.NewInternalError(
							fmt.Errorf("namespace %s request failed", tc.primaryNsErrorVerb))
					})
			}

			err := CreatePrimaryNamespaceIfMissing(logger, primaryClient, service, registry)

			if tc.expectedErr == "" {
				assert.Nil(t, err)
				primaryNs, nsErr := primaryClient.Kube().CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
				assert.Nil(t, nsErr)
				assert.Equal(t, namespace, primaryNs.GetName())
			} else {
				assert.Equal(t, tc.expectedErr, err.Error())
			}

			// validate K8s actions
			filteredKubeClientActions := filterInformerActions(primaryFakeClient.KubeClient.Actions())
			for idx, expectedAction := range tc.expectedKubeClientActions {
				checkAction(t, expectedAction, filteredKubeClientActions[idx])
			}

			if tc.expectedMetric != "" {
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, tc.expectedMetric,
					metricsLabels, 1)
			}
		})
	}
}

func TestIsOperatorDisabled(t *testing.T) {
	testCases := []struct {
		name string
		obj  interface{}

		expectedResult bool
	}{
		{
			name: "Disabled by legacy annotation",
			obj: kubetest.NewServiceBuilder("svc", "ns").
				SetAnnotations(map[string]string{routingConfigEnabledAnnotationAsStr: "false"}).
				GetServiceAsUnstructuredObject(),
			expectedResult: true,
		},
		{
			name: "Disabled by annotation",
			obj: kubetest.NewServiceBuilder("svc", "ns").
				SetAnnotations(map[string]string{constants.MeshOperatorEnabled: "false"}).
				GetServiceAsUnstructuredObject(),
			expectedResult: true,
		},
		{
			name: "No annotations",
			obj: kubetest.NewServiceBuilder("svc", "ns").
				GetServiceAsUnstructuredObject(),
			expectedResult: false,
		},
		{
			name: "Legacy takes precedense",
			obj: kubetest.NewServiceBuilder("svc", "ns").
				SetAnnotations(
					map[string]string{
						routingConfigEnabledAnnotationAsStr: "false",
						constants.MeshOperatorEnabled:       "true",
					}).
				GetServiceAsUnstructuredObject(),
			expectedResult: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := common.IsOperatorDisabled(tc.obj)

			assert.Equal(t, tc.expectedResult, actual)
		})
	}
}

func TestOnControllerRetryHandler(t *testing.T) {
	var metricName = "test_counter"
	registry := prometheus.NewRegistry()

	queueItem := NewQueueItem(
		"test-cluster-1",
		"test-namespace/good-object",
		"uid-1",
		controllers_api.EventAdd)

	onRetry := OnControllerRetryHandler(metricName, registry)
	onRetry(queueItem)

	labels := map[string]string{
		commonmetrics.ClusterLabel:      "test-cluster-1",
		commonmetrics.NamespaceLabel:    "test-namespace",
		commonmetrics.ResourceNameLabel: commonmetrics.DeprecatedLabel,
		commonmetrics.EventTypeLabel:    "add",
	}

	assertCounterWithLabels(t, registry, metricName, labels, 1)
}

type testableOnRetryHandler struct {
	onRetryExecuted bool
}

func (h *testableOnRetryHandler) onRetry(_ QueueItem) {
	h.onRetryExecuted = true
}
