package http

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTPRequest(t *testing.T) {
	// Skipped: this test hits an external HTTP endpoint and fails in sandboxed
	// CI environments with "HTTP/1.1 426 Upgrade Required". Unskip locally to
	// reproduce.
	t.Skip("skipping: requires outbound HTTP connectivity")

	testCases := []struct {
		name     string
		config   *Config
		contains string
	}{
		{
			"Simple Get",
			&Config{
				Url:     "www.google.com",
				Headers: []string{""},
				Options: []string{""},
				EnvVars: []string{""},
			},
			"",
		},
		{
			"Http 1.0 get",
			&Config{
				Url:     "www.google.com",
				Headers: []string{""},
				Options: []string{"--http1.0"},
				EnvVars: []string{""},
			},
			"",
		},
		{
			"Options with Http 1.0 get",
			&Config{
				Url:     "www.google.com",
				Headers: []string{""},
				Options: []string{"-k", "-I", "--http1.0"},
				EnvVars: []string{""},
			},
			"HTTP/1.0",
		},
		{
			"Status code with Http 1.0 get",
			&Config{
				Url:     "www.google.com",
				Headers: []string{""},
				Options: []string{"-I", "-k", "--http1.0"},
				EnvVars: []string{""},
			},
			"200",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := Request(tc.config)
			require.NoError(t, err, "failed to get response")
			require.NotEmpty(t, resp)
			require.Contains(t, resp, tc.contains)
		})
	}
}
