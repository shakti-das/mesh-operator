package controllers

import (
	"context"
	goerrors "errors"
	"fmt"
	"reflect"

	error2 "github.com/istio-ecosystem/mesh-operator/pkg/errors"

	mopv1alpha1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type QueueItem struct {
	key     string
	uid     types.UID
	event   controllers_api.Event
	cluster string
}

func (c *QueueItem) GetEventName() string {
	if c.event == controllers_api.EventAdd {
		return "add"
	}
	if c.event == controllers_api.EventUpdate {
		return "update"
	}
	if c.event == controllers_api.EventDelete {
		return "delete"
	}

	return controllers_api.UnknownEvent
}

func NewQueueItem(cluster, key string, uid types.UID, event controllers_api.Event) QueueItem {
	return QueueItem{
		key:     key,
		event:   event,
		uid:     uid,
		cluster: cluster,
	}
}

type Controller interface {
	Run(workerThreads int, stopCh <-chan struct{}) error
}

type Reconciler func(item QueueItem) error
type OnReconcileRetry func(item QueueItem)

type objectReader func(namespace string, name string) (interface{}, error)

func RunWorker(logger *zap.SugaredLogger,
	workqueue workqueue.RateLimitingInterface,
	reconciler Reconciler,
	onRetry OnReconcileRetry) {
	for processNextWorkItem(logger, workqueue, reconciler, onRetry) {
	}
}

func enqueue(workQueue workqueue.RateLimitingInterface, clusterOfEvent string, obj interface{}, event controllers_api.Event) {
	if key, uid := buildQueueKeyAndUid(obj); key != "" {
		workQueue.AddRateLimited(NewQueueItem(clusterOfEvent, key, uid, event))
	}
}

func enqueueNow(workQueue workqueue.RateLimitingInterface, clusterOfEvent string, obj interface{}, event controllers_api.Event) {
	if key, uid := buildQueueKeyAndUid(obj); key != "" {
		workQueue.Add(NewQueueItem(clusterOfEvent, key, uid, event))
	}
}

func buildQueueKeyAndUid(obj interface{}) (string, types.UID) {
	if obj == nil {
		return "", ""
	}
	object := verifyAndRecoverIfRequired(obj)
	if key, err := cache.MetaNamespaceKeyFunc(object); err != nil {
		utilruntime.HandleError(err)
		return "", ""
	} else {
		return key, object.GetUID()
	}
}

// verifyAndRecoverIfRequired verifies whether the event's object is a tombstone (cache.DeletedFinalStateUnknown).
// Returns the actual event's object or nil if recovery failed.
func verifyAndRecoverIfRequired(obj interface{}) metav1.Object {
	object, ok := obj.(metav1.Object)
	if !ok {
		var tombstone cache.DeletedFinalStateUnknown
		var ok bool
		if tombstone, ok = obj.(cache.DeletedFinalStateUnknown); !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type: %v", reflect.TypeOf(obj)))
			return nil
		}

		if object, ok = tombstone.Obj.(metav1.Object); !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type: %v", reflect.TypeOf(tombstone.Obj)))
			return nil
		}
	}
	return object
}

// extractOrRestoreObject - reads object from the provided object reader
func extractOrRestoreObject(item QueueItem, reader objectReader) (string, string, interface{}, error) {
	// Convert the namespace/name string into a distinct namespace and name.
	namespace, name, err := cache.SplitMetaNamespaceKey(item.key)
	if err != nil {
		return "", "", nil, fmt.Errorf("invalid resource key: %s", item.key)
	}

	object, readerErr := reader(namespace, name)
	if readerErr != nil {
		// The resource may no longer exist, in which case we stop processing.
		if errors.IsNotFound(readerErr) {
			return namespace, name, nil, nil
		}
		utilruntime.HandleError(readerErr)
		return namespace, name, object, readerErr
	}

	return namespace, name, object, nil
}

// processNextWorkItem will read a single work item off the work queue and
// attempt to process it, by calling the reconcile.
func processNextWorkItem(
	logger *zap.SugaredLogger,
	workqueue workqueue.RateLimitingInterface,
	reconciler Reconciler,
	onRetry OnReconcileRetry) bool {
	obj, shutdown := workqueue.Get()

	if shutdown {
		logger.Warn("shutting down controller")
		return false
	}

	// We wrap this block in a func so we can defer c.serviceWorkQueue.Done.
	err := func(obj interface{}) error2.ControllerReconcileGenericError {
		// We call Done here so the work queue knows we have finished processing this item.
		// We also must remember to call Forget if we do not want this work item being re-queued.
		// For example, we do not call Forget if a transient error occurs, instead the item is
		// put back on the work queue and attempted again after a back-off period.
		defer workqueue.Done(obj)

		item, ok := obj.(QueueItem)
		// We expect QueueItem type to come off the work queue. These contain a key of the form namespace/name.
		// We use key instead of the object directly because the items in the informer cache may actually be
		// more up to date than when the item was initially put onto the work queue.
		if !ok {
			msg := fmt.Sprintf("expected *item in work queue but got %#v", obj)
			logger.Warn(msg)
			// As the item in the work queue is actually invalid, we call Forget here
			// else we'd go into a loop of attempting to process a work item that is invalid.
			workqueue.Forget(obj)
			utilruntime.HandleError(goerrors.New(msg))
			return nil
		}

		// Run the reconcile, passing it the namespace/name, event and metadata of the resource to be synced.
		if err := reconciler(item); err != nil {
			// Put the item back on the work queue to handle any transient errors
			workqueue.AddRateLimited(item)
			onRetry(item)
			return &error2.ControllerReconcileGenericErrorImpl{
				Message: fmt.Sprintf("error syncing key. Will requeue: %s", item.key),
				Cause:   err,
			}
		}
		// Finally, if no error occurs we Forget this item so it does not get queued again until another change happens.
		workqueue.Forget(obj)
		logger.Infof("successfully synced '%s'", item.key)
		return nil
	}(obj)

	if err != nil {
		if err.ShouldReportAsError() {
			logger.Errorf("reconcile error: %s, %q", err.Error(), err.GetCause())
		} else {
			// No need to report extensive stack and report as an error
			logger.Warnf("non critical error during reconcile: %s", err.Error())
		}
		return true
	}

	return true
}

func IncrementCounterForQueueItem(registry *prometheus.Registry, counterName string, cluster string, item QueueItem) {
	namespace, _, err := cache.SplitMetaNamespaceKey(item.key)
	if err != nil {
		namespace = "unknown"
	}
	// Counter metrics reflect accumulated state over time and should not be object specific.
	// For this reason, lets hardcode the object name value to a constant and gradually deprecate this metric tag on counters.
	objectName := commonmetrics.DeprecatedLabel

	IncrementCounterForObject(registry, counterName, cluster, namespace, objectName, item.GetEventName())
}

func IncrementCounterForObject(registry *prometheus.Registry, counterName string, cluster, namespace, name, event string) {
	labels := map[string]string{
		commonmetrics.ClusterLabel:      cluster,
		commonmetrics.NamespaceLabel:    namespace,
		commonmetrics.ResourceNameLabel: name,
		commonmetrics.EventTypeLabel:    event,
	}
	commonmetrics.GetOrRegisterCounterWithLabels(counterName, labels, registry).Inc()
}

// CreatePrimaryNamespaceIfMissing create namespace (with the same name as remote namespace) in primary cluster if missing for events coming from remote cluster
func CreatePrimaryNamespaceIfMissing(logger *zap.SugaredLogger, primaryClient kube.Client, object runtime.Object, metricsRegistry *prometheus.Registry) error {
	metaObject := common.GetMetaObject(object)
	objType := object.GetObjectKind().GroupVersionKind().Kind
	remoteNamespace := metaObject.GetNamespace()
	logger = logger.With("type", objType, "namespace", metaObject.GetNamespace(), "name", metaObject.GetName())

	// get namespace from primary cluster
	namespaceInformer := primaryClient.KubeInformerFactory().Core().V1().Namespaces()

	_, err := namespaceInformer.Lister().Get(remoteNamespace)
	if err == nil {
		// namespace found, nothing more to do.
		logger.Debugf("namespace %s was found in the primary cluster for the remote event.", remoteNamespace)
		return nil
	}

	// if namespace doesn't exist, create it
	if kube.IsErrorResourceNotFound(err) {
		logger.Debugf("namespace %s not found in primary cluster, creating it.", remoteNamespace)
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: remoteNamespace,
			},
		}
		_, err := primaryClient.Kube().CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
		if err != nil {
			if k8serrors.IsAlreadyExists(err) {
				logger.Debugf("namespace %s already exists in primary cluster", remoteNamespace)
				return nil
			}
			commonmetrics.IncrementMetric(commonmetrics.PrimaryNamespaceCreationFailed, objType, metaObject, metricsRegistry)
			return fmt.Errorf("failed while creating the namespace %s in primary cluster for %s:%s. Error: %w",
				remoteNamespace, objType, metaObject.GetName(), err)
		}
		logger.Debugf("namespace %s created succesfully in primary cluster", remoteNamespace)
		commonmetrics.IncrementMetric(commonmetrics.PrimaryNamespaceCreated, objType, metaObject, metricsRegistry)
	} else {
		commonmetrics.IncrementMetric(commonmetrics.PrimaryNamespaceRetrievalFailed, objType, metaObject, metricsRegistry)
		return fmt.Errorf("failed while checking for namespace %s in primary cluster. Error :%w",
			remoteNamespace, err)
	}
	return nil
}

func IsRemoteClusterEvent(client kube.Client, primaryClient kube.Client) bool {
	return client != primaryClient
}

func MsmToOwnerRef(msm *mopv1alpha1.MeshServiceMetadata) *metav1.OwnerReference {
	if msm == nil {
		return nil
	}
	return &metav1.OwnerReference{
		APIVersion: mopv1alpha1.ApiVersion,
		Kind:       mopv1alpha1.MsmKind.Kind,
		Name:       msm.GetName(),
		UID:        msm.GetUID(),
	}
}

type onControllerRetryHandler struct {
	retryCounterName string
	metrics          *prometheus.Registry
}

func OnControllerRetryHandler(retryCounterName string, metrics *prometheus.Registry) func(item QueueItem) {
	handler := &onControllerRetryHandler{
		retryCounterName: retryCounterName,
		metrics:          metrics,
	}
	return handler.OnRetry
}

func (h *onControllerRetryHandler) OnRetry(item QueueItem) {
	IncrementCounterForQueueItem(h.metrics, h.retryCounterName, item.cluster, item)
}
