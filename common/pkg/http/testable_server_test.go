package http

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/common/pkg/k8swebhook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestTestableHTTPServer(t *testing.T) {
	logger := zaptest.NewLogger(t)
	sugar := logger.Sugar()

	testCases := []struct {
		name         string
		createServer func() TestableHTTPServer
	}{
		{
			"StandardHTTPServerPlaintext",
			func() TestableHTTPServer {
				client := &http.Client{}

				server := &http.Server{
					Handler: createPingHandler(),
				}

				return NewStandardHTTPServer(sugar, client, 0, server, false)
			},
		},
		{
			"StandardHTTPServerTLS",
			func() TestableHTTPServer {
				client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}

				server := &http.Server{
					Handler:   createPingHandler(),
					TLSConfig: &tls.Config{GetCertificate: k8swebhook.NewNaiveGetCertificateFunc(sugar, "testdata/cert.pem", "testdata/key.pem")},
				}

				return NewStandardHTTPServer(sugar, client, 0, server, true)
			},
		},
		{
			"TestHTTPServerPlaintext",
			func() TestableHTTPServer {
				return NewTestHTTPServer(httptest.NewUnstartedServer(createPingHandler()), false)
			},
		},
		{
			"TestHTTPServerTLS",
			func() TestableHTTPServer {
				return NewTestHTTPServer(httptest.NewUnstartedServer(createPingHandler()), true)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.createServer()

			s.Start()
			defer s.Stop()

			resp, err := s.Client().Get(s.URL() + "/ping")
			require.NoError(t, err, "failed to get /ping response")
			resp.Body.Close()

			assert.Equal(t, 200, resp.StatusCode)
		})
	}
}

func createPingHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(responseWriter http.ResponseWriter, request *http.Request) {
		fmt.Fprint(responseWriter, "pong")
	})
	return mux
}
