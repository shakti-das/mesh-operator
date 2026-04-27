package statefulset

import (
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/tools/cache"
)

// GetStatefulSetByService returns all statefulSets which are referencing specified service
func GetStatefulSetByService(clients []kube.Client, namespace string, serviceName string) ([]*appsv1.StatefulSet, error) {
	var objs []interface{}
	for _, client := range clients {
		stsInformer := client.KubeInformerFactory().Apps().V1().StatefulSets().Informer()
		stss, err := stsInformer.GetIndexer().ByIndex(constants.ServiceIndexName, fmt.Sprintf("%s/%s", namespace, serviceName))
		if err != nil {
			return nil, err
		}

		objs = append(objs, stss...)
	}

	var statefulSets []*appsv1.StatefulSet
	for _, obj := range objs {
		if sts := obj.(*appsv1.StatefulSet); sts != nil {
			statefulSets = append(statefulSets, sts)
		}
	}
	return statefulSets, nil
}

func AddStsIndexerIfNotExists(informer cache.SharedIndexInformer) error {
	_, alreadyExists := informer.GetIndexer().GetIndexers()[constants.ServiceIndexName]
	if !alreadyExists {
		return informer.AddIndexers(cache.Indexers{
			constants.ServiceIndexName: func(obj interface{}) (strings []string, e error) {
				var s []string
				sts := obj.(*appsv1.StatefulSet)
				if sts == nil {
					return nil, e
				}
				pair := fmt.Sprintf("%s/%s", sts.Namespace, sts.Spec.ServiceName)
				s = append(s, pair)
				return s, nil
			},
		})
	}
	return nil
}
