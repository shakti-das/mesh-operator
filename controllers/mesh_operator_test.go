package controllers

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controllers_api"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/resources_test"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"

	k8stesting "k8s.io/client-go/testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/controller_test"
	kubetest "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating_test"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	coretesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zaptest"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
)

var (
	testFaultFilter = v1alpha1.ExtensionElement{
		FaultFilter: &v1alpha1.HttpFaultFilter{Abort: &v1alpha1.HttpFaultFilterAbort{HttpStatus: kube_test.DefaultAbortHttpStatus}},
	}

	testConfigNsExtension = v1alpha1.ExtensionElement{
		ActiveHealthCheckFilter: &v1alpha1.ActiveHealthCheck{},
	}
)

// TestReconcileMopActions validates object create/update/delete operations done in meshOperatorController.reconcile
// for Add/Update/Delete events for a single cluster.
func TestReconcileMopActions(t *testing.T) {
	clusterName := "clusterName1"
	testNamespace := "test-ns"
	svcFilterName := "test-mop-abc123-0"
	nsFilterName := "test-mop-0"

	testOverlay := v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: retriesOverlayBytes,
		}}

	testError := fmt.Errorf("test error")
	testMop := kube_test.NewMopBuilder("test-ns", "test-mop").AddFilter(testFaultFilter).Build()
	testOverlayMop := kube_test.NewMopBuilder("test-ns", "test-mop").AddOverlay(testOverlay).Build()
	testMopScheduledForDeletion := kube_test.NewMopBuilderFrom(testMop).
		SetFinalizers("mesh.io/mesh-operator").
		SetDeletionTimestamp().Build()

	configNsMop := kube_test.NewMopBuilder("test-ns", "test-mop").AddFilter(testConfigNsExtension).Build()
	configMopScheduledForDeletion := kube_test.NewMopBuilderFrom(configNsMop).
		SetFinalizers("mesh.io/mesh-operator").
		SetDeletionTimestamp().Build()

	emptyNamespaceResourceStatus := v1alpha1.MeshOperatorStatus{
		Phase: PhaseSucceeded,
		RelatedResources: []*v1alpha1.ResourceStatus{
			{
				Phase:      PhaseSucceeded,
				ApiVersion: "networking.istio.io/v1alpha3",
				Kind:       "EnvoyFilter",
				Name:       nsFilterName,
			},
		},
	}

	nonEmptyNamespaceResourceStatus := v1alpha1.MeshOperatorStatus{
		Phase: PhaseSucceeded,
		RelatedResources: []*v1alpha1.ResourceStatus{
			{
				Phase:      PhaseSucceeded,
				ApiVersion: "networking.istio.io/v1alpha3",
				Kind:       "EnvoyFilter",
				Name:       nsFilterName,
				Namespace:  "test-ns",
			},
		},
	}

	terminatingMopWithSvcFilterStatus := v1alpha1.MeshOperatorStatus{
		Phase: PhaseTerminating,
		Services: map[string]*v1alpha1.ServiceStatus{
			"svc1": {
				RelatedResources: []*v1alpha1.ResourceStatus{
					{
						ApiVersion: "networking.istio.io/v1alpha3",
						Kind:       "EnvoyFilter",
						Name:       svcFilterName,
					},
				},
			},
		},
	}

	terminatingConfigNsMopWithSvcFilterStatus := v1alpha1.MeshOperatorStatus{
		Phase: PhaseTerminating,
		Services: map[string]*v1alpha1.ServiceStatus{
			"svc1": {
				RelatedResources: []*v1alpha1.ResourceStatus{
					{
						ApiVersion: "networking.istio.io/v1alpha3",
						Kind:       "EnvoyFilter",
						Name:       svcFilterName,
						Namespace:  "config-namespace",
					},
				},
			},
		},
	}

	terminatingMopWithNsFilterStatus := v1alpha1.MeshOperatorStatus{
		Phase: PhaseTerminating,
		RelatedResources: []*v1alpha1.ResourceStatus{
			{
				ApiVersion: "networking.istio.io/v1alpha3",
				Kind:       "EnvoyFilter",
				Name:       nsFilterName,
			},
		},
	}

	svcFilter := kube_test.CreateEnvoyFilter(svcFilterName, testNamespace, "clusterName1/svc1")
	_ = unstructured.SetNestedField(svcFilter.Object, map[string]interface{}{}, "spec")
	_ = unstructured.SetNestedField(svcFilter.Object, map[string]interface{}{}, "status")
	_ = unstructured.SetNestedField(svcFilter.Object, nil, "metadata", "creationTimestamp")
	nsFilter := kube_test.CreateEnvoyFilter(nsFilterName, testNamespace, "clusterName1")
	_ = unstructured.SetNestedField(nsFilter.Object, map[string]interface{}{}, "spec")
	_ = unstructured.SetNestedField(nsFilter.Object, map[string]interface{}{}, "status")
	_ = unstructured.SetNestedField(nsFilter.Object, nil, "metadata", "creationTimestamp")
	configNsFilter := kube_test.CreateEnvoyFilter(svcFilterName, "config-namespace", "clusterName1/test-ns/svc1")
	_ = unstructured.SetNestedField(configNsFilter.Object, map[string]interface{}{}, "spec")
	_ = unstructured.SetNestedField(configNsFilter.Object, map[string]interface{}{}, "status")
	_ = unstructured.SetNestedField(configNsFilter.Object, nil, "metadata", "creationTimestamp")

	testCases := []struct {
		name                         string
		queueItem                    QueueItem
		mopsInCluster                []runtime.Object
		filtersInCluster             []runtime.Object
		servicesInCluster            []runtime.Object
		renderEmptyResult            bool
		mopClientError               error
		expectedMopClientActions     []coretesting.Action
		expectedDynamicClientActions []coretesting.Action
		expectedError                error
		expectedQueueItems           []QueueItem
		enablePendingTracking        bool
	}{
		{
			name:      "MopNotInCluster",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
		},
		{
			name:          "MopCreatedEvent",
			queueItem:     NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventAdd),
			mopsInCluster: []runtime.Object{testMop},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(testMop).SetFinalizers("mesh.io/mesh-operator").Build()),
			},
		},
		{
			name:          "OverlayMopCreatedEvent",
			queueItem:     NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventAdd),
			mopsInCluster: []runtime.Object{testOverlayMop},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(testOverlayMop).SetFinalizers("mesh.io/mesh-operator").Build()),
			},
		},
		{
			name:          "MopUpdatedToPendingEvent",
			queueItem:     NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster: []runtime.Object{testMop},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						SetPhase(PhasePending).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
		},
		{
			name:          "MopUpdatedToPendingGenerationSet",
			queueItem:     NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster: []runtime.Object{kube_test.NewMopBuilderFrom(testMop).SetGeneration(1).Build()},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						SetPhase(PhasePending).
						SetLastReconciledTime(nil).
						SetGeneration(1).
						SetObservedGeneration(1).Build()),
			},
		},
		{
			name:          "MopUpdatedFromPendingEvent",
			queueItem:     NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster: []runtime.Object{kube_test.NewMopBuilderFrom(testMop).SetPhase(PhasePending).Build()},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						SetPhase(PhaseSucceeded).
						AddRelatedResourceToStatus(PhaseSucceeded, "", nsFilterName, "test-ns").
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).
						Build()),
			},
		},
		{
			name:          "MopDeletedEvent",
			queueItem:     NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{testMop},
		},
		{
			name:          "MopDeletedEvent_NotInClusterAnymore",
			queueItem:     NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{},
		},
		{
			name:           "FinalizerAddError",
			queueItem:      NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventAdd),
			mopsInCluster:  []runtime.Object{testMop},
			mopClientError: testError,
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(testMop).SetFinalizers("mesh.io/mesh-operator").Build()),
			},
			expectedError: testError,
		},
		{
			name:           "StatusUpdateError_ExtensionReconcile",
			queueItem:      NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster:  []runtime.Object{kube_test.NewMopBuilderFrom(testMop).SetPhase(PhasePending).Build()},
			mopClientError: testError,
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						SetPhase(PhaseSucceeded).
						AddRelatedResourceToStatus(PhaseSucceeded, "", nsFilterName, "test-ns").
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
			expectedError: testError,
		},
		{
			name:      "DeleteFinalizerError",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).
					SetFinalizers("mesh.io/mesh-operator").
					SetStatus(terminatingMopWithSvcFilterStatus).Build(),
			},
			filtersInCluster: []runtime.Object{kube_test.UnstructuredToFilterObject(svcFilter)},
			mopClientError:   testError,
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).
						SetStatus(terminatingMopWithSvcFilterStatus).
						SetFinalizers().Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, "test-ns", svcFilterName),
			},
			expectedError: testError,
		},
		{
			name:          "SoftDeletedMopPutToTerminatingPhase",
			queueItem:     NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster: []runtime.Object{testMopScheduledForDeletion},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).
						SetPhase(PhaseTerminating).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
		},
		{
			name:      "MopInTerminatingPhaseScheduledForDeletion",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).SetPhase(PhaseTerminating).Build(),
			},
			expectedMopClientActions: []coretesting.Action{},
			expectedQueueItems: []QueueItem{
				NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			},
		},
		{
			name:      "TerminatingMopDeleted",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).SetStatus(terminatingMopWithSvcFilterStatus).Build(),
			},
			filtersInCluster: []runtime.Object{kube_test.UnstructuredToFilterObject(svcFilter)},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).
						SetStatus(terminatingMopWithSvcFilterStatus).
						SetFinalizers().Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, "test-ns", svcFilterName),
			},
		},
		{
			name:      "Config NS - TerminatingMopDeleted",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(configMopScheduledForDeletion).SetStatus(terminatingConfigNsMopWithSvcFilterStatus).Build(),
			},
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(filterWithSource(configNsFilter, "clusterName1/test-ns/svc1")),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(configMopScheduledForDeletion).SetStatus(terminatingConfigNsMopWithSvcFilterStatus).SetFinalizers().Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, "config-namespace", svcFilterName),
			},
		},
		{
			name:      "TerminatingMopDeleted_NoSelector",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).SetStatus(terminatingMopWithNsFilterStatus).Build(),
			},
			filtersInCluster: []runtime.Object{kube_test.UnstructuredToFilterObject(nsFilter)},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).SetStatus(terminatingMopWithNsFilterStatus).SetFinalizers().Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, "test-ns", nsFilterName),
			},
		},
		{
			name:      "TerminatingMopRemovedFromFilterSource",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).SetStatus(terminatingMopWithSvcFilterStatus).Build(),
			},
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(filterWithSource(svcFilter, "clusterName1/svc1,remote1/svc1")),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).
						SetStatus(terminatingMopWithSvcFilterStatus).
						SetFinalizers().Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svcFilter, "remote1/svc1")),
			},
		},
		{
			name:      "Config NS - Wrong MOP in different ns (same cluster) trying to delete resource",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(configMopScheduledForDeletion).SetStatus(terminatingConfigNsMopWithSvcFilterStatus).Build(),
			},
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(filterWithSource(configNsFilter, "clusterName1/some-other-ns/svc1")),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(configMopScheduledForDeletion).SetStatus(terminatingConfigNsMopWithSvcFilterStatus).SetFinalizers().Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{},
		},
		{
			name:      "Config NS - Wrong MOP in different cluster (same ns) trying to delete resource",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(configMopScheduledForDeletion).SetStatus(terminatingConfigNsMopWithSvcFilterStatus).Build(),
			},
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(filterWithSource(configNsFilter, "some-other-cluster/test-ns/svc1")),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(configMopScheduledForDeletion).SetStatus(terminatingConfigNsMopWithSvcFilterStatus).SetFinalizers().Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{},
		},
		{
			name:      "Config NS - TerminatingMopRemovedFromFilterSource",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(configMopScheduledForDeletion).SetStatus(terminatingConfigNsMopWithSvcFilterStatus).Build(),
			},
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(filterWithSource(configNsFilter, "clusterName1/test-ns/svc1,clusterName1/other-test-ns/svc1,remote1/test-ns/svc1")),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(configMopScheduledForDeletion).SetStatus(terminatingConfigNsMopWithSvcFilterStatus).SetFinalizers().Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, "config-namespace", filterWithSource(configNsFilter, "clusterName1/other-test-ns/svc1,remote1/test-ns/svc1")),
			},
		},
		{
			name:      "TerminatingMopRemovedFromFilterSource_NoSelector",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventDelete),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).SetStatus(terminatingMopWithNsFilterStatus).Build(),
			},
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(filterWithSource(nsFilter, "clusterName1,remote1")),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).
						SetStatus(terminatingMopWithNsFilterStatus).
						SetFinalizers().Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(nsFilter, "remote1")),
			},
		},
		{
			name:      "SoftDeletedMopPassedToAdd",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventAdd),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).Build(),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).
						SetPhase(PhaseTerminating).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
		},
		{
			name:      "SoftDeletedMopPassedToUpdate",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).Build(),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMopScheduledForDeletion).
						SetPhase(PhaseTerminating).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
		},
		{
			name:      "NoSelectorOverlayMop",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testOverlayMop).Build(),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testOverlayMop).
						SetPhase(PhaseSucceeded).
						SetStatusMessage(noServicesForSelectorMessage).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
		},
		{
			name:      "Empty namespace owned reference",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testMop).SetStatus(emptyNamespaceResourceStatus).Build(),
			},
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(filterWithSource(nsFilter, "clusterName1")),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						SetStatus(nonEmptyNamespaceResourceStatus).
						SetPhase(PhaseSucceeded).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
		},
		{
			name:      "ExtensionMop - no services to apply to",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testMop).AddSelector("app", "my-app").SetPhase(PhasePending).Build(),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).AddSelector("app", "my-app").
						SetPhase(PhaseSucceeded).
						SetStatusMessage(noServicesForSelectorMessage).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
		},
		{
			name:      "OverlayMop - no services to apply to",
			queueItem: NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testOverlayMop).AddSelector("app", "my-app").Build(),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testOverlayMop).AddSelector("app", "my-app").
						SetPhase(PhaseSucceeded).
						SetStatusMessage(noServicesForSelectorMessage).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
		},
		{
			name:           "StatusUpdateError_OverlayReconcile",
			queueItem:      NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopClientError: testError,
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(testOverlayMop).AddSelector("app", "my-app").Build(),
			},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testOverlayMop).AddSelector("app", "my-app").
						SetPhase(PhaseSucceeded).
						SetStatusMessage(noServicesForSelectorMessage).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
			expectedError: testError,
		},
		{
			name:                  "[Pending tracking] Resources status set to pending",
			enablePendingTracking: true,
			queueItem:             NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster:         []runtime.Object{kube_test.NewMopBuilderFrom(testMop).SetPhase(PhasePending).Build()},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						SetPhase(PhasePending).
						AddRelatedResourceToStatus(PhasePending, "", nsFilterName, "test-ns").
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						SetPhase(PhaseSucceeded).
						AddRelatedResourceToStatus(PhaseSucceeded, "", nsFilterName, "test-ns").
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
		},
		{
			name:                  "[Pending tracking] Stale resource deleted",
			enablePendingTracking: true,
			queueItem:             NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(kube_test.CreateEnvoyFilter("stale-filter", "test-ns", "clusterName1")),
			},
			mopsInCluster: []runtime.Object{kube_test.NewMopBuilderFrom(testMop).SetPhase(PhasePending).
				AddRelatedResourceToStatus(PhaseSucceeded, "", "stale-filter", "test-ns").Build()},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						SetPhase(PhasePending).
						AddRelatedResourceToStatus(PhaseSucceeded, "", "stale-filter", "test-ns").
						AddRelatedResourceToStatus(PhasePending, "", nsFilterName, "test-ns").
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						SetPhase(PhaseSucceeded).
						AddRelatedResourceToStatus(PhaseSucceeded, "", nsFilterName, "test-ns").
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, "test-ns", "stale-filter"),
			},
		},
		{
			name:                  "[Pending tracking] Stale object resource deleted",
			enablePendingTracking: true,
			queueItem:             NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(kube_test.CreateEnvoyFilter("stale-filter", "test-ns", "clusterName1")),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-ns", "svc1", map[string]string{"app": "my-app"}),
			},
			mopsInCluster: []runtime.Object{kube_test.NewMopBuilderFrom(testMop).AddSelector("app", "my-app").SetPhase(PhasePending).
				SetServiceHash("svc1", "5478f454cb").
				AddRelatedResourceToService("svc1", "test-ns", "stale-filter", PhaseSucceeded).Build()},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).SetPhase(PhasePending).
						AddSelector("app", "my-app").
						SetPhaseService("svc1", PhasePending).
						SetServiceHash("svc1", "5478f454cb").
						AddRelatedResourceToService("svc1", "test-ns", "stale-filter", PhaseSucceeded).
						AddRelatedResourceToService("svc1", "test-ns", "test-mop-5478f454cb-0", PhasePending).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).
						Build()),
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).SetPhase(PhaseSucceeded).
						AddSelector("app", "my-app").
						SetPhaseService("svc1", PhaseSucceeded).
						SetServiceStatusMessage("svc1", "all resources generated successfully").
						SetServiceHash("svc1", "5478f454cb").
						AddRelatedResourceToService("svc1", "test-ns", "test-mop-5478f454cb-0", PhaseSucceeded).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, "test-ns", "stale-filter"),
			},
		},
		{
			name:                  "Extension MOP added in Pending state",
			enablePendingTracking: true,
			queueItem:             NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventAdd),
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(svcFilter),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-ns", "svc1", map[string]string{"app": "my-app"}),
			},
			mopsInCluster: []runtime.Object{kube_test.NewMopBuilderFrom(testMop).AddSelector("app", "my-app").SetPhase(PhasePending).
				SetFinalizers("mesh.io/mesh-operator").
				SetServiceHash("svc1", "5478f454cb").
				SetPhaseService("svc1", PhasePending).
				AddRelatedResourceToService("svc1", "test-ns", svcFilterName, PhasePending).Build()},
			expectedQueueItems: []QueueItem{
				NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			},
		},
		{
			name:                  "Extension MOP scheduled for deletion added in Pending state",
			enablePendingTracking: true,
			queueItem:             NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventAdd),
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(svcFilter),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-ns", "svc1", map[string]string{"app": "my-app"}),
			},
			mopsInCluster: []runtime.Object{
				kube_test.NewMopBuilderFrom(configMopScheduledForDeletion).
					AddSelector("app", "my-app").SetPhase(PhasePending).
					SetServiceHash("svc1", "5478f454cb").
					SetPhaseService("svc1", PhasePending).
					AddRelatedResourceToService("svc1", "test-ns", svcFilterName, PhasePending).
					SetFinalizers("mesh.io/mesh-operator").Build()},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(configMopScheduledForDeletion).
						AddSelector("app", "my-app").
						SetPhase(PhaseTerminating).
						SetServiceHash("svc1", "5478f454cb").
						SetPhaseService("svc1", PhasePending).
						AddRelatedResourceToService("svc1", "test-ns", svcFilterName, PhasePending).
						SetLastReconciledTime(nil).
						SetFinalizers("mesh.io/mesh-operator").Build()),
			},
			expectedQueueItems: []QueueItem{},
		},
		{
			name:                  "Extension MOP without finalizer added in Pending state",
			enablePendingTracking: true,
			queueItem:             NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventAdd),
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(svcFilter),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-ns", "svc1", map[string]string{"app": "my-app"}),
			},
			mopsInCluster: []runtime.Object{kube_test.NewMopBuilderFrom(testMop).AddSelector("app", "my-app").
				SetPhase(PhasePending).
				//SetFinalizers("mesh.io/mesh-operator").
				SetServiceHash("svc1", "5478f454cb").
				SetPhaseService("svc1", PhasePending).
				AddRelatedResourceToService("svc1", "test-ns", svcFilterName, PhasePending).Build()},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(v1alpha1.MopVersionResource, "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						AddSelector("app", "my-app").
						SetPhase(PhasePending).
						SetServiceHash("svc1", "5478f454cb").
						SetPhaseService("svc1", PhasePending).
						AddRelatedResourceToService("svc1", "test-ns", svcFilterName, PhasePending).
						SetFinalizers("mesh.io/mesh-operator").Build()),
			},
		},
		{
			name:                  "Overlay MOP added in Pending state",
			enablePendingTracking: true,
			queueItem:             NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventAdd),
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(svcFilter),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-ns", "svc1", map[string]string{"app": "my-app"}),
			},
			mopsInCluster: []runtime.Object{kube_test.NewMopBuilderFrom(testOverlayMop).AddSelector("app", "my-app").SetPhase(PhasePending).
				SetFinalizers("mesh.io/mesh-operator").
				SetPhaseService("svc1", PhasePending).Build()},
			expectedQueueItems: []QueueItem{
				NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			},
		},
		{
			name:                  "New extension MOP added (with finalizer)",
			enablePendingTracking: true,
			queueItem:             NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventAdd),
			filtersInCluster: []runtime.Object{
				kube_test.UnstructuredToFilterObject(svcFilter),
			},
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-ns", "svc1", map[string]string{"app": "my-app"}),
			},
			mopsInCluster: []runtime.Object{kube_test.NewMopBuilderFrom(testMop).AddSelector("app", "my-app").
				SetFinalizers("mesh.io/mesh-operator").Build()},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						AddSelector("app", "my-app").
						SetPhase(PhasePending).
						SetLastReconciledTime(nil).
						SetFinalizers("mesh.io/mesh-operator").Build()),
			},
			expectedQueueItems: []QueueItem{
				NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			},
		},
		{
			name:                  "[Pending tracking] No resources rendered",
			enablePendingTracking: true,
			renderEmptyResult:     true,
			queueItem:             NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate),
			mopsInCluster:         []runtime.Object{kube_test.NewMopBuilderFrom(testMop).SetPhase(PhasePending).Build()},
			expectedMopClientActions: []coretesting.Action{
				coretesting.NewUpdateSubresourceAction(v1alpha1.MopVersionResource, "status", "test-ns",
					kube_test.NewMopBuilderFrom(testMop).
						SetPhase(PhaseSucceeded).
						SetLastReconciledTime(nil).
						SetObservedGeneration(0).Build()),
			},
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.enablePendingTracking {
				defer func() {
					features.EnableMopPendingTracking = false
				}()
				features.EnableMopPendingTracking = true
			}
			logger := zaptest.NewLogger(t).Sugar()
			applicator := &templating_test.TestableApplicator{
				AppliedResults: []templating_test.GeneratedConfigMetadata{},
			}
			var renderer templating.TemplateRenderer

			if tc.renderEmptyResult {
				renderer = &templating_test.FixedResultRenderer{
					ResultToReturn: templating.GeneratedConfig{},
				}
			} else {
				renderer = &templating_test.TestRendererForMop{
					TemplateType: "testTemplate",
					FilterName:   "filter-0",
				}
			}

			recorder := &record.FakeRecorder{}
			registry := prometheus.NewRegistry()
			testableQueue := &TestableQueue{}

			client := kubetest.NewKubeClientBuilder().
				AddDynamicClientObjects(tc.filtersInCluster...).
				AddMopClientObjects(tc.mopsInCluster...).
				AddK8sObjects(tc.servicesInCluster...).
				Build()
			primaryClient := client

			controller := NewMeshOperatorController(
				logger,
				&controller_test.FakeNamespacesNamespaceFilter{},
				recorder,
				applicator,
				renderer,
				registry,
				clusterName,
				client,
				primaryClient,
				NewSingleQueueEnqueuer(createWorkQueue("MeshOperators")),
				&NoOpEnqueuer{},
				&NoOpEnqueuer{},
				&resources_test.FakeResourceManager{},
				false,
				true,
				&NoOpReconcileManager{},
				nil,
				&kube_test.FakeTimeProvider{}).(interface{}).(*meshOperatorController)
			controller.mopEnqueuer = NewSingleQueueEnqueuer(testableQueue)

			err := client.RunAndWait(stopCh, true, nil)
			if err != nil {
				t.Fatalf("unexpected error when running client: %s", err.Error())
			}

			fakeClient := (client).(*kubetest.FakeClient)
			primaryFakeClient := (primaryClient).(*kubetest.FakeClient)

			if tc.mopClientError != nil {
				fakeClient.MopClient.PrependReactor("*", v1alpha1.MopVersionResource.Resource,
					func(action coretesting.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, tc.mopClientError
					},
				)
			}

			err = controller.reconcile(tc.queueItem)
			logger.Info(fakeClient.MopClient.Actions())
			filteredMopClientActions := filterInformerActions(fakeClient.MopClient.Actions())
			assert.Equal(t, len(tc.expectedMopClientActions), len(filteredMopClientActions))
			// always iterate over expectedActions. In false positive cases when filteredActions is nil,
			// no assertion will ever take place and test will still pass
			for idx, action := range tc.expectedMopClientActions {
				checkAction(t, action, filteredMopClientActions[idx])
			}

			// validate K8s actions
			filteredDynamicClientActions := filterInformerActions(primaryFakeClient.DynamicClient.Actions())
			assert.Equal(t, len(tc.expectedDynamicClientActions), len(filteredDynamicClientActions))
			for idx, expectedAction := range tc.expectedDynamicClientActions {
				checkAction(t, expectedAction, filteredDynamicClientActions[idx])
			}

			// validate enqueued items
			assert.ElementsMatch(t, tc.expectedQueueItems, testableQueue.recordedItems)
			assert.ErrorIs(t, err, tc.expectedError)
		})
	}
}

func TestReconcileOnMopSelectorChange(t *testing.T) {
	clusterName := "clusterName1"

	testOverlay := v1alpha1.Overlay{
		Kind: "VirtualService",
		StrategicMergePatch: runtime.RawExtension{
			Raw: retriesOverlayBytes,
		}}

	mopWithMultipleServiceSelector := kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app", "istio-shipping").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).Build()
	mopWithSingleServiceSelector := kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app_instance", "istio-shipping1").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).SetPhaseService("svc2", PhaseSucceeded).Build()
	mopWithNoServiceSelector := kube_test.NewMopBuilder("test-namespace", "test-mop").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).SetPhaseService("svc2", PhaseSucceeded).Build()
	mopServiceSelectorUnchanged := kube_test.NewMopBuilder("test-namespace", "test-mop").AddSelector("app", "istio-shipping").AddOverlay(testOverlay).SetPhase(PhaseSucceeded).SetPhaseService("svc1", PhaseSucceeded).SetPhaseService("svc2", PhaseSucceeded).Build()

	mopWithNoServiceSelectorWithSes := kube_test.NewMopBuilder("test-namespace", "test-mop").
		AddOverlay(testOverlay).
		SetPhase(PhaseSucceeded).
		SetPhaseService("svc1", PhaseSucceeded).
		SetPhaseService("svc2", PhaseSucceeded).
		SetPhaseServiceEntries("se1", PhaseSucceeded).
		SetPhaseServiceEntries("se2", PhaseSucceeded).
		Build()

	testCases := []struct {
		name                           string
		queueItem                      QueueItem
		servicesInCluster              []runtime.Object
		serviceEntriesInCluster        []runtime.Object
		mopsInCluster                  []runtime.Object
		seOverlaysEnabled              bool
		expectedError                  error
		expectedEnqueuedServiceList    []string
		expectedEnqueuedServiceEntries []string
	}{
		{
			name:      "ServiceSelector changed (multiple object to single service selection)",
			queueItem: NewQueueItem(clusterName, "test-namespace/test-mop", "", controllers_api.EventUpdate),
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping1"}),
				kube_test.CreateServiceWithLabels("test-namespace", "svc2", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping2"}),
			},
			mopsInCluster:               []runtime.Object{mopWithSingleServiceSelector},
			expectedEnqueuedServiceList: []string{"test-namespace/svc1", "test-namespace/svc2"},
		},
		{
			name:      "ServiceSelector changed (multiple service to no service selection)",
			queueItem: NewQueueItem(clusterName, "test-namespace/test-mop", "", controllers_api.EventUpdate),
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping1"}),
				kube_test.CreateServiceWithLabels("test-namespace", "svc2", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping2"}),
			},
			mopsInCluster:               []runtime.Object{mopWithNoServiceSelector},
			expectedEnqueuedServiceList: []string{"test-namespace/svc1", "test-namespace/svc2"},
		},
		{
			name:      "ServiceSelector changed (single service to multiple service selection)",
			queueItem: NewQueueItem(clusterName, "test-namespace/test-mop", "", controllers_api.EventUpdate),
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping1"}),
				kube_test.CreateServiceWithLabels("test-namespace", "svc2", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping2"}),
			},
			mopsInCluster:               []runtime.Object{mopWithMultipleServiceSelector},
			expectedEnqueuedServiceList: []string{"test-namespace/svc1", "test-namespace/svc2"},
		},
		{
			name:      "ServiceSelector not changed",
			queueItem: NewQueueItem(clusterName, "test-namespace/test-mop", "", controllers_api.EventUpdate),
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping1"}),
				kube_test.CreateServiceWithLabels("test-namespace", "svc2", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping2"}),
			},
			mopsInCluster:               []runtime.Object{mopServiceSelectorUnchanged},
			expectedEnqueuedServiceList: []string{"test-namespace/svc1", "test-namespace/svc2"},
		},
		{
			name:              "Selector picks SE and SVC",
			seOverlaysEnabled: true,
			queueItem:         NewQueueItem(clusterName, "test-namespace/test-mop", "", controllers_api.EventUpdate),
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping1"}),
			},
			serviceEntriesInCluster: []runtime.Object{
				kube_test.CreateServiceEntryWithLabels("test-namespace", "se1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping-se1"}),
			},
			mopsInCluster:                  []runtime.Object{mopServiceSelectorUnchanged},
			expectedEnqueuedServiceList:    []string{"test-namespace/svc1"},
			expectedEnqueuedServiceEntries: []string{"test-namespace/se1"},
		},
		{
			name:              "ServiceSelector changed (multiple targets to no selection)",
			seOverlaysEnabled: true,
			queueItem:         NewQueueItem(clusterName, "test-namespace/test-mop", "", controllers_api.EventUpdate),
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping1"}),
				kube_test.CreateServiceWithLabels("test-namespace", "svc2", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping2"}),
			},
			serviceEntriesInCluster: []runtime.Object{
				kube_test.CreateServiceEntryWithLabels("test-namespace", "se1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping-se1"}),
				kube_test.CreateServiceEntryWithLabels("test-namespace", "se2", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping-se2"}),
			},
			mopsInCluster:                  []runtime.Object{mopWithNoServiceSelectorWithSes},
			expectedEnqueuedServiceList:    []string{"test-namespace/svc1", "test-namespace/svc2"},
			expectedEnqueuedServiceEntries: []string{"test-namespace/se1", "test-namespace/se2"},
		},
		{
			name:      "No SE enqueued if SE-overlays disabled",
			queueItem: NewQueueItem(clusterName, "test-namespace/test-mop", "", controllers_api.EventUpdate),
			servicesInCluster: []runtime.Object{
				kube_test.CreateServiceWithLabels("test-namespace", "svc1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping1"}),
			},
			serviceEntriesInCluster: []runtime.Object{
				kube_test.CreateServiceEntryWithLabels("test-namespace", "se1", map[string]string{"app": "istio-shipping", "app_instance": "istio-shipping-se1"}),
			},
			mopsInCluster:               []runtime.Object{mopServiceSelectorUnchanged},
			expectedEnqueuedServiceList: []string{"test-namespace/svc1"},
		},
	}
	stopCh := make(chan struct{})
	defer close(stopCh)

	defer func() {
		features.EnableServiceEntryOverlays = false
	}()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			features.EnableServiceEntryOverlays = tc.seOverlaysEnabled

			logger := zaptest.NewLogger(t).Sugar()
			applicator := &templating_test.TestableApplicator{
				AppliedResults: []templating_test.GeneratedConfigMetadata{},
			}
			renderer := &templating_test.TestRendererForMop{
				TemplateType: "testTemplate",
				FilterName:   "filterName",
			}
			recorder := &record.FakeRecorder{}
			registry := prometheus.NewRegistry()

			client := kubetest.NewKubeClientBuilder().
				AddK8sObjects(tc.servicesInCluster...).
				AddIstioObjects(tc.serviceEntriesInCluster...).
				AddMopClientObjects(tc.mopsInCluster...).
				Build()
			primaryClient := client

			serviceQueue := &TestableQueue{}
			seQueue := &TestableQueue{}
			mopQueue := createWorkQueue("MeshOperators")
			controller := NewMeshOperatorController(
				logger,
				&controller_test.FakeNamespacesNamespaceFilter{},
				recorder,
				applicator,
				renderer,
				registry,
				clusterName,
				client,
				primaryClient,
				NewSingleQueueEnqueuer(mopQueue),
				NewSingleQueueEnqueuer(serviceQueue),
				NewSingleQueueEnqueuer(seQueue),
				&resources_test.FakeResourceManager{},
				false,
				true,
				&NoOpReconcileManager{},
				nil,
				&kube_test.FakeTimeProvider{}).(interface{}).(*meshOperatorController)

			err := client.RunAndWait(stopCh, true, nil)
			if err != nil {
				t.Fatalf("unexpected error when running client: %s", err.Error())
			}

			err = controller.reconcile(tc.queueItem)

			// validate enqueued items
			assert.ElementsMatch(t, tc.expectedEnqueuedServiceList, getObjectNames(t, serviceQueue.recordedItems))
			assert.ElementsMatch(t, tc.expectedEnqueuedServiceEntries, getObjectNames(t, seQueue.recordedItems))
			assert.ErrorIs(t, err, tc.expectedError)
		})
	}
}

var (
	svc1     = kube_test.CreateServiceWithLabels(namespace, "svc1", map[string]string{"app": "test", "object": "svc1"})
	svc1Hash = "5478f454cb"
	svc2     = kube_test.CreateServiceWithLabels(namespace, "svc2", map[string]string{"app": "test", "object": "svc2"})
	svc2Hash = "54d9677f77"

	svc1filter0, svc1filter1, svc2filter0, svc2filter1, filter0 = createEnvoyFilters(svc1Hash, svc2Hash)

	mopSelectingSvc1WithSingleFilter = kube_test.
						NewMopBuilder(namespace, "mop1").
						AddSelector("object", "svc1").
						AddFilter(testFaultFilter).
						Build()
	mopSelectingSvc1AndSvc2SingleFilter = kube_test.
						NewMopBuilder(namespace, "mop1").
						AddSelector("app", "test").
						AddFilter(testFaultFilter).
						Build()
	mopSelectingNoServices = kube_test.
				NewMopBuilder(namespace, "mop1").
				AddSelector("app", "doesnt-exist").
				Build()

	filterElem = v1alpha1.ExtensionElement{
		FaultFilter: &v1alpha1.HttpFaultFilter{Abort: &v1alpha1.HttpFaultFilterAbort{
			HttpStatus: kube_test.DefaultAbortHttpStatus}},
	}
	mopSelectingSvc1AndSvc2MultipleFilters = kube_test.
						NewMopBuilder(namespace, "mop1").
						AddSelector("app", "test").
						AddFilter(filterElem).
						Build()

	mopWithNoSelectorSingleFilter = kube_test.NewMopBuilder(namespace, "mop1").AddFilter(testFaultFilter).Build()

	renderResultSingleNoWorkloadSelector = map[string][]*unstructured.Unstructured{
		"some_template": {
			{
				Object: map[string]interface{}{
					"apiVersion": "networking.istio.io/v1alpha3",
					"kind":       "EnvoyFilter",
					"metadata": map[string]interface{}{
						"namespace": filter0.GetNamespace(),
						"name":      filter0.GetName(),
					},
				},
			},
		},
	}

	nsError = fmt.Errorf("failed to create namespace")

	primaryClusterName = "clusterName1"
	remoteClusterName  = "remote1"

	remoteNs = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
)

func createEnvoyFilters(svc1Hash, svc2Hash string) (*unstructured.Unstructured, *unstructured.Unstructured, *unstructured.Unstructured,
	*unstructured.Unstructured, *unstructured.Unstructured) {
	svc1filter0 := kube_test.CreateEnvoyFilter("mop1-"+svc1Hash+"-0", namespace, "clusterName1/svc1")
	svc1filter0.SetLabels(map[string]string{"mesh.io/managed-by": "mesh-operator"})
	svc1filter1 := kube_test.CreateEnvoyFilter("mop1-"+svc1Hash+"-1", namespace, "clusterName1/svc1")
	svc1filter1.SetLabels(map[string]string{"mesh.io/managed-by": "mesh-operator"})
	svc2filter0 := kube_test.CreateEnvoyFilter("mop1-"+svc2Hash+"-0", namespace, "clusterName1/svc2")
	svc2filter0.SetLabels(map[string]string{"mesh.io/managed-by": "mesh-operator"})
	svc2filter1 := kube_test.CreateEnvoyFilter("mop1-"+svc2Hash+"-1", namespace, "clusterName1/svc2")
	svc2filter1.SetLabels(map[string]string{"mesh.io/managed-by": "mesh-operator"})

	filter0 := kube_test.CreateEnvoyFilter("mop1-0", namespace, "")
	filter0.SetLabels(map[string]string{"mesh.io/managed-by": "mesh-operator"})

	return svc1filter0, svc1filter1, svc2filter0, svc2filter1, filter0
}

type reconcileMeshConfigTestCase struct {
	name                         string
	mopsInCluster                []runtime.Object
	svcsInCluster                []runtime.Object
	filtersInCluster             map[string][]*unstructured.Unstructured
	renderResult                 map[string][]*unstructured.Unstructured
	mopToReconcile               *v1alpha1.MeshOperator
	expectedDynamicClientActions []coretesting.Action
	expectedKubeClientActions    []coretesting.Action
	dryRun                       bool
	failNsCreation               bool
	expectedError                error
}

// TestReconcileMeshConfigActionsInSingleCluster validates object create/update/delete operations run in
// meshOperatorController.reconcileMeshConfig during an Update event for a single-cluster setup.
func TestReconcileMeshConfigActionsInSingleCluster(t *testing.T) {

	configNsSvc1filter := kube_test.CreateEnvoyFilter("mop1-68dff8484f-0", constants.DefaultConfigNamespace, "")
	configNsSvc1filter.SetLabels(map[string]string{"mesh.io/managed-by": "mesh-operator"})

	configNsSvc1filterInCluster := configNsSvc1filter.DeepCopy()
	configNsSvc1filter.SetAnnotations(map[string]string{constants.ConfigNamespaceExtensionAnnotation: "true"})

	configNsSvc1BypassConflictFilter := kube_test.CreateEnvoyFilter("mop1-5458c9c9b4-0", constants.DefaultConfigNamespace, "")
	configNsSvc1BypassConflictFilter.SetLabels(map[string]string{"mesh.io/managed-by": "mesh-operator"})
	configNsSvc1BypassConflictFilterInCluster := configNsSvc1BypassConflictFilter.DeepCopy()
	configNsSvc1BypassConflictFilter.SetAnnotations(map[string]string{constants.ConfigNamespaceExtensionAnnotation: "true"})

	testCases := []reconcileMeshConfigTestCase{
		{
			name:           "PrimaryMop_FiltersCreatedForMopWithSingleFilterSelectingSingleService",
			svcsInCluster:  []runtime.Object{svc1, svc2},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource,
					namespace, filterWithSource(svc1filter0, "clusterName1/svc1")),
			},
		},
		{
			name:             "PrimaryMop_FiltersUpdatedForMopWithSingleFilterSelectingSingleService",
			svcsInCluster:    []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{"svc1": {svc1filter0}},
			renderResult:     getFilters(1),
			mopToReconcile:   mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "clusterName1/svc1")),
			},
		},
		{
			name:           "PrimaryMop_FiltersCreatedForMopWithSingleFilterSelectingMultipleServices",
			svcsInCluster:  []runtime.Object{svc1, svc2},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1AndSvc2SingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, svc1filter0),
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, svc2filter0),
			},
		},
		{
			name:           "PrimaryMop_FiltersCreatedForMopWithMultipleFiltersSelectingMultipleServices",
			svcsInCluster:  []runtime.Object{svc1, svc2},
			renderResult:   getFilters(2),
			mopToReconcile: mopSelectingSvc1AndSvc2MultipleFilters,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, svc1filter0),
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, svc1filter1),
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, svc2filter0),
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, svc2filter1),
			},
		},
		{
			name:          "PrimaryMop_FiltersDeletedWhenMopMovesFromMultipleFiltersToSingleFilter",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {svc1filter0, svc1filter1},
				"svc2": {svc2filter0, svc2filter1}},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1AndSvc2SingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, svc1filter0),
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, svc2filter0),
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc1filter1.GetName()),
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc2filter1.GetName()),
			},
		},
		{
			name:          "PrimaryMop_FiltersDeletedWhenMopMovesFromSelectingMultipleServicesToSingleService",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {svc1filter0, svc1filter1},
				"svc2": {svc2filter0, svc2filter1}},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, svc1filter0),
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc1filter1.GetName()),
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc2filter0.GetName()),
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc2filter1.GetName()),
			},
		},
		{
			name:           "PrimaryMop_FiltersCreatedForMopWithNoSelectorMultipleServicesInNamespace",
			svcsInCluster:  []runtime.Object{svc1, svc2},
			renderResult:   renderResultSingleNoWorkloadSelector,
			mopToReconcile: mopWithNoSelectorSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(filter0, "clusterName1")),
			},
		},
		{
			name:           "PrimaryMop_FiltersCreatedForMopWithNoSelectorZeroServicesInNamespace",
			svcsInCluster:  []runtime.Object{},
			renderResult:   renderResultSingleNoWorkloadSelector,
			mopToReconcile: mopWithNoSelectorSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(filter0, "clusterName1")),
			},
		},
		{
			name:          "PrimaryMop_FilterUpdatedWhenParallelMopAddedToPrimary",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "remote1/svc1")},
				"svc2": {filterWithSource(svc2filter0, "remote1/svc2")}},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1AndSvc2SingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1,clusterName1/svc1")),
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc2filter0, "remote1/svc2,clusterName1/svc2")),
			},
		},
		{
			name:          "PrimaryMop_FilterUpdatedWhenParallelMopMovesFromSelectingMultipleServicesToOneService",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "remote1/svc1,clusterName1/svc1")},
				"svc2": {filterWithSource(svc2filter0, "remote1/svc2,clusterName1/svc2")}},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				// Render
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1,clusterName1/svc1")),
				// Delete obsolete
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc2filter0, "remote1/svc2")),
			},
		},
		{
			name:          "PrimaryMop_FilterUpdatedWhenParallelMopDeleted",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "remote1/svc1,clusterName1/svc1")}},
			renderResult: map[string][]*unstructured.Unstructured{
				"some_template": {},
			},
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1")),
			},
		},
		{
			name:          "PrimaryMop_FilterUpdatedWhenParallelMopDeleted (clusters have similar prefix)",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "clusterName1-2/svc1,clusterName1/svc1")}},
			renderResult: map[string][]*unstructured.Unstructured{
				"some_template": {},
			},
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "clusterName1-2/svc1")),
			},
		},
		{
			name:          "PrimaryMop_FilterDeletedWhenZeroServicesSelected",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "clusterName1/svc1")},
			},
			renderResult: map[string][]*unstructured.Unstructured{
				"some_template": {},
			},
			mopToReconcile: mopSelectingNoServices,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc1filter0.GetName()),
			},
		},
		{
			name:          "PrimaryMop_FilterUpdatedWhenMopDoesntSelectAnyServices",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "clusterName1/svc1,remote1/svc1")},
			},
			renderResult: map[string][]*unstructured.Unstructured{
				"some_template": {},
			},
			mopToReconcile: mopSelectingNoServices,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1")),
			},
		},
		{
			name:             "Config NS - FiltersCreatedForMopWithSingleFilterSelectingSingleService",
			svcsInCluster:    []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{},
			renderResult:     getConfigNsFilter(),
			mopToReconcile:   mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, constants.DefaultConfigNamespace, filterWithSource(configNsSvc1filterInCluster, "clusterName1/test-namespace/svc1")),
			},
		},
		{
			name:             "Config NS - FiltersUpdatedForMopWithSingleFilterSelectingSingleService",
			svcsInCluster:    []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{"svc1": {filterWithSource(configNsSvc1filter, "remoteCluster/test-namespace/svc1")}},
			renderResult:     getConfigNsFilter(),
			mopToReconcile:   mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, constants.DefaultConfigNamespace, filterWithSource(configNsSvc1filterInCluster, "remoteCluster/test-namespace/svc1,clusterName1/test-namespace/svc1")),
			},
		},
		{
			name:             "Config NS - FiltersUpdatedForMop - two services in different ns",
			svcsInCluster:    []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{"svc1": {filterWithSource(configNsSvc1filter, "clusterName1/other-namespace/svc1")}},
			renderResult:     getConfigNsFilter(),
			mopToReconcile:   mopSelectingSvc1WithSingleFilter.DeepCopy(),
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, constants.DefaultConfigNamespace, filterWithSource(configNsSvc1BypassConflictFilterInCluster, "clusterName1/test-namespace/svc1")),
			},
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runReconcileMeshConfigTestCase(t, tc, stopCh, false)
		})
	}
}

func TestReconcileMeshConfigActionsMultiCluster(t *testing.T) {
	testCases := []reconcileMeshConfigTestCase{
		{
			name:           "RemoteMop_FiltersCreatedForMopWithNoSelectorZeroServicesInNamespace",
			svcsInCluster:  []runtime.Object{},
			renderResult:   getFilters(1),
			mopToReconcile: mopWithNoSelectorSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(filter0, "remote1")),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:           "RemoteMop_FiltersCreatedForMopWithNoSelectorMultipleServicesInNamespace",
			svcsInCluster:  []runtime.Object{svc1, svc2},
			renderResult:   renderResultSingleNoWorkloadSelector,
			mopToReconcile: mopWithNoSelectorSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(filter0, "remote1")),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:           "RemoteMop_FiltersCreatedForMopWithNoSelectorZeroServicesInNamespace",
			svcsInCluster:  []runtime.Object{},
			renderResult:   renderResultSingleNoWorkloadSelector,
			mopToReconcile: mopWithNoSelectorSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(filter0, "remote1")),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:           "RemoteMop_ErrorCreatingNamespaceInPrimaryCluster",
			svcsInCluster:  []runtime.Object{svc1, svc2},
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			failNsCreation: true,
			expectedError:  nsError,
		},
		{
			name:           "RemoteMop_FiltersCreatedForMopWithSingleFilterSelectingSingleService",
			svcsInCluster:  []runtime.Object{svc1, svc2},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1")),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:          "RemoteMop_FiltersUpdatedForMopWithSingleFilterSelectingSingleService",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "remote1/svc1")}},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1")),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:           "RemoteMop_FiltersCreatedForMopWithSingleFilterSelectingMultipleServices",
			svcsInCluster:  []runtime.Object{svc1, svc2},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1AndSvc2SingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1")),
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc2filter0, "remote1/svc2")),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:           "RemoteMop_FiltersCreatedForMopWithMultipleFiltersSelectingMultipleServices",
			svcsInCluster:  []runtime.Object{svc1, svc2},
			renderResult:   getFilters(2),
			mopToReconcile: mopSelectingSvc1AndSvc2MultipleFilters,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1")),
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter1, "remote1/svc1")),
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc2filter0, "remote1/svc2")),
				coretesting.NewCreateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc2filter1, "remote1/svc2")),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:          "RemoteMop_FiltersDeletedWhenMopMovesFromMultipleFiltersToSingleFilter",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "remote1/svc1"), filterWithSource(svc1filter1, "remote1/svc1")},
				"svc2": {filterWithSource(svc2filter0, "remote1/svc2"), filterWithSource(svc2filter1, "remote1/svc2")}},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1AndSvc2SingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1")),
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc2filter0, "remote1/svc2")),
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc1filter1.GetName()),
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc2filter1.GetName()),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:          "RemoteMop_FiltersDeletedWhenMopMovesFromSelectingMultipleServicesToSingleService",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "remote1/svc1"), filterWithSource(svc1filter1, "remote1/svc1")},
				"svc2": {filterWithSource(svc2filter0, "remote1/svc2"), filterWithSource(svc2filter1, "remote1/svc2")}},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1")),
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc1filter1.GetName()),
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc2filter0.GetName()),
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc2filter1.GetName()),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:          "RemoteMop_FilterUpdatedWhenParallelMopAddedToRemote",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "clusterName1/svc1")},
				"svc2": {filterWithSource(svc2filter0, "clusterName1/svc2")}},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1AndSvc2SingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "clusterName1/svc1,remote1/svc1")),
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc2filter0, "clusterName1/svc2,remote1/svc2")),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:          "RemoteMop_FilterUpdatedWhenParallelMopMovesFromSelectingMultipleServicesToOneService",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "remote1/svc1,clusterName1/svc1")},
				"svc2": {filterWithSource(svc2filter0, "remote1/svc2,clusterName1/svc2")}},
			renderResult:   getFilters(1),
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "remote1/svc1,clusterName1/svc1")),
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc2filter0, "clusterName1/svc2")),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:          "RemoteMop_FilterUpdatedWhenParallelMopDeleted",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "remote1/svc1,clusterName1/svc1")}},
			renderResult: map[string][]*unstructured.Unstructured{
				"some_template": {},
			},
			mopToReconcile: mopSelectingSvc1WithSingleFilter,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "clusterName1/svc1")),
			},
			expectedKubeClientActions: []coretesting.Action{
				coretesting.NewCreateAction(constants.NamespaceResource, namespace, remoteNs),
			},
		},
		{
			name:          "RemoteMop_FilterDeletedWhenZeroServicesSelected",
			svcsInCluster: []runtime.Object{svc1, svc2},
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "remote1/svc1")},
			},
			renderResult: map[string][]*unstructured.Unstructured{
				"some_template": {},
			},
			mopToReconcile: mopSelectingNoServices,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewDeleteAction(constants.EnvoyFilterResource, namespace, svc1filter0.GetName()),
			},
		},
		{
			name: "RemoteMop_FilterUpdatedWhenMopDoesntSelectAnyServices",
			filtersInCluster: map[string][]*unstructured.Unstructured{
				"svc1": {filterWithSource(svc1filter0, "clusterName1/svc1,remote1/svc1")},
			},
			renderResult: map[string][]*unstructured.Unstructured{
				"some_template": {},
			},
			mopToReconcile: mopSelectingNoServices,
			expectedDynamicClientActions: []coretesting.Action{
				coretesting.NewUpdateAction(constants.EnvoyFilterResource, namespace, filterWithSource(svc1filter0, "clusterName1/svc1")),
			},
		},
	}
	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runReconcileMeshConfigTestCase(t, tc, stopCh, true)
		})
	}
}

func TestDeleteStaleResources_RemoteCluster(t *testing.T) {
	clusterName := "test-remote-cluster"
	resource := kube_test.CreateEnvoyFilter("mop-resource", "test-namespace", "")
	resource.SetLabels(map[string]string{"mesh.io/managed-by": "mesh-operator"})

	staleResources := []*v1alpha1.ResourceStatus{
		{
			ApiVersion: resource.GetAPIVersion(),
			Kind:       resource.GetKind(),
			Namespace:  resource.GetNamespace(),
			Name:       resource.GetName(),
		},
	}

	mop := kube_test.
		NewMopBuilder(namespace, "mop1").
		AddSelector("object", "svc1").
		AddFilter(testFaultFilter).
		Build()

	// Regular primary client
	primaryClient := kubetest.NewKubeClientBuilder().SetClusterName("primary").AddDynamicClientObjects(resource).Build()

	// Remote client. Initialized with empty discovery client, so it's not aware of envoy-filters
	remoteClient := (&kubetest.KubeClientBuilder{}).SetClusterName(clusterName).Build()

	// Controller under test
	controller := NewMeshOperatorController(
		zaptest.NewLogger(t).Sugar(),
		&controller_test.FakeNamespacesNamespaceFilter{},
		&record.FakeRecorder{},
		nil,
		nil,
		prometheus.NewRegistry(),
		clusterName,
		remoteClient,
		primaryClient,
		nil,
		&NoOpEnqueuer{},
		&NoOpEnqueuer{},
		&resources_test.FakeResourceManager{},
		false,
		false,
		&NoOpReconcileManager{},
		nil,
		&kube_test.FakeTimeProvider{}).(interface{}).(*meshOperatorController)

	// assert that record in primary is deleted
	controller.deleteStaleResources(mop, staleResources)

	primaryFakeClient := (primaryClient).(*kube_test.FakeClient)
	filteredDynamicClientActions := filterInformerActions(primaryFakeClient.DynamicClient.Actions())
	expectedDeleteAction := coretesting.NewDeleteAction(constants.EnvoyFilterResource, resource.GetNamespace(), resource.GetName())
	checkAction(t, expectedDeleteAction, filteredDynamicClientActions[0])
}

func runReconcileMeshConfigTestCase(t *testing.T, tc reconcileMeshConfigTestCase, stopCh chan struct{}, isRemote bool) {
	// Clone the MOP, so it's not changed
	mopToReconcile := tc.mopToReconcile.DeepCopy()

	// Update mop status with the existing filters in cluster.
	var existingFilters []runtime.Object
	for svcName, filters := range tc.filtersInCluster {
		for _, filter := range filters {
			existingFilters = append(existingFilters, filter)
			kube_test.AddPhaseRelatedResource(mopToReconcile, svcName, PhaseSucceeded, filter)
		}
	}

	logger := zaptest.NewLogger(t).Sugar()
	nsFilter := &controller_test.FakeNamespacesNamespaceFilter{}

	recorder := &record.FakeRecorder{}
	registry := prometheus.NewRegistry()

	var client, primaryClient kube.Client
	clientBuilder := kubetest.NewKubeClientBuilder().AddK8sObjects(tc.svcsInCluster...).AddMopClientObjects(mopToReconcile)

	// dynamicClient is used by renderer to upsert EnvoyFilter resources and create Namespace in primary if required.
	// istioClient is used to delete EnvoyFilter resources by the reconcile method.
	// For remote cluster events, existing filters should only be added to primary dynamicClient.
	var clusterName string
	if isRemote {
		clusterName = remoteClusterName
		client = clientBuilder.Build()
		primaryClient = clientBuilder.
			AddDynamicClientObjects(existingFilters...).
			Build()
	} else {
		clusterName = primaryClusterName
		client = clientBuilder.
			AddDynamicClientObjects(existingFilters...).
			Build()
		primaryClient = client
	}

	renderer := templating_test.FixedResultRenderer{
		ResultToReturn: templating.GeneratedConfig{
			Config:       tc.renderResult,
			TemplateType: "some_template",
		},
	}

	fakeClient := (client).(*kube_test.FakeClient)
	primaryFakeClient := (primaryClient).(*kube_test.FakeClient)

	if tc.failNsCreation {
		primaryFakeClient.KubeClient.PrependReactor("create",
			constants.NamespaceResource.Resource,
			func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
				return true, nil, nsError
			})
	}

	applicator := templating.NewK8sApplicator(logger, primaryClient)
	mopQueue := createWorkQueue("MeshOperators")
	controller := NewMeshOperatorController(
		logger,
		nsFilter,
		recorder,
		applicator,
		&renderer,
		registry,
		clusterName,
		client,
		primaryClient,
		NewSingleQueueEnqueuer(mopQueue),
		&NoOpEnqueuer{},
		&NoOpEnqueuer{},
		&resources_test.FakeResourceManager{},
		tc.dryRun,
		true,
		&NoOpReconcileManager{},
		nil,
		&kube_test.FakeTimeProvider{}).(interface{}).(*meshOperatorController)

	err := client.RunAndWait(stopCh, !isRemote, nil)
	if err != nil {
		t.Fatalf("unexpected error when running client: %s", err.Error())
	}

	err = controller.reconcileMeshConfig(mopToReconcile, logger)

	// Check expected EnvoyFilter create/update actions.
	// Namespace create action will always be the first one for remote cluster events.
	filteredDynamicClientActions := filterInformerActions(primaryFakeClient.DynamicClient.Actions())
	if isRemote {
		assert.Equal(t, 0, len(filterInformerActions(fakeClient.DynamicClient.Actions())), "no remote dynamicClient create/update/delete actions expected")
	}
	assert.Equal(t, len(tc.expectedDynamicClientActions), len(filteredDynamicClientActions)) // assert primary client dynamic actions
	for idx, expectedAction := range tc.expectedDynamicClientActions {
		checkAction(t, expectedAction, filteredDynamicClientActions[idx])
	}

	if tc.expectedKubeClientActions != nil {
		filteredKubeClientActions := filterInformerActions(primaryFakeClient.KubeClient.Actions())
		assert.Equal(t, len(tc.expectedKubeClientActions), len(filteredKubeClientActions))
		for idx, expectedAction := range tc.expectedKubeClientActions {
			checkAction(t, expectedAction, filteredKubeClientActions[idx])
		}
	}

	// Check expected EnvoyFilter delete actions.
	// Since stale resource identification is currently part of service hashmap (no order guarantees), the delete sequence is not guaranteed.
	if isRemote {
		assert.Equal(t, 0, len(fakeClient.IstioClient.Actions()), "no remote istioClient delete actions expected")
	}
	assert.ErrorIs(t, err, tc.expectedError)
}

func getFilters(number int) map[string][]*unstructured.Unstructured {
	config := make([]*unstructured.Unstructured, number)
	for i := 0; i < number; i++ {
		config[i] = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "networking.istio.io/v1alpha3",
				"kind":       "EnvoyFilter",
				"metadata": map[string]interface{}{
					"namespace": namespace,
					"name":      "filter-" + strconv.Itoa(i),
				},
			},
		}
	}
	return map[string][]*unstructured.Unstructured{
		"some_template": config,
	}
}

func getConfigNsFilter() map[string][]*unstructured.Unstructured {
	filter := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "EnvoyFilter",
			"metadata": map[string]interface{}{
				"namespace": namespace,
				"name":      "filter-0",
			},
		},
	}
	filter.SetAnnotations(map[string]string{
		constants.ConfigNamespaceExtensionAnnotation: constants.DefaultConfigNamespace,
	})
	return map[string][]*unstructured.Unstructured{
		"some_template": {
			filter,
		},
	}
}

// filterInformerActions filters get, list and watch actions for testing resources.
// Since get, list and watch don't change resource state we can filter it to lower noise level in our tests.
func filterInformerActions(actions []coretesting.Action) []coretesting.Action {
	resourcesToFilter := []string{"meshoperators", "envoyfilters", "namespaces", "rollouts"}
	verbListToFilter := []string{"get", "list", "watch"}
	var ret []coretesting.Action
	for _, action := range actions {
		matchFound := false
		for _, resource := range resourcesToFilter {
			for _, verb := range verbListToFilter {
				if action.Matches(verb, resource) {
					matchFound = true
					break
				}
			}
			if matchFound {
				break
			}
		}
		if !matchFound {
			ret = append(ret, action)
		}
	}

	return ret
}

func checkAction(t *testing.T, expected, actual coretesting.Action) {
	if !(expected.Matches(actual.GetVerb(), actual.GetResource().Resource) && actual.GetSubresource() == expected.GetSubresource()) {
		t.Errorf("Expected\n\t%#v\ngot\n\t%#v", expected, actual)
		return
	}

	if reflect.TypeOf(actual) != reflect.TypeOf(expected) {
		t.Errorf("Action has wrong type. Expected: %t. Got: %t", expected, actual)
		return
	}

	switch a := actual.(type) {
	case coretesting.CreateActionImpl:
		e, _ := expected.(coretesting.CreateActionImpl)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintSideBySide(expObject, object))
		}
	case coretesting.UpdateActionImpl:
		e, _ := expected.(coretesting.UpdateActionImpl)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintSideBySide(expObject, object))
		}
	case coretesting.PatchActionImpl:
		e, _ := expected.(coretesting.PatchActionImpl)
		expPatch := e.GetPatch()
		patch := a.GetPatch()

		if !reflect.DeepEqual(expPatch, patch) {
			t.Errorf("Action %s %s has wrong patch\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintSideBySide(expPatch, patch))
		}
	case coretesting.DeleteActionImpl:
		e, _ := expected.(coretesting.DeleteActionImpl)
		if e.GetResource().Resource != a.GetResource().Resource || e.GetNamespace() != a.GetNamespace() || e.GetName() != a.GetName() {
			t.Errorf("Action %s %s has incorrect object %s/%s", a.GetVerb(), a.GetResource().Resource, a.GetNamespace(), a.GetName())
			t.Errorf("Expected object %s %s/%s", e.GetResource().Resource, e.GetNamespace(), e.GetName())
		}
	default:
		t.Errorf("Uncaptured Action %s %s, you should explicitly add a case to capture it",
			actual.GetVerb(), actual.GetResource().Resource)
	}
}

func filterWithSource(filter *unstructured.Unstructured, filterSource string) *unstructured.Unstructured {
	newFilter := filter.DeepCopy()
	annotations := newFilter.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	annotations[constants.ExtensionSourceAnnotation] = filterSource
	newFilter.SetAnnotations(annotations)
	return newFilter
}

func getObjectNames(t *testing.T, objects []interface{}) []string {
	var result []string
	for _, object := range objects {
		item, ok := object.(QueueItem)
		if !ok {
			assert.Fail(t, "failed to extract name from the enqueued object")
		}
		result = append(result, item.key)
	}
	return result
}
