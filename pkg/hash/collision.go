package hash

import (
	"context"
	"fmt"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"go.uber.org/zap"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collisionExists checks if filter collision exists with objects living in cluster
func collisionExists(logger *zap.SugaredLogger, configNamespaceExtension bool, objectKind, objectNamespace, objectName string, objectLookupNamespace string, objectLookUpName string, kubeClient kube.Client, groupVersionKind schema.GroupVersionKind) (bool, error) {
	groupVersionResource, err := kube.ConvertGvkToGvr(kubeClient.GetClusterName(), kubeClient.Discovery(), groupVersionKind)
	if err != nil {
		return false, fmt.Errorf("unable to access existing filter: %s/%s, error: %w", objectLookupNamespace, objectLookUpName, err)
	}
	filterInCluster, err := kubeClient.Dynamic().Resource(groupVersionResource).Namespace(objectLookupNamespace).Get(context.TODO(), objectLookUpName, metav1.GetOptions{})
	switch {
	case err == nil:
		// resource exist with the same name in cluster, check for service reference
		// if both the objects reference the same service object - that means no collision, otherwise collision exists
		filterInClusterAnnotations := filterInCluster.GetAnnotations()
		extensionSourceInCluster := filterInClusterAnnotations[constants.ExtensionSourceAnnotation]
		if extensionSourceInCluster == "" {
			return false, fmt.Errorf("conflicting filter in cluster %s/%s", objectLookupNamespace, objectLookUpName)
		}

		if IsExtensionSourceConflicts(configNamespaceExtension, objectKind, objectNamespace, objectName, extensionSourceInCluster) {
			logger.Infof("found hash collision for filter %s/%s (linked to svc: %s) with another in cluster filter (linked to svc: %s)", objectLookupNamespace, objectLookUpName, objectName, extensionSourceInCluster)
			return true, nil
		}
		return false, nil
	case k8serrors.IsNotFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("unable to access existing filter: %s/%s, error: %w", objectLookupNamespace, objectLookUpName, err)
	}
}
