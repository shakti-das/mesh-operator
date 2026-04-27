package testing

import (
	"testing"

	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

const metricName = "test_metric"

func TestAssertEqualsCounterValue(t *testing.T) {
	registry := prometheus.NewRegistry()

	AssertEqualsCounterValue(t, registry, metricName, 0)

	commonmetrics.GetOrRegisterCounter(metricName, registry).Inc()

	AssertEqualsCounterValue(t, registry, metricName, 1)
}

func TestAssertEqualsGaugeValue(t *testing.T) {
	registry := prometheus.NewRegistry()

	AssertEqualsGaugeValue(t, registry, metricName, 0)

	commonmetrics.GetOrRegisterGauge(metricName, registry).Inc()

	AssertEqualsGaugeValue(t, registry, metricName, 1)
}
