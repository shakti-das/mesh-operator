package dynamicrouting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetLabelCombinationsByKind(t *testing.T) {
	tests := []struct {
		name                        string
		deploymentLabelCombinations map[string]map[string]string
		rolloutLabelCombinations    map[string]map[string]string
		expectedOutput              map[string]map[string]map[string]string
	}{
		{
			name:           "Both deployment and rollout label combinations are nil",
			expectedOutput: map[string]map[string]map[string]string{},
		},
		{
			name:                        "Both deployment and rollout label combinations are empty",
			deploymentLabelCombinations: map[string]map[string]string{},
			rolloutLabelCombinations:    map[string]map[string]string{},
			expectedOutput:              map[string]map[string]map[string]string{},
		},
		{
			name: "Only deployment label combinations exists",
			deploymentLabelCombinations: map[string]map[string]string{
				"v1": {"version": "v1"},
				"v2": {"version": "v2"},
			},
			expectedOutput: map[string]map[string]map[string]string{
				"Deployment": {
					"v1": {"version": "v1"},
					"v2": {"version": "v2"},
				},
			},
		},
		{
			name: "Only rollout label combinations exists",
			rolloutLabelCombinations: map[string]map[string]string{
				"v3": {"version": "v3"},
				"v4": {"version": "v4"},
			},
			deploymentLabelCombinations: map[string]map[string]string{},
			expectedOutput: map[string]map[string]map[string]string{
				"Rollout": {
					"v3": {"version": "v3"},
					"v4": {"version": "v4"},
				},
			},
		},
		{
			name: "Both deployment and rollout label combinations exists",
			deploymentLabelCombinations: map[string]map[string]string{
				"v1": {"version": "v1"},
				"v2": {"version": "v2"},
			},
			rolloutLabelCombinations: map[string]map[string]string{
				"v3": {"version": "v3"},
				"v4": {"version": "v4"},
			},
			expectedOutput: map[string]map[string]map[string]string{
				"Deployment": {
					"v1": {"version": "v1"},
					"v2": {"version": "v2"},
				},
				"Rollout": {
					"v3": {"version": "v3"},
					"v4": {"version": "v4"},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output := GetLabelCombinationsByKind(tc.deploymentLabelCombinations, tc.rolloutLabelCombinations)
			assert.Equal(t, tc.expectedOutput, output)
		})
	}
}
