package templating

import (
	"reflect"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	vs1 = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.VirtualServiceResource.GroupVersion().String(),
			"kind":       constants.VirtualServiceKind.Kind,
			"metadata": map[string]interface{}{
				"namespace": "testns",
				"name":      "vs-1",
			},
		},
	}
	vs2 = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.VirtualServiceResource.GroupVersion().String(),
			"kind":       constants.VirtualServiceKind.Kind,
			"metadata": map[string]interface{}{
				"namespace": "testns",
				"name":      "vs-2",
			},
		},
	}
	dr1 = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.DestinationRuleResource.GroupVersion().String(),
			"kind":       constants.DestinationRuleKind.Kind,
			"metadata": map[string]interface{}{
				"namespace": "testns",
				"name":      "dr-1",
			},
		},
	}
	ef1 = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.EnvoyFilterResource.GroupVersion().String(),
			"kind":       constants.EnvoyFilterKind.Kind,
			"metadata": map[string]interface{}{
				"namespace": "testns",
				"name":      "filter-1",
			},
		},
	}
	namespace = &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.NamespaceResource.GroupVersion().String(),
			"kind":       constants.NamespaceKind.Kind,
			"metadata": map[string]interface{}{
				"name": "testns",
			},
		},
	}
	configObjs = []*unstructured.Unstructured{
		vs1, vs2, dr1, ef1, namespace,
	}
	config = GeneratedConfig{
		Config: map[string][]*unstructured.Unstructured{
			"default_default_virtualservice": {
				vs1, vs2,
			},
			"default_default_destinationrule": {
				dr1,
			},
			"default_default_envoyfilter": {
				ef1,
			},
			"default_default_namespace": {
				namespace,
			},
		},
		TemplateType: "default_default",
	}

	vs1Index = getIndexKey(vs1)
	vs2Index = getIndexKey(vs2)
	dr1Index = getIndexKey(dr1)
	ef1Index = getIndexKey(ef1)
	nsIndex  = getIndexKey(namespace)

	defaultObjectToTemplateIndex = map[string]string{
		vs1Index: "default_default_virtualservice",
		vs2Index: "default_default_virtualservice",
		dr1Index: "default_default_destinationrule",
		ef1Index: "default_default_envoyfilter",
		nsIndex:  "default_default_namespace",
	}
)

func TestFlattenConfig(t *testing.T) {
	actual := config.FlattenConfig()
	assert.ElementsMatch(t, configObjs, actual)
}

func TestUnflattenConfig(t *testing.T) {
	actual := UnflattenConfig(configObjs, config.TemplateType)
	assert.Equal(t, config.TemplateType, actual.TemplateType)
	assert.True(t, reflect.DeepEqual(config.Config, actual.Config))
}

func TestUnflattenConfigByTemplate(t *testing.T) {
	restoredConfigByTemplate := UnflattenConfigByTemplate(configObjs, config.TemplateType, defaultObjectToTemplateIndex)
	assert.Equal(t, config.TemplateType, restoredConfigByTemplate.TemplateType)
	assert.True(t, reflect.DeepEqual(config.Config, restoredConfigByTemplate.Config))
}

func TestGetObjectToTemplateIndex(t *testing.T) {
	config := map[string][]*unstructured.Unstructured{
		"default_default_virtualservice": {
			vs1, vs2,
		},
		"default_default_destinationrule": {
			dr1,
		},
		"default_default_envoyfilter": {
			ef1,
		},
		"default_default_namespace": {
			namespace,
		},
		"addon": nil,
	}

	gc := &GeneratedConfig{
		Config:       config,
		TemplateType: "default_default",
	}
	actual := gc.GetObjectToTemplateIndex()

	assert.Equal(t, len(defaultObjectToTemplateIndex), len(actual))

	for objKey, templateType := range defaultObjectToTemplateIndex {
		tt, exists := actual[objKey]
		assert.True(t, exists)
		assert.Equal(t, templateType, tt)
	}
}

func getIndexKey(obj *unstructured.Unstructured) string {
	return obj.GroupVersionKind().GroupVersion().String() + "/" + obj.GetKind() + "/" + obj.GetNamespace() + "/" + obj.GetName()
}
