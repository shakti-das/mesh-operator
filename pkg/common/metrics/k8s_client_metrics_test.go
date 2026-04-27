package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func TestUrlOrObject(t *testing.T) {
	assert.Equal(t,
		"status_meshoperators",
		urlOrObject("/apis/mesh.io/v1alpha1/namespaces/test-namespace/meshoperators/vlad-mixed-mode/status"))
	assert.Equal(t,
		"envoyfilters",
		urlOrObject("/apis/networking.istio.io/v1alpha3/namespaces/mesh-control-plane/envoyfilters/istio-shipping-ingress--service-mesh--max-conn-duration"))
	assert.Equal(t,
		"list_secrets",
		urlOrObject("/api/v1/namespaces/mesh-control-plane/secrets"))
	assert.Equal(t,
		"list-all_namespaces",
		urlOrObject("/api/v1/namespaces"))
	assert.Equal(t,
		"list-all_meshoperators",
		urlOrObject("/apis/mesh.io/v1alpha1/meshoperators"))

	assert.Equal(t,
		"services",
		urlOrObject("/api/v1/namespaces/anc-innovation/services/partner-connect"))
	assert.Equal(t,
		"namespaces",
		urlOrObject("/api/v1/namespaces/anc-innovation"))
}

func TestNewLatencyMetric_DuplicateRegistration(t *testing.T) {
	registry := prometheus.NewRegistry()
	logger := zaptest.NewLogger(t).Sugar()

	// First registration - success path
	observer1 := newLatencyMetric(logger, registry)
	assert.NotNil(t, observer1)

	// Second registration - error path
	observer2 := newLatencyMetric(logger, registry)
	assert.NotNil(t, observer2)
}
