package templating

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	meshoperrors "github.com/istio-ecosystem/mesh-operator/pkg/errors"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	// test-vs-1/http/test-route-1/
	testVs = unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "VirtualService",
			"metadata": map[string]interface{}{
				"name": "test-vs-1",
			},
			"spec": map[string]interface{}{
				"http": []interface{}{
					buildRoute("test-route-1", 0, "", "", 0),
				},
			},
		},
	}

	// test-vs-1/http/test-route-2/
	otherVs = unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "VirtualService",
			"metadata": map[string]interface{}{
				"name": "test-vs-2",
			},
			"spec": map[string]interface{}{
				"http": []interface{}{
					buildRoute("test-route-2", 0, "", "", 0),
				},
			},
		},
	}

	generatedConfig = UnflattenConfig([]*unstructured.Unstructured{&testVs, &otherVs}, "test-template")

	matchingOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				buildRoute("test-route-1", 0, "", "", 100),
			},
		},
	})

	someOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				buildRoute("default", 0, "10s", "", 0),
			},
		},
	})

	matchingPort123OverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				buildRoute("", 123, "10s", "", 0),
			},
		},
	})

	matchingPort321OverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				buildRoute("", 321, "10s", "", 0),
			},
		},
	})

	noMatchingPortOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				buildRoute("", 666, "10s", "", 0),
			},
		},
	})

	matchingNameAndPortOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				buildRoute("default", 123, "10s", "", 0),
			},
		},
	})

	nonMatchingNameAndPortOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				buildRoute("default", 666, "10s", "", 0),
			},
		},
	})

	drOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"subsets": []interface{}{
				map[string]interface{}{
					"name": "live",
				},
			},
		},
	})
	// /http/test-route-1/
	matchingOverlayWithVSName = v1alpha1.Overlay{
		Name: "test-vs-1",
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: matchingOverlayBytes,
		},
	}

	// DR overlay
	drOverlay = v1alpha1.Overlay{
		Kind: "DestinationRule",
		StrategicMergePatch: runtime.RawExtension{
			Raw: drOverlayBytes,
		},
	}
	// /http//123
	matchingOverlayWithPort1 = buildVsOverlayWithBytes(matchingPort123OverlayBytes)

	// /http//321
	matchingOverlayWithPort2 = buildVsOverlayWithBytes(matchingPort321OverlayBytes)

	// /http/default/123
	matchingNameAndPortOverlay = buildVsOverlayWithBytes(matchingNameAndPortOverlayBytes)

	// /http/default/666
	nonMatchingPortWithNameOverlay = buildVsOverlayWithBytes(nonMatchingNameAndPortOverlayBytes)

	// /http//666
	noMatchingPortOverlay = buildVsOverlayWithBytes(noMatchingPortOverlayBytes)

	// /http/default/666
	nonMatchingNameWithPortOverlay = buildVsOverlayWithBytes(nonMatchingNameAndPortOverlayBytes)

	// /http/test-route-1/
	matchingOverlayNoVsNameWithRetries = buildVsOverlayWithBytes(matchingOverlayBytes)

	// /http/default/
	overlayWithNoNameWithTimeout = buildVsOverlayWithBytes(someOverlayBytes)

	overlaidRetriesVs = unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "VirtualService",
			"metadata": map[string]interface{}{
				"name": "test-vs-1",
			},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"name":    "test-route-1",
						"retries": "100",
					},
				},
			},
		},
	}

	overlaidBothLabelsRetriesVs = unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "VirtualService",
			"metadata": map[string]interface{}{
				"name": "test-vs-1",
				"labels": map[string]interface{}{
					"overlay1": "value1",
					"overlay2": "value2",
				},
			},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"name":    "test-route-1",
						"retries": "100",
					},
				},
			},
		},
	}

	overlaidBothLabelsVs = unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "VirtualService",
			"metadata": map[string]interface{}{
				"name": "test-vs-1",
				"labels": map[string]interface{}{
					"overlay1": "value1",
					"overlay2": "value2",
				},
			},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"name": "test-route-1",
					},
				},
			},
		},
	}

	overlay1Bytes, _ = json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"overlay1": "value1",
			},
		},
	})
	overlay1 = v1alpha1.Overlay{
		Name: "test-vs-1",
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: overlay1Bytes,
		},
	}

	overlay2Bytes, _ = json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"overlay2": "value2",
			},
		},
	})
	overlay2 = v1alpha1.Overlay{
		Name: "test-vs-1",
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: overlay2Bytes,
		},
	}

	nonMatchingOverlayBytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []map[string]interface{}{
				{
					"name":    "non-matching-route",
					"retries": "100",
				},
			},
		},
	})

	nonMatchingOverlay = v1alpha1.Overlay{
		Name: "test-vs-1",
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: nonMatchingOverlayBytes,
		},
	}
)

func buildVsOverlayWithBytes(overlayBytes []byte) v1alpha1.Overlay {
	return v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: overlayBytes,
		},
	}
}

func getOverlayNoMatchingRouteErrorMessage(overlayIdx int) string {
	return fmt.Sprintf("error encountered when overlaying: mop test-mop, overlay <%d>: no matching route found to apply overlay", overlayIdx)
}

func TestApplyOverlays_NegativeTests(t *testing.T) {
	testCases := []struct {
		name          string
		overlays      []v1alpha1.Overlay
		expectedError string
		config        *GeneratedConfig
	}{
		{
			name:     "Nil overlays",
			overlays: nil,
			config:   generatedConfig,
		},
		{
			name:     "Empty overlays",
			overlays: []v1alpha1.Overlay{},
			config:   generatedConfig,
		},
		{
			name: "Non matching overlays (VS specific)",
			overlays: []v1alpha1.Overlay{
				nonMatchingOverlay,
			},
			config:        generatedConfig,
			expectedError: "error encountered when overlaying: mop test-mop, overlay <0>: no matching route found to apply overlay",
		},
		{
			name:     "Empty generated config",
			overlays: []v1alpha1.Overlay{matchingOverlayWithVSName},
			config: &GeneratedConfig{
				Config: nil,
			},
		},
		{
			name:     "Nil generated config",
			overlays: []v1alpha1.Overlay{matchingOverlayWithVSName},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			overlayMap := map[string][]v1alpha1.Overlay{
				"test-mop": tc.overlays,
			}
			result, err := ApplyOverlays(tc.config, overlayMap)

			if tc.expectedError != "" {
				assert.EqualError(t, err, tc.expectedError)
				assert.True(t, meshoperrors.IsOverlayingError(err))
				assert.Equal(t, tc.config, result) // return original config if this overlay doesn't go through
			} else {
				assert.Nil(t, err)
				if tc.config != nil {
					assert.ElementsMatch(t, tc.config.FlattenConfig(), result.FlattenConfig())
				}
			}
		})
	}
}

func TestApplyOverlays_HappyCase(t *testing.T) {
	expectedResult := []*unstructured.Unstructured{&overlaidRetriesVs, &otherVs}

	result, err := ApplyOverlays(generatedConfig, map[string][]v1alpha1.Overlay{"test-mop": {matchingOverlayWithVSName}})

	assert.Nil(t, err)
	assert.ElementsMatch(t, expectedResult, result.FlattenConfig())
}

func TestApplyMultipleOverlays_HappyCase(t *testing.T) {
	result, err := ApplyOverlays(generatedConfig, map[string][]v1alpha1.Overlay{"test-mop": {matchingOverlayWithVSName, overlay1, overlay2}})

	assert.Nil(t, err)

	expectedResult := []*unstructured.Unstructured{&overlaidBothLabelsRetriesVs, &otherVs}
	assert.ElementsMatch(t, expectedResult, result.FlattenConfig())
}

func TestApplyOverlayWithMultipleMops_HappyCase(t *testing.T) {
	result, err := ApplyOverlays(generatedConfig, map[string][]v1alpha1.Overlay{
		"test-mop-1": {matchingOverlayWithVSName},
		"test-mop-2": {overlay1},
		"test-mop-3": {overlay2},
	})

	assert.Nil(t, err)

	expectedResult := []*unstructured.Unstructured{&overlaidBothLabelsRetriesVs, &otherVs}
	assert.ElementsMatch(t, expectedResult, result.FlattenConfig())
}

var (
	retriesOverlaidVs = unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "VirtualService",
			"metadata": map[string]interface{}{
				"name": "test-vs-1",
			},
			"spec": map[string]interface{}{
				"http": []interface{}{
					map[string]interface{}{
						"name":    "test-route-1",
						"retries": "11",
					},
				},
			},
		},
	}

	retries200Bytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []interface{}{
				map[string]interface{}{
					"name":    "test-route-1",
					"retries": "200",
				},
			},
		},
	})
	retries200Overlay = v1alpha1.Overlay{
		Name: "test-vs-1",
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: retries200Bytes,
		},
	}

	retries11Bytes, _ = json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []interface{}{
				map[string]interface{}{
					"name":    "test-route-1",
					"retries": "11",
				},
			},
		},
	})

	retries11Overlay = v1alpha1.Overlay{
		Name: "test-vs-1",
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: retries11Bytes,
		},
	}
)

// If two overlays modifying the same property, the last one wins.
// Overlay application order must not change during application.
func TestApplyMultipleOverlaysInSingleMop_SamePropertyUpdate(t *testing.T) {
	result, err := ApplyOverlays(generatedConfig, map[string][]v1alpha1.Overlay{"test-mop": {matchingOverlayWithVSName, retries200Overlay, retries11Overlay}})

	assert.Nil(t, err)

	expectedResult := []*unstructured.Unstructured{&retriesOverlaidVs, &otherVs}
	assert.ElementsMatch(t, expectedResult, result.FlattenConfig())
}

func TestApplyOverlaysMultipleMops(t *testing.T) {
	overlayErr := "no matching route found to apply overlay"
	testCases := []struct {
		name              string
		mopsToOverlays    map[string][]v1alpha1.Overlay
		expectedResult    []*unstructured.Unstructured
		expectedMopErrors map[string]*meshoperrors.OverlayErrorInfo
	}{
		{
			name:           "Two good mops",
			mopsToOverlays: map[string][]v1alpha1.Overlay{"mop1": {matchingOverlayWithVSName}, "mop2": {overlay1, overlay2}},
			expectedResult: []*unstructured.Unstructured{&overlaidBothLabelsRetriesVs, &otherVs},
		},
		{
			name:              "Good then bad mops",
			mopsToOverlays:    map[string][]v1alpha1.Overlay{"mop1": {matchingOverlayWithVSName}, "mop2": {nonMatchingOverlay}},
			expectedResult:    []*unstructured.Unstructured{&overlaidRetriesVs, &otherVs},
			expectedMopErrors: map[string]*meshoperrors.OverlayErrorInfo{"mop2": {0, overlayErr}},
		},
		{
			name:              "Good then bad then good mops",
			mopsToOverlays:    map[string][]v1alpha1.Overlay{"mop1": {overlay1}, "mop2": {nonMatchingOverlay}, "mop3": {overlay2}},
			expectedResult:    []*unstructured.Unstructured{&overlaidBothLabelsVs, &otherVs},
			expectedMopErrors: map[string]*meshoperrors.OverlayErrorInfo{"mop2": {0, overlayErr}},
		},
		{
			name:              "Bad then good mops",
			mopsToOverlays:    map[string][]v1alpha1.Overlay{"mop1": {nonMatchingOverlay}, "mop2": {overlay1, overlay2}},
			expectedResult:    []*unstructured.Unstructured{&overlaidBothLabelsVs, &otherVs},
			expectedMopErrors: map[string]*meshoperrors.OverlayErrorInfo{"mop1": {0, overlayErr}},
		},
		{
			name:           "Two bad mops",
			mopsToOverlays: map[string][]v1alpha1.Overlay{"mop1": {nonMatchingOverlay}, "mop2": {nonMatchingNameWithPortOverlay}},
			expectedResult: []*unstructured.Unstructured{&testVs, &otherVs},
			expectedMopErrors: map[string]*meshoperrors.OverlayErrorInfo{
				"mop1": {0, overlayErr},
				"mop2": {0, overlayErr},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ApplyOverlays(generatedConfig, tc.mopsToOverlays)

			if len(tc.expectedMopErrors) > 0 {
				assert.NotNil(t, err)
				overlayErrImpl, isImpl := err.(*meshoperrors.OverlayingErrorImpl)
				assert.True(t, isImpl)
				assert.Equal(t, len(tc.expectedMopErrors), len(overlayErrImpl.ErrorMap))
				assert.True(t, reflect.DeepEqual(tc.expectedMopErrors, overlayErrImpl.ErrorMap))
			} else {
				assert.Nil(t, err)
			}

			assert.ElementsMatch(t, tc.expectedResult, result.FlattenConfig())
		})
	}
}

func TestOverlaysAppliedByKind(t *testing.T) {
	_, err := ApplyOverlays(generatedConfig, map[string][]v1alpha1.Overlay{"test-mop": {drOverlay}})

	assert.EqualError(t, err, getOverlayNoMatchingRouteErrorMessage(0), "DR overlay must not be applied to VS")
}

func TestApplyOverlayWhenVsNameIsNotGiven(t *testing.T) {
	virtualservice1 := buildCustomizedVirtualService("testvs1-7443", "http", "default", "1s", 0, 0, "")
	virtualservice2 := buildCustomizedVirtualService("testvs1-15372", "http", "default", "1s", 0, 0, "")
	vs3 := buildCustomizedVirtualService("testvs", "http", "http-7443", "1s", 0, 0, "")
	overlaidVirtualService1 := buildCustomizedVirtualService("testvs1-7443", "http", "default", "10s", 0, 0, "")
	overlaidVirtualService2 := buildCustomizedVirtualService("testvs1-15372", "http", "default", "10s", 0, 0, "")

	configTemplateType := "test-template"

	testCases := []struct {
		name            string
		generatedConfig *GeneratedConfig
		overlays        []v1alpha1.Overlay
		expectedResult  *GeneratedConfig
		expectedError   string
	}{
		{
			name:            "RouteMatchFoundInOneVs",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{virtualservice1, vs3}, configTemplateType),
			expectedResult:  UnflattenConfig([]*unstructured.Unstructured{overlaidVirtualService1, vs3}, generatedConfig.TemplateType),
			overlays:        []v1alpha1.Overlay{overlayWithNoNameWithTimeout},
		},
		{
			name:            "RouteMatchFoundInMultipleVs",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{virtualservice1, virtualservice2}, configTemplateType),
			expectedResult:  UnflattenConfig([]*unstructured.Unstructured{overlaidVirtualService1, overlaidVirtualService2}, generatedConfig.TemplateType),
			overlays:        []v1alpha1.Overlay{overlayWithNoNameWithTimeout},
		},
		{
			name:            "NoMatchFound",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{vs3}, configTemplateType),
			expectedResult:  nil,
			overlays:        []v1alpha1.Overlay{overlayWithNoNameWithTimeout},
			expectedError:   getOverlayNoMatchingRouteErrorMessage(0),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			overlayMap := map[string][]v1alpha1.Overlay{
				"test-mop": tc.overlays,
			}
			result, err := ApplyOverlays(tc.generatedConfig, overlayMap)

			if tc.expectedError != "" {
				assert.EqualError(t, err, tc.expectedError)
				assert.Equal(t, tc.generatedConfig, result)
			} else {
				assert.Nil(t, err)
				assert.ElementsMatch(t, tc.expectedResult.FlattenConfig(), result.FlattenConfig())
			}
		})
	}
}

func TestApplyOverlayMatchByPort(t *testing.T) {
	virtualservice1 := buildCustomizedVirtualService("testvs1-7443", "http", "default", "1s", 0, 123, "")
	virtualservice2 := buildCustomizedVirtualService("testvs1-15372", "http", "default", "1s", 0, 0, "")
	vs3 := buildCustomizedVirtualService("testvs", "http", "default", "1s", 0, 0, "")
	overlaidVirtualService1 := buildCustomizedVirtualService("testvs1-7443", "http", "default", "10s", 0, 123, "")

	configTemplateType := "test-template"

	testCases := []struct {
		name            string
		generatedConfig *GeneratedConfig
		overlays        []v1alpha1.Overlay
		expectedResult  *GeneratedConfig
		expectedError   string
	}{
		{
			name:            "RouteMatchFoundInVs",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{virtualservice1}, configTemplateType),
			expectedResult:  UnflattenConfig([]*unstructured.Unstructured{overlaidVirtualService1}, generatedConfig.TemplateType),
			overlays:        []v1alpha1.Overlay{matchingOverlayWithPort1},
		},
		{
			name:            "RouteMismatch",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{virtualservice2}, configTemplateType),
			expectedResult:  nil,
			overlays:        []v1alpha1.Overlay{matchingOverlayWithPort1},
			expectedError:   getOverlayNoMatchingRouteErrorMessage(0),
		},
		{
			name:            "NoMatchFound",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{vs3}, configTemplateType),
			expectedResult:  nil,
			overlays:        []v1alpha1.Overlay{noMatchingPortOverlay},
			expectedError:   getOverlayNoMatchingRouteErrorMessage(0),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			overlayMap := map[string][]v1alpha1.Overlay{
				"test-mop": tc.overlays,
			}
			result, err := ApplyOverlays(tc.generatedConfig, overlayMap)

			if tc.expectedError != "" {
				assert.EqualError(t, err, tc.expectedError)
				assert.Equal(t, tc.generatedConfig, result)
			} else {
				assert.Nil(t, err)
				assert.ElementsMatch(t, tc.expectedResult.FlattenConfig(), result.FlattenConfig())
			}
		})
	}
}

func TestApplyOverlayToSingleDelegate(t *testing.T) {
	rootVSWithName := buildCustomizedVirtualService("rootVS", "http", "default", "1s", 0, 0, "delegateVS1")          // rootVS/http/name/
	rootVSWithPort := buildCustomizedVirtualService("rootVS", "http", "", "1s", 0, 123, "delegateVS1")               // rootVS/http//port
	rootVSWithNameAndPort := buildCustomizedVirtualService("rootVS", "http", "default", "1s", 0, 123, "delegateVS1") // rootVS/http/name/port

	delegateVS1 := buildCustomizedVirtualService("delegateVS1", "http", "", "1s", 0, 0, "") // delegateVS

	overlaidDelegateVS1 := buildCustomizedVirtualService("delegateVS1", "http", "", "10s", 0, 0, "")

	configTemplateType := "test-template"

	testCases := []struct {
		name            string
		generatedConfig *GeneratedConfig
		overlays        []v1alpha1.Overlay
		expectedResult  *GeneratedConfig
		expectedError   string
	}{
		{
			name:            "DelegateWithName",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{rootVSWithName, delegateVS1}, configTemplateType),
			expectedResult:  UnflattenConfig([]*unstructured.Unstructured{rootVSWithName, overlaidDelegateVS1}, generatedConfig.TemplateType),
			overlays:        []v1alpha1.Overlay{overlayWithNoNameWithTimeout},
		},
		{
			name:            "DelegateWithNameFail",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{rootVSWithName, delegateVS1}, configTemplateType),
			overlays:        []v1alpha1.Overlay{matchingOverlayNoVsNameWithRetries},
			expectedError:   getOverlayNoMatchingRouteErrorMessage(0),
		},
		{
			name:            "DelegateWithPort",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{rootVSWithPort, delegateVS1}, configTemplateType),
			expectedResult:  UnflattenConfig([]*unstructured.Unstructured{rootVSWithPort, overlaidDelegateVS1}, generatedConfig.TemplateType),
			overlays:        []v1alpha1.Overlay{matchingOverlayWithPort1},
		},
		{
			name:            "DelegateWithPortFail",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{rootVSWithPort, delegateVS1}, configTemplateType),
			overlays:        []v1alpha1.Overlay{matchingOverlayNoVsNameWithRetries},
			expectedError:   getOverlayNoMatchingRouteErrorMessage(0),
		},
		{
			name:            "DelegateWithNameAndPort",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{rootVSWithNameAndPort, delegateVS1}, configTemplateType),
			expectedResult:  UnflattenConfig([]*unstructured.Unstructured{rootVSWithNameAndPort, overlaidDelegateVS1}, generatedConfig.TemplateType),
			overlays:        []v1alpha1.Overlay{matchingNameAndPortOverlay},
		},
		{
			name:            "DelegateWithNameAndPortFailByPort",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{rootVSWithNameAndPort, delegateVS1}, configTemplateType),
			overlays:        []v1alpha1.Overlay{nonMatchingPortWithNameOverlay},
			expectedError:   getOverlayNoMatchingRouteErrorMessage(0),
		},
		{
			name:            "DelegateWithNameAndPortFailByName",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{rootVSWithNameAndPort, delegateVS1}, configTemplateType),
			overlays:        []v1alpha1.Overlay{nonMatchingNameWithPortOverlay},
			expectedError:   getOverlayNoMatchingRouteErrorMessage(0),
		},
		{ // No-Op patch based on AND condition in Overlay. (overlay: /http/name/port; baseVS: /http//port)
			name:            "DelegateWithNameAndPortNoMatch",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{rootVSWithPort, delegateVS1}, configTemplateType),
			expectedError:   getOverlayNoMatchingRouteErrorMessage(0),
			overlays:        []v1alpha1.Overlay{matchingNameAndPortOverlay},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			overlayMap := map[string][]v1alpha1.Overlay{
				"test-mop": tc.overlays,
			}
			result, err := ApplyOverlays(tc.generatedConfig, overlayMap)

			if tc.expectedError != "" {
				assert.EqualError(t, err, tc.expectedError)
				assert.Equal(t, tc.generatedConfig, result)
			} else {
				assert.Nil(t, err)
				assert.ElementsMatch(t, tc.expectedResult.FlattenConfig(), result.FlattenConfig())
			}
		})
	}
}

func TestApplyOverlayToMultipleDelegatesUnOrdered(t *testing.T) {
	rootVSWithTwoRoutesWithNames := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "VirtualService",
			"metadata": map[string]interface{}{
				"name": "test-vs-1",
			},
			"spec": map[string]interface{}{
				"http": buildRoutesWithNameAndDelegate("delegateVS1", "delegateVS2", "test-route-1", "default"),
			},
		},
	}
	rootVSWithTwoRoutesWithPorts := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "VirtualService",
			"metadata": map[string]interface{}{
				"name": "test-vs-1",
			},
			"spec": map[string]interface{}{
				"http": buildRoutesWithPortAndDelegate("delegateVS3", "delegateVS4", 123, 321),
			},
		},
	}

	delegateVS1 := buildCustomizedDelegateRoutes("delegateVS1", "http", "", "1s", 0, 0, "")
	delegateVS2 := buildCustomizedDelegateRoutes("delegateVS2", "http", "", "1s", 0, 0, "")

	delegateVS3 := buildCustomizedDelegateRoutes("delegateVS3", "http", "", "1s", 0, 0, "")
	delegateVS4 := buildCustomizedDelegateRoutes("delegateVS4", "http", "", "1s", 0, 0, "")

	overlaidDelegateVS1 := buildCustomizedDelegateRoutes("delegateVS1", "http", "", "1s", 100, 0, "")
	overlaidDelegateVS2 := buildCustomizedDelegateRoutes("delegateVS2", "http", "", "10s", 0, 0, "")

	overlaidDelegateVS3 := buildCustomizedDelegateRoutes("delegateVS3", "http", "", "10s", 0, 0, "")
	overlaidDelegateVS4 := buildCustomizedDelegateRoutes("delegateVS4", "http", "", "10s", 0, 0, "")

	configTemplateType := "test-template"

	testCases := []struct {
		name            string
		generatedConfig *GeneratedConfig
		overlays        []v1alpha1.Overlay
		expectedResult  *GeneratedConfig
		expectedError   string
	}{
		// all of unOrdered VSes tests should expect sortVS to work
		{
			name:            "MatchBothDelegatesWithNames",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{delegateVS1, delegateVS2, rootVSWithTwoRoutesWithNames}, configTemplateType),
			expectedResult:  UnflattenConfig([]*unstructured.Unstructured{rootVSWithTwoRoutesWithNames, overlaidDelegateVS1, overlaidDelegateVS2}, generatedConfig.TemplateType),
			overlays:        []v1alpha1.Overlay{matchingOverlayWithVSName, overlayWithNoNameWithTimeout},
		},
		{
			name:            "MatchBothDelegatesWithPorts",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{delegateVS3, delegateVS4, rootVSWithTwoRoutesWithPorts}, configTemplateType),
			expectedResult:  UnflattenConfig([]*unstructured.Unstructured{rootVSWithTwoRoutesWithPorts, overlaidDelegateVS3, overlaidDelegateVS4}, generatedConfig.TemplateType),
			overlays:        []v1alpha1.Overlay{matchingOverlayWithPort1, matchingOverlayWithPort2},
		},
		{
			name:            "SingleOverlayFiredWithName",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{delegateVS1, delegateVS2, rootVSWithTwoRoutesWithNames}, configTemplateType),
			overlays:        []v1alpha1.Overlay{matchingOverlayWithVSName, nonMatchingPortWithNameOverlay},
			expectedError:   getOverlayNoMatchingRouteErrorMessage(1),
		},
		{
			name:            "SingleOverlayFiredWithPort",
			generatedConfig: UnflattenConfig([]*unstructured.Unstructured{delegateVS3, delegateVS4, rootVSWithTwoRoutesWithPorts}, configTemplateType),
			overlays:        []v1alpha1.Overlay{matchingOverlayWithPort1, noMatchingPortOverlay},
			expectedError:   getOverlayNoMatchingRouteErrorMessage(1),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			overlayMap := map[string][]v1alpha1.Overlay{
				"test-mop": tc.overlays,
			}
			result, err := ApplyOverlays(tc.generatedConfig, overlayMap)

			if tc.expectedError != "" {
				assert.EqualError(t, err, tc.expectedError)
				assert.Equal(t, tc.generatedConfig, result)
			} else {
				assert.Nil(t, err)
				assert.ElementsMatch(t, tc.expectedResult.FlattenConfig(), result.FlattenConfig())
			}
		})
	}
}

func buildCustomizedVirtualService(vsName string, routeType string, routeName string, timeout string, retries int64, port int64, delegateName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "VirtualService",
			"metadata": map[string]interface{}{
				"name": vsName,
			},
			"spec": map[string]interface{}{
				routeType: []interface{}{
					buildRoute(routeName, port, timeout, delegateName, retries),
				},
			},
		},
	}
}

func buildCustomizedDelegateRoutes(vsName string, routeType string, routeName string, timeout string, retries int64, port int64, delegateName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind": "VirtualService",
			"metadata": map[string]interface{}{
				"name": vsName,
			},
			"spec": map[string]interface{}{
				routeType: []interface{}{
					buildRoute(routeName, port, timeout, delegateName, retries),
					buildRoute(routeName, port, timeout, delegateName, retries),
				},
			},
		},
	}
}

func buildRoute(routeName string, port int64, timeout string, delegateName string, retries int64) map[string]interface{} {
	if port > 0 {
		matchAttr := []interface{}{
			map[string]interface{}{
				"port": port,
			},
		}
		return buildRouteWithMatch(routeName, timeout, matchAttr, delegateName, retries)
	}
	return buildRouteWithName(routeName, timeout, delegateName, retries)
}

func buildRouteWithMatch(routeName string, timeout string, matchAttr []interface{}, delegateName string, retries int64) map[string]interface{} {
	routeAsMap := map[string]interface{}{
		"match": matchAttr,
	}
	populateRouteFields(routeName, timeout, delegateName, retries, routeAsMap)
	return routeAsMap
}

func buildRouteWithName(routeName string, timeout string, delegateName string, retries int64) map[string]interface{} {
	routeAsMap := make(map[string]interface{})
	populateRouteFields(routeName, timeout, delegateName, retries, routeAsMap)
	return routeAsMap
}

func populateRouteFields(routeName string, timeout string, delegateName string, retries int64, routeAsMap map[string]interface{}) {
	if routeName != "" {
		routeAsMap["name"] = routeName
	}
	if timeout != "" {
		routeAsMap["timeout"] = timeout
	}
	if delegateName != "" {
		buildDelegateField(delegateName, routeAsMap)
	}
	if retries > 0 {
		routeAsMap["retries"] = fmt.Sprintf("%d", retries)
	}
}

func buildDelegateField(delegateName string, routeAsMap map[string]interface{}) {
	routeAsMap["delegate"] = map[string]interface{}{
		"name":      delegateName,
		"namespace": "default",
	}
}

func buildRoutesWithNameAndDelegate(delegateName1 string, delegateName2 string, routeName1 string, routeName2 string) []interface{} {
	routes := []interface{}{
		map[string]interface{}{
			"name": routeName1,
			"delegate": map[string]interface{}{
				"name":      delegateName1,
				"namespace": "defaultNS",
			},
		},
		map[string]interface{}{
			"name": routeName2,
			"delegate": map[string]interface{}{
				"name":      delegateName2,
				"namespace": "defaultNS",
			},
		},
	}
	return routes
}

func buildRoutesWithPortAndDelegate(delegateName1 string, delegateName2 string, port1 int64, port2 int64) []interface{} {
	routes := []interface{}{
		map[string]interface{}{
			"match": []interface{}{
				map[string]interface{}{
					"port": port1,
				},
			},
			"delegate": map[string]interface{}{
				"name":      delegateName1,
				"namespace": "defaultNS",
			},
		},
		map[string]interface{}{
			"match": []interface{}{
				map[string]interface{}{
					"port": port2,
				},
			},
			"delegate": map[string]interface{}{
				"name":      delegateName2,
				"namespace": "defaultNS",
			},
		},
	}
	return routes
}
