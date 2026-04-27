package reconcilemetadata

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
)

func TestComputeServiceHash(t *testing.T) {
	serviceResourceVersion := "1234"
	svc := kube_test.NewServiceBuilder("my-service-name", "remote-namespace").SetResourceVersion(serviceResourceVersion).Build()

	reconcileHashMgr := NewReconcileHashManager()
	hash := reconcileHashMgr.ComputeObjectHash(&svc, "Service")
	assert.Equal(t, serviceResourceVersion, hash)
}

func TestComputeServiceEntryHash(t *testing.T) {
	serviceResourceVersion := "1234"
	testSe := kube_test.NewServiceEntryBuilder("my-service-entry", "remote-namespace").SetResourceVersion(serviceResourceVersion).Build()

	reconcileHashMgr := NewReconcileHashManager()
	hash := reconcileHashMgr.ComputeObjectHash(&testSe, "ServiceEntry")
	assert.Equal(t, serviceResourceVersion, hash)
}

func TestComputeStatefulsetHash(t *testing.T) {
	testSts := kube_test.NewStatefulSetBuilder("test-namespace", "sts1", "svc1", 2).
		SetGeneration(23).
		Build()
	testSts.SetUID("cdedab11-b318-4031-b288-3e2e0fc9a7c9")

	reconcileHashMgr := NewReconcileHashManager()
	hash := reconcileHashMgr.ComputeObjectHash(testSts, "StatefulSet")
	assert.Equal(t, "cdedab11-b318-4031-b288-3e2e0fc9a7c9-23", hash)
}

func TestComputeRolloutHash(t *testing.T) {
	testRollout := kube_test.NewRolloutBuilder("rollout1", "namespace").
		SetGeneration(23).
		Build()
	testRollout.SetUID("cdedab11-b318-4031-b288-3e2e0fc9a7c9")

	reconcileHashMgr := NewReconcileHashManager()
	hash := reconcileHashMgr.ComputeObjectHash(testRollout, "Rollout")
	assert.Equal(t, "cdedab11-b318-4031-b288-3e2e0fc9a7c9-23", hash)
}

func TestComputeDeploymentHash(t *testing.T) {
	deploymentWithoutLabel := kube_test.NewDeploymentBuilder("namespace", "deploy1").
		SetGeneration(23).
		Build()
	deploymentWithoutLabel.SetUID("cdedab11-b318-4031-b288-3e2e0fc9a7c9")

	reconcileHashMgr := NewReconcileHashManager()
	hash1 := reconcileHashMgr.ComputeObjectHash(deploymentWithoutLabel, "Deployment")
	assert.Equal(t, "cdedab11-b318-4031-b288-3e2e0fc9a7c9-23-x", hash1)

	deploymentWithLabel := kube_test.NewDeploymentBuilder("namespace", "deploy2").
		SetGeneration(23).
		SetLabels(map[string]string{string(constants.DynamicRoutingServiceLabel): "test-svc"}).
		Build()
	deploymentWithLabel.SetUID("cdedab11-b318-4031-b288-3e2e0fc9a7c9")

	labelHash := sha256.Sum256([]byte("test-svc"))
	expectedHash := fmt.Sprintf("cdedab11-b318-4031-b288-3e2e0fc9a7c9-%d-%x", 23, labelHash)

	hash2 := reconcileHashMgr.ComputeObjectHash(deploymentWithLabel, "Deployment")

	assert.Equal(t, expectedHash, hash2)

}

func TestComputeHashForUnknownKind(t *testing.T) {
	testMop := kube_test.NewMopBuilder("mop", "namespace").SetResourceVersion("1234").Build()

	reconcileHashMgr := NewReconcileHashManager()
	hash := reconcileHashMgr.ComputeObjectHash(testMop, "MeshOperator")
	assert.Equal(t, "", hash)
}

func TestComputeHashKey(t *testing.T) {

	cluster := "primary"
	kind := "Service"
	namespace := "my-namespace"
	name := "my-service"

	expectedHashKey := "primary_Service_my-namespace_my-service"
	reconcileHashMgr := NewReconcileHashManager()

	actualHashKey := reconcileHashMgr.ComputeHashKey(cluster, kind, namespace, name)
	assert.Equal(t, expectedHashKey, actualHashKey)

}
