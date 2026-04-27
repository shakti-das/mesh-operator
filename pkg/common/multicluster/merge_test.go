package multicluster

import (
	"testing"
	"time"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestMergeServices(t *testing.T) {
	dateInThePast := metav1.Time{Time: time.Now().Add(-100 * time.Hour)}
	dateInTheFuture := metav1.Time{Time: time.Now().Add(100 * time.Hour)}

	resourceVersion := "1234567"

	noConflictsOlderSvc := kube_test.NewServiceBuilder("svc1", "ns-1").
		SetLabels(map[string]string{
			"svc1-older-label": "svc1-older-label-value",
		}).SetAnnotations(map[string]string{
		"svc1-older-annotaton": "svc1-older-annotation-value",
	}).
		SetPorts([]corev1.ServicePort{
			{
				Port: 7443,
				Name: "svc1-older-port",
			},
		}).SetResourceVersion(resourceVersion).Build()
	noConflictsOlderSvc.CreationTimestamp = dateInThePast

	noConflictsNewerSvc := kube_test.NewServiceBuilder("svc1", "ns-1").
		SetLabels(map[string]string{
			"svc1-newer-label": "svc1-newer-label-value",
		}).SetAnnotations(map[string]string{
		"svc1-newer-annotaton": "svc1-newer-annotation-value",
	}).
		SetPorts([]corev1.ServicePort{
			{
				Port: 7442,
				Name: "svc1-newer-port",
			},
		}).Build()
	noConflictsNewerSvc.CreationTimestamp = dateInTheFuture

	nonConflictingMergedService := kube_test.NewServiceBuilder("svc1", "ns-1").
		SetLabels(map[string]string{
			"svc1-older-label": "svc1-older-label-value",
			"svc1-newer-label": "svc1-newer-label-value",
		}).SetAnnotations(map[string]string{
		"svc1-older-annotaton": "svc1-older-annotation-value",
		"svc1-newer-annotaton": "svc1-newer-annotation-value",
	}).SetPorts([]corev1.ServicePort{
		{
			Port: 7442,
			Name: "svc1-newer-port",
		},
		{
			Port: 7443,
			Name: "svc1-older-port",
		},
	}).SetResourceVersion(resourceVersion).Build()
	nonConflictingMergedService.CreationTimestamp = noConflictsOlderSvc.CreationTimestamp

	conflictingOlderService := noConflictsOlderSvc.DeepCopy()
	conflictingOlderService.Labels["conflicting-label"] = "older-service-conflicting-label"
	conflictingOlderService.Annotations["conflicting-annotation"] = "older-service-conflicting-annotation"
	conflictingOlderService.Spec.Ports = append(conflictingOlderService.Spec.Ports, corev1.ServicePort{
		Name:       "conflicting-older-port",
		Port:       9443,
		TargetPort: intstr.IntOrString{IntVal: 9443},
	})

	conflictingNewerService := noConflictsNewerSvc.DeepCopy()
	conflictingNewerService.Labels["conflicting-label"] = "newer-service-conflicting-label"
	conflictingNewerService.Annotations["conflicting-annotation"] = "newer-service-conflicting-annotation"
	conflictingNewerService.Spec.Ports = append(conflictingNewerService.Spec.Ports, corev1.ServicePort{
		Name:       "conflicting-newer-port",
		Port:       9443,
		TargetPort: intstr.IntOrString{IntVal: 443},
	})

	conflictingMergedService := kube_test.NewServiceBuilder("svc1", "ns-1").
		SetLabels(map[string]string{
			"svc1-older-label":  "svc1-older-label-value",
			"svc1-newer-label":  "svc1-newer-label-value",
			"conflicting-label": "older-service-conflicting-label",
		}).SetAnnotations(map[string]string{
		"svc1-older-annotaton":   "svc1-older-annotation-value",
		"svc1-newer-annotaton":   "svc1-newer-annotation-value",
		"conflicting-annotation": "older-service-conflicting-annotation",
	}).SetPorts([]corev1.ServicePort{
		{
			Port: 7442,
			Name: "svc1-newer-port",
		},
		{
			Port: 7443,
			Name: "svc1-older-port",
		},
		{
			Name:       "conflicting-older-port",
			Port:       9443,
			TargetPort: intstr.IntOrString{IntVal: 9443},
		},
	}).SetResourceVersion(resourceVersion).Build()
	conflictingMergedService.CreationTimestamp = conflictingOlderService.CreationTimestamp

	nonConflictingHeadlessOlderSvc := noConflictsOlderSvc.DeepCopy()
	nonConflictingHeadlessOlderSvc.Spec.ClusterIP = "None"

	nonConflictingHeadlessMergedSvc := nonConflictingHeadlessOlderSvc.DeepCopy()
	nonConflictingHeadlessMergedSvc.Labels["svc1-newer-label"] = "svc1-newer-label-value"
	nonConflictingHeadlessMergedSvc.Annotations["svc1-newer-annotaton"] = "svc1-newer-annotation-value"
	nonConflictingHeadlessMergedSvc.Spec.Ports = []corev1.ServicePort{
		{
			Port: 7442,
			Name: "svc1-newer-port",
		},
		{
			Port: 7443,
			Name: "svc1-older-port",
		},
	}
	nonConflictingHeadlessMergedSvc.SetResourceVersion(resourceVersion)

	testCases := []struct {
		name           string
		services       []*corev1.Service
		expectedResult *corev1.Service
	}{
		{
			name:           "Single service to merge",
			services:       []*corev1.Service{&noConflictsOlderSvc},
			expectedResult: &noConflictsOlderSvc,
		},
		{
			name:           "Conflicting: older service labels, annotations, ports win",
			services:       []*corev1.Service{conflictingNewerService, conflictingOlderService},
			expectedResult: &conflictingMergedService,
		},
		{
			name:           "Non-conflicting: labels, annotations, ports merged",
			services:       []*corev1.Service{&noConflictsNewerSvc, &noConflictsOlderSvc},
			expectedResult: &nonConflictingMergedService,
		},
		{
			name:           "Headlessness: Older service wins",
			services:       []*corev1.Service{nonConflictingHeadlessOlderSvc, &noConflictsNewerSvc},
			expectedResult: nonConflictingHeadlessMergedSvc,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualResult := MergeServices(tc.services, resourceVersion)

			assert.Equal(t, tc.expectedResult, actualResult)
		})
	}
}

func TestMergeMeshOperators(t *testing.T) {
	dateInThePast := metav1.Time{Time: time.Now().Add(-100 * time.Hour)}
	dateInTheFuture := metav1.Time{Time: time.Now().Add(100 * time.Hour)}

	nonOverlapingMop := kube_test.NewMopBuilder("test-ns", "non-overlapping-mop").Build()
	otherNonOverlappingMop := kube_test.NewMopBuilder("test-ns", "other-non-overlapping-mop").Build()

	overlappingMop := kube_test.NewMopBuilder("test-ns", "overlapping-mop").SetUID("older-mop").Build()
	overlappingMop.CreationTimestamp = dateInThePast

	otherOverlappingMop := kube_test.NewMopBuilder("test-ns", "overlapping-mop").SetUID("newer-mop").Build()
	otherOverlappingMop.CreationTimestamp = dateInTheFuture

	testCases := []struct {
		name           string
		mopsPerCluster map[string][]*v1alpha1.MeshOperator
		expectedResult []*v1alpha1.MeshOperator
	}{
		{
			name: "Same cluster MOPs only",
			mopsPerCluster: map[string][]*v1alpha1.MeshOperator{
				"cluster-1": {nonOverlapingMop, otherNonOverlappingMop},
			},
			expectedResult: []*v1alpha1.MeshOperator{nonOverlapingMop, otherNonOverlappingMop},
		},
		{
			name: "Non overlapping MOPs in clusters",
			mopsPerCluster: map[string][]*v1alpha1.MeshOperator{
				"cluster-1": {nonOverlapingMop},
				"cluster-2": {otherNonOverlappingMop},
			},
			expectedResult: []*v1alpha1.MeshOperator{nonOverlapingMop, otherNonOverlappingMop},
		},
		{
			name: "Overlapping MOPs in clusters",
			mopsPerCluster: map[string][]*v1alpha1.MeshOperator{
				"cluster-1": {otherOverlappingMop},
				"cluster-2": {overlappingMop},
			},
			expectedResult: []*v1alpha1.MeshOperator{overlappingMop},
		},
		{
			name: "Mix of non overlapping and overlapping MOPs in clusters",
			mopsPerCluster: map[string][]*v1alpha1.MeshOperator{
				"cluster-1": {nonOverlapingMop, overlappingMop},
				"cluster-2": {otherNonOverlappingMop, otherOverlappingMop},
			},
			expectedResult: []*v1alpha1.MeshOperator{nonOverlapingMop, otherNonOverlappingMop, overlappingMop},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualResult := MergeMeshOperators(tc.mopsPerCluster)

			assert.ElementsMatch(t, tc.expectedResult, actualResult)
		})
	}
}

func TestGetOldestService(t *testing.T) {
	dateInThePast := metav1.Time{Time: time.Now().Add(-100 * time.Hour)}
	dateInTheFuture := metav1.Time{Time: time.Now().Add(100 * time.Hour)}

	olderSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").SetUID("older-svc-uid").Build()
	olderSvc.CreationTimestamp = dateInThePast

	newerSvc := kube_test.NewServiceBuilder("test-svc", "test-ns").SetUID("newer-svc-uid").Build()
	newerSvc.CreationTimestamp = dateInTheFuture

	testCases := []struct {
		name            string
		services        map[string]*corev1.Service
		expectedCluster string
		expectedService *corev1.Service
	}{
		{
			name:            "no services",
			services:        map[string]*corev1.Service{},
			expectedCluster: "",
			expectedService: nil,
		},
		{
			name: "one service",
			services: map[string]*corev1.Service{
				"cluster-1": &olderSvc,
			},
			expectedCluster: "cluster-1",
			expectedService: &olderSvc,
		},
		{
			name: "two services",
			services: map[string]*corev1.Service{
				"cluster-1": &newerSvc,
				"cluster-2": &olderSvc,
			},
			expectedCluster: "cluster-2",
			expectedService: &olderSvc,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualCluster, actualService := GetOldestService(tc.services)

			assert.Equal(t, tc.expectedCluster, actualCluster)
			assert.Equal(t, tc.expectedService, actualService)
		})
	}
}
