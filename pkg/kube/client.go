package kube

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"go.uber.org/zap"

	"github.com/istio-ecosystem/mesh-operator/pkg/generated/clientset/versioned"
	"github.com/istio-ecosystem/mesh-operator/pkg/generated/informers/externalversions"

	istioclient "istio.io/client-go/pkg/clientset/versioned"
	istioinformer "istio.io/client-go/pkg/informers/externalversions"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

// Client is a helper for common Kubernetes client operations. This contains various different kubernetes
// clients using a shared config.
// TODO shakti make this interface testable, then replace all controllers_api New with a single kube.Client parameter.
type Client interface {
	// TODO - stop embedding this, it will conflict with future additions. Use Kube() instead is preferred
	kubernetes.Interface

	// Kube returns the core kube client
	Kube() kubernetes.Interface

	// Dynamic client.
	Dynamic() dynamic.Interface

	Discovery() discovery.DiscoveryInterface

	// Istio returns the Istio kube client.
	Istio() istioclient.Interface

	// KubeInformer returns an informer for core kube client
	KubeInformerFactory() informers.SharedInformerFactory

	// DynamicInformer returns an informer for dynamic client
	DynamicInformerFactory() dynamicinformer.DynamicSharedInformerFactory

	// IstioInformer returns an informer for the istio client
	IstioInformerFactory() istioinformer.SharedInformerFactory

	MopInformerFactory() externalversions.SharedInformerFactory
	MopApiClient() versioned.Interface

	// RunAndWait starts all informers and waits for their caches to sync.
	// Warning: this must be called AFTER .Informer() is called, which will register the informer.
	RunAndWait(stop <-chan struct{}, isPrimaryCluster bool, additionalInformers []informers.GenericInformer) error

	// GetClusterName get the name of the cluster this client is associated with
	GetClusterName() string
}

type client struct {
	kubernetes.Interface

	logger *zap.SugaredLogger

	config *rest.Config

	kube                kubernetes.Interface
	kubeInformerFactory informers.SharedInformerFactory

	dynamic                dynamic.Interface
	dynamicInformerFactory dynamicinformer.DynamicSharedInformerFactory

	discovery discovery.DiscoveryInterface

	istio                istioclient.Interface
	istioInformerFactory istioinformer.SharedInformerFactory

	mopClient          versioned.Interface
	mopInformerFactory externalversions.SharedInformerFactory
	clusterName        string

	InformerCacheSyncTimeoutSeconds int

	metricsRegistry *prometheus.Registry
}

// NewClientWithTweakListOption creates a Kubernetes client from the given rest config WithTweakListOptions.
func NewClientWithTweakListOption(logger *zap.SugaredLogger, clusterName string, restConfig *rest.Config, rescanPeriod time.Duration, tweakListOptionsFunc func(options *v1.ListOptions), informerCacheSyncTimeoutSeconds int, metricsRegistry *prometheus.Registry) (Client, error) {
	return newClientInternal(logger, clusterName, restConfig, rescanPeriod, tweakListOptionsFunc, informerCacheSyncTimeoutSeconds, metricsRegistry)
}

func newClientInternal(logger *zap.SugaredLogger, clusterName string, restConfig *rest.Config, rescanPeriod time.Duration, tweakListOptionsFunc func(options *v1.ListOptions), informerCacheSyncTimeoutSeconds int, metricsRegistry *prometheus.Registry) (*client, error) {
	var c client
	var err error

	c.config = restConfig
	c.clusterName = clusterName

	c.InformerCacheSyncTimeoutSeconds = informerCacheSyncTimeoutSeconds

	c.metricsRegistry = metricsRegistry

	c.kube, err = kubernetes.NewForConfig(c.config)
	if err != nil {
		return nil, err
	}

	c.kubeInformerFactory = kubeinformers.NewSharedInformerFactoryWithOptions(c.kube, rescanPeriod, kubeinformers.WithTweakListOptions(tweakListOptionsFunc))

	c.istio, err = istioclient.NewForConfig(c.config)
	if err != nil {
		return nil, err
	}
	c.istioInformerFactory = istioinformer.NewSharedInformerFactoryWithOptions(c.istio, rescanPeriod, istioinformer.WithTweakListOptions(tweakListOptionsFunc))

	c.dynamic, err = dynamic.NewForConfig(c.config)
	if err != nil {
		return nil, err
	}
	c.dynamicInformerFactory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(c.dynamic, rescanPeriod, v1.NamespaceAll, nil)

	c.discovery, err = discovery.NewDiscoveryClientForConfig(c.config)
	if err != nil {
		return nil, err
	}

	c.mopClient, err = versioned.NewForConfig(c.config)
	if err != nil {
		return nil, err
	}
	c.mopInformerFactory = externalversions.NewSharedInformerFactory(c.mopClient, rescanPeriod)

	c.logger = logger

	return &c, nil
}

func (c *client) Kube() kubernetes.Interface {
	return c.kube
}

func (c *client) Discovery() discovery.DiscoveryInterface {
	return c.discovery
}

func (c *client) Dynamic() dynamic.Interface {
	return c.dynamic
}

func (c *client) Istio() istioclient.Interface {
	return c.istio
}

func (c *client) KubeInformerFactory() informers.SharedInformerFactory {
	return c.kubeInformerFactory
}

func (c *client) DynamicInformerFactory() dynamicinformer.DynamicSharedInformerFactory {
	return c.dynamicInformerFactory
}

func (c *client) IstioInformerFactory() istioinformer.SharedInformerFactory {
	return c.istioInformerFactory
}

func (c *client) MopInformerFactory() externalversions.SharedInformerFactory {
	return c.mopInformerFactory
}

func (c *client) MopApiClient() versioned.Interface {
	return c.mopClient
}

func (c *client) GetClusterName() string {
	return c.clusterName
}

// RunAndWait starts all informers and waits for their caches to sync.
// Warning: this must be called AFTER .Informer() is called, which will register the informer.
func (c *client) RunAndWait(stop <-chan struct{}, isPrimaryCluster bool, additionalInformers []informers.GenericInformer) error {

	serviceInformer := c.kubeInformerFactory.Core().V1().Services()

	// We're running the informers for service-dependent resources after the service cache has synced.
	// This is to ensure init-time events are handled in the correct order. See W-11696764.
	c.logger.Info("Running service informer and waiting for service cache to sync")
	go serviceInformer.Informer().Run(stop)
	if ok := cache.WaitForCacheSync(stop, serviceInformer.Informer().HasSynced); !ok {
		return fmt.Errorf("failed to wait for service cache to sync")
	}

	if isPrimaryCluster {
		// do not start kubeInformerFactory factory as secret informer is already running
		// and spawn k8s informers in separate goroutine
		statefulSetInformer := c.kubeInformerFactory.Apps().V1().StatefulSets()
		deploymentInformer := c.kubeInformerFactory.Apps().V1().Deployments()
		serviceEntriesInformer := c.istioInformerFactory.Networking().V1alpha3().ServiceEntries()
		mopInformer := c.mopInformerFactory.Mesh().V1alpha1().MeshOperators()

		c.logger.Infof("Running other informers")
		go statefulSetInformer.Informer().Run(stop)
		go deploymentInformer.Informer().Run(stop)
		go serviceEntriesInformer.Informer().Run(stop)
		go mopInformer.Informer().Run(stop)

		c.logger.Infof("Starting all informer factories")
		c.dynamicInformerFactory.Start(stop)
		c.istioInformerFactory.Start(stop)
		c.mopInformerFactory.Start(stop)

		// WaitForCacheSync waits for all started informers' cache sync to complete
		c.logger.Infof("Waiting for MOP informer factory cache sync to complete")
		c.mopInformerFactory.WaitForCacheSync(stop)

		for _, informer := range additionalInformers {
			go informer.Informer().Run(stop)
			if ok := cache.WaitForCacheSync(stop, informer.Informer().HasSynced); !ok {
				return fmt.Errorf("failed to wait for additional object cache to sync")
			}
		}

		return nil
	}

	// There is no need to run Start methods in a separate goroutine.
	// Start method is non-blocking and runs all registered informers in a dedicated goroutine.
	c.logger.Infof("Starting all informer factories")
	c.kubeInformerFactory.Start(stop)
	c.dynamicInformerFactory.Start(stop)
	c.istioInformerFactory.Start(stop)
	c.mopInformerFactory.Start(stop)

	// WaitForCacheSync waits for all started informers' cache were synced.
	c.logger.Infof("Waiting for all informer factory cache syncs")
	c.kubeInformerFactory.WaitForCacheSync(stop)
	c.dynamicInformerFactory.WaitForCacheSync(stop)
	c.istioInformerFactory.WaitForCacheSync(stop)
	c.mopInformerFactory.WaitForCacheSync(stop)

	return nil
}
