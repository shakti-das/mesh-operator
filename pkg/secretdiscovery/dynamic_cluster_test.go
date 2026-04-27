package secretdiscovery

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"k8s.io/client-go/rest"
)

// Some fake valid kube config
const fakeKubeConfig = "" +
	"apiVersion: v1\n" +
	"kind: Config\n" +
	"clusters:\n" +
	"  - cluster:\n" +
	"      insecure-skip-tls-verify: true\n" +
	"      server: https://1.2.3.4\n" +
	"    name: development\ncontexts:\n" +
	"  - context:\n" +
	"      cluster: development\n" +
	"      namespace: frontend\n" +
	"      user: developer\n" +
	"    name: dev-frontend\n" +
	"current-context: \"dev-frontend\"\n" +
	"users:\n" +
	"  - name: developer\n" +
	"    user:\n" +
	"      password: some-password\n" +
	"      username: developer\n"

type fakeClientFactory struct {
	clientToReturn Client
	errorToReturn  error
}

func (ff *fakeClientFactory) NewClient(*zap.SugaredLogger, *rest.Config) (Client, error) {
	return ff.clientToReturn, ff.errorToReturn
}

func TestClusterImpl_GetClient_AlreadyInitialized(t *testing.T) {
	existingClient := &FakeClient{}

	cluster := &clusterImpl{
		name:          "cluster-name",
		client:        existingClient,
		lock:          sync.RWMutex{},
		clientFactory: &fakeClientFactory{errorToReturn: fmt.Errorf("must not be called")},
	}

	clientFromCluster, err := cluster.GetClient()

	assert.NoError(t, err)
	assert.Equal(t, existingClient, clientFromCluster)
}

func TestClusterImpl_GetClient_Error(t *testing.T) {
	cluster := &clusterImpl{
		name:          "cluster-name",
		lock:          sync.RWMutex{},
		logger:        zaptest.NewLogger(t).Sugar(),
		kubeConfig:    []byte(fakeKubeConfig),
		clientFactory: &fakeClientFactory{errorToReturn: fmt.Errorf("test-error")},
	}

	clientFromCluster, err := cluster.GetClient()

	assert.ErrorContains(t, err, "test-error")
	assert.Nil(t, clientFromCluster)
}

func TestClusterImpl_GetClient_NotInitialized(t *testing.T) {
	clientToInit := &FakeClient{}
	cluster := &clusterImpl{
		name:          "cluster-name",
		lock:          sync.RWMutex{},
		logger:        zaptest.NewLogger(t).Sugar(),
		kubeConfig:    []byte(fakeKubeConfig),
		clientFactory: &fakeClientFactory{clientToReturn: clientToInit},
	}

	clientFromCluster, err := cluster.GetClient()

	assert.NoError(t, err)
	// Make sure client is created and returned
	assert.Equal(t, clientToInit, clientFromCluster)
	// Make sure that client was cached
	assert.Equal(t, clientToInit, cluster.client)
}
