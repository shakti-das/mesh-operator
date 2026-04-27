package kube

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

type GvkToGvrCache struct {
	cache  map[string]schema.GroupVersionResource
	rwLock sync.RWMutex
}

func NewGvkToGvrCache() *GvkToGvrCache {
	return &GvkToGvrCache{
		cache: make(map[string]schema.GroupVersionResource),
	}
}

var gvkToGvrCache = NewGvkToGvrCache()

func ConvertGvkToGvr(clusterName string, discoveryClient discovery.DiscoveryInterface, gvk schema.GroupVersionKind) (schema.GroupVersionResource, error) {

	cachedGvr, exists := getGvrFromCache(clusterName, gvk)
	if exists {
		return cachedGvr, nil
	}

	gvr, err := ConvertKindToResource(discoveryClient, gvk)
	if err != nil {
		return schema.GroupVersionResource{}, err
	}

	storeGvrInCache(clusterName, gvk, gvr)

	return gvr, nil
}

func getGvrFromCache(clusterName string, gvk schema.GroupVersionKind) (schema.GroupVersionResource, bool) {
	gvkToGvrCache.rwLock.RLock()
	defer gvkToGvrCache.rwLock.RUnlock()

	cacheKey := getCacheKey(clusterName, gvk)
	gvr, exists := gvkToGvrCache.cache[cacheKey]

	return gvr, exists
}

func storeGvrInCache(clusterName string, gvk schema.GroupVersionKind, gvr schema.GroupVersionResource) {
	gvkToGvrCache.rwLock.Lock()
	defer gvkToGvrCache.rwLock.Unlock()

	cacheKey := getCacheKey(clusterName, gvk)
	gvkToGvrCache.cache[cacheKey] = gvr
}

func getCacheKey(clusterName string, gvk schema.GroupVersionKind) string {
	return fmt.Sprintf("%s/%s", clusterName, gvk.String())
}
func ClearGvkToGvrCache() {
	for key := range gvkToGvrCache.cache {
		delete(gvkToGvrCache.cache, key)
	}
}
