//go:build !ignore_test_utils

package kube_test

import (
	"fmt"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/generated/clientset/versioned"
	meshfake "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/generated/clientset/versioned/fake"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/generated/informers/externalversions"
	istiofake "istio.io/client-go/pkg/clientset/versioned/fake"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	istioclient "istio.io/client-go/pkg/clientset/versioned"
	istioinformer "istio.io/client-go/pkg/informers/externalversions"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

type FakeClient struct {
	kubernetes.Interface

	KubeClient          *k8sfake.Clientset
	kubeInformerFactory informers.SharedInformerFactory

	DynamicClient          *dynamicfake.FakeDynamicClient
	dynamicInformerFactory dynamicinformer.DynamicSharedInformerFactory

	DiscoveryClient *discoveryfake.FakeDiscovery

	IstioClient          *istiofake.Clientset
	istioInformerFactory istioinformer.SharedInformerFactory

	MopClient          *meshfake.Clientset
	mopInformerFactory externalversions.SharedInformerFactory
	clusterName        string
}

func (c *FakeClient) InitFactory() {
	c.kubeInformerFactory = kubeinformers.NewSharedInformerFactory(c.KubeClient, 0)
	c.istioInformerFactory = istioinformer.NewSharedInformerFactory(c.IstioClient, 0)
	c.dynamicInformerFactory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(c.DynamicClient, 0, v1.NamespaceAll, nil)
	c.mopInformerFactory = externalversions.NewSharedInformerFactory(c.MopClient, 0)
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

func (c *FakeClient) Istio() istioclient.Interface {
	return c.IstioClient
}

func (c *FakeClient) KubeInformerFactory() informers.SharedInformerFactory {
	return c.kubeInformerFactory
}

func (c *FakeClient) DynamicInformerFactory() dynamicinformer.DynamicSharedInformerFactory {
	return c.dynamicInformerFactory
}

func (c *FakeClient) IstioInformerFactory() istioinformer.SharedInformerFactory {
	return c.istioInformerFactory
}

func (c *FakeClient) MopInformerFactory() externalversions.SharedInformerFactory {
	return c.mopInformerFactory
}

func (c *FakeClient) MopApiClient() versioned.Interface {
	return c.MopClient
}

func (c *FakeClient) GetClusterName() string {
	return c.clusterName
}

// RunAndWait starts all informers and waits for their caches to sync.
// Warning: this must be called AFTER .Informer() is called, which will register the informer.
func (c *FakeClient) RunAndWait(stop <-chan struct{}, isPrimaryCluster bool, additionalInformers []informers.GenericInformer) error {

	if isPrimaryCluster {
		// do not start kubeInformerFactory factory as secret informer is already running
		// spawn k8s informers in seperate goroutine
		serviceInformer := c.kubeInformerFactory.Core().V1().Services()
		statefulSetInformer := c.kubeInformerFactory.Apps().V1().StatefulSets()
		deploymentInformer := c.kubeInformerFactory.Apps().V1().Deployments()
		rolloutsInformer := c.dynamicInformerFactory.ForResource(constants.RolloutResource)
		go deploymentInformer.Informer().Run(stop)
		go serviceInformer.Informer().Run(stop)
		go rolloutsInformer.Informer().Run(stop)

		if ok := cache.WaitForCacheSync(stop, serviceInformer.Informer().HasSynced); !ok {
			return fmt.Errorf("failed to wait for service cache to sync")
		}
		if ok := cache.WaitForCacheSync(stop, rolloutsInformer.Informer().HasSynced); !ok {
			return fmt.Errorf("failed to wait for rollout cache to sync")
		}

		go statefulSetInformer.Informer().Run(stop)

		for _, informer := range additionalInformers {
			go informer.Informer().Run(stop)
			if ok := cache.WaitForCacheSync(stop, informer.Informer().HasSynced); !ok {
				return fmt.Errorf("failed to wait for additional object cache to sync")
			}
		}

		// There is no need to run Start methods in a separate goroutine.
		// Start method is non-blocking and runs all registered informers in a dedicated goroutine.
		c.dynamicInformerFactory.Start(stop)
		c.istioInformerFactory.Start(stop)
		c.mopInformerFactory.Start(stop)

		// WaitForCacheSync waits for all started informers' cache were synced.
		c.kubeInformerFactory.WaitForCacheSync(stop)
		c.dynamicInformerFactory.WaitForCacheSync(stop)
		c.istioInformerFactory.WaitForCacheSync(stop)
		c.mopInformerFactory.WaitForCacheSync(stop)

		return nil
	}

	// There is no need to run Start methods in a separate goroutine.
	// Start method is non-blocking and runs all registered informers in a dedicated goroutine.
	c.kubeInformerFactory.Start(stop)
	c.dynamicInformerFactory.Start(stop)
	c.istioInformerFactory.Start(stop)
	c.mopInformerFactory.Start(stop)

	// WaitForCacheSync waits for all started informers' cache were synced.
	c.kubeInformerFactory.WaitForCacheSync(stop)
	c.dynamicInformerFactory.WaitForCacheSync(stop)
	c.istioInformerFactory.WaitForCacheSync(stop)
	c.mopInformerFactory.WaitForCacheSync(stop)

	return nil
}
