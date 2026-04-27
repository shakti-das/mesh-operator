package templating

import (
	"strings"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type GeneratedConfig struct {
	// Config maps the generated config using the template type and object kind as key
	Config map[string][]*unstructured.Unstructured
	// TemplateType is the template used for config generation.
	TemplateType string
	// Messages holds user-facing errors emitted by templates via msg.error().
	// Non-empty only for MOP extension renders where templates signaled issues.
	Messages []TemplateMessage
}

func NewGeneratedConfig(config map[string][]*unstructured.Unstructured, templateType string, messages []TemplateMessage) *GeneratedConfig {
	var configs map[string][]*unstructured.Unstructured
	if config == nil {
		configs = make(map[string][]*unstructured.Unstructured, 0)
	} else {
		configs = config
	}
	return &GeneratedConfig{
		Config:       configs,
		TemplateType: templateType,
		Messages:     messages,
	}
}

// FlattenConfig transforms the config into a slice of unstructured objects. Reverses UnflattenConfig.
func (c *GeneratedConfig) FlattenConfig() []*unstructured.Unstructured {
	var result []*unstructured.Unstructured
	for _, objects := range c.Config {
		result = append(result, objects...)
	}
	return result
}

// UnflattenConfig transforms a slice of unstructured objects into a GeneratedConfig object.
// Reverses GeneratedConfig.FlattenConfig.
func UnflattenConfig(objects []*unstructured.Unstructured, templateType string) *GeneratedConfig {
	config := make(map[string][]*unstructured.Unstructured, 0)
	for _, obj := range objects {
		key := createConfigKey(templateType, obj)
		if slc, exists := config[key]; exists {
			slc = append(slc, obj)
			config[key] = slc
		} else {
			config[key] = make([]*unstructured.Unstructured, 1)
			config[key][0] = obj
		}
	}
	return &GeneratedConfig{
		Config:       config,
		TemplateType: templateType,
	}
}

func createConfigKey(templateType string, obj *unstructured.Unstructured) string {
	return strings.Join([]string{templateType, strings.ToLower(obj.GetKind())}, constants.TemplateKeyDelimiter)
}

func (c *GeneratedConfig) GetObjectToTemplateIndex() map[string]string {
	objectToTemplateMapping := make(map[string]string)
	for templateName, configList := range c.Config {
		for _, config := range configList {
			key := config.GroupVersionKind().GroupVersion().String() + "/" + config.GetKind() + "/" + config.GetNamespace() + "/" + config.GetName()
			objectToTemplateMapping[key] = templateName
		}
	}
	return objectToTemplateMapping
}

// UnflattenConfigByTemplate transforms a slice of unstructured objects into a GeneratedConfig object restoring the original templateType to config list mapping
func UnflattenConfigByTemplate(objects []*unstructured.Unstructured, templateType string, objectToTemplateIndex map[string]string) *GeneratedConfig {
	config := make(map[string][]*unstructured.Unstructured, 0)
	for _, obj := range objects {
		configKey := obj.GroupVersionKind().GroupVersion().String() + "/" + obj.GetKind() + "/" + obj.GetNamespace() + "/" + obj.GetName()
		template := objectToTemplateIndex[configKey]
		config[template] = append(config[template], obj)
	}
	return &GeneratedConfig{
		Config:       config,
		TemplateType: templateType,
	}
}

func BucketizeConfigByKind(configObjects []*unstructured.Unstructured) map[string][]*unstructured.Unstructured {
	result := map[string][]*unstructured.Unstructured{}
	for _, obj := range configObjects {
		result[obj.GetKind()] = append(result[obj.GetKind()], obj)
	}
	return result
}
