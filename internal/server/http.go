package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// HTTPServer contains address and mux for the HTTP transport layer.
type HTTPServer struct {
	Addr   string
	Mux    *http.ServeMux
	server *http.Server
}

// NewHTTPServer registers service endpoints.
// /v1/check returns decision responses.
// /healthz returns plain ok when server is healthy.
func NewHTTPServer(addr string, checkHandler http.Handler) *HTTPServer {
	mux := http.NewServeMux()
	mux.Handle("/v1/check", checkHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	return &HTTPServer{Addr: addr, Mux: mux}
}

// ListenAndServe starts the HTTP server.
func (s *HTTPServer) ListenAndServe() error {
	s.server = &http.Server{Addr: s.Addr, Handler: s.Mux}
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
