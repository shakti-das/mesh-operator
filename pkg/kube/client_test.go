package kube

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func TestNewClientInternal(t *testing.T) {
	fakeRestConfig := &rest.Config{Host: "https://fake-k8s", APIPath: "/api"}
	logger := zaptest.NewLogger(t).Sugar()
	someCluster := "somecluster"
	testInformerCacheSyncTimeoutSeconds := 1
	var tweakListOptionsFunc func(options *v1.ListOptions)
	kubeClient, err := NewClientWithTweakListOption(logger, someCluster, fakeRestConfig, 2*time.Second, tweakListOptionsFunc, testInformerCacheSyncTimeoutSeconds, prometheus.NewRegistry())
	clientObject := (kubeClient).(*client)
	assert.Nil(t, err)
	assert.Equal(t, someCluster, clientObject.clusterName)
	assert.Equal(t, testInformerCacheSyncTimeoutSeconds, clientObject.InformerCacheSyncTimeoutSeconds)
	assert.NotNil(t, clientObject.kube)
	assert.NotNil(t, clientObject.kubeInformerFactory)
	assert.NotNil(t, clientObject.istio)
	assert.NotNil(t, clientObject.istioInformerFactory)
	assert.NotNil(t, clientObject.dynamic)
	assert.NotNil(t, clientObject.dynamicInformerFactory)
	assert.NotNil(t, clientObject.discovery)
	assert.NotNil(t, clientObject.dynamicInformerFactory)
	assert.NotNil(t, clientObject.mopClient)
	assert.NotNil(t, clientObject.dynamicInformerFactory)
	assert.NotNil(t, clientObject.mopInformerFactory)
}

func TestRestConfigQPSAndBurst(t *testing.T) {
	testCases := []struct {
		name  string
		qps   float32
		burst int
	}{
		{"Default k8s values", 5.0, 10},
		{"Custom values", 50.0, 100},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &rest.Config{Host: "https://fake-k8s", QPS: tc.qps, Burst: tc.burst}
			kubeClient, err := NewClientWithTweakListOption(zaptest.NewLogger(t).Sugar(), "test-cluster",
				config, 2*time.Second, nil, 1, prometheus.NewRegistry())

			assert.Nil(t, err)
			assert.Equal(t, tc.qps, kubeClient.(*client).config.QPS)
			assert.Equal(t, tc.burst, kubeClient.(*client).config.Burst)
		})
	}
}

func TestBuildClientsFromConfig_AppliesQPSAndBurst(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	clusterName := "test-cluster"
	testQPS := float32(50.0)
	testBurst := 100
	validKubeConfig := []byte(`apiVersion: v1
kind: Config
clusters:
- {cluster: {server: https://fake-k8s}, name: test}
contexts:
- {context: {cluster: test, user: test}, name: test}
current-context: test
users:
- {name: test, user: {token: fake}}`)
	_, _ = BuildClientsFromConfig(logger, clusterName, validKubeConfig, 2*time.Second, "", 1, prometheus.NewRegistry(), testQPS, testBurst)
}
