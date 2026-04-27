package http

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"

	"go.uber.org/zap"
)

// TestableHTTPServer defines the interface needed for interacting with the underlying http server.
// Extracting this interface rather than using the server directly allows us to use the httptest
// tooling in our unit tests.
type TestableHTTPServer interface {
	Client() *http.Client
	Start()
	Stop()
	URL() string
}

// standardHTTPServer is a TestableHTTPServer implementation for the standard http library.
type standardHTTPServer struct {
	sugar *zap.SugaredLogger

	client *http.Client
	port   int
	server *http.Server
	tls    bool

	wg sync.WaitGroup
}

// NewStandardHTTPServer returns an http.Server-based TestableHTTPServer implementation.
func NewStandardHTTPServer(sugar *zap.SugaredLogger, client *http.Client, port int, server *http.Server, tls bool) TestableHTTPServer {
	return &standardHTTPServer{sugar: sugar, client: client, port: port, server: server, tls: tls}
}

// Client returns an http.Client configured to connect to this server.
func (s *standardHTTPServer) Client() *http.Client {
	return s.client
}

// Start tells this server to begin serving requests. It does not block.
func (s *standardHTTPServer) Start() {
	var wgListener sync.WaitGroup

	s.wg.Add(1)
	wgListener.Add(1)
	go func() {
		defer s.wg.Done()

		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
		if err != nil {
			s.sugar.Errorw("http server listener creation failed", "error", err)
			return
		}

		// Reset the port incase it was set to zero so that we get the random port that was assigned.
		s.port = listener.Addr().(*net.TCPAddr).Port

		// The listener has been created, allow the Start() method to finish waiting.
		wgListener.Done()

		if s.tls {
			err = s.server.ServeTLS(listener, "", "")
		} else {
			err = s.server.Serve(listener)
		}

		if err != nil && err != http.ErrServerClosed {
			s.sugar.Errorw("http server failed", "error", err)
		}
	}()

	wgListener.Wait()
}

// Stop performs a graceful shutdown of this server.
func (s *standardHTTPServer) Stop() {
	if err := s.server.Shutdown(context.Background()); err != nil {
		s.sugar.Errorw("http server shutdown failed", "error", err)
	}
	s.wg.Wait()
}

// URL returns a localhost URL pointing to the base path of this server.
func (s *standardHTTPServer) URL() string {
	protocol := "http"
	if s.tls {
		protocol = "https"
	}
	return fmt.Sprintf("%s://localhost:%d", protocol, s.port)
}

// TestHTTPServer is a TestableHTTPServer implementation for the httptest library.
type testHTTPServer struct {
	server *httptest.Server
	tls    bool
}

// NewTestHTTPServer returns an httptest.Server-based TestableHTTPServer implementation.
func NewTestHTTPServer(server *httptest.Server, tls bool) TestableHTTPServer {
	return &testHTTPServer{server: server, tls: tls}
}

// Client returns an http.Client configured to connect to this server.
func (s *testHTTPServer) Client() *http.Client {
	return s.server.Client()
}

// Start tells this server to begin serving requests. It does not block.
func (s *testHTTPServer) Start() {
	if s.tls {
		s.server.StartTLS()
	} else {
		s.server.Start()
	}
}

// Stop performs a graceful shutdown of this server.
func (s *testHTTPServer) Stop() {
	s.server.Close()
}

// URL returns a localhost URL pointing to the base path of this server.
func (s *testHTTPServer) URL() string {
	return s.server.URL
}
