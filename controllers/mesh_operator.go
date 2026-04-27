package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"

	"github.com/google/uuid"

	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/patch"

	meshOpErrors "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/resources"

	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/matching"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/hash"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/generated/clientset/versioned"

	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/generated/listers/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
	"k8s.io/client-go/tools/record"

	meshv1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
)

// Phases are a list of verbs/adverbs to describe the current state.
const (
	PhasePending     meshv1alpha1.Phase = "Pending"
	PhaseSucceeded   meshv1alpha1.Phase = "Succeeded"
	PhaseFailed      meshv1alpha1.Phase = "Failed"
	PhaseTerminating meshv1alpha1.Phase = "Terminating"
	_                meshv1alpha1.Phase = "Unknown"

	FinalizerName = "mesh.io/mesh-operator"

	noServicesForSelectorMessage = "No mesh-enabled Services or ServiceEntries selected by the serviceSelector"
)

type meshOperatorController struct {
	logger          *zap.SugaredLogger
	namespaceFilter controllers_api.NamespaceFilter

	mopClient   versioned.Interface
	mopInformer cache.SharedIndexInformer
	mopLister   v1alpha1.MeshOperatorLister

	objectReader       objectReader
	servicesReader     matching.ServiceReader
	serviceEntryReader matching.ServiceEntryReader

	recorder record.EventRecorder

	configGenerator ConfigGenerator

	clusterName     string
	metricsRegistry *prometheus.Registry

	mopEnqueuer     controllers_api.QueueAccessor
	serviceEnqueuer controllers_api.ObjectEnqueuer
	seEnqueuer      controllers_api.ObjectEnqueuer
	resourceManager resources.ResourceManager

	client        kube.Client
	primaryClient kube.Client

	dryRun           bool
	reconcileTracker ReconcileManager

	additionalObjManager AdditionalObjectManager

	timeProvider common.TimeProvider
}

func NewMeshOperatorController(logger *zap.SugaredLogger,
	namespaceFilter controllers_api.NamespaceFilter,
	recorder record.EventRecorder,
	applicator templating.Applicator,
	renderer templating.TemplateRenderer,
	metricsRegistry *prometheus.Registry,
	clusterName string,
	client kube.Client,
	primaryClient kube.Client,
	mopEnqueuer controllers_api.QueueAccessor,
	serviceEnqueuer controllers_api.ObjectEnqueuer,
	seEnqueuer controllers_api.ObjectEnqueuer,
	resourceManager resources.ResourceManager,
	dryRun bool,
	primary bool,
	reconcileTracker ReconcileManager,
	additionalObjManager AdditionalObjectManager,
	timeProvider common.TimeProvider) Controller {

	ctxLogger := logger.With("controller", "MeshOperator", "cluster", clusterName)
	mopInformer := client.MopInformerFactory().Mesh().V1alpha1().MeshOperators().Informer()
	errorHandler := NewWatchErrorHandlerWithMetrics(ctxLogger, clusterName, "meshoperators", metricsRegistry)
	client.KubeInformerFactory().Core().V1().Services()
	_ = mopInformer.SetWatchErrorHandler(errorHandler)

	configMutators := []templating.Mutator{
		&templating.ManagedByMutator{},
		// Note: Name mutator depends on the hash being present, so ordering is important.
		&templating.ExtensionHashMutator{Logger: ctxLogger, KubeClient: primaryClient},
		&templating.ExtensionNameMutator{},
		&templating.ExtensionSourceMutator{KubeClient: primaryClient},
		&templating.ConfigNamespaceAnnotationCleanup{},
	}

	svcReader := matching.CreateServiceReader(ctxLogger, client)
	var seReader matching.ServiceEntryReader = nil
	if primary {
		// Ignore SEs in non-primary cluster
		seReader = matching.CreateServiceEntryReader(ctxLogger, client)
	} else {
		seReader = matching.CreateRemoteClusterServiceEntryReader()
	}
	controller := &meshOperatorController{
		logger:               ctxLogger,
		mopEnqueuer:          mopEnqueuer,
		namespaceFilter:      namespaceFilter,
		mopClient:            client.MopApiClient(),
		mopInformer:          mopInformer,
		mopLister:            client.MopInformerFactory().Mesh().V1alpha1().MeshOperators().Lister(),
		servicesReader:       svcReader,
		serviceEntryReader:   seReader,
		recorder:             recorder,
		configGenerator:      NewConfigGenerator(renderer, configMutators, nil, applicator, templating.DontApplyOverlays, ctxLogger, metricsRegistry),
		metricsRegistry:      metricsRegistry,
		clusterName:          clusterName,
		resourceManager:      resourceManager,
		serviceEnqueuer:      serviceEnqueuer,
		seEnqueuer:           seEnqueuer,
		client:               client,
		primaryClient:        primaryClient,
		dryRun:               dryRun,
		reconcileTracker:     reconcileTracker,
		additionalObjManager: additionalObjManager,
		timeProvider:         timeProvider,
	}

	controller.objectReader = controller.readMopObject

	err := addMopExtensionIndexer(mopInformer)

	if err != nil {
		controller.logger.Errorf("error adding indexer for mopInformer: %s", err)
	}

	_, _ = mopInformer.AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: namespaceFilter.IsNamespaceMopEnabledForObject,
		Handler:    NewMeshOperatorEventHandler(mopEnqueuer, clusterName, ctxLogger),
	})

	return controller
}

func (c *meshOperatorController) readMopObject(namespace string, name string) (interface{}, error) {
	return c.mopLister.MeshOperators(namespace).Get(name)
}

func (c *meshOperatorController) Run(workerThreads int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.mopEnqueuer.GetQueue().ShutDown()

	// Start the informer factories to begin populating the informer caches
	c.logger.Info("starting mop controller")

	// Wait for the caches to be synced before starting workers
	c.logger.Info("waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh,
		c.mopInformer.HasSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync: mop controller")
	}

	onRetryHandler := OnControllerRetryHandler(
		commonmetrics.MeshOperatorRetries,
		c.metricsRegistry)

	c.logger.Info("starting workers")
	// Launch workers to process Service resources
	for i := 0; i < workerThreads; i++ {
		go wait.Until(func() {
			RunWorker(
				c.logger,
				c.mopEnqueuer.GetQueue(),
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

func (c *meshOperatorController) reconcile(item QueueItem) error {
	if !features.EnableFiltersConfig {
		return nil
	}
	defer IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.MeshOperatorRecordsReconciledTotal, c.clusterName, item)

	namespace, name, object, err := extractOrRestoreObject(item, c.objectReader)
	if err != nil {
		return err
	}
	if object == nil {
		c.logger.Warnf("mop '%s' in work queue no longer exists in the cluster", item.key)
		return nil
	}

	mop := object.(*meshv1alpha1.MeshOperator).DeepCopy()

	reconcileId := uuid.New()
	mopCtxLogger := c.logger.With("mopNamespace", mop.Namespace, "mopName", mop.Name, "mopReconcileId", reconcileId)

	keyAlreadyTracked := c.reconcileTracker.trackKey(namespace, name)
	if keyAlreadyTracked {
		IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.MeshOperatorReconcileNonCriticalError, c.clusterName, item)
		return &meshOpErrors.NonCriticalReconcileError{Message: fmt.Sprintf("MOP being reconciled already, will retry: %s", namespace+"/"+name)}
	}

	defer c.reconcileTracker.unTrackKey(mopCtxLogger, c.clusterName, namespace, name)

	switch item.event {
	case controllers_api.EventAdd:
		return c.handleAdd(item, mop, mopCtxLogger)
	case controllers_api.EventUpdate:
		if mop.GetObjectMeta().GetDeletionTimestamp().IsZero() {
			// set status to Pending for new mop
			if !isOverlayingMop(mop) && len(mop.Status.Phase) == 0 {
				mopCtxLogger.Debugf("set status to pending from blank for mop")
				mop = updateRootStatus(mop, PhasePending, "")
				_, updateStatusErr := c.updateStatusInTheCluster(mop, mopCtxLogger)
				return updateStatusErr
			}
			err = validateExtensionsMop(mop)
			if err != nil {
				mop = updateRootStatus(mop, PhaseFailed, err.Error())
				_, updateStatusErr := c.updateStatusInTheCluster(mop, mopCtxLogger)
				return updateStatusErr // not returning error `err` to prevent requeue to happen
			}
			err = c.reconcileMeshConfig(mop, mopCtxLogger)
			if err == nil && (isOverlayingMop(mop) || isEmptyMop(mop)) {
				enqueueErr := c.enqueueTargetsForMop(item, mop)
				if enqueueErr != nil {
					return enqueueErr
				}
			}
		} else {
			return c.setPhaseTerminatingOrEnqueueDelete(mop, mopCtxLogger)
		}
	case controllers_api.EventDelete:
		// Object is waiting for finalizer
		err := c.deleteMeshConfig(mop, mopCtxLogger)
		if err != nil {
			return fmt.Errorf("failed to delete mesh config for %s: %w", item.key, err)
		}
		enqueueErr := c.enqueueTargetsForMop(item, mop)
		if enqueueErr != nil {
			return enqueueErr
		}
		err = c.deleteFinalizer(mop, mopCtxLogger)
		if err != nil {
			return fmt.Errorf("failed to delete finalizer for %s: %w", item.key, err)
		}
		EraseMopRenderingStats(c.metricsRegistry, c.clusterName, namespace, name)
		return nil
	default:
		mopCtxLogger.Errorw("unsupported event", "key", item.key, "event", item.event)
	}

	if err != nil {
		if meshOpErrors.IsUserConfigError(err) || meshOpErrors.IsOverlayingError(err) {
			mopCtxLogger.Warn("user config error in MOP", "item", item, "error", err)
			IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.MeshOperatorReconcileUserError, c.clusterName, item)
			return nil
		}
		if meshOpErrors.IsNonCriticalReconcileError(err) {
			// Non-critical error. Just retry.
			IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.MeshOperatorReconcileNonCriticalError, c.clusterName, item)
			return err
		}
		mopCtxLogger.Errorw("failed to reconcile", "item", item, "error", err)
		IncrementCounterForQueueItem(c.metricsRegistry, commonmetrics.MeshOperatorReconcileFailed, c.clusterName, item)

		if meshOpErrors.IsCriticalNoRetryError(err) {
			return nil
		} else {
			return err
		}
	}

	return nil
}

func (c *meshOperatorController) handleAdd(item QueueItem, mop *meshv1alpha1.MeshOperator, mopCtxLogger *zap.SugaredLogger) error {
	// Update phase to Pending and stop, the subsequent Update event's reconcile loop will take care of moving mop to next phase.
	finalizerAdded, err := c.addFinalizer(mop)
	if err != nil {
		return fmt.Errorf("failed to add finalizer to: %s, %w", item.key, err)
	}
	if !mop.GetObjectMeta().GetDeletionTimestamp().IsZero() {
		return c.setPhaseTerminatingOrEnqueueDelete(mop, mopCtxLogger)
	}

	if finalizerAdded {
		// When finalizer is added, no need to do status update to PENDING, since UPDATE event's reconcile loop will take care of that
		return nil
	}

	// If root status is pending for new mop, we assume that previous leader was killed/crashed and force-enqueue mop.
	if mop.Status.Phase == PhasePending {
		c.mopEnqueuer.Enqueue(c.clusterName, mop, controllers_api.EventUpdate)
		mopCtxLogger.Info("enqueued mop. pending phase during EventAdd")
		return nil
	}
	if !isOverlayingMop(mop) {
		mopCtxLogger.Debug("set status to pending for new mop")
		mop = updateRootStatus(mop, PhasePending, "")

		updatedMop, err := c.updateStatusInTheCluster(mop, mopCtxLogger)
		if err == nil {
			mopCtxLogger.Info("No change in status, enqueueing UPDATE event")
			// Force-enqueue MOP. This is necessary if MOP has already been in Pending status.
			c.mopEnqueuer.Enqueue(c.clusterName, updatedMop, controllers_api.EventUpdate)
		}
		return err
	}
	return nil
}

func (c *meshOperatorController) setPhaseTerminatingOrEnqueueDelete(mop *meshv1alpha1.MeshOperator, mopCtxLogger *zap.SugaredLogger) error {
	if mop.Status.Phase != PhaseTerminating {
		// Schedule a delete
		mop = updateRootStatus(mop, PhaseTerminating, "")
		_, updateStatusErr := c.updateStatusInTheCluster(mop, mopCtxLogger)
		return updateStatusErr
	}
	c.mopEnqueuer.Enqueue(c.clusterName, mop, controllers_api.EventDelete)
	return nil
}

func (c *meshOperatorController) enqueueTargetsForMop(item QueueItem, mop *meshv1alpha1.MeshOperator) error {
	services, err := matching.ListServicesForMop(mop, c.servicesReader.List, matching.HasServiceSelector)
	if err != nil {
		return fmt.Errorf("failed to requeue affected services for %s: %w", item.key, err)
	}
	for _, svc := range services {
		c.serviceEnqueuer.Enqueue(c.clusterName, svc, controllers_api.EventUpdate)
		c.logger.Infow("enqueued service for mop", "cluster", c.clusterName, "mop", mop.Name, "namespace", mop.Namespace, "service", svc.Name)
	}

	if features.EnableServiceEntryOverlays {
		ses, err := matching.ListServiceEntriesForMop(mop, c.serviceEntryReader.List)

		if err != nil {
			return fmt.Errorf("failed to requeue affected service entries for %s: %w", item.key, err)
		}
		for _, se := range ses {
			c.seEnqueuer.Enqueue(c.clusterName, se, controllers_api.EventUpdate)
			c.logger.Infow("enqueued se for mop", "cluster", c.clusterName, "mop", mop.Name, "namespace", mop.Namespace, "se", se.Name)
		}
	}

	return nil
}

func addMopExtensionIndexer(informer cache.SharedIndexInformer) error {

	indexName := constants.ExtensionIndexName

	err := informer.AddIndexers(cache.Indexers{
		indexName: func(obj interface{}) (strings []string, e error) {
			var index []string
			mop := obj.(*meshv1alpha1.MeshOperator)
			if mop == nil {
				return nil, e
			}
			for _, ext := range mop.Spec.Extensions {
				extensionName, err := templating.GetExtensionType(ext)
				if err != nil {
					return nil, err
				}
				index = append(index, extensionName)
			}
			return index, nil
		},
	})

	return err
}

func validateExtensionsMop(mop *meshv1alpha1.MeshOperator) error {
	if len(mop.Spec.Extensions) > constants.MaxAllowedFilters {
		return fmt.Errorf("mop.spec.extensions length should not be more than %d", constants.MaxAllowedFilters)
	}
	if len(mop.Name) > constants.MaxMopNameLenAllowed {
		return fmt.Errorf("MOP metadata.name should not be more than %d characters", constants.MaxMopNameLenAllowed)
	}
	if err := validateServiceSelector(mop.Spec.ServiceSelector); err != nil {
		return err
	}

	return nil
}

func (c *meshOperatorController) reconcileMeshConfig(mop *meshv1alpha1.MeshOperator, mopCtxLogger *zap.SugaredLogger) error {
	var err error
	mopWithOldStatus := mop.DeepCopy()

	mop, err = c.processMop(mop, mopCtxLogger)
	if !isOverlayingMop(mop) {
		RecordMopRenderingStats(c.metricsRegistry, c.clusterName, mop, constants.ServiceKind.Kind)
	}

	if err != nil {
		_, _ = c.updateStatusInTheCluster(mop, mopCtxLogger)
		return err
	}
	staleResources := identifyStaleRelatedResources(mopWithOldStatus, mop)
	if staleResources != nil {
		c.deleteStaleResources(mop, staleResources)
	}

	if isOverlayingMop(mop) {
		serviceNamesNoLongerSelectedByNewMop := getServicesNotSelectedByNewMop(mopWithOldStatus, mop)
		servicesNoLongerSelectedByMop, listServiceErr := c.getListOfServices(mop.Namespace, serviceNamesNoLongerSelectedByNewMop)
		if listServiceErr != nil && !k8serrors.IsNotFound(listServiceErr) {
			mopCtxLogger.Errorw("failed to list services which are no longer selected by overlaying MOP", "error", listServiceErr)
		}
		for _, service := range servicesNoLongerSelectedByMop {
			c.serviceEnqueuer.Enqueue(c.clusterName, service, controllers_api.EventUpdate)
			mopCtxLogger.Info("enqueued service for mop. No longer applied", "service", service.Name)
		}
		if features.EnableServiceEntryOverlays {
			serviceEntriesNoLongerSelectedByNewMop := getTargetsNotSelectedByNewMop(mopWithOldStatus.Status.ServiceEntries, mop.Status.ServiceEntries)
			sesNoLongerSelectedByMop, listSeErr := c.getListOfServiceEntries(mop.Namespace, serviceEntriesNoLongerSelectedByNewMop)
			if listSeErr != nil && !k8serrors.IsNotFound(listSeErr) {
				mopCtxLogger.Errorw("failed to list service-entries which are no longer selected by overlaying MOP", mop.Name, "error", listSeErr)
			}
			for _, se := range sesNoLongerSelectedByMop {
				c.seEnqueuer.Enqueue(c.clusterName, se, controllers_api.EventUpdate)
				mopCtxLogger.Info("enqueued se for mop. No longer applied", "service", se.Name)
			}
		}
	}

	_, updateStatusErr := c.updateStatusInTheCluster(mop, mopCtxLogger)
	return updateStatusErr
}

func (c *meshOperatorController) deleteMeshConfig(mop *meshv1alpha1.MeshOperator, mopCtxLogger *zap.SugaredLogger) error {
	mopCtxLogger.Info("deleting mesh config")

	if mop.Status.RelatedResources != nil {
		c.deleteStaleResources(mop, mop.Status.RelatedResources)
	}
	if mop.Status.Services != nil {
		for _, svcStatus := range mop.Status.Services {
			c.deleteStaleResources(mop, svcStatus.RelatedResources)
		}
	}

	if features.EnableServiceEntryExtension {
		if mop.Status.ServiceEntries != nil {
			for _, seStatus := range mop.Status.ServiceEntries {
				c.deleteStaleResources(mop, seStatus.RelatedResources)
			}
		}
	}

	return nil
}

// processMop processes the MOP spec, updates the status in the MOP object copy, returns back the updated MOP object.
func (c *meshOperatorController) processMop(mop *meshv1alpha1.MeshOperator, mopCtxLogger *zap.SugaredLogger) (*meshv1alpha1.MeshOperator, error) {
	mopCtxLogger.Debugw("processing mop", "serviceSelector", mop.Spec.ServiceSelector)

	var services []*corev1.Service
	var serviceEntries []*istiov1alpha3.ServiceEntry
	var err, seListErr error

	services, err = matching.ListServicesForMop(mop, c.servicesReader.List, matching.HasServiceSelector)
	if err != nil {
		mopCtxLogger.Errorw("error listing services", "error", err)
		mop = updateRootStatus(mop, PhaseFailed, err.Error())
		return mop, err
	}

	overlayingMop := isOverlayingMop(mop)

	// list SE for both overlay and extensions
	if (overlayingMop && features.EnableServiceEntryOverlays) || (!overlayingMop && features.EnableServiceEntryExtension) {
		serviceEntries, seListErr = matching.ListServiceEntriesForMop(mop, c.serviceEntryReader.List)
		if seListErr != nil {
			mopCtxLogger.Errorw("error listing serviceentries", "error", seListErr)
			mop = updateRootStatus(mop, PhaseFailed, seListErr.Error())
			return mop, err
		}
	}

	err = c.createPrimaryNamespaceIfRequired(mop, services, mopCtxLogger)
	if err != nil {
		return mop, err
	}

	mop = initStatus(mop, services, serviceEntries)

	// only initialize status for mop with overlays
	if isOverlayingMop(mop) {
		return mop, nil
	}

	anyFailures := false

	// Process MOP with extensions, for any services it selects.
	// Erase metrics, as MOP could have changed service selector
	EraseMopRenderingStats(c.metricsRegistry, c.clusterName, mop.Namespace, mop.Name)

	var objectsToProcess []*unstructured.Unstructured
	for _, service := range services {
		ok, updatedMop, serviceAsUnstructured := convertObjectToUnstructured(c.logger, mop, constants.ServiceKind.Kind, service)
		if !ok {
			mop = updatedMop
			continue
		}
		objectsToProcess = append(objectsToProcess, serviceAsUnstructured)
	}

	if features.EnableServiceEntryExtension {
		for _, serviceEntry := range serviceEntries {
			ok, updatedMop, seAsUnstructured := convertObjectToUnstructured(c.logger, mop, constants.ServiceEntryKind.Kind, serviceEntry)
			if !ok {
				mop = updatedMop
				continue
			}
			objectsToProcess = append(objectsToProcess, seAsUnstructured)
		}
	}

	for _, obj := range objectsToProcess {
		mop, err = c.processMopForObjectOrNamespace(mop, obj, mopCtxLogger)
		if err != nil {
			mop, _ = updateMopStatusForObject(mop, obj.GetKind(), obj.GetName(), PhaseFailed, err.Error(), "")
			anyFailures = true
		}
	}

	// Process MOP for entire namespace if no serviceSelector.
	if len(mop.Spec.ServiceSelector) == 0 {
		mop, err = c.processMopForObjectOrNamespace(mop, nil, mopCtxLogger)
		if err != nil {
			anyFailures = true
		}
	}

	if anyFailures {
		mop = updateRootStatus(mop, PhaseFailed, "error generating one or more config")
	} else {
		if len(mop.Spec.ServiceSelector) > 0 && len(services) == 0 && len(serviceEntries) == 0 {
			mop = updateRootStatus(mop, PhaseSucceeded, noServicesForSelectorMessage)
		} else {
			mop = updateRootStatus(mop, PhaseSucceeded, "")
		}
	}

	return mop, err
}

func convertObjectToUnstructured(logger *zap.SugaredLogger, mop *meshv1alpha1.MeshOperator, kind string, typedObject metav1.Object) (bool, *meshv1alpha1.MeshOperator, *unstructured.Unstructured) {
	objAsUnstructured, err := kube.ObjectToUnstructured(typedObject)
	if typedObject == nil {
		logger.Errorf("received nil %s object from api or informer list for mop %s", kind, mop.Name)
		return false, mop, nil
	}
	if err != nil {
		logger.Errorf("failed to convert %s object to unstructured for mop %s: %v. Error: %v", kind, mop.Name, typedObject, err)
		message := fmt.Sprintf("failed to convert %s object %s/%s to unstructured Object, error %s", kind, typedObject.GetNamespace(), typedObject.GetName(), err.Error())
		updatedMop, _ := updateMopStatusForObject(mop, kind, typedObject.GetName(), PhaseFailed, message, "")
		return false, updatedMop, nil
	}
	return true, mop, objAsUnstructured
}

func (c *meshOperatorController) getListOfServices(namespace string, serviceNameList []string) ([]*corev1.Service, error) {
	var services []*corev1.Service
	for _, serviceName := range serviceNameList {
		service, err := c.servicesReader.Get(namespace, serviceName)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, nil
}

func (c *meshOperatorController) getListOfServiceEntries(namespace string, seNameList []string) ([]*istiov1alpha3.ServiceEntry, error) {
	var ses []*istiov1alpha3.ServiceEntry
	for _, seName := range seNameList {
		se, err := c.serviceEntryReader.Get(namespace, seName)
		if err != nil {
			return nil, err
		}
		ses = append(ses, se)
	}
	return ses, nil
}

// createPrimaryNamespaceIfRequired creates namespace in primary for remote event iff:
//  1. There are any filters to upsert.
//  2. There are any services selected by the selector OR no selector is specified, i.e., namespace-level MOP.
//
// Ideally it could be just before the upsert iff there are any resulting filters to be upserted based on render result,
// but collision check logic also tries to fetch filter from the namespace in primary cluster.
func (c *meshOperatorController) createPrimaryNamespaceIfRequired(mop *meshv1alpha1.MeshOperator, services []*corev1.Service, mopCtxLogger *zap.SugaredLogger) error {
	if c.dryRun || !IsRemoteClusterEvent(c.client, c.primaryClient) || len(mop.Spec.Extensions) == 0 {
		return nil
	}
	if len(services) > 0 || len(mop.Spec.ServiceSelector) == 0 {
		err := CreatePrimaryNamespaceIfMissing(mopCtxLogger, c.primaryClient, mop, c.metricsRegistry)
		if err != nil {
			return err
		}
	}
	return nil
}

// processMopForObjectOrNamespace processes MOP for the object (if provided)
// If no object is provided, it processes MOP for the entire namespace.
func (c *meshOperatorController) processMopForObjectOrNamespace(mop *meshv1alpha1.MeshOperator, object *unstructured.Unstructured, mopLogger *zap.SugaredLogger) (
	*meshv1alpha1.MeshOperator, error) {
	// Create per Service/SE per mop rendering context
	updatedMop, generatedConfig, err := c.generateConfig(mop, object, mopLogger)
	if object != nil {
		if err == nil {
			updatedMop, _ = updateMopStatusForObject(updatedMop, object.GetKind(), object.GetName(), PhaseSucceeded, "", "")
		} else {
			updatedMop, _ = updateMopStatusForObject(updatedMop, object.GetKind(), object.GetName(), PhaseFailed, err.Error(), "")
		}
	}

	updatedMop, resourceErr := updateStatusForRelatedResources(updatedMop, object, generatedConfig)
	if resourceErr != nil {
		return updatedMop, resourceErr
	}
	return updatedMop, err
}

func (c *meshOperatorController) generateConfig(mop *meshv1alpha1.MeshOperator, object *unstructured.Unstructured, mopLogger *zap.SugaredLogger) (
	*meshv1alpha1.MeshOperator, []*templating.AppliedConfigObject, error) {
	updatedMop, config, err := c.handleMopConfigGeneration(mop, object, mopLogger)
	if err != nil {
		return updatedMop, nil, err
	}

	return updatedMop, config, err
}

// handleMopConfigGeneration generates config for the given mop for all cases except service config overlays.
// See also handleServiceConfigOverlays
func (c *meshOperatorController) handleMopConfigGeneration(mop *meshv1alpha1.MeshOperator, object *unstructured.Unstructured,
	logger *zap.SugaredLogger) (*meshv1alpha1.MeshOperator, []*templating.AppliedConfigObject, error) {

	var err error

	var additionalObjects map[string]*templating.AdditionalObjects

	additionalObjMgr := c.additionalObjManager

	if object != nil && object.GetKind() == constants.ServiceKind.Kind {
		if additionalObjMgr != nil {
			additionalObjects, err = additionalObjMgr.GetAdditionalObjectsForExtension(mop, object)
			if err != nil {
				return mop, nil, &meshOpErrors.CriticalNoRetryError{Message: fmt.Sprintf("error getting additional objects for extension: %v", err)}
			}
		}
	}

	if object != nil && (features.EnableServiceEntryAdditionalObjects && object.GetKind() == constants.ServiceEntryKind.Kind) {
		if additionalObjMgr != nil {
			additionalObjects, err = additionalObjMgr.GetAdditionalObjectsForExtension(mop, object)
			if err != nil {
				return mop, nil, &meshOpErrors.CriticalNoRetryError{Message: fmt.Sprintf("error getting additional objects for extension: %v", err)}
			}
		}
	}

	ctx := templating.NewMOPRenderRequestContext(object, mop, nil, c.clusterName, mopToOwnerRef(mop), additionalObjects)

	mopWithTrackedResources := mop
	config, err := c.configGenerator.GenerateConfig(&ctx, func(config *templating.GeneratedConfig) error {
		// This callback is responsible for tracking resources that have been generated, but not applied to cluster yet.
		// It is done for the cases, where error happens mid-application, and we loose track of the resources applied before the error.
		// Here we add all the generated rtesources to a MOP record in cluster as Pending.
		// If reconcile completes successfully, status will be overwritten and only contain actual resources.
		// If reconcile fails, status will keep track of old resources as well as new ones.

		if !features.EnableMopPendingTracking {
			return nil
		}
		// Fetch MOP from informer
		objInCluster, readMopError := c.objectReader(mop.Namespace, mop.Name)
		if readMopError != nil {
			return readMopError
		}
		if len(config.FlattenConfig()) == 0 {
			// Nothing generated. Skip
			return nil
		}
		// Append pending resources to status
		mopInCluster := objInCluster.(*meshv1alpha1.MeshOperator).DeepCopy()
		appendPendingResources(mopInCluster, object, config)
		mopInCluster.Status.Phase = PhasePending

		// Persist status in cluster
		updatedMop, statusUpdateError := c.updateStatusInTheCluster(mopInCluster, logger)
		if statusUpdateError != nil {
			return statusUpdateError
		}

		// Update in-memory MOP
		updatedMop.Status = mopWithTrackedResources.Status
		mopWithTrackedResources = updatedMop

		return nil
	}, logger)

	if err != nil {
		logger.Errorw("error while generating mop config", "error", err)
		return mopWithTrackedResources, nil, err
	}

	return mopWithTrackedResources, config, nil
}

func (c *meshOperatorController) addFinalizer(mop *meshv1alpha1.MeshOperator) (bool, error) {
	if !controllerutil.ContainsFinalizer(mop, FinalizerName) {
		controllerutil.AddFinalizer(mop, FinalizerName)
		_, err := c.mopClient.MeshV1alpha1().MeshOperators(mop.Namespace).Update(context.TODO(), mop, metav1.UpdateOptions{})
		return true, err
	}
	return false, nil
}

func (c *meshOperatorController) deleteFinalizer(mop *meshv1alpha1.MeshOperator, mopCtxLogger *zap.SugaredLogger) error {
	if controllerutil.ContainsFinalizer(mop, FinalizerName) {
		controllerutil.RemoveFinalizer(mop, FinalizerName)
		_, err := c.mopClient.MeshV1alpha1().MeshOperators(mop.Namespace).Update(context.TODO(), mop, metav1.UpdateOptions{})
		return err
	}
	mopCtxLogger.Debugf("deleted %s finalizer for mop %s in ns %s", FinalizerName, mop.Name, mop.Namespace)
	return nil
}

func validateOverlayingMop(mop *meshv1alpha1.MeshOperator) error {
	if err := validateServiceSelector(mop.Spec.ServiceSelector); err != nil {
		return err
	}

	for overlayIdx, overlay := range mop.Spec.Overlays {
		validator := patch.GetValidationStrategyForKindOrDefault(overlay.Kind)
		err := validator.Validate(&overlay)
		if err != nil {
			return &meshOpErrors.UserConfigError{Message: fmt.Sprintf("overlay %d is invalid: %s", overlayIdx, err.Error())}
		}
	}

	return nil
}

func mopToOwnerRef(mop *meshv1alpha1.MeshOperator) *metav1.OwnerReference {
	return &metav1.OwnerReference{
		APIVersion: meshv1alpha1.ApiVersion,
		Kind:       meshv1alpha1.MopKind.Kind,
		Name:       mop.GetName(),
		UID:        mop.GetUID(),
	}
}

// updateRootStatus updates the MOP root status - phase and message.
func updateRootStatus(mop *meshv1alpha1.MeshOperator, phase meshv1alpha1.Phase, message string) *meshv1alpha1.MeshOperator {
	mop.Status.Phase = phase
	mop.Status.Message = message
	return mop
}

// initStatus initializes all status to Pending, clears existing resources, and copies over the service hash if exists.
func initStatus(mop *meshv1alpha1.MeshOperator, services []*corev1.Service, serviceEntries []*istiov1alpha3.ServiceEntry) *meshv1alpha1.MeshOperator {
	oldStatus := mop.Status
	newStatus := meshv1alpha1.MeshOperatorStatus{
		Phase: PhasePending,
	}

	overlayingMop := isOverlayingMop(mop)

	newServices := make(map[string]*meshv1alpha1.ServiceStatus)
	for _, svc := range services {
		newSvcStatus := &meshv1alpha1.ServiceStatus{
			Phase: PhasePending,
		}
		if svcStatus, exists := oldStatus.Services[svc.Name]; exists {
			newSvcStatus.Hash = svcStatus.Hash
		}
		newServices[svc.Name] = newSvcStatus
	}

	newServiceEntries := make(map[string]*meshv1alpha1.ServiceStatus)
	for _, se := range serviceEntries {
		if common.IsOperatorDisabled(se) && overlayingMop {
			continue
		}
		newSeStatus := &meshv1alpha1.ServiceStatus{
			Phase: PhasePending,
		}
		newServiceEntries[se.Name] = newSeStatus
	}

	if len(newServices) > 0 || len(newServiceEntries) > 0 {
		if len(newServices) > 0 {
			newStatus.Services = newServices
		}
		if len(newServiceEntries) > 0 {
			newStatus.ServiceEntries = newServiceEntries
		}
	} else {
		newStatus.Services = nil
		if overlayingMop {
			newStatus.Phase = PhaseSucceeded
			newStatus.Message = noServicesForSelectorMessage
		}
	}

	mop.Status = newStatus
	return mop
}

func isEmptyMop(mop *meshv1alpha1.MeshOperator) bool {
	if len(mop.Spec.Overlays) == 0 && len(mop.Spec.Extensions) == 0 {
		return true
	}

	return false
}

func isOverlayingMop(mop *meshv1alpha1.MeshOperator) bool {
	return len(mop.Spec.Overlays) > 0
}

func validateServiceSelector(serviceSelectorMap map[string]string) error {
	for _, selectorVal := range serviceSelectorMap {
		if errs := validation.IsValidLabelValue(selectorVal); len(errs) > 0 {
			errMsg := fmt.Sprintf("invalid selector value: `%s`, '%s'", selectorVal, strings.Join(errs, "; "))
			return &meshOpErrors.UserConfigError{Message: errMsg}
		}
	}
	return nil
}

// updateMopStatusForObject updates the status for the provided object on the provided mop.
func updateMopStatusForObject(mop *meshv1alpha1.MeshOperator, objectKind string, objectName string, phase meshv1alpha1.Phase, objectMessage, rootFailedMessage string) (*meshv1alpha1.MeshOperator, *meshv1alpha1.ServiceStatus) {

	var serviceStatus *meshv1alpha1.ServiceStatus
	if objectKind == constants.ServiceKind.Kind {
		serviceStatus = updateMopStatusForService(mop, objectName, phase, objectMessage)
	} else if objectKind == constants.ServiceEntryKind.Kind {
		serviceStatus = updateMopStatusForServiceEntry(mop, objectName, phase, objectMessage)
	}

	if phase == PhaseFailed {
		if len(rootFailedMessage) > 0 {
			mop = updateRootStatus(mop, PhaseFailed, rootFailedMessage)
		} else {
			mop = updateRootStatus(mop, PhaseFailed, objectMessage)
		}
	}
	return mop, serviceStatus
}

func updateMopStatusForService(mop *meshv1alpha1.MeshOperator, serviceName string, phase meshv1alpha1.Phase, serviceMessage string) *meshv1alpha1.ServiceStatus {
	if mop.Status.Services == nil {
		mop.Status.Services = make(map[string]*meshv1alpha1.ServiceStatus)
	}

	if svcStatus, exists := mop.Status.Services[serviceName]; exists {
		svcStatus.Phase = phase
		svcStatus.Message = serviceMessage
		mop.Status.Services[serviceName] = svcStatus
	} else {
		mop.Status.Services[serviceName] = &meshv1alpha1.ServiceStatus{Phase: phase, Message: serviceMessage}
	}
	return mop.Status.Services[serviceName]
}

func updateMopStatusForServiceEntry(mop *meshv1alpha1.MeshOperator, seName string, phase meshv1alpha1.Phase, seMessage string) *meshv1alpha1.ServiceStatus {
	if mop.Status.ServiceEntries == nil {
		mop.Status.ServiceEntries = make(map[string]*meshv1alpha1.ServiceStatus)
	}

	if seStatus, exists := mop.Status.ServiceEntries[seName]; exists {
		seStatus.Phase = phase
		seStatus.Message = seMessage
		mop.Status.ServiceEntries[seName] = seStatus
	} else {
		mop.Status.ServiceEntries[seName] = &meshv1alpha1.ServiceStatus{Phase: phase, Message: seMessage}
	}
	return mop.Status.ServiceEntries[seName]
}

// updateStatusForRelatedResources updates the status for the related resources.
// If service is provided, it will update the relatedResources for the provided service in services.
// If service is not provided, it will update the parent relatedResources.
func updateStatusForRelatedResources(mop *meshv1alpha1.MeshOperator, object *unstructured.Unstructured,
	applicatorResults []*templating.AppliedConfigObject) (*meshv1alpha1.MeshOperator, error) {
	if len(applicatorResults) == 0 {
		return mop, nil
	}
	var relatedResources []*meshv1alpha1.ResourceStatus
	var anyErrors bool
	var resourceErrorCount int
	var conflictErrorCount int
	var objectStatus *meshv1alpha1.ServiceStatus

	// relatedResources will reflect status in the same list order as returned by patching result
	for _, pr := range applicatorResults {
		resourcePhase := PhaseSucceeded
		resourceMessage := ""
		if pr.Error != nil {
			resourceErrorCount++
			resourcePhase = PhaseFailed
			resourceMessage = pr.Error.Error()
			anyErrors = true
			if k8serrors.IsConflict(pr.Error) {
				conflictErrorCount++
			}
		}
		resourceNamespace := pr.Object.GetNamespace()
		if resourceNamespace == "" {
			// default to mop ns if no resource ns is set
			resourceNamespace = mop.Namespace
		}

		resourceStatus := &meshv1alpha1.ResourceStatus{
			ApiVersion: pr.Object.GetAPIVersion(),
			Kind:       pr.Object.GetKind(),
			Name:       pr.Object.GetName(),
			Namespace:  resourceNamespace,
			Phase:      resourcePhase,
			Message:    resourceMessage,
		}
		relatedResources = append(relatedResources, resourceStatus)
	}

	var err error
	var message = "all resources generated successfully"
	if anyErrors {
		message = "error generating one or more config"
		err = errors.New(message)
	}

	if (resourceErrorCount > 0) && (conflictErrorCount == resourceErrorCount) { // if all errors are conflict errors
		message = "Conflict error while updating resources"
		err = &meshOpErrors.NonCriticalReconcileError{Message: message}
	}

	if object != nil {
		objectPhase := PhaseSucceeded
		if anyErrors {
			objectPhase = PhaseFailed
		}
		mop, objectStatus = updateMopStatusForObject(mop, object.GetKind(), object.GetName(), objectPhase, message, "")
		objectStatus.RelatedResources = relatedResources
	} else {
		mop.Status.RelatedResources = relatedResources
	}

	return mop, err
}

func appendPendingResources(mop *meshv1alpha1.MeshOperator, object *unstructured.Unstructured, config *templating.GeneratedConfig) {
	var pendingResources []*meshv1alpha1.ResourceStatus
	var objectStatus *meshv1alpha1.ServiceStatus

	for _, cfgObject := range config.FlattenConfig() {
		resourceStatus := &meshv1alpha1.ResourceStatus{
			ApiVersion: cfgObject.GetAPIVersion(),
			Kind:       cfgObject.GetKind(),
			Name:       cfgObject.GetName(),
			Namespace:  cfgObject.GetNamespace(),
			Phase:      PhasePending,
		}

		pendingResources = append(pendingResources, resourceStatus)
	}

	// Update related resources in such a way that:
	// existing resources not present in rendered config - stay in original status
	// existing resources present in rendered config - Pending
	// brand new resources - Pending
	if object == nil {
		existingOldResources := difference(mop.Namespace, mop.Status.RelatedResources, pendingResources)
		mop.Status.RelatedResources = append(existingOldResources, pendingResources...)
	} else {
		mop, objectStatus = updateMopStatusForObject(mop, object.GetKind(), object.GetName(), PhasePending, "", "")
		existingOldResources := difference(mop.Namespace, objectStatus.RelatedResources, pendingResources)
		objectStatus.RelatedResources = append(existingOldResources, pendingResources...)
	}
}

func (c *meshOperatorController) updateStatusInTheCluster(mop *meshv1alpha1.MeshOperator, mopCtxLogger *zap.SugaredLogger) (*meshv1alpha1.MeshOperator, error) {
	mop.Status.LastReconciledTime = nil
	mop.Status.ObservedGeneration = mop.Generation

	updatedMop, err := c.mopClient.MeshV1alpha1().MeshOperators(mop.Namespace).UpdateStatus(context.TODO(), mop, metav1.UpdateOptions{})
	if err != nil {
		if k8serrors.IsConflict(err) {
			// This is a normal situation, where we have competing reconcile loops running for the same MOP record.
			// This mostly happens on startup where we have Create event enqueued by mop informer
			// and Update event enqueued by service-informer
			mopCtxLogger.Info("conflict while updating status, will retry")
			return nil, &meshOpErrors.NonCriticalReconcileError{Message: err.Error()}
		} else {
			mopCtxLogger.Errorw("error updating MOP status", "error", err)
		}
		return nil, err
	}
	return updatedMop, nil
}

func identifyStaleRelatedResources(
	oldMop *meshv1alpha1.MeshOperator,
	newMop *meshv1alpha1.MeshOperator) []*meshv1alpha1.ResourceStatus {

	if newMop.Status.Phase != PhaseSucceeded {
		// Delete existing stale resources only if the mop has processed successfully.
		// There is a possibility that due to repeated failure, some stale resources may stay in the cluster.
		// But it's a trade-off between actively deleting existing resources and risk breaking working config when
		// mop update fails to process v/s risking a possibility of stale resources in the cluster.
		return nil
	}

	var staleResources []*meshv1alpha1.ResourceStatus

	// Check for stale namespace level related resources.
	diffForNamespace := difference(newMop.Namespace, oldMop.Status.RelatedResources, newMop.Status.RelatedResources)
	staleResources = append(staleResources, diffForNamespace...)

	// stale service level resources
	staleResources = append(staleResources, getStaleResources(oldMop.Status.Services, newMop.Status.Services, newMop.Namespace)...)

	// stale SE level resources
	staleResources = append(staleResources, getStaleResources(oldMop.Status.ServiceEntries, newMop.Status.ServiceEntries, newMop.Namespace)...)
	return staleResources
}

// getStaleResources checks for stale service level related resources
func getStaleResources(oldServiceStatusMap map[string]*meshv1alpha1.ServiceStatus, newServiceStatusMap map[string]*meshv1alpha1.ServiceStatus, mopNamespace string) []*meshv1alpha1.ResourceStatus {
	var staleResources []*meshv1alpha1.ResourceStatus
	// Check for stale service level related resources.
	for oldService, oldServiceStatus := range oldServiceStatusMap {
		if newServiceStatus, exists := newServiceStatusMap[oldService]; exists {
			// Service is selected by the updated mop as well, check each related resource to find stale.
			diffForService := difference(mopNamespace, oldServiceStatus.RelatedResources, newServiceStatus.RelatedResources)
			staleResources = append(staleResources, diffForService...)
		} else {
			// Service is no longer selected by the updated mop, remove all related resources.
			staleResources = append(staleResources, oldServiceStatus.RelatedResources...)
		}
	}
	return staleResources
}

// difference returns the resources in left that do not exist in right
func difference(mopNamespace string, left, right []*meshv1alpha1.ResourceStatus) []*meshv1alpha1.ResourceStatus {
	var diff []*meshv1alpha1.ResourceStatus
	newResourcesHashMap := make(map[string]struct{}, len(right))
	for _, newResource := range right {
		newResourcesHashMap[getResourceKey(mopNamespace, newResource)] = struct{}{}
	}
	for _, oldResource := range left {
		if _, found := newResourcesHashMap[getResourceKey(mopNamespace, oldResource)]; !found {
			diff = append(diff, oldResource)
		}
	}
	return diff
}

func (c *meshOperatorController) deleteStaleResources(mop *meshv1alpha1.MeshOperator, staleResources []*meshv1alpha1.ResourceStatus) {
	ctxLogger := c.logger.With("mop", mop.Name, "namespace", mop.Namespace)

	// Sort the resources to give it a predictable order
	sort.Slice(staleResources, func(i, j int) bool {
		return staleResources[i].ApiVersion < staleResources[j].ApiVersion ||
			staleResources[i].Kind < staleResources[j].Kind ||
			staleResources[i].Name < staleResources[j].Name ||
			staleResources[i].UID < staleResources[j].UID
	})

	for _, resource := range staleResources {
		resourceCtxLogger := ctxLogger.With(
			"resourceName", resource.Name,
			"resourceKind", resource.Kind)

		// Note: all extension resources are created in the primary cluster, thus discovery and search use primary client
		gvk := schema.FromAPIVersionAndKind(resource.ApiVersion, resource.Kind)
		gvr, err := kube.ConvertGvkToGvr(c.primaryClient.GetClusterName(), c.primaryClient.Discovery(), gvk)
		if err != nil {
			resourceCtxLogger.Errorw("failed to obtain group-version-resource", "resource", gvk, "error", err)
			continue
		}
		resourceClient := c.primaryClient.Dynamic().Resource(gvr)

		resourceNamespace := resource.Namespace
		if resourceNamespace == "" {
			// Handling existing resource-references that don't have namespace populated
			resourceNamespace = mop.Namespace
		}
		resourceInCluster, err := resourceClient.Namespace(resourceNamespace).Get(context.TODO(), resource.Name, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// Already deleted, ignore
				resourceCtxLogger.Info("resource already deleted")
			} else {
				resourceCtxLogger.Errorw("failed to obtain resource for deletion", "error", err)
			}
			continue
		}

		// Adjust filterSource and update
		existingFilterSource := resourceInCluster.GetAnnotations()[constants.ExtensionSourceAnnotation]
		isConfigNsResource := mop.Namespace != resourceNamespace

		// Extension source for this controller live in a cluster specific to this controller, thus c.clusterName is used to update tracking information
		newFilterSource, existingSourceFound := hash.RemoveExtensionSourceForCluster(isConfigNsResource, c.clusterName, mop.Namespace, existingFilterSource)
		if isConfigNsResource && !existingSourceFound {
			resourceCtxLogger.Error("trying to delete a resource which isn't owned by the mop")
			continue
		}

		if newFilterSource == "" {
			// This was a last MOP referencing the filter. Delete it.
			err := resourceClient.Namespace(resourceNamespace).Delete(context.TODO(), resource.Name, metav1.DeleteOptions{})
			if err != nil {
				// Ignore errors as resource is already deleted, but capture in log.
				// Re-think behavior if we introduce finalizers.
				resourceCtxLogger.Errorw("deletion of related resource failed", "error", err)
			} else {
				resourceCtxLogger.Infow("deleted stale resource")
			}
		} else if existingFilterSource != newFilterSource {
			// There are other MOPs referencing the filter, update the annotation
			annotations := resourceInCluster.GetAnnotations()
			annotations[constants.ExtensionSourceAnnotation] = newFilterSource
			resourceInCluster.SetAnnotations(annotations)
			_, err := resourceClient.Namespace(resourceNamespace).Update(context.TODO(), resourceInCluster, metav1.UpdateOptions{})
			if err != nil {
				// Ignore the error, since we can't retry.
				// This can potentially be a dangling resource.
				resourceCtxLogger.Errorw("failed to update filter source", "error", err)
			} else {
				resourceCtxLogger.Info("updated resource references")
			}
		}
	}
}

// RecordMopRenderingStats - record several of the MOP metrics, using MOP.Status as a source of information.
// Metrics that are being set:
// - MeshOperatorFailed
// - MeshOperatorResourcesTotal
// - MeshOperatorResourcesFailed
func RecordMopRenderingStats(registry *prometheus.Registry, clusterName string, mop *meshv1alpha1.MeshOperator, targetKind string) {
	mopType := commonmetrics.ExtensionMopType
	if isOverlayingMop(mop) {
		mopType = commonmetrics.OverlayMopType
	}
	labels := commonmetrics.GetLabelsForK8sResource(meshv1alpha1.MopKind.Kind, clusterName, mop.Namespace, mop.Name)

	var totalResources float64 = 0
	var failedResources float64 = 0
	for serviceName, status := range mop.Status.Services {
		if status.RelatedResources != nil {
			totalResources += float64(len(status.RelatedResources))
			for _, relatedResource := range status.RelatedResources {
				if relatedResource.Phase != PhaseSucceeded {
					failedResources += 1
				}
			}
		}
		var failure float64 = 0
		if status.Phase != PhaseSucceeded {
			failure = 1
		}
		targetedLabels := commonmetrics.GetLabelsForMopResourceWithTarget(meshv1alpha1.MopKind.Kind, clusterName, mop.Namespace, mop.Name, serviceName, constants.ServiceKind.Kind, mopType)
		commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.MeshOperatorFailed, targetedLabels, registry).Set(failure)
	}

	for seName, status := range mop.Status.ServiceEntries {
		var failure float64 = 0
		if status.Phase != PhaseSucceeded {
			failure = 1
		}
		targetedLabels := commonmetrics.GetLabelsForMopResourceWithTarget(meshv1alpha1.MopKind.Kind, clusterName, mop.Namespace, mop.Name, seName, constants.ServiceEntryKind.Kind, mopType)
		commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.MeshOperatorFailed, targetedLabels, registry).Set(failure)
	}

	if len(mop.Status.Services) == 0 && len(mop.Status.ServiceEntries) == 0 { // MOP failed validation or NS-level MOP
		var failure float64 = 0
		if mop.Status.Phase != PhaseSucceeded {
			failure = 1
		}

		targetedLabels := commonmetrics.GetLabelsForMopResourceWithTarget(meshv1alpha1.MopKind.Kind, clusterName, mop.Namespace, mop.Name, commonmetrics.TargetAll, targetKind, mopType)
		commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.MeshOperatorFailed, targetedLabels, registry).Set(failure)
	}

	commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.MeshOperatorResourcesTotal, labels, registry).Set(totalResources)
	commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.MeshOperatorResourcesFailed, labels, registry).Set(failedResources)
}

func EraseMopRenderingStats(metricsRegistry *prometheus.Registry, clusterName, namespace, name string) {
	EraseObjectMetrics(metricsRegistry, meshv1alpha1.MopKind.Kind, clusterName, namespace, name)

	labels := commonmetrics.GetLabelsForK8sResource(meshv1alpha1.MopKind.Kind, clusterName, namespace, name)
	commonmetrics.EraseMetricsForK8sResource(
		metricsRegistry,
		[]string{
			commonmetrics.MeshOperatorFailed,
			commonmetrics.MeshOperatorResourcesTotal,
			commonmetrics.MeshOperatorResourcesFailed},
		labels)
}

func getTargetsNotSelectedByNewMop(oldStatus, newStatus map[string]*meshv1alpha1.ServiceStatus) []string {
	targetsSelectedByOldMop := getTargetsSelectedByMop(oldStatus)
	targetsSelectedByNewMop := getTargetsSelectedByMop(newStatus)

	var diff []string
	newTargetsHashMap := make(map[string]struct{}, len(targetsSelectedByNewMop))
	for _, newService := range targetsSelectedByNewMop {
		newTargetsHashMap[newService] = struct{}{}
	}
	for _, oldService := range targetsSelectedByOldMop {
		if _, exist := newTargetsHashMap[oldService]; !exist {
			diff = append(diff, oldService)
		}
	}
	return diff
}

func getServicesNotSelectedByNewMop(mopWithOldStatus *meshv1alpha1.MeshOperator, mopWithNewStatus *meshv1alpha1.MeshOperator) []string {
	servicesSelectedByOldMop := getServicesSelectedByMop(mopWithOldStatus)
	servicesSelectedByNewMop := getServicesSelectedByMop(mopWithNewStatus)

	var diff []string
	newServiceHashMap := make(map[string]struct{}, len(servicesSelectedByNewMop))
	for _, newService := range servicesSelectedByNewMop {
		newServiceHashMap[newService] = struct{}{}
	}
	for _, oldService := range servicesSelectedByOldMop {
		if _, exist := newServiceHashMap[oldService]; !exist {
			diff = append(diff, oldService)
		}
	}
	return diff
}

func getTargetsSelectedByMop(status map[string]*meshv1alpha1.ServiceStatus) []string {
	var targetList []string
	for targetName := range status {
		targetList = append(targetList, targetName)
	}
	return targetList
}

func getServicesSelectedByMop(mop *meshv1alpha1.MeshOperator) []string {
	serviceStatus := mop.Status.Services
	var serviceList []string
	for serviceName := range serviceStatus {
		serviceList = append(serviceList, serviceName)
	}
	return serviceList
}

func getResourceKey(mopNamespace string, resource *meshv1alpha1.ResourceStatus) string {
	resourceNamespace := resource.Namespace
	if len(resourceNamespace) == 0 {
		resourceNamespace = mopNamespace
	}
	return fmt.Sprintf("%s/%s/%s", resource.Kind, resourceNamespace, resource.Name)
}
