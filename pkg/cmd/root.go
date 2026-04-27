package cmd

import (
	"flag"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/version"
	"github.com/spf13/cobra"
)

func NewRootCmd(args []string) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "mesh-operator",
		Short: "mesh-operator is a controller to operate on MeshOperator CRD",
		Args:  cobra.ExactArgs(0),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	rootCmd.SetArgs(args)
	rootCmd.PersistentFlags().AddGoFlagSet(flag.CommandLine)

	rootCmd.AddCommand(NewDefaultServerCommand())
	rootCmd.AddCommand(NewTemplateRendererCommand())
	rootCmd.AddCommand(NewMutateCommand())
	rootCmd.AddCommand(version.CobraCommand())

	return rootCmd
}
