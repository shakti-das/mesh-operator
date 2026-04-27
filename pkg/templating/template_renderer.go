package templating

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/exp/maps"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	meshOpErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/google/go-jsonnet"
	"go.uber.org/zap"
)

type RenderRequestContext struct {
	Object            *unstructured.Unstructured
	MeshOperator      *v1alpha1.MeshOperator
	Metadata          map[string]string
	OwnerRef          *metav1.OwnerReference
	ClusterName       string
	TemplateOverride  string
	MopOverlays       map[string][]v1alpha1.Overlay
	AdditionalObjects map[string]*AdditionalObjects // key - extension type, value - additional object for this extension type for the service
	policies          map[string]*unstructured.Unstructured
}

type AdditionalObjects struct {
	Singletons map[string]interface{} `json:"singletons"` // key - ContextKey as in rendering-config file, value singleton object found
}

func NewRenderRequestContext(
	objectToRender *unstructured.Unstructured,
	metadata map[string]string,
	ownerRef *metav1.OwnerReference,
	clusterName string,
	templateOverride string,
	mopNameToOverlays map[string][]v1alpha1.Overlay,
	policies map[string]*unstructured.Unstructured) RenderRequestContext {
	return RenderRequestContext{
		Object:           objectToRender,
		Metadata:         metadata,
		OwnerRef:         ownerRef,
		ClusterName:      clusterName,
		TemplateOverride: templateOverride,
		MopOverlays:      mopNameToOverlays,
		policies:         policies,
	}
}

func NewMOPRenderRequestContext(
	objectToRender *unstructured.Unstructured,
	meshOperator *v1alpha1.MeshOperator,
	metadata map[string]string,
	clusterName string,
	mopOwnerRef *metav1.OwnerReference,
	additionalObjects map[string]*AdditionalObjects) RenderRequestContext {
	return RenderRequestContext{
		Object:            objectToRender,
		MeshOperator:      meshOperator,
		Metadata:          metadata,
		ClusterName:       clusterName,
		OwnerRef:          mopOwnerRef,
		AdditionalObjects: additionalObjects,
	}
}

// TemplateRenderer defines the interface for the kubernetes resource template renderer.
type TemplateRenderer interface {
	Render(context *RenderRequestContext) (*GeneratedConfig, error)
}

type templateRenderer struct {
	templatePaths    []string
	logger           *zap.SugaredLogger
	templateSelector *TemplateSelector
}

func NewTemplateRenderer(
	logger *zap.SugaredLogger,
	templatePaths []string,
	templateSelector *TemplateSelector) TemplateRenderer {

	return &templateRenderer{
		templatePaths:    templatePaths,
		logger:           logger,
		templateSelector: templateSelector,
	}
}

// Fetches the requested template set from the underlying store, and renders each of the templates.
func (tr *templateRenderer) Render(context *RenderRequestContext) (*GeneratedConfig, error) {
	if context == nil {
		return nil, fmt.Errorf("request context must not be nil")
	}

	// for Mop context - service object is optional
	if context.MeshOperator == nil && context.Object == nil {
		return nil, fmt.Errorf("both MOP and object cannot be nil, either one of them needs to be passed")
	}

	var renderResults *GeneratedConfig
	var err error
	if context.MeshOperator == nil {
		renderResults, err = tr.renderObject(context)
	} else {
		renderResults, err = tr.renderMeshOperator(context)
	}

	if err != nil {
		return nil, err
	}

	return renderResults, nil
}

// renderObject renders objects such as Service, ServiceEntry
func (tr *templateRenderer) renderObject(context *RenderRequestContext) (*GeneratedConfig, error) {
	object := context.Object

	err := validateObject(object)
	if err != nil {
		return NewGeneratedConfig(nil, "", []TemplateMessage{}), err
	}

	// disable owner Reference generation
	object.SetUID("")

	namespace, name, err := tr.templateSelector.GetTemplate(context)
	if err != nil {
		return nil, fmt.Errorf("failed to determine template set: %w", err)
	}

	combinedTemplates := make(map[string]string)
	baseTemplateType := tr.addServiceTemplates(context, namespace, name, combinedTemplates, object.GetKind())

	vm, err := tr.getVM(context)
	if err != nil {
		return nil, err
	}
	renderedTmpls := make(map[string][]*unstructured.Unstructured, len(combinedTemplates))
	for name, tmpl := range combinedTemplates {
		renderedTmpl, err := vm.EvaluateAnonymousSnippet(name, tmpl)
		if err != nil {
			return nil, fmt.Errorf("failed to render template(%s) for %s object %s/%s, templateType: %s, error: %v", name, object.GetKind(), object.GetNamespace(), object.GetName(), baseTemplateType, err)
		}

		unstructObjects, err := renderOutputToUnstructuredList(renderedTmpl)
		if err != nil {
			return nil, fmt.Errorf("failed to extract objects from template %s: %w", name, err)
		}

		renderedTmpls[name] = unstructObjects
	}

	return NewGeneratedConfig(renderedTmpls, baseTemplateType, []TemplateMessage{}), nil
}

func (tr *templateRenderer) renderMeshOperator(context *RenderRequestContext) (*GeneratedConfig, error) {
	mop := context.MeshOperator
	result := map[string][]*unstructured.Unstructured{}

	collector := NewMessageCollector()

	for filterIndex, extension := range mop.Spec.Extensions {
		extensionFieldName, unstructuredObjects, err := tr.renderExtension(context, extension, collector)
		if err != nil {
			return nil, err
		}

		if unstructuredObjects == nil {
			return NewGeneratedConfig(result, "", collector.Messages()), nil
		}

		configNsExtension, configNamespace := IsConfigNamespaceExtension(extensionFieldName)
		for objectIdx, obj := range unstructuredObjects {
			objectName := fmt.Sprintf("filter-%d", filterIndex)
			if objectIdx > 0 {
				// Use two-parts naming schema only for cases, where extension generates more than one object
				objectName = fmt.Sprintf("filter-%d.%d", filterIndex, objectIdx)
			}
			obj.SetName(objectName)
			if configNsExtension {
				if obj.GetAnnotations() == nil {
					obj.SetAnnotations(map[string]string{})
				}
				annotations := obj.GetAnnotations()
				annotations[constants.ConfigNamespaceExtensionAnnotation] = configNamespace
				obj.SetAnnotations(annotations)
			}
		}
		result[strconv.Itoa(filterIndex)] = unstructuredObjects
	}

	return NewGeneratedConfig(result, "", collector.Messages()), nil
}

func (tr *templateRenderer) renderExtension(context *RenderRequestContext, extension v1alpha1.ExtensionElement, collector *MessageCollector) (string, []*unstructured.Unstructured, error) {
	extensionType, extensionFieldName, err := getExtensionTypeAndFieldName(extension)
	if err != nil {
		return "", nil, err
	}
	var templatePrefix, objectKind string

	if context.Object != nil {
		objectKind = context.Object.GetKind()
	}

	// For ServiceEntries extensions - templates are prefixed with `se_`
	// If you're here to add another object/prefix, please replace the if/else chain with a hash-map lookup
	if context.Object != nil && objectKind == constants.ServiceEntryKind.Kind {
		templatePrefix = "filters" + constants.TemplateKeyDelimiter + constants.ServiceEntryExtensionTemplatePrefix + constants.TemplateKeyDelimiter + strings.ToLower(extensionType)
	} else {
		templatePrefix = "filters" + constants.TemplateKeyDelimiter + strings.ToLower(extensionType)
	}
	templates := tr.templateSelector.manager.GetTemplatesByPrefix(templatePrefix)

	if len(templates) == 0 {
		if context.Object == nil || objectKind == constants.ServiceKind.Kind {
			return "", nil, fmt.Errorf("no templates provided for filter type %s for kind: %s", extensionType, objectKind)
		} else {
			// Handle the case, where extension is not supported for a specific kind gracefully.
			// This is being done for backward compatibility for cases, where MOP.serviceSelector is not properly crafted and picks up other objects.
			return "", nil, nil
		}
	}

	result := make([]*unstructured.Unstructured, 0)

	// Sort templates, to have a repeatable application order
	templateNames := maps.Keys(templates)
	slices.Sort(templateNames)

	for _, templateName := range templateNames {
		collector.SetCurrentTemplate(templateName)
		vm, err := tr.getVMForMop(context, extension, collector)
		if err != nil {
			return "", nil, fmt.Errorf("error creating VM for template %s: %w", templateName, err)
		}
		renderedTmpl, err := vm.EvaluateAnonymousSnippet(templateName, templates[templateName])
		if err != nil {
			return "", nil, fmt.Errorf("error rendering template %s: %w", templateName, err)
		}
		renderedObjects, err := renderOutputToUnstructuredList(renderedTmpl)
		if err != nil {
			return "", nil, fmt.Errorf("failed to extract objects from extension template %s: %w", templateName, err)
		}
		result = append(result, renderedObjects...)
	}
	return extensionFieldName, result, nil
}

func (tr *templateRenderer) addServiceTemplates(
	context *RenderRequestContext, namespace, name string,
	combinedTemplates map[string]string, kind string) string {
	templates, templateType := tr.templateSelector.GetTemplatesOrDefault(context, namespace, name, kind)
	for k, v := range templates {
		combinedTemplates[k] = v
	}
	return templateType
}

func (tr *templateRenderer) getVM(context *RenderRequestContext) (*jsonnet.VM, error) {
	vm := jsonnet.MakeVM()
	vm.Importer(&jsonnet.FileImporter{
		JPaths: tr.templatePaths,
	})

	for policyName, policyObject := range context.policies {
		policyBytes, err := json.Marshal(policyObject)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal policy %s: %w", policyName, err)
		}

		context.Metadata[policyName] = string(policyBytes)
	}

	err := setVmArgsForObjectKind(vm, context.Object, context.Metadata)
	if err != nil {
		return nil, fmt.Errorf("encountered error while setting jsonnet VM arg for object kind, error: %w", err)
	}

	return vm, nil
}

func setVmArgsForObjectKind(vm *jsonnet.VM, object *unstructured.Unstructured, metadata map[string]string) error {
	if metadata == nil {
		metadata = map[string]string{}
	}

	objectJSON, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("failed to serialize service: %w", err)
	}
	switch object.GetKind() {
	case constants.ServiceKind.Kind:
		vm.TLACode("service", string(objectJSON))
		externalName, _, _ := unstructured.NestedString(object.Object, "spec", "externalName")
		parseIP := net.ParseIP(externalName)
		// externalNameIsIpAddress equals to "true" if service.ExternalName is an ip address, otherwise "false"
		vm.ExtVar("externalNameIsIpAddress", strconv.FormatBool(parseIP != nil))
	case constants.ServiceEntryKind.Kind:
		vm.TLACode("serviceentry", string(objectJSON))
		// if metadata doesn't contain maxConnectionDurationFilter key, then use feature flag value
		if _, ok := metadata["enableMaxConnectionDurationFilter"]; !ok {
			metadata["enableMaxConnectionDurationFilter"] = strconv.FormatBool(features.EnableMaxConnectionDurationFilter)
		}
	case constants.KnativeIngressKind.Kind:
		vm.TLACode("ingress", string(objectJSON))
	case constants.KnativeServerlessServiceKind.Kind:
		vm.TLACode("serverlessService", string(objectJSON))
	case "TrafficShardingPolicy":
		vm.TLACode("tsp", string(objectJSON))
	default:
		return fmt.Errorf("VM arg not supported for kind: %s", object.GetKind())
	}

	metadataJson, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to serialize context: %w", err)
	}
	vm.TLACode("context", string(metadataJson))

	return nil
}

func (tr *templateRenderer) getVMForMop(context *RenderRequestContext, extension v1alpha1.ExtensionElement, collector *MessageCollector) (*jsonnet.VM, error) {
	var objectJson []byte
	var err error
	var objectKind string
	if context.Object != nil {
		objectKind = context.Object.GetKind()
	}

	if objectKind != "" {
		objectJson, err = json.Marshal(context.Object)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize object: %w", err)
		}
	}

	mopJSON, err := json.Marshal(context.MeshOperator)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize service: %w", err)
	}

	filterJSON, err := json.Marshal(extension)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize filter obj: %w", err)
	}

	extensionTypeName, err := GetExtensionType(extension)
	if err != nil {
		return nil, fmt.Errorf("failed to determine extension type: %w", err)
	}
	extensionContext := buildExtensionContext(context, extensionTypeName)
	contextJson, err := json.Marshal(extensionContext)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize extension context: %w", err)
	}

	vm := jsonnet.MakeVM()
	vm.Importer(&jsonnet.FileImporter{
		JPaths: tr.templatePaths,
	})
	registerMessageFunction(vm, collector)

	vm.TLACode("mop", string(mopJSON))
	vm.TLACode("filter", string(filterJSON))
	if objectKind != "" {
		vm.TLACode(strings.ToLower(objectKind), string(objectJson))
	}
	vm.TLACode("context", string(contextJson))

	return vm, nil
}

func validateObject(object *unstructured.Unstructured) error {
	if object.GetKind() == constants.ServiceKind.Kind {
		// skip validating target port if for LB service type
		svcType, hasType, err := unstructured.NestedString(object.Object, "spec", "type")
		if err != nil {
			return err
		}
		if hasType && svcType == constants.LBServiceType {
			return nil
		}
		// do not generate config if service reference a target port of type `string`
		if targetPortIsString, err := svcReferenceTargetPortOfTypeString(object); err != nil {
			return err
		} else if targetPortIsString {
			return &meshOpErrors.UserConfigError{Message: fmt.Sprintf("unable to generate mesh routing config, please provide exact integer port as targetPort")}
		}
	}
	return nil
}

func svcReferenceTargetPortOfTypeString(service *unstructured.Unstructured) (bool, error) {
	spec, _, err := unstructured.NestedMap(service.Object, "spec")
	if err != nil {
		return false, nil
	}
	ports, _, err := unstructured.NestedSlice(spec, "ports")
	if err != nil {
		return false, err
	}
	for _, port := range ports {
		portAsMap, valid := port.(map[string]interface{})
		if !valid {
			return false, fmt.Errorf("error parsing service port, service: %s/%s", service.GetNamespace(), service.GetName())
		}
		_, targetPortIsString := portAsMap["targetPort"].(string)
		if targetPortIsString {
			return true, nil
		}
	}
	return false, nil
}

func renderOutputToUnstructuredList(renderOutput string) ([]*unstructured.Unstructured, error) {
	rawObjs, err := extractRawObjects(renderOutput)
	if err != nil {
		return nil, fmt.Errorf("failed to extract raw object: %w", err)
	}

	var templateObjects []*unstructured.Unstructured
	for _, rawObj := range rawObjs {
		unstructObject, err := rawObjectToUnstructured(rawObj)
		if err != nil {
			return nil, fmt.Errorf("failed to extract unstructured object: %w", err)
		}
		templateObjects = append(templateObjects, unstructObject)
	}
	return templateObjects, nil
}

func extractRawObjects(renderedTemplate string) ([]json.RawMessage, error) {
	rawObjs := make([]json.RawMessage, 0)
	if err := json.Unmarshal([]byte(renderedTemplate), &rawObjs); err != nil {
		return nil, err
	}
	return rawObjs, nil
}

func rawObjectToUnstructured(rawObj json.RawMessage) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}

	if err := obj.UnmarshalJSON(rawObj); err != nil {
		return nil, err
	}

	return obj, nil
}

func buildExtensionContext(context *RenderRequestContext, extensionType string) map[string]interface{} {
	extensionContext := map[string]interface{}{}
	if context.AdditionalObjects != nil && context.AdditionalObjects[extensionType] != nil {
		extensionContext["additionalObjects"] = map[string]interface{}{
			"singletons": context.AdditionalObjects[extensionType].Singletons,
		}
	}
	return extensionContext
}
