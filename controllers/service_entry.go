package controllers

import (
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	"github.com/istio-ecosystem/mesh-operator/common/pkg/metrics"
	"github.com/istio-ecosystem/mesh-operator/pkg/reconcilemetadata"

	"github.com/google/uuid"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	meshOpErrors "github.com/istio-ecosystem/mesh-operator/pkg/errors"
	"github.com/istio-ecosystem/mesh-operator/pkg/generated/listers/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/matching"
	"k8s.io/apimachinery/pkg/labels"

	mopv1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/apimachinery/pkg/runtime"

	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/istio-ecosystem/mesh-operator/pkg/resources"

	"github.com/istio-ecosystem/mesh-operator/pkg/transition"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	"github.com/prometheus/client_golang/prometheus"

	"reflect"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	"github.com/istio-ecosystem/mesh-operator/pkg/templating"

	"go.uber.org/zap"
	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	istioinformersv1alpha3 "istio.io/client-go/pkg/informers/externalversions/networking/v1alpha3"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
)

type seUpsertHandler func(obj interface{}, logger *zap.SugaredLogger) error

type serviceEntryDeletionHandler func(clusterName string, namespace string, name string, uid types.UID, logger *zap.SugaredLogger) error

type serviceEntryController struct {
	logger     *zap.SugaredLogger
	seEnqueuer controllers_api.QueueAccessor
	seInformer istioinformersv1alpha3.ServiceEntryInformer

	namespaceFilter controllers_api.NamespaceFilter
	recorder        record.EventRecorder

	resourceManager resources.ResourceManager

	configGenerator ConfigGenerator

	objectReader            objectReader
	upsertMeshConfigHandler seUpsertHandler
	deleteMeshConfigHandler serviceEntryDeletionHandler

	metricsRegistry *prometheus.Registry

	mopLister v1alpha1.MeshOperatorLister

	mopEnqueuer controllers_api.ObjectEnqueuer

	clusterName   string
	client        kube.Client
	primaryClient kube.Client

	reconcileOptimizer ReconcileOptimizer
	dryRun             bool
}

func NewServiceEntryController(
	logger *zap.SugaredLogger,
	applicator templating.Applicator,
	renderer templating.TemplateRenderer,
	resourceManager resources.ResourceManager,
	eventRecorder record.EventRecorder,
	namespaceFilter controllers_api.NamespaceFilter,
	metricsRegistry *prometheus.Registry,
	seEnqueuer controllers_api.QueueAccessor,
	mopEnqueuer controllers_api.ObjectEnqueuer,
	clusterName string,
	client kube.Client,
	primaryClient kube.Client,
	dryRun bool) Controller {

	ctxLogger := logger.With("controller", "ServiceEntries")

	seInformer := client.IstioInformerFactory().Networking().V1alpha3().ServiceEntries()
	errorHandler := NewWatchErrorHandlerWithMetrics(ctxLogger, clusterName, "serviceentries", metricsRegistry)
	_ = seInformer.Informer().SetWatchErrorHandler(errorHandler)

	configMutators := []templating.Mutator{
		&templating.ManagedByMutator{},
		&templating.OwnerRefMutator{},
		&templating.ConfigResourceParentMutator{},
	}

	comparer := transition.NewMeshConfigComparer(logger, primaryClient, metricsRegistry, clusterName)

	controller := &serviceEntryController{
		logger: ctxLogger,

		seInformer:         seInformer,
		namespaceFilter:    namespaceFilter,
		seEnqueuer:         seEnqueuer,
		recorder:           eventRecorder,
		resourceManager:    resourceManager,
		metricsRegistry:    metricsRegistry,
		clusterName:        clusterName,
		client:             client,
		primaryClient:      primaryClient,
		mopEnqueuer:        mopEnqueuer,
		dryRun:             dryRun,
		configGenerator:    NewConfigGenerator(renderer, configMutators, comparer, applicator, templating.ApplyOverlays, ctxLogger, metricsRegistry),
		mopLister:          client.MopInformerFactory().Mesh().V1alpha1().MeshOperators().Lister(),
		reconcileOptimizer: NewServiceReconcileOptimizer(primaryClient.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas(), reconcilemetadata.NewReconcileHashManager()),
	}

	controller.objectReader = controller.readObject
	controller.upsertMeshConfigHandler = controller.createMeshConfig
	controller.deleteMeshConfigHandler = controller.deleteMeshConfig

	seInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: namespaceFilter.IsNamespaceMopEnabledForObject,
		Handler: cache.ResourceEventHandlerDetailedFuncs{
			AddFunc: func(obj interface{}, isInInitialList bool) {
				se := obj.(*istiov1alpha3.ServiceEntry)
				if isInInitialList && !controller.reconcileOptimizer.ShouldReconcile(controller.logger, obj, clusterName, "ServiceEntry") {
					controller.logger.Infow("Reconcile skipped for ServiceEntry due to startup reconcile optimization", "namespace", se.Namespace, "serviceEntryName", se.Name)
					commonmetrics.EmitObjectsConfiguredStats(metricsRegistry, clusterName, se.Namespace, se.Name, "ServiceEntry", constants.NotAvailable)
					metrics.GetOrRegisterCounterWithLabels(commonmetrics.ReconcileSkipped, map[string]string{commonmetrics.ClusterLabel: clusterName, commonmetrics.NamespaceLabel: se.Namespace, commonmetrics.ResourceNameLabel: se.Name}, metricsRegistry).Inc()
				} else {
					seEnqueuer.Enqueue(clusterName, obj, controllers_api.EventAdd)
					controller.enqueueMopReferencingGivenServiceEntry(se)
				}
			},
			UpdateFunc: func(old, new interface{}) {
				newServiceEntry := new.(*istiov1alpha3.ServiceEntry)
				oldServiceEntry := old.(*istiov1alpha3.ServiceEntry)
				if newServiceEntry.ResourceVersion == oldServiceEntry.ResourceVersion {
					// Periodic resync will send update events for all known service entries.
					// Two different versions of the same Service will always have different RVs.
					return
				}
				seEnqueuer.Enqueue(clusterName, new, controllers_api.EventUpdate)
				controller.enqueueMopReferencingGivenServiceEntry(oldServiceEntry)
				controller.enqueueMopReferencingGivenServiceEntry(newServiceEntry)
			},
			DeleteFunc: func(obj interface{}) {
				se := obj.(*istiov1alpha3.ServiceEntry)
				seEnqueuer.Enqueue(clusterName, obj, controllers_api.EventDelete)
				controller.enqueueMopReferencingGivenServiceEntry(se)
			}},
	})
	return controller
}

func (c *serviceEntryController) Run(workerThreads int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.seEnqueuer.GetQueue().ShutDown()

	// Start the informer factories to begin populating the informer caches
	c.logger.Info("starting service-entry controller")

	// Wait for the caches to be synced before starting workers
	c.logger.Info("waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh,
		c.seInformer.Informer().HasSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	onRetryHandler := OnControllerRetryHandler(
		commonmetrics.ServiceEntryRetries,
		c.metricsRegistry)

	c.logger.Info("starting workers")
	// Launch workers to process Service resources
	for i := 0; i < workerThreads; i++ {
		go wait.Until(func() {
			RunWorker(
				c.logger,
				c.seEnqueuer.GetQueue(),
				c.reconcile,
				onRetryHandler)
		},
			time.Second,
			stopCh)
	}

	c.logger.Info("started workers")
	<-stopCh
	c.logger.Info("shutting down workers")

	return nil
}

func (c *serviceEntryController) reconcile(item QueueItem) error {
	if !features.EnableRoutingConfig {
		return nil
	}

	defer IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.ServiceEntryReconciledTotal, c.clusterName, item)
	namespace, name, object, err := extractOrRestoreObject(item, c.objectReader)
	if err != nil {
		c.logger.Errorf("failed to extract object for key:%s : %q", item.key, err)
		return nil
	}

	reconcileId := uuid.New()
	seCtxLogger := c.logger.With("seNamespace", namespace, "serviceEntry", name, "seReconcileId", reconcileId)

	if item.event == controllers_api.EventDelete || (object != nil && common.IsOperatorDisabled(object)) {
		if item.event != controllers_api.EventDelete && !features.CleanupDisabledConfig {
			seCtxLogger.Infof("operator disabled for ServiceEntry, mesh config won't be generated: %s/%s", namespace, name)
			return nil
		} else {
			seCtxLogger.Infof("operator disabled for ServiceEntry. Deleting its mesh-config: %s/%s", namespace, name)
		}
		err = c.deleteMeshConfigHandler(c.clusterName, namespace, name, item.uid, seCtxLogger)
	} else if item.event == controllers_api.EventAdd || item.event == controllers_api.EventUpdate {
		if object == nil {
			seCtxLogger.Warnf("object '%s' in work queue no longer exists", item.key)
			return nil
		}
		err = c.upsertMeshConfigHandler(object, seCtxLogger)
	} else {
		seCtxLogger.Errorw(
			"unsupported event",
			"namespace", namespace,
			"name", name,
			"event", item.event)
	}

	if err != nil {
		if meshOpErrors.IsNonCriticalReconcileError(err) {
			// Non-critical error. Just retry.
			IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.ServiceEntryReconcileNonCriticalError, c.clusterName, item)
			return err
		}
		IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.ServiceEntryReconcileFailed, c.clusterName, item)
		seCtxLogger.Debugf("recording event %d for %s/%s", item.event, namespace, name)
		if item.event != controllers_api.EventDelete {
			c.recorder.Event(object.(runtime.Object), corev1.EventTypeWarning, constants.MeshConfigError, err.Error())
		}
	}
	return err
}

func (c *serviceEntryController) readObject(namespace string, name string) (interface{}, error) {
	return c.seInformer.Lister().ServiceEntries(namespace).Get(name)
}

func (c *serviceEntryController) createMeshConfig(obj interface{}, ctxLogger *zap.SugaredLogger) error {
	serviceEntry, ok := obj.(*istiov1alpha3.ServiceEntry)
	if !ok {
		return fmt.Errorf("object of a wrong type received %s", reflect.TypeOf(obj))
	}
	// GVK needs to be set explicitly in order to make sure SE.TypeMetadata field is set (TypeMetadata is used further while converting SE to unstructured object type)
	serviceEntry.SetGroupVersionKind(schema.GroupVersionKind{Group: constants.ServiceEntryKind.Group, Version: constants.ServiceEntryKind.Version, Kind: constants.ServiceEntryKind.Kind})

	var mops []*mopv1.MeshOperator
	var err error

	// get all relevant mops
	if serviceEntry != nil && c.mopLister != nil {
		mops, err = matching.ListMopsForServiceEntry(serviceEntry, c.listAllMopForGivenNamespace)
		if err != nil {
			c.logger.Errorf("error while listing matching MOPs for service %s/%s : %v", serviceEntry.Namespace, serviceEntry.Namespace, err)
			return err
		}
	}

	isServiceEntryTracked := true
	trackedOwner := resources.ServiceEntryToReference(c.clusterName, serviceEntry)
	msm, err := c.resourceManager.TrackOwner(c.clusterName, serviceEntry.Namespace, trackedOwner, ctxLogger)
	if err != nil {
		isServiceEntryTracked = false
		c.logger.Errorf("encountered error while tracking service entry: %v", err)
	}

	configObjects, err := c.generateConfig(serviceEntry, msm, mops, ctxLogger)
	if err != nil && !meshOpErrors.IsOverlayingError(err) {
		return err
	}

	if isServiceEntryTracked {
		trackingError := c.resourceManager.OnOwnerChange(c.clusterName, c.client, msm, serviceEntry, trackedOwner, templating.GetConfigObjects(configObjects), ctxLogger)
		return common.GetFirstNonNil(err, trackingError)
	}

	return err
}

func (c *serviceEntryController) generateConfig(serviceEntry *istiov1alpha3.ServiceEntry, msm *mopv1.MeshServiceMetadata, mops []*mopv1.MeshOperator, ctxLogger *zap.SugaredLogger) ([]*templating.AppliedConfigObject, error) {

	object, err := kube.ObjectToUnstructured(serviceEntry)
	if err != nil {
		return nil, fmt.Errorf("failed to convert %s object %s/%s to unstructured Object, error: %w", serviceEntry.Kind, serviceEntry.Namespace, serviceEntry.Name, err)
	}

	mopsWithValidOverlays := make([]*mopv1.MeshOperator, 0)
	mopNameToOverlay := make(map[string][]mopv1.Overlay, 0)
	mopNamesWithValidOverlays := make([]string, 0)

	if features.EnableServiceEntryOverlays && len(mops) > 0 {
		for _, mop := range mops {
			overlaysInMop := mop.Spec.Overlays
			if len(overlaysInMop) > 0 {
				err = validateOverlayingMop(mop)
				if err != nil {
					c.logger.Errorw("mop record failed validation", "error", err)
					err = c.updateOverlayValidationErrorStatus(mop, err)
					continue
				}
				mopsWithValidOverlays = append(mopsWithValidOverlays, mop)
				mopNamesWithValidOverlays = append(mopNamesWithValidOverlays, mop.Name)
				mopNameToOverlay[mop.Name] = overlaysInMop
			}
		}
	}

	renderCtx := templating.NewRenderRequestContext(object, map[string]string{}, MsmToOwnerRef(msm),
		c.clusterName, "", mopNameToOverlay, nil)

	ctxLogger.Debugf("generating configs for all valid overlaying MOPs %v targeting the service", mopNamesWithValidOverlays)
	applicatorResults, generateConfigErr := c.configGenerator.GenerateConfig(&renderCtx, NoOpOnBeforeApply, ctxLogger)

	mopStatusUpdateErr := c.updateOverlayingMopStatus(serviceEntry, mopsWithValidOverlays, generateConfigErr, ctxLogger)

	if mopStatusUpdateErr != nil {
		// Fail and retry if failed to update MOP status.
		return nil, mopStatusUpdateErr
	}

	if generateConfigErr != nil {
		// The applicator results are meaningful even if the overlays cannot be applied.
		if meshOpErrors.IsOverlayingError(generateConfigErr) {
			return applicatorResults, generateConfigErr
		}
		return nil, generateConfigErr
	}

	for _, result := range applicatorResults {
		if result.Error != nil {
			obj := result.Object
			return nil, fmt.Errorf("failed to upsert object %s.%s of kind %v: %w",
				obj.GetName(), obj.GetNamespace(), obj.GroupVersionKind(), result.Error)
		}
	}

	return applicatorResults, nil
}

func (c *serviceEntryController) deleteMeshConfig(clusterName, namespace, name string, uid types.UID, ctxLogger *zap.SugaredLogger) error {
	ctxLogger.Infof("deleting meshconfig for ServiceEntry: %s/%s/%s/%s", clusterName, namespace, name, uid)
	return c.resourceManager.UnTrackOwner(clusterName, namespace, constants.ServiceEntryKind.Kind, name, uid, ctxLogger)
}

func (c *serviceEntryController) listAllMopForGivenNamespace(namespace string) ([]*mopv1.MeshOperator, error) {
	return c.mopLister.MeshOperators(namespace).List(labels.Everything())
}

func (c *serviceEntryController) updateOverlayValidationErrorStatus(mop *mopv1.MeshOperator, validationError error) error {
	c.logger.Debugf("updating validation results for mop %v", mop.Name)
	mop.Status = mopv1.MeshOperatorStatus{
		Phase:   PhaseFailed,
		Message: validationError.Error(),
	}

	recordOverlayingMopRenderingStats(mop, c.metricsRegistry, c.clusterName, constants.ServiceEntryKind.Kind)

	err := updateMopStatusInTheCluster(mop, c.client.MopApiClient(), c.logger)
	return err
}

func (c *serviceEntryController) updateOverlayingMopStatus(serviceEntry *istiov1alpha3.ServiceEntry, mopsWithOverlays []*mopv1.MeshOperator, generateConfigErr error, ctxLogger *zap.SugaredLogger) error {
	var err error

	var mopErrorMap map[string]*meshOpErrors.OverlayErrorInfo
	var overlayingError *meshOpErrors.OverlayingErrorImpl
	if generateConfigErr != nil && meshOpErrors.IsOverlayingError(generateConfigErr) {
		overlayingError, _ = generateConfigErr.(*meshOpErrors.OverlayingErrorImpl)
		mopErrorMap = overlayingError.ErrorMap
	}

	for _, mop := range mopsWithOverlays {
		ctxLogger.Debugf("updating status for overlaying mop %v", mop.Name)
		var updatedMop *mopv1.MeshOperator

		if errInfo, exists := mopErrorMap[mop.Name]; exists {
			updatedMop, _ = updateMopStatusForObject(mop, serviceEntry.Kind, serviceEntry.Name, PhaseFailed, getOverlayFailureSvcMessage(mop.Name, errInfo), "issues with applying overlay")
		} else {
			updatedMop, _ = updateMopStatusForObject(mop, serviceEntry.Kind, serviceEntry.Name, PhaseSucceeded, "", "")
		}

		noPendingPhases := true
		noFailedPhases := true

		for _, seStatus := range mop.Status.ServiceEntries {
			if seStatus.Phase == PhasePending {
				noPendingPhases = false
			}
			if seStatus.Phase == PhaseFailed {
				noFailedPhases = false
			}
		}

		for _, svcStatus := range mop.Status.Services {
			if svcStatus.Phase == PhasePending {
				noPendingPhases = false
			}
			if svcStatus.Phase == PhaseFailed {
				noFailedPhases = false
			}
		}

		// check if all serviceEntries in mop.status are posted
		if noPendingPhases && noFailedPhases {
			updateRootStatus(mop, PhaseSucceeded, "")
		}
		if noPendingPhases { // this is the last iteration for the MOP, record stats
			recordOverlayingMopRenderingStats(updatedMop, c.metricsRegistry, c.clusterName, constants.ServiceEntryKind.Kind)
		}
		err = updateMopStatusInTheCluster(updatedMop, c.client.MopApiClient(), c.logger)
	}
	return err
}

func (c *serviceEntryController) enqueueMopReferencingGivenServiceEntry(serviceEntry *istiov1alpha3.ServiceEntry) {
	mops, err := matching.ListMopsForServiceEntry(serviceEntry, c.listAllMopForGivenNamespace)
	if err != nil {
		c.logger.Errorf("error while listing matching MOPs for serviceEntry %s/%s : %v", serviceEntry.Namespace, serviceEntry.Namespace, err)
		return
	}
	for _, mop := range mops {
		if isOverlayingMop(mop) && common.IsOperatorDisabled(serviceEntry) {
			continue
		}
		c.mopEnqueuer.Enqueue(c.clusterName, mop, controllers_api.EventUpdate)
	}
}
