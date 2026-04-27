package templating

import (
	"sigs.k8s.io/yaml"
)

// TemplateMetadata holds metadata for a template directory.
// This metadata is loaded from metadata.yaml files within template directories.
type TemplateMetadata struct {
	Rollout *RolloutMetadata `yaml:"rollout,omitempty"`
}

type RolloutMetadata struct {
	// MutationTemplate is the template name used for rollout mutation (e.g., "blueGreen", "canary")
	MutationTemplate string `yaml:"mutationTemplate,omitempty"`
	// ReMutationTemplate is the template name used for rollout re-mutation (e.g., "reMutateBlueGreen", "reMutateCanary")
	ReMutationTemplate string `yaml:"reMutationTemplate,omitempty"`
}

func ParseTemplateMetadata(content []byte) (*TemplateMetadata, error) {
	var metadata TemplateMetadata
	if err := yaml.Unmarshal(content, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}
