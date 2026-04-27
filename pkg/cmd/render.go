package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/alias"
	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/controllers"
	"go.uber.org/zap"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/logging"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

const (
	meshOperatorResourceInfo      = "meshoperator resource"
	servicePolicyResourceInfo     = "service policy resource"
	serviceEntryResourceInfo      = "serviceEntry resource"
	ingressResourceInfo           = "ingress resource"
	serverlessServiceResourceInfo = "serverlessService resource"
)

func getApplicator(printTemplates bool, sugar *zap.SugaredLogger) templating.Applicator {
	if printTemplates {
		return templating.NewFormatPrinterApplicator(sugar)
	}
	return templating.NewConsoleApplicator(sugar)
}

func NewTemplateRendererCommand() *cobra.Command {
	var (
		logLevel               zapcore.Level
		metadata               map[string]string
		servicePath            string
		serviceEntryPath       string
		meshOperatorPath       string
		ingressPath            string
		serverlessServicePath  string
		policiesPath           string
		clusterName            string
		templatePaths          []string
		templateSelectorLabels []string
		defaultTemplateType    map[string]string
		printTemplates         bool
		labelAliases           map[string]string
		annotationAliases      map[string]string
		additionalObjectsPath  string
	)
	renderCmd := &cobra.Command{
		Use:   "render",
		Short: "Renders provided templates for a service or ingress.",
		RunE: func(c *cobra.Command, args []string) error {
			zapLogger, err := logging.NewLogger(logLevel)
			if err != nil {
				panic(fmt.Sprintf("Failed to create root logger %v", err))
			}
			logger := zapLogger.Sugar()

			// Count how many resource paths are provided
			resourcePathCount := 0
			if servicePath != "" {
				resourcePathCount++
			}
			if serviceEntryPath != "" {
				resourcePathCount++
			}
			if ingressPath != "" {
				resourcePathCount++
			}
			if serverlessServicePath != "" {
				resourcePathCount++
			}

			// Only one resource type should be provided for rendering (excluding meshOperator)
			if resourcePathCount > 1 {
				return fmt.Errorf("multiple resource paths provided. only one of servicePath, serviceEntryPath, ingressPath, or serverlessServicePath should be provided for rendering")
			}

			renderMop := false
			renderServiceEntry := false
			renderIngress := false
			renderServerlessService := false

			meshOperatorResource := &v1alpha1.MeshOperator{}
			objectToRender := &unstructured.Unstructured{}
			additionalObjects := map[string]*templating.AdditionalObjects{}

			if meshOperatorPath != "" {
				meshOperatorResourceFile := ReadFileOrDie(logger, meshOperatorPath, meshOperatorResourceInfo)
				defer meshOperatorResourceFile.Close()
				DecodeYamlOrJsonOrDie(logger, meshOperatorResourceFile, meshOperatorResourceInfo, meshOperatorResource)
				renderMop = len(meshOperatorResource.Spec.Overlays) == 0
			}

			if serviceEntryPath != "" {
				renderServiceEntry = true
				serviceEntryResourceFile := ReadFileOrDie(logger, serviceEntryPath, serviceEntryResourceInfo)
				defer serviceEntryResourceFile.Close()
				DecodeYamlOrJsonOrDie(logger, serviceEntryResourceFile, serviceEntryResourceInfo, objectToRender)
			}

			if ingressPath != "" {
				renderIngress = true
				ingressResourceFile := ReadFileOrDie(logger, ingressPath, ingressResourceInfo)
				defer ingressResourceFile.Close()
				DecodeYamlOrJsonOrDie(logger, ingressResourceFile, ingressResourceInfo, objectToRender)
			}

			if serverlessServicePath != "" {
				renderServerlessService = true
				serverlessServiceResourceFile := ReadFileOrDie(logger, serverlessServicePath, serverlessServiceResourceInfo)
				defer serverlessServiceResourceFile.Close()
				DecodeYamlOrJsonOrDie(logger, serverlessServiceResourceFile, serverlessServiceResourceInfo, objectToRender)
			}

			//var service *v1.Service
			if servicePath == "" {
				// for rendering svc templates, svc resource is mandatory
				if !renderMop && !renderServiceEntry && !renderIngress && !renderServerlessService {
					logger.Panicf("No path to resource specified: %s", serviceResourceInfo)
				}
			} else {
				serviceFile := ReadFileOrDie(logger, servicePath, serviceResourceInfo)
				defer serviceFile.Close()
				DecodeYamlOrJsonOrDie(logger, serviceFile, serviceResourceInfo, objectToRender)
			}

			if additionalObjectsPath != "" {
				if !renderMop {
					logger.Fatal("additional objects are only supported for MOP extensions")
				}
				additionalObjectsFile := ReadFileOrDie(logger, additionalObjectsPath, additionalObjectsResourceInfo)
				defer additionalObjectsFile.Close()
				DecodeYamlOrJsonOrDie(logger, additionalObjectsFile, additionalObjectsResourceInfo, &additionalObjects)
			}

			stop := make(chan struct{})
			templateManager, err := templating.NewTemplatesManager(logger, stop, templatePaths)
			if err != nil {
				logger.Fatalw("Cannot create templates manager", "error", err)
				return err
			}

			alias.Manager = alias.NewAliasManager(labelAliases, annotationAliases)

			templateSelector := templating.NewTemplateSelector(logger, templateManager, templateSelectorLabels, defaultTemplateType, metricsRegistry)
			templateRenderer := templating.NewTemplateRenderer(logger, templatePaths, templateSelector)
			applicator := getApplicator(printTemplates, logger)
			mutators := []templating.Mutator{
				&templating.OwnerRefMutator{},
				&templating.ConfigNamespaceAnnotationCleanup{},
			}
			configGenerator := controllers.NewConfigGenerator(
				templateRenderer,
				mutators,
				nil,
				applicator,
				templating.ApplyOverlays,
				logger,
				prometheus.NewRegistry())

			policiesRaw := map[string]json.RawMessage{}
			policies := map[string]*unstructured.Unstructured{}
			if policiesPath != "" {
				policiesFile := ReadFileOrDie(logger, policiesPath, servicePolicyResourceInfo)
				defer policiesFile.Close()
				DecodeYamlOrJsonOrDie(logger, policiesFile, servicePolicyResourceInfo, &policiesRaw)

				for name, rawPolicy := range policiesRaw {
					policy := &unstructured.Unstructured{}
					err := json.Unmarshal(rawPolicy, policy)
					if err != nil {
						logger.Panicf("failed to unmarshal policy %s in file %s", name, policiesPath)
					}
					policies[name] = policy
				}
			} else {
				policies = nil
			}

			var requestContext templating.RenderRequestContext
			if renderMop {
				requestContext = templating.NewMOPRenderRequestContext(objectToRender, meshOperatorResource, metadata,
					clusterName, nil, additionalObjects)
			} else {
				requestContext = templating.NewRenderRequestContext(
					objectToRender,
					metadata,
					nil,
					"",
					"",
					map[string][]v1alpha1.Overlay{meshOperatorResource.Name: meshOperatorResource.Spec.Overlays},
					policies)
			}

			_, err = configGenerator.GenerateConfig(&requestContext, controllers.NoOpOnBeforeApply, logger)

			if err != nil {
				logError(err, logger, metadata, servicePath)
			}
			return nil
		},
	}

	renderCmd.Flags().AddGoFlag(logging.NewFlag(&logLevel))
	renderCmd.Flags().StringVar(&servicePath, "service-path", "", "path to the service json or yaml file")
	renderCmd.Flags().StringVar(&serviceEntryPath, "service-entry-path", "", "path to the serviceentry json or yaml file")
	renderCmd.Flags().StringVar(&ingressPath, "ingress-path", "", "path to the ingress json or yaml file")
	renderCmd.Flags().StringVar(&serverlessServicePath, "serverless-service-path", "", "path to the serverless service json or yaml file")
	renderCmd.Flags().StringToStringVar(&metadata, "metadata", map[string]string{}, "Metadata for this template rendering request (e.g. routing context or statefulset replicas)")
	renderCmd.Flags().StringSliceVar(&templatePaths, "template-paths", []string{}, "Comma-separated list of template directories")
	renderCmd.Flags().StringSliceVar(&templateSelectorLabels, "template-selector-labels", []string{}, "list of labels used for template selection such as `app`, `name`")
	renderCmd.Flags().StringToStringVar(&defaultTemplateType, "default-template-type", map[string]string{}, "kind to default template type mapping")
	renderCmd.Flags().StringVar(&meshOperatorPath, "meshoperator-path", "", "path to the MeshOperator CR json or yaml file")
	renderCmd.Flags().StringVar(&policiesPath, "service-policy-path", "", "path to the service policies json or yaml file")
	renderCmd.Flags().BoolVarP(&printTemplates, "print-templates", "p", false, "Whether the rendered templates should be formatted and printed instead of being logged")
	renderCmd.Flags().StringVar(&clusterName, "clusterName", "primary", "name of the cluster to render mesh config")
	renderCmd.Flags().StringToStringVar(&labelAliases, "label-aliases", map[string]string{}, "supports aliasing in object labels")
	renderCmd.Flags().StringToStringVar(&annotationAliases, "annotation-aliases", map[string]string{}, "supports aliasing in object annotations")
	renderCmd.Flags().StringVar(&additionalObjectsPath, "additional-objects-path", "", "path to the json or yaml file containing additional objects context")

	return renderCmd
}

func logError(err error, logger *zap.SugaredLogger, metadata map[string]string, servicePath string) {
	if err != nil {
		logger.Fatalw(
			"Cannot render templates",
			"service", servicePath,
			"metadata", metadata,
			"error", err)
	}
}
