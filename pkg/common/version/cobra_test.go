package version

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestOpts(t *testing.T) {
	ordinaryCmd := CobraCommand()

	cases := []struct {
		args       string
		cmd        *cobra.Command
		expectFail bool
	}{
		{
			"version",
			ordinaryCmd,
			false,
		},
		{
			"version --short",
			ordinaryCmd,
			false,
		},
		{
			"version --output yaml",
			ordinaryCmd,
			false,
		},
		{
			"version --output json",
			ordinaryCmd,
			false,
		},
		{
			"version --output xuxa",
			ordinaryCmd,
			true,
		},
	}

	for _, v := range cases {
		t.Run(v.args, func(t *testing.T) {
			v.cmd.SetArgs(strings.Split(v.args, " "))
			var out bytes.Buffer
			v.cmd.SetOutput(&out)
			err := v.cmd.Execute()

			if !v.expectFail && err != nil {
				t.Errorf("Got %v, expecting success", err)
			}
			if v.expectFail && err == nil {
				t.Errorf("Expected failure, got success")
			}
		})
	}
}

func TestVersion(t *testing.T) {
	cases := []struct {
		args           []string
		err            error
		expectFail     bool
		expectedOutput string         // Expected constant output
		expectedRegexp *regexp.Regexp // Expected regexp output
	}{
		{
			args: strings.Split("version --short=false", " "),
			expectedRegexp: regexp.MustCompile("version.BuildInfo{Version:\"unknown\", BuildTimestamp:\"unknown\", " +
				"GolangVersion:\"go1.([0-9+?(\\.)?]+)(rc[0-9]?)?(beta[0-9]?)?.+(X:nocoverageredesign)?\", " +
				"BuildStatus:\"unknown\"}"),
		},
		{
			args:           strings.Split("version -s", " "),
			expectedOutput: "unknown-unknown-unknown\n",
		},
		{
			args: strings.Split("version -o yaml", " "),
			expectedRegexp: regexp.MustCompile(
				"golang_version: go1.([0-9+?(\\.)?]+)(rc[0-9]?)?(beta[0-9]?)?.+(X:nocoverageredesign)?\n" +
					"status: unknown\n" +
					"timestamp: unknown\n" +
					"version: unknown\n\n"),
		},
		{
			args: strings.Split("version -o json", " "),
			expectedRegexp: regexp.MustCompile("{\n" +
				"  \"version\": \"unknown\",\n" +
				"  \"timestamp\": \"unknown\",\n" +
				"  \"golang_version\": \"go1.([0-9+?(\\.)?]+)(rc[0-9]?)?(beta[0-9]?)?.+(X:nocoverageredesign)?\",\n" +
				"  \"status\": \"unknown\"\n" +
				"}\n"),
		},
		{
			args:           strings.Split("version --typo", " "),
			expectedRegexp: regexp.MustCompile("Error: unknown flag: --typo\n"),
			expectFail:     true,
		},

		{
			args:           strings.Split("version --output xyz", " "),
			expectedRegexp: regexp.MustCompile("Error: --output must be 'yaml' or 'json'\n"),
			expectFail:     true,
		},
	}

	for i, v := range cases {
		t.Run(fmt.Sprintf("case %d %s", i, strings.Join(v.args, " ")), func(t *testing.T) {
			cmd := CobraCommand()
			var out bytes.Buffer
			cmd.SetOutput(&out)
			cmd.SetArgs(v.args)
			err := cmd.Execute()
			output := out.String()

			if v.expectedOutput != "" && v.expectedOutput != output {
				t.Fatalf("Unexpected output for '%s'\n got: %q\nwant: %q",
					strings.Join(v.args, " "), output, v.expectedOutput)
			}

			if v.expectedRegexp != nil && !v.expectedRegexp.MatchString(output) {
				t.Fatalf("Output didn't match for '%s'\n got: %v\nwant: %v",
					strings.Join(v.args, " "), output, v.expectedRegexp)
			}

			if !v.expectFail && err != nil {
				t.Errorf("Got %v, expecting success", err)
			}
			if v.expectFail && err == nil {
				t.Errorf("Expected failure, got success")
			}
		})
	}
}
