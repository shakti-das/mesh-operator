package k8swebhook

import (
	"crypto/tls"

	"go.uber.org/zap"
)

// GetCertificateFunc provides a concrete type for the GetCertificate function
// in the tls.Config struct (https://golang.org/pkg/crypto/tls/#Config).
type GetCertificateFunc func(*tls.ClientHelloInfo) (*tls.Certificate, error)

// NewNaiveGetCertificateFunc returns a GetCertificateFunc implementation that
// just naively reloads the certificate files from disk on each call, with no
// caching or filesystem watching.
func NewNaiveGetCertificateFunc(logger *zap.SugaredLogger, certFile string, keyFile string) GetCertificateFunc {
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		logger.Debugw(
			"reading certificate files",
			"certFile", certFile,
			"keyFile", keyFile)

		cert, err := tls.LoadX509KeyPair(certFile, keyFile)

		if err != nil {
			logger.Errorw(
				"failed to read certificate files",
				"error", err,
				"certFile", certFile,
				"keyFile", keyFile)

			return nil, err
		}

		return &cert, nil
	}
}
