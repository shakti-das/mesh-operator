package controllers

import (
	"reflect"

	meshv1alpha1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"
)

// tspEventHandler handles TSP events
type tspEventHandler struct {
	clusterName string
	queue       controllers_api.QueueAccessor
}

func (h *tspEventHandler) OnAdd(obj interface{}, _ bool) {
	enqueue(h.queue.GetQueue(), h.clusterName, obj, controllers_api.EventAdd)
}

func (h *tspEventHandler) OnUpdate(old, new interface{}) {
	oldTsp, ok1 := old.(*meshv1alpha1.TrafficShardingPolicy)
	newTsp, ok2 := new.(*meshv1alpha1.TrafficShardingPolicy)

	// If we can't cast, enqueue anyway to be safe
	if !ok1 || !ok2 {
		enqueue(h.queue.GetQueue(), h.clusterName, new, controllers_api.EventUpdate)
		return
	}

	// Skip reconciliation if only status changed
	if oldTsp.Generation == newTsp.Generation &&
		reflect.DeepEqual(oldTsp.Labels, newTsp.Labels) &&
		reflect.DeepEqual(oldTsp.Annotations, newTsp.Annotations) {
		// Only status changed, skip re-enqueueing
		return
	}

	enqueue(h.queue.GetQueue(), h.clusterName, new, controllers_api.EventUpdate)
}

func (h *tspEventHandler) OnDelete(obj interface{}) {
	enqueue(h.queue.GetQueue(), h.clusterName, obj, controllers_api.EventDelete)
}
