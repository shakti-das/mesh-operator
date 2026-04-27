package kube

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
)

// AddServiceIndexer registers an indexer on the service informer.
// Index values are built by joining label values with "/".
// Must be called before the informer starts. Safe to call multiple times.
func AddServiceIndexer(client Client, indexName string, labels []string) error {
	informer := client.KubeInformerFactory().Core().V1().Services().Informer()

	if _, exists := informer.GetIndexer().GetIndexers()[indexName]; exists {
		return nil
	}

	return informer.AddIndexers(cache.Indexers{
		indexName: func(obj interface{}) ([]string, error) {
			svc, ok := obj.(*corev1.Service)
			if !ok {
				return nil, fmt.Errorf("expected *corev1.Service but got %T", obj)
			}
			values := make([]string, 0, len(labels))
			for _, label := range labels {
				val, exists := svc.Labels[label]
				if !exists || val == "" {
					return nil, nil
				}
				values = append(values, val)
			}
			return []string{strings.Join(values, "/")}, nil
		},
	})
}

// GetServicesByIndex retrieves services matching the given index name and value.
func GetServicesByIndex(client Client, indexName string, indexValue string) ([]*corev1.Service, error) {
	informer := client.KubeInformerFactory().Core().V1().Services().Informer()

	objs, err := informer.GetIndexer().ByIndex(indexName, indexValue)
	if err != nil {
		return nil, fmt.Errorf("failed to query services by index %s=%s: %w", indexName, indexValue, err)
	}

	services := make([]*corev1.Service, 0, len(objs))
	for _, obj := range objs {
		if svc, ok := obj.(*corev1.Service); ok {
			services = append(services, svc)
		}
	}
	return services, nil
}
