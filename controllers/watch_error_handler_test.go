package controllers

import (
	"io"
	"testing"
	"time"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zaptest"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

func TestHandleError(t *testing.T) {
	testCases := []struct {
		name                string
		watchError          error
		expectedMetricValue float64
	}{
		{
			name:                "Watch expired error",
			watchError:          &errors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonExpired}},
			expectedMetricValue: 0,
		},
		{
			name:                "Watch EOF",
			watchError:          io.EOF,
			expectedMetricValue: 0,
		},
		{
			name:                "Watch unexpected EOF",
			watchError:          io.ErrUnexpectedEOF,
			expectedMetricValue: 1,
		},
		{
			name:                "Watch unexpected error",
			expectedMetricValue: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			watchName := "test-watch"
			metricsRegistry := prometheus.NewRegistry()
			logger := zaptest.NewLogger(t).Sugar()
			handler := NewWatchErrorHandlerWithMetrics(logger, "test-cluster", watchName, metricsRegistry)
			reflector := cache.NewNamedReflector(watchName, nil, nil, nil, time.Hour)

			handler(reflector, tc.watchError)

			labels := map[string]string{
				metrics.ReasonTag: watchName,
			}
			assertCounterWithLabels(t, metricsRegistry, metrics.WatchErrorsMetric, labels, tc.expectedMetricValue)
		})
	}
}
