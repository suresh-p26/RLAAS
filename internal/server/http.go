package server

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync/atomic"
	"time"
)

// HTTPServer contains address and mux for the HTTP transport layer.
type HTTPServer struct {
	Addr           string
	Mux            *http.ServeMux
	server         *http.Server
	tlsConfig      *tls.Config
	readTimeout    time.Duration
	writeTimeout   time.Duration
	idleTimeout    time.Duration
	maxHeaderBytes int
	ready          atomic.Bool
	wrapFn         func(http.Handler) http.Handler
}

// HTTPOption configures the HTTP server at construction time.
type HTTPOption func(*HTTPServer)

// WithTLS attaches a TLS configuration (may be nil to disable).
func WithTLS(tc *tls.Config) HTTPOption {
	return func(s *HTTPServer) { s.tlsConfig = tc }
}

// WithTimeouts sets read, write, and idle timeouts.
func WithTimeouts(read, write, idle time.Duration) HTTPOption {
	return func(s *HTTPServer) {
		s.readTimeout = read
		s.writeTimeout = write
		s.idleTimeout = idle
	}
}

// WithMaxHeaderBytes limits the maximum size of request headers.
func WithMaxHeaderBytes(n int) HTTPOption {
	return func(s *HTTPServer) { s.maxHeaderBytes = n }
}

// NewHTTPServer registers service endpoints.
// /v1/check returns decision responses.
// /healthz returns plain ok when server is healthy.
// /readyz returns 200 only after SetReady(true) is called.
func NewHTTPServer(addr string, checkHandler http.Handler, opts ...HTTPOption) *HTTPServer {
	mux := http.NewServeMux()
	s := &HTTPServer{
		Addr:           addr,
		Mux:            mux,
		readTimeout:    5 * time.Second,
		writeTimeout:   10 * time.Second,
		idleTimeout:    120 * time.Second,
		maxHeaderBytes: 1 << 20,
	}
	for _, o := range opts {
		o(s)
	}

	mux.Handle("/v1/check", checkHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if s.ready.Load() {
			_, _ = w.Write([]byte("ok"))
		} else {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
		}
	})
	return s
}

// SetReady toggles the readiness probe.
func (s *HTTPServer) SetReady(v bool) { s.ready.Store(v) }

// WrapHandler sets a function that wraps the root handler before
// ListenAndServe builds the underlying http.Server.  The wrapper is
// applied around the mux (but not the panic-recovery layer, which
// always sits outermost so crashes are always caught).
func (s *HTTPServer) WrapHandler(fn func(http.Handler) http.Handler) {
	s.wrapFn = fn
}

// ListenAndServe starts the HTTP server with production-safe defaults.
func (s *HTTPServer) ListenAndServe() error {
	var handler http.Handler = s.Mux
	if s.wrapFn != nil {
		handler = s.wrapFn(handler)
	}
	handler = PanicRecovery(handler)
	s.server = &http.Server{
		Addr:           s.Addr,
		Handler:        handler,
		TLSConfig:      s.tlsConfig,
		ReadTimeout:    s.readTimeout,
		WriteTimeout:   s.writeTimeout,
		IdleTimeout:    s.idleTimeout,
		MaxHeaderBytes: s.maxHeaderBytes,
	}
	if s.tlsConfig != nil {
		slog.Info("https server listening", "addr", s.Addr)
		return s.server.ListenAndServeTLS("", "") // certs already in tls.Config
	}
	slog.Info("http server listening", "addr", s.Addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully drains in-flight requests.
func (s *HTTPServer) Shutdown(timeout time.Duration) error {
	if s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.server.Shutdown(ctx)
}

// PanicRecovery wraps an http.Handler with a deferred recover() so that a
// single panicking request does not crash the process.
func PanicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered",
					"error", rec,
					"path", r.URL.Path,
					"method", r.Method,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
