package cache

import (
	"fmt"
	"testing"
	"time"

	metricstesting "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/common/pkg/metrics/testing"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/tools/cache"
)

// MockInformer mocks cache.SharedIndexInformer
type MockInformer struct {
	hasSyncedFunc func() bool
}

func (m *MockInformer) HasSynced() bool {
	return m.hasSyncedFunc()
}

func (m *MockInformer) AddEventHandler(_ cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}

func (m *MockInformer) AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, resyncPeriod time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}

func (m *MockInformer) GetStore() cache.Store {
	return nil
}

func (m *MockInformer) GetController() cache.Controller {
	return nil
}

func (m *MockInformer) Run(_ <-chan struct{}) {
}

func (m *MockInformer) LastSyncResourceVersion() string {
	return ""
}

func (m *MockInformer) SetWatchErrorHandler(_ cache.WatchErrorHandler) error {
	return nil
}

func (m *MockInformer) AddIndexers(_ cache.Indexers) error {
	return nil
}

func (m *MockInformer) GetIndexer() cache.Indexer {
	return nil
}

func (m *MockInformer) IsStopped() bool { return false }

func (m *MockInformer) RemoveEventHandler(_ cache.ResourceEventHandlerRegistration) error { return nil }

func (m *MockInformer) SetTransform(_ cache.TransformFunc) error { return nil }

func TestWaitForCacheSyncWithTimeout(t *testing.T) {
	clusterName := "cluster2"
	informerName := "dummyInformer"

	tests := []struct {
		name              string
		informer          *MockInformer
		timeout           time.Duration
		expectedErr       string
		expectedErrorType string
	}{
		{
			name: "Successful Sync",
			informer: &MockInformer{
				hasSyncedFunc: func() bool { return true },
			},
			timeout:     4 * time.Second,
			expectedErr: "",
		},
		{
			name: "Delayed Successful Sync",
			informer: &MockInformer{
				hasSyncedFunc: func() bool {
					time.Sleep(1 * time.Second)
					return true
				},
			},
			timeout:     4 * time.Second,
			expectedErr: "",
		},
		{
			name: "Timeout",
			informer: &MockInformer{
				hasSyncedFunc: func() bool { select {}; return false },
			},
			timeout:           1 * time.Second,
			expectedErr:       fmt.Sprintf("timed out waiting for cache sync: %s", informerName),
			expectedErrorType: INFORMER_CACHE_SYNC_TIMEOUT,
		},
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			err := waitForCacheSyncWithTimeout(tc.informer, informerName, tc.timeout, stopCh, clusterName, registry)

			// assert metrics
			if tc.expectedErrorType != "" {
				labels := map[string]string{
					metrics.ClusterLabel:                 clusterName,
					metrics.InformerName:                 informerName,
					metrics.InformerCacheSyncFailureType: INFORMER_CACHE_SYNC_TIMEOUT,
				}
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, metrics.InformerCacheSyncFailureMetric, labels, 1)
			}

			if (err != nil && err.Error() != tc.expectedErr) || (err == nil && tc.expectedErr != "") {
				t.Errorf("expected error %v, got %v", tc.expectedErr, err)
			}
		})
	}
}
