package metrics

import (
	"errors"
	"fmt"
	"log"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

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

func UpdateErrorMetrics(err error, registry *prometheus.Registry) {
	var updateMetricError ErrorWithMetric
	if errors.As(err, &updateMetricError) {
		if updateMetricError.Labels != nil {
			GetOrRegisterCounterWithLabels(updateMetricError.Metric, updateMetricError.Labels, registry).Inc()
		} else {
			GetOrRegisterCounter(updateMetricError.Metric, registry).Inc()
		}
	}
}

// GetOrRegisterCounter returns a Counter with the passed in name if it exists otherwise it registers a Counter to the Registry.
func GetOrRegisterCounter(metricName string, registry *prometheus.Registry) prometheus.Counter {
	counter, _ := registerOrGetCounter(registry, newCounter(metricName, ""))
	return counter
}

// GetOrRegisterCounterWithLabels returns a Counter with the passed in name and labels if it exists otherwise it registers a Counter to the Registry.
func GetOrRegisterCounterWithLabels(metricName string, labels map[string]string, registry *prometheus.Registry) prometheus.Counter {
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
	gauge, _ := registerOrGetGauge(registry, newGaugeWithLabels(metricName, "", labels))
	return gauge
}

func GetOrRegisterSummary(metricName string, registry *prometheus.Registry, objectives map[float64]float64) prometheus.Summary {
	summary, _ := registerOrGetSummary(registry, newSummary(metricName, "", objectives))
	return summary
}

func GetOrRegisterSummaryWithLabels(metricName string, registry *prometheus.Registry, objectives map[float64]float64, labels map[string]string) prometheus.Summary {
	summary, _ := registerOrGetSummary(registry, newSummaryWithLabels(metricName, "", objectives, labels))
	return summary
}

// RegisterCounter registers a Counter with the given name and description to the registry.
func RegisterCounter(name, desc string, registry *prometheus.Registry) {
	//nolint:errcheck
	registry.Register(newCounter(name, desc))
}

// RegisterCounterWithLabels registers a Counter with the given name, description and labels  to the registry.
func RegisterCounterWithLabels(name, desc string, labels map[string]string, registry *prometheus.Registry) {
	//nolint:errcheck
	registry.Register(newCounterWithLabels(name, desc, labels))
}

// RegisterGauge registers a Gauge with the given name and description to the registry.
func RegisterGauge(name, desc string, registry *prometheus.Registry) {
	//nolint:errcheck
	registry.Register(newGauge(name, desc))
}

// RegisterGaugeWithLabels registers a Gauge with the given name and description and labels to the registry.
func RegisterGaugeWithLabels(name, desc string, labels map[string]string, registry *prometheus.Registry) {
	//nolint:errcheck
	registry.Register(newGaugeWithLabels(name, desc, labels))
}

// RegisterGaugeFunc registers a functional gauage with the given name and description to the registry and attaches the function to it.
func RegisterGaugeFunc(name, desc string, function func() float64, registry *prometheus.Registry) {
	//nolint:errcheck
	registry.Register(newGaugeFunc(name, desc, function))
}

// UnregisterGaugeWithLabels removes a gauge from the registry
func UnregisterGaugeWithLabels(name string, labels map[string]string, registry *prometheus.Registry) {
	registry.Unregister(newGaugeWithLabels(name, "", labels))
}

// GetCounterValue returns a counter's value
func GetCounterValue(counterName string, registry *prometheus.Registry) float64 {
	pb := &dto.Metric{}
	err := GetOrRegisterCounter(counterName, registry).Write(pb)
	if err != nil {
		log.Printf("Error while retrieving metric value %v", err)
	}
	return pb.GetCounter().GetValue()
}

// GetCounterWithLabelsValue returns a counter's value
func GetCounterWithLabelsValue(counterName string, labels map[string]string, registry *prometheus.Registry) float64 {
	pb := &dto.Metric{}
	err := GetOrRegisterCounterWithLabels(counterName, labels, registry).Write(pb)
	if err != nil {
		log.Printf("Error while retrieving metric value %v", err)
	}
	return pb.GetCounter().GetValue()
}

func newRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
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
			ConstLabels: prometheus.Labels(labels),
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
			ConstLabels: prometheus.Labels(labels),
		},
	)
	return c
}

func newSummary(name, desc string, objectives map[float64]float64) prometheus.Summary {
	if desc == "" {
		desc = name
	}
	s := prometheus.NewSummary(
		prometheus.SummaryOpts{
			Name:       name,
			Help:       desc,
			Objectives: objectives,
		},
	)
	return s
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

func newGaugeFunc(name, desc string, function func() float64) prometheus.GaugeFunc {
	if desc == "" {
		desc = name
	}
	return prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: name,
			Help: desc,
		},
		function,
	)
}

func registerOrGetCounter(registry *prometheus.Registry, c prometheus.Counter) (prometheus.Counter, error) {
	if err := registry.Register(c); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(prometheus.Counter), nil
		}
		log.Printf("Error while registerOrGetCounter metric value %v", err)
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
