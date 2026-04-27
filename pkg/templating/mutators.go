package templating

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/hash"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	"go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Mutator interface {
	Mutate(ctx *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error)
}

// ManagedByMutator - Config mutator that adds a mesh.io/managed-by: mesh-operator label to all the rendered resources
type ManagedByMutator struct{}

func (m *ManagedByMutator) Mutate(_ *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	common.AddLabel(config, constants.MeshIoManagedByLabel, "mesh-operator")
	return config, nil
}

// OwnerRefMutator - Config mutator that adds an owner reference to service owned resources
// Should not be applied to MOP resources
type OwnerRefMutator struct{}

func (m *OwnerRefMutator) Mutate(ctx *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	var newOwnerRefs []metav1.OwnerReference = nil

	if ctx.OwnerRef != nil && ctx.Object.GetNamespace() == config.GetNamespace() {
		// Only add any new owner refs if the mop-generated config object lives in the same namespace as the
		// config-generation target. This is to prevent k8s api from deleting the config object if these live in
		// different namespaces, as it expects the ownerRef to be in the same namespace as the owned resource.
		newOwnerRefs = []metav1.OwnerReference{*ctx.OwnerRef}
	}

	existingOwnerRefs := config.GetOwnerReferences()
	mergedOwnerRef := common.MergeOwnerReferences(existingOwnerRefs, newOwnerRefs)
	if len(mergedOwnerRef) > 0 {
		config.SetOwnerReferences(mergedOwnerRef)
	} else {
		config.SetOwnerReferences(nil)
	}
	return config, nil
}

// ExtensionHashMutator - Config mutator responsible for determining a non-conflicting hash for a MOP filters
// Should not be applied to service resources
type ExtensionHashMutator struct {
	Logger     *zap.SugaredLogger
	KubeClient kube.Client
}

func (m *ExtensionHashMutator) Mutate(ctx *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	isConfigNamespaceExtension, configNamespace := hash.IsConfigNamespaceExtension(config)

	if ctx.Object == nil && !isConfigNamespaceExtension {
		// It's a NS level filter, no need to check for conflicts
		return config, nil
	}

	var knownHash = hash.ReadHash(isConfigNamespaceExtension, ctx.MeshOperator, ctx.Object)
	if knownHash == "" {
		var mopName = ctx.MeshOperator.Name
		var mopNamespace = ctx.MeshOperator.Namespace
		var objectName = ""
		var objectKind = ""

		if ctx.Object != nil {
			objectName = ctx.Object.GetName()
			objectKind = ctx.Object.GetKind()
		}

		computedHash, err := hash.ComputeObjectHash(
			m.Logger,
			isConfigNamespaceExtension,
			configNamespace,
			mopName,
			objectKind,
			mopNamespace,
			objectName,
			m.KubeClient,
			config.GroupVersionKind())

		if ctx.Object == nil {
			if err != nil {
				return nil, fmt.Errorf("failed to compute namespace hash for mop %s/%s error: %w", ctx.MeshOperator.Namespace, mopName, err)
			}
			ctx.MeshOperator.Status.NamespaceHash = computedHash
		} else {
			if err != nil {
				return nil, fmt.Errorf("failed to compute service hash for mop %s svc: %s/%s with error: %w", mopName, mopNamespace, objectName, err)
			}
			// Store the hash in the MOP status
			hash.UpdateMopHashMap(ctx.MeshOperator, objectKind, objectName, computedHash)
		}
	}
	return config, nil
}

// ExtensionNameMutator - a mutator responsible for setting a predictable non-conflicting filter names.
// It is relying on the service-hash computed by the ExtensionHashMutator
type ExtensionNameMutator struct{}

func (m *ExtensionNameMutator) Mutate(ctx *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	isConfigNamespaceExtension, configNamespace := hash.IsConfigNamespaceExtension(config)
	var extensionIndex = retrieveExtensionIndex(config.GetName())
	hashToUse := hash.ReadHash(isConfigNamespaceExtension, ctx.MeshOperator, ctx.Object)

	extensionName := hash.ComposeExtensionName(ctx.MeshOperator.Name, hashToUse, extensionIndex)
	extensionNamespace := ctx.MeshOperator.Namespace

	if isConfigNamespaceExtension {
		extensionNamespace = configNamespace
	}

	config.SetName(extensionName)
	config.SetNamespace(extensionNamespace)

	return config, nil
}

// ExtensionSourceMutator - Config mutator that maintains the filterSource annotation, which is necessary for handling cross-cluster filter ownership
type ExtensionSourceMutator struct {
	KubeClient kube.Client
}

func (m *ExtensionSourceMutator) Mutate(ctx *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {

	gvk := schema.FromAPIVersionAndKind(config.GetAPIVersion(), config.GetKind())
	gvr, err := kube.ConvertGvkToGvr(m.KubeClient.GetClusterName(), m.KubeClient.Discovery(), gvk)

	if err != nil {
		return nil, fmt.Errorf("failed to get group version for resource mop:%s/%s resource:%s", config.GetNamespace(), ctx.MeshOperator.GetName(), gvk)
	}

	resourceNamespace := ctx.MeshOperator.Namespace
	isConfigNamespaceExtension, configNamespace := hash.IsConfigNamespaceExtension(config)
	if isConfigNamespaceExtension {
		resourceNamespace = configNamespace
	}
	extensionInCluster, err := m.KubeClient.Dynamic().Resource(gvr).Namespace(resourceNamespace).Get(context.TODO(), config.GetName(), metav1.GetOptions{})
	filterSource := hash.GetExtensionSource(ctx.ClusterName, ctx.MeshOperator.Namespace, ctx.Object, isConfigNamespaceExtension)
	switch {
	case err == nil:
		svcKeyCsvInCluster := extensionInCluster.GetAnnotations()[constants.ExtensionSourceAnnotation]
		common.UpdateExtensionSourceAnnotation(config, filterSource, svcKeyCsvInCluster)
		return config, nil
	case k8serrors.IsNotFound(err):
		common.UpdateExtensionSourceAnnotation(config, filterSource, "")
		return config, nil
	}
	return nil, fmt.Errorf("failed to reconcile filterSource mop:%s/%s filter:%s", config.GetNamespace(), ctx.MeshOperator.GetName(), config.GetName())
}

type ConfigNamespaceAnnotationCleanup struct{}

func (m *ConfigNamespaceAnnotationCleanup) Mutate(_ *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	annotations := config.GetAnnotations()
	delete(annotations, constants.ConfigNamespaceExtensionAnnotation)
	config.SetAnnotations(annotations)

	return config, nil
}

type ConfigResourceParentMutator struct{}

func (m *ConfigResourceParentMutator) Mutate(ctx *RenderRequestContext, config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	var parentNamespace string
	var parentName string

	if ctx.Object != nil {
		parentNamespace = ctx.Object.GetNamespace()
		parentName = ctx.Object.GetName()
		if parentNamespace != config.GetNamespace() {
			parent := common.GetResourceParent(parentNamespace, parentName, ctx.Object.GetKind())
			common.AddAnnotation(config, constants.ResourceParent, parent)
		}
	}
	return config, nil
}

func MutateRenderedConfigs(ctx *RenderRequestContext, renderResult *GeneratedConfig, mutators []Mutator) (*GeneratedConfig, error) {
	mutatedResult := map[string][]*unstructured.Unstructured{}

	if renderResult == nil {
		return nil, nil
	}

	for k, v := range renderResult.Config {
		mutatedConfig, err := applyMutators(ctx, v, mutators)
		if err != nil {
			return nil, err
		}
		mutatedResult[k] = mutatedConfig
	}
	return NewGeneratedConfig(mutatedResult, renderResult.TemplateType, []TemplateMessage{}), nil
}

func applyMutators(ctx *RenderRequestContext, configs []*unstructured.Unstructured,
	mutators []Mutator) ([]*unstructured.Unstructured, error) {
	mutatedConfigs := make([]*unstructured.Unstructured, 0)

	for _, config := range configs {
		currentConfig := config
		for _, mutator := range mutators {
			mutatedConfig, err := mutator.Mutate(ctx, currentConfig)

			if err != nil {
				return nil, fmt.Errorf("failed to process Config %s: %w", config.GetName(), err)
			}
			currentConfig = mutatedConfig
		}
		mutatedConfigs = append(mutatedConfigs, currentConfig)
	}

	return mutatedConfigs, nil
}
