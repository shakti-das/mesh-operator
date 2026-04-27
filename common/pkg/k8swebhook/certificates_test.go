package k8swebhook

import (
	"crypto/tls"
	"io"
	"io/ioutil"
	"os"
	"path"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/common/pkg/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewGetCertificateFuncWithRotation(t *testing.T) {
	logger, _ := logging.NewLogger(zap.DebugLevel)
	sugar := logger.Sugar()

	certsPath, err := ioutil.TempDir("", "certificates")

	require.NoError(t, err, "Failed to create temporary certs dir: %v", err)

	defer os.RemoveAll(certsPath)

	certFile := path.Join(certsPath, "server.pem")
	keyFile := path.Join(certsPath, "server.key")

	clientHelloInfo := &tls.ClientHelloInfo{}

	server1Certs, err := tls.LoadX509KeyPair("testdata/server1.pem", "testdata/server1.key")
	require.NoError(t, err, "Failed to load server1 test certs: %v", err)

	server2Certs, err := tls.LoadX509KeyPair("testdata/server2.pem", "testdata/server2.key")
	require.NoError(t, err, "Failed to load server2 test certs: %v", err)

	testCases := []struct {
		name               string
		getCertificateFunc GetCertificateFunc
	}{
		{
			"NaiveGetCertificateFunc",
			NewNaiveGetCertificateFunc(sugar, certFile, keyFile),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Copy the server1 certs to the configured certs path.
			copy(t, "testdata/server1.pem", certFile)
			copy(t, "testdata/server1.key", keyFile)

			certs1, err := tc.getCertificateFunc(clientHelloInfo)

			assert.NoError(t, err)
			assert.Equal(t, server1Certs, *certs1)

			// Copy the server2 certs to the configured certs path.
			copy(t, "testdata/server2.pem", certFile)
			copy(t, "testdata/server2.key", keyFile)

			certs2, err := tc.getCertificateFunc(clientHelloInfo)

			assert.NoError(t, err)
			assert.Equal(t, server2Certs, *certs2)
		})
	}
}

func TestNewGetCertificateFuncWithInvalidCertPaths(t *testing.T) {
	logger, _ := logging.NewLogger(zap.DebugLevel)
	sugar := logger.Sugar()

	certsPath, err := ioutil.TempDir("", "certificates")

	require.NoError(t, err, "Failed to create temporary certs dir: %v", err)

	defer os.RemoveAll(certsPath)

	certFile := path.Join(certsPath, "server.pem")
	keyFile := path.Join(certsPath, "server.key")

	clientHelloInfo := &tls.ClientHelloInfo{}

	testCases := []struct {
		name               string
		getCertificateFunc GetCertificateFunc
	}{
		{
			"NaiveGetCertificateFunc",
			NewNaiveGetCertificateFunc(sugar, certFile, keyFile),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// No real cert files have been copied over, so getting the certs should fail.
			certs, err := tc.getCertificateFunc(clientHelloInfo)

			assert.Error(t, err)
			assert.Nil(t, certs)
		})
	}
}

func copy(t *testing.T, src string, dest string) {
	from, err := os.Open(src)
	if err != nil {
		t.Fatalf("failed to open copy src: %v", err)
	}
	defer from.Close()

	to, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("failed to open copy dest: %v", err)
	}
	defer to.Close()

	_, err = io.Copy(to, from)
	if err != nil {
		t.Fatalf("failed to perform copy: %v", err)
	}
}
