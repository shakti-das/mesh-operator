package secretdiscovery

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	corev1informers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/cache"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
)

const (
	defaultInformerRescanPeriod = 60 * 15 * time.Second
	multiClusterSecretLabel     = "istio/multiCluster"
)

type Discovery interface {
	GetClusters() []DynamicCluster
	GetPrimaryCluster() DynamicCluster
	GetRemoteClusters() []DynamicCluster
}

func NewDynamicDiscovery(
	logger *zap.SugaredLogger,
	primaryClusterName string,
	client Client,
	secretsNamespace string,
	stopCh <-chan struct{},
) (Discovery, error) {
	k8sInformersFactory := kubeinformers.NewSharedInformerFactoryWithOptions(
		client.Kube(),
		defaultInformerRescanPeriod,
		kubeinformers.WithNamespace(secretsNamespace))

	discovery := &dynamicDiscovery{
		logger:         logger,
		primaryCluster: NewPrimaryCluster(primaryClusterName, client),
		lock:           sync.RWMutex{},
		clusters:       map[string]map[string]DynamicCluster{},
	}

	secretsInformer := k8sInformersFactory.Core().V1().Secrets()

	secretsEventHandler := &multiClusterSecretEventHandler{
		logger:           logger,
		clientManager:    discovery,
		secretsNamespace: secretsNamespace,
	}
	secretsInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: secretsEventHandler.isMultiClusterSecret,
		Handler:    secretsEventHandler,
	})

	if !runSecretInformer(logger, secretsInformer, stopCh) {
		return nil, fmt.Errorf("failed to start secrets informer: timed out waiting for sync")
	}

	return discovery, nil
}

type dynamicDiscovery struct {
	logger         *zap.SugaredLogger
	primaryCluster DynamicCluster
	lock           sync.RWMutex
	clusters       map[string]map[string]DynamicCluster
}

func (m *dynamicDiscovery) GetPrimaryCluster() DynamicCluster {
	return m.primaryCluster
}

func (m *dynamicDiscovery) GetRemoteClusters() []DynamicCluster {
	var flatClusters []DynamicCluster

	m.lock.RLock()
	defer func() {
		m.lock.RUnlock()
	}()

	for _, clustersForSecret := range m.clusters {
		for _, cluster := range clustersForSecret {
			flatClusters = append(flatClusters, cluster)
		}
	}

	// Sort clusters by name, so that we have predictable order.
	// Typical use-case for this method is "Search for object X in all clusters".
	// clustersForSecret is a map, so ordering of clusters returned is random.
	// This opens the door to weird sporadic behavior (e.g. on request 1 lookup finds service1, but on request 2 it finds service2) - we don't want that.
	sort.Slice(flatClusters, func(i, j int) bool {
		return flatClusters[i].GetName() > flatClusters[j].GetName()
	})

	return flatClusters
}

func (m *dynamicDiscovery) GetClusters() []DynamicCluster {
	// No need to acquire a read lock, GetRemoteClusters does it for us, while primary cluster must not change.
	// Prepend primary cluster, so it always goes first
	return append([]DynamicCluster{m.primaryCluster}, m.GetRemoteClusters()...)
}

func (m *dynamicDiscovery) syncClustersForSecret(secret *corev1.Secret) {
	secretKey := m.getSecretKey(secret)

	m.lock.Lock()
	defer func() {
		m.lock.Unlock()
	}()

	// Delete stale clusters in the secretKey
	for _, cluster := range m.clusters[secretKey] {
		if _, exists := secret.Data[cluster.GetName()]; !exists {
			delete(m.clusters[secretKey], cluster.GetName())
			m.logger.Warnf("removed stale cluster from known clusters list: %s, secret: %s", cluster.GetName(), secretKey)
		}
	}

	for clusterId, kubeConfig := range secret.Data {
		// Skip if the same cluster exists under a different key
		existingSecretKey, exists := m.clusterExists(secretKey, clusterId)
		if exists {
			m.logger.Warnf("cluster %v already exist in secret key: %v. Ignoring cluster creation for secret key: %v", clusterId, existingSecretKey, secretKey)
			continue
		}

		// Add cluster
		if m.clusters[secretKey] == nil {
			m.clusters[secretKey] = map[string]DynamicCluster{}
		}
		m.clusters[secretKey][clusterId] = NewCluster(clusterId, kubeConfig, m.logger)
		m.logger.Infof("adding cluster %s from secret key %s", clusterId, secretKey)
	}
}

func (m *dynamicDiscovery) removeClustersFromSecret(secret *corev1.Secret) {
	secretKey := m.getSecretKey(secret)

	m.lock.Lock()
	defer func() {
		m.lock.Unlock()
	}()

	var clustersRemoved []string
	for clusterId := range m.clusters[secretKey] {
		clustersRemoved = append(clustersRemoved, clusterId)
	}
	m.logger.Warnf("removing clusters %s from secret %s", clustersRemoved, secretKey)

	delete(m.clusters, secretKey)
}

func (m *dynamicDiscovery) getSecretKey(secret *corev1.Secret) string {
	return fmt.Sprintf("%s/%s", secret.Namespace, secret.Name)
}

func (m *dynamicDiscovery) clusterExists(secretKey string, clusterID string) (string, bool) {
	for key, clusters := range m.clusters {
		if key != secretKey {
			if _, ok := clusters[clusterID]; ok {
				return key, true
			}
		}
	}
	return "", false
}

// multiClusterSecretEventHandler - a secrets informer event handler tightly coupled with the multiClusterClientManager
// it links secrets informer to the client manager and notifies of changes
type multiClusterSecretEventHandler struct {
	logger           *zap.SugaredLogger
	clientManager    *dynamicDiscovery
	secretsNamespace string
}

func (h *multiClusterSecretEventHandler) OnAdd(obj interface{}, _ bool) {
	secret := h.castObjectToSecret(obj)
	if secret != nil {
		h.clientManager.syncClustersForSecret(secret)
	}
}

func (h *multiClusterSecretEventHandler) OnUpdate(_, newObj interface{}) {
	secret := h.castObjectToSecret(newObj)
	if secret != nil {
		h.clientManager.syncClustersForSecret(secret)
	}
}

func (h *multiClusterSecretEventHandler) OnDelete(obj interface{}) {
	secret := h.castObjectToSecret(obj)
	if secret != nil {
		h.clientManager.removeClustersFromSecret(secret)
	}
}

func (h *multiClusterSecretEventHandler) castObjectToSecret(obj interface{}) *corev1.Secret {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		h.logger.Error("Received a non-secret object: %T", obj)
		return nil
	}
	return secret
}

func (h *multiClusterSecretEventHandler) isMultiClusterSecret(obj interface{}) bool {
	accessor, _ := meta.Accessor(obj)
	secret := accessor

	if secret.GetNamespace() != h.secretsNamespace {
		return false
	}
	labels := secret.GetLabels()
	return labels[multiClusterSecretLabel] == "true"
}

func runSecretInformer(logger *zap.SugaredLogger, secretInformer corev1informers.SecretInformer, stopCh <-chan struct{}) bool {
	logger.Infof("Starting secret informer..")
	go wait.Until(func() { secretInformer.Informer().Run(stopCh) }, time.Second, stopCh)
	logger.Infof("Waiting for secret informer cache sync ..")
	return cache.WaitForCacheSync(stopCh, secretInformer.Informer().HasSynced)
}
