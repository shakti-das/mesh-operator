package features

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultValue(t *testing.T) {
	assert.Equal(t, getBooleanEnvValue("dummy", false), false)
}

func TestEnvLookUp(t *testing.T) {
	os.Setenv("dummy", "true")
	assert.Equal(t, getBooleanEnvValue("dummy", false), true)
}

func TestParseErr(t *testing.T) {
	os.Setenv("dummy", "2")
	assert.Equal(t, getBooleanEnvValue("dummy", false), false)
}

func TestGetStringValueOrDefault(t *testing.T) {
	os.Setenv("dummy", "some-value")
	assert.Equal(t, "some-value", getStringEnvValueOrDefault("dummy", "default-value"))

	os.Setenv("dummy", "")
	assert.Equal(t, "default-value", getStringEnvValueOrDefault("dummy", "default-value"))
}

func TestGetIntValueOrDefault(t *testing.T) {
	assert.Equal(t, 5, getIntEnvValueOrDefault("non-existent", 5))

	os.Setenv("dummy-int", "3")
	assert.Equal(t, 3, getIntEnvValueOrDefault("dummy-int", 5))

	os.Setenv("invalid-int", "invalid-value")
	assert.Equal(t, 5, getIntEnvValueOrDefault("invalid-int", 5))
}

func TestGetSetEnvValue(t *testing.T) {
	testCases := []struct {
		name         string
		envValue     string
		expectedSet  map[string]struct{}
		shouldSetEnv bool
	}{
		{
			name:         "Environment variable not set",
			envValue:     "",
			expectedSet:  map[string]struct{}{},
			shouldSetEnv: false,
		},
		{
			name:         "Empty environment variable",
			envValue:     "",
			expectedSet:  map[string]struct{}{},
			shouldSetEnv: true,
		},
		{
			name:     "Single value",
			envValue: "namespace1",
			expectedSet: map[string]struct{}{
				"namespace1": {},
			},
			shouldSetEnv: true,
		},
		{
			name:     "Multiple values with comma",
			envValue: "namespace1,namespace2,namespace3",
			expectedSet: map[string]struct{}{
				"namespace1": {},
				"namespace2": {},
				"namespace3": {},
			},
			shouldSetEnv: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			envVar := "TEST_SET_ENV_VAR"

			// Clean up environment variable
			defer os.Unsetenv(envVar)

			if tc.shouldSetEnv {
				os.Setenv(envVar, tc.envValue)
			}

			result := getStringSetEnvValue(envVar)

			// Check that the result has the same length as expected
			assert.Equal(t, len(tc.expectedSet), len(result), "Set should have expected number of elements")

			// Check that all expected elements are present
			for expectedKey := range tc.expectedSet {
				_, exists := result[expectedKey]
				assert.True(t, exists, "Expected key '%s' should exist in result set", expectedKey)
			}
		})
	}
}
