package metrics

import (
	"fmt"
	"strings"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	"github.com/prometheus/client_golang/prometheus"
)

const FakeAPIQMetricName = "apiq-migration-fake-service"

// ErrorWithMetric identifies arbitrary error with string identifier for handling at runtime
type ErrorWithMetric struct {
	Err    error
	Metric string
	Labels map[string]string
}

func (e ErrorWithMetric) Unwrap() error { return e.Err }

func (e ErrorWithMetric) Error() string {
	return fmt.Sprintf("%v, metric = %q", e.Err, e.Metric)
}

// GetOrRegisterCounter returns a Counter with the passed in name if it exists otherwise it registers a Counter to the Registry.
func GetOrRegisterCounter(metricName string, registry *prometheus.Registry) prometheus.Counter {
	counter, _ := registerOrGetCounter(registry, newCounter(metricName, ""))
	return counter
}

// GetOrRegisterCounterWithLabels returns a Counter with the passed in name and labels if it exists otherwise it registers a Counter to the Registry.
func GetOrRegisterCounterWithLabels(metricName string, labels map[string]string, registry *prometheus.Registry) prometheus.Counter {
	labels = translateLabels(labels)
	counter, _ := registerOrGetCounter(registry, newCounterWithLabels(metricName, "", labels))
	return counter
}

// GetOrRegisterGauge returns a Gauge with the passed in name if it exists otherwise it registers a Gauge to the Registry.
func GetOrRegisterGauge(metricName string, registry *prometheus.Registry) prometheus.Gauge {
	gauge, _ := registerOrGetGauge(registry, newGauge(metricName, ""))
	return gauge
}

// GetOrRegisterGaugeWithLabels returns a Gauge with the passed in name if it exists otherwise it registers a Gauge to the Registry.
func GetOrRegisterGaugeWithLabels(metricName string, labels map[string]string, registry *prometheus.Registry) prometheus.Gauge {
	labels = translateLabels(labels)
	gauge, _ := registerOrGetGauge(registry, newGaugeWithLabels(metricName, "", labels))
	return gauge
}

func GetOrRegisterSummaryWithLabels(metricName string, registry *prometheus.Registry, objectives map[float64]float64, labels map[string]string) prometheus.Summary {
	summary, _ := registerOrGetSummary(registry, newSummaryWithLabels(metricName, "", objectives, labels))
	return summary
}

// UnregisterGaugeWithLabels removes a gauge from the registry
func UnregisterGaugeWithLabels(name string, labels map[string]string, registry *prometheus.Registry) {
	registry.Unregister(newGaugeWithLabels(name, "", labels))
}

func newCounter(name, desc string) prometheus.Counter {
	if desc == "" {
		desc = name
	}
	c := prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: name,
			Help: desc,
		},
	)
	return c
}

func newCounterWithLabels(name, desc string, labels map[string]string) prometheus.Counter {
	if desc == "" {
		desc = name
	}
	c := prometheus.NewCounter(
		prometheus.CounterOpts{
			Name:        name,
			Help:        desc,
			ConstLabels: labels,
		},
	)
	return c
}

func newGauge(name, desc string) prometheus.Gauge {
	if desc == "" {
		desc = name
	}
	c := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: name,
			Help: desc,
		},
	)
	return c
}

func newGaugeWithLabels(name, desc string, labels map[string]string) prometheus.Gauge {
	if desc == "" {
		desc = name
	}
	c := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        name,
			Help:        desc,
			ConstLabels: labels,
		},
	)
	return c
}

func newSummaryWithLabels(name, desc string, objectives map[float64]float64, labels map[string]string) prometheus.Summary {
	if desc == "" {
		desc = name
	}
	s := prometheus.NewSummary(
		prometheus.SummaryOpts{
			Name:        name,
			Help:        desc,
			Objectives:  objectives,
			ConstLabels: labels,
		},
	)
	return s
}

func registerOrGetCounter(registry *prometheus.Registry, c prometheus.Counter) (prometheus.Counter, error) {
	if err := registry.Register(c); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(prometheus.Counter), nil
		}
		return nil, err
	}
	return c, nil
}

func registerOrGetGauge(registry *prometheus.Registry, c prometheus.Gauge) (prometheus.Gauge, error) {
	if err := registry.Register(c); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(prometheus.Gauge), nil
		}
		return nil, err
	}
	return c, nil
}

func registerOrGetSummary(registry *prometheus.Registry, s prometheus.Summary) (prometheus.Summary, error) {
	if err := registry.Register(s); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(prometheus.Summary), nil
		}
		return nil, err
	}
	return s, nil
}

func translateLabels(labels map[string]string) map[string]string {
	if !features.EnableSkipReconcileLatencyMetric {
		return labels
	}
	if labels[NamespaceLabel] == constants.SharedNamespace && strings.HasPrefix(labels[ResourceNameLabel], constants.SharedMigPrefix) {
		labels[ResourceNameLabel] = FakeAPIQMetricName
	}
	return labels
}
