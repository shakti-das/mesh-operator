package patch

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"testing"

	error2 "github.com/istio-ecosystem/mesh-operator/pkg/errors"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/joeyb/goldenfiles"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func TestPatchVirtualServiceBasics(t *testing.T) {
	testFilesDir := "../testdata/TestPatchVirtualService/"
	workspacePath := os.Getenv("WORKSPACE_PATH") // required for bazel to write goldfiles back to source
	goldenfiles.GoldenFilePath = workspacePath + testFilesDir

	testCases := []struct {
		name      string
		baseFile  string
		patchFile string
	}{
		{
			// Add a label to the object
			name:      "Merge-objects",
			baseFile:  "base-object-with-name.yaml",
			patchFile: "Merge-objects",
		},
		{
			// Delete VS route by name
			name:      "Delete-item-in-named-object-list",
			baseFile:  "base-object-with-name.yaml",
			patchFile: "Delete-item-in-named-object-list",
		},
		{
			// Delete VS route by port
			name:      "Delete-item-in-named-object-list-by-port",
			baseFile:  "base-object-with-port-and-name.yaml",
			patchFile: "Delete-item-in-named-object-list-by-port",
		},
		{
			// Add gateway to VS by name
			name:      "Append-item-to-primitives-list",
			baseFile:  "base-object-with-name.yaml",
			patchFile: "Append-item-to-primitives-list",
		},
		{
			// Add gateway to VS by port
			name:      "Append-item-to-primitives-list-by-port",
			baseFile:  "base-object-with-port-and-name.yaml",
			patchFile: "Append-item-to-primitives-list",
		},
		{
			// Delete host entry from VS
			name:      "Delete-item-in-primitives-list",
			baseFile:  "base-object-with-name.yaml",
			patchFile: "Delete-item-in-primitives-list",
		},
		{
			name:      "Replace-item-in-primitives-list",
			baseFile:  "base-object-with-name.yaml",
			patchFile: "Replace-item-in-primitives-list",
		},
		{
			// Apply timeout to all routes
			name:      "Apply-item-to-unnamed-object-list",
			baseFile:  "multiple-routes-base-object.yaml",
			patchFile: "Apply-item-to-unnamed-object-list",
		},
		{
			// VS routes must not be reordered
			name:      "Vs-routes-not-reordered",
			baseFile:  "vs-with-routes-reordering.yaml",
			patchFile: "Route-patch-causing-reordering",
		},
		{
			// VS routes (spec.http.route) with replace strategy
			name:      "Replace-routes-name-match",
			baseFile:  "vs-route-replace-strategy.yaml",
			patchFile: "Route-for-replace-strategy",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			baseObjectBytes, err := ioutil.ReadFile(testFilesDir + "use-cases/" + tc.baseFile)
			assert.NoError(t, err)

			patchFilePath := testFilesDir + "use-cases/" + tc.patchFile + ".patch.json"
			assert.NoError(t, err)

			err, patchedObject, wasApplied := runStrategicPatch(t, err, baseObjectBytes, patchFilePath)

			assert.True(t, wasApplied)
			yamlBytes, err := yaml.Marshal(patchedObject)
			assert.NoError(t, err)
			goldenfiles.EqualString(t, string(yamlBytes), goldenfiles.Config{Suffix: ".yaml"})

		})
	}
}

func TestPatchVirtualServiceSuccess(t *testing.T) {
	testFilesDir := "../testdata/TestPatchVirtualService/"
	workspacePath := os.Getenv("WORKSPACE_PATH") // required for bazel to write goldfiles back to source
	goldenfiles.GoldenFilePath = workspacePath + testFilesDir

	testCases := []struct {
		name      string
		baseFile  string
		patchFile string
	}{
		{
			name:      "Match-by-name",
			baseFile:  "base-object-with-name.yaml",
			patchFile: "Match-by-name",
		},
		{
			name:      "Match-by-port",
			baseFile:  "base-object-with-port.yaml",
			patchFile: "Match-by-port",
		},
		{
			name:      "Match-by-name-and-port",
			baseFile:  "base-object-with-port-and-name.yaml",
			patchFile: "Match-by-port",
		},
		{
			name:      "Match-multiple-routes-name",
			baseFile:  "multiple-routes-name-base-object.yaml",
			patchFile: "Match-multiple-routes-name",
		},
		{
			name:      "Match-multiple-route-types",
			baseFile:  "multiple-route-types-base-object.yaml",
			patchFile: "Match-multiple-route-types",
		},
		{
			name:      "Match-multiple-routes-port",
			baseFile:  "multiple-routes-port-base-object.yaml",
			patchFile: "Match-multiple-routes-port",
		},
		{
			name:      "Match-by-port",
			baseFile:  "base-object-with-header-match.yaml",
			patchFile: "Match-by-port",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			baseObjectBytes, err := ioutil.ReadFile(testFilesDir + "use-cases/" + tc.baseFile)
			assert.NoError(t, err)

			patchFilePath := testFilesDir + "use-cases/" + tc.patchFile + ".patch.json"
			assert.NoError(t, err)

			err, patchedObject, wasApplied := runStrategicPatch(t, err, baseObjectBytes, patchFilePath)

			assert.NoError(t, err)
			assert.NotNil(t, patchedObject)

			if wasApplied {
				yamlBytes, err := yaml.Marshal(patchedObject)
				assert.NoError(t, err)
				goldenfiles.EqualString(t, string(yamlBytes), goldenfiles.Config{Suffix: ".yaml"})
			}
		})
	}
}

func TestPatchVirtualServiceFailure(t *testing.T) {
	testFilesDir := "../testdata/TestPatchVirtualService/"
	workspacePath := os.Getenv("WORKSPACE_PATH") // required for bazel to write goldfiles back to source
	goldenfiles.GoldenFilePath = workspacePath + testFilesDir

	testCases := []struct {
		name      string
		baseFile  string
		patchFile string
	}{
		{
			name:      "Match-by-name-fail",
			baseFile:  "base-object-with-name.yaml",
			patchFile: "Match-by-name-fail",
		},
		{
			name:      "Match-by-port-fail",
			baseFile:  "base-object-with-port-and-name.yaml",
			patchFile: "Match-by-port-fail",
		},
		{
			name:      "Match-by-name-and-port-fail",
			baseFile:  "base-object-with-port-and-name.yaml",
			patchFile: "Match-by-port-fail",
		},
		{
			name:      "Match-multiple-routes-port-fail",
			baseFile:  "multiple-routes-port-base-object.yaml",
			patchFile: "Match-multiple-routes-port-fail",
		},
		{
			name:      "Match-multiple-route-types-fail-tcp",
			baseFile:  "multiple-route-types-base-object.yaml",
			patchFile: "Match-multiple-route-types-fail-tcp",
		},
		{
			name:      "Match-multiple-route-types-fail-http",
			baseFile:  "multiple-route-types-base-object.yaml",
			patchFile: "Match-multiple-route-types-fail-http",
		},
		{
			// Add a route to VS (not supported)
			name:      "Append-item-to-named-object-list",
			baseFile:  "base-object-with-name.yaml",
			patchFile: "Append-item-to-named-object-list",
		},
		{
			// NOT-SUPPORTED: Add destination to a VS route
			name:      "Append-item-to-unnamed-object-list",
			baseFile:  "base-object-with-name.yaml",
			patchFile: "Append-item-to-unnamed-object-list",
		},
		{ // Skip matching when delegate is present /http/name+port+delegate
			name:      "Match-by-name-delegate-present",
			baseFile:  "base-object-with-delegate.yaml",
			patchFile: "Match-by-name",
		},
		{ // Skip matching when delegate is present /http/name+port+delegate
			name:      "Match-by-port-delegate-present",
			baseFile:  "base-object-with-delegate.yaml",
			patchFile: "Match-by-port",
		},
		{
			// can't match TCP routes against HTTP routes
			name:      "Apply-item-to-unnamed-object-list-fail",
			baseFile:  "multiple-routes-base-object.yaml",
			patchFile: "Apply-item-to-unnamed-object-list-fail",
		},
		{
			// can't apply an overlay with a bad port
			name:      "Apply-overlay-with-bad-port-value",
			baseFile:  "multiple-routes-base-object.yaml",
			patchFile: "Apply-overlay-with-bad-port",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			baseObjectBytes, err := ioutil.ReadFile(testFilesDir + "use-cases/" + tc.baseFile)
			assert.NoError(t, err)

			patchFilePath := testFilesDir + "use-cases/" + tc.patchFile + ".patch.json"
			assert.NoError(t, err)

			err, _, wasApplied := runStrategicPatch(t, err, baseObjectBytes, patchFilePath)

			assert.False(t, wasApplied)
		})
	}
}

func TestPatchDestinationRule(t *testing.T) {
	testFilesDir := "../testdata/TestPatchDestinationRule/"
	workspacePath := os.Getenv("WORKSPACE_PATH") // required for bazel to write goldfiles back to source
	goldenfiles.GoldenFilePath = workspacePath + testFilesDir

	testCases := []struct {
		name          string
		patchFile     string
		expectedError string
	}{
		{
			name:      "Override-low-risk-params",
			patchFile: "Override-low-risk-params",
		},
		{
			name:      "Overlay-with-non-matching-host",
			patchFile: "Overlay-with-non-matching-host",
		},
		{
			name:      "Override-high-risk-params",
			patchFile: "Override-high-risk-params",
		},
		{
			name:      "Overlay-with-empty-slice",
			patchFile: "Overlay-with-empty-slice",
		},
		{
			name:      "Overlay-with-consistentHash-enabled",
			patchFile: "Overlay-with-consistentHash-enabled",
		},
		{
			name:      "Overlay-with-consistentHash-disabled",
			patchFile: "Overlay-with-consistentHash-disabled",
		},
	}

	baseObjectBytes, err := ioutil.ReadFile(testFilesDir + "use-cases/base-dr.yaml")
	assert.NoError(t, err)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patchFilePath := testFilesDir + "use-cases/" + tc.patchFile + ".patch.yaml"
			err, patchedObject, wasApplied := runStrategicPatch(t, err, baseObjectBytes, patchFilePath)

			if tc.expectedError != "" {
				assert.Error(t, err)
				assert.Equal(t, tc.expectedError, err.Error())
				assert.False(t, wasApplied)
			} else {
				assert.True(t, wasApplied)
				yamlBytes, err := yaml.Marshal(patchedObject)
				assert.NoError(t, err)
				goldenfiles.EqualString(t, string(yamlBytes), goldenfiles.Config{Suffix: ".yaml"})
			}
		})
	}
}

func TestValidateVirtualServiceOverlay(t *testing.T) {
	testFilesDir := "../testdata/TestValidateVirtualServiceOverlay/"

	testCases := []struct {
		name          string
		patchFile     string
		errorExpected bool
		expectedError string
	}{
		{
			name:      "Happy case (route override)",
			patchFile: "Happy-case-route",
		},
		{
			name:      "Happy case (uri match)",
			patchFile: "Happy-case-uri-match",
		},
		{
			name:      "port and uri in match request allowed",
			patchFile: "Happy-case-port-and-uri-match",
		},
		{
			name:      "Happy case (gateways override)",
			patchFile: "Happy-case-gateways",
		},
		{
			name:      "Happy case (metadata override)",
			patchFile: "Happy-case-metadata",
		},
		{
			name:          "Invalid 'match' object type",
			errorExpected: true,
			patchFile:     "broken-overlay",
			expectedError: "invalid match object, please make sure it follows Istio VirtualService spec",
		},
		{
			name:          "Metadata and spec override",
			errorExpected: true,
			patchFile:     "Metadata-and-spec",
			expectedError: "mixing metadata and spec overrides not allowed in the same overlay",
		},
		{
			name:          "Gateway and route override",
			patchFile:     "Gateway-and-route",
			errorExpected: true,
			expectedError: "mixing route types and/or non-route overrides not allowed in the same overlay",
		},
		{
			name:          "Multiple route types",
			patchFile:     "Multiple-route-types",
			errorExpected: true,
			expectedError: "mixing route types and/or non-route overrides not allowed in the same overlay",
		},
		{
			name:          "Multiple routes of the same type",
			patchFile:     "Multiple-routes",
			errorExpected: true,
			expectedError: "only one route can be overridden per overlay",
		},
		{
			name:          "Multiple route matches",
			patchFile:     "Multiple-route-matches",
			errorExpected: true,
			expectedError: "only exactly one route match allowed per route override. route: 0",
		},
		{
			name:          "Invalid singlematch condition present",
			patchFile:     "Invalid-single-match-condition-present",
			errorExpected: true,
			expectedError: "only port/uri match allowed in route override. route: 0",
		},
		{
			name:          "Invalid match condition present",
			patchFile:     "Invalid-match-condition-present",
			errorExpected: true,
			expectedError: "only port/uri match allowed in route override. route: 0",
		},
		{
			name:          "Invalid multiple match condition present",
			patchFile:     "Invalid-multiple-match-condition-present",
			errorExpected: true,
			expectedError: "only port/uri match allowed in route override. route: 0",
		},
	}

	vsValidationStrategy := virtualServiceValidationStrategy{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patchFilePath := testFilesDir + tc.patchFile + ".patch.yaml"
			patchInYamlFormat, err := ioutil.ReadFile(patchFilePath)
			patchBytes, _ := yaml.YAMLToJSON(patchInYamlFormat)
			assert.NoError(t, err)
			overlay := v1alpha1.Overlay{
				StrategicMergePatch: runtime.RawExtension{
					Raw: patchBytes,
				},
			}

			err = vsValidationStrategy.Validate(&overlay)

			if tc.errorExpected {
				assert.NotNil(t, err)
				assert.Equal(t, tc.expectedError, err.Error())
				assert.True(t, error2.IsUserConfigError(err))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateDestinationRuleOverlay(t *testing.T) {
	testFilesDir := "../testdata/TestValidateDestinationRuleOverlay/"

	testCases := []struct {
		name          string
		patchFile     string
		errorExpected bool
		expectedError string
	}{
		{
			name:          "Overlay without host",
			patchFile:     "Overlay-without-host",
			errorExpected: false,
		},
		{
			name:          "Overlay with host",
			patchFile:     "Overlay-with-host",
			errorExpected: false,
		},
	}

	drValidationStrategy := destinationRuleValidationStrategy{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			patchFilePath := testFilesDir + tc.patchFile + ".patch.yaml"
			patchInYamlFormat, err := ioutil.ReadFile(patchFilePath)
			patchBytes, _ := yaml.YAMLToJSON(patchInYamlFormat)
			overlay := v1alpha1.Overlay{
				StrategicMergePatch: runtime.RawExtension{
					Raw: patchBytes,
				},
			}

			err = drValidationStrategy.Validate(&overlay)

			if tc.errorExpected {
				assert.NotNil(t, err)
				assert.Equal(t, tc.expectedError, err.Error())
				assert.True(t, error2.IsUserConfigError(err))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestOverlayToObject(t *testing.T) {
	overlayBytesWithIntValue, _ := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"field1": 1,
		},
	})

	overlayBytesWithFloatValue, _ := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"field2": 1.1,
		},
	})

	overlayBytesWithIntAndFloatValue, _ := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"field1": 1,
			"field2": 1.1,
		},
	})

	testCases := []struct {
		name             string
		overlay          *v1alpha1.Overlay
		assertIntValue   bool
		assertFloatValue bool
		expectedError    error
	}{
		{
			name: "OverlayWithIntValue",
			overlay: &v1alpha1.Overlay{
				StrategicMergePatch: runtime.RawExtension{
					Raw: overlayBytesWithIntValue,
				},
			},
			assertIntValue: true,
		},
		{
			name: "OverlayWithFloatValue",
			overlay: &v1alpha1.Overlay{
				StrategicMergePatch: runtime.RawExtension{
					Raw: overlayBytesWithFloatValue,
				},
			},
			assertFloatValue: true,
		},
		{
			name: "OverlayWithIntAndFloatValue",
			overlay: &v1alpha1.Overlay{
				StrategicMergePatch: runtime.RawExtension{
					Raw: overlayBytesWithIntAndFloatValue,
				},
			},
			assertFloatValue: true,
		},
		{
			name: "BadOverlay - Nil Patch Value",
			overlay: &v1alpha1.Overlay{
				Kind: "whatever",
			},
			expectedError: errors.New("unexpected end of JSON input"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			overlayObject, err := OverlayToObject(tc.overlay)

			if tc.expectedError != nil {
				assert.Error(t, err, tc.expectedError.Error())
			} else {
				assert.Nil(t, err)
			}

			if tc.assertIntValue {
				fieldValue, _, _ := unstructured.NestedFieldNoCopy(overlayObject, "spec", "field1")
				_, isInt := fieldValue.(int64)
				assert.True(t, isInt)
			}

			if tc.assertFloatValue {
				fieldValue, _, _ := unstructured.NestedFieldNoCopy(overlayObject, "spec", "field2")
				_, isFloat := fieldValue.(float64)
				assert.True(t, isFloat)
			}

		})
	}
}

func runStrategicPatch(t *testing.T, err error, baseObjectBytes []byte, patchFilePath string) (error, *unstructured.Unstructured, bool) {
	baseObjectMap := map[string]interface{}{}
	err = yaml.Unmarshal(baseObjectBytes, &baseObjectMap)
	assert.NoError(t, err)
	baseObject := unstructured.Unstructured{Object: baseObjectMap}

	patchInYamlFormat, err := ioutil.ReadFile(patchFilePath)
	patchBytes, _ := yaml.YAMLToJSON(patchInYamlFormat)
	overlay := v1alpha1.Overlay{
		StrategicMergePatch: runtime.RawExtension{
			Raw: patchBytes,
		},
	}

	strategy := GetStrategyForKindOrDefault(baseObject.GetKind(), []*unstructured.Unstructured{&baseObject})
	appliedTo, err := strategy.Patch(&overlay)
	if err != nil {
		return err, nil, false
	}
	return nil, strategy.GetObjects()[0], len(appliedTo) > 0
}
