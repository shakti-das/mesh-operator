package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/secretdiscovery"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"

	meshhttp "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"k8s.io/api/admission/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func TestServerArguments(t *testing.T) {
	cmd := NewDefaultServerCommand()

	probeAddr, err := cmd.Flags().GetInt("management-port")
	assert.Nil(t, err)
	assert.Equal(t, 15372, probeAddr)

	dryRun, err := cmd.Flags().GetBool("dry-run")
	assert.Nil(t, err)
	assert.True(t, dryRun)

	templatePaths, err := cmd.Flags().GetStringSlice("template-paths")
	assert.Nil(t, err)
	assert.True(t, reflect.DeepEqual(templatePaths, []string{}))

	// Test Kubernetes API client throttling settings
	kubeAPIQPS, err := cmd.Flags().GetFloat32("kube-api-qps")
	assert.Nil(t, err)
	assert.Equal(t, float32(5.0), kubeAPIQPS, "Default kube-api-qps should be 5.0")

	kubeAPIBurst, err := cmd.Flags().GetInt("kube-api-burst")
	assert.Nil(t, err)
	assert.Equal(t, 10, kubeAPIBurst, "Default kube-api-burst should be 10")
}

func TestMutatingWebhookEndPoint(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	path := []string{TemplatesLocation}
	stopCh := make(chan struct{})

	registry := prometheus.NewRegistry()
	templateManager := templating.NewTemplatesManagerOrDie(logger, stopCh, path)

	k8sClient := fake.NewSimpleClientset()
	primaryFakeClient := &secretdiscovery.FakeClient{KubeClient: k8sClient}
	primaryCluster := secretdiscovery.NewFakeCluster(primaryClusterName, true, primaryFakeClient)
	remoteClusters := []secretdiscovery.DynamicCluster{}

	discovery := secretdiscovery.NewFakeDynamicDiscovery(primaryCluster, remoteClusters)

	admittorServer, err := createAdmissionServer(logger, k8sClient, discovery, templateManager, templateManager, registry, true,
		"/etc/identity/server/certificates/server.pem", "/etc/identity/server/keys/server-key.pem",
		func(handler http.Handler) (meshhttp.TestableHTTPServer, error) {
			return meshhttp.NewTestHTTPServer(httptest.NewUnstartedServer(handler), true), nil
		})

	admittorServer.Start()
	defer admittorServer.Stop()

	require.NoError(t, err)

	req := &v1beta1.AdmissionReview{
		Request: &v1beta1.AdmissionRequest{
			UID: types.UID(ksuid.New().String()),
		},
	}
	req2 := &v1beta1.AdmissionReview{
		Request: &v1beta1.AdmissionRequest{
			UID: types.UID(ksuid.New().String()),
		},
	}

	reqJSON, err := json.Marshal(req)
	require.NoError(t, err)
	reqJSON2, err := json.Marshal(req2)
	require.NoError(t, err)

	// validates /mutate endpoint
	res, err := admittorServer.Client().Post(admittorServer.URL()+"/mutate", "application/json", bytes.NewBuffer(reqJSON))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	//validates /mutate/ endpoint
	res, err = admittorServer.Client().Post(admittorServer.URL()+"/mutate/", "application/json", bytes.NewBuffer(reqJSON2))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
}

func TestMetricsEndpoint(t *testing.T) {
	server := createMetricAndHealthServer(t)
	defer server.Stop()

	resp, err := server.Client().Get(server.URL() + "/metrics")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, isResponseContains(resp, "go_threads"))
}

func TestHealthAndReadinessEndpoints(t *testing.T) {
	server := createMetricAndHealthServer(t)
	defer server.Stop()

	resp, err := server.Client().Get(server.URL() + "/healthz")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, isResponseContains(resp, "Ok"))

	resp, err = server.Client().Get(server.URL() + "/readyz")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, isResponseContains(resp, "Ok"))
}

func createMetricAndHealthServer(t *testing.T) meshhttp.TestableHTTPServer {
	logger := zaptest.NewLogger(t).Sugar()
	probePort := 15372
	server := createMetricsAndHealthHttpServer(logger, probePort)
	return server
}

func isResponseContains(response *http.Response, expectedContains string) bool {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(response.Body)
	if err != nil {
		return false
	}
	return strings.Contains(buf.String(), expectedContains)
}
