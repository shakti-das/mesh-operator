package metrics

import (
	"reflect"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	promgo "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

func TestRegisterCounter(t *testing.T) {
	registry := newRegistry()
	RegisterCounter("test_counter", "test counter", registry)

	assertCounterExists(registry, t, "test_counter")
	// Register counter again and validate it does not panic.
	assert.NotPanics(t, func() { RegisterCounter("test_counter", "test counter", registry) }, "register same gauge twice failed")
}

func TestRegisterCounterWithLabels(t *testing.T) {
	registry := newRegistry()
	RegisterCounterWithLabels("test_counter", "test counter", map[string]string{"label_1": "value_1", "label_2": "value_2"}, registry)

	assertCounterExists(registry, t, "test_counter")
	// Register counter again and validate it does not panic.
	assert.NotPanics(t, func() {
		RegisterCounterWithLabels("test_counter", "test counter", map[string]string{"label_1": "value_1", "label_2": "value_2"}, registry)
	}, "register same gauge twice failed")
}

func TestRegisterGauge(t *testing.T) {
	registry := newRegistry()
	RegisterGauge("test_gauge", "test gauge", registry)

	assertGaugeExists(registry, t, "test_gauge")

	// Register gauge again and validate it does not panic.
	assert.NotPanics(t, func() { RegisterGauge("test_gauge", "test gauge", registry) }, "register same gauge twice failed")
}

func TestRegisterGaugeWithLabels(t *testing.T) {
	registry := newRegistry()
	RegisterGaugeWithLabels("test_gauge_with_labels", "test gauge with labels", map[string]string{"label_1": "value_1", "label_2": "value_2"}, registry)

	assertGaugeExists(registry, t, "test_gauge_with_labels")
	// Register gauge again and validate it does not panic.
	assert.NotPanics(t, func() {
		RegisterGaugeWithLabels("test_gauge_with_labels", "test gauge with labels", map[string]string{"label_1": "value_1", "label_2": "value_2"}, registry)
	}, "register same gauge twice failed")
}

func TestRegisterGaugeFunc(t *testing.T) {
	registry := newRegistry()
	RegisterGaugeFunc("test_gauge_func", "test gauge function", func() float64 { return 12.4 }, registry)

	assertGaugeExists(registry, t, "test_gauge_func")

	// Register gauge again and validate it does not panic.
	assert.NotPanics(t, func() {
		RegisterGaugeFunc("test_gauge_func", "test gauge function", func() float64 { return 12.4 }, registry)
	}, "register same gauge twice failed")
}

func TestGetOrRegisterCounter(t *testing.T) {
	registry := newRegistry()
	counter1 := GetOrRegisterCounter("test_counter", registry)

	assertCounterExists(registry, t, "test_counter")

	counter2 := GetOrRegisterCounter("test_counter", registry)

	// Validate that the same counter is returned.
	assert.True(t, reflect.DeepEqual(counter1, counter2), "GetOrRegisterCounter returns different counter")
}

func TestGetOrRegisterCounterWithLabels(t *testing.T) {
	registry := newRegistry()
	counter1 := GetOrRegisterCounterWithLabels("test_counter", map[string]string{"label_1": "value_1", "label_2": "value_2"}, registry)

	assertCounterExists(registry, t, "test_counter")

	counter2 := GetOrRegisterCounterWithLabels("test_counter", map[string]string{"label_1": "value_1", "label_2": "value_2"}, registry)

	// Validate that the same counter is returned.
	assert.True(t, reflect.DeepEqual(counter1, counter2), "GetOrRegisterCounterWithLabels returns different counter")
}

func TestGetOrRegisterGauge(t *testing.T) {
	registry := newRegistry()
	gauge1 := GetOrRegisterGauge("test_gauge", registry)

	assertGaugeExists(registry, t, "test_gauge")

	gauge2 := GetOrRegisterGauge("test_gauge", registry)

	// Validate that the same gauge is returned.
	assert.True(t, reflect.DeepEqual(gauge1, gauge2), "GetOrRegisterGauge returns different gauge")
}

func TestGetOrRegisterGaugeWithLabels(t *testing.T) {
	registry := newRegistry()
	gauge1 := GetOrRegisterGaugeWithLabels("test_gauge_with_labels", map[string]string{"label_1": "value_1", "label_2": "value_2"}, registry)

	assertGaugeExists(registry, t, "test_gauge_with_labels")

	gauge2 := GetOrRegisterGaugeWithLabels("test_gauge_with_labels", map[string]string{"label_1": "value_1", "label_2": "value_2"}, registry)

	// Validate that the same gauge is returned.
	assert.True(t, reflect.DeepEqual(gauge1, gauge2), "GetOrRegisterGaugeWithLabels returns different gauge")
}

func assertCounterExists(registry *prometheus.Registry, t *testing.T, expectedName string) {
	metricfamily, _ := registry.Gather()

	var counter *promgo.Counter
	var name string
	for _, mf := range metricfamily {
		if mf.GetType() == promgo.MetricType_COUNTER {
			counter = mf.Metric[0].Counter
			name = mf.GetName()
		}
	}
	assert.NotNil(t, counter)
	assert.Equal(t, name, expectedName)
}

func assertGaugeExists(registry *prometheus.Registry, t *testing.T, expectedName string) {
	metricfamily, _ := registry.Gather()

	var gauge *promgo.Gauge
	var name string
	for _, mf := range metricfamily {
		if mf.GetType() == promgo.MetricType_GAUGE {
			gauge = mf.Metric[0].Gauge
			name = mf.GetName()
		}
	}
	assert.NotNil(t, gauge)
	assert.Equal(t, name, expectedName)
}
