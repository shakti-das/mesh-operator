package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/common/pkg/fmtclient"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/fmt/configprovider"
	falconmetadata "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/fmt/metadata"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/fmt/metadata/monitoring"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	constants2 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/common/pkg/k8s/constants"

	goruntime "runtime"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/secretdiscovery"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/alias"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/resources"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	"k8s.io/client-go/tools/leaderelection"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/cluster"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"k8s.io/client-go/dynamic"
	corev1informers "k8s.io/client-go/informers/core/v1"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/k8swebhook"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/rollout"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	meshhttp "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/http"

	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	"k8s.io/apimachinery/pkg/util/wait"

	"go.uber.org/zap"
	istioclient "istio.io/client-go/pkg/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"

	meshv1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/controllers"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/logging"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
)

const (
	InformerRescanPeriodSeconds = 60 * 15 * time.Second
	webhookConfigName           = "mesh-operator"
	webhookName                 = "mesh-operator.sfdc.internal"
	shutdownGracePeriod         = 15 * time.Second

	fmtConfigMetricPrefix     = "fmt_"
	DefaultFmtTimeout         = 180 * time.Second
	DefaultInsecureSkipVerify = false
	DefaultFmtCertRefresh     = 15 * time.Minute
)

var (
	DefaultFmtEndpoint = JoinPath(features.FMTSEHost, features.FMTSEPath)
	primaryClusterName = os.Getenv("CLUSTER_NAME")
	scheme             = runtime.NewScheme()
	metricsRegistry    = prometheus.NewRegistry()
)

func JoinPath(base, path string) string {
	result, err := url.JoinPath(base, path)
	if err != nil {
		log.Fatalf("failed to join URL path %q with %q: %v", base, path, err)
	}
	return result
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(istiov1alpha3.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))

	// Register go runtime metric collectors
	metricsRegistry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	metricsRegistry.MustRegister(collectors.NewGoCollector())
	// Initialize metrics provider once so all the other components can use it
	metrics.NewMetricsProvider(metricsRegistry)

	// Register k8s client metrics adapter early(before any k8s clients are created)
	// to win the sync.Once race in k8s.io/client-go/tools/metrics.Register()
	// Pass nil logger here since logging isn't initialized yet; actual logging happens in newLatencyMetric
	metrics.RegisterK8sClientMetrics(nil, metricsRegistry)
}

type IstioClientFactory func(sugarLogger *zap.SugaredLogger, config *rest.Config) istioclient.Interface
type KubernetesClientFactory func(sugarLogger *zap.SugaredLogger, config *rest.Config) kubernetes.Interface
type DynamicClientFactory func(sugarLogger *zap.SugaredLogger, config *rest.Config) dynamic.Interface
type TerminationChannelFactory func() chan os.Signal
type KubeClientFactory func(sugarLogger *zap.SugaredLogger, clusterName string, restConfig *rest.Config, rescanPeriod time.Duration, selector string, informerCacheSyncTimeoutSeconds int, metricsRegistry *prometheus.Registry) kube.Client
type ClusterDiscoveryFactory func(logger *zap.SugaredLogger, config *rest.Config, stopCh <-chan struct{}) (secretdiscovery.Discovery, error)

func NewDefaultServerCommand() *cobra.Command {
	return NewServerCommand(createKubeClientFactory, createClusterDiscoveryFactory, createApplicator, createTerminationChannel, InformerRescanPeriodSeconds, kube.BuildClientsFromConfig)
}

func NewServerCommand(
	kubeClientFactory KubeClientFactory,
	clusterDiscoveryFactory ClusterDiscoveryFactory,
	applicatorFactory templating.ApplicatorFactory,
	terminationChannelFactory TerminationChannelFactory,
	rescanPeriod time.Duration,
	buildClientFromKubeConfigHandler kube.BuildClientFromKubeConfigHandler) *cobra.Command {
	var (
		probeAddr               int
		logLevel                zapcore.Level
		klogLevel               int
		templatePaths           []string
		templateSelectorLabels  []string
		defaultTemplateType     map[string]string
		mutationTemplatesPath   []string
		dryRun                  bool
		allNamespaces           bool
		mutatingWebhookPort     int
		bindWebhookToAddress    string
		certFile                string
		keyFile                 string
		caCertFile              string
		patchWebhook            bool
		secretNamespace         string
		labelAliases            map[string]string
		annotationAliases       map[string]string
		electionOpts            LeaderElectionOptions
		renderingConfigFile     string
		controllerCfg           common.ControllerConfig
		FmtCacheEnabled         bool
		FmtRefreshDurationMs    int
		FalconInstance          string
		FunctionalDomain        string
		FmtEndpoint             string
		FmtBearerToken          string
		tspCellTypeLabel        string
		tspServiceInstanceLabel string
		tspCellLabel            string
		tspServiceNameLabel     string
	)

	var command = cobra.Command{
		Use:   "server",
		Short: "Runs the mesh-operator controller",
		RunE: func(c *cobra.Command, args []string) error {
			zapLogger, err := logging.NewLogger(logLevel)
			logging.InitKlog(zapLogger, klogLevel)

			if err != nil {
				panic(fmt.Sprintf("Failed to create root logger %v", err))
			}

			zaplogger := zapLogger.Sugar()
			logger := zaplogger.With("cluster", primaryClusterName)

			stopCh := make(chan struct{})

			// Kube config
			config := createKubeConfig(logger, &controllerCfg)

			primaryClusterKubeClient := kubeClientFactory(logger, primaryClusterName, config, rescanPeriod, controllerCfg.Selector, controllerCfg.InformerCacheSyncTimeoutSeconds, metricsRegistry)

			// Create templating components (some not used)
			templateManager := templating.NewTemplatesManagerOrDie(logger, stopCh, templatePaths)
			mutationTemplateManager := templating.NewTemplatesManagerOrDie(logger, stopCh, mutationTemplatesPath)
			templateSelector := templating.NewTemplateSelector(logger, templateManager, templateSelectorLabels, defaultTemplateType, metricsRegistry)
			renderer := templating.NewTemplateRenderer(logger, templatePaths, templateSelector)

			alias.Manager = alias.NewAliasManager(labelAliases, annotationAliases)

			// kube Informer factory
			kubeInformerFactory := primaryClusterKubeClient.KubeInformerFactory()

			// NS filter
			namespaceInformer := kubeInformerFactory.Core().V1().Namespaces()
			namespaceFilter := controllers.NewNamespaceFilter(logger, allNamespaces, namespaceInformer)

			renderConfig := &controllers.RenderingConfig{}
			if renderingConfigFile != "" {
				renderConfig, err = controllers.ParseRenderingConfig(renderingConfigFile)
				if err != nil {
					logger.Fatal("Unable to parse rendering-config file")

				}
			}

			renderConfig.ServiceConfig = createServicePolicyConfig()

			additionalObjectManager := controllers.NewAdditionalObjManager(renderConfig, metricsRegistry)

			secretInformer := kubeInformerFactory.Core().V1().Secrets()

			var resourceManager resources.ResourceManager
			if dryRun {
				resourceManager = resources.NewDryRunResourceManager(logger)
			} else {
				resourceManager = resources.NewServiceResourceManager(
					logger,
					primaryClusterKubeClient.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas(),
					primaryClusterKubeClient.MopApiClient(),
					primaryClusterKubeClient.Dynamic(),
					primaryClusterKubeClient.Discovery(),
					metricsRegistry)
			}

			// Multicluster handling and shared components
			clusterStore := controllers_api.NewClustersStore(
				logger,
				func(c controllers_api.Cluster, s <-chan struct{}, timeout time.Duration) error {
					return controllers.TestCluster(logger, metricsRegistry, c, s, timeout)
				},
				time.Duration(controllerCfg.InformerCacheSyncTimeoutSeconds)*time.Second,
				time.Duration(controllerCfg.ClusterSyncRetryTimeoutSeconds)*time.Second)

			svcMulticlusterTracker := controllers.NewMultiClusterReconcileTracker(logger)
			applicator := applicatorFactory(logger, primaryClusterKubeClient, dryRun)

			if reconcileOptimizerRun := controllers.RunReconcileOptimizer(logger, primaryClusterKubeClient, stopCh, metricsRegistry); !reconcileOptimizerRun {
				logger.Fatal("failed running reconcile optimizer")
			}

			var leaderStartHook LeaderStartHook
			var fmtCacheConfig falconmetadata.FalconServiceMetadataCache
			var fmtEventsCh chan configprovider.ServiceAuthorizationEvent
			if features.EnableFMTMetadataCache {
				fmtCacheMetrics := monitoring.NewFalconMetadataConfigMetrics(fmtConfigMetricPrefix, metricsRegistry)
				fmtCacheMetrics.RegisterMetrics()
				fmtCacheMetrics.UpdateFalconMetadataConfigState(FmtCacheEnabled)

				fmtClientConfig := fmtclient.FmtClientConfig{
					// TODO: once we have a globally accessible endpoint, add a fallback endpoint here
					FmtEndpoint:        DefaultFmtEndpoint,
					Timeout:            DefaultFmtTimeout,
					InsecureSkipVerify: DefaultInsecureSkipVerify,
					CertRefresh:        DefaultFmtCertRefresh,
					MetricsRegistry:    metricsRegistry,
					Logger:             zaplogger,
					OptionalAuthToken:  FmtBearerToken,
				}

				fmtCacheConfig, err = falconmetadata.NewFmtCache(&fmtClientConfig, FalconInstance, FunctionalDomain, FmtRefreshDurationMs, fmtCacheMetrics)
				if err != nil {
					logger.Fatal("failed to initialize fmt cache", "error", err)
				}

				var authEventPublisher configprovider.ServiceAuthorizationEventPublisher
				if features.EnableSidecarConfig {
					fmtEventsCh = make(chan configprovider.ServiceAuthorizationEvent, 100)
					authEventPublisher = configprovider.NewServiceAuthorizationEventPublisher(logger, fmtEventsCh)
				} else {
					authEventPublisher = nil
				}
				fmtConfigProvider := configprovider.NewFalconConfigProvider(logger, fmtCacheConfig, fmtCacheMetrics, configprovider.ServiceAuthorizationEventHandler(logger, authEventPublisher))

				leaderStartHook = buildFmtLeaderStartHook(logger, fmtCacheConfig, fmtConfigProvider)
			}

			// Multicluster service controller
			serviceMulticlusterController := controllers.NewMulticlusterServiceController(
				logger,
				primaryClusterKubeClient,
				renderer,
				applicator,
				clusterStore,
				resourceManager,
				svcMulticlusterTracker,
				metricsRegistry,
				mutationTemplateManager,
				templateManager,
				additionalObjectManager,
				dryRun,
				&controllerCfg)

			// Multicluster Ingress controller
			ingressMulticlusterTracker := controllers.NewMultiClusterReconcileTracker(logger)
			knativeIngressController := controllers.NewKnativeIngressController(
				applicator,
				logger,
				metricsRegistry,
				primaryClusterKubeClient,
				renderer,
				clusterStore,
				resourceManager,
				&controllerCfg,
				ingressMulticlusterTracker)

			// Multicluster ServerlessService controller
			serverlessServiceMulticlusterTracker := controllers.NewMultiClusterReconcileTracker(logger)
			knativeServerlessServiceController := controllers.NewKnativeServerlessServiceController(
				applicator,
				logger,
				metricsRegistry,
				primaryClusterKubeClient,
				renderer,
				clusterStore,
				resourceManager,
				&controllerCfg,
				serverlessServiceMulticlusterTracker)

			// TSP controller - multicluster controller for TrafficShardingPolicy CRs
			var tspController controllers_api.MulticlusterController
			if features.EnableTrafficShardingPolicyController {
				if tspCellTypeLabel == "" || tspServiceInstanceLabel == "" || tspCellLabel == "" || tspServiceNameLabel == "" {
					return fmt.Errorf("TSP controller enabled but label flags not fully configured: cell-type-label=%q, service-instance-label=%q, cell-label=%q, service-name-label=%q",
						tspCellTypeLabel, tspServiceInstanceLabel, tspCellLabel, tspServiceNameLabel)
				}
				logger.Info("TrafficShardingPolicy controller is enabled, creating TSP controller")
				tspController = controllers.NewTrafficShardController(
					logger,
					renderer,
					applicator,
					resourceManager,
					clusterStore,
					metricsRegistry,
					controllers.TSPLabelConfig{
						CellTypeLabel:        tspCellTypeLabel,
						ServiceInstanceLabel: tspServiceInstanceLabel,
						CellLabel:            tspCellLabel,
						ServiceNameLabel:     tspServiceNameLabel,
					},
				)
			}

			// Create ProxyAccessController (if SidecarConfig feature is enabled) before multicluster controller
			// This allows ProxyAccessController to be passed to the multicluster controller for dynamic cluster registration
			var proxyAccessController controllers_api.ProxyAccessController
			if features.EnableSidecarConfig {
				// Register SidecarConfig types in the runtime scheme so the event recorder
				// can construct ObjectReferences for SidecarConfig resources (used by SidecarConfigController).
				utilruntime.Must(meshv1alpha1.AddToScheme(scheme))

				dynamicClient := primaryClusterKubeClient.Dynamic()
				proxyAccessController = controllers.NewProxyAccess(
					logger,
					clusterStore,
					fmtCacheConfig, // nil when FMT not initialized
					dynamicClient,
					fmtEventsCh, // nil when FMT not initialized (same as fmtCacheConfig)
					metricsRegistry,
				)
			}

			// multicluster controller
			multiClusterController := createMultiClusterController(
				logLevel,
				secretNamespace,
				primaryClusterKubeClient,
				namespaceFilter,
				svcMulticlusterTracker,
				resourceManager,
				renderer,
				cluster.ID(primaryClusterName),
				allNamespaces, dryRun,
				applicator,
				buildClientFromKubeConfigHandler,
				clusterStore,
				additionalObjectManager,
				serviceMulticlusterController,
				knativeIngressController,
				knativeServerlessServiceController,
				tspController,
				mutationTemplateManager,
				&controllerCfg,
				proxyAccessController)

			// Start namespace filter (which carries NS informer) so that namespace information
			// is available by the time we start syncing secret events
			if ok := namespaceFilter.Run(stopCh); !ok {
				logger.Fatal("Unable to sync namespaces cache")
			}

			// Start secret informer
			if ok := runSecretInformer(logger, secretInformer, stopCh); !ok {
				logger.Fatal("Unable to sync secret informer cache")
			}

			discovery, discoveryErr := clusterDiscoveryFactory(logger, config, stopCh)
			if discoveryErr != nil {
				logger.Fatal("failed to create cluster discovery: %w", discoveryErr)
			}

			admittorServer, err := createAdmissionServer(zaplogger, primaryClusterKubeClient.Kube(), discovery, mutationTemplateManager, templateManager, metricsRegistry,
				features.EnableArgoIntegration, certFile, keyFile,
				func(handler http.Handler) (meshhttp.TestableHTTPServer, error) {
					client := &http.Client{Transport: &http.Transport{}}
					var server *http.Server
					var useTls bool
					if features.PlainTextMutatingWebhook {
						server = &http.Server{
							Handler: handler,
						}
						useTls = false
						logger.Infof("created plain-text admission server")
					} else {
						server = &http.Server{
							Handler: handler,
							TLSConfig: &tls.Config{
								GetCertificate: k8swebhook.NewNaiveGetCertificateFunc(logger, certFile, keyFile),
								MinVersion:     tls.VersionTLS12,
							},
						}
						useTls = true
						logger.Infof("created tls admission server")
					}

					return meshhttp.NewStandardHTTPServer(logger, client, bindWebhookToAddress, mutatingWebhookPort, server, useTls), nil
				})

			if err != nil {
				logger.Errorf("error in creating admissionServer:%s", err)
			} else if admittorServer == nil {
				logger.Debugf("mutatingWebhook is disabled")
			} else {
				admittorServer.Start()
				defer admittorServer.Stop()
				logger.Infof("started mutating webhook on port: %d", mutatingWebhookPort)

				if patchWebhook {
					patcher := k8swebhook.CreateCertPatcher(caCertFile, webhookConfigName, webhookName, logger)
					err := patcher.Start(stopCh, false, features.EnableArgoIntegration)
					if err != nil {
						logger.Fatalw("failed to start patcher", "error", err)
						return err
					}
					logger.Infof("started certificate patcher")
				}
			}

			signalChan := terminationChannelFactory()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if !electionOpts.LeaderElect {
				logger.Info("Leader election is turned off. Running in single-instance mode")
				if leaderStartHook != nil {
					if err := leaderStartHook(ctx); err != nil {
						logger.Fatal("failed to start leaderStartHook", "error", err)
					}
				}
				startMultiClusterControllers(logger, multiClusterController, stopCh)
			} else {
				leConfig, err := CreateLeConfig(
					logger,
					electionOpts,
					primaryClusterKubeClient.Kube(),
					multiClusterController,
					metricsRegistry,
					leaderStartHook)
				if err != nil {
					logger.Fatalf("Failed to start leader election: %v", err)
				}
				go func() {
					leaderelection.RunOrDie(ctx, leConfig)
					close(signalChan)
				}()
			}

			httpServer := createMetricsAndHealthHttpServer(logger, probeAddr)

			logger.Info("app fully started")

			sig := <-signalChan

			if features.EnableThreadDump {
				switch sig {
				case syscall.SIGQUIT, syscall.SIGKILL, syscall.SIGABRT:
					buf := make([]byte, 1<<20)
					stacklen := goruntime.Stack(buf, true)
					logger.Debugf("*** goroutine dump...\n%s\n*** end\n", buf[:stacklen])
				}
			}

			logger.Info("stopping application")

			if httpServer != nil {
				httpServer.Stop()
				logger.Infof("stopped livenes/rediness. Waiting for %s before shutdown.", shutdownGracePeriod)
				time.Sleep(shutdownGracePeriod)
			}
			close(stopCh)
			return nil
		},
	}

	command.Flags().AddGoFlag(logging.NewFlag(&logLevel))
	command.Flags().IntVar(&klogLevel, "klog-level", 1, "Klog verbosity level. -1 disabled, 1 is minimal verbosity and higher is more verbose")
	command.Flags().IntVar(&probeAddr, "management-port", 15372, "Port on which health and metrics endpoints are exposed. No endpoints created if value below 0")
	command.Flags().BoolVar(&dryRun, "dry-run", true, "Running mesh-operator in a logging-only mode")
	command.Flags().StringSliceVar(&templatePaths, "template-paths", []string{}, "Comma-separated list of template directories")
	command.Flags().StringSliceVar(&templateSelectorLabels, "template-selector-labels", []string{}, "list of labels used for template selection such as `app`, `name`")
	command.Flags().StringToStringVar(&defaultTemplateType, "default-template-type", map[string]string{}, "kind to default template type mapping")
	command.Flags().StringSliceVar(&mutationTemplatesPath, "mutation-template-paths", []string{}, "Comma-separated list of mutation template directories")
	command.Flags().BoolVar(&allNamespaces, "all-namespaces", false, "If enabled, mesh-operator will render templates in all namespaces")
	command.Flags().IntVarP(&mutatingWebhookPort, "mutating-webhook-port", "p", 443, "port for the webhook https server")
	command.Flags().StringVar(&bindWebhookToAddress, "bind-to-address", "0.0.0.0", "address to which mutating webhook will be bound")
	command.Flags().StringVarP(&certFile, "cert", "c", "", "path to the x509 cert used to serve https")
	command.Flags().StringVarP(&keyFile, "key", "k", "", "path to the x509 private key used to serve https")
	command.Flags().StringVar(&caCertFile, "ca-cert", "", "path to the x509 CA cert")
	command.Flags().BoolVar(&patchWebhook, "patch-webhook", false, "enable auto patching for mesh operator's webhook")
	command.Flags().StringVar(&secretNamespace, "remote-secret-namespace", "mesh-control-plane", "namespace used to read remote cluster secret")
	command.Flags().StringVar(&renderingConfigFile, "rendering-config", "", "Path to rendering config file")
	command.Flags().IntVar(&controllerCfg.InformerCacheSyncTimeoutSeconds, "informer-cache-sync-timeout", constants.DefaultInformerCacheSyncTimeoutSeconds, "informer cache sync timeout in seconds")
	command.Flags().IntVar(&controllerCfg.ClusterSyncRetryTimeoutSeconds, "cluster-sync-retry-timeout", constants.DefaultClusterSyncRetryTimeoutSeconds, "retry timeout for failed to sync clusters in seconds")
	command.Flags().StringVar(&controllerCfg.Selector, "selector", "", "Selector (label query) to filter on, supports '=', '==', and '!='.(e.g. --selector=operator.mesh.io/enabled!=false,!serving.knative.dev/service). Matching objects must satisfy all of the specified label constraints.")
	command.Flags().Float32Var(&controllerCfg.KubeAPIQPS, "kube-api-qps", constants.DefaultKubeAPIQPS, "Maximum queries per second (QPS) to the Kubernetes API server for both primary and remote clusters")
	command.Flags().IntVar(&controllerCfg.KubeAPIBurst, "kube-api-burst", constants.DefaultKubeAPIBurst, "Maximum burst for throttle to the Kubernetes API server for both primary and remote clusters")

	//for leader elections
	command.Flags().BoolVar(&electionOpts.LeaderElect, "leader-elect", constants.DefaultLeaderElect, "If true, controller will perform leader election between instances to ensure no more than one instance of controller operates at a time")
	command.Flags().StringVar(&electionOpts.LeaderElectionNamespace, "leader-election-namespace", constants.DefaultLeaderElectionNamespace, "Namespace used to perform leader election. Only used if leader election is enabled")
	command.Flags().DurationVar(&electionOpts.LeaderElectionLeaseDuration, "leader-election-lease-duration", constants.DefaultLeaderElectionLeaseDuration, "The duration that non-leader candidates will wait after observing a leadership renewal until attempting to acquire leadership of a led but unrenewed leader slot. This is effectively the maximum duration that a leader can be stopped before it is replaced by another candidate. This is only applicable if leader election is enabled.")
	command.Flags().DurationVar(&electionOpts.LeaderElectionRenewDeadline, "leader-election-renew-deadline", constants.DefaultLeaderElectionRenewDeadline, "The interval between attempts by the acting master to renew a leadership slot before it stops leading. This must be less than or equal to the lease duration. This is only applicable if leader election is enabled.")
	command.Flags().DurationVar(&electionOpts.LeaderElectionRetryPeriod, "leader-election-retry-period", constants.DefaultLeaderElectionRetryPeriod, "The duration the clients should wait between attempting acquisition and renewal of a leadership. This is only applicable if leader election is enabled.")

	command.Flags().StringToStringVar(&labelAliases, "label-aliases", map[string]string{}, "supports aliasing in object labels")
	command.Flags().StringToStringVar(&annotationAliases, "annotation-aliases", map[string]string{}, "supports aliasing in object annotations")

	command.Flags().IntVar(&FmtRefreshDurationMs, "fmt-refresh-duration-ms", 600000, "fmt fetch delay")
	command.Flags().StringVar(&FmtEndpoint, "fmt-endpoint", "http://fmtapi.service-mesh.svc.mesh.sfdc.net:443/graphql", "FMT endpoint via ServiceEntry")
	command.Flags().StringVar(&FmtBearerToken, "fmt-bearer-token", "", "Optional FMT OAuth2 token for local development (falcon CLI tokens). Not required in production.")
	command.Flags().StringVar(&FalconInstance, "falcon-instance", "", "FI where MOP is running")
	command.Flags().StringVar(&FunctionalDomain, "functional-domain", "", "FD where MOP is running")

	// TSP label configuration for route-to-target resolution (values hardcoded in helm chart)
	command.Flags().StringVar(&tspCellTypeLabel, "cell-type-label", "", "Service label key for cell type used in TSP route validation")
	command.Flags().StringVar(&tspServiceInstanceLabel, "service-instance-label", "", "Service label key for service instance used in TSP route validation")
	command.Flags().StringVar(&tspCellLabel, "cell-label", "", "Service label key for cell used in TSP route validation")
	command.Flags().StringVar(&tspServiceNameLabel, "service-name-label", "", "Service label key for service name used in TSP validation")

	return &command
}

func createTerminationChannel() chan os.Signal {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL, syscall.SIGABRT, syscall.SIGQUIT)
	return signalChan
}

func createMetricsAndHealthHttpServer(logger *zap.SugaredLogger, probeAddr int) meshhttp.TestableHTTPServer {
	if probeAddr <= 0 {
		return nil
	}
	mux := http.NewServeMux()

	handler := promhttp.InstrumentMetricHandler(
		metricsRegistry, promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{}),
	)
	mux.Handle("/metrics", handler)

	mux.HandleFunc("/healthz", func(responseWriter http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprint(responseWriter, "Ok")
	})
	mux.HandleFunc("/readyz", func(responseWriter http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprint(responseWriter, "Ok")
	})

	server := &http.Server{Handler: mux}

	client := &http.Client{Transport: &http.Transport{}}
	httpServer := meshhttp.NewStandardHTTPServer(logger, client, "", probeAddr, server, false)

	httpServer.Start()
	logger.Infof("started management endpoint on %d", probeAddr)

	return httpServer
}

func createKubeConfig(logger *zap.SugaredLogger, controllerCfg *common.ControllerConfig) *rest.Config {
	config, err := kube.BuildClientCmd("", "").ClientConfig()
	if err != nil {
		logger.Fatalw("Error building kubeconfig", "error", err)
	}

	// Apply client-side throttling settings for primary cluster
	config.QPS = controllerCfg.KubeAPIQPS
	config.Burst = controllerCfg.KubeAPIBurst
	logger.Infof("Primary cluster Kubernetes client configured with QPS: %.1f, Burst: %d", config.QPS, config.Burst)

	return config
}

// set up for rollout mutating webhook
func createAdmissionServer(logger *zap.SugaredLogger, kubernetesClient kubernetes.Interface, discovery secretdiscovery.Discovery,
	mutationTemplateManager templating.TemplatesManager, serviceTemplatesManager templating.TemplatesManager, registry *prometheus.Registry, mutatingWebhookEnabled bool, certFile string,
	keyFile string, newServer func(handler http.Handler) (meshhttp.TestableHTTPServer, error)) (meshhttp.TestableHTTPServer, error) {

	if !mutatingWebhookEnabled {
		logger.Infof("mutatingWebhookListener not enabled, feature flag not set")
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		logger.Fatalf("mutatingWebhookListener not enabled, insufficient arguments")
		return nil, nil
	}

	mux := http.NewServeMux()

	rolloutAdmittor, err := rollout.NewRolloutAdmittor(kubernetesClient, mutationTemplateManager, serviceTemplatesManager, logger, registry, false, discovery, primaryClusterName) //placeholder for admit_on_failure
	if err != nil {
		return nil, err
	}

	rolloutAdmissionHandler := k8swebhook.NewAdmissionHandlerV1(logger, rolloutAdmittor)
	mux.Handle("/mutate", rolloutAdmissionHandler)
	mux.Handle("/mutate/", rolloutAdmissionHandler)

	admittorServer, err := newServer(mux)
	if err != nil {
		return nil, err
	}

	return admittorServer, nil
}

func createApplicator(logger *zap.SugaredLogger, primaryClient kube.Client, dryRun bool) templating.Applicator {
	if dryRun {
		return templating.NewConsoleApplicator(logger)
	}
	return templating.NewK8sApplicator(
		logger,
		primaryClient)
}

func buildFmtLeaderStartHook(logger *zap.SugaredLogger, fmtCache falconmetadata.FalconServiceMetadataCache, fmtConfigProvider configprovider.FalconConfigProvider) LeaderStartHook {
	return func(ctx context.Context) error {
		if err := fmtCache.Start(); err != nil {
			logger.Errorf("failed to start fmt cache [%v]", err)
			return err
		}

		if err := fmtConfigProvider.Start(ctx); err != nil {
			logger.Errorf("failed to start fmt config provider [%v]", err)
			return err
		}

		go func() {
			<-ctx.Done()
			if err := fmtConfigProvider.Stop(); err != nil {
				logger.Errorf("Error while closing fmt config provider [%v]", err)
			}
			if err := fmtCache.Stop(); err != nil {
				logger.Errorf("Error while closing fmt cache [%v]", err)
			}
		}()

		return nil
	}
}

func createClusterDiscoveryFactory(logger *zap.SugaredLogger, config *rest.Config, stopCh <-chan struct{}) (secretdiscovery.Discovery, error) {
	client, clientError := secretdiscovery.NewClient(logger, config)
	if clientError != nil {
		logger.Errorf("failed to create client for mesh-operator mutating webhook: %w", clientError)
	}

	discovery, discoveryError := secretdiscovery.NewDynamicDiscovery(
		logger,
		primaryClusterName,
		client,
		constants.ControlPlaneNamespace,
		stopCh)
	if discoveryError != nil {
		return nil, discoveryError
	}
	return discovery, nil
}

func createMultiClusterController(logLevel zapcore.Level,
	namespace string,
	primaryKubeClient kube.Client,
	namespaceFilter controllers_api.NamespaceFilter,
	multiClusterReconcileTracker controllers.ReconcileManager,
	resourceManager resources.ResourceManager,
	renderer templating.TemplateRenderer,
	localClusterID cluster.ID,
	allNamespaces bool,
	dryRun bool,
	applicator templating.Applicator,
	buildClientFromKubeConfigHandler kube.BuildClientFromKubeConfigHandler,
	clusterStore controllers_api.ClusterManager,
	additionalObjectManager controllers.AdditionalObjectManager,
	multiclusterServiceController controllers_api.MulticlusterController,
	multiclusterIngressController controllers_api.MulticlusterController,
	serverlessServiceController controllers_api.MulticlusterController,
	tspController controllers_api.MulticlusterController,
	mutatingTemplateManager templating.TemplatesManager,
	controllerCfg *common.ControllerConfig,
	proxyAccessController controllers_api.ProxyAccessController) controllers.Controller {

	kubeInformerFactory := primaryKubeClient.KubeInformerFactory()
	secretInformer := kubeInformerFactory.Core().V1().Secrets()
	mopArg := common.MopArgs{AllNamespaces: allNamespaces, DryRun: dryRun, LogLevel: logLevel}

	multiclusterController := controllers.NewSecretController(
		namespace,
		secretInformer,
		namespaceFilter,
		renderer,
		multiClusterReconcileTracker,
		resourceManager,
		primaryKubeClient,
		localClusterID,
		mopArg,
		applicator,
		metricsRegistry,
		buildClientFromKubeConfigHandler,
		clusterStore,
		additionalObjectManager,
		multiclusterServiceController,
		multiclusterIngressController,
		serverlessServiceController,
		tspController,
		mutatingTemplateManager,
		controllerCfg,
		proxyAccessController)
	return multiclusterController
}

func runSecretInformer(logger *zap.SugaredLogger, secretInformer corev1informers.SecretInformer, stopCh <-chan struct{}) bool {
	logger.Infof("Starting secret informer..")
	go wait.Until(func() { secretInformer.Informer().Run(stopCh) }, time.Second, stopCh)
	logger.Infof("Waiting for secret informer cache sync ..")
	return cache.WaitForCacheSync(stopCh, secretInformer.Informer().HasSynced)
}

func startMultiClusterControllers(logger *zap.SugaredLogger,
	multiClusterController controllers.Controller,
	stopCh <-chan struct{}) {

	logger.Info("Starting multicluster controller")
	go wait.Until(func() {
		err := multiClusterController.Run(features.ClusterControllerThreads, stopCh)
		if err != nil {
			logger.Error(err)
		}
	}, time.Second, stopCh)
	logger.Info("Started multicluster controllers")
}

func createKubeClientFactory(logger *zap.SugaredLogger, clusterName string, restConfig *rest.Config, rescanPeriod time.Duration, selector string, informerCacheSyncTimeoutSeconds int, metricsRegistry *prometheus.Registry) kube.Client {
	var tweakListOptionsFunc func(options *v1.ListOptions)
	var err error
	if len(selector) > 0 {
		tweakListOptionsFunc, err = kube.GetTweakListOptionsFunc(selector)
		if err != nil {
			logger.Fatalw("Error parsing label selector", "error", err)
		}
	}

	primaryClusterKubeClient, err := kube.NewClientWithTweakListOption(logger, clusterName, restConfig, rescanPeriod, tweakListOptionsFunc, informerCacheSyncTimeoutSeconds, metricsRegistry)
	if err != nil {
		logger.Fatalw("Error building primary Cluster filtered client", "error", err)
	}

	return primaryClusterKubeClient
}

func createServicePolicyConfig() map[string]controllers.AdditionalObject {
	serviceConfig := make(map[string]controllers.AdditionalObject)
	if features.EnableMulticlusterCTP {
		serviceConfig[constants.ClusterTrafficPolicyName] = controllers.AdditionalObject{
			Group:    constants2.ClusterTrafficPolicyResource.Group,
			Version:  constants2.ClusterTrafficPolicyResource.Version,
			Resource: constants2.ClusterTrafficPolicyResource.Resource,
			Lookup: controllers.Lookup{
				BySvcNameAndNamespace: true,
			},
		}
	}

	/*
		When both k8s service name is same as falcon service name, we can re-use this.
		if features.EnableTrafficShardingPolicy {
			// NOTE: name/namespace lookup configured here relies on the fact that TSP.services field is constrained to one item and matches TSP name
			// This will change once we better understand service targeting for TSPs.
			// serviceConfig[constants.TrafficShardingPolicyName] = controllers.AdditionalObject{
			// 	Group:    constants2.TrafficShardingPolicyResource.Group,
			// 	Version:  constants2.TrafficShardingPolicyResource.Version,
			// 	Resource: constants2.TrafficShardingPolicyResource.Resource,
			// 	Lookup: controllers.Lookup{
			// 		BySvcNameAndNamespaceArray: []string{"spec", "services"},
			// 	},
			// }
		}
	*/

	return serviceConfig
}
