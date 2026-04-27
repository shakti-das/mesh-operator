package patch

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/joeyb/goldenfiles"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

func TestMatchByUri(t *testing.T) {
	testFilesDir := "projects/services/servicemesh/mesh-operator/pkg/testdata/TestPatchVirtualService/use-cases/"
	workspacePath := os.Getenv("WORKSPACE_PATH") // required for bazel to write goldfiles back to source

	useCaseName := "match-by-uri"

	testCases := []struct {
		name string
	}{
		{
			name: "delegateVS-with-single-route_no-uri-match-found",
		},
		{
			name: "delegateVS-with-single-route_uri-match-found",
		},
		{
			name: "delegateVS-with-multiple-routes_no-uri-match_found",
		},
		{
			name: "delegateVS-with-multiple-routes_uri-match_found",
		},
		{
			name: "baseVS-with-single-route_no-uri-match-found",
		},
		{
			name: "baseVS-with-single-route_uri-match-found",
		},
		{
			name: "baseVS-with-multiple-routes_no-uri-match-found",
		},
		{
			name: "baseVS-with-multiple-routes_uri-match-found",
		},
		{
			name: "add-uri-route-with-destination-override",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			useCaseDirPath := testFilesDir + useCaseName + "/" + tc.name + "/"

			patchFilePath := useCaseDirPath + "/" + "patch.json"

			goldenfiles.GoldenFilePath = workspacePath + testFilesDir + useCaseName + "/" + tc.name + "/" + "result"

			err, patchedObjects, wasApplied := runPatch(t, useCaseDirPath, patchFilePath)

			assert.NoError(t, err)
			assert.NotNil(t, patchedObjects)

			if wasApplied {
				for idx, patchedObject := range patchedObjects {
					yamlBytes, err := yaml.Marshal(patchedObject)
					assert.NoError(t, err)
					goldenfiles.EqualString(t, string(yamlBytes), goldenfiles.Config{Suffix: fmt.Sprintf("-%d.yaml", idx)})
				}
			}
		})
	}
}

func TestMatchByUriAndPort(t *testing.T) {
	testFilesDir := "projects/services/servicemesh/mesh-operator/pkg/testdata/TestPatchVirtualService/use-cases/"
	workspacePath := os.Getenv("WORKSPACE_PATH") // required for bazel to write goldfiles back to source

	useCaseName := "match-by-uri-and-port"

	testCases := []struct {
		name string
	}{
		{
			name: "delegateVS-with-single-route_no-uri-match-found",
		},
		{
			name: "delegateVS-with-single-route_uri-match-found",
		},
		{
			name: "delegateVS-with-multiple-routes_no-uri-match_found",
		},
		{
			name: "delegateVS-with-multiple-routes_uri-match_found",
		},
		{
			name: "baseVS-with-single-route_no-uri-match-found",
		},
		{
			name: "baseVS-with-single-route_uri-match-found",
		},
		{
			name: "baseVS-with-multiple-routes_no-uri-match-found",
		},
		{
			name: "baseVS-with-multiple-routes_uri-match-found",
		},
		{
			name: "add-uri-route-with-destination-override",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			useCaseDirPath := testFilesDir + useCaseName + "/" + tc.name + "/"

			patchFilePath := useCaseDirPath + "/" + "patch.json"

			goldenfiles.GoldenFilePath = workspacePath + testFilesDir + useCaseName + "/" + tc.name + "/" + "result"

			err, patchedObjects, wasApplied := runPatch(t, useCaseDirPath, patchFilePath)

			assert.NoError(t, err)
			assert.NotNil(t, patchedObjects)

			if wasApplied {
				for idx, patchedObject := range patchedObjects {
					yamlBytes, err := yaml.Marshal(patchedObject)
					assert.NoError(t, err)
					goldenfiles.EqualString(t, string(yamlBytes), goldenfiles.Config{Suffix: fmt.Sprintf("-%d.yaml", idx)})
				}
			}
		})
	}
}

func runPatch(t *testing.T, useCaseDirPath string, patchFilePath string) (error, []*unstructured.Unstructured, bool) {

	baseVirtualServices := getBaseObjects(t, useCaseDirPath)

	patchInYamlFormat, err := ioutil.ReadFile(patchFilePath)
	patchBytes, _ := yaml.YAMLToJSON(patchInYamlFormat)
	overlay := v1alpha1.Overlay{
		StrategicMergePatch: runtime.RawExtension{
			Raw: patchBytes,
		},
	}

	strategy := &virtualServicePatchStrategy{objects: baseVirtualServices}
	appliedTo, err := strategy.Patch(&overlay)
	if err != nil {
		return err, nil, false
	}
	return nil, strategy.GetObjects(), len(appliedTo) > 0
}

func getBaseObjects(t *testing.T, useCaseDirPath string) []*unstructured.Unstructured {
	var result []*unstructured.Unstructured
	files, err := ioutil.ReadDir(useCaseDirPath)
	assert.Nil(t, err)

	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".yaml") {
			baseObjectBytes, err := ioutil.ReadFile(useCaseDirPath + file.Name())
			assert.Nil(t, err)
			baseObjectMap := map[string]interface{}{}
			err = yaml.Unmarshal(baseObjectBytes, &baseObjectMap)
			assert.NoError(t, err)
			baseObject := unstructured.Unstructured{Object: baseObjectMap}
			result = append(result, &baseObject)
		}
	}
	return result
}
