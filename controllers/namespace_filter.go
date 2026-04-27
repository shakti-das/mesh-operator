package controllers

import (
	"time"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	corev1informers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/cache"
)

type mopEnabledNamespaceFilter struct {
	logger            *zap.SugaredLogger
	allNamespaces     bool
	namespaceInformer corev1informers.NamespaceInformer
}

// IsNamespaceMopEnabledForObject - determines whether MOP is enabled for the namespace where object resides.
// If operator is running in -all-namespaces mode:
//
//	all namespaces considered to be on unless mesh-operator or istio-injection are explicitly disabled
//
// In non -all-namespaces mode:
//
//	for namespace to be considered, it has to explicitly have mesh-operator and istio-injection enabled
func (f *mopEnabledNamespaceFilter) IsNamespaceMopEnabledForObject(obj interface{}) bool {
	if ns, ok := obj.(*corev1.Namespace); ok {
		return f.isMopEnabledForNamespace(ns)
	}
	metaObject := common.GetMetaObject(obj)
	nsKey := metaObject.GetNamespace()
	if nsKey == "" {
		f.logger.Warnf("Namespace key is empty for object %v", metaObject)
		return false
	}
	namespace, err := f.namespaceInformer.Lister().Get(nsKey)
	if err != nil {
		f.logger.Debugf("Namespace %s dont exist while doing GET, must have been deleted", nsKey)
		return false
	}
	return f.isMopEnabledForNamespace(namespace)
}

func (f *mopEnabledNamespaceFilter) isMopEnabledForNamespace(namespace *corev1.Namespace) bool {
	if namespace == nil {
		return false
	}
	labels := namespace.Labels
	istioEnabled := false
	mopEnabled := false
	if f.allNamespaces {
		istioEnabled = labels[constants.IstioInjectionLabel] != "disabled"
		mopEnabled = labels[constants.MeshOperatorEnabled] != "false"
	} else {
		istioEnabled = labels[constants.IstioInjectionLabel] == "enabled"
		mopEnabled = labels[constants.MeshOperatorEnabled] == "true"
	}
	return istioEnabled && mopEnabled
}

func (f *mopEnabledNamespaceFilter) Run(stopCh <-chan struct{}) bool {
	f.logger.Infof("Starting NS filter...")
	go wait.Until(func() { f.namespaceInformer.Informer().Run(stopCh) }, time.Second, stopCh)
	f.logger.Infof("Waiting for NS cache sync...")
	return cache.WaitForNamedCacheSync("namespaceFilter", stopCh, f.namespaceInformer.Informer().HasSynced)
}

func NewNamespaceFilter(logger *zap.SugaredLogger,
	allNamespaces bool,
	namespaceInformer corev1informers.NamespaceInformer) controllers_api.NamespaceFilter {
	return &mopEnabledNamespaceFilter{
		logger:            logger,
		allNamespaces:     allNamespaces,
		namespaceInformer: namespaceInformer,
	}
}
