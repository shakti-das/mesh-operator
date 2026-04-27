package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/util/workqueue"
)

// Work-Queue metrics and tags
const (
	QueueNameTag = "name"

	QueueDepth                          = "queue_depth"
	QueueAdds                           = "queue_adds"
	QueueLatencySeconds                 = "queue_latency_seconds"
	QueueDurationSeconds                = "queue_duration_seconds"
	QueueUnfinishedWorkSeconds          = "queue_unfinished_work_seconds"
	QueueLongestRunningProcessorSeconds = "queue_longest_running_processor_seconds"
	QueueRetries                        = "queue_retries"
)

var (
	metricsProviderInstance *prometheusQueueMetricsProvider
	metricsProviderOnce     sync.Once
)

// NewMetricsProvider creates and returns a singleton instance of prometheusQueueMetricsProvider
// Only the first call will create the instance, subsequent calls return the same instance
func NewMetricsProvider(registry *prometheus.Registry) *prometheusQueueMetricsProvider {
	metricsProviderOnce.Do(func() {
		metricsProviderInstance = &prometheusQueueMetricsProvider{
			registry: registry,
		}
	})
	return metricsProviderInstance
}

// GetMetricsProvider returns the existing singleton instance of prometheusQueueMetricsProvider
// Returns nil if NewMetricsProvider has not been called yet or if the instance is invalid
func GetMetricsProvider() *prometheusQueueMetricsProvider {
	return metricsProviderInstance
}

type prometheusQueueMetricsProvider struct {
	registry *prometheus.Registry
}

func (p *prometheusQueueMetricsProvider) NewDepthMetric(name string) workqueue.GaugeMetric {
	return GetOrRegisterGaugeWithLabels(QueueDepth, p.getLabelsForQueue(name), p.registry)
}

func (p *prometheusQueueMetricsProvider) NewAddsMetric(name string) workqueue.CounterMetric {
	return GetOrRegisterCounterWithLabels(QueueAdds, p.getLabelsForQueue(name), p.registry)
}

func (p *prometheusQueueMetricsProvider) NewLatencyMetric(name string) workqueue.HistogramMetric {
	latencyMetric := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Name:        QueueLatencySeconds,
		Help:        "How long a queue item stays in the queue (in seconds)",
		ConstLabels: p.getLabelsForQueue(name),
		Objectives:  map[float64]float64{0.25: 0.1, 0.5: 0.1, 0.95: 0.1, 0.99: 0.1, 1.0: 0.1},
	}, []string{})

	_ = p.registry.Register(latencyMetric)
	return latencyMetric.With(map[string]string{})
}

func (p *prometheusQueueMetricsProvider) NewWorkDurationMetric(name string) workqueue.HistogramMetric {
	durationMetric := prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Name:        QueueDurationSeconds,
		Help:        "How long it takes to process a queue item (in seconds).",
		ConstLabels: p.getLabelsForQueue(name),
		Objectives:  map[float64]float64{0.25: 0.1, 0.5: 0.1, 0.95: 0.1, 0.99: 0.1, 1.0: 0.1},
	}, []string{})

	_ = p.registry.Register(durationMetric)
	return durationMetric.With(map[string]string{})
}

func (p *prometheusQueueMetricsProvider) NewUnfinishedWorkSecondsMetric(name string) workqueue.SettableGaugeMetric {
	unfinishedMetric := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: QueueUnfinishedWorkSeconds,
		Help: "How many seconds of work has been done that " +
			"is in progress and hasn't been observed by work_duration. Large " +
			"values indicate stuck threads. One can deduce the number of stuck " +
			"threads by observing the rate at which this increases.",
	}, []string{"name"})
	_ = p.registry.Register(unfinishedMetric)
	return unfinishedMetric.WithLabelValues(name)
}

func (p *prometheusQueueMetricsProvider) NewLongestRunningProcessorSecondsMetric(name string) workqueue.SettableGaugeMetric {
	longestRunningProcessorMetric := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: QueueLongestRunningProcessorSeconds,
		Help: "How many seconds has the longest running " +
			"processor for workqueue been running.",
	}, []string{"name"})
	_ = p.registry.Register(longestRunningProcessorMetric)
	return longestRunningProcessorMetric.WithLabelValues(name)
}

func (p *prometheusQueueMetricsProvider) NewRetriesMetric(name string) workqueue.CounterMetric {
	return GetOrRegisterCounterWithLabels(QueueRetries, p.getLabelsForQueue(name), p.registry)
}

func (p *prometheusQueueMetricsProvider) getLabelsForQueue(name string) map[string]string {
	return map[string]string{
		QueueNameTag: name,
	}
}
