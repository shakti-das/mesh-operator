package ocm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIsOCMManagedObject(t *testing.T) {
	testCases := []struct {
		name           string
		labels         map[string]string
		expectedResult bool
	}{
		{
			name:           "OCM managed object",
			labels:         map[string]string{ManagedByLabel: ManagedByValue},
			expectedResult: true,
		},
		{
			name:           "Wrong label value",
			labels:         map[string]string{ManagedByLabel: "other"},
			expectedResult: false,
		},
		{
			name:           "Missing label",
			labels:         map[string]string{"some-other-label": "ocm"},
			expectedResult: false,
		},
		{
			name:           "No labels",
			labels:         nil,
			expectedResult: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{}
			obj.SetLabels(tc.labels)

			result := IsOcmManagedObject(obj)

			assert.Equal(t, tc.expectedResult, result)
		})
	}
}

func TestNilIsOCMManagedObject(t *testing.T) {
	assert.False(t, IsOcmManagedObject(nil))
}
