package controllers

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRenderingConfig(t *testing.T) {
	// Create a temporary YAML file with sample data
	sampleYAML := `
extensions:
  extension1:
    additionalObjects:
    - group: "group1"
      version: "v1"
      resource: "resource1"
      namespace: "namespace1"
      lookup:
        matchByServiceLabels:
        - "label1"
      singleton: true
      contextKey: "deploy1"
  extension2:
    additionalObjects:
    - group: "group2"
      version: "v2"
      resource: "resource2"
      namespace: "namespace2"
      lookup:
        matchByName: "resource-name"
      singleton: true
      contextKey: "deploy2"
`

	tmpfile, err := ioutil.TempFile("", "test-rendering-config-*.yaml")
	assert.Nil(t, err, "Failed to create temporary YAML file")
	defer os.Remove(tmpfile.Name()) // clean up

	_, err = tmpfile.Write([]byte(sampleYAML))
	assert.Nil(t, err, "Failed to write sample YAML to temporary file")
	tmpfile.Close()

	renderingConfig, err := ParseRenderingConfig(tmpfile.Name())
	assert.Nil(t, err, "Failed to parse renderingConfig from YAML file")

	expectedExtensionCount := 2
	assert.Equal(t, expectedExtensionCount, len(renderingConfig.Extensions))

	extension1, _ := renderingConfig.Extensions["extension1"]
	addObj1 := extension1.AdditionalObjects[0]
	assert.Equal(t, "group1", addObj1.Group)
	assert.Equal(t, "v1", addObj1.Version)
	assert.Equal(t, "resource1", addObj1.Resource)
	assert.Equal(t, "namespace1", addObj1.Namespace)
	assert.Equal(t, "label1", addObj1.Lookup.MatchByServiceLabels[0])
	assert.Equal(t, true, addObj1.Singleton)
	assert.Equal(t, "deploy1", addObj1.ContextKey)

	extension2, _ := renderingConfig.Extensions["extension2"]
	addObj2 := extension2.AdditionalObjects[0]
	assert.Equal(t, "group2", addObj2.Group)
	assert.Equal(t, "v2", addObj2.Version)
	assert.Equal(t, "resource2", addObj2.Resource)
	assert.Equal(t, "namespace2", addObj2.Namespace)
	assert.Equal(t, "resource-name", addObj2.Lookup.MatchByName)
	assert.Equal(t, true, addObj2.Singleton)
	assert.Equal(t, "deploy2", addObj2.ContextKey)

}
