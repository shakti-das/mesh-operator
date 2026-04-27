package cmd

import (
	"context"
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	"github.com/istio-ecosystem/mesh-operator/pkg/dynamicrouting"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"github.com/istio-ecosystem/mesh-operator/pkg/secretdiscovery"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/alias"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/core/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/logging"
	"github.com/istio-ecosystem/mesh-operator/pkg/rollout"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
)

const (
	serviceResourceInfo           = "service resource"
	admissionReviewResourceInfo   = "admission-review resource"
	additionalObjectsResourceInfo = "additional-objects resource"
)

func NewMutateCommand() *cobra.Command {
	var (
		logLevel              zapcore.Level
		servicePath           string
		admissionReviewPath   string
		templatesPath         []string
		mutationTemplatesPath []string
		labelAliases          map[string]string
		annotationAliases     map[string]string
		metadata              map[string]string
	)

	mutateCmd := &cobra.Command{
		Use:   "mutate",
		Short: "Renders mutation templates for an AdmissionReview.",
		RunE: func(c *cobra.Command, args []string) error {
			zapLogger, _ := logging.NewLogger(logLevel)
			logger := zapLogger.Sugar()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			serviceFile := ReadFileOrDie(logger, servicePath, serviceResourceInfo)
			defer serviceFile.Close()
			admissionReviewFile := ReadFileOrDie(logger, admissionReviewPath, admissionReviewResourceInfo)
			defer admissionReviewFile.Close()

			service := &v1.Service{}
			DecodeYamlOrJsonOrDie(logger, serviceFile, serviceResourceInfo, service)
			admissionReview := &admissionv1.AdmissionReview{}
			DecodeYamlOrJsonOrDie(logger, admissionReviewFile, admissionReviewResourceInfo, admissionReview)

			k8sClient := k8sfake.NewSimpleClientset(service)

			alias.Manager = alias.NewAliasManager(labelAliases, annotationAliases)

			serviceTemplatesManager, err := templating.NewTemplatesManager(logger, ctx.Done(), templatesPath)
			if err != nil {
				logger.Fatalw("Failed to create service templates manager", "error", err)
			}

			mutationTemplatesManager, err := templating.NewTemplatesManager(logger, ctx.Done(), mutationTemplatesPath)
			if err != nil {
				logger.Fatalw("Failed to create mutation templates manager", "error", err)
			}

			primaryFakeClient := &secretdiscovery.FakeClient{KubeClient: k8sClient}

			primaryCluster := secretdiscovery.NewFakeCluster(primaryClusterName, true, primaryFakeClient)
			remoteClusters := []secretdiscovery.DynamicCluster{}
			discovery := secretdiscovery.NewFakeDynamicDiscovery(primaryCluster, remoteClusters)

			rolloutAdmittor, err := rollout.NewRolloutAdmittor(k8sClient, mutationTemplatesManager, serviceTemplatesManager, logger, metricsRegistry, false, discovery, primaryClusterName)
			if err != nil {
				logger.Fatalw("Failed to create fake admittor", "error", err)
			}

			response := rolloutAdmittor.Admit(admissionReview, primaryClusterName)
			if err != nil {
				logger.Fatalw("Failed to marshal result", "error", err)
			}

			// Re-mutate patches
			_, remutationTemplate := rollout.GetReMutationTemplate(serviceTemplatesManager, service)

			if features.EnableDynamicRoutingForBGAndCanaryServices {
				rolloutObj, unmarshalError := common.UnmarshallObject(admissionReview)
				if unmarshalError != nil {
					logger.Fatalw("Failed to unmarshal admissionReview object", "error", unmarshalError)
				}
				dynamicRoutingMetadata := dynamicrouting.GetMetadataIfDynamicRoutingEnabled(rolloutObj, service, logger, metricsRegistry)
				metadata = common.MergeMaps(metadata, dynamicRoutingMetadata)
			}

			reMutatePatches, err := rollout.CreateRolloutPatches(mutationTemplatesManager, service, admissionReview.Request.Object.Raw, remutationTemplate, metadata)
			if err != nil {
				logger.Fatalw("Failed to create re-mutation patches", "error", err)
			}

			fmt.Print(string(response.Patch))
			if reMutatePatches != "" {
				fmt.Println("// -- re-mutate")
				fmt.Print(reMutatePatches)
			}
			return nil
		},
	}

	mutateCmd.Flags().AddGoFlag(logging.NewFlag(&logLevel))
	mutateCmd.Flags().StringVar(&servicePath, "service-path", "", "[REQUIRED] path to the service json or yaml file")
	mutateCmd.Flags().StringVar(&admissionReviewPath, "admission-path", "", "[REQUIRED] path to the AdmissionReiview json or yaml file")
	mutateCmd.Flags().StringSliceVar(&templatesPath, "template-paths", []string{}, "Comma-separated list of service template directories")
	mutateCmd.Flags().StringSliceVar(&mutationTemplatesPath, "mutation-template-paths", []string{}, "Comma-separated list of mutation template directories")
	mutateCmd.Flags().StringToStringVar(&labelAliases, "label-aliases", map[string]string{}, "supports aliasing in object labels")
	mutateCmd.Flags().StringToStringVar(&annotationAliases, "annotation-aliases", map[string]string{}, "supports aliasing in object annotations")
	mutateCmd.Flags().StringToStringVar(&metadata, "metadata", map[string]string{}, "Metadata for this mutation request")

	return mutateCmd
}
