package controllers

import (
	"io"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"
)

type watchErrorHandlerWithMetrics struct {
	cluster string
	// watchName - a human-readable name of the watch/informer to aid debugging
	watchName       string
	logger          *zap.SugaredLogger
	metricsRegistry *prometheus.Registry
}

func NewWatchErrorHandlerWithMetrics(
	logger *zap.SugaredLogger,
	cluster string,
	watchName string,
	registry *prometheus.Registry) cache.WatchErrorHandler {

	ctxLogger := logger.With("cluster", cluster)
	handler := &watchErrorHandlerWithMetrics{
		cluster:         cluster,
		watchName:       watchName,
		metricsRegistry: registry,
		logger:          ctxLogger,
	}
	return handler.handleError
}

/*
handleError - implementation is inspired by the DefaultWatchErrorHandler
It preserves the original error handler behavior.
Additionally, a metric is raised on an unknown watch error or unexpected end-of-file.
*/
func (h *watchErrorHandlerWithMetrics) handleError(r *cache.Reflector, err error) {
	// Invoke default error handler to preserve the original functionality
	cache.DefaultWatchErrorHandler(r, err)

	if apierrors.IsResourceExpired(err) || err == io.EOF {
		// Normal operations
		return
	}

	if err == io.ErrUnexpectedEOF {
		h.logger.Errorf("Watch %s closed with unexpected EOF: %v", h.watchName, err)
	} else {
		h.logger.Errorf("Failed to watch %s: %v", h.watchName, err)
	}

	labels := map[string]string{
		metrics.ReasonTag: h.watchName,
	}

	metrics.GetOrRegisterCounterWithLabels(metrics.WatchErrorsMetric, labels, h.metricsRegistry).Inc()
}
