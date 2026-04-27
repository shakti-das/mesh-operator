package secretdiscovery

import (
	"k8s.io/client-go/discovery"
	discoveryfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

type FakeClient struct {
	kubernetes.Interface
	Config     *rest.Config
	KubeClient *k8sfake.Clientset

	DynamicClient   *dynamicfake.FakeDynamicClient
	DiscoveryClient *discoveryfake.FakeDiscovery
	ExecResult      string
	ExecError       error
}

func (c *FakeClient) RESTConfig() *rest.Config {
	return c.Config
}

func (c *FakeClient) Kube() kubernetes.Interface {
	return c.KubeClient
}

func (c *FakeClient) Dynamic() dynamic.Interface {
	return c.DynamicClient
}

func (c *FakeClient) Discovery() discovery.DiscoveryInterface {
	return c.DiscoveryClient
}

func (c *FakeClient) ExecCmd(_ string, _ string, _ string, _ []string) (string, error) {
	return c.ExecResult, c.ExecError
}
