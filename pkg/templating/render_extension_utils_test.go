package templating

import (
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestGetFilterType(t *testing.T) {
	testCases := []struct {
		name                       string
		filter                     v1alpha1.ExtensionElement
		expectedExtensionType      string
		expectedExtensionFieldName string
		expectedErrorString        string
	}{
		{
			name: "FaultFilter",
			filter: v1alpha1.ExtensionElement{
				FaultFilter: &v1alpha1.HttpFaultFilter{
					Delay: &v1alpha1.HttpFaultFilterDelay{
						FixedDelay: "1s",
					},
				},
			},
			expectedExtensionType:      "faultInjection",
			expectedExtensionFieldName: "FaultFilter",
		},
		{
			name: "UnsupportedFilter",
			filter: v1alpha1.ExtensionElement{
				FaultFilter: nil,
			},
			expectedExtensionType: "",
			expectedErrorString:   "extension type not supported by MeshOperator",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			extensionType, extensionFieldName, err := getExtensionTypeAndFieldName(tc.filter)
			assert.Equal(t, tc.expectedExtensionType, extensionType)
			assert.Equal(t, tc.expectedExtensionFieldName, extensionFieldName)
			if tc.expectedErrorString != "" {
				assert.EqualError(t, err, tc.expectedErrorString)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestRetrieveFilterIndex(t *testing.T) {
	testCases := []struct {
		name          string
		filterName    string
		expectedIndex string
	}{
		{
			name:          "getIndex",
			filterName:    "mop-12345-7",
			expectedIndex: "7",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			index := retrieveExtensionIndex(tc.filterName)
			assert.Equal(t, tc.expectedIndex, index)
		})
	}
}
