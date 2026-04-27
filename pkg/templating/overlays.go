package templating

import (
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	meshOpErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/patch"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Overlayer applies overlays per mop if it can, and returns overlaid config if at least one mop was successfully applied, and the provided config otherwise.
type Overlayer func(config *GeneratedConfig, mopOverlays map[string][]v1alpha1.Overlay) (*GeneratedConfig, meshOpErrors.OverlayingError)

func DontApplyOverlays(config *GeneratedConfig, _ map[string][]v1alpha1.Overlay) (*GeneratedConfig, meshOpErrors.OverlayingError) {
	return config, nil
}

// ApplyOverlays applies overlays to config from the mop name -> overlay map.
// Returns the config with overlaid changes from all valid mops and errors from any that failed.
func ApplyOverlays(config *GeneratedConfig, mopOverlays map[string][]v1alpha1.Overlay) (*GeneratedConfig, meshOpErrors.OverlayingError) {
	if config == nil || len(config.Config) == 0 {
		return config, nil // nothing to do here
	}

	if !hasOverlaysToApply(mopOverlays) {
		return config, nil // No-Op
	}

	objToTemplateNameMapping := config.GetObjectToTemplateIndex()
	mopOverlayErrorInfos := make(map[string]*meshOpErrors.OverlayErrorInfo)

	configObjs := config.FlattenConfig()

	for mopName, overlays := range mopOverlays {
		var errInfo *meshOpErrors.OverlayErrorInfo
		if len(overlays) > 0 {
			configObjs, errInfo = applyOverlaysForOneMop(configObjs, overlays)
			if errInfo != nil {
				mopOverlayErrorInfos[mopName] = errInfo
			}
		}
	}

	var err meshOpErrors.OverlayingError

	if len(mopOverlayErrorInfos) > 0 {
		err = &meshOpErrors.OverlayingErrorImpl{
			ErrorMap: mopOverlayErrorInfos,
		}
	}

	return UnflattenConfigByTemplate(configObjs, config.TemplateType, objToTemplateNameMapping), err
}

// applyOverlaysForOneMop tries to apply given overlays to the given config objects, and:
// returns the overlaid config when no errors are seen;
// returns the original configObjects along with error info when any error is seen
func applyOverlaysForOneMop(configObjects []*unstructured.Unstructured, overlays []v1alpha1.Overlay) (
	[]*unstructured.Unstructured, *meshOpErrors.OverlayErrorInfo) {

	originalConfigObjects := common.CreateDeepCopyListOfUnstructuredObjects(configObjects)

	var result []*unstructured.Unstructured
	configByKind := BucketizeConfigByKind(configObjects)
	var overlayFired = make([]bool, len(overlays))

	for kind, objects := range configByKind {
		strategy := patch.GetStrategyForKindOrDefault(kind, objects)

		for overlayIdx, overlay := range overlays {
			if !strategy.CanApply(&overlay) {
				continue
			}
			appliedTo, err := strategy.Patch(&overlay)

			if err != nil {
				return originalConfigObjects, &meshOpErrors.OverlayErrorInfo{
					OverlayIndex: overlayIdx,
					Message:      err.Error(),
				}
			}

			if len(appliedTo) > 0 {
				overlayFired[overlayIdx] = true
			}
		}
		result = append(result, strategy.GetObjects()...)
	}

	// fail fast with an error - if an overlay didn't fire
	for overlayIdx, hasOverlayFired := range overlayFired {
		if !hasOverlayFired {
			return originalConfigObjects, &meshOpErrors.OverlayErrorInfo{
				OverlayIndex: overlayIdx,
				Message:      "no matching route found to apply overlay",
			}
		}
	}

	return result, nil
}

func hasOverlaysToApply(mopOverlays map[string][]v1alpha1.Overlay) bool {
	if len(mopOverlays) == 0 {
		return false
	}

	totalNumOverlays := 0
	for _, overlays := range mopOverlays {
		totalNumOverlays += len(overlays)
	}
	if totalNumOverlays == 0 {
		return false
	}

	return true
}
