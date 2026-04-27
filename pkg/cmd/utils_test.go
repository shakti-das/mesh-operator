package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func TestReadFileOrDie(t *testing.T) {
	testCases := []struct {
		name          string
		filePath      string
		expectedError string
	}{
		{
			name:          "EmptyFilePath",
			filePath:      "",
			expectedError: "No path to resource specified: test resource",
		},
		{
			name:          "NonExistentPath",
			filePath:      "non-existent-path",
			expectedError: "Failed to open resource file.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Errorf("Expected panic")
				}
				assert.True(t, strings.HasPrefix(fmt.Sprint(r), tc.expectedError))
			}()

			logger := zaptest.NewLogger(t).Sugar()
			ReadFileOrDie(logger, tc.filePath, "test resource")
		})
	}
}
