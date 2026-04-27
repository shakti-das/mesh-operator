package testing

import (
	"testing"

	"github.com/istio-ecosystem/mesh-operator/common/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

const metricName = "test_metric"

func TestAssertEqualsCounterValue(t *testing.T) {
	registry := prometheus.NewRegistry()

	AssertEqualsCounterValue(t, registry, metricName, 0)

	metrics.GetOrRegisterCounter(metricName, registry).Inc()

	AssertEqualsCounterValue(t, registry, metricName, 1)
}

func TestAssertEqualsGaugeValue(t *testing.T) {
	registry := prometheus.NewRegistry()

	AssertEqualsGaugeValue(t, registry, metricName, 0)

	metrics.GetOrRegisterGauge(metricName, registry).Inc()

	AssertEqualsGaugeValue(t, registry, metricName, 1)
}
