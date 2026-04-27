package http

// WARNING: DO NOT USE THIS!
// See W-13479099
//
// Use of this utility is very problematic. It has an implicit dependency on a
// system installed tool (curl), which is not tracked in any build metadata.
// If you use this utility in some environments, curl may not be there and this
// call will fail. Or, the version of curl might be too old and not support an
// option you rely on (e.g. http2).
//
// Currently, this is only used in tests, which is some relief. But even that is
// a problem because SFCI can change their base image and break your test.

import (
	"os"
	"os/exec"
)

// Request curl with given input options and headers
func Request(config *Config) (string, error) {
	arguments := config.Headers
	arguments = append(arguments, config.Options...)
	arguments = append(arguments, config.Url)
	cmd := exec.Command("curl", arguments...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, config.EnvVars...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

type Config struct {
	Url     string
	Headers []string
	Options []string
	EnvVars []string
}
