package cmd

import (
	"testing"

	"github.com/istio-ecosystem/mesh-operator/controllers"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/stretchr/testify/assert"
)

func TestCreateServicePolicyConfig_none(t *testing.T) {
	config := createServicePolicyConfig()

	assert.Empty(t, config)
}

func TestCreateServicePolicyConfig_ctp(t *testing.T) {
	features.EnableMulticlusterCTP = true
	defer func() {
		features.EnableMulticlusterCTP = false
	}()

	config := createServicePolicyConfig()

	assert.Equal(t, config, map[string]controllers.AdditionalObject{
		"clusterTrafficPolicy": {
			Group:    "mesh.io",
			Version:  "v1alpha1",
			Resource: "clustertrafficpolicies",
			Lookup: controllers.Lookup{
				BySvcNameAndNamespace: true,
			},
		},
	})
}

func TestCreateServicePolicyConfig_tsp(t *testing.T) {
	features.EnableTrafficShardingPolicyController = true
	defer func() {
		features.EnableTrafficShardingPolicyController = false
	}()

	config := createServicePolicyConfig()

	// TSP is now handled by dedicated TrafficShardController, not as a service policy
	// So the config should be empty even when TSP is enabled
	assert.Empty(t, config)
}
