package metrics

import (
	"context"
	"net/url"
	"regexp"
	"time"

	"go.uber.org/zap"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/tools/metrics"
)

const (
	K8sClientLatencyMetric = "k8s_client_latency"

	UrlLabel  = "url"
	VerbLabel = "verb"
)

var (
	pathGlobalListKindRegExp = regexp.MustCompile("/(api|apis)/.*/(?P<kind>.*)")
	pathListKindRegExp       = regexp.MustCompile("/(api|apis)/.*/namespaces/.*/(?P<kind>.*)")
	pathKindRegExp           = regexp.MustCompile("/(api|apis)/.*/.*/namespaces/.*/(?P<kind>.*)/.*")
	pathBaseKindRegExp       = regexp.MustCompile("/(api|apis)/.*/namespaces/.*/(?P<kind>.*)/.*")
	pathNamespacesRegExp     = regexp.MustCompile("/(api|apis)/v1/namespaces/[a-z0-9-]*$")
	pathKindStatusRegExp     = regexp.MustCompile("/(api|apis)/.*/.*/namespaces/.*/(?P<kind>.*)/.*/status")
)

func RegisterK8sClientMetrics(logger *zap.SugaredLogger, registry *prometheus.Registry) {
	latencyAdapter := newLatencyMetric(logger, registry)

	metrics.Register(metrics.RegisterOpts{
		RequestLatency: latencyAdapter,
	})

	// WORKAROUND for sync.Once race condition (see PR #20206):
	metrics.RequestLatency = latencyAdapter
}

func newLatencyMetric(logger *zap.SugaredLogger, registry *prometheus.Registry) metrics.LatencyMetric {
	latencyMetric := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Name:       K8sClientLatencyMetric,
		Help:       "Latency of the k8s client calls",
		Objectives: map[float64]float64{0.25: 0.1, 0.5: 0.1, 0.95: 0.1, 0.99: 0.1, 1.0: 0.1},
	}, []string{UrlLabel, VerbLabel})

	if err := registry.Register(latencyMetric); err != nil {
		if logger != nil {
			logger.Warnf("Failed to register k8s_client_latency metric with prometheus (may already be registered): %v", err)
		}
	} else {
		if logger != nil {
			logger.Infof("Successfully registered k8s_client_latency metric with prometheus")
		}
	}

	return &latencyMetricObserver{
		latencyMetric: latencyMetric,
		registry:      registry,
		logger:        logger,
	}
}

type latencyMetricObserver struct {
	latencyMetric *prometheus.SummaryVec
	registry      *prometheus.Registry
	logger        *zap.SugaredLogger
}

func (o *latencyMetricObserver) Observe(_ context.Context, verb string, u url.URL, latency time.Duration) {
	object := urlOrObject(u.Path)
	labels := map[string]string{
		UrlLabel:  object,
		VerbLabel: verb,
	}

	if o.logger != nil {
		o.logger.Debugf("K8s API request. %s:%s %f", verb, u.Path, latency.Seconds())
	}
	o.latencyMetric.With(labels).Observe(latency.Seconds())
}

// * urlOrObject - try parsing out object type name from the URL path, if not possible, return the whole path
func urlOrObject(path string) string {
	if pathNamespacesRegExp.MatchString(path) {
		return "namespaces"
	}
	if pathKindStatusRegExp.MatchString(path) {
		return "status_" + pathKindStatusRegExp.FindStringSubmatch(path)[2]
	}
	if pathKindRegExp.MatchString(path) {
		return pathKindRegExp.FindStringSubmatch(path)[2]
	}
	if pathBaseKindRegExp.MatchString(path) {
		return pathBaseKindRegExp.FindStringSubmatch(path)[2]
	}
	if pathListKindRegExp.MatchString(path) {
		return "list_" + pathListKindRegExp.FindStringSubmatch(path)[2]
	}
	if pathGlobalListKindRegExp.MatchString(path) {
		return "list-all_" + pathGlobalListKindRegExp.FindStringSubmatch(path)[2]
	}

	return path
}
