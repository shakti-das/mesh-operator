package templating

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseTemplateMetadata(t *testing.T) {
	testCases := []struct {
		name               string
		yamlContent        string
		expectedMutation   string
		expectedReMutation string
		expectError        bool
	}{
		{
			name: "valid metadata with both templates",
			yamlContent: `
rollout:
  mutationTemplate: blueGreen
  reMutationTemplate: reMutateBlueGreen
`,
			expectedMutation:   "blueGreen",
			expectedReMutation: "reMutateBlueGreen",
			expectError:        false,
		},
		{
			name: "valid metadata with only mutation template",
			yamlContent: `
rollout:
  mutationTemplate: canary
`,
			expectedMutation:   "canary",
			expectedReMutation: "",
			expectError:        false,
		},
		{
			name: "empty rollout section",
			yamlContent: `
rollout:
`,
			expectedMutation:   "",
			expectedReMutation: "",
			expectError:        false,
		},
		{
			name:               "empty content",
			yamlContent:        "",
			expectedMutation:   "",
			expectedReMutation: "",
			expectError:        false,
		},
		{
			name: "invalid yaml",
			yamlContent: `
rollout:
  mutationTemplate: [invalid
`,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			metadata, err := ParseTemplateMetadata([]byte(tc.yamlContent))

			if tc.expectError {
				assert.NotNil(t, err)
				return
			}

			assert.Nil(t, err)
			assert.NotNil(t, metadata)

			if tc.expectedMutation != "" || tc.expectedReMutation != "" {
				assert.NotNil(t, metadata.Rollout)
				assert.Equal(t, tc.expectedMutation, metadata.Rollout.MutationTemplate)
				assert.Equal(t, tc.expectedReMutation, metadata.Rollout.ReMutationTemplate)
			}
		})
	}
}
