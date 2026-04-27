package templating

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"go.uber.org/zap"
)

// Applicator provides functionality to apply generated config to the destination
type Applicator interface {
	ApplyConfig(context *RenderRequestContext, generatedConfig *GeneratedConfig, ctxLogger *zap.SugaredLogger) ([]*AppliedConfigObject, error)
}

type ApplicatorFactory func(
	logger *zap.SugaredLogger,
	primaryClient kube.Client,
	dryRun bool) Applicator

// AppliedConfigObject is the result returned by Applicator for one config object
type AppliedConfigObject struct {
	Object *unstructured.Unstructured
	Error  error
}

// GetConfigObjects extracts the actual config object from each AppliedConfigObject
// in the applicatorResult provided to it.
func GetConfigObjects(applicatorResult []*AppliedConfigObject) []*unstructured.Unstructured {
	objs := make([]*unstructured.Unstructured, len(applicatorResult))
	for i, ao := range applicatorResult {
		objs[i] = ao.Object
	}
	return objs
}

func NewConsoleApplicator(
	logger *zap.SugaredLogger) Applicator {
	return &consoleApplicator{
		logger: logger,
	}
}

// consoleApplicator simply applies the rendered templates as logs on console.
type consoleApplicator struct {
	logger *zap.SugaredLogger
}

func (p *consoleApplicator) ApplyConfig(context *RenderRequestContext, generatedConfig *GeneratedConfig, _ *zap.SugaredLogger) (
	[]*AppliedConfigObject, error) {
	if context.Object == nil && context.MeshOperator == nil {
		return nil, fmt.Errorf("renderContext with no service and no mesh-operator passed for render")
	}

	isService := context.MeshOperator == nil
	var mode, targetNs, targetName, output string
	var err error

	if isService {
		mode = "service"
		targetNs = context.Object.GetNamespace()
		targetName = context.Object.GetName()
		output, err = jsonifyServiceOutput(generatedConfig.Config)
	} else {
		mode = "mesh-operator"
		targetNs = context.MeshOperator.GetNamespace()
		targetName = context.MeshOperator.GetName()
		output, err = jsonifyMopRenderedOutput(generatedConfig.Config)
	}

	ctxLogger := p.logger.With(
		"mode", mode,
		"name", targetName,
		"namespace", targetNs)

	if err != nil {
		return nil, fmt.Errorf("failed to log template output: %w", err)
	}
	ctxLogger.Info(output)
	return nil, nil
}

// formatPrinterApplicator formats and prints out the rendered templates
type formatPrinterApplicator struct {
	logger *zap.SugaredLogger
}

func NewFormatPrinterApplicator(logger *zap.SugaredLogger) Applicator {
	return &formatPrinterApplicator{
		logger: logger,
	}
}

func (p *formatPrinterApplicator) ApplyConfig(context *RenderRequestContext,
	generatedConfig *GeneratedConfig, _ *zap.SugaredLogger) ([]*AppliedConfigObject, error) {

	isService := context.MeshOperator == nil
	var output string
	var err error

	if generatedConfig == nil {
		fmt.Println("null")
		return nil, nil
	}

	if isService {
		output, err = jsonifyServiceOutput(generatedConfig.Config)
	} else {
		output, err = jsonifyMopRenderedOutput(generatedConfig.Config)
	}

	if err != nil {
		p.logger.Errorf("encountered error while creating json: %v", err)
		return nil, err
	}

	fmt.Println(output)
	return nil, nil
}

// jsonifyServiceOutput creates an indented json string for the rendered templates.
//
// The templates map's values are json strings (more specifically, string representation of json arrays of objects),
// while the keys are not. So this function creates a new map with the
// values unmarshalled, and then recreates a json representation of the entire map.
func jsonifyServiceOutput(input map[string][]*unstructured.Unstructured) (string, error) {
	keys := make([]string, len(input))
	i := 0
	for k := range input {
		keys[i] = k
		i++
	}
	sort.Strings(keys)

	strippedTemplates := make(map[string]any, len(input))
	for _, key := range keys {
		// get just the object kind for key
		kind := strings.Split(key, TemplateKeyDelimiter)[2]
		if val, exists := input[key]; exists {
			// Sort entries
			sort.Slice(val, func(i, j int) bool {
				return compareConfigObjects(val[i], val[j])
			})
			// Add generated-by
			strippedTemplates[kind] = addGeneratedBy(val, key)
		}
	}

	jsonBytes, err := json.MarshalIndent(strippedTemplates, "", "  ")
	if err != nil {
		return "", err
	}

	// Json marshaler replaces the valid UTF-8 and JSON characters "&". "<", ">" with the "slash u" unicode escaped forms (e.g. \u0026)
	// so the marshaled output need to unescaped explicitly in order to preserve the actual output returned from jsonnet templates
	jsonBytes, err = unescapeUnicodeCharactersInJSON(jsonBytes)
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}

func unescapeUnicodeCharactersInJSON(jsonRaw json.RawMessage) (json.RawMessage, error) {
	str, err := strconv.Unquote(strings.Replace(strconv.Quote(string(jsonRaw)), `\\u`, `\u`, -1))
	if err != nil {
		return nil, err
	}
	return []byte(str), nil
}

// jsonifyMopRenderedOutput - print mop rendered template in indented json format
func jsonifyMopRenderedOutput(input map[string][]*unstructured.Unstructured) (string, error) {
	keys := make([]string, len(input))
	i := 0
	for k := range input {
		keys[i] = k
		i++
	}
	sort.Strings(keys)

	objectsFlattened := make([]*unstructured.Unstructured, 0)
	for _, key := range keys {
		objectsFlattened = append(objectsFlattened, input[key]...)
	}

	jsonBytes, err := json.MarshalIndent(objectsFlattened, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}

type emptyElement struct {
	GeneratedBy string `json:"$generatedBy"`
}

func addGeneratedBy(templateOutput []*unstructured.Unstructured, template string) any {
	if templateOutput == nil || len(templateOutput) == 0 {
		return []emptyElement{
			{
				GeneratedBy: fmt.Sprintf("Empty output from: %s", template),
			},
		}
	}

	var result []*unstructured.Unstructured
	for _, item := range templateOutput {
		labels := item.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels["$generatedBy"] = template
		item.SetLabels(labels)

		result = append(result, item)
	}
	return result
}

func compareConfigObjects(left, right *unstructured.Unstructured) bool {
	leftComparisonString := fmt.Sprintf("%s/%s", left.GetNamespace(), left.GetName())
	rightComparisonString := fmt.Sprintf("%s/%s", right.GetNamespace(), right.GetName())

	return leftComparisonString < rightComparisonString
}
