package controllers

import (
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func TestOnAdd(t *testing.T) {
	clusterName := "test-cluster"
	queue := &TestableQueue{}
	mopEnqueuer := NewSingleQueueEnqueuer(queue)
	logger := zaptest.NewLogger(t).Sugar()
	eventHandler := NewMeshOperatorEventHandler(mopEnqueuer, clusterName, logger)

	mop := kube_test.NewMopBuilder("test-ns", "test-mop").Build()

	eventHandler.OnAdd(mop, false)

	expectedQueueItem := NewQueueItem(
		clusterName,
		"test-ns/test-mop",
		"",
		controllers_api.EventAdd)

	assert.Equal(t, 1, len(queue.recordedItems))
	assert.Equal(t, expectedQueueItem, queue.recordedItems[0])
}

func TestOnDelete(t *testing.T) {
	baseMop := kube_test.NewMopBuilder("test-ns", "test-mop").Build()
	expectedQueueKey := "test-ns/test-mop"

	clusterName := "test-cluster"
	testCases := []struct {
		name              string
		mop               *v1alpha1.MeshOperator
		expectedQueueItem QueueItem
	}{
		{
			name:              "MOP in Terminating phase",
			mop:               kube_test.NewMopBuilderFrom(baseMop).SetPhase(PhaseTerminating).Build(),
			expectedQueueItem: NewQueueItem(clusterName, expectedQueueKey, "", controllers_api.EventDelete),
		},
		{
			name:              "MOP in Pending phase",
			mop:               kube_test.NewMopBuilderFrom(baseMop).SetPhase(PhasePending).Build(),
			expectedQueueItem: NewQueueItem(clusterName, expectedQueueKey, "", controllers_api.EventUpdate),
		},
		{
			name:              "MOP in Succeeded phase",
			mop:               kube_test.NewMopBuilderFrom(baseMop).SetPhase(PhaseSucceeded).Build(),
			expectedQueueItem: NewQueueItem(clusterName, expectedQueueKey, "", controllers_api.EventUpdate),
		},
		{
			name:              "MOP in Failed phase",
			mop:               kube_test.NewMopBuilderFrom(baseMop).SetPhase(PhaseFailed).Build(),
			expectedQueueItem: NewQueueItem(clusterName, expectedQueueKey, "", controllers_api.EventUpdate),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue := &TestableQueue{}
			mopEnqueuer := NewSingleQueueEnqueuer(queue)
			logger := zaptest.NewLogger(t).Sugar()
			eventHandler := NewMeshOperatorEventHandler(mopEnqueuer, clusterName, logger)

			eventHandler.OnDelete(tc.mop)

			assert.Equal(t, 1, len(queue.recordedItems))
			assert.Equal(t, tc.expectedQueueItem, queue.recordedItems[0])
		})
	}
}

func TestOnUpdate(t *testing.T) {
	baseExtensionMop := kube_test.NewMopBuilder("test-ns", "test-mop").
		AddFilter(v1alpha1.ExtensionElement{}).Build()
	baseOverlayMop := kube_test.NewMopBuilder("test-ns", "test-mop").
		AddOverlay(v1alpha1.Overlay{
			Name: "test-vs-1",
			Kind: "VirtualService"}).
		Build()

	clusterName := "test-cluster"
	expectedQueueItem := NewQueueItem(clusterName, "test-ns/test-mop", "", controllers_api.EventUpdate)

	testCases := []struct {
		name                          string
		oldMop                        *v1alpha1.MeshOperator
		newMop                        *v1alpha1.MeshOperator
		enablePendingResourceTracking bool
		expectedQueueItem             *QueueItem
	}{
		{
			name:              "Extension MOP: brand new",
			oldMop:            kube_test.NewMopBuilderFrom(baseExtensionMop).SetResourceVersion("1").Build(),
			newMop:            kube_test.NewMopBuilderFrom(baseExtensionMop).SetResourceVersion("2").Build(),
			expectedQueueItem: &expectedQueueItem,
		},
		{
			name:              "Extension MOP: changed by user",
			oldMop:            kube_test.NewMopBuilderFrom(baseExtensionMop).SetPhase(PhaseSucceeded).SetResourceVersion("1").SetGeneration(1).Build(),
			newMop:            kube_test.NewMopBuilderFrom(baseExtensionMop).SetPhase(PhaseSucceeded).SetResourceVersion("2").SetGeneration(2).Build(),
			expectedQueueItem: &expectedQueueItem,
		},
		{
			name:   "Extension MOP: periodic update",
			oldMop: kube_test.NewMopBuilderFrom(baseExtensionMop).SetResourceVersion("1").SetPhase(PhaseSucceeded).Build(),
			newMop: kube_test.NewMopBuilderFrom(baseExtensionMop).SetResourceVersion("1").SetPhase(PhaseSucceeded).Build(),
		},
		{
			name:   "Extension MOP: status updated to Succeeded",
			oldMop: kube_test.NewMopBuilderFrom(baseExtensionMop).SetResourceVersion("1").SetPhase(PhasePending).Build(),
			newMop: kube_test.NewMopBuilderFrom(baseExtensionMop).SetResourceVersion("2").SetPhase(PhaseSucceeded).Build(),
		},
		{
			name:   "Extension MOP: status updated to Failed",
			oldMop: kube_test.NewMopBuilderFrom(baseExtensionMop).SetResourceVersion("1").SetPhase(PhasePending).Build(),
			newMop: kube_test.NewMopBuilderFrom(baseExtensionMop).SetResourceVersion("2").SetPhase(PhaseFailed).Build(),
		},
		{
			name:              "Extension MOP: status set to Terminating",
			oldMop:            kube_test.NewMopBuilderFrom(baseExtensionMop).SetResourceVersion("1").SetPhase(PhaseSucceeded).Build(),
			newMop:            kube_test.NewMopBuilderFrom(baseExtensionMop).SetResourceVersion("2").SetPhase(PhaseTerminating).Build(),
			expectedQueueItem: &expectedQueueItem,
		},

		{
			name:              "Overlay MOP: brand new",
			oldMop:            kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("1").Build(),
			newMop:            kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("2").Build(),
			expectedQueueItem: &expectedQueueItem,
		},
		{
			name: "Overlay MOP: changed by user",
			oldMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetPhase(PhaseSucceeded).
				SetResourceVersion("1").
				SetGeneration(1).Build(),
			newMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetPhase(PhaseSucceeded).
				SetResourceVersion("2").
				SetGeneration(2).Build(),
			expectedQueueItem: &expectedQueueItem,
		},
		{
			name:   "Overlay MOP: periodic update",
			oldMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("1").SetPhase(PhaseSucceeded).Build(),
			newMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("2").SetPhase(PhaseSucceeded).Build(),
		},
		{
			name:   "Overlay MOP: status set to Pending",
			oldMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("1").SetPhase(PhaseSucceeded).Build(),
			newMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("2").SetPhase(PhasePending).Build(),
		},
		{
			name:   "Overlay MOP: status updated while Pending",
			oldMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("1").SetPhase(PhasePending).Build(),
			newMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("2").SetPhase(PhasePending).SetPhaseService("some-service", PhaseSucceeded).Build(),
		},
		{
			name:   "Overlay MOP: status set to Succeeded",
			oldMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("1").SetPhase(PhasePending).Build(),
			newMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("2").SetPhase(PhaseSucceeded).Build(),
		},
		{
			name:   "Overlay MOP: status set to Failed",
			oldMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("1").SetPhase(PhasePending).Build(),
			newMop: kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("2").SetPhase(PhaseFailed).Build(),
		},
		{
			name:              "Overlay MOP: status set to Terminating",
			oldMop:            kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("1").SetPhase(PhaseSucceeded).Build(),
			newMop:            kube_test.NewMopBuilderFrom(baseOverlayMop).SetResourceVersion("2").SetPhase(PhaseTerminating).Build(),
			expectedQueueItem: &expectedQueueItem,
		},
		{
			name:                          "Pending resource tracking event skipped",
			oldMop:                        kube_test.NewMopBuilderFrom(baseExtensionMop).SetPhase(PhaseSucceeded).Build(),
			newMop:                        kube_test.NewMopBuilderFrom(baseExtensionMop).SetPhase(PhasePending).SetObjectRelatedResourcesPhasesForKind(constants.ServiceKind.Kind, "svc1", PhasePending).Build(),
			enablePendingResourceTracking: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				features.EnableMopPendingTracking = false
			}()
			features.EnableMopPendingTracking = tc.enablePendingResourceTracking

			queue := &TestableQueue{}
			mopEnqueuer := NewSingleQueueEnqueuer(queue)
			logger := zaptest.NewLogger(t).Sugar()
			eventHandler := NewMeshOperatorEventHandler(mopEnqueuer, clusterName, logger)

			eventHandler.OnUpdate(tc.oldMop, tc.newMop)

			if tc.expectedQueueItem != nil {
				assert.Equal(t, 1, len(queue.recordedItems))
				assert.Equal(t, *tc.expectedQueueItem, queue.recordedItems[0])
			} else {
				assert.Equal(t, 0, len(queue.recordedItems))
			}
		})
	}
}
