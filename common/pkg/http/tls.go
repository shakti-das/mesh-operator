package http

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// BuildRootCAs returns a CertPool that combines both the system pool and the given CA cert.
func BuildRootCAs(pkiCACertPath string) (*x509.CertPool, error) {
	// Get the SystemCertPool, continue with an empty pool on error
	rootCAs, _ := x509.SystemCertPool()
	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	// Read in the cert file
	certs, err := os.ReadFile(pkiCACertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert at %q: %w", pkiCACertPath, err)
	}

	// Append our cert to the system pool
	if ok := rootCAs.AppendCertsFromPEM(certs); !ok {
		return nil, fmt.Errorf("failed to append CA cert at %q to pool: file does not contain any valid certs", pkiCACertPath)
	}

	return rootCAs, nil
}

func BuildCertificates(keyPath string, certPath string) ([]tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cert and key at (%q, %q): %w", certPath, keyPath, err)
	}
	return []tls.Certificate{cert}, nil
}
