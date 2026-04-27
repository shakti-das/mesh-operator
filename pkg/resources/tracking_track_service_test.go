package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/reconcilemetadata"

	"github.com/prometheus/client_golang/prometheus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/generated/clientset/versioned/fake"
	meshiov1alpha1 "github.com/istio-ecosystem/mesh-operator/pkg/generated/informers/externalversions/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

func TestTrackService(t *testing.T) {
	var clusterName = "cluster-1"
	var serviceName = "svc1"
	var uid types.UID = "abcd-123"
	var namespace = "test-namespace"
	var service = kube_test.CreateService(namespace, serviceName)
	service.UID = uid

	serviceRef := v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "v1",
		Kind:       "Service",
		Name:       serviceName,
		UID:        uid,
	}
	otherServiceRef := v1alpha1.Owner{
		Cluster:    "other-cluster",
		ApiVersion: "v1",
		Kind:       "Service",
		Name:       "svc2",
		UID:        "xyz-123",
	}

	testCases := []struct {
		name               string
		msmRecordInCluster runtime.Object
		getError           error
		createError        error
		updateError        error
		msmRecordExpected  *v1alpha1.MeshServiceMetadata
	}{
		{
			name: "MSM Record exists, service already tracked",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef, otherServiceRef},
				},
			},
			msmRecordExpected: &v1alpha1.MeshServiceMetadata{
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef, otherServiceRef},
				},
			},
		},
		{
			name: "MSM Record exists, service added to tracking",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{otherServiceRef},
				},
			},
			msmRecordExpected: &v1alpha1.MeshServiceMetadata{
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef, otherServiceRef},
				},
			},
		},
		{
			name: "MSM Record exists, error on update",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{otherServiceRef},
				},
			},
			updateError: fmt.Errorf("test error"),
		},
		{
			name: "New MSM record",
			msmRecordExpected: &v1alpha1.MeshServiceMetadata{
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef},
				},
			},
		},
		{
			name:        "Fail to add new record",
			createError: fmt.Errorf("test-error"),
		},
		{
			name:     "Error obtaining record",
			getError: fmt.Errorf("test-error"),
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			objectsInCluster := []runtime.Object{}
			if tc.msmRecordInCluster != nil {
				objectsInCluster = append(objectsInCluster, tc.msmRecordInCluster)
			}
			client := fake.NewSimpleClientset(objectsInCluster...)

			if tc.getError != nil {
				client.PrependReactor(
					"get",
					"meshservicemetadatas",
					func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, tc.getError
					})
			}
			if tc.createError != nil {
				client.PrependReactor(
					"create",
					"meshservicemetadatas",
					func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, tc.createError
					})
			}
			if tc.updateError != nil {
				client.PrependReactor(
					"update",
					"meshservicemetadatas",
					func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, tc.updateError
					})
			}

			registry := prometheus.NewRegistry()

			var msmInformer meshiov1alpha1.MeshServiceMetadataInformer

			manager := NewServiceResourceManager(logger, msmInformer, client, nil, nil, registry)
			ownerService := ServiceToReference(clusterName, service)
			msm, err := manager.TrackOwner(clusterName, namespace, ownerService, logger)

			if tc.getError != nil {
				assert.Nil(t, msm)
				assert.NotNil(t, err)
				assert.True(t, strings.Contains(err.Error(), "failed to obtain msm record"))
				return
			}
			if tc.createError != nil {
				assert.Nil(t, msm)
				assert.NotNil(t, err)
				assert.True(t, strings.Contains(err.Error(), "failed to create msm record"))
				return
			}
			if tc.updateError != nil {
				assert.Nil(t, msm)
				assert.NotNil(t, err)
				assert.True(t, strings.Contains(err.Error(), "failed to add owner to msm record"))
				return
			}

			assert.NotNil(t, msm)
			// Compare the record returned from method
			assert.ElementsMatchf(t, tc.msmRecordExpected.Spec.Owners, msm.Spec.Owners, "")
			// Compare with the record in cluster
			msmInCluster, err := client.MeshV1alpha1().MeshServiceMetadatas(namespace).Get(context.TODO(), serviceName, metav1.GetOptions{})
			assert.NoError(t, err)
			assert.Equal(t, msmInCluster, msm)
		})
	}
}

func TestTrackServiceEntry(t *testing.T) {
	var serviceEntry = kube_test.CreateServiceEntryWithLabels(namespace, "se1", map[string]string{})
	serviceEntry.UID = uid

	serviceEntryMsmName := "se1--se"

	seRef := v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "networking.istio.io/v1alpha3",
		Kind:       "ServiceEntry",
		Name:       "se1",
		UID:        uid,
	}

	wrongApiVersionSeRef := v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "v1alpha3",
		Kind:       "ServiceEntry",
		Name:       "se1",
		UID:        uid,
	}

	otherSeRef := v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "networking.istio.io/v1alpha3",
		Kind:       "ServiceEntry",
		Name:       "other-se1",
		UID:        "other-uid-123",
	}

	testCases := []struct {
		name               string
		msmRecordInCluster runtime.Object
		msmRecordExpected  *v1alpha1.MeshServiceMetadata
	}{
		{
			name: "New MSM record",
			msmRecordExpected: &v1alpha1.MeshServiceMetadata{
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{seRef},
				},
			},
		},
		{
			name: "MSM record exists, SE added to owners",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceEntryMsmName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{otherSeRef},
				},
			},
			msmRecordExpected: &v1alpha1.MeshServiceMetadata{
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{otherSeRef, seRef},
				},
			},
		},
		{
			name: "MSM record exists, Owner has wrong apiVersion",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceEntryMsmName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{wrongApiVersionSeRef},
				},
			},
			msmRecordExpected: &v1alpha1.MeshServiceMetadata{
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{seRef},
				},
			},
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			objectsInCluster := []runtime.Object{}
			if tc.msmRecordInCluster != nil {
				objectsInCluster = append(objectsInCluster, tc.msmRecordInCluster)
			}
			client := fake.NewSimpleClientset(objectsInCluster...)

			registry := prometheus.NewRegistry()
			var msmInformer meshiov1alpha1.MeshServiceMetadataInformer
			manager := NewServiceResourceManager(logger, msmInformer, client, nil, nil, registry)
			ownerSe := ServiceEntryToReference(clusterName, serviceEntry)
			msm, err := manager.TrackOwner(clusterName, namespace, ownerSe, logger)

			assert.NoError(t, err)
			assert.NotNil(t, msm)
			// Compare the record returned from method
			assert.ElementsMatchf(t, tc.msmRecordExpected.Spec.Owners, msm.Spec.Owners, "")
			// Compare with the record in cluster
			msmInCluster, err := client.MeshV1alpha1().MeshServiceMetadatas(namespace).Get(context.TODO(), serviceEntryMsmName, metav1.GetOptions{})
			assert.NoError(t, err)
			assert.Equal(t, msmInCluster, msm)
		})
	}
}

func TestGetOrCreateMSMRecord(t *testing.T) {

	testServiceOwner := v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "v1",
		Kind:       "Service",
		Name:       serviceName,
		UID:        uid,
	}

	testCases := []struct {
		name                           string
		msmsInCluster                  []runtime.Object
		clusterName                    string
		useInformerInTrackingComponent bool
		expectedMsmRecord              *v1alpha1.MeshServiceMetadata
		expectedError                  error
	}{
		{
			name:                           "MSM record don't exist in cluster - create new record",
			msmsInCluster:                  []runtime.Object{},
			clusterName:                    "primary",
			useInformerInTrackingComponent: true,
			expectedMsmRecord: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: namespace,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners:         []v1alpha1.Owner{},
					OwnedResources: []v1alpha1.OwnedResource{},
				},
			},
			expectedError: nil,
		},
		{
			name: "MSM record exist in cluster",
			msmsInCluster: []runtime.Object{&v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{testServiceOwner},
				},
			}},
			clusterName:                    "primary",
			useInformerInTrackingComponent: true,
			expectedMsmRecord: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: namespace,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{testServiceOwner},
				},
			},
			expectedError: nil,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.UseInformerInTrackingComponent = true
			defer func() {
				features.UseInformerInTrackingComponent = false
			}()
			client := kube_test.NewKubeClientBuilder().AddMopClientObjects(tc.msmsInCluster...).Build()
			fakeClient := (client).(*kube_test.FakeClient)

			msmInformer := client.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()
			msmInformer.Informer()
			client.MopInformerFactory().Start(ctx.Done())
			client.MopInformerFactory().WaitForCacheSync(ctx.Done())

			resourceMgr := &resourceManager{logger: logger, msmInformer: msmInformer, meshApiClient: fakeClient.MopClient, dynamicClient: fakeClient.DynamicClient, discoveryClient: fakeClient.DiscoveryClient, metricsRegistry: registry, reconcileHashManager: reconcilemetadata.NewReconcileHashManager()}
			ownerService := ServiceToReference(clusterName, service)

			msmRecord, err := resourceMgr.getOrCreateMsmRecord(tc.clusterName, namespace, ownerService)

			assert.Equal(t, tc.expectedMsmRecord, msmRecord)
			assert.Equal(t, tc.expectedError, err)

		})
	}
}

func TestGetMSMRecord(t *testing.T) {

	testServiceOwner := v1alpha1.Owner{
		Cluster:    clusterName,
		ApiVersion: "v1",
		Kind:       "Service",
		Name:       serviceName,
		UID:        uid,
	}

	testCases := []struct {
		name                           string
		msmsInCluster                  []runtime.Object
		useInformerInTrackingComponent bool
		expectedMsmRecord              *v1alpha1.MeshServiceMetadata
		expectedError                  error
	}{
		{
			name:                           "MSM record don't exist in cluster, useInformerInTrackingComponent flag enabled",
			useInformerInTrackingComponent: true,
			expectedError:                  errors.New("meshservicemetadata.mesh.io \"svc1\" not found"),
		},
		{
			name: "MSM record exist in cluster",
			msmsInCluster: []runtime.Object{&v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{testServiceOwner},
				},
			}},
			useInformerInTrackingComponent: true,
			expectedMsmRecord: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: namespace,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{testServiceOwner},
				},
			},
			expectedError: nil,
		},
		{
			name:                           "MSM record don't exist in cluster, useInformerInTrackingComponent flag disabled",
			useInformerInTrackingComponent: false,
			expectedError:                  errors.New("meshservicemetadatas.mesh.io \"svc1\" not found"),
		},
		{
			name: "MSM record exist in cluster, useInformerInTrackingComponent flag disabled",
			msmsInCluster: []runtime.Object{&v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{testServiceOwner},
				},
			}},
			useInformerInTrackingComponent: true,
			expectedMsmRecord: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: namespace,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{testServiceOwner},
				},
			},
			expectedError: nil,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.UseInformerInTrackingComponent = tc.useInformerInTrackingComponent
			defer func() {
				features.UseInformerInTrackingComponent = false
			}()
			client := kube_test.NewKubeClientBuilder().AddMopClientObjects(tc.msmsInCluster...).Build()
			fakeClient := (client).(*kube_test.FakeClient)

			var msmInformer meshiov1alpha1.MeshServiceMetadataInformer
			if tc.useInformerInTrackingComponent {
				msmInformer = client.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()
				msmInformer.Informer()
				client.MopInformerFactory().Start(ctx.Done())
				client.MopInformerFactory().WaitForCacheSync(ctx.Done())
			}

			resourceMgr := &resourceManager{logger: logger, msmInformer: msmInformer, meshApiClient: fakeClient.MopClient, dynamicClient: fakeClient.DynamicClient, discoveryClient: fakeClient.DiscoveryClient, metricsRegistry: registry, reconcileHashManager: reconcilemetadata.NewReconcileHashManager()}

			msmRecord, err := resourceMgr.getMSMRecord(namespace, serviceName)

			assert.Equal(t, tc.expectedMsmRecord, msmRecord)
			if tc.expectedError != nil {
				assert.EqualError(t, tc.expectedError, err.Error())
			} else {
				assert.Nil(t, err)
			}

		})
	}
}
