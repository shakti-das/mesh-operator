package controllers

import (
	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// ---- Single Queue Enqueuer
func NewSingleQueueEnqueuer(workQueue workqueue.RateLimitingInterface) controllers_api.QueueAccessor {
	return &singleQueueEnquer{workQueue: workQueue}
}

// singleQueueEnquer - an enqueuer wrapping a single queue
type singleQueueEnquer struct {
	workQueue workqueue.RateLimitingInterface
}

func (e *singleQueueEnquer) Enqueue(clusterOfEvent string, obj interface{}, event controllers_api.Event) {
	if features.UsePriorityQueue {
		objName, err := cache.ObjectToName(obj)
		if err == nil {
			if _, exists := features.PriorityNamespaces[objName.Namespace]; exists {
				// Priority namespace objects bypass rate limiting for immediate processing
				enqueueNow(e.workQueue, clusterOfEvent, obj, event)
				return
			}
		}
	}
	enqueue(e.workQueue, clusterOfEvent, obj, event)
}

func (e *singleQueueEnquer) EnqueueNow(clusterOfEvent string, obj interface{}, event controllers_api.Event) {
	enqueueNow(e.workQueue, clusterOfEvent, obj, event)
}

func (e *singleQueueEnquer) GetQueue() workqueue.RateLimitingInterface {
	return e.workQueue
}

// ---- NoOp Enqueuer. Does nothing.
type NoOpEnqueuer struct {
}

func (e *NoOpEnqueuer) Enqueue(_ string, _ interface{}, _ controllers_api.Event) {
}

func (e *NoOpEnqueuer) EnqueueNow(_ string, _ interface{}, _ controllers_api.Event) {

}

func (e *NoOpEnqueuer) GetQueue() workqueue.RateLimitingInterface {
	return nil
}
