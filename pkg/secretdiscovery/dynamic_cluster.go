package secretdiscovery

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// DynamicCluster provides and abstraction for a known cluster and access to the underlying k8s client
type DynamicCluster interface {
	// GetName return name of the cluster (eg. sam-processig1)
	GetName() string

	// GetClient returns the kubernetes client and clientConfig
	GetClient() (Client, error)

	// IsPrimary detects whether the cluster is a primary cluster
	IsPrimary() bool
}

type ClientFactory interface {
	NewClient(*zap.SugaredLogger, *rest.Config) (Client, error)
}

type defaultClientFactory struct{}

func (f *defaultClientFactory) NewClient(logger *zap.SugaredLogger, config *rest.Config) (Client, error) {
	return NewClient(logger, config)
}

func NewPrimaryCluster(name string, client Client) DynamicCluster {
	return &clusterImpl{
		name:      name,
		client:    client,
		lock:      sync.RWMutex{},
		isPrimary: true,
	}
}

func NewCluster(name string, kubeConfig []byte, logger *zap.SugaredLogger) DynamicCluster {
	return &clusterImpl{
		name:          name,
		kubeConfig:    kubeConfig,
		lock:          sync.RWMutex{},
		logger:        logger,
		clientFactory: &defaultClientFactory{},
	}
}

type clusterImpl struct {
	name          string
	isPrimary     bool
	kubeConfig    []byte
	clientConfig  *rest.Config
	client        Client
	lock          sync.RWMutex
	logger        *zap.SugaredLogger
	clientFactory ClientFactory
}

func (c *clusterImpl) GetName() string {
	return c.name
}

func (c *clusterImpl) IsPrimary() bool {
	return c.isPrimary
}

func (c *clusterImpl) GetClient() (Client, error) {
	c.lock.RLock()

	// Client is initialized, just return it
	if c.client != nil {
		defer c.lock.RUnlock()
		return c.client, nil
	}

	c.logger.Infof("initializing client for cluster: %s", c.name)

	// Release read lock before acquiring write lock
	c.lock.RUnlock()
	c.lock.Lock()
	defer c.lock.Unlock()

	// Check again, when we have the write lock
	if c.client == nil {
		if len(c.kubeConfig) == 0 {
			return nil, fmt.Errorf("kubeconfig is empty for cluster: %s", c.name)
		}

		rawConfig, err := clientcmd.Load(c.kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("kubeconfig cannot be loaded for cluster %s: %v", c.name, err)
		}

		if err := clientcmd.Validate(*rawConfig); err != nil {
			return nil, fmt.Errorf("kubeconfig is not valid for cluster %s: %v", c.name, err)
		}

		clientConfig := clientcmd.NewDefaultClientConfig(*rawConfig, &clientcmd.ConfigOverrides{})
		config, err := clientConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed due to client config error in cluster %s: %v", c.name, err)
		}

		client, err := c.clientFactory.NewClient(c.logger, config)
		if err != nil {
			return nil, err
		}
		c.client = client
	}

	return c.client, nil
}
