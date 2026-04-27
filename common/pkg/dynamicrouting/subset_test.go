package dynamicrouting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetUniqueLabelSubsets(t *testing.T) {
	testCases := []struct {
		name                 string
		subsetLabelKeys      []string
		deploymentLabels     []map[string]string
		expectedSubsetLabels map[string]map[string]string
	}{
		{
			"SubsetAdded",
			[]string{"version", "user"},
			[]map[string]string{{
				"version": "v1", "user": "u1",
			}, {
				"version": "v1", "user": "u2",
			}},
			map[string]map[string]string{
				"v1-u1": {"user": "u1", "version": "v1"},
				"v1-u2": {"user": "u2", "version": "v1"},
			},
		},
		{
			"PartialSubsetDiscarded",
			[]string{"version", "user"},
			[]map[string]string{{
				"version": "v1", "user": "u1",
			}, {
				"user": "u2",
			}, {
				"version": "v3",
			}, {
				"version": "v4", "user": "u4",
			}},
			map[string]map[string]string{
				"v1-u1": {"user": "u1", "version": "v1"},
				"v4-u4": {"user": "u4", "version": "v4"},
			},
		},
		{
			"EmptySubset",
			[]string{"version", "user"},
			[]map[string]string{{
				"vers": "v1", "user": "u1",
			}, {
				"ver": "v1", "user": "u2",
			}},
			map[string]map[string]string{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			subsetLabels := GetUniqueLabelSubsets(tc.subsetLabelKeys, tc.deploymentLabels)
			assert.Equal(t, tc.expectedSubsetLabels, subsetLabels)
		})
	}
}
