package resources

import (
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func TestDryRunResourceManager(t *testing.T) {
	var clusterName = "test-cluster"
	var service = kube_test.CreateService("test-namespace", "svc-1")
	manager := NewDryRunResourceManager(zaptest.NewLogger(t).Sugar())

	logger := zaptest.NewLogger(t).Sugar()

	ownerService := ServiceToReference(clusterName, service)
	msm, err := manager.TrackOwner(clusterName, "test-namespace", ownerService, logger)
	assert.Nil(t, err)
	assert.NotNil(t, msm)

	client := kube_test.NewKubeClientBuilder().Build()

	err = manager.OnOwnerChange(clusterName, client, msm, service, ownerService, nil, logger)
	assert.Nil(t, err)

	err = manager.UnTrackOwner(clusterName, "test-namespace", "Service", "svc-1", "uid-1", logger)
	assert.Nil(t, err)
}
