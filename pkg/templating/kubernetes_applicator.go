package templating

import (
	"errors"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/ocm"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func NewK8sApplicator(
	logger *zap.SugaredLogger,
	primaryClient kube.Client) Applicator {
	return &k8sApplicator{
		logger:                logger,
		primaryClient:         primaryClient,
		retainFieldManager:    &kube.ArgoFieldManager{},
		ocmRetainFieldManager: &kube.OcmFieldManager{},
	}
}

// k8sApplicator applies provided config as objects (with create/patch operations) in k8s cluster
type k8sApplicator struct {
	logger                *zap.SugaredLogger
	renderer              TemplateRenderer
	primaryClient         kube.Client
	retainFieldManager    kube.RetainFieldManager
	ocmRetainFieldManager kube.RetainFieldManager
}

func (p *k8sApplicator) ApplyConfig(
	ctx *RenderRequestContext,
	generatedConfig *GeneratedConfig,
	ctxLogger *zap.SugaredLogger) ([]*AppliedConfigObject, error) {

	fieldManagerToUse := p.retainFieldManager
	if features.EnableOcmIntegration && ocm.IsOcmManagedObject(ctx.Object) {
		fieldManagerToUse = p.ocmRetainFieldManager
	}

	objects := generatedConfig.FlattenConfig()
	return p.patchResources(objects, ctxLogger, fieldManagerToUse)
}

func (p *k8sApplicator) patchResources(configObjects []*unstructured.Unstructured, ctxLogger *zap.SugaredLogger, fieldManager kube.RetainFieldManager) ([]*AppliedConfigObject, error) {
	var result []*AppliedConfigObject

	for _, obj := range configObjects {
		err := p.upsertK8sObject(obj, ctxLogger, fieldManager)
		r := AppliedConfigObject{Error: err, Object: obj}
		result = append(result, &r)
	}
	return result, nil
}

func (p *k8sApplicator) upsertK8sObject(obj *unstructured.Unstructured, ctxLogger *zap.SugaredLogger, fieldManager kube.RetainFieldManager) error {
	gvr, err := kube.ConvertGvkToGvr(p.primaryClient.GetClusterName(), p.primaryClient.Discovery(), obj.GroupVersionKind())
	if err != nil {
		return err
	}
	err = kube.CreateOrUpdateObject(p.primaryClient.Dynamic(), obj, gvr, fieldManager, ctxLogger)
	if err != nil {
		var updateSameError *kube.UpdateSameError
		if errors.As(err, &updateSameError) {
			ctxLogger.Debugw("received UpdateSameError",
				"namespace", obj.GetNamespace(), "name", obj.GetName(), "kind", obj.GetKind())
			return nil
		}
		ctxLogger.Errorw("error upserting object",
			"namespace", obj.GetNamespace(), "name", obj.GetName(), "kind", obj.GetKind(), "error", err)
		return err
	}
	return nil
}
