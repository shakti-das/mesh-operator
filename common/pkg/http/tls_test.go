package http

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRootCAs(t *testing.T) {
	testCases := []struct {
		name          string
		pkiCACertPath string

		expectedErr string
	}{
		{
			"NonexistentCACertPath",
			"testdata/nonexistentfile.pem",

			"failed to read CA cert at \"testdata/nonexistentfile.pem\": open testdata/nonexistentfile.pem: no such file or directory",
		},
		{
			"BadCACertPath",
			"testdata/key.pem",

			"failed to append CA cert at \"testdata/key.pem\" to pool: file does not contain any valid certs",
		},
		{
			"ValidCACertPath",
			"testdata/cert.pem",

			"",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cp, err := BuildRootCAs(tc.pkiCACertPath)

			if tc.expectedErr == "" {
				require.NoError(t, err)

				// TODO: Assert CertPool contents.
				assert.NotNil(t, cp)
			} else {
				assert.EqualError(t, err, tc.expectedErr)
			}
		})
	}
}

func TestBuildCertificates(t *testing.T) {
	testCases := []struct {
		name           string
		clientCertPath string
		clientKeyPath  string

		expectedErr string
	}{
		{
			"NonexistentClientCertKeyPath",
			"testdata/nonexistentcertfile.pem",
			"testdata/nonexistentkeyfile.pem",
			"failed to read cert and key at (\"testdata/nonexistentcertfile.pem\", \"testdata/nonexistentkeyfile.pem\"): open testdata/nonexistentcertfile.pem: no such file or directory",
		},
		{
			"NonexistentClientKeyPath",
			"testdata/nonexistentcertfile.pem",
			"testdata/key.pem",
			"failed to read cert and key at (\"testdata/nonexistentcertfile.pem\", \"testdata/key.pem\"): open testdata/nonexistentcertfile.pem: no such file or directory",
		},
		{
			"NonexistentClientCertPath",
			"testdata/cert.pem",
			"testdata/nonexistentkeyfile.pem",
			"failed to read cert and key at (\"testdata/cert.pem\", \"testdata/nonexistentkeyfile.pem\"): open testdata/nonexistentkeyfile.pem: no such file or directory",
		},
		{
			"ValidClientCertKeyPath",
			"testdata/cert.pem",
			"testdata/key.pem",
			"",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cp, err := BuildCertificates(tc.clientKeyPath, tc.clientCertPath)

			if tc.expectedErr == "" {
				require.NoError(t, err)

				// TODO: Assert CertPool contents.
				assert.NotNil(t, cp)
			} else {
				assert.EqualError(t, err, tc.expectedErr)
			}
		})
	}
}
