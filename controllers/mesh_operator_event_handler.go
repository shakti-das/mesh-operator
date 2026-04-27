package controllers

import (
	meshv1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"
	"go.uber.org/zap"
	"k8s.io/client-go/tools/cache"
)

func NewMeshOperatorEventHandler(mopEnqueuer controllers_api.QueueAccessor, clusterOfEvent string, logger *zap.SugaredLogger) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			mop := obj.(*meshv1alpha1.MeshOperator)
			logger.Debugw("informer event: Add", "namespace", mop.Namespace, "mopName", mop.Name)
			mopEnqueuer.Enqueue(clusterOfEvent, obj, controllers_api.EventAdd)
			logger.Infow("enqueued mop on informer event", "namespace", mop.Namespace, "mopName", mop.Name, "event", "add")
		},
		UpdateFunc: func(old, new interface{}) {
			oldMop := old.(*meshv1alpha1.MeshOperator)
			newMop := new.(*meshv1alpha1.MeshOperator)
			logger.Debugw("informer event: Update", "namespace", newMop.Namespace, "mopName", newMop.Name,
				"oldMopRV", oldMop.ResourceVersion, "newMopRV", newMop.ResourceVersion,
				"oldMopGeneration", oldMop.Generation, "newMopGeneration", newMop.Generation,
				"oldMopFinalizers", oldMop.Finalizers, "newMopFinalizers", newMop.Finalizers,
				"oldMopPhase", oldMop.Status.Phase, "newMopPhase", newMop.Status.Phase)
			if WasChangedByUser(oldMop, newMop) {
				mopEnqueuer.EnqueueNow(clusterOfEvent, newMop, controllers_api.EventUpdate)
				logger.Infow("enqueued mop. user changes detected", "namespace", newMop.Namespace, "mopName", newMop.Name)
				return
			} else if !WasChanged(oldMop, newMop) && newMop.Status.Phase != PhasePending {
				// Init and periodic resync will send update events for all known mops.
				// Two different versions of the same will always have different RVs.
				// If there is a mop resource in the cluster in Pending state, we should still try to process it.
				logger.Debugw("not enqueueing mop as it wasn't changed and the phase isn't pending", "namespace", newMop.Namespace, "mopName", newMop.Name)
				return
			} else if IsFinalStatusUpdate(oldMop, newMop) {
				logger.Debugw("not enqueueing mop for status change from pending", "namespace", newMop.Namespace, "mopName", newMop.Name)
				return
			} else if isOverlayingMop(newMop) && !IsBrandNewMop(newMop) {
				if !PhaseChanged(oldMop, newMop) {
					logger.Debugw("not enqueueing overlaying mop for status change coming from service controller", "namespace", newMop.Namespace, "mopName", newMop.Name)
					return
				} else if PhaseChangedToPending(oldMop, newMop) {
					logger.Debugw("not enqueueing overlaying mop for status change (to Pending) coming from MOP controller", "namespace", newMop.Namespace, "mopName", newMop.Name)
					return
				}
			} else if isPendingResourceTracking(newMop) {
				logger.Debugw("not enqueuing mop for pending-resources tracking event", "namespace", newMop.Namespace, "mopName", newMop.Name)
				return
			}
			mopEnqueuer.Enqueue(clusterOfEvent, newMop, controllers_api.EventUpdate)
			logger.Infow("enqueued mop on informer event", "namespace", newMop.Namespace, "mopName", newMop.Name, "event", "update")
		},
		DeleteFunc: func(obj interface{}) {
			mop := obj.(*meshv1alpha1.MeshOperator)
			logger.Debugw("informer event: Delete", "namespace", mop.Namespace, "mopName", mop.Name)
			if mop.Status.Phase == PhaseTerminating {
				mopEnqueuer.Enqueue(clusterOfEvent, obj, controllers_api.EventDelete)
				logger.Infow("enqueued mop on informer event", "namespace", mop.Namespace, "mopName", mop.Name, "event", "delete")
			} else {
				mopEnqueuer.Enqueue(clusterOfEvent, obj, controllers_api.EventUpdate)
				logger.Infow("enqueued mop on informer event", "namespace", mop.Namespace, "mopName", mop.Name, "event", "update")
			}
		},
	}
}
