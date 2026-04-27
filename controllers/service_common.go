package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	ocm2 "github.com/istio-ecosystem/mesh-operator/pkg/common/ocm"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"github.com/istio-ecosystem/mesh-operator/pkg/deployment"

	"github.com/istio-ecosystem/mesh-operator/pkg/statefulset"

	cachesync "github.com/istio-ecosystem/mesh-operator/pkg/common/cache"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"

	"k8s.io/client-go/informers"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/istio-ecosystem/mesh-operator/pkg/rollout"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/istio-ecosystem/mesh-operator/pkg/resources"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/istio-ecosystem/mesh-operator/pkg/dynamicrouting"

	"k8s.io/client-go/tools/record"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	meshOpErrors "github.com/istio-ecosystem/mesh-operator/pkg/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/matching"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube"

	"go.uber.org/zap"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/tools/cache"

	meshv1alpha1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	mopv1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/generated/clientset/versioned"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
)

// MopLister - a function that can return a list of MOPs targeting a specific service
type ListMopsForService func(service *corev1.Service) ([]*mopv1.MeshOperator, error)
type UpdateMopStatus func(logger *zap.SugaredLogger, service *corev1.Service, validationError error, errInfo *meshOpErrors.OverlayErrorInfo, mop *mopv1.MeshOperator) error
type EnqueueFunc func(event controllers_api.Event, objs ...interface{})
type PredicateFunc func(oldObject, newObject interface{}) bool

// CreateRelatedObjectHandlerWithPredicate - A common handler that re-enqueues main objects based on related objects
func CreateRelatedObjectHandlerWithPredicate(enqueueFunc EnqueueFunc, predicateFunc PredicateFunc) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			enqueueFunc(controllers_api.EventAdd, obj)
		},
		UpdateFunc: func(old, new interface{}) {
			if predicateFunc(old, new) {
				enqueueFunc(controllers_api.EventUpdate, old, new)
			}
		},
		DeleteFunc: func(obj interface{}) {
			enqueueFunc(controllers_api.EventDelete, obj)
		},
	}
}

func CreateStsObjectHandler(enqueueFunc EnqueueFunc) cache.ResourceEventHandlerFuncs {
	return CreateRelatedObjectHandlerWithPredicate(enqueueFunc, func(old, new interface{}) bool {
		newRelatedObj := new.(*appsv1.StatefulSet)
		oldRelatedObj := old.(*appsv1.StatefulSet)

		return generationChanged(oldRelatedObj, newRelatedObj) &&
			(stsReplicasChanged(oldRelatedObj, newRelatedObj) || // replicas count change might affect the generated config - must reconcile
				stsTemplateLabelsChanged(oldRelatedObj, newRelatedObj) || // template labels change might affect dynamic routing - reconcile is needed
				stsOrdinalsStartChanged(oldRelatedObj, newRelatedObj)) // ordinals start change might affect the generated config - must reconcile
	})
}

func CreateDeploymentObjectHandler(enqueueFunc EnqueueFunc) cache.ResourceEventHandlerFuncs {
	return CreateRelatedObjectHandlerWithPredicate(enqueueFunc, func(old, new interface{}) bool {
		newRelatedObj := new.(*appsv1.Deployment)
		oldRelatedObj := old.(*appsv1.Deployment)

		return (generationChanged(oldRelatedObj, newRelatedObj) && deploymentTemplateLabelsChanged(oldRelatedObj, newRelatedObj)) || // template.label change might affect dynamic routing - reconcile is needed
			labelsChanged(oldRelatedObj, newRelatedObj) // deployment labels might add/remove deployment from the dynamic routing - reconcile is needed
	})
}

func CreateRelatedRolloutObjectHandler(enqueueFunc EnqueueFunc) cache.ResourceEventHandlerFuncs {
	return CreateRelatedObjectHandlerWithPredicate(
		enqueueFunc,
		func(oldObject, newObject interface{}) bool {
			newRelatedObj := newObject.(*unstructured.Unstructured)
			oldRelatedObj := oldObject.(*unstructured.Unstructured)

			return generationChanged(oldRelatedObj, newRelatedObj) &&
				rolloutTemplateLabelsChanged(oldRelatedObj, newRelatedObj) // template.label change might affect dynamic routing - reconcile is needed
		},
	)
}

func CreateServiceConfigMutators() []templating.Mutator {
	return []templating.Mutator{
		&templating.ManagedByMutator{},
		&templating.OwnerRefMutator{},
		&templating.ConfigResourceParentMutator{},
	}
}

func TestCluster(
	logger *zap.SugaredLogger,
	registry *prometheus.Registry,
	cluster controllers_api.Cluster,
	stopCh <-chan struct{},
	timeout time.Duration) error {

	client := cluster.GetKubeClient()
	clusterName := cluster.GetId().String()
	if stsCacheSyncWaitErr := cachesync.WaitForInformerCacheSync(client.KubeInformerFactory().Apps().V1().StatefulSets().Informer(), "statefulset", timeout, clusterName, stopCh, logger, registry); stsCacheSyncWaitErr != nil {
		logger.Infof("failed waiting for %s informer to cache sync in %s cluster, error: %v", "statefulset", clusterName, stsCacheSyncWaitErr)
		return stsCacheSyncWaitErr
	}

	// Wait for deployment informer to sync (if enabled)
	if features.EnableDynamicRouting {
		if deploymentCacheSyncWaitErr := cachesync.WaitForInformerCacheSync(client.KubeInformerFactory().Apps().V1().StatefulSets().Informer(), "deployment", timeout, clusterName, stopCh, logger, registry); deploymentCacheSyncWaitErr != nil {
			logger.Infof("failed waiting for %s informer to cache sync in %s cluster, error: %v", "deployment", clusterName, deploymentCacheSyncWaitErr)
			return deploymentCacheSyncWaitErr
		}
	}

	// Wait for MOP informer to sync
	if mopCacheSyncWaitErr := cachesync.WaitForInformerCacheSync(client.MopInformerFactory().Mesh().V1alpha1().MeshOperators().Informer(), "mopInformer", timeout, clusterName, stopCh, logger, registry); mopCacheSyncWaitErr != nil {
		logger.Infof("failed waiting for %s informer to cache sync in %s cluster, error: %v", "meshoperator", clusterName, mopCacheSyncWaitErr)
		return mopCacheSyncWaitErr
	}

	// Wait for Rollout informer to sync
	if isArgoPresentInCluster(client) {
		if rolloutCacheSyncWaitErr := cachesync.WaitForInformerCacheSync(client.DynamicInformerFactory().ForResource(constants.RolloutResource).Informer(), "rollout", timeout, clusterName, stopCh, logger, registry); rolloutCacheSyncWaitErr != nil {
			logger.Infof("failed waiting for %s informer to cache sync in %s cluster, error: %v", "rollout", clusterName, rolloutCacheSyncWaitErr)
			return rolloutCacheSyncWaitErr
		}
	}
	return nil
}

func EnqueueMopReferencingGivenService(logger *zap.SugaredLogger, clusterOfEvent string, client kube.Client, mopEnqueuer controllers_api.ObjectEnqueuer, service *corev1.Service) {
	mopLister := client.MopInformerFactory().Mesh().V1alpha1().MeshOperators().Lister()
	mops, err := matching.ListMopsForService(service, func(namespace string) ([]*v1alpha1.MeshOperator, error) {
		return mopLister.MeshOperators(namespace).List(labels.Everything())
	})

	if err != nil {
		logger.Errorf("error while listing matching MOPs for service %s/%s : %w", service.Namespace, service.Namespace, err)
	}
	for _, mop := range mops {
		logger.Infow("enqueued MOP due to service change",
			"namespace", service.Namespace,
			"svcName", service.Name, "cluster",
			client.GetClusterName())
		mopEnqueuer.Enqueue(clusterOfEvent, mop, controllers_api.EventUpdate)
	}
}

func createMeshConfig(
	ctxLogger *zap.SugaredLogger,
	dryRun bool,
	clusterOfEvent string,
	clusterClient kube.Client,
	isRemoteClusterEvent bool,
	service *corev1.Service,
	trackedOwner *v1alpha1.Owner,
	metadata map[string]string,
	mopLister ListMopsForService,
	mopStatusUpdate UpdateMopStatus,
	primaryClient kube.Client,
	configGenerator ConfigGenerator,
	resourceManager resources.ResourceManager,
	metricsRegistry *prometheus.Registry,
	policies map[string]*unstructured.Unstructured,
) error {

	var mops []*mopv1.MeshOperator
	var err error

	// get all relevant mops: matching.ListMopsForService(service, c.listAllMopForGivenNamespace)
	mops, err = mopLister(service)
	if err != nil {
		ctxLogger.Errorf("error while listing matching MOPs for service %s/%s : %v", service.Namespace, service.Namespace, err)
		return err
	}

	// GVK needs to be set explicitly in order to make sure service.TypeMetadata field is set (TypeMetadata is used futher while converting svc to unstructured object type)
	service.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"})

	if isRemoteClusterEvent && !dryRun {
		err := CreatePrimaryNamespaceIfMissing(ctxLogger, primaryClient, service, metricsRegistry)
		if err != nil {
			ctxLogger.Errorf("encountered error while checking remote namespace existence in primary cluster for SVC event: %v", err)
			return err
		}
	}

	isServiceTracked := true
	msm, err := resourceManager.TrackOwner(clusterOfEvent, service.Namespace, trackedOwner, ctxLogger)
	if err != nil {
		isServiceTracked = false
		ctxLogger.Errorf("encountered error while tracking service: %v", err)
	}

	configObjects, err := generateConfig(
		ctxLogger,
		clusterOfEvent,
		service,
		metadata,
		msm,
		mops,
		mopStatusUpdate,
		configGenerator,
		policies)
	if err != nil && !meshOpErrors.IsOverlayingError(err) {
		return err
	}

	// if msm retrieval/update operation failed while tracking service (i.e. isServiceTracked=false),
	// do not invoke onServiceChange (since it performs operations over msm which is nil in this scenario)
	if isServiceTracked {
		trackingError := resourceManager.OnOwnerChange(clusterOfEvent, clusterClient, msm, service, trackedOwner, templating.GetConfigObjects(configObjects), ctxLogger)
		return common.GetFirstNonNil(err, trackingError)
	}
	return err
}

func generateConfig(
	ctxLogger *zap.SugaredLogger,
	clusterOfEvent string,
	service *corev1.Service,
	metadata map[string]string,
	msm *mopv1.MeshServiceMetadata,
	mops []*mopv1.MeshOperator,
	mopStatusUpdate UpdateMopStatus,
	configGenerator ConfigGenerator,
	policies map[string]*unstructured.Unstructured,
) ([]*templating.AppliedConfigObject, error) {

	object, objToUnstructuredErr := kube.ObjectToUnstructured(service)
	if objToUnstructuredErr != nil {
		return nil, fmt.Errorf("failed to convert %s object %s/%s to unstructured Object", service.Kind, service.Namespace, service.Name)
	}

	var err error
	var applicatorResults []*templating.AppliedConfigObject

	mopsWithValidOverlays := make([]*mopv1.MeshOperator, 0)
	mopNameToOverlay := make(map[string][]mopv1.Overlay, 0)
	mopNamesWithValidOverlays := make([]string, 0)
	var lastNotEmptyTemplate mopv1.TemplateType

	if features.EnableServiceConfigOverlays && len(mops) > 0 {
		for _, mop := range mops {
			overlaysInMop := mop.Spec.Overlays
			if len(overlaysInMop) > 0 {
				err = validateOverlayingMop(mop)
				if err != nil {
					ctxLogger.Errorw("mop record failed validation", "error", err)
					err = mopStatusUpdate(ctxLogger, service, err, nil, mop)
					continue
				}
				mopsWithValidOverlays = append(mopsWithValidOverlays, mop)
				mopNamesWithValidOverlays = append(mopNamesWithValidOverlays, mop.Name)
				mopNameToOverlay[mop.Name] = overlaysInMop
			}
			if mop.Spec.TemplateType != "" {
				lastNotEmptyTemplate = mop.Spec.TemplateType
			}
		}
	}

	ctxLogger.Debugf("generating configs for all valid overlaying MOPs %v targeting the service", mopNamesWithValidOverlays)
	renderCtx := templating.NewRenderRequestContext(
		object,
		metadata,
		MsmToOwnerRef(msm),
		clusterOfEvent,
		string(lastNotEmptyTemplate),
		mopNameToOverlay,
		policies)
	applicatorResults, generateConfigErr := configGenerator.GenerateConfig(&renderCtx, NoOpOnBeforeApply, ctxLogger)

	var applicationError error
	for _, result := range applicatorResults {
		if result.Error != nil {
			if k8serrors.IsConflict(result.Error) {
				applicationError = &meshOpErrors.NonCriticalReconcileError{Message: result.Error.Error()}
			}
			obj := result.Object
			applicationError = fmt.Errorf("failed to upsert object %s.%s of kind %v: %w",
				obj.GetName(), obj.GetNamespace(), obj.GroupVersionKind(), result.Error)
		}
	}

	mopStatusUpdateErr := updateOverlayingMopStatusForService(
		ctxLogger,
		service,
		mopsWithValidOverlays,
		generateConfigErr,
		applicationError,
		mopStatusUpdate)

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

	if applicationError != nil {
		return nil, applicationError
	}

	return applicatorResults, nil
}

func updateOverlayingMopStatusForService(
	ctxLogger *zap.SugaredLogger,
	service *corev1.Service,
	mopsWithOverlays []*mopv1.MeshOperator,
	generateConfigErr error,
	applicationError error,
	mopStatusUpdate UpdateMopStatus,
) error {
	var statusUpdateErr error

	var mopErrorMap map[string]*meshOpErrors.OverlayErrorInfo
	var overlayingError *meshOpErrors.OverlayingErrorImpl
	if generateConfigErr != nil && meshOpErrors.IsOverlayingError(generateConfigErr) {
		overlayingError, _ = generateConfigErr.(*meshOpErrors.OverlayingErrorImpl)
		mopErrorMap = overlayingError.ErrorMap
	}

	for _, mop := range mopsWithOverlays {
		ctxLogger.Debugf("updating status for overlaying mop %v", mop.Name)

		// Here we can encounter two types of issues: either an error resulting from overlaying process or an error while updating object in cluster.
		// In the latter case, we can't be sure it is caused by the overlay, but we're going to surface it in MOP.Status anyway.
		errInfo, overlayErrorExists := mopErrorMap[mop.Name]
		if !overlayErrorExists && applicationError != nil {
			errInfo = &meshOpErrors.OverlayErrorInfo{
				Message: applicationError.Error(),
			}
		}

		err := mopStatusUpdate(ctxLogger, service, nil, errInfo, mop)
		if err != nil {
			statusUpdateErr = err
		}
	}
	return statusUpdateErr
}

func UpdateOverlayMopStatus(
	ctxLogger *zap.SugaredLogger,
	mopClient versioned.Interface,
	clusterOfEvent string,
	service *corev1.Service,
	validationError error,
	errInfo *meshOpErrors.OverlayErrorInfo,
	mop *mopv1.MeshOperator,
	metricsRegistry *prometheus.Registry) error {

	if validationError != nil {
		ctxLogger.Debugf("updating validation results for mop %v", mop.Name)
		mop.Status = mopv1.MeshOperatorStatus{
			Phase:   PhaseFailed,
			Message: validationError.Error(),
		}

		recordOverlayingMopRenderingStats(mop, metricsRegistry, clusterOfEvent, constants.ServiceKind.Kind)
		return updateMopStatusInTheCluster(mop, mopClient, ctxLogger)
	}

	var updatedMop *mopv1.MeshOperator
	if errInfo != nil {
		updatedMop, _ = updateMopStatusForObject(mop, service.Kind, service.Name, PhaseFailed, getOverlayFailureSvcMessage(mop.Name, errInfo), "issues with applying overlay")
	} else {
		updatedMop, _ = updateMopStatusForObject(mop, service.Kind, service.Name, PhaseSucceeded, "", "")
	}

	noPendingPhases := true
	noFailedPhases := true

	for _, svcStatus := range mop.Status.Services {
		if svcStatus.Phase == PhasePending {
			noPendingPhases = false
		}
		if svcStatus.Phase == PhaseFailed {
			noFailedPhases = false
		}
	}

	for _, seStatus := range mop.Status.ServiceEntries {
		if seStatus.Phase == PhasePending {
			noPendingPhases = false
		}
		if seStatus.Phase == PhaseFailed {
			noFailedPhases = false
		}
	}

	// check if all services in mop.status are posted
	if noPendingPhases && noFailedPhases {
		updateRootStatus(mop, PhaseSucceeded, "")
	}
	if noPendingPhases { // this is the last iteration for the MOP, we can record stats
		recordOverlayingMopRenderingStats(updatedMop, metricsRegistry, clusterOfEvent, constants.ServiceKind.Kind)
	}
	return updateMopStatusInTheCluster(mop, mopClient, ctxLogger)
}

func recordOverlayingMopRenderingStats(overlayMop *mopv1.MeshOperator, metricsRegistry *prometheus.Registry, clusterName, targetKind string) {
	// Erase metrics for overlay mop, in case service-selector has changed since last reconcile
	EraseMopRenderingStats(metricsRegistry, clusterName, overlayMop.Namespace, overlayMop.Name)

	// Record cluster total stats
	for svcName := range overlayMop.Status.Services {
		metricLabels := metrics.GetLabelsForReconciledResource(meshv1alpha1.MopKind.Kind, clusterName, overlayMop.Namespace, overlayMop.Name, svcName, constants.ServiceKind.Kind, metrics.OverlayMopType)
		metrics.GetOrRegisterGaugeWithLabels(metrics.ObjectsConfiguredTotal, metricLabels, metricsRegistry).Set(1)
	}

	// Record cluster total stats
	for seName := range overlayMop.Status.ServiceEntries {
		metricLabels := metrics.GetLabelsForReconciledResource(meshv1alpha1.MopKind.Kind, clusterName, overlayMop.Namespace, overlayMop.Name, seName, constants.ServiceEntryKind.Kind, metrics.OverlayMopType)
		metrics.GetOrRegisterGaugeWithLabels(metrics.ObjectsConfiguredTotal, metricLabels, metricsRegistry).Set(1)
	}

	if len(overlayMop.Status.Services) == 0 && len(overlayMop.Status.ServiceEntries) == 0 { // Failed validation, or other issue
		metricLabels := metrics.GetLabelsForReconciledResource(meshv1alpha1.MopKind.Kind, clusterName, overlayMop.Namespace, overlayMop.Name, metrics.TargetAll, targetKind, metrics.OverlayMopType)
		metrics.GetOrRegisterGaugeWithLabels(metrics.ObjectsConfiguredTotal, metricLabels, metricsRegistry).Set(1)
	}

	if overlayMop.Status.Phase != PhaseSucceeded {

		if noMatchingRouteFoundError(overlayMop, targetKind) { // all services failed due to no matching route found to apply overlay error.
			IncrementCounterForObject(metricsRegistry, metrics.MeshOperatorReconcileUserError, clusterName, overlayMop.Namespace, overlayMop.Name, controllers_api.UnknownEvent)
		} else {
			IncrementCounterForObject(metricsRegistry, metrics.MeshOperatorReconcileFailed, clusterName, overlayMop.Namespace, overlayMop.Name, controllers_api.UnknownEvent)

		}

	}

	// Record MOP rendering stats
	RecordMopRenderingStats(metricsRegistry, clusterName, overlayMop, constants.ServiceKind.Kind)
}

func noMatchingRouteFoundError(overlayMop *mopv1.MeshOperator, targetKind string) bool {

	var noMatchingRouteErrorCount int
	var failedErrorCount int

	overlayMopStatus := overlayMop.Status.ServiceEntries

	if targetKind == "Service" {
		overlayMopStatus = overlayMop.Status.Services

	}

	for _, status := range overlayMopStatus {
		if status.Phase != PhaseSucceeded {
			failedErrorCount++
			if strings.Contains(status.Message, "no matching route found to apply overlay") {
				noMatchingRouteErrorCount++
			}
		}
	}
	return noMatchingRouteErrorCount == failedErrorCount
}

func handleReconcileError(
	err error,
	item QueueItem,
	clusterOfEvent string,
	metricsRegistry *prometheus.Registry,
	recordEvent func(eventType, reason, message string),
) error {
	if err != nil {
		if meshOpErrors.IsUserConfigError(err) || meshOpErrors.IsOverlayingError(err) {
			// User config error. Record an event and don't retry.
			IncrementCounterForQueueItem(metricsRegistry, metrics.ServiceReconcileUserError, clusterOfEvent, item)
			recordEvent(corev1.EventTypeWarning, constants.UnableToGenerateMeshConfig, err.Error())
			return nil
		}
		if meshOpErrors.IsNonCriticalReconcileError(err) {
			// Non-critical error. Just retry.
			IncrementCounterForQueueItem(metricsRegistry, metrics.ServiceReconcileNonCriticalError, clusterOfEvent, item)
			return err
		}
		IncrementCounterForQueueItem(metricsRegistry, metrics.ServicesReconcileFailed, clusterOfEvent, item)
		if item.event != controllers_api.EventDelete && !k8serrors.IsConflict(err) {
			recordEvent(corev1.EventTypeWarning, constants.MeshConfigError, err.Error())
		}
	}
	return err
}

func ExtractServiceObjectReference(object interface{}) *corev1.ObjectReference {
	return &corev1.ObjectReference{
		Kind:            constants.ServiceKind.Kind,
		APIVersion:      constants.ServiceResource.GroupVersion().String(),
		ResourceVersion: constants.ServiceResource.Version,
		Name:            object.(metav1.Object).GetName(),
		Namespace:       object.(metav1.Object).GetNamespace(),
		UID:             object.(metav1.Object).GetUID(),
	}
}

func updateMopStatusInTheCluster(mop *v1alpha1.MeshOperator, mopClient versioned.Interface, logger *zap.SugaredLogger) error {
	_, err := mopClient.MeshV1alpha1().MeshOperators(mop.Namespace).UpdateStatus(context.TODO(), mop, metav1.UpdateOptions{})
	if err != nil {
		if k8serrors.IsConflict(err) {
			logger.Infof("conflict while updating status, will retry: %s/%s", mop.Namespace, mop.Name)
			return &meshOpErrors.NonCriticalReconcileError{Message: err.Error()}
		} else {
			logger.Errorw("error updating status", "namespace", mop.Namespace, "name", mop.Name, "error", err)
		}
		return err
	}
	return nil
}

// GetEnabledServicesOnly - filter out services that have routing disabled
func GetEnabledServicesOnly(services map[string]*corev1.Service) map[string]*corev1.Service {
	enabledServices := map[string]*corev1.Service{}
	for clusterName, service := range services {
		if !common.IsOperatorDisabled(service) {
			enabledServices[clusterName] = service
		}
	}

	return enabledServices
}

func GetServices(services map[string]*corev1.Service) []*corev1.Service {
	var result []*corev1.Service
	for _, service := range services {
		result = append(result, service)
	}
	return result
}

// EnqueueServicesForStatefulSet - takes a StatefulSet resource, extracts the related Service and enqueues it.
func EnqueueServicesForStatefulSet(
	logger *zap.SugaredLogger,
	clusterName string,
	serviceEnqueuer controllers_api.ObjectEnqueuer,
	client kube.Client,
	stsEvent controllers_api.Event,
	optimizer ReconcileOptimizer,
	objects ...interface{}) {

	uniqueServices := make(map[string]*corev1.Service)

	for _, obj := range objects {
		object := verifyAndRecoverIfRequired(obj)
		statefulSet := object.(*appsv1.StatefulSet)
		service, err := client.KubeInformerFactory().Core().V1().Services().Lister().Services(statefulSet.GetNamespace()).Get(statefulSet.Spec.ServiceName)

		ctxLogger := logger.With(
			"service", fmt.Sprintf("%s/%s", statefulSet.GetNamespace(), statefulSet.Spec.ServiceName),
			"statefulSet", fmt.Sprintf("%s/%s", statefulSet.GetNamespace(), statefulSet.GetName()),
			"cluster", clusterName)

		if err != nil {
			ctxLogger.Warnf("failed to requeue service for statefulset: %q", err)
			continue
		}

		if !optimizer.ShouldEnqueueServiceForRelatedObjects(ctxLogger, stsEvent, service, statefulSet, clusterName, constants.StatefulSetKind.Kind) {
			ctxLogger.Debug("Not enqueuing. STS not changed")
			continue
		}

		// Use serviceName as the key for deduplication (we know that services live in the same namespace)
		uniqueServices[service.Name] = service
	}

	// Note that we enqueue service with an UpdateEvent regardless of the event occurred to the related object, as we want config to be re-rendered.
	for serviceName, service := range uniqueServices {
		serviceEnqueuer.Enqueue(clusterName, service, controllers_api.EventUpdate)
		logger.With(
			"service", serviceName,
			"namespace", service.Namespace,
			"cluster", clusterName,
		).Info("enqueued service for statefulset")
	}
}

func EnqueueServicesForDeployments(
	logger *zap.SugaredLogger,
	clusterName string,
	serviceEnqueuer controllers_api.ObjectEnqueuer,
	client kube.Client,
	relatedObjectEvent controllers_api.Event,
	optimizer ReconcileOptimizer,
	objects ...interface{}) {

	uniqueServices := make(map[string]*corev1.Service)

	for _, obj := range objects {
		object := verifyAndRecoverIfRequired(obj)
		metaObject := common.GetMetaObject(object)
		serviceName := common.GetLabelOrAlias(constants.DynamicRoutingServiceLabel, metaObject.GetLabels())

		ctxLogger := logger.With(
			"service", fmt.Sprintf("%s/%s", metaObject.GetNamespace(), serviceName),
			"related object", fmt.Sprintf("%s/%s", metaObject.GetNamespace(), metaObject.GetName()),
			"event", relatedObjectEvent,
			"cluster", clusterName)

		if serviceName == "" {
			ctxLogger.Warnf("%s label is empty in object", constants.DynamicRoutingServiceLabel)
			continue
		}

		service, err := client.KubeInformerFactory().Core().V1().Services().Lister().Services(metaObject.GetNamespace()).Get(serviceName)
		if err != nil {
			ctxLogger.Errorf("failed to requeue service for related object: %v", err)
			continue
		}

		if !optimizer.ShouldEnqueueServiceForRelatedObjects(ctxLogger, relatedObjectEvent, service, object, clusterName, constants.DeploymentKind.Kind) {
			ctxLogger.Debug("Not enqueuing. Deployment not changed")
			continue
		}

		// Use serviceName as the key for deduplication (we know that services live in the same namespace)
		uniqueServices[serviceName] = service
	}

	// Note that we enqueue service with an UpdateEvent regardless of the event occurred to the related object, as we want config to be re-rendered.
	for serviceName, service := range uniqueServices {
		serviceEnqueuer.Enqueue(clusterName, service, controllers_api.EventUpdate)
		logger.With(
			"service", serviceName,
			"namespace", service.Namespace,
			"event", relatedObjectEvent,
			"cluster", clusterName,
		).Info("enqueued service for deployment object")
	}
}

// EnqueueServiceForRolloutObj enqueues a service object which is referenced in a given Rollout object
func EnqueueServiceForRolloutObj(logger *zap.SugaredLogger,
	clusterName string,
	serviceEnqueuer controllers_api.ObjectEnqueuer,
	client kube.Client,
	rolloutEvent controllers_api.Event,
	optimizer ReconcileOptimizer,
	objs ...interface{}) {

	uniqueServices := make(map[string]*corev1.Service)

	for _, obj := range objs {
		object := verifyAndRecoverIfRequired(obj)
		rolloutObj := object.(*unstructured.Unstructured)
		namespace := rolloutObj.GetNamespace()

		serviceName, ok := rolloutObj.GetAnnotations()[constants.BgActiveServiceAnnotation]

		if !ok {
			logger.Warnf(constants.BgActiveServiceAnnotation+" annotation is missing in Rollout object: %s/%s", rolloutObj.GetNamespace(), rolloutObj.GetName())
			continue
		}

		if serviceName == "" {
			logger.Warnf(constants.BgActiveServiceAnnotation+" annotation value is empty in Rollout object: %s/%s", rolloutObj.GetNamespace(), rolloutObj.GetName())
			continue
		}

		ctxLogger := logger.With(
			"service", fmt.Sprintf("%s/%s", namespace, serviceName),
			"rollout", fmt.Sprintf("%s/%s", namespace, rolloutObj.GetName()),
			"cluster", clusterName)

		service, err := client.KubeInformerFactory().Core().V1().Services().Lister().Services(namespace).Get(serviceName)
		if err != nil {
			ctxLogger.Errorf("failed to enqueue service for Rollout: %q", err)
			continue
		}

		if !dynamicrouting.IsDynamicRoutingEnabled(service) {
			ctxLogger.Debug("Not enqueuing. Rollout/service not using dynamic-routing")
			continue
		}

		if !optimizer.ShouldEnqueueServiceForRelatedObjects(ctxLogger, rolloutEvent, service, rolloutObj, clusterName, constants.RolloutKind.Kind) {
			ctxLogger.Debug("Not enqueuing. Rollout not changed")
			continue
		}

		uniqueServices[serviceName] = service
	}

	// Note that we enqueue service with an UpdateEvent regardless of the event occurred to the related object, as we want config to be re-rendered.
	for serviceName, service := range uniqueServices {
		serviceEnqueuer.Enqueue(clusterName, service, controllers_api.EventUpdate)
		logger.With(
			"service", serviceName,
			"namespace", service.Namespace,
			"cluster", clusterName,
		).Info("enqueued service for rollout (deduped)")
	}
}

func ExtractStsMetadata(
	ctxLogger *zap.SugaredLogger,
	object metav1.Object,
	clients []kube.Client,
	eventRecorder record.EventRecorder,
) (map[string]string, error) {

	var metadata = map[string]string{}
	sts, err := statefulset.GetStatefulSetByService(clients, object.GetNamespace(), object.GetName())
	if err != nil {
		return nil, fmt.Errorf("error accessing STS index for service %s/%s: %w", object.GetNamespace(), object.GetName(), err)
	}
	if len(sts) > 0 {
		metadata[constants.ContextStsReplicaName] = fmt.Sprintf("%v", sts[0].GetName())
		metadata[constants.ContextStsReplicas] = fmt.Sprint(*sts[0].Spec.Replicas)
		metadata[constants.ContextStsOrdinalsStart] = "0"
		if sts[0].Spec.Ordinals != nil {
			metadata[constants.ContextStsOrdinalsStart] = fmt.Sprint(sts[0].Spec.Ordinals.Start)
		}
		if len(sts) > 1 {
			// Headless services (ClusterIP=None) legitimately have multiple StatefulSets,
			// so only emit a warning event for non-headless services.
			if common.IsHeadlessService(object) {
				ctxLogger.Info("headless service is used by multiple statefulsets (expected)")
			} else {
				ctxLogger.Warn("service is used by multiple statefulsets")
				eventRecorder.Event(ExtractServiceObjectReference(object), corev1.EventTypeWarning, constants.MeshConfigError, "service is used by multiple statefulsets")
			}
		}
		stss := []map[string]string{}
		for _, s := range sts {
			stss = append(stss, map[string]string{
				constants.ContextStsReplicaName: fmt.Sprintf("%v", s.GetName()),
				constants.ContextStsReplicas:    fmt.Sprint(*s.Spec.Replicas),
			})
		}
		jsonStss, err := json.Marshal(stss)
		if err != nil {
			return nil, fmt.Errorf("error marshalling multiple statefulsets into json string for service %s/%s: %w", object.GetNamespace(), object.GetName(), err)
		}
		metadata[constants.ContextStatefulSets] = string(jsonStss)
	} else {
		ctxLogger.Info("no STS info for service")
	}
	return metadata, nil
}

func ExtractDynamicRoutingMetadata(
	ctxLogger *zap.SugaredLogger,
	object metav1.Object,
	metricsRegistry *prometheus.Registry,
	clients []kube.Client) (map[string]string, error) {
	if !features.EnableDynamicRouting {
		return map[string]string{}, nil
	}

	metaObject := common.GetMetaObject(object)
	if !dynamicrouting.IsDynamicRoutingEnabled(metaObject) {
		return map[string]string{}, nil
	}

	subsetLabelKeys := common.GetAnnotationOrAlias(constants.DynamicRoutingLabels, metaObject.GetAnnotations())
	if subsetLabelKeys == "" {
		return map[string]string{}, nil
	}

	metricsLabel := map[string]string{
		metrics.ServiceNameTag: metaObject.GetName(),
		metrics.NamespaceTag:   metaObject.GetNamespace(),
	}

	deployments, err := deployment.GetDeploymentsByService(clients, metaObject.GetNamespace(), metaObject.GetName())
	if err != nil {
		ctxLogger.Errorf("error accessing Deployment index for %s/%s", metaObject.GetNamespace(), metaObject.GetName())
		metricsLabel[metrics.ReasonTag] = "failed_getting_deployments"
		metrics.GetOrRegisterCounterWithLabels(metrics.DynamicRoutingFailedMetrics, metricsLabel, metricsRegistry).Inc()
		return nil, err
	}
	labelCombinationsByKindIndex := make(map[string]map[string]map[string]string)

	var rollouts []*unstructured.Unstructured
	if features.EnableDynamicRoutingForBGAndCanaryServices {
		rollouts, err = rollout.GetRolloutsByServiceAcrossClusters(clients, metaObject.GetNamespace(), metaObject.GetName())
		if err != nil {
			ctxLogger.Errorf("error accessing Rollout index for %s/%s", metaObject.GetNamespace(), metaObject.GetName())
			metricsLabel[metrics.ReasonTag] = "failed_getting_rollouts"
			metrics.GetOrRegisterCounterWithLabels(metrics.DynamicRoutingFailedMetrics, metricsLabel, metricsRegistry).Inc()
			return nil, err
		}
	}

	if len(deployments) > 0 || len(rollouts) > 0 {
		var rolloutLabelCombinations map[string]map[string]string
		deploymentLabelCombinations := dynamicrouting.GetLabelCombinationsForDeployments(metaObject, deployments)
		if features.EnableDynamicRoutingForBGAndCanaryServices {
			rolloutLabelCombinations = dynamicrouting.GetLabelCombinationsForRollouts(metaObject, rollouts)
		}
		labelCombinationsByKindIndex = dynamicrouting.GetLabelCombinationsByKind(deploymentLabelCombinations, rolloutLabelCombinations)
	} else {
		// Try if dynamicrouting is wired with statefulsets instead
		sts, err := statefulset.GetStatefulSetByService(clients, metaObject.GetNamespace(), metaObject.GetName())
		if err != nil {
			ctxLogger.Errorf("error accessing STS index for %s/%s", metaObject.GetNamespace(), metaObject.GetName())
			return nil, err
		}
		stsLabelCombinations := dynamicrouting.GetLabelCombinationsForSTS(metaObject, sts)
		if len(stsLabelCombinations) > 0 {
			labelCombinationsByKindIndex["Statefulset"] = stsLabelCombinations
		}
	}

	if len(labelCombinationsByKindIndex) == 0 {
		return map[string]string{}, nil
	}

	retVal := make(map[string]string)
	if combinationsJson, err := json.Marshal(labelCombinationsByKindIndex); err != nil {
		ctxLogger.Errorf("error marshalling subset combination map into JSON string for %s/%s", metaObject.GetNamespace(), metaObject.GetName())
		metricsLabel[metrics.ReasonTag] = "failed_marshalling_subsets"
		metrics.GetOrRegisterCounterWithLabels(metrics.DynamicRoutingFailedMetrics, metricsLabel, metricsRegistry).Inc()
		return nil, err
	} else {
		retVal[constants.ContextDynamicRoutingCombinations] = string(combinationsJson)
	}
	defaultSubsetName, defaultSubsetErr := dynamicrouting.GetDefaultSubsetName(labelCombinationsByKindIndex, metaObject, ctxLogger, metricsRegistry)
	if defaultSubsetErr != nil {
		return nil, defaultSubsetErr
	}
	if defaultSubsetName != "" {
		retVal[constants.ContextDynamicRoutingDefaultSubset] = defaultSubsetName
	}

	if strings.Contains(subsetLabelKeys, ",") {
		ctxLogger.Warnf("service %s.%s has multiple subset label keys, path based routing behavior is undefined.", metaObject.GetName(), metaObject.GetNamespace())
		metrics.GetOrRegisterCounterWithLabels(metrics.DynamicRoutingMultipleLabelsMetrics, metricsLabel, metricsRegistry).Inc()
	} else {
		metrics.GetOrRegisterCounterWithLabels(metrics.DynamicRoutingSingleLabelMetrics, metricsLabel, metricsRegistry).Inc()
	}
	return retVal, nil
}

// We are explicitly deleting the Service owned mesh config instead of relying on k8s GC by using ownerRef because
// this same logic will also be used for multi cluster setup, where the controller running in the primary cluster
// will be creating, updating and deleting mesh config in the remote cluster as well.
func deleteMeshConfig(
	resourceManager resources.ResourceManager,
	metricsRegistry *prometheus.Registry,
	clusterName, namespace, name string,
	uid types.UID,
	ctxLogger *zap.SugaredLogger) error {
	ctxLogger.Infof("deleting meshconfig for service: %s/%s/%s/%s", clusterName, namespace, name, uid)
	return resourceManager.UnTrackOwner(clusterName, namespace, constants.ServiceKind.Kind, name, uid, ctxLogger)
}

func isArgoPresentInCluster(clusterClient kube.Client) bool {
	return features.EnableArgoIntegration &&
		rollout.IsRolloutResourcePresentInCluster(clusterClient)
}

func createRolloutInformerAndIndexer(logger *zap.SugaredLogger, clusterClient kube.Client) (informers.GenericInformer, error) {
	if !isArgoPresentInCluster(clusterClient) {
		logger.Infof("argo is not enabled in cluster")
		return nil, nil
	}

	logger.Infof("argo is enabled in cluster, creating Rollout indexer.")
	rolloutInformer := clusterClient.DynamicInformerFactory().ForResource(constants.RolloutResource)
	err := rollout.AddServiceNameToRolloutIndexer(rolloutInformer.Informer())
	if err != nil {
		return nil, err
	}
	return rolloutInformer, nil
}

// reMutateRollouts - patch the argo rollout associated to the service, if any and feature is enabled.
// Errors are fully handled.
func reMutateRollouts(
	logger *zap.SugaredLogger,
	mutatingTemplatesManager templating.TemplatesManager,
	serviceTemplatesManager templating.TemplatesManager,
	metricsRegistry *prometheus.Registry,
	svc *corev1.Service,
	clusterName string,
	clusterClient kube.Client) error {

	if !isArgoPresentInCluster(clusterClient) {
		logger.Debug("re-mutation not enabled or Rollout CRD not in cluster")
		return nil
	}

	argoManaged, remutationTemplate := rollout.GetReMutationTemplate(serviceTemplatesManager, svc)
	if !argoManaged {
		logger.Debug("not argo managed")
		return nil
	}

	rolloutsInformer := clusterClient.DynamicInformerFactory().ForResource(constants.RolloutResource)
	rollouts, err := rollout.GetRolloutsByService(rolloutsInformer.Informer(), svc.Namespace, svc.Name)

	if err != nil {
		metrics.GetOrRegisterCounterWithLabels(metrics.RolloutReMutateErrors,
			rollout.GetMutatingErrorLabel(rollout.MutatingErrorNoRollout, clusterName),
			metricsRegistry).Inc()
		return fmt.Errorf("failed to obtain rollouts for service: %w", err)
	}

	// Sort rollouts to make sure these are updated in a predictable order
	slices.SortFunc(rollouts, func(left, right *unstructured.Unstructured) int {
		return strings.Compare(left.GetName(), right.GetName())
	})

	for _, rol := range rollouts {
		if features.EnableOcmIntegration && ocm2.IsOcmManagedObject(rol) {
			logger.Infof("skipped re-mutation for ocm managed rollout %s for service %s", rol.GetName(), svc.Name)
			continue
		}
		rolloutJson, _ := json.Marshal(rol)
		var metadata map[string]string
		if features.EnableDynamicRoutingForBGAndCanaryServices {
			metadata = dynamicrouting.GetMetadataIfDynamicRoutingEnabled(rol, svc, logger, metricsRegistry)
		}
		patches, err := rollout.CreateRolloutPatches(mutatingTemplatesManager, svc, rolloutJson, remutationTemplate, metadata)

		if err != nil {
			metrics.GetOrRegisterCounterWithLabels(metrics.RolloutReMutateErrors,
				rollout.GetMutatingErrorLabel(rollout.MutatingErrorPatching, clusterName),
				metricsRegistry).Inc()

			return fmt.Errorf("failed to render patches for service and rollout %s: %w", rol.GetName(), err)
		}

		if patches != "" {
			_, err = clusterClient.Dynamic().Resource(constants.RolloutResource).Namespace(svc.Namespace).
				Patch(context.TODO(), rol.GetName(), types.JSONPatchType, []byte(patches), metav1.PatchOptions{})

			if err != nil {
				metrics.GetOrRegisterCounterWithLabels(metrics.RolloutReMutateErrors,
					rollout.GetMutatingErrorLabel(rollout.MutatingErrorPatching, clusterName),
					metricsRegistry).Inc()

				return fmt.Errorf("failed to patch rollout %s: %w", rol.GetName(), err)
			}

			logger.Infof("remutated rollout %s for service %s", rol.GetName(), svc.Name)
			metrics.GetOrRegisterCounter(metrics.RolloutReMutateSuccess, metricsRegistry).Inc()
		}
	}
	return nil
}

func getOverlayFailureSvcMessage(mopName string, errInfo *meshOpErrors.OverlayErrorInfo) string {
	return fmt.Sprintf("error encountered when overlaying: mop %s, overlay <%d>: %v",
		mopName, errInfo.OverlayIndex, errInfo.Message)
}

func createWorkQueue(name string) workqueue.RateLimitingInterface {
	var queue workqueue.TypedRateLimitingInterface[any]

	if features.UsePriorityQueue {
		queue = priorityqueue.New[any](name, func(opts *priorityqueue.Opts[any]) {
			if provider := metrics.GetMetricsProvider(); provider != nil {
				opts.MetricProvider = provider
			}
		})
	} else {
		config := workqueue.TypedRateLimitingQueueConfig[any]{
			Name: name,
		}
		if provider := metrics.GetMetricsProvider(); provider != nil {
			config.MetricsProvider = provider
		}
		queue = workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[any](), config)
	}

	return queue
}

func createWorkQueueWithWatchdog(ctx context.Context, name string, logger *zap.SugaredLogger, metricsRegistry *prometheus.Registry) workqueue.RateLimitingInterface {
	queue := createWorkQueue(name)

	// Wrap with watchdog if feature is enabled
	if features.EnableQueueWatchdog {
		// Need to convert to TypedRateLimitingInterface[any]
		if typedQueue, ok := queue.(workqueue.TypedRateLimitingInterface[any]); ok {
			logger.Infof("Creating queue with watchdog for: %s", name)
			return NewWorkqueueWatchdog(ctx, typedQueue, name, logger, metricsRegistry)
		}
	}

	return queue
}
