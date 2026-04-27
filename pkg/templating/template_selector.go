package templating

import (
	"fmt"
	"strings"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"

	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"go.uber.org/zap"
)

type TemplateSelector struct {
	logger                 *zap.SugaredLogger
	manager                TemplatesManager
	templateSelectorLabels []string
	defaultTemplateType    map[string]string
	metricsRegistry        *prometheus.Registry
}

func NewTemplateSelector(
	logger *zap.SugaredLogger,
	manager TemplatesManager,
	templateSelectorLabels []string,
	defaultTemplateType map[string]string,
	metricsRegistry *prometheus.Registry) *TemplateSelector {
	return &TemplateSelector{logger: logger, manager: manager, templateSelectorLabels: templateSelectorLabels, defaultTemplateType: defaultTemplateType, metricsRegistry: metricsRegistry}
}

// The template set name is derived based on the following order of precedence:
// 1. If MOP reference a non empty template override field, use that.
// 2. if ctx.metadata references a specific template type, use that.
// 3. If object contains template config annotation "template.mesh.io/*", template name should be derived from this
// 4. If given, the value specified in the annotation "routing.mesh.io/template" on the Service.
// 5. If given and exists, derive template name via template-selector-labels. The namespace is the Service namespace.
// 6. The object name and namespace.
func (s *TemplateSelector) GetTemplate(ctx *RenderRequestContext) (string, string, error) {
	object := ctx.Object

	if ctx.TemplateOverride != "" {
		namespace, name, err := SplitTemplateName(ctx.TemplateOverride)
		if err != nil {
			return "", "", fmt.Errorf("invalid template override provided %s: %w", ctx.TemplateOverride, err)
		}
		templatePrefix := getTemplateType(namespace, name) + TemplateKeyDelimiter
		if !s.manager.Exists(templatePrefix) {
			return "", "", fmt.Errorf("template override doesn't exist: %s", ctx.TemplateOverride)
		}
		return namespace, name, nil
	}

	// derive template if svc contains templateConfig annotation (template.mesh.io/*)
	if template, err := templateFromConfigAnnotation(object.GetAnnotations()); err != nil {
		return "", "", err
	} else if template != "" {
		annotationFound := common.GetAnnotationOrAlias(constants.TemplateOverrideAnnotation, object.GetAnnotations())
		if annotationFound != "" {
			s.logger.Warnf("found config as well template override annotation in object %s/%s, %s annotation will be used for template selection", object.GetNamespace(), object.GetName(), constants.TemplateOverrideAnnotation)
			commonmetrics.IncrementMetric(commonmetrics.AnnotationConflict, object.GetKind(), common.GetMetaObject(object), s.metricsRegistry)
		}
		s.logger.Debugf("using template config annotation (%s) for service %s/%s", template, object.GetNamespace(), object.GetName())
		return SplitTemplateName(template)
	}

	template := common.GetAnnotationOrAlias(constants.TemplateOverrideAnnotation, object.GetAnnotations())
	if template != "" {
		s.logger.Debugf("using template override from annotation for service %s/%s", object.GetNamespace(), object.GetName())
		return SplitTemplateName(template)
	}

	for _, label := range s.templateSelectorLabels {
		if labelValue := object.GetLabels()[label]; labelValue != "" && s.manager.Exists(getTemplateType(object.GetNamespace(), labelValue)+TemplateKeyDelimiter) {
			s.logger.Debugf("using template from %s label %s for service %s/%s", label, labelValue, object.GetNamespace(), object.GetName())
			return object.GetNamespace(), labelValue, nil
		}
	}

	return object.GetNamespace(), object.GetName(), nil
}

// If clusterTrafficPolicy is present, attempt to get a multicluster templates, otherwise try getting regular templates
func (s *TemplateSelector) getTemplatesOrMulticluster(ctx *RenderRequestContext, templateNamespace string, templateName string) (map[string]string, string) {
	clusterTrafficPolicy := ctx.policies[constants.ClusterTrafficPolicyName]
	if clusterTrafficPolicy != nil {
		s.logger.Debugf("attempt to use multicluster template for object %v/%v", ctx.Object.GetNamespace(), ctx.Object.GetName())
		multiclusterTemplateName := constants.MulticlusterTemplatePrefix + templateName
		multiclusterTemplateType := getTemplateType(templateNamespace, multiclusterTemplateName)
		templatePrefix := multiclusterTemplateType + TemplateKeyDelimiter
		if s.manager.Exists(templatePrefix) {
			s.logger.Debugf("multicluster template found, use %v template", multiclusterTemplateName)
			return s.manager.GetTemplatesByPrefix(templatePrefix), multiclusterTemplateType
		}
	}

	templateType := getTemplateType(templateNamespace, templateName)
	templatePrefix := templateType + TemplateKeyDelimiter
	return s.manager.GetTemplatesByPrefix(templatePrefix), templateType
}

func templateFromConfigAnnotation(annotations map[string]string) (string, error) {
	for key := range annotations {
		if hasPrefix, annotationKeySuffix := common.FindAnnotationSuffixByPrefixOrAlias(constants.TemplateConfigAnnotationPrefix, key); hasPrefix {
			value := strings.Split(annotationKeySuffix, ".")
			if len(value) == 1 {
				return "default" + "/" + value[0], nil
			} else if len(value) == 2 {
				return value[0] + "/" + value[1], nil
			} else {
				return "", fmt.Errorf("mesh template annotation is invalid %s", key)
			}
		}
	}
	return "", nil
}

func (s *TemplateSelector) GetTemplatesOrDefault(context *RenderRequestContext, namespace, name string, kind string) (map[string]string, string) {
	templates, templateType := s.getTemplatesOrMulticluster(context, namespace, name)
	if len(templates) > 0 {
		return templates, templateType
	}

	// Fallback to default
	defaultNamespace, defaultTemplate, _ := SplitTemplateName(s.defaultTemplateType[kind])
	return s.getTemplatesOrMulticluster(context, defaultNamespace, defaultTemplate)
}

// getTemplateType returns the prefix (`namespace` + `_` + `service_name`) of the base template
// used while rendering svc object
// eg. default_default_virtualservice -> `default_default` (`template_type`)
func getTemplateType(namespace string, name string) string {
	return strings.Join([]string{namespace, name}, TemplateKeyDelimiter)
}

func SplitTemplateName(ta string) (string, string, error) {
	parts := strings.Split(ta, constants.TemplateNameSeparator)

	if len(parts) != 2 {
		return "", "", fmt.Errorf("value was %q but it is expected to be given as \"<namespace>/<name>\"", ta)
	}

	return parts[0], parts[1], nil
}
