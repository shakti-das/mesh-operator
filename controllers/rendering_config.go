package controllers

import (
	"os"

	"sigs.k8s.io/yaml"
)

type RenderingConfig struct {
	Extensions    map[string]Extension        `yaml:"extensions"`
	ServiceConfig map[string]AdditionalObject `yaml:"serviceConfig"`
}

type Extension struct {
	AdditionalObjects []AdditionalObject `yaml:"additionalObjects"`
}

type AdditionalObject struct {
	Group      string `yaml:"group"`
	Version    string `yaml:"version"`
	Resource   string `yaml:"resource"`
	Namespace  string `yaml:"namespace"`
	Lookup     Lookup `yaml:"lookup"`
	Singleton  bool   `yaml:"singleton"`
	ContextKey string `yaml:"contextKey"`
}

type Lookup struct {
	MatchByServiceLabels []string `yaml:"matchByServiceLabels"`
	MatchByName          string   `yaml:"matchByName"`

	// Lookups below aren't wired in the regular additional-obj-manager and only applicable to service
	BySvcNameAndNamespace bool `yaml:"bySvcNameAndNamespace"`
	// Value specifies path to an array containing {namespace, name} tuples in the additional object
	BySvcNameAndNamespaceArray []string `yaml:"bySvcNameAndNamespaceArray"`
}

func ParseRenderingConfig(configFile string) (*RenderingConfig, error) {
	configContent, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	var renderingConfig RenderingConfig
	err = yaml.Unmarshal(configContent, &renderingConfig)
	if err != nil {
		return nil, err
	}

	return &renderingConfig, nil
}
