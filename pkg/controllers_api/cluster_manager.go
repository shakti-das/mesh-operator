package controllers_api

import (
	"sync"
	"time"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"k8s.io/client-go/tools/record"

	"github.com/istio-ecosystem/mesh-operator/pkg/cluster"

	"go.uber.org/zap"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
)

type Cluster interface {
	GetId() cluster.ID
	GetKubeClient() kube.Client
	IsPrimary() bool
	Run(manager ClusterManager) error
	Stop()

	GetEventRecorder() record.EventRecorder
	GetMopEnqueuer() ObjectEnqueuer
	GetMulticlusterServiceController() MulticlusterController
	GetNamespaceFilter() NamespaceFilter
}

type ClusterManager interface {
	GetClusterById(clusterId string) (bool, Cluster)
	// GetExistingClusters - returns all known clusters, including primary. Cluster health is not accounted for.
	GetExistingClusters() []Cluster
	// GetExistingHealthyClusters - returns only healthy remote clusters and primary.
	GetExistingHealthyClusters() []Cluster

	GetExistingClustersForKey(secretKey string) []Cluster
	// IsClusterExistsForOtherKey - checks whether there's a cluster with the same name BUT for a different key
	IsClusterExistsForOtherKey(secretKey string, clusterId cluster.ID) (string, bool)
	AddClusterForKey(secretKey string, value Cluster)
	SetPrimaryCluster(cluster Cluster)
	DeleteClustersForKey(secretKey string)
	DeleteClusterForKey(secretKey string, clusterId cluster.ID)
	StopAllClusters()

	// TestRemoteClustersOrRetry - apply a testing function to every remote cluster.
	// If testing function fails with an error or times out, cluster is marked as unhealthy.
	// Test will be infinitely retried with a configured interval until it succeeds. In this case, cluster will be marked as healthy.
	// Testing function can be blocking, but it must honor and stop on a message from the stop channel.
	TestRemoteClustersOrRetry(<-chan struct{})
	IsClusterHealthy(id cluster.ID) bool
}

// ClusterStore is a collection of clusters
type ClusterStore struct {
	logger *zap.SugaredLogger

	sync.RWMutex
	// keyed by secret key(ns/name)->clusterID
	remoteClusters map[string]map[cluster.ID]Cluster

	primaryClusterId cluster.ID
	primaryCluster   Cluster

	clusterTestingFunc        func(Cluster, <-chan struct{}, time.Duration) error
	clusterTestingTimeout     time.Duration
	clusterTestingRetryPeriod time.Duration
	unhealthyClusters         map[cluster.ID]interface{}

	// retrySpawn runs the retry loop in a goroutine. Default is go loop(); tests may override (e.g. with errgroup) to wait for retries to drain.
	retrySpawn func(loop func())
}

// newClustersStore initializes data struct to store clusters information
func NewClustersStore(
	logger *zap.SugaredLogger,
	clusterTestingFunc func(Cluster, <-chan struct{}, time.Duration) error,
	clusterTestingTimeout time.Duration,
	clusterTestingRetryPeriod time.Duration,
) ClusterManager {
	return &ClusterStore{
		logger:                    logger,
		remoteClusters:            make(map[string]map[cluster.ID]Cluster),
		clusterTestingFunc:        clusterTestingFunc,
		clusterTestingTimeout:     clusterTestingTimeout,
		clusterTestingRetryPeriod: clusterTestingRetryPeriod,
		unhealthyClusters:         map[cluster.ID]interface{}{},
		retrySpawn:                func(loop func()) { go loop() },
	}
}

func (c *ClusterStore) SetPrimaryCluster(cluster Cluster) {
	c.Lock()
	defer c.Unlock()

	c.primaryClusterId = cluster.GetId()
	c.primaryCluster = cluster
}

func (c *ClusterStore) AddClusterForKey(secretKey string, value Cluster) {
	c.Lock()
	defer c.Unlock()
	if _, ok := c.remoteClusters[secretKey]; !ok {
		c.remoteClusters[secretKey] = make(map[cluster.ID]Cluster)
	}
	c.remoteClusters[secretKey][value.GetId()] = value
}

// GetExistingClustersForKey return existing clusters registered for the given secret
func (c *ClusterStore) GetExistingClustersForKey(secretKey string) []Cluster {
	c.RLock()
	defer c.RUnlock()
	out := make([]Cluster, 0, len(c.remoteClusters[secretKey]))
	for _, remoteCluster := range c.remoteClusters[secretKey] {
		out = append(out, remoteCluster)
	}
	return out
}

func (c *ClusterStore) IsClusterExistsForOtherKey(secretKey string, clusterID cluster.ID) (string, bool) {
	c.RLock()
	defer c.RUnlock()
	for key, clusters := range c.remoteClusters {
		if key != secretKey {
			if _, ok := clusters[clusterID]; ok {
				return key, true
			}
		}
	}
	return "", false
}

func (c *ClusterStore) DeleteClustersForKey(secretKey string) {
	c.Lock()
	defer c.Unlock()
	delete(c.remoteClusters, secretKey)
}

func (c *ClusterStore) DeleteClusterForKey(secretKey string, clusterId cluster.ID) {
	c.Lock()
	defer c.Unlock()
	if clusters, ok := c.remoteClusters[secretKey]; ok {
		if cls, ok := clusters[clusterId]; ok {
			c.logger.Infof("Deleting cluster_id=%v configured by secret=%v", clusterId, secretKey)
			cls.Stop()
			delete(c.remoteClusters[secretKey], clusterId)
			delete(c.unhealthyClusters, clusterId)
		}
	}
}

func (c *ClusterStore) GetClusterById(clusterId string) (bool, Cluster) {
	c.RLock()
	defer c.RUnlock()

	if clusterId == c.primaryClusterId.String() {
		return true, c.primaryCluster
	}

	for _, clustersForSecret := range c.remoteClusters {
		for knownClusterId, knownCluster := range clustersForSecret {
			if knownClusterId.String() == clusterId {
				return true, knownCluster
			}
		}
	}
	return false, nil
}

// GetExistingClusters - return a collection of the known clusters
func (c *ClusterStore) GetExistingClusters() []Cluster {
	c.RLock()
	defer c.RUnlock()

	result := []Cluster{c.primaryCluster}
	result = append(result, c.collectRemoteClusters()...)
	return result
}

func (c *ClusterStore) GetExistingHealthyClusters() []Cluster {
	c.RLock()
	defer c.RUnlock()
	result := []Cluster{c.primaryCluster}
	remoteClusters := c.collectRemoteClusters()

	if len(c.unhealthyClusters) == 0 { // shortcut
		result = append(result, remoteClusters...)
	} else {
		for _, cls := range remoteClusters {
			_, foundUnhealthy := c.unhealthyClusters[cls.GetId()]
			if !foundUnhealthy {
				result = append(result, cls)
			}
		}
	}
	return result
}

func (c *ClusterStore) GetRemoteClusters() []Cluster {
	c.RLock()
	defer c.RUnlock()
	return c.collectRemoteClusters()
}

// collectRemoteClusters - non locking. Collect known remote clusters. Locking should be performed by client.
func (c *ClusterStore) collectRemoteClusters() []Cluster {
	result := []Cluster{}
	for _, clusterPerSecret := range c.remoteClusters {
		for _, knownCluster := range clusterPerSecret {
			result = append(result, knownCluster)
		}
	}
	return result
}

func (c *ClusterStore) StopAllClusters() {
	c.Lock()
	defer c.Unlock()
	for _, clusterMap := range c.remoteClusters {
		for _, cls := range clusterMap {
			cls.Stop()
		}
	}
}

func (c *ClusterStore) TestRemoteClustersOrRetry(stopCh <-chan struct{}) {
	clusters := c.GetRemoteClusters()

	for _, cls := range clusters {
		c.testClusterOrRetry(stopCh, cls)
	}
}

func (c *ClusterStore) IsClusterHealthy(id cluster.ID) bool {
	c.RLock()
	defer c.RUnlock()

	_, foundUnhealthy := c.unhealthyClusters[id]
	return !foundUnhealthy
}

func (c *ClusterStore) markClusterUnhealthy(id cluster.ID) {
	c.Lock()
	defer c.Unlock()

	c.unhealthyClusters[id] = true
}

func (c *ClusterStore) markClusterHealthy(id cluster.ID) {
	c.Lock()
	defer c.Unlock()

	delete(c.unhealthyClusters, id)
}

func (c *ClusterStore) testClusterOrRetry(stopCh <-chan struct{}, cls Cluster) {
	// Initial test
	err := c.clusterTestingFunc(cls, stopCh, c.clusterTestingTimeout)
	if err == nil {
		c.markClusterHealthy(cls.GetId())
		return
	}

	c.logger.Errorf("initial cluster validation failed. cluster: %s, %v. Cluster test retries enabled: %s", cls.GetId(), err, features.EnableClusterSyncRetry)
	c.markClusterUnhealthy(cls.GetId())

	// If retries are not enabled, return without setting up the retry loop
	if !features.EnableClusterSyncRetry {
		return
	}

	// Retry loop in a goroutine
	c.retrySpawn(func() { c.runClusterRetryLoop(stopCh, cls) })
}

// runClusterRetryLoop runs the retry loop for a cluster until it becomes healthy or stopCh is closed.
func (c *ClusterStore) runClusterRetryLoop(stopCh <-chan struct{}, cls Cluster) {
	for {
		select {
		case <-time.After(c.clusterTestingRetryPeriod):
			err := c.clusterTestingFunc(cls, stopCh, c.clusterTestingTimeout)
			if err == nil {
				c.markClusterHealthy(cls.GetId())
				return // Exit the loop when successful
			}
			c.logger.Errorf("failed to validate cluster: %s, %v. Will retry", cls.GetId(), err)
			c.markClusterUnhealthy(cls.GetId())
			// Go on and retry
		case <-stopCh:
			// Server shutting down
			return
		}
	}
}
