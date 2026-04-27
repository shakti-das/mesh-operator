package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRootCmd(t *testing.T) {
	rootCmd := NewRootCmd([]string{})
	assert.Equal(t, "mesh-operator", rootCmd.Use)
	assert.Equal(t, "mesh-operator is a controller to operate on MeshOperator CRD", rootCmd.Short)
	assert.Equal(t, 4, len(rootCmd.Commands()))

	subCommands := []string{}
	for _, cmd := range rootCmd.Commands() {
		subCommands = append(subCommands, cmd.Name())
	}

	assert.ElementsMatch(t, []string{"server", "mutate", "render", "version"}, subCommands)
}
