package dynamicrouting

import (
	"fmt"
	"reflect"
	"strings"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	meshOpErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// GetUniqueLabelSubsets returns a json string where key is a unique combination of all subset label values and value is the
// corresponding key-value pair {labelKey: labelValue} that is used to represent the key.
func GetUniqueLabelSubsets(subsetLabelKeys []string, podTemplateLabels []map[string]string) map[string]map[string]string {
	// key is unique subset label value concatenated with '-'. Eg : v1-u1
	// value is a map that is used to represent above key {version: v1, user: u1}, where version,user
	// are the keys that are defined in subsetLabelKeys
	uniqueSubsets := make(map[string]map[string]string)
	for _, podTemplateLabel := range podTemplateLabels {
		partialMatch := false
		uniqueKey := ""
		uniqueValue := map[string]string{}
		//skipSeparator := true
		for _, subsetLabelKey := range subsetLabelKeys {

			if val, ok := podTemplateLabel[subsetLabelKey]; ok {
				uniqueKey += val + "-"
				uniqueValue[subsetLabelKey] = val
			} else {
				partialMatch = true
				break
			}
		}
		// add unique subset only if deployment labels match with subsetLabel definition
		if !partialMatch {
			// trim any trailing dashes
			uniqueKey = strings.TrimSuffix(uniqueKey, "-")
			uniqueSubsets[uniqueKey] = uniqueValue
		}

	}
	return uniqueSubsets
}

func GetDefaultSubsetName(labelCombinationsByKindIndex map[string]map[string]map[string]string, serviceMetaObject metav1.Object, ctxLogger *zap.SugaredLogger, metricsRegistry *prometheus.Registry) (string, error) {
	metricsLabel := map[string]string{
		metrics.ServiceNameTag: serviceMetaObject.GetName(),
		metrics.NamespaceTag:   serviceMetaObject.GetNamespace(),
	}

	val := common.GetAnnotationOrAlias(constants.DynamicRoutingDefaultCoordinates, serviceMetaObject.GetAnnotations())
	if val != "" {
		defaultCoordinatesMap := make(map[string]string)
		for _, kvString := range strings.Split(val, ",") {
			kvPair := strings.Split(kvString, "=")
			if len(kvPair) != 2 || kvPair[0] == "" || kvPair[1] == "" {
				metricsLabel[metrics.ReasonTag] = "malformed_default_coordinates"
				metrics.GetOrRegisterCounterWithLabels(metrics.DynamicRoutingFailedMetrics, metricsLabel, metricsRegistry).Inc()
				return "", &meshOpErrors.UserConfigError{
					Message: fmt.Sprintf("malformed default coordinates. expected k=v format, found %s for %s/%s", kvString, serviceMetaObject.GetNamespace(), serviceMetaObject.GetName()),
				}
			}
			defaultCoordinatesMap[kvPair[0]] = kvPair[1]
		}
		defaultSubsetName := ""
		for _, labelCombinations := range labelCombinationsByKindIndex {
			for subsetName, subsetValues := range labelCombinations {
				if reflect.DeepEqual(defaultCoordinatesMap, subsetValues) {
					ctxLogger.Debugf("found default subset %s for %s/%s", subsetName, serviceMetaObject.GetNamespace(), serviceMetaObject.GetName())
					defaultSubsetName = subsetName
					break
				}
			}
		}

		if defaultSubsetName == "" {
			metricsLabel[metrics.ReasonTag] = "default_coordinates_not_found"
			metrics.GetOrRegisterCounterWithLabels(metrics.DynamicRoutingFailedMetrics, metricsLabel, metricsRegistry).Inc()
			return "", &meshOpErrors.UserConfigError{
				Message: fmt.Sprintf("could not find subset matching default %v for %s/%s", val, serviceMetaObject.GetNamespace(), serviceMetaObject.GetName()),
			}
		}
		return defaultSubsetName, nil
	}
	return "", nil
}

// GetMetadataIfDynamicRoutingEnabled returns dynamic routing related metadata (includes subset key name) if object is dynamic routing enabled
func GetMetadataIfDynamicRoutingEnabled(object *unstructured.Unstructured, service metav1.Object, logger *zap.SugaredLogger, metricsRegistry *prometheus.Registry) map[string]string {
	metadata := make(map[string]string)
	if !IsDynamicRoutingEnabled(service) {
		return nil
	}

	labels, _, _ := unstructured.NestedMap(object.Object, "spec", "template", "metadata", "labels")
	if len(labels) == 0 {
		logger.Warnf("no subset found for dynamic routing enabled object: %s/%s", object.GetNamespace(), service.GetName())
		return nil
	}
	podTemplateLabels := make(map[string]string)
	for k, v := range labels {
		podTemplateLabels[k] = v.(string)
	}

	subsetLabelCombinations := getLabelCombinations(service, []map[string]string{podTemplateLabels})
	for subsetKey := range subsetLabelCombinations {
		metadata[constants.ContextDynamicRoutingSubsetName] = subsetKey
		labelCombinations := map[string]map[string]map[string]string{"Rollout": subsetLabelCombinations}
		defaultSubsetName, _ := GetDefaultSubsetName(labelCombinations, service, logger, metricsRegistry)
		if defaultSubsetName != "" {
			metadata[constants.ContextDynamicRoutingDefaultSubset] = defaultSubsetName
		}
		return metadata
	}
	return nil
}
