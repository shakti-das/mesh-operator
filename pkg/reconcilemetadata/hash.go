package reconcilemetadata

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
)

// ReconcileHashManager is responsible for computing reconciliation hash for different resources
type ReconcileHashManager interface {
	ComputeObjectHash(ownerObject interface{}, kind string) string
	ComputeHashKey(clusterName, kind, namespace, name string) string
	GetConfigTemplateHash() string
}

type reconcileHashManager struct {
}

func NewReconcileHashManager() ReconcileHashManager {
	return &reconcileHashManager{}
}

func (rm *reconcileHashManager) ComputeObjectHash(object interface{}, kind string) string {
	metaObject := common.GetMetaObject(object)
	switch kind {
	case constants.ServiceKind.Kind, constants.ServiceEntryKind.Kind:
		return metaObject.GetResourceVersion()
	case constants.StatefulSetKind.Kind, constants.RolloutKind.Kind:
		generation := strconv.FormatInt(metaObject.GetGeneration(), 10)
		uuid := metaObject.GetUID()

		return fmt.Sprintf("%s-%s", uuid, generation)
	case constants.DeploymentKind.Kind:
		generation := strconv.FormatInt(metaObject.GetGeneration(), 10)
		uuid := metaObject.GetUID()
		labelHash := "x"

		if metaObject.GetLabels() != nil {
			labelVal := common.GetLabelOrAlias(constants.DynamicRoutingServiceLabel, metaObject.GetLabels())
			if labelVal != "" {
				labelHash = fmt.Sprintf("%x", sha256.Sum256([]byte(labelVal)))
			}
		}
		return fmt.Sprintf("%s-%s-%s", uuid, generation, labelHash)
	default:
		return ""
	}
}

func (rm *reconcileHashManager) ComputeHashKey(clusterName, kind, namespace, name string) string {
	return fmt.Sprintf("%s_%s_%s_%s", clusterName, kind, namespace, name)
}

func (rm *reconcileHashManager) GetConfigTemplateHash() string {
	return features.MopConfigTemplatesHash
}
