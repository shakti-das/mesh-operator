package controllers

import (
	"go.uber.org/zap"
	"k8s.io/client-go/tools/cache"

	meshv1alpha1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"
)

// NewSidecarConfigEventHandler creates a new event handler for SidecarConfig resources.
func NewSidecarConfigEventHandler(enqueuer controllers_api.ObjectEnqueuer, clusterName string, logger *zap.SugaredLogger) cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			sc := obj.(*meshv1alpha1.SidecarConfig)
			logger.Infof("SidecarConfig added: %s/%s", sc.Namespace, sc.Name)
			enqueuer.Enqueue(clusterName, sc, controllers_api.EventAdd)
		},
		UpdateFunc: func(old, new interface{}) {
			newSC := new.(*meshv1alpha1.SidecarConfig)
			oldSC := old.(*meshv1alpha1.SidecarConfig)

			if newSC.ResourceVersion == oldSC.ResourceVersion {
				return
			}

			logger.Infof("SidecarConfig updated: %s/%s", newSC.Namespace, newSC.Name)
			enqueuer.Enqueue(clusterName, newSC, controllers_api.EventUpdate)
		},
		DeleteFunc: func(obj interface{}) {
			sc := obj.(*meshv1alpha1.SidecarConfig)
			logger.Infof("SidecarConfig deleted: %s/%s", sc.Namespace, sc.Name)
			enqueuer.Enqueue(clusterName, sc, controllers_api.EventDelete)
		},
	}
}
