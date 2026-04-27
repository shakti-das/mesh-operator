package resources

import (
	"context"
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/deployment"

	meshiov1alpha1 "github.com/istio-ecosystem/mesh-operator/pkg/generated/informers/externalversions/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	"github.com/istio-ecosystem/mesh-operator/pkg/reconcilemetadata"
	"github.com/istio-ecosystem/mesh-operator/pkg/rollout"
	"github.com/istio-ecosystem/mesh-operator/pkg/statefulset"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	meshOpErrors "github.com/istio-ecosystem/mesh-operator/pkg/errors"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/transition"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"

	"k8s.io/client-go/util/retry"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/generated/clientset/versioned"
	"go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ResourceManager interface {
	TrackOwner(clusterName string, namespace string, owner *v1alpha1.Owner, ctxLogger *zap.SugaredLogger) (*v1alpha1.MeshServiceMetadata, error)
	OnOwnerChange(clusterName string, clusterClient kube.Client, msm *v1alpha1.MeshServiceMetadata, ownerObject interface{}, owner *v1alpha1.Owner, resources []*unstructured.Unstructured, ctxLogger *zap.SugaredLogger) error
	UnTrackOwner(clusterName string, namespace string, kind string, name string, uid types.UID, ctxLogger *zap.SugaredLogger) error
}

type resourceManager struct {
	logger *zap.SugaredLogger

	msmInformer meshiov1alpha1.MeshServiceMetadataInformer

	meshApiClient versioned.Interface

	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface

	metricsRegistry      *prometheus.Registry
	reconcileHashManager reconcilemetadata.ReconcileHashManager
	timeProvider         common.TimeProvider
}

type dryRunResourceManager struct {
	logger *zap.SugaredLogger
}

func NewDryRunResourceManager(logger *zap.SugaredLogger) ResourceManager {
	return &dryRunResourceManager{logger: logger}
}

func NewServiceResourceManager(
	logger *zap.SugaredLogger,
	msmInformer meshiov1alpha1.MeshServiceMetadataInformer,
	meshApiClient versioned.Interface,
	dynamicClient dynamic.Interface,
	discoveryClient discovery.DiscoveryInterface,
	metricsRegistry *prometheus.Registry) ResourceManager {
	return &resourceManager{
		logger:               logger,
		msmInformer:          msmInformer,
		meshApiClient:        meshApiClient,
		dynamicClient:        dynamicClient,
		discoveryClient:      discoveryClient,
		metricsRegistry:      metricsRegistry,
		reconcileHashManager: reconcilemetadata.NewReconcileHashManager(),
		timeProvider:         common.NewRealTimeProvider(),
	}
}

// TrackOwner - creates, if necessary a new MeshServiceMetadata record and associates it with the owner.
func (m *resourceManager) TrackOwner(clusterName string, namespace string, owner *v1alpha1.Owner, ctxLogger *zap.SugaredLogger) (*v1alpha1.MeshServiceMetadata, error) {
	var msm *v1alpha1.MeshServiceMetadata
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ctxLogger.Debugf("retrying tracking for owner: %s/%s", namespace, owner.Name)
		foundMsm, err := m.getOrCreateMsmRecord(clusterName, namespace, owner)
		if err != nil {
			return fmt.Errorf("failed to obtain msm record for owner %s/%s/%s: %w", clusterName, namespace, owner.Name, err)
		}

		// Owner is already tracked
		if isOwnerTracked(foundMsm, clusterName, owner.Kind, owner.Name, owner.UID) {
			updatedMsm, fixError := m.fixOwnerReference(foundMsm, clusterName, namespace, owner)
			if fixError == nil {
				msm = updatedMsm
			}
			return fixError
		}

		// Add owner to tracking
		foundMsm.Spec.Owners = append(foundMsm.Spec.Owners, *owner)
		updatedMsm, err := m.meshApiClient.MeshV1alpha1().MeshServiceMetadatas(namespace).Update(context.TODO(), foundMsm, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to add owner to msm record %s/%s/%s: %w", clusterName, namespace, owner.Name, err)
		}
		msm = updatedMsm
		return nil
	})

	return msm, err
}

// fixOwnerReference - if necessary, update owner reference to have a proper apiVersion to group/version format
func (m *resourceManager) fixOwnerReference(msm *v1alpha1.MeshServiceMetadata, clusterName, namespace string, owner *v1alpha1.Owner) (*v1alpha1.MeshServiceMetadata, error) {
	if features.EnableMsmOwnerFix && owner.Kind == constants.ServiceEntryKind.Kind {
		updateRecord := false
		for idx, ref := range msm.Spec.Owners {
			if isReferenceToTheOwner(clusterName, owner.Kind, owner.Name, owner.UID, ref) {
				if ref.ApiVersion != owner.ApiVersion {
					// This reference needs fixing
					msm.Spec.Owners[idx].ApiVersion = owner.ApiVersion
					updateRecord = true
				}
			}
		}

		if updateRecord {
			return m.meshApiClient.MeshV1alpha1().MeshServiceMetadatas(namespace).Update(context.TODO(), msm, metav1.UpdateOptions{})
		}
	}
	return msm, nil
}

// OnOwnerChange - should be invoked after the owner resources change, like after Service Create / Update events.
// This method is responsible for updating the list of resources associated with the owner service as well as deleting the obsolete ones.
func (m *resourceManager) OnOwnerChange(clusterName string, clusterClient kube.Client, msm *v1alpha1.MeshServiceMetadata, ownerObject interface{}, owner *v1alpha1.Owner, resources []*unstructured.Unstructured, ctxLogger *zap.SugaredLogger) error {
	newResources := resourceToReference(resources...)
	// Diff existing vs new resources and delete delta
	combinedResources, staleResources := markStaleResources(msm.Spec.OwnedResources, newResources)

	var deletedResources []v1alpha1.OwnedResource
	var deletionError error

	deletedObjects := make(map[string]v1alpha1.OwnedResource, len(staleResources))

	if features.EnableCleanUpObsoleteResources {
		deletedResources, deletionError = m.deleteResources(clusterName, staleResources, msm.Namespace, msm.Name, true, ctxLogger)
		for _, deletedResource := range deletedResources {
			lookupKey := makeLookUpKey(deletedResource)
			deletedObjects[lookupKey] = deletedResource
		}
		recordStaleResourceDeletedMetrics(owner, clusterName, m.metricsRegistry, msm.Namespace, len(deletedObjects))

		if len(deletedObjects) > 0 {
			combinedResources = deleteObjectsInCombinedResources(combinedResources, deletedObjects)
		}
	}

	updatedMSM, err := m.updateOwnerResourcesInMsm(msm, combinedResources, owner, ctxLogger)
	if err != nil {
		return err
	}
	// track reconcile metadata on MSM status
	m.trackReconcileMetadata(clusterName, clusterClient, updatedMSM, ownerObject, owner.Kind)
	recordStaleResourceMetrics(owner.Kind, clusterName, m.metricsRegistry, msm, len(staleResources)-len(deletedObjects))

	return deletionError
}

// UnTrackOwner - should be invoked whenever an owning resource Delete event is received.
// Method is responsible for dis-associating the owner from its MeshServiceMetadata record and,
// if no more services/service entries are associated with it, deleting the MSM record.
func (m *resourceManager) UnTrackOwner(clusterName string, namespace string, kind string, ownerName string,
	ownerUid types.UID, ctxLogger *zap.SugaredLogger) error {

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {

		// naming convention for msm based on kind
		msmRecordName := common.GetRecordNameSuffixedByKind(ownerName, kind)

		var msm *v1alpha1.MeshServiceMetadata
		var err error

		msm, err = m.getMSMRecord(namespace, msmRecordName)

		if err != nil {
			if k8serrors.IsNotFound(err) {
				ctxLogger.Warnf("owner service not tracked:%s/%s/%s Ignoring untrack request.",
					clusterName, namespace, ownerName)
				return nil
			}
			return fmt.Errorf("failed to find msm record: %w", err)
		}

		msm.Spec.Owners = removeOwnerFromOwnersList(clusterName, kind, ownerName, ownerUid, msm.Spec.Owners)
		if len(msm.Spec.Owners) > 0 {
			// Update record, there are other owner services using it
			updatedMSM, err := m.meshApiClient.MeshV1alpha1().MeshServiceMetadatas(namespace).
				Update(context.TODO(), msm, metav1.UpdateOptions{})
			if err != nil {
				return wrapError(err, "failed to update msm record")
			}
			// delete reconcile metadata for given owner from MSM status
			msmUntrackError := m.unTrackReconcileMetadata(clusterName, updatedMSM)
			if msmUntrackError != nil {
				return wrapError(msmUntrackError, "failed to untrack reconcile metadata")
			}
		} else {
			if features.EnableCrossNamespaceResourceDeletion {
				crossNamespaceResources := getCrossNamespaceResources(msm)
				if len(crossNamespaceResources) > 0 {
					deletedResources, crossNsDeletionError := m.deleteResources(clusterName, crossNamespaceResources, namespace, msmRecordName, false, ctxLogger)
					recordCrossNamespaceResourceDeletionMetrics(kind, namespace, ownerName, clusterName, m.metricsRegistry, len(deletedResources))

					// return the error so that deletion will be retried again in next retry loop
					if crossNsDeletionError != nil {
						return crossNsDeletionError
					}
				}
			}

			// Delete, no more services rely on it
			// Note that we're using optimistic locking here to avoid deleting record if it has been updated
			opts := metav1.DeleteOptions{
				Preconditions: &metav1.Preconditions{ResourceVersion: &msm.ResourceVersion},
			}
			err = m.meshApiClient.MeshV1alpha1().MeshServiceMetadatas(namespace).Delete(context.TODO(), msmRecordName, opts)
			if err != nil {
				if k8serrors.IsNotFound(err) {
					ctxLogger.Infof("msm record %s/%s not found in cluster, consider that as deleted", msm.Namespace, msm.Name)
				} else {
					return wrapError(err, "failed to delete msm record")
				}
			}

			removeAllMetricsForObject(m.metricsRegistry, msm)
			transition.EraseTransitionStats(m.metricsRegistry, namespace, constants.ServiceKind.Kind, ownerName)
		}
		return nil
	})

	if err != nil {
		m.logger.Errorf("failed to untrack service %s/%s/%s: %o", clusterName, namespace, ownerName, err)
	}
	return err
}

// trackReconcileMetadata tracks reconciliation related metadata on MSM status
func (m *resourceManager) trackReconcileMetadata(clusterName string, clusterClient kube.Client, msm *v1alpha1.MeshServiceMetadata, ownerObject interface{}, kind string) error {
	if !features.EnableSkipReconcileOnRestart {
		return nil
	}

	metaObject := common.GetMetaObject(ownerObject)
	namespace := metaObject.GetNamespace()
	name := metaObject.GetName()

	msm.Status.LastReconciledTime = m.timeProvider.Now()
	if msm.Status.ReconciledObjectHash == nil {
		msm.Status.ReconciledObjectHash = make(map[string]string)
	}

	hashKey := m.reconcileHashManager.ComputeHashKey(clusterName, kind, namespace, name)
	msm.Status.ReconciledObjectHash[hashKey] = m.reconcileHashManager.ComputeObjectHash(ownerObject, kind)
	msm.Status.ReconciledTemplateHash = m.reconcileHashManager.GetConfigTemplateHash()

	if features.SkipSvcEnqueueForRelatedObjects && kind == constants.ServiceKind.Kind { // fetch related objects for service and update hash in MSM

		clients := []kube.Client{clusterClient}
		stsKind := constants.StatefulSetKind.Kind
		rolloutKind := constants.RolloutKind.Kind
		deploymentKind := constants.DeploymentKind.Kind

		// fetch statefulsets for service
		statefulsets, err := statefulset.GetStatefulSetByService(clients, namespace, name)
		if err != nil {
			return err
		}

		for _, sts := range statefulsets {
			stsHashKey := m.reconcileHashManager.ComputeHashKey(clusterName, stsKind, sts.GetNamespace(), sts.GetName())
			msm.Status.ReconciledObjectHash[stsHashKey] = m.reconcileHashManager.ComputeObjectHash(sts, stsKind)
		}

		// fetch rollouts for service
		rollouts, err := rollout.GetRolloutsByServiceAcrossClusters(clients, namespace, name)
		if err != nil {
			return err
		}

		for _, rollout := range rollouts {
			rolloutHashKey := m.reconcileHashManager.ComputeHashKey(clusterName, rolloutKind, rollout.GetNamespace(), rollout.GetName())
			msm.Status.ReconciledObjectHash[rolloutHashKey] = m.reconcileHashManager.ComputeObjectHash(rollout, rolloutKind)
		}

		//fetch deployments for service
		deployments, err := deployment.GetDeploymentsByService(clients, namespace, name)
		if err != nil {
			return err
		}

		for _, deployment := range deployments {
			deploymentHashKey := m.reconcileHashManager.ComputeHashKey(clusterName, deploymentKind, deployment.GetNamespace(), deployment.GetName())
			msm.Status.ReconciledObjectHash[deploymentHashKey] = m.reconcileHashManager.ComputeObjectHash(deployment, deploymentKind)
		}
	}
	_, msmStatusUpdateErr := m.meshApiClient.MeshV1alpha1().MeshServiceMetadatas(msm.Namespace).UpdateStatus(context.TODO(), msm, metav1.UpdateOptions{})
	return msmStatusUpdateErr
}

// unTrackReconcileMetadata removes reconcile metadata from MSM status
func (m *resourceManager) unTrackReconcileMetadata(clusterName string, msm *v1alpha1.MeshServiceMetadata) error {
	if !features.EnableSkipReconcileOnRestart {
		return nil
	}
	delete(msm.Status.ReconciledObjectHash, clusterName)
	_, msmStatusUpdateErr := m.meshApiClient.MeshV1alpha1().MeshServiceMetadatas(msm.Namespace).UpdateStatus(context.TODO(), msm, metav1.UpdateOptions{})
	return msmStatusUpdateErr
}

// deleteResources deletes list of resources referenced in MSM record msmName
func (m *resourceManager) deleteResources(clusterName string, resources []v1alpha1.OwnedResource, msmNamespace string, msmName string, staleResource bool, ctxLogger *zap.SugaredLogger) ([]v1alpha1.OwnedResource, error) {

	var deletionError error
	var deletedResources []v1alpha1.OwnedResource
	for _, resource := range resources {
		gvk := schema.FromAPIVersionAndKind(resource.ApiVersion, resource.Kind)
		gvr, kindToResourceConversionError := kube.ConvertGvkToGvr(clusterName, m.discoveryClient, gvk)
		if kindToResourceConversionError != nil {
			deletionError = kindToResourceConversionError
			ctxLogger.Errorf("encountered kind to resource conversion error while deleting resource <%s/%s/%s> referenced in MSM: %s/%s: %v", resource.Kind,
				resource.Namespace, resource.Name, msmNamespace, msmName, kindToResourceConversionError.Error())
			continue
		}

		object, err := m.dynamicClient.Resource(gvr).Namespace(resource.Namespace).Get(context.TODO(), resource.Name, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// if object is not found, pretend resource was deleted
				ctxLogger.Errorf("object <%s/%s/%s> (referenced in MSM: %s/%s) not found in cluster, consider that as deleted", resource.Kind,
					resource.Namespace, resource.Name, msmNamespace, msmName)
				deletedResources = append(deletedResources, resource)
				continue
			} else {
				deletionError = err
			}
		}

		if canConfigBeDeleted(object, msmNamespace, msmName) {
			err = m.deleteResource(resource, gvr, msmNamespace, msmName, staleResource)
			if err != nil {
				deletionError = err
				ctxLogger.Errorf("error while attempting to delete resource <%s/%s/%s> referenced in MSM: %s/%s : %v", resource.Kind,
					resource.Namespace, resource.Name, msmNamespace, msmName, deletionError.Error())
				continue
			}
			deletedResources = append(deletedResources, resource)
		}
	}
	return deletedResources, deletionError
}

// canConfigBeDeleted checks whether the config object provided can be deleted. These checks cover mesh-op template takeover, and cross-ns object deletion.
func canConfigBeDeleted(object *unstructured.Unstructured, msmNamespace string, ownerName string) bool {
	if !transition.TakenOverByMeshOp(object.GetLabels()) {
		// delete resource iff it is owned by MeshOperator
		return false
	}

	if object.GetNamespace() == msmNamespace {
		return true
	}
	return features.EnableCrossNamespaceResourceDeletion &&
		common.GetAnnotation(object, constants.ResourceParent) == common.GetResourceParent(msmNamespace, ownerName, object.GetKind())
}

func (m *resourceManager) getOrCreateMsmRecord(clusterName string, namespace string, owner *v1alpha1.Owner) (
	*v1alpha1.MeshServiceMetadata, error) {
	if owner == nil {
		return nil, fmt.Errorf("failed to create msm record as the owner is nil")
	}

	// naming convention for msm based on kind
	msmRecordName := common.GetRecordNameSuffixedByKind(owner.Name, owner.Kind)

	var msm *v1alpha1.MeshServiceMetadata
	var err error

	msm, err = m.getMSMRecord(namespace, msmRecordName)

	// Found the record
	if err == nil {
		return msm, nil
	}
	// Some other error
	if !k8serrors.IsNotFound(err) {
		return nil, err
	}

	m.logger.Debugf("creating msm record for %s/%s/%s/%s ...", clusterName, namespace, owner.Kind, owner.Name)
	msmToCreate := &v1alpha1.MeshServiceMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      msmRecordName,
		},
		Spec: v1alpha1.MeshServiceMetadataSpec{
			OwnedResources: []v1alpha1.OwnedResource{},
			Owners:         []v1alpha1.Owner{},
		},
	}

	newMsm, err := m.meshApiClient.MeshV1alpha1().MeshServiceMetadatas(namespace).
		Create(context.TODO(), msmToCreate, metav1.CreateOptions{})

	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return nil, &meshOpErrors.NonCriticalReconcileError{Message: fmt.Sprintf("failed to create msm record: %s", err.Error())}
		} else {
			return nil, fmt.Errorf("failed to create msm record: %w", err)
		}
	}
	return newMsm, nil
}

func (m *resourceManager) getMSMRecord(namespace, name string) (*v1alpha1.MeshServiceMetadata, error) {
	if features.UseInformerInTrackingComponent {
		msm, err := m.msmInformer.Lister().MeshServiceMetadatas(namespace).Get(name)
		return msm, err
	}
	msm, err := m.meshApiClient.MeshV1alpha1().MeshServiceMetadatas(namespace).
		Get(context.TODO(), name, metav1.GetOptions{})
	return msm, err
}

func (m *dryRunResourceManager) TrackOwner(clusterName string, namespace string, owner *v1alpha1.Owner, _ *zap.SugaredLogger) (*v1alpha1.MeshServiceMetadata, error) {
	m.logger.Debugf("Dry run. Track owner: %s/%s/%s", clusterName, namespace, owner.Name)
	return &v1alpha1.MeshServiceMetadata{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.MsmKind.Kind,
			APIVersion: v1alpha1.ApiVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: common.GetRecordNameSuffixedByKind(owner.Name, owner.Kind),
		},
	}, nil
}

func (m *dryRunResourceManager) OnOwnerChange(_ string, _ kube.Client, msm *v1alpha1.MeshServiceMetadata, _ interface{}, owner *v1alpha1.Owner,
	_ []*unstructured.Unstructured, _ *zap.SugaredLogger) error {
	m.logger.Debugf("Dry run. On owner change: %s/%s/%s", owner.Cluster, msm.Namespace, owner.Name)
	return nil
}

func (m *dryRunResourceManager) UnTrackOwner(clusterName string, namespace string, kind string, ownerName string, ownerUid types.UID, _ *zap.SugaredLogger) error {
	m.logger.Debugf("Dry run. Untrack owner: %s/%s/%s/%s/%s", clusterName, namespace, kind, ownerName, ownerUid)
	return nil
}

func (m *resourceManager) updateOwnerResourcesInMsm(msm *v1alpha1.MeshServiceMetadata, updatedResources []v1alpha1.OwnedResource, owner *v1alpha1.Owner, ctxLogger *zap.SugaredLogger) (*v1alpha1.MeshServiceMetadata, error) {
	// Update msm resources list
	msmToUpdate := msm.DeepCopy()
	msmToUpdate.Spec.OwnedResources = updatedResources
	// If we encounter a conflict (optimistic locking) exit with error to retry render again
	ctxLogger.Infof("updating MSM record %s/%s with the latest ownedResources: %v",
		msm.GetName(), msm.GetNamespace(), updatedResources)
	updatedMSM, err := m.meshApiClient.MeshV1alpha1().MeshServiceMetadatas(msm.Namespace).
		Update(context.TODO(), msmToUpdate, metav1.UpdateOptions{})
	if err != nil {
		return nil, &meshOpErrors.NonCriticalReconcileError{
			Message: fmt.Sprintf("failed to update resources list for %s/%s/%s: %s",
				owner.Cluster, msm.Namespace, owner.Name, err.Error())}
	}
	return updatedMSM, nil
}

func (m *resourceManager) deleteResource(resource v1alpha1.OwnedResource, gvr schema.GroupVersionResource, msmNamespace string, msmName string, staleResource bool) error {
	deletionMessage := "deleting resource"
	if staleResource {
		deletionMessage = "deleting stale resource"
	}
	m.logger.Infof("%s <%s/%s/%s/%s> referenced in MSM: %s/%s", deletionMessage,
		resource.ApiVersion, resource.Kind, resource.Namespace, resource.Name, msmNamespace, msmName)
	err := m.dynamicClient.Resource(gvr).Namespace(resource.Namespace).Delete(context.TODO(), resource.Name, metav1.DeleteOptions{})
	if err == nil || k8serrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return &meshOpErrors.NonCriticalReconcileError{Message: err.Error()}
	}
	return nil
}

// getCrossNamespaceResources fetches all the cross namespace resource referenced in MSM record
func getCrossNamespaceResources(msm *v1alpha1.MeshServiceMetadata) []v1alpha1.OwnedResource {
	serviceNamespace := msm.Namespace
	ownedResources := msm.Spec.OwnedResources
	var crossNamespaceResources []v1alpha1.OwnedResource
	for _, ownedResource := range ownedResources {
		if serviceNamespace != ownedResource.Namespace {
			crossNamespaceResources = append(crossNamespaceResources, ownedResource)
		}
	}
	return crossNamespaceResources
}

func recordStaleResourceMetrics(kind string, clusterName string, registry *prometheus.Registry, msm *v1alpha1.MeshServiceMetadata, staleResources int) {
	labels := commonmetrics.GetLabelsForK8sResource(kind, clusterName, msm.GetNamespace(), msm.GetName())
	commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.StaleConfigObjects, labels, registry).Set(float64(staleResources))
}

func recordStaleResourceDeletedMetrics(owner *v1alpha1.Owner, clusterName string, registry *prometheus.Registry, namespace string, staleResources int) {
	labels := commonmetrics.GetLabelsForK8sResource(owner.Kind, clusterName, namespace, owner.Name)
	commonmetrics.GetOrRegisterGaugeWithLabels(commonmetrics.StaleConfigDeleted, labels, registry).Set(float64(staleResources))
}

func recordCrossNamespaceResourceDeletionMetrics(ownerKind string, ownerNamespace string, ownerName string, clusterName string, registry *prometheus.Registry, numberOfDeletedResources int) {
	labels := commonmetrics.GetLabelsForK8sResource(ownerKind, clusterName, ownerNamespace, ownerName)
	commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.CrossNamespaceConfigDeleted, labels, registry).Add(float64(numberOfDeletedResources))
}

func removeAllMetricsForObject(registry *prometheus.Registry, msm *v1alpha1.MeshServiceMetadata) {
	commonmetrics.EraseMetricsForK8sResource(
		registry,
		[]string{
			commonmetrics.StaleConfigObjects,
			commonmetrics.StaleConfigDeleted,
			commonmetrics.ObjectsConfiguredTotal,
			commonmetrics.MeshConfigResourcesTotal,
		},
		map[string]string{
			commonmetrics.NamespaceLabel:    msm.Namespace,
			commonmetrics.ResourceNameLabel: msm.Name,
		})

	commonmetrics.EraseMetricsForK8sResource(
		registry,
		[]string{
			commonmetrics.MeshOperatorFailed,
			commonmetrics.ObjectsConfiguredTotal,
		},
		map[string]string{
			commonmetrics.NamespaceLabel:          msm.Namespace,
			commonmetrics.TargetResourceNameLabel: msm.Name,
		})
}

func deleteObjectsInCombinedResources(combinedResources []v1alpha1.OwnedResource, deletedObjects map[string]v1alpha1.OwnedResource) []v1alpha1.OwnedResource {
	updatedResources := make([]v1alpha1.OwnedResource, 0)
	for _, object := range combinedResources {
		if _, ok := deletedObjects[makeLookUpKey(object)]; ok {
			continue
		}
		updatedResources = append(updatedResources, object)
	}
	return updatedResources
}

func makeLookUpKey(resource v1alpha1.OwnedResource) string {
	return resource.Kind + "/" + resource.Namespace + "/" + resource.Name
}

func wrapError(err error, message string) error {
	if k8serrors.IsConflict(err) {
		return &meshOpErrors.NonCriticalReconcileError{
			Message: fmt.Sprintf(message+": %s", err.Error()),
		}
	}
	return fmt.Errorf(message+": %w", err)
}
