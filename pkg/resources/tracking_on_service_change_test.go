package resources

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/deployment"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	"github.com/istio-ecosystem/mesh-operator/pkg/rollout"
	"github.com/istio-ecosystem/mesh-operator/pkg/statefulset"
	"k8s.io/client-go/tools/cache"

	"github.com/istio-ecosystem/mesh-operator/pkg/reconcilemetadata"

	"github.com/istio-ecosystem/mesh-operator/pkg/transition"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	metricstesting "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics/testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

var (
	clusterName                      = "cluster-1"
	serviceName                      = "svc1"
	uid                    types.UID = "abcd-123"
	namespace                        = "test-namespace"
	virtualServiceKind               = "VirtualService"
	testApiVersion                   = "networking.istio.io/v1alpha3"
	serviceResourceVersion           = "1234567"
	service                          = kube_test.CreateService(namespace, serviceName)

	resource1       = buildResource(testApiVersion, virtualServiceKind, "vs-1", namespace, "uid-1", "mesh-operator")
	reference1      = buildReference(testApiVersion, virtualServiceKind, "vs-1", namespace, "uid-1", false)
	staleReference1 = buildReference(testApiVersion, virtualServiceKind, "vs-1", namespace, "uid-1", true)

	resource2  = buildResource(testApiVersion, virtualServiceKind, "vs-2", namespace, "uid-2", "mesh-operator")
	reference2 = buildReference(testApiVersion, virtualServiceKind, "vs-2", namespace, "uid-2", false)

	resource3  = buildResource(testApiVersion, virtualServiceKind, "vs-3", namespace, "uid-3", "mesh-operator")
	reference3 = buildReference(testApiVersion, virtualServiceKind, "vs-3", namespace, "uid-3", false)

	otherReference      = buildReference(testApiVersion, virtualServiceKind, "vs-4", namespace, "uid-4", false)
	otherStaleReference = buildReference(testApiVersion, virtualServiceKind, "vs-4", namespace, "uid-4", true)

	transitionReference      = buildReference(testApiVersion, virtualServiceKind, "vs-5", namespace, "uid-5", false)
	transitionStaleReference = buildReference(testApiVersion, virtualServiceKind, "vs-5", namespace, "uid-5", true)

	brokenReference      = buildReference("bad-api-version", virtualServiceKind, "vs-3", namespace, "uid-3", false)
	staleBrokenReference = buildReference("bad-api-version", virtualServiceKind, "vs-3", namespace, "uid-3", true)

	nonExistentReference      = buildReference(testApiVersion, virtualServiceKind, "doesnt-exist", namespace, "uid-10", false)
	staleNonExistentReference = buildReference(testApiVersion, virtualServiceKind, "doesnt-exist", namespace, "uid-10", true)

	crossNamespaceResource = buildCrossNamespaceResource(testApiVersion, virtualServiceKind, "cross-ns-resource", "other-namespace", "uid-20", "mesh-operator",
		common.GetResourceParent(namespace, serviceName, "Service"))
	crossNamespaceResourceForSe = buildCrossNamespaceResource(testApiVersion, virtualServiceKind, "cross-ns-resource", "other-namespace", "uid-20", "mesh-operator",
		common.GetResourceParent(namespace, serviceName, "ServiceEntry"))
	crossNamespaceReference      = buildReference(testApiVersion, virtualServiceKind, "cross-ns-resource", "other-namespace", "uid-20", false)
	staleCrossNamespaceReference = buildReference(testApiVersion, virtualServiceKind, "cross-ns-resource", "other-namespace", "uid-20", true)
)

func TestOnServiceChangeDeleteDisabled(t *testing.T) {
	service.UID = uid
	testCases := []struct {
		name                      string
		msmInCluster              *v1alpha1.MeshServiceMetadata
		newResources              []*unstructured.Unstructured
		initialResourcesInCluster []runtime.Object
		assertReferences          []v1alpha1.OwnedResource
		updateError               error
		enableCrossNsDeletion     bool
		expectedStaleMetric       int
	}{
		{
			name: "No obsolete resources",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{},
				},
			},
			newResources:        []*unstructured.Unstructured{resource1, resource2},
			assertReferences:    []v1alpha1.OwnedResource{reference1, reference2},
			expectedStaleMetric: 0,
		},
		{
			name: "Obsolete resources - cleanUp not enabled",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, reference2},
				},
			},
			newResources:              []*unstructured.Unstructured{resource2, resource3},
			initialResourcesInCluster: []runtime.Object{resource1, resource2},
			assertReferences:          []v1alpha1.OwnedResource{staleReference1, reference2, reference3},
			expectedStaleMetric:       1,
		},
		{
			name: "Invalid reference in list",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{brokenReference, reference1, brokenReference},
				},
			},
			newResources:              []*unstructured.Unstructured{resource2},
			initialResourcesInCluster: []runtime.Object{resource1},
			assertReferences:          []v1alpha1.OwnedResource{staleBrokenReference, staleBrokenReference, staleReference1, reference2},
			expectedStaleMetric:       3,
		},
		{
			name: "Update msm failed",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, reference2},
				},
			},
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{resource1, resource2},
			assertReferences:          []v1alpha1.OwnedResource{reference1, reference2},
			updateError:               fmt.Errorf("test-error"),
		},
		{
			name: "Non existent reference in the list",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, nonExistentReference},
				},
			},
			newResources:              []*unstructured.Unstructured{resource2},
			initialResourcesInCluster: []runtime.Object{resource1},
			assertReferences:          []v1alpha1.OwnedResource{staleReference1, staleNonExistentReference, reference2},
			expectedStaleMetric:       2,
		},
		{
			name: "Cross ns reference being removed, cross-ns deletes disabled",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, crossNamespaceReference},
				},
			},
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{resource1, crossNamespaceResource},
			assertReferences:          []v1alpha1.OwnedResource{reference1, staleCrossNamespaceReference},
			expectedStaleMetric:       1,
		},
		{
			name: "Cross ns reference being removed, cross-ns deletes enabled",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, crossNamespaceReference},
				},
			},
			enableCrossNsDeletion:     true,
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{resource1, crossNamespaceResource},
			assertReferences:          []v1alpha1.OwnedResource{reference1, staleCrossNamespaceReference},
			expectedStaleMetric:       1,
		},
		{
			name: "Cross ns reference being removed for service entry, cross-ns deletes enabled",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      common.GetRecordNameSuffixedByKind(serviceName, constants.ServiceEntryKind.Kind),
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, crossNamespaceReference},
				},
			},
			enableCrossNsDeletion:     true,
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{resource1, crossNamespaceResourceForSe},
			assertReferences:          []v1alpha1.OwnedResource{reference1, staleCrossNamespaceReference},
			expectedStaleMetric:       1,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			client := kube_test.NewKubeClientBuilder().AddDynamicClientObjects(tc.initialResourcesInCluster...).AddMopClientObjects(tc.msmInCluster).Build()
			fakeClient := (client).(*kube_test.FakeClient)

			if tc.updateError != nil {
				fakeClient.MopClient.PrependReactor("update", "meshservicemetadatas", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.updateError
				})
			}

			msmInformer := client.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()

			m := NewServiceResourceManager(logger, msmInformer, fakeClient.MopClient, fakeClient.DynamicClient, fakeClient.DiscoveryClient, registry)
			owner := ServiceToReference(clusterName, service)
			err := m.OnOwnerChange(clusterName, client, tc.msmInCluster, service, owner, tc.newResources, logger)

			if tc.updateError != nil {
				// Even if msm update failed, we expect resources to be deleted.
				// Don't return, continue asserts.
				assert.NotNil(t, err)
				assert.True(t, strings.Contains(err.Error(), tc.updateError.Error()))
			}

			if tc.enableCrossNsDeletion {
				features.EnableCrossNamespaceResourceDeletion = true
				defer func() { features.EnableCrossNamespaceResourceDeletion = false }()
			}

			// Verify the msm record in cluster
			msmInCluster, err := fakeClient.MopClient.MeshV1alpha1().MeshServiceMetadatas(namespace).Get(context.TODO(), tc.msmInCluster.Name, metav1.GetOptions{})
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.assertReferences, msmInCluster.Spec.OwnedResources)

			metricsLabels := map[string]string{
				metrics.ResourceNameLabel: msmInCluster.Name,
				metrics.NamespaceLabel:    msmInCluster.Namespace,
				metrics.ClusterLabel:      clusterName,
				metrics.ResourceKind:      "Service"}

			metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, metrics.StaleConfigObjects,
				metricsLabels, float64(tc.expectedStaleMetric))

			metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, metrics.StaleConfigDeleted,
				metricsLabels, 0)
		})
	}
}

func TestOnServiceChangeDeleteEnabled(t *testing.T) {
	service.UID = uid
	otherResource := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": testApiVersion,
			"kind":       virtualServiceKind,
			"metadata": map[string]interface{}{
				"namespace": namespace,
				"name":      "vs-4",
				"uid":       "uid-4",
				"labels": map[string]interface{}{
					transition.ManagedByCopilotLabel: transition.ManagedByCopilotValue,
				},
			},
		},
	}
	transitionResource := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": testApiVersion,
			"kind":       virtualServiceKind,
			"metadata": map[string]interface{}{
				"namespace": namespace,
				"name":      "vs-5",
				"uid":       "uid-5",
				"labels": map[string]interface{}{
					"mesh.io/managed-by":             "mesh-operator",
					transition.ManagedByCopilotLabel: transition.ManagedByCopilotValue,
				},
			},
		},
	}
	testCases := []struct {
		name                      string
		msmInCluster              *v1alpha1.MeshServiceMetadata
		newResources              []*unstructured.Unstructured
		initialResourcesInCluster []runtime.Object
		enableCrossNsDeletion     bool
		assertReferences          []v1alpha1.OwnedResource
		assertDeleted             []v1alpha1.OwnedResource
		deleteError               error
		updateError               error
		expectedDeletedMetric     int
		expectedStaleMetric       int
		expectedError             error
	}{
		{
			name: "No obsolete resources - cleanUp enabled",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{},
				},
			},
			newResources:     []*unstructured.Unstructured{resource1, resource2},
			assertReferences: []v1alpha1.OwnedResource{reference1, reference2},
		},
		{
			name: "Obsolete resources - cleanUp enabled",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, reference2},
				},
			},
			newResources:              []*unstructured.Unstructured{resource2, resource3},
			initialResourcesInCluster: []runtime.Object{resource1, resource2},
			assertReferences:          []v1alpha1.OwnedResource{reference2, reference3},
			assertDeleted:             []v1alpha1.OwnedResource{reference1},
			expectedDeletedMetric:     1,
		},
		{
			name: "Obsolete resources - multiple objects deleted",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, reference2},
				},
			},
			newResources:              []*unstructured.Unstructured{resource3},
			initialResourcesInCluster: []runtime.Object{resource1, resource2},
			assertReferences:          []v1alpha1.OwnedResource{reference3},
			assertDeleted:             []v1alpha1.OwnedResource{reference1, reference2},
			expectedDeletedMetric:     2,
		},
		{
			name: "Resource managed by other controller",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{otherReference},
				},
			},
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{&otherResource},
			assertReferences:          []v1alpha1.OwnedResource{reference1, otherStaleReference},
			expectedStaleMetric:       1,
		},
		{
			name: "Resource in transition",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{transitionReference},
				},
			},
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{&transitionResource},
			assertReferences:          []v1alpha1.OwnedResource{reference1, transitionStaleReference},
			expectedStaleMetric:       1,
		},
		{
			name: "Resource missing in cluster",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{otherReference},
				},
			},
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{},
			assertReferences:          []v1alpha1.OwnedResource{reference1},
			expectedDeletedMetric:     1,
		},
		{
			name: "Delete failed (no msm update)",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, reference2},
				},
			},
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{resource1, resource2},
			assertReferences:          []v1alpha1.OwnedResource{reference1, reference2},
			assertDeleted:             []v1alpha1.OwnedResource{},
			deleteError:               fmt.Errorf("test-error"),
			expectedError:             fmt.Errorf("test-error"),
		},
		{
			name: "Update msm failed (delete resources)",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, reference2},
				},
			},
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{resource1, resource2},
			assertReferences:          []v1alpha1.OwnedResource{reference1, reference2},
			assertDeleted:             []v1alpha1.OwnedResource{reference2},
			updateError:               fmt.Errorf("test-error"),
			expectedDeletedMetric:     1,
			expectedError:             fmt.Errorf("test-error"),
		},
		{
			name: "Invalid reference in list",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{brokenReference, reference1},
				},
			},
			newResources:              []*unstructured.Unstructured{resource2},
			initialResourcesInCluster: []runtime.Object{resource1},
			assertReferences:          []v1alpha1.OwnedResource{staleBrokenReference, reference2},
			assertDeleted:             []v1alpha1.OwnedResource{reference1},
			expectedDeletedMetric:     1,
			expectedStaleMetric:       1,
		},
		{
			name: "Cross ns reference being deleted, cross-ns deletes disabled",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, crossNamespaceReference},
				},
			},
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{resource1, crossNamespaceResource},
			assertReferences:          []v1alpha1.OwnedResource{reference1, staleCrossNamespaceReference},
			assertDeleted:             []v1alpha1.OwnedResource{},
			expectedStaleMetric:       1,
			expectedDeletedMetric:     0,
		},
		{
			name: "Cross ns reference being deleted, cross-ns deletes enabled",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, crossNamespaceReference},
				},
			},
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{resource1, crossNamespaceResource},
			enableCrossNsDeletion:     true,
			assertReferences:          []v1alpha1.OwnedResource{reference1},
			assertDeleted:             []v1alpha1.OwnedResource{staleCrossNamespaceReference},
			expectedStaleMetric:       0,
			expectedDeletedMetric:     1,
		},
		{
			name: "Cross ns reference being deleted for service entry, cross-ns deletes enabled",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      common.GetRecordNameSuffixedByKind(serviceName, constants.ServiceEntryKind.Kind),
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{reference1, crossNamespaceReference},
				},
			},
			newResources:              []*unstructured.Unstructured{resource1},
			initialResourcesInCluster: []runtime.Object{resource1, crossNamespaceResourceForSe},
			enableCrossNsDeletion:     true,
			assertReferences:          []v1alpha1.OwnedResource{reference1},
			assertDeleted:             []v1alpha1.OwnedResource{staleCrossNamespaceReference},
			expectedStaleMetric:       0,
			expectedDeletedMetric:     1,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	features.EnableCleanUpObsoleteResources = true

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			client := kube_test.NewKubeClientBuilder().AddDynamicClientObjects(tc.initialResourcesInCluster...).AddMopClientObjects(tc.msmInCluster).Build()
			fakeClient := (client).(*kube_test.FakeClient)

			if tc.deleteError != nil {
				fakeClient.DynamicClient.PrependReactor("delete", "*", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.deleteError
				})
			}
			var msmUpdateCalled bool
			if tc.updateError != nil {
				fakeClient.MopClient.PrependReactor("update", "meshservicemetadatas", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					msmUpdateCalled = true
					return true, nil, tc.updateError
				})
			}

			if tc.enableCrossNsDeletion {
				features.EnableCrossNamespaceResourceDeletion = true
				defer func() { features.EnableCrossNamespaceResourceDeletion = false }()
			}

			msmInformer := client.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()

			m := NewServiceResourceManager(logger, msmInformer, fakeClient.MopClient, fakeClient.DynamicClient, fakeClient.DiscoveryClient, registry)
			ownerService := ServiceToReference(clusterName, service)
			err := m.OnOwnerChange(clusterName, client, tc.msmInCluster, service, ownerService, tc.newResources, logger)

			if tc.expectedError != nil {
				assert.Error(t, err, tc.expectedError.Error())
			}

			if tc.deleteError != nil {
				assert.False(t, msmUpdateCalled)
				// If delete fails, we don't expect msm record to be updated. So nothing to assert.
				return
			}

			if tc.updateError != nil {
				// Even if msm update failed, we expect resources to be deleted.
				// Don't return, continue asserts.
				assert.NotNil(t, err)
			}

			// Verify the msm record in cluster
			msmInCluster, err := fakeClient.MopClient.MeshV1alpha1().MeshServiceMetadatas(namespace).Get(context.TODO(), tc.msmInCluster.Name, metav1.GetOptions{})
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.assertReferences, msmInCluster.Spec.OwnedResources)

			// Verify deleted resources do not exist cluster anymore
			for _, deletedResource := range tc.assertDeleted {
				testSchema := schema.GroupVersionResource{
					Group:    deletedResource.ApiVersion,
					Version:  deletedResource.Kind,
					Resource: "virtualservices",
				}
				_, err = fakeClient.DynamicClient.Resource(testSchema).Namespace(namespace).
					Get(context.TODO(), deletedResource.Name, metav1.GetOptions{})

				assert.True(t, k8serrors.IsNotFound(err))
			}

			metricsLabels := map[string]string{
				metrics.ResourceNameLabel: service.Name,
				metrics.NamespaceLabel:    service.Namespace,
				metrics.ClusterLabel:      clusterName,
				metrics.ResourceKind:      "Service"}

			metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, metrics.StaleConfigObjects,
				metricsLabels, float64(tc.expectedStaleMetric))

			metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, metrics.StaleConfigDeleted,
				metricsLabels, float64(tc.expectedDeletedMetric))
		})
	}
	features.EnableCleanUpObsoleteResources = false
}

// TestSkippedCrossNamespaceResourceDeletions tests for cases where deletion is skipped when both obsolete resource cleanup and cross-ns deletion is enabled.
func TestSkippedCrossNamespaceResourceDeletions(t *testing.T) {
	differentOwnerResource := buildCrossNamespaceResource(testApiVersion, virtualServiceKind, "cross-ns-different-parent", "other-namespace", "uid", "mesh-operator",
		common.GetResourceParent(namespace, "other-svc", "Service"))
	differentOwnerReference := buildReference(testApiVersion, virtualServiceKind, "cross-ns-different-parent", "other-namespace", "uid", false)
	staleDifferentOwnerReference := buildReference(testApiVersion, virtualServiceKind, "cross-ns-different-parent", "other-namespace", "uid", true)

	missingParentAnnotationResource := buildResource(testApiVersion, virtualServiceKind, "cross-ns-missing-parent-annotation", "other-namespace", "uid", "mesh-operator")
	missingParentAnnotationReference := buildReference(testApiVersion, virtualServiceKind, "cross-ns-missing-parent-annotation", "other-namespace", "uid", false)
	staleMissingParentAnnotationReference := buildReference(testApiVersion, virtualServiceKind, "cross-ns-missing-parent-annotation", "other-namespace", "uid", true)

	testCases := []struct {
		name                      string
		msmInCluster              *v1alpha1.MeshServiceMetadata
		initialResourcesInCluster []runtime.Object
		assertExists              []v1alpha1.OwnedResource
	}{
		{
			name: "Cross ns resource with different parent name",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{differentOwnerReference},
				},
			},
			initialResourcesInCluster: []runtime.Object{differentOwnerResource},
			assertExists:              []v1alpha1.OwnedResource{staleDifferentOwnerReference},
		},
		{
			name: "Cross ns resource with a missing parent annotation",
			msmInCluster: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{missingParentAnnotationReference},
				},
			},
			initialResourcesInCluster: []runtime.Object{missingParentAnnotationResource},
			assertExists:              []v1alpha1.OwnedResource{staleMissingParentAnnotationReference},
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	features.EnableCleanUpObsoleteResources = true
	features.EnableCrossNamespaceResourceDeletion = true

	defer func() {
		features.EnableCleanUpObsoleteResources = false
		features.EnableCrossNamespaceResourceDeletion = false
	}()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			client := kube_test.NewKubeClientBuilder().AddDynamicClientObjects(tc.initialResourcesInCluster...).AddMopClientObjects(tc.msmInCluster).Build()
			fakeClient := (client).(*kube_test.FakeClient)

			msmInformer := client.MopInformerFactory().Mesh().V1alpha1().MeshServiceMetadatas()

			m := NewServiceResourceManager(logger, msmInformer, fakeClient.MopClient, fakeClient.DynamicClient, fakeClient.DiscoveryClient, registry)
			ownerService := ServiceToReference(clusterName, service)
			err := m.OnOwnerChange(clusterName, client, tc.msmInCluster, service, ownerService, []*unstructured.Unstructured{}, logger)

			// Verify the msm record in cluster
			msmInCluster, err := fakeClient.MopClient.MeshV1alpha1().MeshServiceMetadatas(namespace).Get(context.TODO(), serviceName, metav1.GetOptions{})
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.assertExists, msmInCluster.Spec.OwnedResources)

			metricsLabels := map[string]string{
				metrics.ResourceNameLabel: msmInCluster.Name,
				metrics.NamespaceLabel:    msmInCluster.Namespace,
				metrics.ClusterLabel:      clusterName,
				metrics.ResourceKind:      "Service"}

			metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, metrics.StaleConfigObjects,
				metricsLabels, 1.0)

			metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, metrics.StaleConfigDeleted,
				metricsLabels, 0.0)
		})
	}
}

func TestTrackReconcileMetadata(t *testing.T) {
	ownerResource := buildReference(testApiVersion, virtualServiceKind, serviceName, namespace, "uid", false)
	primarySvcResourceVersion := "1234567"
	remoteSvcResourceVersion := "789126"

	testsvc := kube_test.NewServiceBuilder(serviceName, namespace).SetResourceVersion(primarySvcResourceVersion).Build()
	primarySvcHashKey := "primary_Service_test-namespace_svc1"

	remoteClusterService := kube_test.NewServiceBuilder(serviceName, namespace).SetResourceVersion(remoteSvcResourceVersion).Build()
	remoteSvcHashKey := "remoteCluster_Service_test-namespace_svc1"

	primaryCluster := "primary"
	remoteCluster := "remoteCluster"

	var stsGeneration int64 = 14
	var stsUuid = "6f80f3cc-8976-4305-a77d-fe0151d353cd"
	testSts := kube_test.NewStatefulSetBuilder(namespace, "sts1", serviceName, 2).
		SetGeneration(stsGeneration).Build()
	testSts.SetUID(types.UID(stsUuid))
	stsHashKey := "primary_StatefulSet_test-namespace_sts1"
	stsHashValue := fmt.Sprintf("%s-%d", stsUuid, stsGeneration)

	var rolloutGeneration int64 = 23
	var rolloutUuid = "42c735d1-219e-4c12-9c4d-f630a23c86cf"
	testRollout := kube_test.NewRolloutBuilder("rollout1", namespace).
		SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: serviceName}).
		SetGeneration(rolloutGeneration).Build()
	testRollout.SetUID(types.UID(rolloutUuid))
	rolloutHashKey := "primary_Rollout_test-namespace_rollout1"
	rolloutHashValue := fmt.Sprintf("%s-%d", rolloutUuid, rolloutGeneration)

	var deploymentGeneration int64 = 43
	var deploymentUuid = "4ef31557-b1e7-497b-930c-a7323eef572d"
	testDeploy := kube_test.NewDeploymentBuilder(namespace, "deploy1").
		SetGeneration(deploymentGeneration).
		SetLabels(map[string]string{string(constants.DynamicRoutingServiceLabel): serviceName}).Build()
	testDeploy.SetUID(types.UID(deploymentUuid))

	labelHash := sha256.Sum256([]byte(serviceName))
	expectedDeployHash := fmt.Sprintf("%s-%d-%x", deploymentUuid, deploymentGeneration, labelHash)
	deploymentHashKey := "primary_Deployment_test-namespace_deploy1"

	testCases := []struct {
		name                            string
		clusterName                     string
		msm                             *v1alpha1.MeshServiceMetadata
		ownerObject                     interface{}
		expectedMsmStatus               *v1alpha1.MeshServiceMetadataStatus
		msmUpdateError                  error
		enableSkipReconcileOnRestart    bool
		skipSvcEnqueueForRelatedObjects bool
		expectedError                   error
		statefulsetsInCluster           []runtime.Object
		rolloutsInCluster               []runtime.Object
		deploymentsInCluster            []runtime.Object
	}{
		{
			name:        "EnableSkipReconcileOnRestart - no update in MSM status",
			clusterName: primaryCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{ownerResource},
				},
			},
			ownerObject:                  &testsvc,
			expectedMsmStatus:            &v1alpha1.MeshServiceMetadataStatus{},
			enableSkipReconcileOnRestart: false,
		},
		{
			name:        "MSM status is empty, track reconcile data",
			clusterName: primaryCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{ownerResource},
				},
			},
			ownerObject: &testsvc,
			expectedMsmStatus: &v1alpha1.MeshServiceMetadataStatus{
				LastReconciledTime:     &constants.FakeTime,
				ReconciledTemplateHash: features.MopConfigTemplatesHash,
				ReconciledObjectHash: map[string]string{
					primarySvcHashKey: primarySvcResourceVersion,
				},
			},
			enableSkipReconcileOnRestart: true,
		},
		{
			name:        "MSM status is not empty, track reconcile data",
			clusterName: primaryCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{ownerResource},
				},
				Status: v1alpha1.MeshServiceMetadataStatus{
					LastReconciledTime:     &constants.FakeTime,
					ReconciledTemplateHash: features.MopConfigTemplatesHash,
					ReconciledObjectHash: map[string]string{
						primarySvcHashKey: primarySvcResourceVersion,
					},
				},
			},
			ownerObject: &testsvc,
			expectedMsmStatus: &v1alpha1.MeshServiceMetadataStatus{
				LastReconciledTime:     &constants.FakeTime,
				ReconciledTemplateHash: features.MopConfigTemplatesHash,
				ReconciledObjectHash: map[string]string{
					primarySvcHashKey: primarySvcResourceVersion,
				},
			},
			enableSkipReconcileOnRestart: true,
		},
		{
			name:        "Track reconcile metadata for object from another cluster",
			clusterName: remoteCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{ownerResource},
				},
				Status: v1alpha1.MeshServiceMetadataStatus{
					LastReconciledTime:     &constants.FakeTime,
					ReconciledTemplateHash: features.MopConfigTemplatesHash,
					ReconciledObjectHash: map[string]string{
						primarySvcHashKey: primarySvcResourceVersion,
					},
				},
			},
			ownerObject: &remoteClusterService,
			expectedMsmStatus: &v1alpha1.MeshServiceMetadataStatus{
				LastReconciledTime:     &constants.FakeTime,
				ReconciledTemplateHash: features.MopConfigTemplatesHash,
				ReconciledObjectHash: map[string]string{
					primarySvcHashKey: primarySvcResourceVersion,
					remoteSvcHashKey:  remoteSvcResourceVersion,
				},
			},
			enableSkipReconcileOnRestart: true,
		},
		{
			name:        "Error updating MSM status",
			clusterName: primaryCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{ownerResource},
				},
			},
			ownerObject:                  &testsvc,
			expectedMsmStatus:            &v1alpha1.MeshServiceMetadataStatus{},
			msmUpdateError:               fmt.Errorf("update error"),
			enableSkipReconcileOnRestart: true,
			expectedError:                fmt.Errorf("update error"),
		},
		{
			name:        "Track reconcile data with related objects hash",
			clusterName: primaryCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{ownerResource},
				},
			},
			ownerObject:           &testsvc,
			statefulsetsInCluster: []runtime.Object{testSts},
			rolloutsInCluster:     []runtime.Object{testRollout},
			deploymentsInCluster:  []runtime.Object{testDeploy},
			expectedMsmStatus: &v1alpha1.MeshServiceMetadataStatus{
				LastReconciledTime:     &constants.FakeTime,
				ReconciledTemplateHash: features.MopConfigTemplatesHash,
				ReconciledObjectHash: map[string]string{
					primarySvcHashKey: primarySvcResourceVersion,
					stsHashKey:        stsHashValue,
					rolloutHashKey:    rolloutHashValue,
					deploymentHashKey: expectedDeployHash,
				},
			},
			enableSkipReconcileOnRestart:    true,
			skipSvcEnqueueForRelatedObjects: true,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.EnableSkipReconcileOnRestart = tc.enableSkipReconcileOnRestart
			defer func() {
				features.EnableSkipReconcileOnRestart = false
			}()
			registry := prometheus.NewRegistry()
			client := kube_test.NewKubeClientBuilder().AddMopClientObjects(tc.msm).Build()
			fakeClient := (client).(*kube_test.FakeClient)
			var clusterClient kube.Client

			if tc.skipSvcEnqueueForRelatedObjects {
				features.SkipSvcEnqueueForRelatedObjects = true
				features.EnableArgoIntegration = true

				defer func() {
					features.SkipSvcEnqueueForRelatedObjects = false
					features.EnableArgoIntegration = false
				}()

				clusterClient = kube_test.NewKubeClientBuilder().
					AddK8sObjects(tc.statefulsetsInCluster...).
					AddK8sObjects(tc.deploymentsInCluster...).
					AddDynamicClientObjects(tc.rolloutsInCluster...).
					Build()

				rolloutInformer := clusterClient.DynamicInformerFactory().ForResource(constants.RolloutResource).Informer()
				err := rollout.AddServiceNameToRolloutIndexer(rolloutInformer)
				assert.Nil(t, err)

				stsInformer := clusterClient.KubeInformerFactory().Apps().V1().StatefulSets()
				err = statefulset.AddStsIndexerIfNotExists(stsInformer.Informer())
				assert.Nil(t, err)

				deploymentsInformer := clusterClient.KubeInformerFactory().Apps().V1().Deployments().Informer()
				err = deployment.AddDeploymentIndexerIfNotExists(deploymentsInformer)
				assert.Nil(t, err)

				go rolloutInformer.Run(ctx.Done())
				cache.WaitForCacheSync(ctx.Done(), rolloutInformer.HasSynced)

				go stsInformer.Informer().Run(ctx.Done())
				cache.WaitForCacheSync(ctx.Done(), stsInformer.Informer().HasSynced)

				go deploymentsInformer.Run(ctx.Done())
				cache.WaitForCacheSync(ctx.Done(), deploymentsInformer.HasSynced)

			}

			if tc.msmUpdateError != nil {
				fakeClient.MopClient.PrependReactor("update", "meshservicemetadatas", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.msmUpdateError
				})
			}

			m := resourceManager{logger: logger, meshApiClient: fakeClient.MopClient, dynamicClient: fakeClient.DynamicClient, discoveryClient: fakeClient.DiscoveryClient, metricsRegistry: registry, reconcileHashManager: reconcilemetadata.NewReconcileHashManager(), timeProvider: kube_test.NewFakeTimeProvider()}
			trackReconcileMetadataErr := m.trackReconcileMetadata(tc.clusterName, clusterClient, tc.msm, tc.ownerObject, "Service")
			if tc.expectedError != nil {
				assert.Error(t, tc.expectedError, trackReconcileMetadataErr.Error())
			} else {
				assert.Nil(t, trackReconcileMetadataErr)
			}

			msmInCluster, err := fakeClient.MopClient.MeshV1alpha1().MeshServiceMetadatas(namespace).Get(context.TODO(), serviceName, metav1.GetOptions{})
			assert.Equal(t, tc.expectedMsmStatus, &msmInCluster.Status)
			assert.Nil(t, err)
		})
	}
}

func TestUnTrackReconcileMetadata(t *testing.T) {
	ownerResource := buildReference(testApiVersion, virtualServiceKind, serviceName, namespace, "uid", false)
	primarySvcResourceVersion := "1234567"
	remoteSvcResourceVersion := "789126"
	primaryCluster := "primary"
	remoteCluster := "remoteCluster"

	testCases := []struct {
		name                         string
		clusterName                  string
		msm                          *v1alpha1.MeshServiceMetadata
		expectedMsmStatus            *v1alpha1.MeshServiceMetadataStatus
		msmUpdateError               error
		enableSkipReconcileOnRestart bool
		expectedError                error
	}{
		{
			name:        "EnableSkipReconcileOnRestart - no update in MSM status",
			clusterName: primaryCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{ownerResource},
				},
				Status: v1alpha1.MeshServiceMetadataStatus{
					ReconciledObjectHash: map[string]string{
						primaryCluster: primarySvcResourceVersion,
					},
				},
			},
			expectedMsmStatus: &v1alpha1.MeshServiceMetadataStatus{
				ReconciledObjectHash: map[string]string{
					primaryCluster: primarySvcResourceVersion,
				},
			},
			enableSkipReconcileOnRestart: false,
		},
		{
			name:        "Happy path - Metadata untracked successfully",
			clusterName: remoteCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{ownerResource},
				},
				Status: v1alpha1.MeshServiceMetadataStatus{
					LastReconciledTime:     &constants.FakeTime,
					ReconciledTemplateHash: features.MopConfigTemplatesHash,
					ReconciledObjectHash: map[string]string{
						primaryCluster: primarySvcResourceVersion,
						remoteCluster:  remoteSvcResourceVersion,
					},
				},
			},
			expectedMsmStatus: &v1alpha1.MeshServiceMetadataStatus{
				LastReconciledTime:     &constants.FakeTime,
				ReconciledTemplateHash: features.MopConfigTemplatesHash,
				ReconciledObjectHash: map[string]string{
					primaryCluster: primarySvcResourceVersion,
				},
			},
			enableSkipReconcileOnRestart: true,
		},
		{
			name:        "Untrack metadata when cluster hash is not set",
			clusterName: remoteCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{ownerResource},
				},
				Status: v1alpha1.MeshServiceMetadataStatus{
					LastReconciledTime:     &constants.FakeTime,
					ReconciledTemplateHash: features.MopConfigTemplatesHash,
					ReconciledObjectHash: map[string]string{
						primaryCluster: primarySvcResourceVersion,
					},
				},
			},
			expectedMsmStatus: &v1alpha1.MeshServiceMetadataStatus{
				LastReconciledTime:     &constants.FakeTime,
				ReconciledTemplateHash: features.MopConfigTemplatesHash,
				ReconciledObjectHash: map[string]string{
					primaryCluster: primarySvcResourceVersion,
				},
			},
			enableSkipReconcileOnRestart: true,
		},
		{
			name:        "Error updating MSM status",
			clusterName: primaryCluster,
			msm: &v1alpha1.MeshServiceMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      serviceName,
				},
				Spec: v1alpha1.MeshServiceMetadataSpec{
					OwnedResources: []v1alpha1.OwnedResource{ownerResource},
				},
				Status: v1alpha1.MeshServiceMetadataStatus{
					LastReconciledTime:     &constants.FakeTime,
					ReconciledTemplateHash: features.MopConfigTemplatesHash,
					ReconciledObjectHash: map[string]string{
						primaryCluster: primarySvcResourceVersion,
					},
				},
			},
			expectedMsmStatus: &v1alpha1.MeshServiceMetadataStatus{
				LastReconciledTime:     &constants.FakeTime,
				ReconciledTemplateHash: features.MopConfigTemplatesHash,
				ReconciledObjectHash: map[string]string{
					primaryCluster: primarySvcResourceVersion,
				},
			},
			msmUpdateError:               fmt.Errorf("update error"),
			enableSkipReconcileOnRestart: true,
			expectedError:                fmt.Errorf("update error"),
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.EnableSkipReconcileOnRestart = tc.enableSkipReconcileOnRestart
			defer func() {
				features.EnableSkipReconcileOnRestart = false
			}()
			registry := prometheus.NewRegistry()
			client := kube_test.NewKubeClientBuilder().AddMopClientObjects(tc.msm).Build()
			fakeClient := (client).(*kube_test.FakeClient)

			if tc.msmUpdateError != nil {
				fakeClient.MopClient.PrependReactor("update", "meshservicemetadatas", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, tc.msmUpdateError
				})
			}

			m := resourceManager{logger: logger, meshApiClient: fakeClient.MopClient, dynamicClient: fakeClient.DynamicClient, discoveryClient: fakeClient.DiscoveryClient, metricsRegistry: registry, reconcileHashManager: reconcilemetadata.NewReconcileHashManager(), timeProvider: kube_test.NewFakeTimeProvider()}
			unTrackReconcileMetadataErr := m.unTrackReconcileMetadata(tc.clusterName, tc.msm)
			if tc.expectedError != nil {
				assert.Error(t, tc.expectedError, unTrackReconcileMetadataErr.Error())
			} else {
				assert.Nil(t, unTrackReconcileMetadataErr)
			}

			msmInCluster, err := fakeClient.MopClient.MeshV1alpha1().MeshServiceMetadatas(namespace).Get(context.TODO(), serviceName, metav1.GetOptions{})
			assert.Equal(t, tc.expectedMsmStatus, &msmInCluster.Status)
			assert.Nil(t, err)
		})
	}
}

func buildResource(apiVersion string, kind string, name string, namespace string, uid string, managedByLabel string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]interface{}{
				"namespace": namespace,
				"name":      name,
				"uid":       uid,
				"labels": map[string]interface{}{
					"mesh.io/managed-by": managedByLabel,
				},
			},
		},
	}
}

func buildCrossNamespaceResource(apiVersion string, kind string, name string, namespace string, uid string, managedByLabel string, parentKey string) *unstructured.Unstructured {
	obj := buildResource(apiVersion, kind, name, namespace, uid, managedByLabel)
	obj.SetAnnotations(map[string]string{constants.ResourceParent: parentKey})
	return obj
}

func buildReference(apiVersion string, kind string, name string, namespace string, uid string, stale bool) v1alpha1.OwnedResource {
	return v1alpha1.OwnedResource{
		ApiVersion: apiVersion,
		Kind:       kind,
		Name:       name,
		Namespace:  namespace,
		UID:        types.UID(uid),
		Stale:      stale,
	}
}
