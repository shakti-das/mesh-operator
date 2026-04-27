package version

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

// CobraCommand returns a command used to print version information.
func CobraCommand() *cobra.Command {
	var (
		short   bool
		output  string
		version *BuildInfo
	)

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Prints out build version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "" && output != "yaml" && output != "json" {
				return errors.New(`--output must be 'yaml' or 'json'`)
			}

			version = &Info

			switch output {
			case "":
				if short {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", version)
				} else {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", version.LongForm())
				}
			case "yaml":
				if marshaled, err := yaml.Marshal(&version); err == nil {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(marshaled))
				}
			case "json":
				if marshaled, err := json.MarshalIndent(&version, "", "  "); err == nil {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(marshaled))
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&short, "short", "s", false, "Use --short=false to generate full version information")
	cmd.Flags().StringVarP(&output, "output", "o", "", "One of 'yaml' or 'json'.")

	return cmd
}
