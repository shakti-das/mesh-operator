package deployment

import (
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/common"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/tools/cache"
)

// GetDeploymentsByService - get all deployments backing given service
func GetDeploymentsByService(clients []kube.Client, namespace, serviceName string) ([]*appsv1.Deployment, error) {
	var objects []interface{}
	for _, client := range clients {
		deploymentInformer := client.KubeInformerFactory().Apps().V1().Deployments().Informer()
		deployments, err := deploymentInformer.GetIndexer().ByIndex(constants.ServiceIndexName, fmt.Sprintf("%s/%s", namespace, serviceName))
		if err != nil {
			return nil, err
		}

		objects = append(objects, deployments...)
	}

	var deployments []*appsv1.Deployment
	for _, obj := range objects {
		if d := obj.(*appsv1.Deployment); d != nil {
			deployments = append(deployments, d)
		}
	}
	return deployments, nil
}

func AddDeploymentIndexerIfNotExists(informer cache.SharedIndexInformer) error {
	_, alreadyExists := informer.GetIndexer().GetIndexers()[constants.ServiceIndexName]
	if !alreadyExists {
		return informer.AddIndexers(cache.Indexers{
			constants.ServiceIndexName: func(obj interface{}) ([]string, error) {
				if d := obj.(*appsv1.Deployment); d != nil {
					var serviceKey []string
					serviceName := common.GetLabelOrAlias(constants.DynamicRoutingServiceLabel, d.GetLabels())
					if serviceName != "" {
						serviceKey = append(serviceKey, fmt.Sprintf("%s/%s", d.GetNamespace(), serviceName))
					}
					return serviceKey, nil
				}
				return []string{}, nil
			},
		})
	}
	return nil
}
