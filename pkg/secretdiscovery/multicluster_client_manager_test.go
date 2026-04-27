package secretdiscovery

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSyncClusterForSecret(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	testCases := []struct {
		name          string
		currentState  map[string]map[string]DynamicCluster
		secret        *corev1.Secret
		expectedState map[string]map[string]DynamicCluster
	}{
		{
			// Stale cluster deleted, while existing cluster updated
			name: "Stale cluster deleted on secret update",
			currentState: map[string]map[string]DynamicCluster{
				"test-ns/secret1": {
					"cluster1": NewCluster("cluster1", []byte{'a'}, logger),
					"cluster2": NewCluster("cluster2", []byte{'b'}, logger),
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "secret1",
				},
				Data: map[string][]byte{
					"cluster1": {'c'},
				},
			},
			expectedState: map[string]map[string]DynamicCluster{
				"test-ns/secret1": {
					"cluster1": NewCluster("cluster1", []byte{'c'}, logger),
				},
			},
		},
		{
			// Existing cluster under a different secret is left intact
			name: "Existing cluster under different secret",
			currentState: map[string]map[string]DynamicCluster{
				"test-ns/secret2": {
					"cluster1": NewCluster("cluster1", []byte{'a'}, logger),
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "secret1",
				},
				Data: map[string][]byte{
					"cluster1": {'c'},
				},
			},
			expectedState: map[string]map[string]DynamicCluster{
				"test-ns/secret2": {
					"cluster1": NewCluster("cluster1", []byte{'a'}, logger),
				},
			},
		},
		{
			name:         "New cluster added",
			currentState: map[string]map[string]DynamicCluster{},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "secret1",
				},
				Data: map[string][]byte{
					"cluster1": {'a'},
				},
			},
			expectedState: map[string]map[string]DynamicCluster{
				"test-ns/secret1": {
					"cluster1": NewCluster("cluster1", []byte{'a'}, logger),
				},
			},
		},
		{
			name: "Cluster updated",
			currentState: map[string]map[string]DynamicCluster{
				"test-ns/secret1": {
					"cluster1": NewCluster("cluster1", []byte{'a'}, logger),
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "secret1",
				},
				Data: map[string][]byte{
					"cluster1": {'b'},
				},
			},
			expectedState: map[string]map[string]DynamicCluster{
				"test-ns/secret1": {
					"cluster1": NewCluster("cluster1", []byte{'b'}, logger),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			manager := &dynamicDiscovery{
				logger:   logger,
				lock:     sync.RWMutex{},
				clusters: map[string]map[string]DynamicCluster{},
			}
			manager.clusters = tc.currentState

			manager.syncClustersForSecret(tc.secret)

			assert.Equal(t, tc.expectedState, manager.clusters)
		})
	}
}

func TestRemoveClustersForSecret(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	testCases := []struct {
		name          string
		currentState  map[string]map[string]DynamicCluster
		secret        *corev1.Secret
		expectedState map[string]map[string]DynamicCluster
	}{
		{
			name: "Secret with no existing clusters removed",
			currentState: map[string]map[string]DynamicCluster{
				"test-ns/secret2": {
					"cluster1": NewCluster("cluster1", []byte{'a'}, logger),
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "secret1",
				},
				Data: map[string][]byte{
					"cluster1": {'c'},
				},
			},
			expectedState: map[string]map[string]DynamicCluster{
				"test-ns/secret2": {
					"cluster1": NewCluster("cluster1", []byte{'a'}, logger),
				},
			},
		},
		{
			name: "Secret with existing clusters removed",
			currentState: map[string]map[string]DynamicCluster{
				"test-ns/secret1": {
					"cluster1": NewCluster("cluster1", []byte{'a'}, logger),
				},
				"test-ns/secret2": {
					"cluster2": NewCluster("cluster2", []byte{'a'}, logger),
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "secret1",
				},
				Data: map[string][]byte{
					"cluster1": {'c'},
				},
			},
			expectedState: map[string]map[string]DynamicCluster{
				"test-ns/secret2": {
					"cluster2": NewCluster("cluster2", []byte{'a'}, logger),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := &dynamicDiscovery{
				logger:   logger,
				lock:     sync.RWMutex{},
				clusters: map[string]map[string]DynamicCluster{},
			}
			manager.clusters = tc.currentState

			manager.removeClustersFromSecret(tc.secret)

			assert.Equal(t, tc.expectedState, manager.clusters)
		})
	}
}

func TestGetAllClusters(t *testing.T) {
	primaryClusterName := "primary"

	testCases := []struct {
		name             string
		clusters         map[string]map[string]DynamicCluster
		expectedClusters []string
	}{
		{
			name:             "Primary only",
			clusters:         map[string]map[string]DynamicCluster{},
			expectedClusters: []string{primaryClusterName},
		},
		{
			name: "Primary and remote",
			clusters: map[string]map[string]DynamicCluster{
				"secret-1": {
					"remote-1": NewTestCluster("remote-1", nil),
				},
			},
			expectedClusters: []string{primaryClusterName, "remote-1"},
		},
		{
			name: "Primary and remotes in same secret",
			clusters: map[string]map[string]DynamicCluster{
				"secret-1": {
					"remote-1": NewTestCluster("remote-1", nil),
					"remote-2": NewTestCluster("remote-2", nil),
				},
			},
			expectedClusters: []string{primaryClusterName, "remote-1", "remote-2"},
		},
		{
			name: "Primary and remotes in different secrets",
			clusters: map[string]map[string]DynamicCluster{
				"secret-1": {
					"remote-1": NewTestCluster("remote-1", nil),
				},
				"secret-2": {
					"remote-2": NewTestCluster("remote-2", nil),
				},
			},
			expectedClusters: []string{primaryClusterName, "remote-1", "remote-2"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := dynamicDiscovery{
				primaryCluster: NewPrimaryCluster(primaryClusterName, nil),
				clusters:       tc.clusters,
			}

			clusters := manager.GetClusters()

			var actualClusterIds []string
			for _, cluster := range clusters {
				actualClusterIds = append(actualClusterIds, cluster.GetName())
			}

			assert.ElementsMatch(t, tc.expectedClusters, actualClusterIds)
		})
	}
}

func NewTestCluster(id string, client Client) DynamicCluster {
	return &clusterImpl{
		name:   id,
		client: client,
		lock:   sync.RWMutex{},
	}
}
