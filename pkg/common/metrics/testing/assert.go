package testing

import (
	"testing"

	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"

	"github.com/prometheus/client_golang/prometheus"
	prommodel "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func GetCounterValue(registry *prometheus.Registry, metric string) (float64, error) {
	counter := commonmetrics.GetOrRegisterCounter(metric, registry)

	m := &prommodel.Metric{}
	err := counter.Write(m)
	if err != nil {
		return 0, err
	}

	return m.GetCounter().GetValue(), nil
}

func GetCounterWithLabelsValue(registry *prometheus.Registry, metric string, labels map[string]string) (float64, error) {
	counter := commonmetrics.GetOrRegisterCounterWithLabels(metric, labels, registry)

	m := &prommodel.Metric{}
	err := counter.Write(m)
	if err != nil {
		return 0, err
	}

	return m.GetCounter().GetValue(), nil
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

	return assert.Equal(t, expected, value)
}

func GetGaugeValue(registry *prometheus.Registry, metric string) (float64, error) {
	gauge := commonmetrics.GetOrRegisterGauge(metric, registry)

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
	gauge := commonmetrics.GetOrRegisterGaugeWithLabels(metric, labels, registry)
	assert.NotNil(t, gauge)

	m := &prommodel.Metric{}
	_ = gauge.Write(m)

	return assert.Equal(t, expected, m.GetGauge().GetValue())
}
