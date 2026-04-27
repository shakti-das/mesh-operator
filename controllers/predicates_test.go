package controllers

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWasChanged(t *testing.T) {
	mop1 := v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			ResourceVersion: "1",
		},
	}
	mop2 := v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			ResourceVersion: "2",
		},
	}

	assert.False(t, WasChanged(&mop1, &mop1))
	assert.True(t, WasChanged(&mop1, &mop2))
}

func TestWasChangedByUser(t *testing.T) {
	oldMop := &v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			Generation:      1,
			ResourceVersion: "1",
			Finalizers:      []string{"mesh-operator"},
			Labels:          map[string]string{"app": "istio-shipping"},
			Annotations:     map[string]string{"managed-by": "something"},
		},
		Status: v1alpha1.MeshOperatorStatus{
			Phase: PhaseSucceeded,
		},
	}

	statusUpdatedToPendingMop := oldMop.DeepCopy()
	statusUpdatedToPendingMop.Status.Phase = PhasePending

	finalizerUpdatedMop := oldMop.DeepCopy()
	finalizerUpdatedMop.Finalizers = []string{"other-finalizer"}

	resourceVersionUpdatedMop := oldMop.DeepCopy()
	resourceVersionUpdatedMop.ResourceVersion = "2"

	labelsUpdatedMop := oldMop.DeepCopy()
	labelsUpdatedMop.Labels = map[string]string{"app": "other-service"}

	annotationChangedMop := oldMop.DeepCopy()
	annotationChangedMop.Annotations = map[string]string{"other-annotation": "other-value"}

	generationChangedMop := oldMop.DeepCopy()
	generationChangedMop.Generation = 2

	testCases := []struct {
		name           string
		oldMop         *v1alpha1.MeshOperator
		newMop         *v1alpha1.MeshOperator
		expectedResult bool
	}{
		{
			name:           "Not changed (Status updated)",
			oldMop:         oldMop,
			newMop:         statusUpdatedToPendingMop,
			expectedResult: false,
		},
		{
			name:           "Not changed (Finalizer updated)",
			oldMop:         oldMop,
			newMop:         finalizerUpdatedMop,
			expectedResult: false,
		},
		{
			name:           "Not changed (Resource version updated)",
			oldMop:         oldMop,
			newMop:         resourceVersionUpdatedMop,
			expectedResult: false,
		},
		{
			name:           "Label changed",
			oldMop:         oldMop,
			newMop:         labelsUpdatedMop,
			expectedResult: true,
		},
		{
			name:           "Annotation changed",
			oldMop:         oldMop,
			newMop:         annotationChangedMop,
			expectedResult: true,
		},
		{
			name:           "Spec changed (generation)",
			oldMop:         oldMop,
			newMop:         generationChangedMop,
			expectedResult: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedResult, WasChangedByUser(tc.oldMop, tc.newMop))
		})
	}
}

func TestIsPendingResourceTracking(t *testing.T) {
	newMopTrackingPending := kube_test.NewMopBuilder("ns", "mop").
		SetPhase(PhasePending).
		SetObjectRelatedResourcesPhasesForKind("Service", "svc1", PhasePending).
		Build()

	newMopWithTargetSETrackingPending := kube_test.NewMopBuilder("ns", "mop").
		SetPhase(PhasePending).
		SetObjectRelatedResourcesPhasesForKind("ServiceEntry", "se1", PhasePending).
		Build()

	newMopWithSvcAndSETrackingPending := kube_test.NewMopBuilder("ns", "mop").
		SetPhase(PhasePending).
		SetObjectRelatedResourcesPhasesForKind("Service", "svc1", PhasePending).
		SetObjectRelatedResourcesPhasesForKind("ServiceEntry", "se1", PhasePending).
		Build()

	newNsMopTrackingPending := kube_test.NewMopBuilder("ns", "mop").
		SetPhase(PhasePending).
		AddRelatedResourceToStatus(PhasePending, "", "some-object", "some-ns").
		Build()

	testCases := []struct {
		name                          string
		enablePendingResourceTracking bool
		newMop                        *v1alpha1.MeshOperator
		expectedResult                bool
	}{
		{
			name:                          "MOP pending resources tracking not enabled",
			enablePendingResourceTracking: false,
			expectedResult:                false,
		},
		{
			name:                          "New MOP not in pending",
			enablePendingResourceTracking: true,
			newMop:                        kube_test.NewMopBuilder("ns", "mop").SetPhase(PhaseFailed).Build(),
			expectedResult:                false,
		},
		{
			name:                          "New MOP no resources in pending",
			enablePendingResourceTracking: true,
			newMop:                        kube_test.NewMopBuilder("ns", "mop").SetPhase(PhasePending).Build(),
			expectedResult:                false,
		},
		{
			name:                          "New MOP with NS level resource in pending",
			enablePendingResourceTracking: true,
			newMop:                        newNsMopTrackingPending,
			expectedResult:                true,
		},
		{
			name:                          "New MOP with SVC level resource in pending",
			enablePendingResourceTracking: true,
			newMop:                        newMopTrackingPending,
			expectedResult:                true,
		},
		{
			name:                          "New MOP with ServiceEntry level resource in pending",
			enablePendingResourceTracking: true,
			newMop:                        newMopWithTargetSETrackingPending,
			expectedResult:                true,
		},
		{
			name:                          "New MOP with Service and SE level related resource in pending",
			enablePendingResourceTracking: true,
			newMop:                        newMopWithSvcAndSETrackingPending,
			expectedResult:                true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				features.EnableMopPendingTracking = false
			}()
			features.EnableMopPendingTracking = tc.enablePendingResourceTracking

			actual := isPendingResourceTracking(tc.newMop)

			assert.Equal(t, tc.expectedResult, actual)
		})
	}
}

func TestRolloutTemplateLabelsChanged(t *testing.T) {
	rolloutNoLabels := kube_test.NewRolloutBuilder("rollout1", "test-ns").Build()
	otherRolloutNoLabels := kube_test.NewRolloutBuilder("rollout2", "test-ns").Build()

	rolloutWithLabels := kube_test.NewRolloutBuilder("rollout1", "test-ns").
		SetTemplateLabel(map[string]string{
			"label1": "v1",
			"label2": "v2",
		}).Build()
	rolloutWithSameLabels := rolloutWithLabels.DeepCopy()

	otherRolloutWithLabels := kube_test.NewRolloutBuilder("rollout1", "test-ns").
		SetTemplateLabel(map[string]string{
			"otherLabel1": "v1",
			"otherLabel2": "v2",
		}).Build()

	testCases := []struct {
		name           string
		oldObject      *unstructured.Unstructured
		newObject      *unstructured.Unstructured
		expectedResult bool
	}{
		{
			name:           "Old object - no labels, new object - has labels",
			oldObject:      rolloutNoLabels,
			newObject:      rolloutWithLabels,
			expectedResult: true,
		},
		{
			name:           "Old object - has labels, new object - no labels",
			oldObject:      rolloutWithLabels,
			newObject:      otherRolloutNoLabels,
			expectedResult: true,
		},
		{
			name:           "Old object - no labels, new object - no labels",
			oldObject:      rolloutNoLabels,
			newObject:      otherRolloutNoLabels,
			expectedResult: false,
		},
		{
			name:           "Objects with different labels",
			oldObject:      rolloutWithLabels,
			newObject:      otherRolloutWithLabels,
			expectedResult: true,
		},
		{
			name:           "Objects with the same labels",
			expectedResult: false,
			oldObject:      rolloutWithLabels,
			newObject:      rolloutWithSameLabels,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := rolloutTemplateLabelsChanged(tc.oldObject, tc.newObject)

			assert.Equal(t, tc.expectedResult, result)
		})
	}
}

func TestStsOrdinalsStartChanged(t *testing.T) {
	stsNoOrdinals := kube_test.NewStatefulSetBuilder("test-ns", "test-sts", "test-svc", 2).Build()
	stsNoOrdinals2 := kube_test.NewStatefulSetBuilder("test-ns", "test-sts2", "test-svc", 2).Build()

	stsWithOrdinalsStart0 := kube_test.NewStatefulSetBuilder("test-ns", "test-sts", "test-svc", 2).
		SetOrdinalsStart(0).Build()
	stsWithOrdinalsStart0Copy := stsWithOrdinalsStart0.DeepCopy()

	stsWithOrdinalsStart5 := kube_test.NewStatefulSetBuilder("test-ns", "test-sts", "test-svc", 2).
		SetOrdinalsStart(5).Build()

	stsWithOrdinalsStart7 := kube_test.NewStatefulSetBuilder("test-ns", "test-sts", "test-svc", 2).
		SetOrdinalsStart(7).Build()

	testCases := []struct {
		name           string
		oldSts         *appsv1.StatefulSet
		newSts         *appsv1.StatefulSet
		expectedResult bool
	}{
		{
			name:           "Both nil (default 0) - no change",
			oldSts:         stsNoOrdinals,
			newSts:         stsNoOrdinals2,
			expectedResult: false,
		},
		{
			name:           "Old nil (default 0), new has ordinals start 0 - no change",
			oldSts:         stsNoOrdinals,
			newSts:         stsWithOrdinalsStart0,
			expectedResult: false,
		},
		{
			name:           "Old nil (default 0), new has ordinals start 5 - change",
			oldSts:         stsNoOrdinals,
			newSts:         stsWithOrdinalsStart5,
			expectedResult: true,
		},
		{
			name:           "Old has ordinals start 0, new nil (default 0) - no change",
			oldSts:         stsWithOrdinalsStart0,
			newSts:         stsNoOrdinals,
			expectedResult: false,
		},
		{
			name:           "Old has ordinals start 5, new nil (default 0) - change",
			oldSts:         stsWithOrdinalsStart5,
			newSts:         stsNoOrdinals,
			expectedResult: true,
		},
		{
			name:           "Both have same ordinals start - no change",
			oldSts:         stsWithOrdinalsStart5,
			newSts:         stsWithOrdinalsStart5.DeepCopy(),
			expectedResult: false,
		},
		{
			name:           "Both have ordinals start 0 - no change",
			oldSts:         stsWithOrdinalsStart0,
			newSts:         stsWithOrdinalsStart0Copy,
			expectedResult: false,
		},
		{
			name:           "Ordinals start changed from 5 to 7 - change",
			oldSts:         stsWithOrdinalsStart5,
			newSts:         stsWithOrdinalsStart7,
			expectedResult: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := stsOrdinalsStartChanged(tc.oldSts, tc.newSts)

			assert.Equal(t, tc.expectedResult, result)
		})
	}
}
