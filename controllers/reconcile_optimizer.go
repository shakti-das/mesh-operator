package controllers

import (
	"time"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/tools/cache"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	meshiov1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/generated/informers/externalversions/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/reconcilemetadata"
	"go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
)

// RunReconcileOptimizer init dependencies which are required during optimizer computation like msm informer
func RunReconcileOptimizer(logger *zap.SugaredLogger, primaryKubeClient kube.Client, stopCh <-chan struct{}, metricsRegistry *prometheus.Registry) bool {
	if !features.EnableSkipReconcileOnRestart {
		return true
	}
	msmInformer := primaryKubeClient.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()
	logger.Infof("Starting msm informer..")
	go wait.Until(func() { msmInformer.Informer().Run(stopCh) }, time.Second, stopCh)
	return cache.WaitForNamedCacheSync("msmInformer", stopCh, msmInformer.Informer().HasSynced)
}

// ReconcileOptimizer defines the interface for deciding whether to skip reconciliation based on specific condition
type ReconcileOptimizer interface {
	ShouldReconcile(logger *zap.SugaredLogger, object interface{}, clusterName string, kind string) bool
	ShouldEnqueueServiceForRelatedObjects(logger *zap.SugaredLogger, event controllers_api.Event, svc interface{}, object interface{}, clusterName string, kind string) bool
}

// ServiceReconcileOptimizer skips reconciliation on restart for resources like service, SE if there is no change in template and associated object hash
type ServiceReconcileOptimizer struct {
	msmInformer          meshiov1alpha1.MeshServiceMetadataInformer
	reconcileHashManager reconcilemetadata.ReconcileHashManager
}

func NewServiceReconcileOptimizer(msmInformer meshiov1alpha1.MeshServiceMetadataInformer, reconcileHashManager reconcilemetadata.ReconcileHashManager) *ServiceReconcileOptimizer {
	return &ServiceReconcileOptimizer{
		msmInformer:          msmInformer,
		reconcileHashManager: reconcileHashManager,
	}
}

func (ro *ServiceReconcileOptimizer) ShouldReconcile(logger *zap.SugaredLogger, object interface{}, clusterName string, kind string) bool {

	if !features.EnableSkipReconcileOnRestart {
		return true
	}
	metaObject := common.GetMetaObject(object)
	msmObject, msmError := ro.getMSMObject(metaObject, kind)
	if msmError != nil {
		if k8serrors.IsNotFound(msmError) {
			logger.Debugf("MSM not found for: %s/%s/%s", kind, metaObject.GetNamespace(), metaObject.GetName())
			return true
		}
		logger.Errorf("failed to get msm for %s/%s/%s: %o", kind, metaObject.GetNamespace(), metaObject.GetName(), msmError)
		return true
	}

	if features.MopConfigTemplatesHash != msmObject.Status.ReconciledTemplateHash {
		return true
	}

	return ro.compareHash(metaObject, msmObject, clusterName, kind)
}

// ShouldEnqueueServiceForRelatedObjects is used to check if there are any changes in sts/rollout/deployment spec
func (ro *ServiceReconcileOptimizer) ShouldEnqueueServiceForRelatedObjects(logger *zap.SugaredLogger, event controllers_api.Event, svc interface{}, object interface{}, clusterName string, kind string) bool {

	if !features.SkipSvcEnqueueForRelatedObjects || event == controllers_api.EventDelete {
		// Don't optimize if feature is disabled or related object is being deleted.
		return true
	}
	svcKind := constants.ServiceKind.Kind

	// check for change in related sts/rollout/deployment
	metaObject := common.GetMetaObject(object)
	svcMeta := common.GetMetaObject(svc)
	msmObject, msmError := ro.getMSMObject(svcMeta, svcKind)
	if msmError != nil {
		if k8serrors.IsNotFound(msmError) {
			logger.Debugf("MSM not found for: %s/%s/%s", svcKind, metaObject.GetNamespace(), metaObject.GetName())
			return true
		}
		logger.Errorf("failed to get msm for %s/%s/%s: %o", svcKind, metaObject.GetNamespace(), metaObject.GetName(), msmError)
		return true
	}
	return ro.compareHash(metaObject, msmObject, clusterName, kind)

}

func (ro *ServiceReconcileOptimizer) getMSMObject(meta metav1.Object, kind string) (*v1alpha1.MeshServiceMetadata, error) {
	msmRecordName := common.GetRecordNameSuffixedByKind(meta.GetName(), kind)
	return ro.msmInformer.Lister().MeshServiceMetadatas(meta.GetNamespace()).Get(msmRecordName)
}

func (ro *ServiceReconcileOptimizer) compareHash(metaObject metav1.Object, msmObject *v1alpha1.MeshServiceMetadata, clusterName string, kind string) bool {
	objectHashKey := ro.reconcileHashManager.ComputeHashKey(clusterName, kind, metaObject.GetNamespace(), metaObject.GetName())
	currentObjectHash := ro.reconcileHashManager.ComputeObjectHash(metaObject, kind)
	lastReconciledObjectHash := msmObject.Status.ReconciledObjectHash[objectHashKey]
	return currentObjectHash != lastReconciledObjectHash
}
