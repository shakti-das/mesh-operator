package rollout

import (
	"encoding/json"
	"fmt"
	"strings"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	"github.com/google/go-jsonnet"
	corev1 "k8s.io/api/core/v1"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
)

const (
	MutatingError           = "error"
	MutatingErrorUnmarshall = "unmarshall_error"
	MutatingErrorPatching   = "patching_error"
	MutatingErrorNoRollout  = "no_rollout"

	UnknownClusterError  = "unknown_cluster_error"
	ClientDiscoveryError = "client_discovery_error"

	OptInServiceTemplateBG     = "default/bg-stateless"
	OptInServiceTemplateCanary = "default/canary-stateless"

	StrategyLabelBG        = "blueGreen"
	StrategyLabelCanary    = "canary"
	StrategyReMutateBG     = "reMutateBlueGreen"
	StrategyReMutateCanary = "reMutateCanary"
)

// GetReMutationTemplate checks the service template and determines if it's opted into argo rollouts.
// If EnableTemplateMetadata is enabled and the template has rollout metadata
// with a reMutationTemplate configured, it returns true along with the re-mutation template name.
// Otherwise, falls back to legacy hardcoded template matching.
func GetReMutationTemplate(templatesManager templating.TemplatesManager, svc *corev1.Service) (bool, string) {
	templateKey := common.GetAnnotationOrAlias(constants.TemplateOverrideAnnotation, svc.GetAnnotations())
	if templateKey == "" {
		return false, ""
	}

	if features.EnableTemplateMetadata {
		templateMetadata := templatesManager.GetTemplateMetadata(templateKey)
		if templateMetadata != nil && templateMetadata.Rollout != nil && templateMetadata.Rollout.ReMutationTemplate != "" {
			return true, templateMetadata.Rollout.ReMutationTemplate
		}
		return false, ""
	}

	if templateKey == OptInServiceTemplateBG {
		return true, StrategyReMutateBG
	}
	if templateKey == OptInServiceTemplateCanary {
		return true, StrategyReMutateCanary
	}
	return false, ""
}

// GetMutationTemplate checks the service template and returns the mutation template if configured.
// If EnableTemplateMetadata is enabled, returns the mutation template name
// from the template's rollout metadata. Otherwise, falls back to legacy hardcoded template matching.
func GetMutationTemplate(templatesManager templating.TemplatesManager, svc *corev1.Service) (bool, string) {
	templateKey := common.GetAnnotationOrAlias(constants.TemplateOverrideAnnotation, svc.GetAnnotations())
	if templateKey == "" {
		return false, ""
	}

	if features.EnableTemplateMetadata {
		templateMetadata := templatesManager.GetTemplateMetadata(templateKey)
		if templateMetadata != nil && templateMetadata.Rollout != nil && templateMetadata.Rollout.MutationTemplate != "" {
			return true, templateMetadata.Rollout.MutationTemplate
		}
		return false, ""
	}

	if templateKey == OptInServiceTemplateBG {
		return true, StrategyLabelBG
	}
	if templateKey == OptInServiceTemplateCanary {
		return true, StrategyLabelCanary
	}
	return false, ""
}

// HasRolloutMetadata checks if a service template has rollout metadata configured.
// Returns true if the template has a metadata.yaml with rollout mutation and re-mutation template defined.
func HasRolloutMetadata(templatesManager templating.TemplatesManager, templateKey string) bool {
	if templateKey == "" {
		return false
	}
	templateMetadata := templatesManager.GetTemplateMetadata(templateKey)
	if templateMetadata == nil || templateMetadata.Rollout == nil {
		return false
	}
	return templateMetadata.Rollout.MutationTemplate != "" && templateMetadata.Rollout.ReMutationTemplate != ""
}

func IsRolloutResourcePresentInCluster(client kube.Client) bool {
	_, err := kube.ConvertGvkToGvr(
		client.GetClusterName(),
		client.Discovery(),
		schema.GroupVersionKind{
			Group:   constants.RolloutKind.Group,
			Version: constants.RolloutKind.Version,
			Kind:    constants.RolloutKind.Kind})

	return err == nil
}

// AddServiceNameToRolloutIndexer - create an indexer over Rollout objects that associates serviceName -> rollout
func AddServiceNameToRolloutIndexer(informer cache.SharedIndexInformer) error {
	return informer.AddIndexers(cache.Indexers{
		constants.ServiceIndexName: func(obj interface{}) ([]string, error) {
			if ro := obj.(*unstructured.Unstructured); ro != nil {
				return getServiceKeyFromRollout(ro), nil
			}
			return []string{}, nil
		},
	})
}

// GetRolloutsByServiceAcrossClusters - get all rollouts backing given service from the provided cluster clients
func GetRolloutsByServiceAcrossClusters(clients []kube.Client, namespace, serviceName string) ([]*unstructured.Unstructured, error) {
	if !features.EnableArgoIntegration {
		return nil, nil
	}
	var rollouts []*unstructured.Unstructured
	for _, client := range clients {
		if !IsRolloutResourcePresentInCluster(client) {
			continue
		}
		rolloutInformer := client.DynamicInformerFactory().ForResource(constants.RolloutResource)
		rolloutsInCluster, err := GetRolloutsByService(rolloutInformer.Informer(), namespace, serviceName)
		if err != nil {
			return nil, err
		}
		rollouts = append(rollouts, rolloutsInCluster...)
	}
	return rollouts, nil
}

// GetRolloutsByService returns all rollouts which are referencing specified service
func GetRolloutsByService(rolloutInformer cache.SharedIndexInformer, serviceNamespace string, serviceName string) ([]*unstructured.Unstructured, error) {
	objs, err := rolloutInformer.GetIndexer().ByIndex(constants.ServiceIndexName, fmt.Sprintf("%s/%s", serviceNamespace, serviceName))
	if err != nil {
		return nil, err
	}
	var rollouts []*unstructured.Unstructured
	for _, obj := range objs {
		if ro := obj.(*unstructured.Unstructured); ro != nil {
			rollouts = append(rollouts, ro)
		}
	}
	return rollouts, nil
}

func CreateRolloutPatches(templatesManager templating.TemplatesManager, service *corev1.Service, rolloutAsJson []byte, strategy string, metadata map[string]string) (string, error) {
	if metadata == nil {
		metadata = map[string]string{}
	}
	vm := jsonnet.MakeVM()
	vm.Importer(&jsonnet.FileImporter{
		JPaths: templatesManager.GetTemplatePaths(),
	})

	serviceJson, err := json.Marshal(service)

	if err != nil {
		return "", fmt.Errorf("failed to serialize service: %w", err)
	}

	metadataJson, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("failed to serialize context: %w", err)
	}

	vm.TLACode("context", string(metadataJson))

	vm.TLACode("service", string(serviceJson))
	vm.TLACode("rollout", string(rolloutAsJson))

	var patch string

	templatePrefix := "rollouts" + constants.TemplateKeyDelimiter + strategy
	templates := templatesManager.GetTemplatesByPrefix(templatePrefix)
	for key, template := range templates {
		patchFromTemplate, err := vm.EvaluateAnonymousSnippet(key+".jsonnet", template)
		if err != nil {
			return "", err
		}

		if strings.TrimSpace(patchFromTemplate) != NullPatch {
			patch = patchFromTemplate
		}
		break
	}
	return patch, nil
}

func getServiceKeyFromRollout(rollout *unstructured.Unstructured) []string {
	var serviceKey []string
	activeServiceName := rollout.GetAnnotations()[constants.BgActiveServiceAnnotation]
	if activeServiceName != "" {
		serviceKey = append(serviceKey, fmt.Sprintf("%s/%s", rollout.GetNamespace(), activeServiceName))
	}
	return serviceKey
}

func GetMutatingErrorLabel(errorType string, clusterName string) map[string]string {
	return getLabel(MutatingError, errorType, clusterName)
}
