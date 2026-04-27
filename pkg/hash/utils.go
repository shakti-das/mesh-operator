package hash

import (
	"fmt"
	"strings"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
)

func ReadHash(isConfigNamespace bool, mop *v1alpha1.MeshOperator, object *unstructured.Unstructured) string {
	if object == nil {
		if isConfigNamespace {
			return mop.Status.NamespaceHash
		}
		return ""
	}

	if object.GetKind() == constants.ServiceKind.Kind {
		if serviceStatus, exists := mop.Status.Services[object.GetName()]; exists {
			return serviceStatus.Hash
		}
	} else {
		if seStatus, exists := mop.Status.ServiceEntries[object.GetName()]; exists {
			return seStatus.Hash
		}
	}
	return ""
}

func UpdateMopHashMap(mop *v1alpha1.MeshOperator, objectKind string, objectName string, objectHash string) {

	var statusMap map[string]*v1alpha1.ServiceStatus
	if objectKind == constants.ServiceKind.Kind {
		if mop.Status.Services == nil {
			mop.Status.Services = make(map[string]*v1alpha1.ServiceStatus)
		}
		statusMap = mop.Status.Services
	} else {
		if mop.Status.ServiceEntries == nil {
			mop.Status.ServiceEntries = make(map[string]*v1alpha1.ServiceStatus)
		}
		statusMap = mop.Status.ServiceEntries
	}
	if serviceStatus, exists := statusMap[objectName]; !exists {
		statusMap[objectName] = &v1alpha1.ServiceStatus{Hash: objectHash}
	} else {
		serviceStatus.Hash = objectHash
	}

}

func RemoveExtensionSourceForCluster(isConfigNsResource bool, clusterName, svcNamespace, existingExtensionSource string) (string, bool) {
	if existingExtensionSource == "" {
		return "", false
	}
	existingElements := strings.Split(existingExtensionSource, ",")
	newElements := make([]string, 0)
	searchPrefix := clusterName
	if isConfigNsResource {
		searchPrefix = GetExtensionSourcePrefixForConfigNs(clusterName, svcNamespace)
	}
	searchPrefixWithSlash := fmt.Sprintf("%s/", searchPrefix)

	somePrefixFound := false
	for _, element := range existingElements {
		if element == searchPrefix || strings.HasPrefix(element, searchPrefixWithSlash) {
			somePrefixFound = true
		} else {
			newElements = append(newElements, element)
		}
	}
	return strings.Join(newElements, ","), somePrefixFound
}

func GetExtensionSourcePrefixForConfigNs(clusterName, svcNamespace string) string {
	return fmt.Sprintf("%s/%s", clusterName, svcNamespace)
}

// GetExtensionSource - composes a value of the extensionSource annotation
// If service is not provided - we're dealing with a namespace-wide MeshOperator record
// Namespace wide MOP - "cluster-name"
// Config-namespace MOP - "cluster-name/namespace/service-name/kind"
// Ordinary same-namespace MOP - "cluster-name/service-name/kind"
// In case of Service Resource, `kind` is not included in source annotation
func GetExtensionSource(clusterName, mopNamespace string, object *unstructured.Unstructured, isConfigNamespaceExtension bool) string {
	if object != nil {
		return getExtensionSourceForObject(clusterName, object, isConfigNamespaceExtension)
	}
	return getExtensionSourceForNamespaceWideMop(clusterName, mopNamespace, isConfigNamespaceExtension)
}

func getExtensionSourceForObject(clusterName string, object *unstructured.Unstructured, isConfigNamespaceExtension bool) string {
	// if flag is enabled, include `Kind` in extensionSource annotation for non-Service objects
	if object.GetKind() != constants.ServiceKind.Kind {
		if isConfigNamespaceExtension {
			return fmt.Sprintf("%s/%s/%s/%s", clusterName, object.GetNamespace(), object.GetName(), object.GetKind())
		} else {
			return fmt.Sprintf("%s/%s/%s", clusterName, object.GetName(), object.GetKind())
		}
	}

	if isConfigNamespaceExtension {
		return fmt.Sprintf("%s/%s/%s", clusterName, object.GetNamespace(), object.GetName())
	}
	return fmt.Sprintf("%s/%s", clusterName, object.GetName())
}

func getExtensionSourceForNamespaceWideMop(clusterName string, mopNamespace string, isConfigNamespaceExtension bool) string {
	if isConfigNamespaceExtension {
		return fmt.Sprintf("%s/%s", clusterName, mopNamespace)
	} else {
		return clusterName
	}
}

func IsExtensionSourceConflicts(configNamespaceExtension bool, objectKind, objectNamespace, objectName, extensionSourceInCluster string) bool {
	existingObjectKey := strings.Split(extensionSourceInCluster, ",")[0]
	objectKey := objectName

	if len(objectName) > 0 {
		objectKey = getObjectKey(configNamespaceExtension, objectKind, objectNamespace, objectName)
	} else {
		if configNamespaceExtension {
			objectKey = objectNamespace
		}
	}
	return !strings.HasSuffix(existingObjectKey, objectKey)
}

func getObjectKey(configNamespaceExtension bool, objectKind, objectNamespace, objectName string) string {
	if objectKind != constants.ServiceKind.Kind {
		if configNamespaceExtension {
			return fmt.Sprintf("%s/%s/%s", objectNamespace, objectName, objectKind)
		}
		return fmt.Sprintf("%s/%s", objectName, objectKind)
	}
	if configNamespaceExtension {
		return fmt.Sprintf("%s/%s", objectNamespace, objectName)
	}
	return fmt.Sprintf("%s", objectName)
}

func ComposeExtensionName(mopName, hashToUse, extensionIndex string) string {
	var extensionName = mopName + "-" + extensionIndex
	if hashToUse != "" {
		extensionName = mopName + "-" + hashToUse + "-" + extensionIndex
	}

	return extensionName
}

func IsConfigNamespaceExtension(config *unstructured.Unstructured) (bool, string) {
	if configNamespace, found := config.GetAnnotations()[constants.ConfigNamespaceExtensionAnnotation]; found {
		return true, configNamespace
	}

	return false, ""
}
