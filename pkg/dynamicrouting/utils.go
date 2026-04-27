package dynamicrouting

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func IsDynamicRoutingEnabled(obj interface{}) bool {
	metaObject := common.GetMetaObject(obj)

	dynamicRoutingLabels := common.GetAnnotationOrAlias(constants.DynamicRoutingLabels, metaObject.GetAnnotations())
	return dynamicRoutingLabels != ""
}

func ContainsDynamicRoutingServiceLabel(obj interface{}) bool {
	metaObject := common.GetMetaObject(obj)
	labelValue := common.GetLabelOrAlias(constants.DynamicRoutingServiceLabel, metaObject.GetLabels())
	return labelValue != ""
}

func getLabelCombinations(service metav1.Object, labelsPerSet []map[string]string) map[string]map[string]string {
	subsetLabelKeys := common.GetAnnotationOrAlias(constants.DynamicRoutingLabels, service.GetAnnotations())
	// create a slice of all deployment/sts labels
	if len(labelsPerSet) > 0 {
		return GetUniqueLabelSubsets(strings.Split(subsetLabelKeys, ","), labelsPerSet)
	}
	return nil
}

func GetLabelCombinationsForDeployments(service metav1.Object, deployments []*appsv1.Deployment) map[string]map[string]string {
	if len(deployments) > 0 {
		var labelsPerDeployment []map[string]string
		for _, d := range deployments {
			if d.Spec.Template.Labels == nil {
				continue
			}
			labelsPerDeployment = append(labelsPerDeployment, d.Spec.Template.Labels)
		}
		return getLabelCombinations(service, labelsPerDeployment)
	}
	return nil
}

func GetLabelCombinationsForRollouts(service metav1.Object, rollouts []*unstructured.Unstructured) map[string]map[string]string {
	if len(rollouts) > 0 {
		var labelsPerRollout []map[string]string
		for _, rollout := range rollouts {
			labels, _, _ := unstructured.NestedMap(rollout.Object, "spec", "template", "metadata", "labels")
			if labels == nil {
				continue
			}
			rolloutLabels := make(map[string]string)
			for k, v := range labels {
				rolloutLabels[k] = v.(string)
			}
			labelsPerRollout = append(labelsPerRollout, rolloutLabels)
		}
		return getLabelCombinations(service, labelsPerRollout)
	}
	return nil
}

func GetLabelCombinationsForSTS(service metav1.Object, statefulsets []*appsv1.StatefulSet) map[string]map[string]string {
	if len(statefulsets) > 0 {
		var labelsPerSts []map[string]string
		for _, sts := range statefulsets {
			if sts.Spec.Template.Labels == nil {
				continue
			}
			labelsPerSts = append(labelsPerSts, sts.Spec.Template.Labels)
		}
		return getLabelCombinations(service, labelsPerSts)
	}
	return nil
}

func GetLabelCombinationsByKind(deploymentLabelCombinations map[string]map[string]string, rolloutLabelCombinations map[string]map[string]string) map[string]map[string]map[string]string {
	labelCombinationsByKindIndex := make(map[string]map[string]map[string]string)
	if len(deploymentLabelCombinations) > 0 {
		labelCombinationsByKindIndex["Deployment"] = deploymentLabelCombinations
	}
	if len(rolloutLabelCombinations) > 0 {
		labelCombinationsByKindIndex["Rollout"] = rolloutLabelCombinations
	}
	return labelCombinationsByKindIndex
}
