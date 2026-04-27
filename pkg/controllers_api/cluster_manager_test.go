package controllers_api

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/tools/cache"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap/zaptest"
	"k8s.io/client-go/tools/record"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/cluster"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
)

// MockCluster is a mock implementation of the Cluster interface for testing
type MockCluster struct {
	mock.Mock
	id        cluster.ID
	isPrimary bool
}

func (m *MockCluster) GetId() cluster.ID {
	return m.id
}

func (m *MockCluster) GetKubeClient() kube.Client {
	args := m.Called()
	return args.Get(0).(kube.Client)
}

func (m *MockCluster) IsPrimary() bool {
	return m.isPrimary
}

func (m *MockCluster) Run(manager ClusterManager) error {
	args := m.Called(manager)
	return args.Error(0)
}

func (m *MockCluster) Stop() {
	m.Called()
}

func (m *MockCluster) GetEventRecorder() record.EventRecorder {
	args := m.Called()
	return args.Get(0).(record.EventRecorder)
}

func (m *MockCluster) GetMopEnqueuer() ObjectEnqueuer {
	args := m.Called()
	return args.Get(0).(ObjectEnqueuer)
}

func (m *MockCluster) GetMulticlusterServiceController() MulticlusterController {
	args := m.Called()
	return args.Get(0).(MulticlusterController)
}

func (m *MockCluster) GetNamespaceFilter() NamespaceFilter {
	args := m.Called()
	return args.Get(0).(NamespaceFilter)
}

func TestGetExistingHealthyClusters(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name                    string
		primaryCluster          *MockCluster
		remoteClusters          map[string]map[cluster.ID]*MockCluster
		unhealthyClusters       map[cluster.ID]interface{}
		expectedHealthyClusters []cluster.ID
	}{
		{
			name:                    "Only primary cluster",
			primaryCluster:          &MockCluster{id: "primary", isPrimary: true},
			remoteClusters:          map[string]map[cluster.ID]*MockCluster{},
			unhealthyClusters:       map[cluster.ID]interface{}{},
			expectedHealthyClusters: []cluster.ID{"primary"},
		},
		{
			name:           "All clusters healthy",
			primaryCluster: &MockCluster{id: "primary", isPrimary: true},
			remoteClusters: map[string]map[cluster.ID]*MockCluster{
				"secret1": {
					"cluster1": &MockCluster{id: "cluster1", isPrimary: false},
					"cluster2": &MockCluster{id: "cluster2", isPrimary: false},
				},
				"secret2": {
					"cluster3": &MockCluster{id: "cluster3", isPrimary: false},
				},
			},
			unhealthyClusters:       map[cluster.ID]interface{}{},
			expectedHealthyClusters: []cluster.ID{"primary", "cluster1", "cluster2", "cluster3"},
		},
		{
			name:           "Some clusters unhealthy",
			primaryCluster: &MockCluster{id: "primary", isPrimary: true},
			remoteClusters: map[string]map[cluster.ID]*MockCluster{
				"secret1": {
					"cluster1": &MockCluster{id: "cluster1", isPrimary: false},
					"cluster2": &MockCluster{id: "cluster2", isPrimary: false},
				},
				"secret2": {
					"cluster3": &MockCluster{id: "cluster3", isPrimary: false},
				},
			},
			unhealthyClusters: map[cluster.ID]interface{}{
				"cluster1": true,
				"cluster3": true,
			},
			expectedHealthyClusters: []cluster.ID{"primary", "cluster2"},
		},
		{
			name:           "All remote clusters unhealthy",
			primaryCluster: &MockCluster{id: "primary", isPrimary: true},
			remoteClusters: map[string]map[cluster.ID]*MockCluster{
				"secret1": {
					"cluster1": &MockCluster{id: "cluster1", isPrimary: false},
					"cluster2": &MockCluster{id: "cluster2", isPrimary: false},
				},
			},
			unhealthyClusters: map[cluster.ID]interface{}{
				"cluster1": true,
				"cluster2": true,
			},
			expectedHealthyClusters: []cluster.ID{"primary"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new ClusterStore with the test data
			store := &ClusterStore{
				logger:            logger,
				primaryCluster:    tc.primaryCluster,
				remoteClusters:    make(map[string]map[cluster.ID]Cluster),
				unhealthyClusters: tc.unhealthyClusters,
			}

			// Convert mock clusters to Cluster interface
			for secretKey, clusters := range tc.remoteClusters {
				store.remoteClusters[secretKey] = make(map[cluster.ID]Cluster)
				for id, cls := range clusters {
					store.remoteClusters[secretKey][id] = cls
				}
			}

			healthyClusters := store.GetExistingHealthyClusters()

			// primary cluster must be always included
			assert.Contains(t, healthyClusters, tc.primaryCluster)

			actualHealthy := []cluster.ID{}
			for _, cls := range healthyClusters {
				actualHealthy = append(actualHealthy, cls.GetId())
			}

			assert.ElementsMatch(t, tc.expectedHealthyClusters, actualHealthy)
		})
	}
}

func TestIsClusterHealthy(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name              string
		clusterId         cluster.ID
		unhealthyClusters map[cluster.ID]interface{}
		expected          bool
	}{
		{
			name:              "Cluster is healthy (not in unhealthy map)",
			clusterId:         "cluster1",
			unhealthyClusters: map[cluster.ID]interface{}{},
			expected:          true,
		},
		{
			name:      "Cluster is unhealthy",
			clusterId: "cluster1",
			unhealthyClusters: map[cluster.ID]interface{}{
				"cluster1": true,
			},
			expected: false,
		},
		{
			name:      "Different cluster is unhealthy",
			clusterId: "cluster1",
			unhealthyClusters: map[cluster.ID]interface{}{
				"cluster2": true,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new ClusterStore with the test data
			store := &ClusterStore{
				logger:            logger,
				unhealthyClusters: tt.unhealthyClusters,
			}

			result := store.IsClusterHealthy(tt.clusterId)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTestRemoteClustersOrRetry(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	// Create a test stop channel
	stopCh := make(chan struct{})
	defer close(stopCh)

	// Create test clusters
	cluster1 := &MockCluster{id: "cluster1", isPrimary: false}
	cluster2 := &MockCluster{id: "cluster2", isPrimary: false}
	cluster3 := &MockCluster{id: "cluster3", isPrimary: false}

	// Test cases
	tests := []struct {
		name            string
		remoteClusters  map[string]map[cluster.ID]Cluster
		testFunc        func(Cluster, <-chan struct{}, time.Duration) error
		expectedHealthy map[cluster.ID]bool
	}{
		{
			name: "All clusters pass test",
			remoteClusters: map[string]map[cluster.ID]Cluster{
				"secret1": {
					"cluster1": cluster1,
					"cluster2": cluster2,
				},
				"secret2": {
					"cluster3": cluster3,
				},
			},
			testFunc: func(c Cluster, stopCh <-chan struct{}, timeout time.Duration) error {
				// All tests pass
				return nil
			},
			expectedHealthy: map[cluster.ID]bool{
				"cluster1": true,
				"cluster2": true,
				"cluster3": true,
			},
		},
		{
			name: "Some clusters fail test",
			remoteClusters: map[string]map[cluster.ID]Cluster{
				"secret1": {
					"cluster1": cluster1,
					"cluster2": cluster2,
				},
				"secret2": {
					"cluster3": cluster3,
				},
			},
			testFunc: func(c Cluster, stopCh <-chan struct{}, timeout time.Duration) error {
				// Cluster2 fails
				if c.GetId() == "cluster2" {
					return assert.AnError
				}
				return nil
			},
			expectedHealthy: map[cluster.ID]bool{
				"cluster1": true,
				"cluster2": false,
				"cluster3": true,
			},
		},
		{
			name: "All clusters fail test",
			remoteClusters: map[string]map[cluster.ID]Cluster{
				"secret1": {
					"cluster1": cluster1,
					"cluster2": cluster2,
				},
			},
			testFunc: func(c Cluster, stopCh <-chan struct{}, timeout time.Duration) error {
				// All tests fail
				return assert.AnError
			},
			expectedHealthy: map[cluster.ID]bool{
				"cluster1": false,
				"cluster2": false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new ClusterStore with the test data
			store := &ClusterStore{
				logger:             logger,
				remoteClusters:     tt.remoteClusters,
				clusterTestingFunc: tt.testFunc,
				unhealthyClusters:  make(map[cluster.ID]interface{}),
			}

			store.TestRemoteClustersOrRetry(stopCh)

			for id, expectedHealthy := range tt.expectedHealthy {
				assert.Equal(t, expectedHealthy, store.IsClusterHealthy(id))
			}
		})
	}
}

func TestRemoteClusterRetry(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	features.EnableClusterSyncRetry = true
	defer func() {
		features.EnableClusterSyncRetry = false
	}()

	cluster1 := &MockCluster{id: "cluster1", isPrimary: false}
	cluster2 := &MockCluster{id: "cluster2", isPrimary: false}

	testCounter := atomic.Int32{}
	// Cluster testing function that:
	// * always fails for cluster2
	// * for cluster1 fails the check on the 1st attempt and succeeds on the subsequent
	clusterTestingFunc := func(c Cluster, i <-chan struct{}, duration time.Duration) error {
		if c.GetId().String() == "cluster2" {
			return fmt.Errorf("cluster2 error")
		}
		result := testCounter.Add(1)
		if result <= 1 {
			return fmt.Errorf("cluster1 initial error")
		}
		return nil
	}

	g, gCtx := errgroup.WithContext(ctx)
	store := &ClusterStore{
		logger:                    logger,
		remoteClusters:            map[string]map[cluster.ID]Cluster{"secret1": {"cluster1": cluster1, "cluster2": cluster2}},
		clusterTestingFunc:        clusterTestingFunc,
		unhealthyClusters:         make(map[cluster.ID]interface{}),
		clusterTestingTimeout:     1 * time.Millisecond,
		clusterTestingRetryPeriod: 1 * time.Millisecond,
		retrySpawn:                func(loop func()) { g.Go(func() error { loop(); return nil }) },
	}

	// Start retries
	store.TestRemoteClustersOrRetry(gCtx.Done())

	// Wait for two iterations
	cache.WaitForCacheSync(ctx.Done(), func() bool {
		return testCounter.Load() >= 2
	})

	assert.True(t, store.IsClusterHealthy("cluster1"))
	assert.False(t, store.IsClusterHealthy("cluster2"))

	// Cancel so retry goroutines exit
	// wait for them via errgroup so they don't log after test teardown (data race with zaptest)
	cancel()
	_ = g.Wait()
}

func TestMarkClusterHealthyUnhealthy(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	// Create a new ClusterStore
	store := &ClusterStore{
		logger:            logger,
		unhealthyClusters: make(map[cluster.ID]interface{}),
	}

	// Test marking a cluster as unhealthy
	store.markClusterUnhealthy("cluster1")
	assert.False(t, store.IsClusterHealthy("cluster1"), "Cluster should be unhealthy after markClusterUnhealthy")

	// Test marking a cluster as healthy
	store.markClusterHealthy("cluster1")
	assert.True(t, store.IsClusterHealthy("cluster1"), "Cluster should be healthy after markClusterHealthy")

	// Test marking multiple clusters
	store.markClusterUnhealthy("cluster1")
	store.markClusterUnhealthy("cluster2")
	assert.False(t, store.IsClusterHealthy("cluster1"), "Cluster1 should be unhealthy")
	assert.False(t, store.IsClusterHealthy("cluster2"), "Cluster2 should be unhealthy")

	store.markClusterHealthy("cluster1")
	assert.True(t, store.IsClusterHealthy("cluster1"), "Cluster1 should be healthy")
	assert.False(t, store.IsClusterHealthy("cluster2"), "Cluster2 should still be unhealthy")
}

// runConcurrently runs the given function in multiple goroutines concurrently,
func runConcurrently(wg *sync.WaitGroup, stopCh <-chan struct{}, threads int, fn func(id int)) {
	for i := 0; i < threads; i++ {
		go func(id int) {
			wg.Add(1)
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					fn(id)
					time.Sleep(1 * time.Millisecond)
				}
			}
		}(i)
	}
}

// TestClusterStoreRaceConditions exercises concurrent reads and writes to ClusterStore
// to detect race conditions. This test will fail with -race flag if locks are removed.
func TestClusterStoreRaceConditions(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	// Create a ClusterStore
	store := NewClustersStore(
		logger,
		func(c Cluster, stopCh <-chan struct{}, timeout time.Duration) error {
			return nil
		},
		1*time.Second,
		1*time.Second,
	).(*ClusterStore)

	// Create primary cluster
	primaryCluster := &MockCluster{id: "primary", isPrimary: true}
	store.SetPrimaryCluster(primaryCluster)

	// Number of goroutines for each operation type
	const numWorkers = 10
	const duration = 2 * time.Second

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// Writer: AddClusterForKey
	runConcurrently(&wg, stopCh, numWorkers, func(id int) {
		secretKey := fmt.Sprintf("secret%d", id%5)
		clusterID := cluster.ID(fmt.Sprintf("cluster%d", id))
		cls := &MockCluster{id: clusterID, isPrimary: false}
		cls.On("Stop").Return()
		store.AddClusterForKey(secretKey, cls)
	})

	// Writer goroutines: DeleteClusterForKey
	runConcurrently(&wg, stopCh, numWorkers, func(id int) {
		secretKey := fmt.Sprintf("secret%d", id%5)
		clusterID := cluster.ID(fmt.Sprintf("cluster%d", id))
		store.DeleteClusterForKey(secretKey, clusterID)
	})

	// Writer: markClusterUnhealthy / markClusterHealthy
	runConcurrently(&wg, stopCh, numWorkers, func(id int) {
		clusterID := cluster.ID(fmt.Sprintf("cluster%d", id))
		store.markClusterUnhealthy(clusterID)
		time.Sleep(1 * time.Millisecond)
		store.markClusterHealthy(clusterID)
		time.Sleep(1 * time.Millisecond)
	})

	// Reader: GetExistingClusters
	runConcurrently(&wg, stopCh, numWorkers, func(_ int) {
		_ = store.GetExistingClusters()
	})

	// Reader: GetExistingHealthyClusters
	runConcurrently(&wg, stopCh, numWorkers, func(_ int) {
		_ = store.GetExistingHealthyClusters()
	})

	// Reader: GetClusterById
	runConcurrently(&wg, stopCh, numWorkers, func(id int) {
		clusterID := fmt.Sprintf("cluster%d", id%20)
		_, _ = store.GetClusterById(clusterID)
	})

	// Reader: GetExistingClustersForKey
	runConcurrently(&wg, stopCh, numWorkers, func(id int) {
		secretKey := fmt.Sprintf("secret%d", id%5)
		_ = store.GetExistingClustersForKey(secretKey)
	})

	// Reader: IsClusterHealthy
	runConcurrently(&wg, stopCh, numWorkers, func(id int) {
		clusterID := cluster.ID(fmt.Sprintf("cluster%d", id%20))
		_ = store.IsClusterHealthy(clusterID)
	})

	// Reader: IsClusterExistsForOtherKey
	runConcurrently(&wg, stopCh, numWorkers, func(id int) {
		secretKey := fmt.Sprintf("secret%d", id%5)
		clusterID := cluster.ID(fmt.Sprintf("cluster%d", id%20))
		_, _ = store.IsClusterExistsForOtherKey(secretKey, clusterID)
	})

	// Reader: GetRemoteClusters
	runConcurrently(&wg, stopCh, numWorkers, func(_ int) {
		_ = store.GetRemoteClusters()
	})

	// Run for specified duration
	time.Sleep(duration)
	close(stopCh)

	// Wait for all goroutines to finish
	wg.Wait()

	// Final sanity check - verify store is still in a valid state
	clusters := store.GetExistingClusters()
	assert.NotNil(t, clusters)
}
