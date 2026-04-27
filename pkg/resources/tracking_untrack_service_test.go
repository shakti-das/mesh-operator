package resources

import (
	"context"
	"fmt"
	"strings"
	"testing"

	error2 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/errors"

	meshiov1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/generated/informers/externalversions/mesh.io/v1alpha1"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/transition"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	dto "github.com/prometheus/client_model/go"
	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	metricstesting "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics/testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

func TestUntrackService(t *testing.T) {
	var clusterName = "cluster-1"
	var serviceName = "svc1"
	var uid types.UID = "abcd-123"
	var namespace = "test-namespace"

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
		name                           string
		msmRecordInCluster             *v1alpha1.MeshServiceMetadata
		updateError                    error
		deleteError                    error
		isCriticalError                bool
		useInformerInTrackingComponent bool
		expectMsmDeleted               bool
		expectedServiceOwners          []v1alpha1.Owner
	}{
		{
			name: "Service remove from owners list",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef, otherServiceRef},
				},
			},
			expectedServiceOwners: []v1alpha1.Owner{otherServiceRef},
		},
		{
			name: "UseInformerInTrackingComponent flag Enabled, remove service from owners list",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef, otherServiceRef},
				},
			},
			useInformerInTrackingComponent: true,
			expectedServiceOwners:          []v1alpha1.Owner{otherServiceRef},
		},
		{
			name: "Last service on the owners list",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef},
				},
			},
			expectMsmDeleted: true,
		},
		{
			name:             "MSM record missing (no error expected)",
			expectMsmDeleted: true,
		},
		{
			name: "MSM update failure (critical)",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef, otherServiceRef},
				},
			},
			updateError:     fmt.Errorf("test-error"),
			isCriticalError: true,
		},
		{
			name: "MSM update failure (non critical)",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef, otherServiceRef},
				},
			},
			updateError:     k8serrors.NewApplyConflict(nil, "test-conflict error"),
			isCriticalError: false,
		},
		{
			name: "MSM delete failure (critical)",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef},
				},
			},
			deleteError:     fmt.Errorf("test-error"),
			isCriticalError: true,
		},
		{
			name: "MSM delete failure (non critical)",
			msmRecordInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					Owners: []v1alpha1.Owner{serviceRef},
				},
			},
			deleteError:     k8serrors.NewApplyConflict(nil, "test-conflict error"),
			isCriticalError: false,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			objectsInCluster := []runtime.Object{}
			if tc.msmRecordInCluster != nil {
				objectsInCluster = append(objectsInCluster, tc.msmRecordInCluster)
			}

			client := kube_test.NewKubeClientBuilder().AddMopClientObjects(objectsInCluster...).Build()
			fakeClient := (client).(*kube_test.FakeClient)

			var msmInformer meshiov1alpha1.MeshServiceMetadataInformer

			if tc.useInformerInTrackingComponent {
				features.UseInformerInTrackingComponent = true
				defer func() {
					features.UseInformerInTrackingComponent = false
				}()
				msmInformer = client.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()
				msmInformer.Informer()
				client.MopInformerFactory().Start(ctx.Done())
				client.MopInformerFactory().WaitForCacheSync(ctx.Done())
			}

			if tc.updateError != nil {
				fakeClient.MopClient.PrependReactor(
					"update",
					"meshservicemetadatas",
					func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, tc.updateError
					})
			}

			if tc.deleteError != nil {
				fakeClient.MopClient.PrependReactor(
					"delete",
					"meshservicemetadatas",
					func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
						return true, nil, tc.deleteError
					})
			}

			registry := prometheus.NewRegistry()
			if tc.msmRecordInCluster != nil {
				// Record a fake stale metric
				recordStaleResourceMetrics("Service", clusterName, registry, tc.msmRecordInCluster, 5)
			}

			manager := NewServiceResourceManager(logger, msmInformer, client.MopApiClient(), nil, nil, registry)
			err := manager.UnTrackOwner(clusterName, namespace, "Service", serviceName, uid, logger)

			if tc.deleteError != nil {
				assert.NotNil(t, err)
				assert.True(t, strings.Contains(err.Error(), "failed to delete msm record"))
				assert.Equal(t, tc.isCriticalError, !error2.IsNonCriticalReconcileError(err))
				return
			}

			if tc.updateError != nil {
				assert.NotNil(t, err)
				assert.True(t, strings.Contains(err.Error(), "failed to update msm record"))
				assert.Equal(t, tc.isCriticalError, !error2.IsNonCriticalReconcileError(err))
				return
			}

			assert.Nil(t, err)
			msmInCluster, err := fakeClient.MopClient.MeshV1alpha1().MeshServiceMetadatas(namespace).Get(context.TODO(), serviceName, metav1.GetOptions{})
			if tc.expectMsmDeleted {
				assert.NotNil(t, err)
				assert.True(t, k8serrors.IsNotFound(err))
			} else {
				assert.ElementsMatchf(t, tc.expectedServiceOwners, msmInCluster.Spec.Owners, "")
			}

			if tc.msmRecordInCluster != nil {
				metricsLabels := map[string]string{
					metrics.ResourceNameLabel: tc.msmRecordInCluster.Name,
					metrics.NamespaceLabel:    tc.msmRecordInCluster.Namespace,
					metrics.ClusterLabel:      clusterName,
					metrics.ResourceKind:      "Service"}

				expectedValue := 5
				if tc.expectMsmDeleted {
					// Verify that stale metric was removed
					expectedValue = 0
				}

				metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, metrics.StaleConfigObjects,
					metricsLabels, float64(expectedValue))
			}
		})
	}
}

func TestCrossNamespaceResourceDeletion(t *testing.T) {
	var primaryCluster = "cluster1"
	var remoteCluster = "cluster2"
	var serviceName = "svc1"
	var serviceUid types.UID = "1234"
	var testNamespace = "test-namespace"
	var otherNamespace = "other-namespace"

	crossNamespaceResourceName := "filter1"
	crossNamespaceParentKey := common.GetResourceParent(testNamespace, serviceName, "Service")

	resourceInOtherNamespace := createEnvoyFilter(otherNamespace, crossNamespaceResourceName, "uid-1", map[string]string{"mesh.io/managed-by": "mesh-operator"}, map[string]string{constants.ResourceParent: crossNamespaceParentKey})
	resourceInOtherNamespaceOwnedByOther := createEnvoyFilter(otherNamespace, crossNamespaceResourceName, "uid-1", map[string]string{transition.ManagedByCopilotLabel: transition.ManagedByCopilotValue}, nil)
	resourceInTestNamespace := createEnvoyFilter(testNamespace, "filter2", "uid-2", map[string]string{"mesh.io/managed-by": "mesh-operator"}, nil)

	otherNamespaceResourceReference := buildReference(constants.EnvoyFilterResource.GroupVersion().String(), "EnvoyFilter", crossNamespaceResourceName, otherNamespace, "uid-1", false)
	testNamespaceResourceReference := buildReference(constants.EnvoyFilterResource.GroupVersion().String(), "EnvoyFilter", "filter2", testNamespace, "uid-2", false)

	primaryClusterServiceOwner := v1alpha1.Owner{
		Cluster:    primaryCluster,
		ApiVersion: "v1",
		Kind:       "Service",
		Name:       serviceName,
		UID:        serviceUid,
	}

	remoteClusterServiceOwner := v1alpha1.Owner{
		Cluster:    remoteCluster,
		ApiVersion: "v1",
		Kind:       "Service",
		Name:       serviceName,
		UID:        "5678",
	}

	expectedMetricLabels := metrics.GetLabelsForK8sResource("Service", primaryCluster, testNamespace, serviceName)

	testCases := []struct {
		name                                   string
		msmInCluster                           *v1alpha1.MeshServiceMetadata
		objectsInCluster                       []runtime.Object
		crossNsResourceDeletionError           error
		msmDeletionError                       error
		crossNamespaceResourceDeletionExpected bool
		msmRecordDeletionExpected              bool
		expectedCrossNsDeletionMetricCount     float64
		expectedError                          error
	}{
		{
			name: "Cross Namespace Resource - deletion successful, MSM record - deletion successful",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{otherNamespaceResourceReference, testNamespaceResourceReference},
					Owners:         []v1alpha1.Owner{primaryClusterServiceOwner},
				},
			},
			objectsInCluster:                       []runtime.Object{resourceInOtherNamespace, resourceInTestNamespace},
			crossNamespaceResourceDeletionExpected: true,
			msmRecordDeletionExpected:              true,
			expectedCrossNsDeletionMetricCount:     1,
		},
		{
			name: "Cross Namespace Resource - deletion successful, MSM record - deletion failed",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{otherNamespaceResourceReference, testNamespaceResourceReference},
					Owners:         []v1alpha1.Owner{primaryClusterServiceOwner},
				},
			},
			objectsInCluster:                       []runtime.Object{resourceInOtherNamespace, resourceInTestNamespace},
			msmDeletionError:                       fmt.Errorf("failed to delete msm"),
			crossNamespaceResourceDeletionExpected: true,
			msmRecordDeletionExpected:              false,
			expectedCrossNsDeletionMetricCount:     1,
			expectedError:                          fmt.Errorf("failed to untrack service cluster1/test-namespace/svc1: failed to delete msm"),
		},
		{
			name: "Cross Namespace Resource - deletion failed, MSM record - not deleted",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{otherNamespaceResourceReference, testNamespaceResourceReference},
					Owners:         []v1alpha1.Owner{primaryClusterServiceOwner},
				},
			},
			objectsInCluster:                       []runtime.Object{resourceInOtherNamespace, resourceInTestNamespace},
			crossNsResourceDeletionError:           fmt.Errorf("failed cross ns deletion"),
			crossNamespaceResourceDeletionExpected: false,
			msmRecordDeletionExpected:              false,
			expectedCrossNsDeletionMetricCount:     0,
			expectedError:                          fmt.Errorf("failed cross ns deletion"),
		},
		{
			name: "Cross ns config owned by same service across multiple cluster - no deletion",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{otherNamespaceResourceReference, testNamespaceResourceReference},
					Owners:         []v1alpha1.Owner{primaryClusterServiceOwner, remoteClusterServiceOwner},
				},
			},
			objectsInCluster:                       []runtime.Object{resourceInOtherNamespace, resourceInTestNamespace},
			crossNamespaceResourceDeletionExpected: false,
			msmRecordDeletionExpected:              false,
		},
		{
			name: "cross ns resource owned by other controller - no deletion",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{otherNamespaceResourceReference, testNamespaceResourceReference},
					Owners:         []v1alpha1.Owner{primaryClusterServiceOwner},
				},
			},
			objectsInCluster:                       []runtime.Object{resourceInOtherNamespaceOwnedByOther, resourceInTestNamespace},
			crossNamespaceResourceDeletionExpected: false,
			msmRecordDeletionExpected:              true,
			expectedCrossNsDeletionMetricCount:     0,
		},
		{
			name: "No cross namespace referenced in MSM - No deletion",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{testNamespaceResourceReference},
					Owners:         []v1alpha1.Owner{primaryClusterServiceOwner},
				},
			},
			objectsInCluster:                       []runtime.Object{resourceInOtherNamespace, resourceInTestNamespace},
			crossNamespaceResourceDeletionExpected: false,
			msmRecordDeletionExpected:              true,
			expectedCrossNsDeletionMetricCount:     0,
		},
		{
			name: "Cross ns resource not found in cluster",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{otherNamespaceResourceReference, testNamespaceResourceReference},
					Owners:         []v1alpha1.Owner{primaryClusterServiceOwner},
				},
			},
			objectsInCluster:                       []runtime.Object{resourceInTestNamespace},
			crossNamespaceResourceDeletionExpected: true,
			msmRecordDeletionExpected:              true,
			expectedCrossNsDeletionMetricCount:     1,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	features.EnableCrossNamespaceResourceDeletion = true

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := kube_test.NewKubeClientBuilder().AddDynamicClientObjects(tc.objectsInCluster...).AddMopClientObjects(tc.msmInCluster).Build()
			fakeClient := (client).(*kube_test.FakeClient)
			registry := prometheus.NewRegistry()

			if tc.crossNsResourceDeletionError != nil {
				fakeClient.DynamicClient.PrependReactor("delete", "envoyfilters", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.crossNsResourceDeletionError
				})
			}

			if tc.msmDeletionError != nil {
				fakeClient.MopClient.PrependReactor("delete", "meshservicemetadatas", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.msmDeletionError
				})
			}

			msmInformer := client.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()

			m := NewServiceResourceManager(logger, msmInformer, fakeClient.MopClient, fakeClient.DynamicClient, fakeClient.DiscoveryClient, registry)

			err := m.UnTrackOwner(primaryCluster, testNamespace, "Service", serviceName, serviceUid, logger)

			// validate untrack error
			if tc.expectedError != nil {
				assert.Errorf(t, err, tc.expectedError.Error())
			} else {
				assert.Nil(t, err)
			}

			// retrieve cross ns resource
			crossNsResourceInCluster, resourceRetrievalErr := fakeClient.DynamicClient.Resource(constants.EnvoyFilterResource).Namespace(otherNamespace).Get(context.TODO(), crossNamespaceResourceName, metav1.GetOptions{})

			// validate cross ns resource was deleted
			if tc.crossNamespaceResourceDeletionExpected {
				assert.Nil(t, crossNsResourceInCluster)
				assert.True(t, k8serrors.IsNotFound(resourceRetrievalErr))
			} else {
				assert.NotNil(t, crossNsResourceInCluster)
				assert.Nil(t, resourceRetrievalErr)
			}

			// retrieve msm record
			msmRecord, msmRetrievalError := fakeClient.MopClient.MeshV1alpha1().MeshServiceMetadatas(testNamespace).Get(context.TODO(), serviceName, metav1.GetOptions{})

			// validate msm record was deleted
			if tc.msmRecordDeletionExpected {
				assert.Nil(t, msmRecord)
				assert.True(t, k8serrors.IsNotFound(msmRetrievalError))
			} else {
				assert.NotNil(t, msmRecord)
				assert.Nil(t, msmRetrievalError)
			}

			assertCounterWithLabels(t, registry, metrics.CrossNamespaceConfigDeleted, expectedMetricLabels, tc.expectedCrossNsDeletionMetricCount)

		})
	}

	features.EnableCrossNamespaceResourceDeletion = false
}

func TestGetCrossNamespaceResources(t *testing.T) {

	var testNamespace = "test-namespace"
	var otherNamespace = "other-namespace"
	var someOtherNamespace = "some-other-namespace"

	otherNamespaceResourceReference := buildReference(constants.EnvoyFilterResource.GroupVersion().String(), "EnvoyFilter", "filter1", otherNamespace, "test1", false)
	someOtherNamespaceResourceReference := buildReference(constants.EnvoyFilterResource.GroupVersion().String(), "EnvoyFilter", "filter1", someOtherNamespace, "someother1", false)
	testNamespaceResource1 := buildReference(constants.EnvoyFilterResource.GroupVersion().String(), "EnvoyFilter", "filter2", testNamespace, "other1", false)
	testNamespaceResource2 := buildReference(constants.EnvoyFilterResource.GroupVersion().String(), "EnvoyFilter", "filter2", testNamespace, "other2", false)

	testCases := []struct {
		name             string
		msm              *v1alpha1.MeshServiceMetadata
		serviceNamespace string
		expectedResults  []v1alpha1.OwnedResource
	}{
		{
			name: "Single Cross namespace resource exist",
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{otherNamespaceResourceReference, testNamespaceResource1, testNamespaceResource2},
				},
			},
			expectedResults: []v1alpha1.OwnedResource{
				otherNamespaceResourceReference,
			},
		},
		{
			name: "Multiple Cross namespace resource exist",
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{otherNamespaceResourceReference, testNamespaceResource1, testNamespaceResource2, someOtherNamespaceResourceReference},
				},
			},
			expectedResults: []v1alpha1.OwnedResource{
				otherNamespaceResourceReference, someOtherNamespaceResourceReference,
			},
		},
		{
			name: "No Cross Namespace Resource",
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{testNamespaceResource1, testNamespaceResource2},
				},
			},
			expectedResults: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := getCrossNamespaceResources(tc.msm)
			assert.ElementsMatch(t, result, tc.expectedResults)

		})
	}
}

func assertCounterWithLabels(t *testing.T, registry *prometheus.Registry, metricName string, labels map[string]string, expectedValue float64) {
	counter := commonmetrics.GetOrRegisterCounterWithLabels(metricName, labels, registry)
	assert.NotNil(t, counter)

	serializedMetric := &dto.Metric{}
	_ = counter.Write(serializedMetric)

	assert.Equal(t, expectedValue, serializedMetric.GetCounter().GetValue())
}

func createEnvoyFilter(namespace string, name string, uid types.UID, labels map[string]string, annotations map[string]string) *istiov1alpha3.EnvoyFilter {
	filter := istiov1alpha3.EnvoyFilter{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.EnvoyFilterKind.Kind,
			APIVersion: constants.EnvoyFilterResource.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       uid,
		},
	}
	filter.SetLabels(labels)
	filter.SetAnnotations(annotations)
	return &filter
}
