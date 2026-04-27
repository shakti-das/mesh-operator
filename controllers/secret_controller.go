package controllers

import (
	"context"
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	"k8s.io/client-go/informers"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"

	"github.com/istio-ecosystem/mesh-operator/pkg/resources"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	corev1informers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/record"

	"github.com/istio-ecosystem/mesh-operator/pkg/cluster"
	corev1 "k8s.io/api/core/v1"

	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type upsertSecretHandler func(secretKey string, s *corev1.Secret, event controllers_api.Event)
type deleteSecretHandler func(secretKey string)
type createRemoteClusterHandler func(kubeConfig []byte, clusterID string) (*Cluster, error)
type addClusterHandler func(secretKey string, cluster *Cluster) error
type deleteClusterHandler func(secretKey string, clusterID cluster.ID)

type SecretReconcileTracker struct {
	sync.RWMutex

	initialQueueSize int
	itemsReconciled  int
}

func NewSecretReconcileTracker() *SecretReconcileTracker {
	return &SecretReconcileTracker{}
}

func (srt *SecretReconcileTracker) setInitialQueueSize(initialQueueSize int) {
	srt.Lock()
	defer srt.Unlock()

	srt.initialQueueSize = initialQueueSize
}

func (srt *SecretReconcileTracker) trackItemReconcile() {
	srt.Lock()
	defer srt.Unlock()

	srt.itemsReconciled += 1
}

func (srt *SecretReconcileTracker) initialQueueItemsReconciled() bool {
	srt.RLock()
	defer srt.RUnlock()
	return srt.itemsReconciled >= srt.initialQueueSize
}

// controller implementation for Secret resources
type secretController struct {
	logger *zap.SugaredLogger

	// secret controller watching for secrets in this namespace
	namespace string

	// informers for the controller
	secretInformer corev1informers.SecretInformer

	// namespace Filter of the local (primary) cluster
	namespaceFilter controllers_api.NamespaceFilter

	// template renderer
	renderer templating.TemplateRenderer

	// resource manager
	resourceManager resources.ResourceManager

	// work queue for the controller
	workQueue workqueue.RateLimitingInterface

	// primary cluster
	primaryClusterID  cluster.ID
	primaryKubeClient kube.Client

	// informer rescan period
	rescanPeriod time.Duration

	cs controllers_api.ClusterManager

	objectReader objectReader

	upsertSecretHandler upsertSecretHandler
	deleteSecretHandler deleteSecretHandler

	createRemoteClusterHandler createRemoteClusterHandler
	addClusterHandler          addClusterHandler
	deleteClusterHandler       deleteClusterHandler

	buildClientFromKubeConfigHandler kube.BuildClientFromKubeConfigHandler

	mopArgs         common.MopArgs
	applicator      templating.Applicator
	metricsRegistry *prometheus.Registry

	multiclusterServiceController controllers_api.MulticlusterController
	multiclusterIngressController controllers_api.MulticlusterController
	serverlessServiceController   controllers_api.MulticlusterController
	tspController                 controllers_api.MulticlusterController
	mopReconcileTracker           ReconcileManager
	additionalObjectManager       AdditionalObjectManager
	mutatingTemplatesManager      templating.TemplatesManager

	controllerCfg *common.ControllerConfig

	// tracker for reconciled secret keys
	secretReconcileTracker *SecretReconcileTracker

	// ProxyAccessController for dynamic cluster registration (can be nil if feature disabled)
	proxyAccessController controllers_api.ProxyAccessController
}

// NewSecretController - returns a new secret controller
func NewSecretController(namespace string,
	secretInformer corev1informers.SecretInformer,
	namespaceFilter controllers_api.NamespaceFilter,
	renderer templating.TemplateRenderer,
	multiClusterReconcileTracker ReconcileManager,
	resourceManager resources.ResourceManager,
	kubeClient kube.Client,
	clusterID cluster.ID,
	mopArgs common.MopArgs,
	applicator templating.Applicator,
	metricsRegistry *prometheus.Registry,
	buildClientFromKubeConfigHandler kube.BuildClientFromKubeConfigHandler,
	clusterStore controllers_api.ClusterManager,
	additionalObjectManager AdditionalObjectManager,
	multiclusterServiceController controllers_api.MulticlusterController,
	multiclusterIngressController controllers_api.MulticlusterController,
	serverlessServiceController controllers_api.MulticlusterController,
	tspController controllers_api.MulticlusterController,
	mutatingTemplatesManager templating.TemplatesManager,
	controllerCfg *common.ControllerConfig,
	proxyAccessController controllers_api.ProxyAccessController) *secretController {

	logger := common.CreateNewLogger(mopArgs.LogLevel)
	ctxLogger := logger.With("controller", "SecretController", "cluster", clusterID)

	var mopReconcileTracker ReconcileManager = &NoOpReconcileManager{}
	if features.EnableMopConcurrencyControl {
		mopReconcileTracker = NewMultiClusterReconcileTracker(logger)
	}

	controller := &secretController{
		logger:    ctxLogger,
		namespace: namespace,

		namespaceFilter: namespaceFilter,
		renderer:        renderer,
		resourceManager: resourceManager,

		primaryClusterID:  clusterID,
		primaryKubeClient: kubeClient,
		cs:                clusterStore,

		secretInformer: secretInformer,
		workQueue:      createWorkQueue("Secrets"),

		mopArgs:                          mopArgs,
		applicator:                       applicator,
		metricsRegistry:                  metricsRegistry,
		buildClientFromKubeConfigHandler: buildClientFromKubeConfigHandler,
		mopReconcileTracker:              mopReconcileTracker,
		additionalObjectManager:          additionalObjectManager,
		multiclusterServiceController:    multiclusterServiceController,
		multiclusterIngressController:    multiclusterIngressController,
		serverlessServiceController:      serverlessServiceController,
		tspController:                    tspController,
		mutatingTemplatesManager:         mutatingTemplatesManager,
		controllerCfg:                    controllerCfg,
		secretReconcileTracker:           NewSecretReconcileTracker(),
		proxyAccessController:            proxyAccessController,
	}

	controller.objectReader = controller.readObject
	controller.upsertSecretHandler = controller.handleUpsert
	controller.deleteSecretHandler = controller.handleDelete

	controller.createRemoteClusterHandler = controller.createRemoteCluster
	controller.addClusterHandler = controller.addCluster
	controller.deleteClusterHandler = controller.cs.DeleteClusterForKey

	secretInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.IsMultiClusterLabeledSecret,
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				secret := obj.(*corev1.Secret)
				logger.Infow("informer event: Add",
					"namespace", secret.GetNamespace(),
					"secretName", secret.Name,
					"initialQueueSize", controller.secretReconcileTracker.initialQueueSize,
					"reconciled", controller.secretReconcileTracker.itemsReconciled,
					"currentQueue", controller.workQueue.Len())
				// Use EventUpdate to facilitate queue de-duping
				enqueue(controller.workQueue, clusterID.String(), obj, controllers_api.EventUpdate)
			},
			UpdateFunc: func(old, new interface{}) {
				newSecret := new.(*corev1.Secret)
				oldSecret := old.(*corev1.Secret)
				logger.Infof("informer event: Update",
					"namespace", newSecret.GetNamespace(),
					"secretName", newSecret.Name,
					"currentQueue", controller.workQueue.Len(),
					"oldRV", oldSecret.ResourceVersion,
					"newRv", newSecret.ResourceVersion,
				)

				if newSecret.ResourceVersion == oldSecret.ResourceVersion {
					return
				}
				enqueue(controller.workQueue, clusterID.String(), new, controllers_api.EventUpdate)
			},
			DeleteFunc: func(obj interface{}) {
				enqueue(controller.workQueue, clusterID.String(), obj, controllers_api.EventDelete)
			},
		},
	})

	return controller
}

func (c *secretController) IsMultiClusterLabeledSecret(obj interface{}) bool {
	secret := common.GetMetaObject(obj)
	// allow events only from namespace that secret controller is watching for
	if secret.GetNamespace() != c.namespace {
		return false
	}
	labels := secret.GetLabels()
	return labels[constants.MultiClusterSecretLabel] == "true"
}

func (c *secretController) Run(workerThreads int, stopCh <-chan struct{}) error {

	defer utilruntime.HandleCrash()
	defer c.workQueue.ShutDown()

	c.logger.Info("Starting multicluster remote secrets controller")
	t0 := time.Now()

	primaryCluster := c.createPrimaryCluster()
	c.cs.SetPrimaryCluster(primaryCluster)
	err := primaryCluster.Run(c.cs)
	if err != nil {
		return err
	}

	// Wait for the caches to be synced before starting workers
	c.logger.Info("waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh,
		c.secretInformer.Informer().HasSynced); !ok {
		return fmt.Errorf("failed to sync multicluster remote secrets controller cache")
	}

	c.logger.Infof("multicluster remote secrets controller cache synced in %v", time.Since(t0))

	c.secretReconcileTracker.setInitialQueueSize(c.workQueue.Len())
	c.logger.Infof("queue length is %v", c.workQueue.Len())

	onRetryHandler := OnControllerRetryHandler(
		commonmetrics.SecretRetries,
		c.metricsRegistry)

	c.logger.Info("starting workers")
	for i := 0; i < workerThreads; i++ {
		go wait.Until(func() {
			RunWorker(
				c.logger,
				c.workQueue,
				c.reconcile,
				onRetryHandler)
		},
			time.Second,
			stopCh)
	}

	c.logger.Info("started workers")

	c.startMulticlusterController("service", features.ServiceControllerThreads, c.multiclusterServiceController, stopCh)
	c.startMulticlusterController("knative-ingress", features.KnativeIngressControllerThreads, c.multiclusterIngressController, stopCh)
	c.startMulticlusterController("knative-serverlessservice", features.KnativeServerlessServiceControllerThreads, c.serverlessServiceController, stopCh)

	if features.EnableTrafficShardingPolicyController {
		c.startMulticlusterController("traffic-sharding-policy", features.TspControllerThreads, c.tspController, stopCh)
	}

	if features.EnableSidecarConfig {
		if err := c.proxyAccessController.Start(stopCh); err != nil {
			return fmt.Errorf("failed to start ProxyAccessController: %w", err)
		}
		defer c.proxyAccessController.Stop()
		c.logger.Info("ProxyAccessController started successfully")
	}

	c.logger.Info()
	<-stopCh
	c.logger.Info("shutting down workers")

	c.logger.Info("shutting down primary cluster")
	close(primaryCluster.stop)

	// note: stop the clusters before closing the secret controller
	c.logger.Info("shutting down remote clusters")
	c.cs.StopAllClusters()

	return nil
}

// allClustersInitialized - a callback method used by other controllers_api to make sure they don't start before all secrets/clusters have been processed.
// Method starts with value false and once all remote-cluster secrets have been processed - returns true.
func (c *secretController) allClustersInitialized() bool {
	return c.secretReconcileTracker.initialQueueItemsReconciled()
}

func (c *secretController) reconcile(item QueueItem) error {
	namespace, name, object, err := extractOrRestoreObject(item, c.objectReader)
	if err != nil {
		c.logger.Errorf("failed to extract object for key:%s : %q", item.key, err)
		return nil
	}

	if object == nil {
		c.logger.Debugf("secret %s does not exist in informer cache, deleting it", item.key)
		item.event = controllers_api.EventDelete
	}

	switch item.event {
	case controllers_api.EventUpdate:
		c.upsertSecretHandler(item.key, object.(*corev1.Secret), item.event)
		c.secretReconcileTracker.trackItemReconcile()
	case controllers_api.EventDelete:
		c.deleteSecretHandler(item.key)
		c.secretReconcileTracker.trackItemReconcile()
	default:
		c.logger.Errorw("unsupported event", "namespace", namespace, "name", name, "event", item.event)
	}
	return nil
}

func (c *secretController) createPrimaryCluster() *Cluster {
	logger := common.CreateNewLogger(c.mopArgs.LogLevel)
	ctxLogger := logger.With("cluster", c.primaryClusterID)
	return &Cluster{
		logger:                        ctxLogger,
		Client:                        c.primaryKubeClient,
		primaryKubeClient:             c.primaryKubeClient,
		ID:                            c.primaryClusterID,
		stop:                          make(chan struct{}),
		primary:                       true,
		namespaceFilter:               c.namespaceFilter,
		allNamespaces:                 c.mopArgs.AllNamespaces,
		renderer:                      c.renderer,
		applicator:                    c.applicator,
		resourceManager:               c.resourceManager,
		eventRecorder:                 kube.CreateEventRecorder(c.primaryKubeClient.Kube()),
		metricsRegistry:               c.metricsRegistry,
		dryRun:                        c.mopArgs.DryRun,
		mopReconcileTracker:           c.mopReconcileTracker,
		additionalObjectManager:       c.additionalObjectManager,
		multiclusterServiceController: c.multiclusterServiceController,
		multiclusterIngressController: c.multiclusterIngressController,
		serverlessServiceController:   c.serverlessServiceController,
		tspController:                 c.tspController,
		mutatingTemplatesManager:      c.mutatingTemplatesManager,
		controllerCfg:                 c.controllerCfg,
		proxyAccessController:         c.proxyAccessController,
	}
}

func (c *secretController) createRemoteCluster(kubeConfig []byte, clusterID string) (*Cluster, error) {
	logger := common.CreateNewLogger(c.mopArgs.LogLevel)
	ctxLogger := logger.With("cluster", clusterID)

	client, err := c.buildClientFromKubeConfigHandler(logger, clusterID, kubeConfig, c.rescanPeriod, c.controllerCfg.Selector, c.controllerCfg.InformerCacheSyncTimeoutSeconds, c.metricsRegistry, c.controllerCfg.KubeAPIQPS, c.controllerCfg.KubeAPIBurst)
	if err != nil {
		return nil, err
	}

	return &Cluster{
		logger:            ctxLogger,
		ID:                cluster.ID(clusterID),
		Client:            client,
		primaryKubeClient: c.primaryKubeClient,
		stop:              make(chan struct{}),
		allNamespaces:     c.mopArgs.AllNamespaces,
		applicator:        c.applicator,
		renderer:          c.renderer,
		resourceManager:   resources.NewServiceResourceManager(ctxLogger, c.primaryKubeClient.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas(), c.primaryKubeClient.MopApiClient(), c.primaryKubeClient.Dynamic(), c.primaryKubeClient.Discovery(), c.metricsRegistry),
		eventRecorder:     kube.CreateEventRecorder(client.Kube()),
		metricsRegistry:   c.metricsRegistry,
		dryRun:            c.mopArgs.DryRun,

		mopReconcileTracker:           c.mopReconcileTracker,
		additionalObjectManager:       c.additionalObjectManager,
		multiclusterServiceController: c.multiclusterServiceController,
		multiclusterIngressController: c.multiclusterIngressController,
		serverlessServiceController:   c.serverlessServiceController,
		tspController:                 c.tspController,
		mutatingTemplatesManager:      c.mutatingTemplatesManager,
		controllerCfg:                 c.controllerCfg,
		proxyAccessController:         c.proxyAccessController,
	}, nil
}

func (c *secretController) handleUpsert(secretKey string, s *corev1.Secret, event controllers_api.Event) {
	c.logger.Infof("handling %v event triggered for secret key %v", event, secretKey)

	// delete stale clusters which no longer exist in the secret key
	existingClusters := c.cs.GetExistingClustersForKey(secretKey)
	for _, existingCluster := range existingClusters {
		if _, ok := s.Data[string(existingCluster.GetId())]; !ok {
			c.logger.Infof("deleting stale cluster %v for secret %v", existingCluster.GetId(), secretKey)
			c.deleteClusterHandler(secretKey, existingCluster.GetId())
		}
	}

	for clusterID, kubeConfig := range s.Data {
		if cluster.ID(clusterID) == c.primaryClusterID {
			c.logger.Warnf("found primary cluster(%v) secrets in secret %v", clusterID, secretKey)
			c.logger.Infof("ignoring update for primary cluster %v from secret %v", clusterID, secretKey)
			continue
		}

		// no-op for ADD event
		c.deleteClusterHandler(secretKey, cluster.ID(clusterID))

		// skip creation of new cluster instance if cluster exists for a different secret key
		key, exists := c.cs.IsClusterExistsForOtherKey(secretKey, cluster.ID(clusterID))
		if exists {
			c.logger.Warnf("cluster %v already exist in secret key: %v. Ignoring cluster creation for secret key: %v", clusterID, key, secretKey)
			continue
		}

		remoteCluster, err := c.createRemoteClusterHandler(kubeConfig, clusterID)
		if err != nil {
			c.logger.Errorf("Error adding cluster_id=%v from secret=%v: %v", clusterID, secretKey, err)
			continue
		}
		err = c.addClusterHandler(secretKey, remoteCluster)
		if err != nil {
			c.logger.Errorf("Error adding remote cluster handler for id %s from secret %v: %v",
				clusterID, secretKey, err)
		}
	}
	c.logger.Infof("Number of remote clusters: %d", len(c.cs.GetExistingClusters()))
}

func (c *secretController) handleDelete(secretKey string) {
	c.logger.Debugf("handling DELETE event triggered for secret key %v", secretKey)
	defer func() {
		c.logger.Infof("Number of remote clusters: %d", len(c.cs.GetExistingClusters()))
	}()

	clustersList := c.cs.GetExistingClustersForKey(secretKey)
	for _, cls := range clustersList {
		if cls.GetId() == c.primaryClusterID {
			c.logger.Warnf("Found primary cluster %v in secret key %v. ignoring deletion for primary", cls.GetId(), secretKey)
			continue
		}
		c.deleteClusterHandler(secretKey, cls.GetId())
	}
	c.cs.DeleteClustersForKey(secretKey)
}

// adds the cluster in the cluster store and run the remote cluster
func (c *secretController) addCluster(secretKey string, cluster *Cluster) error {
	c.logger.Infof("Adding cluster_id=%v configured by secret=%v", cluster.ID, secretKey)
	c.cs.AddClusterForKey(secretKey, cluster)
	c.logger.Infof("Added cluster_id=%v configured by secret=%v", cluster.ID, secretKey)
	go func() {
		if err := cluster.Run(c.cs); err != nil {
			c.logger.Errorf("cluster %s initialization failed (secret=%s): %v", cluster.ID, secretKey, err)
		}
	}()
	return nil
}

func (c *secretController) readObject(namespace string, name string) (interface{}, error) {
	return c.secretInformer.Lister().Secrets(namespace).Get(name)
}

func (c *secretController) startMulticlusterController(name string, threads int, ctrl controllers_api.MulticlusterController, stopCh <-chan struct{}) {
	go wait.Until(func() {
		// Wait for clusters initialization to be complete
		c.logger.Info("waiting for secret controller to init all clusters...")
		if ok := cache.WaitForCacheSync(stopCh, c.allClustersInitialized); !ok {
			c.logger.Errorf("failed to wait for cluster controller to do initial clusters sync. Will retry again for controller: %s", name)
			return
		}
		c.logger.Info("clusters init sync completed")

		// Run/start test on the clusters to determine unhealthy ones
		c.cs.TestRemoteClustersOrRetry(stopCh)

		// Now, run controller
		err := ctrl.Run(threads, stopCh)
		if err != nil {
			c.logger.Error(err)
		}
	}, time.Second, stopCh)
	c.logger.Infof("started multicluster controller %s with %d threads", name, threads)
}

// Cluster defines cluster struct
type Cluster struct {
	logger *zap.SugaredLogger

	// ID of the cluster.
	ID cluster.ID

	// Client for accessing the cluster.
	Client kube.Client

	// primary cluster client
	primaryKubeClient kube.Client

	stop chan struct{}

	// true if cluster in context is primary
	primary bool

	// namespace Filter of the cluster
	namespaceFilter controllers_api.NamespaceFilter

	allNamespaces bool

	applicator templating.Applicator
	renderer   templating.TemplateRenderer

	resourceManager resources.ResourceManager
	eventRecorder   record.EventRecorder
	metricsRegistry *prometheus.Registry

	dryRun bool

	mopReconcileTracker           ReconcileManager
	additionalObjectManager       AdditionalObjectManager
	multiclusterServiceController controllers_api.MulticlusterController
	multiclusterIngressController controllers_api.MulticlusterController
	serverlessServiceController   controllers_api.MulticlusterController
	tspController                 controllers_api.MulticlusterController
	mutatingTemplatesManager      templating.TemplatesManager

	// cluster specific enqueuers
	mopEnqueuer controllers_api.ObjectEnqueuer

	// common controller configuration
	controllerCfg *common.ControllerConfig

	// ProxyAccessController for dynamic cluster registration (can be nil if feature disabled)
	proxyAccessController controllers_api.ProxyAccessController
}

func (r *Cluster) GetId() cluster.ID {
	return r.ID
}

func (r *Cluster) Stop() {
	close(r.stop)
	r.logger.Infof("cluster stopeed: %s", r.ID)
}

// Run starts the cluster's informers and waits for caches to sync.
// This should be called after each of the controllers_api have registered their respective informers, and should be run in a goroutine.
func (r *Cluster) Run(manager controllers_api.ClusterManager) error {
	// ns filter is already up and running for primary cluster
	if !r.IsPrimary() {
		if err := r.initNamespaceFilter(); err != nil {
			return err
		}
	}
	return r.initControllers(r.stop, manager)
}

func (r *Cluster) initNamespaceFilter() error {
	r.logger.Debugf("Initializing namespace filter")
	errorHandler := NewWatchErrorHandlerWithMetrics(r.logger, r.ID.String(), "namespaces", r.metricsRegistry)
	namespaceInformer := r.Client.KubeInformerFactory().Core().V1().Namespaces()
	_ = namespaceInformer.Informer().SetWatchErrorHandler(errorHandler)

	namespaceFilter := NewNamespaceFilter(r.logger, r.allNamespaces, namespaceInformer)

	// Start namespace filter (which carries NS informer) so that namespace information
	// is available by the time we enable service processing.
	if ok := namespaceFilter.Run(r.stop); !ok {
		return fmt.Errorf("unable to sync namespaces cache for cluster %s", r.ID)
	}
	r.namespaceFilter = namespaceFilter
	r.logger.Debugf("namespace filter initialization completed")
	return nil
}

func (r *Cluster) initControllers(stop <-chan struct{}, clusterManager controllers_api.ClusterManager) error {
	r.logger.Infof("Starting controllers_api")

	// A cluster specific mop queue/enqueuer
	mopEnqueuer := NewSingleQueueEnqueuer(
		createWorkQueueWithWatchdog(context.Background(), "MeshOperators", r.logger, r.metricsRegistry),
	)
	r.mopEnqueuer = mopEnqueuer

	err := r.additionalObjectManager.AddIndexersToSvcInformer(r.Client)
	if err != nil {
		return fmt.Errorf("failed to create additional-object indexer for svc: %w", err)
	}

	err = r.multiclusterServiceController.OnNewClusterAdded(r)
	if err != nil {
		return fmt.Errorf("failed to add new cluster %s to service controller: %w", r.ID, err)
	}

	// For maintaining backward compatibility, we will have to switch to EnableKnativeControllers flag once we go through one round of deployments.
	if features.EnableKnativeIngressController {
		err := r.multiclusterIngressController.OnNewClusterAdded(r)
		if err != nil {
			return fmt.Errorf("failed to add new cluster %s to knative ingress controller: %w", r.ID, err)
		}
	}
	if features.EnableKnativeControllers {
		err = r.serverlessServiceController.OnNewClusterAdded(r)
		if err != nil {
			return fmt.Errorf("failed to add new cluster %s to knative serverless service controller: %w", r.ID, err)
		}
	}

	if features.EnableTrafficShardingPolicyController {
		err = r.tspController.OnNewClusterAdded(r)
		if err != nil {
			return fmt.Errorf("failed to add new cluster %s to TSP controller: %w", r.ID, err)
		}
	}

	if features.EnableSidecarConfig {
		err = r.proxyAccessController.OnNewClusterAdded(r)
		if err != nil {
			return fmt.Errorf("failed to add new cluster %s to ProxyAccessController: %w", r.ID, err)
		}
	}

	var seController Controller
	var msmController Controller
	var seEnqueuer controllers_api.QueueAccessor
	var sidecarConfigController Controller
	var sidecarConfigEnqueuer controllers_api.QueueAccessor
	if r.IsPrimary() {
		seEnqueuer = NewSingleQueueEnqueuer(
			createWorkQueueWithWatchdog(context.Background(), "ServiceEntries", r.logger, r.metricsRegistry))

		seController = NewServiceEntryController(
			r.logger,
			r.applicator,
			r.renderer,
			r.resourceManager,
			r.eventRecorder,
			r.namespaceFilter,
			r.metricsRegistry,
			seEnqueuer,
			r.mopEnqueuer,
			r.ID.String(),
			r.Client,
			r.primaryKubeClient,
			r.dryRun)

		if features.EnableSidecarConfig {
			sidecarConfigEnqueuer = NewSingleQueueEnqueuer(
				createWorkQueueWithWatchdog(context.Background(), "SidecarConfigs", r.logger, r.metricsRegistry))

			sidecarConfigController = NewSidecarConfigController(
				r.logger,
				r.namespaceFilter,
				r.eventRecorder,
				r.applicator,
				r.renderer,
				r.metricsRegistry,
				r.ID.String(),
				r.Client,
				sidecarConfigEnqueuer,
				r.dryRun,
				NewMultiClusterReconcileTracker(r.logger),
			)
		}

		if features.EnableStaleMsmCleanup {
			msmController = NewMeshServiceMetadataController(
				r.logger,
				r.Client,
				r.ID.String(),
				clusterManager,
				r.resourceManager,
				r.metricsRegistry)
		}
		err := r.additionalObjectManager.CreateInformersForAddObjs(r.Client, clusterManager, r.logger)
		if err != nil {
			return fmt.Errorf("failed to add additional object informers in primary cluster: %w", err)
		}
	}

	mopController := NewMeshOperatorController(
		r.logger,
		r.namespaceFilter,
		r.eventRecorder,
		r.applicator,
		r.renderer,
		r.metricsRegistry,
		r.ID.String(),
		r.Client,
		r.primaryKubeClient,
		mopEnqueuer,
		r.multiclusterServiceController.GetObjectEnqueuer(),
		seEnqueuer,
		r.resourceManager,
		r.dryRun,
		r.primary,
		r.mopReconcileTracker,
		r.additionalObjectManager,
		&common.RealTimeProvider{})

	// start informer factories
	r.logger.Infof("Starting informer factories for cluster %s", r.ID.String())

	additionalInformers := []informers.GenericInformer{}
	if r.IsPrimary() {
		additionalInformers = r.additionalObjectManager.GetInformers()
	}

	err = r.Client.RunAndWait(stop, r.IsPrimary(), additionalInformers)
	if err != nil {
		return fmt.Errorf("failed to wait for informers to sync in cluster %s: %w", r.ID, err)
	}

	if r.IsPrimary() {
		go wait.Until(func() {
			err = seController.Run(features.ServiceEntryControllerThreads, stop)
			if err != nil {
				r.logger.Error(err)
			}
		}, time.Second, stop)
		if features.EnableSidecarConfig {
			go wait.Until(func() {
				err = sidecarConfigController.Run(features.SidecarConfigControllerThreads, stop)
				if err != nil {
					r.logger.Error(err)
				}
			}, time.Second, stop)
		}
		if features.EnableStaleMsmCleanup {
			go wait.Until(func() {
				err = msmController.Run(features.MsmControllerThreads, stop)
				if err != nil {
					r.logger.Error()
				}
			}, time.Second, stop)
		}
	}
	go wait.Until(func() {
		err = mopController.Run(features.MeshOperatorControllerThreads, stop)
		if err != nil {
			r.logger.Error(err)
		}
	}, time.Second, stop)

	r.logger.Infof("Started controllers_api")

	return nil
}

func (r *Cluster) IsPrimary() bool {
	return r.primary
}

func (r *Cluster) GetKubeClient() kube.Client {
	return r.Client
}

func (r *Cluster) GetMulticlusterServiceController() controllers_api.MulticlusterController {
	return r.multiclusterServiceController
}

func (r *Cluster) GetMopEnqueuer() controllers_api.ObjectEnqueuer {
	return r.mopEnqueuer
}

func (r *Cluster) GetEventRecorder() record.EventRecorder {
	return r.eventRecorder
}

func (r *Cluster) GetNamespaceFilter() controllers_api.NamespaceFilter {
	return r.namespaceFilter
}
