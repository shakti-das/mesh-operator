package testing

import (
	"testing"

	"github.com/istio-ecosystem/mesh-operator/common/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
	prommodel "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func GetCounterValue(registry *prometheus.Registry, metric string) (float64, error) {
	counter := metrics.GetOrRegisterCounter(metric, registry)

	m := &prommodel.Metric{}
	err := counter.Write(m)
	if err != nil {
		return 0, err
	}

	return m.GetCounter().GetValue(), nil
}

func GetCounterWithLabelsValue(registry *prometheus.Registry, metric string, labels map[string]string) (float64, error) {
	counter := metrics.GetOrRegisterCounterWithLabels(metric, labels, registry)

	m := &prommodel.Metric{}
	err := counter.Write(m)
	if err != nil {
		return 0, err
	}

	return m.GetCounter().GetValue(), nil
}

func GetSummaryCountWithLabelsValue(registry *prometheus.Registry, metric string, objectives map[float64]float64, labels map[string]string) (uint64, error) {
	counter := metrics.GetOrRegisterSummaryWithLabels(metric, registry, objectives, labels)

	m := &prommodel.Metric{}
	err := counter.Write(m)
	if err != nil {
		return 0, err
	}

	return m.GetSummary().GetSampleCount(), nil
}

func AssertEqualsCounterValue(t *testing.T, registry *prometheus.Registry, metric string, expected float64) bool {
	value, err := GetCounterValue(registry, metric)
	require.NoError(t, err)

	return assert.Equal(t, expected, value)
}

func AssertEqualsCounterValueWithLabel(t *testing.T, registry *prometheus.Registry, metric string,
	labels map[string]string, expected float64) bool {
	value, err := GetCounterWithLabelsValue(registry, metric, labels)
	require.NoError(t, err)

	if expected != value {
		t.Logf("Expected value: %f, Actual value: %f, Metric: %s, Labels: %v", expected, value, metric, labels)
	}
	return assert.Equal(t, expected, value)
}

func AssertSummarySamples(t *testing.T, registry *prometheus.Registry, metric string, objectives map[float64]float64, labels map[string]string, expected uint64) {
	value, err := GetSummaryCountWithLabelsValue(registry, metric, objectives, labels)
	require.NoError(t, err)

	assert.Equal(t, expected, value)
}

func GetGaugeValue(registry *prometheus.Registry, metric string) (float64, error) {
	gauge := metrics.GetOrRegisterGauge(metric, registry)

	m := &prommodel.Metric{}
	err := gauge.Write(m)
	if err != nil {
		return 0, err
	}

	return m.GetGauge().GetValue(), nil
}

func AssertEqualsGaugeValue(t *testing.T, registry *prometheus.Registry, metric string, expected float64) bool {
	value, err := GetGaugeValue(registry, metric)
	require.NoError(t, err)

	return assert.Equal(t, expected, value)
}

func AssertEqualsGaugeValueWithLabel(t *testing.T, registry *prometheus.Registry, metric string, labels map[string]string, expected float64) bool {
	gauge := metrics.GetOrRegisterGaugeWithLabels(metric, labels, registry)
	assert.NotNil(t, gauge)

	m := &prommodel.Metric{}
	_ = gauge.Write(m)

	return assert.Equal(t, expected, m.GetGauge().GetValue())
}
