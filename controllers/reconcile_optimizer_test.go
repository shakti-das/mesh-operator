package controllers

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"

	"go.uber.org/zap/zaptest"

	"github.com/istio-ecosystem/mesh-operator/pkg/reconcilemetadata"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestShouldServiceReconcileOnRestart(t *testing.T) {
	svcObjNamespace := "some-namespace"
	svcObjName := "some-service"

	serviceResourceVersion := "12345678"
	differentResourceVersion := "78"

	svcHashKey := "primary_Service_some-namespace_some-service"

	svcObj := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       svcObjNamespace,
			Name:            svcObjName,
			UID:             serviceUid,
			Annotations:     map[string]string{},
			ResourceVersion: serviceResourceVersion,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
	}

	templateHash := "somehash"
	someOtherTemplateHash := "some-otherhash"

	msmWithEmptyStatus := &v1alpha1.MeshServiceMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svcObjNamespace,
			Name:      svcObjName,
			UID:       "some-uid",
		},
	}

	msmWithDifferentTemplateHash := &v1alpha1.MeshServiceMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svcObjNamespace,
			Name:      svcObjName,
			UID:       "some-uid",
		},
		Status: v1alpha1.MeshServiceMetadataStatus{
			ReconciledTemplateHash: someOtherTemplateHash,
		},
	}

	msmWithSameTemplateHash := &v1alpha1.MeshServiceMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svcObjNamespace,
			Name:      svcObjName,
			UID:       "some-uid",
		},
		Status: v1alpha1.MeshServiceMetadataStatus{
			ReconciledTemplateHash: templateHash,
		},
	}

	msmWithDifferentObjectHash := &v1alpha1.MeshServiceMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svcObjNamespace,
			Name:      svcObjName,
			UID:       "some-uid",
		},
		Status: v1alpha1.MeshServiceMetadataStatus{
			ReconciledTemplateHash: templateHash,
			ReconciledObjectHash: map[string]string{
				svcHashKey: differentResourceVersion,
			},
		},
	}

	testMSM := &v1alpha1.MeshServiceMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svcObjNamespace,
			Name:      svcObjName,
			UID:       "some-uid",
		},
		Status: v1alpha1.MeshServiceMetadataStatus{
			ReconciledTemplateHash: templateHash,
			ReconciledObjectHash: map[string]string{
				svcHashKey: serviceResourceVersion,
			},
		},
	}

	testCases := []struct {
		name                         string
		msmsInCluster                []runtime.Object
		clusterName                  string
		enableSkipReconcileOnRestart bool
		expectedReconcile            bool
	}{
		{
			name:                         "EnableSkipReconcileOnRestart flag is disabled - Perform reconcile",
			msmsInCluster:                []runtime.Object{testMSM},
			clusterName:                  "primary",
			enableSkipReconcileOnRestart: false,
			expectedReconcile:            true,
		},
		{
			name:                         "MSM record not found",
			msmsInCluster:                []runtime.Object{},
			clusterName:                  "primary",
			enableSkipReconcileOnRestart: true,
			expectedReconcile:            true,
		},
		{
			name:                         "MSM record exist with empty status - Perform reconcile",
			msmsInCluster:                []runtime.Object{msmWithEmptyStatus},
			clusterName:                  "primary",
			enableSkipReconcileOnRestart: true,
			expectedReconcile:            true,
		},
		{
			name:                         "Same TemplateHash, ReconciledObjectHash field is nil - Perform reconcile",
			msmsInCluster:                []runtime.Object{msmWithSameTemplateHash},
			clusterName:                  "primary",
			enableSkipReconcileOnRestart: true,
			expectedReconcile:            true,
		},
		{
			name:                         "Different TemplateHash - Perform reconcile",
			msmsInCluster:                []runtime.Object{msmWithDifferentTemplateHash},
			clusterName:                  "primary",
			enableSkipReconcileOnRestart: true,
			expectedReconcile:            true,
		},
		{
			name:                         "Same TemplateHash, Different ObjectHash - Perform reconcile",
			msmsInCluster:                []runtime.Object{msmWithDifferentObjectHash},
			clusterName:                  "primary",
			enableSkipReconcileOnRestart: true,
			expectedReconcile:            true,
		},
		{
			name:                         "Striped Service, New Service created in different cluster -  Perform reconcile",
			msmsInCluster:                []runtime.Object{testMSM},
			clusterName:                  "remote",
			enableSkipReconcileOnRestart: true,
			expectedReconcile:            true,
		},
		{
			name:                         "Same TemplateHash, Same ObjectHash - Skip reconcile",
			msmsInCluster:                []runtime.Object{testMSM},
			clusterName:                  "primary",
			enableSkipReconcileOnRestart: true,
			expectedReconcile:            false,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hashManager := reconcilemetadata.NewReconcileHashManager()
	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.MopConfigTemplatesHash = templateHash
			features.EnableSkipReconcileOnRestart = tc.enableSkipReconcileOnRestart
			defer func() {
				features.MopConfigTemplatesHash = ""
				features.EnableSkipReconcileOnRestart = false
			}()
			client := kube_test.NewKubeClientBuilder().AddMopClientObjects(tc.msmsInCluster...).Build()

			msmInformer := client.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()
			msmInformer.Informer()
			client.MopInformerFactory().Start(ctx.Done())
			client.MopInformerFactory().WaitForCacheSync(ctx.Done())

			serviceReconcileOnRestartChecker := NewServiceReconcileOptimizer(msmInformer, hashManager)
			reconcile := serviceReconcileOnRestartChecker.ShouldReconcile(logger, svcObj, tc.clusterName, "Service")
			assert.Equal(t, tc.expectedReconcile, reconcile)

		})
	}
}

func TestShouldEnqueueServiceForRelatedObjects(t *testing.T) {
	svcObjNamespace := "some-namespace"
	svcObjName := "some-service"

	svcHashKey := "primary_Service_some-namespace_some-service"

	serviceResourceVersion := "12345678"

	svcObj := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       svcObjNamespace,
			Name:            svcObjName,
			UID:             serviceUid,
			Annotations:     map[string]string{},
			ResourceVersion: serviceResourceVersion,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
	}

	templateHash := "somehash"

	var stsGeneration int64 = 23
	var stsUuid = "a24fa48c-5939-4cbb-b2d8-2397359121b6"
	var differentStsGeneration int64 = 20

	testSts := kube_test.NewStatefulSetBuilder(svcObjNamespace, "sts1", svcObjName, 2).
		SetGeneration(stsGeneration).
		Build()
	testSts.SetUID("a24fa48c-5939-4cbb-b2d8-2397359121b6")

	stsHashKey := "primary_StatefulSet_some-namespace_sts1"

	testMSM := &v1alpha1.MeshServiceMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svcObjNamespace,
			Name:      svcObjName,
			UID:       "some-uid",
		},
		Status: v1alpha1.MeshServiceMetadataStatus{
			ReconciledTemplateHash: templateHash,
			ReconciledObjectHash: map[string]string{
				svcHashKey: serviceResourceVersion,
				stsHashKey: fmt.Sprintf("%s-%s", stsUuid, strconv.FormatInt(stsGeneration, 10)),
			},
		},
	}

	msmWithDiffStsHash := &v1alpha1.MeshServiceMetadata{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svcObjNamespace,
			Name:      svcObjName,
			UID:       "some-uid",
		},
		Status: v1alpha1.MeshServiceMetadataStatus{
			ReconciledTemplateHash: templateHash,
			ReconciledObjectHash: map[string]string{
				svcHashKey: serviceResourceVersion,
				stsHashKey: strconv.FormatInt(differentStsGeneration, 10),
			},
		},
	}

	testCases := []struct {
		name                            string
		msmsInCluster                   []runtime.Object
		object                          interface{}
		kind                            string
		clusterName                     string
		expectedReconcile               bool
		skipSvcEnqueueForRelatedObjects bool
		event                           controllers_api.Event
	}{
		{
			name:                            "SkipSvcEnqueueForRelatedObjects flag is disabled - Perform reconcile",
			msmsInCluster:                   []runtime.Object{testMSM},
			clusterName:                     "primary",
			skipSvcEnqueueForRelatedObjects: false,
			event:                           controllers_api.EventUpdate,
			expectedReconcile:               true,
		},
		{
			name:                            "Different Sts hash - Perform reconcile", // svc and template hash same.
			msmsInCluster:                   []runtime.Object{msmWithDiffStsHash},
			clusterName:                     "primary",
			object:                          testSts,
			kind:                            "StatefulSet",
			skipSvcEnqueueForRelatedObjects: true,
			event:                           controllers_api.EventUpdate,
			expectedReconcile:               true,
		},
		{
			name:                            "Same Sts hash - Skip reconcile", // svc and template hash same.
			msmsInCluster:                   []runtime.Object{testMSM},
			clusterName:                     "primary",
			object:                          testSts,
			kind:                            "StatefulSet",
			skipSvcEnqueueForRelatedObjects: true,
			event:                           controllers_api.EventUpdate,
			expectedReconcile:               false,
		},
		{
			name:                            "No MSM - must reconcile",
			msmsInCluster:                   []runtime.Object{},
			clusterName:                     "primary",
			object:                          testSts,
			kind:                            "StatefulSet",
			skipSvcEnqueueForRelatedObjects: true,
			event:                           controllers_api.EventUpdate,
			expectedReconcile:               true,
		},
		{
			name:                            "Reconcile on delete",
			msmsInCluster:                   []runtime.Object{testMSM},
			clusterName:                     "primary",
			object:                          testSts,
			kind:                            "StatefulSet",
			skipSvcEnqueueForRelatedObjects: true,
			event:                           controllers_api.EventDelete,
			expectedReconcile:               true,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hashManager := reconcilemetadata.NewReconcileHashManager()
	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.SkipSvcEnqueueForRelatedObjects = tc.skipSvcEnqueueForRelatedObjects
			defer func() {
				features.SkipSvcEnqueueForRelatedObjects = false
			}()
			client := kube_test.NewKubeClientBuilder().AddMopClientObjects(tc.msmsInCluster...).Build()

			msmInformer := client.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()
			msmInformer.Informer()
			client.MopInformerFactory().Start(ctx.Done())
			client.MopInformerFactory().WaitForCacheSync(ctx.Done())

			serviceReconcileOnRestartChecker := NewServiceReconcileOptimizer(msmInformer, hashManager)
			reconcile := serviceReconcileOnRestartChecker.ShouldEnqueueServiceForRelatedObjects(logger, tc.event, svcObj, tc.object, tc.clusterName, tc.kind)
			assert.Equal(t, tc.expectedReconcile, reconcile)

		})
	}
}
