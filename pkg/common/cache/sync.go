package cache

import (
	"context"
	"fmt"
	"time"

	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"k8s.io/client-go/tools/cache"
)

const (
	INFORMER_CACHE_SYNC_TIMEOUT = "timeout"
	INFORMER_FAILED_TO_WAIT     = "failed_to_wait"
)

func WaitForInformerCacheSync(informer cache.SharedIndexInformer, informerName string, timeout time.Duration, clusterName string, stopCh <-chan struct{}, logger *zap.SugaredLogger, metricsRegistry *prometheus.Registry) error {
	// Wait for informer to sync
	logger.Debugf("waiting for %s informer cache sync in cluster: %s...", informerName, clusterName)

	if cacheSyncWaitErr := waitForCacheSyncWithTimeout(informer, informerName, timeout, stopCh, clusterName, metricsRegistry); cacheSyncWaitErr != nil {
		return cacheSyncWaitErr
	}
	logger.Debugf("debug: cache sync completed for %s informer", informerName)
	return nil
}

func waitForCacheSyncWithTimeout(informer cache.SharedIndexInformer, name string, timeout time.Duration, stopCh <-chan struct{}, clusterName string, metricsRegistry *prometheus.Registry) error {

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan bool)
	go func() {
		done <- cache.WaitForNamedCacheSync(name, stopCh, informer.HasSynced)
	}()

	select {
	case <-ctx.Done():
		emitStatsForCacheSyncFailure(metricsRegistry, name, clusterName, INFORMER_CACHE_SYNC_TIMEOUT)
		return fmt.Errorf("timed out waiting for cache sync: %s", name)
	case ok := <-done:
		if !ok {
			emitStatsForCacheSyncFailure(metricsRegistry, name, clusterName, INFORMER_FAILED_TO_WAIT)
			return fmt.Errorf("failed to wait for informer cache sync: %s", name)
		}
		return nil
	}
}

func emitStatsForCacheSyncFailure(metricsRegistry *prometheus.Registry, informerName string, clusterName string, failureType string) {
	labels := map[string]string{
		commonmetrics.InformerName:                 informerName,
		commonmetrics.ClusterLabel:                 clusterName,
		commonmetrics.InformerCacheSyncFailureType: failureType,
	}
	commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.InformerCacheSyncFailureMetric, labels, metricsRegistry).Inc()
}
