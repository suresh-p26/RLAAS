package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"

	rlaasv1 "github.com/rlaas-io/rlaas/api/proto"
	grpcadapter "github.com/rlaas-io/rlaas/internal/adapter/grpc"
	httpadapter "github.com/rlaas-io/rlaas/internal/adapter/http"
	"github.com/rlaas-io/rlaas/internal/analytics"
	"github.com/rlaas-io/rlaas/internal/config"
	"github.com/rlaas-io/rlaas/internal/controlplane/invalidation"
	"github.com/rlaas-io/rlaas/internal/metrics"
	"github.com/rlaas-io/rlaas/internal/server"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	filestore "github.com/rlaas-io/rlaas/internal/store/policy/file"
	"github.com/rlaas-io/rlaas/internal/version"
	"github.com/rlaas-io/rlaas/pkg/rlaas"
)

// main starts the RLAAS server with production-safe defaults.  Config is
// loaded from environment variables and validated before any networking
// starts.  Both HTTP and gRPC servers receive graceful shutdown on SIGTERM.
func main() {
	cfg := config.LoadFromEnv()
	initLogging(cfg.Logging)

	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	if err := runAll(cfg, defaultListen, defaultGRPCListen); err != nil {
		slog.Error("rlaas-server startup failed", "error", err)
		// Return rather than os.Exit so deferred cleanup runs and tests
		// that invoke main() directly are not killed.
		return
	}
}

// initLogging configures the global slog logger based on config.
func initLogging(lc config.LoggingConfig) {
	level := slog.LevelInfo
	switch strings.ToLower(lc.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.ToLower(lc.Format) == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}

// run starts only the HTTP transport layer (used in tests).
func run(cfg config.Config, listenFn func(config.Config, *server.HTTPServer) error) error {
	return runAll(cfg, listenFn, func(config.Config, *server.GRPCServer) error { return nil })
}

// runAll boots the full server: policy store, counter store, HTTP mux,
// gRPC server, middleware chain, and invalidation dispatcher.
func runAll(cfg config.Config, listenFn func(config.Config, *server.HTTPServer) error, grpcListenFn func(config.Config, *server.GRPCServer) error) error {
	policyFile := os.Getenv("RLAAS_POLICY_FILE")
	if policyFile == "" {
		policyFile = "examples/policies.json"
	}
	if _, err := os.Stat(policyFile); err != nil {
		return fmt.Errorf("policy file not found: %s", policyFile)
	}

	counterStore := memory.NewWithGC(time.Minute)
	defer counterStore.Stop()

	client := rlaas.New(rlaas.Options{
		PolicyStore:  filestore.New(policyFile),
		CounterStore: counterStore,
		KeyPrefix:    "rlaas",
		CacheTTL:     cfg.CacheTTL,
	})

	policyStore := filestore.New(policyFile)
	b := invalidation.NewBroker()
	analyticsRecorder := analytics.NewRecorder()
	metricsCollector := metrics.New()
	invalidationTargets := loadInvalidationTargets()
	pushClient := &http.Client{Timeout: 2 * time.Second}
	enqueueInvalidation := startInvalidationDispatcher(pushClient, invalidationTargets, analyticsRecorder)
	b.Subscribe("policy.changed", func(event map[string]string) {
		analyticsRecorder.Record(context.Background(), "policy_invalidation_event", event)
	})

	checkHandler := httpadapter.CheckHandler(client)

	// Build TLS config from env (nil when disabled).
	tlsCfg, err := server.NewTLSConfig(cfg.TLS)
	if err != nil {
		return fmt.Errorf("tls setup: %w", err)
	}

	httpServer := server.NewHTTPServer(cfg.Server.HTTPAddr, checkHandler,
		server.WithTimeouts(cfg.Server.ReadTimeout, cfg.Server.WriteTimeout, cfg.Server.IdleTimeout),
		server.WithMaxHeaderBytes(cfg.Server.MaxHeaderBytes),
		server.WithTLS(tlsCfg),
	)
	mux := httpServer.Mux

	acquireHandler, releaseHandler := httpadapter.NewAcquireReleaseHandlers(client)
	mux.Handle("/rlaas/v1/acquire", acquireHandler)
	mux.Handle("/rlaas/v1/release", releaseHandler)

	policiesHandler := httpadapter.PoliciesHandlerWithHooks(policyStore, func(ctx context.Context, topic string, event map[string]string) error {
		_ = b.Publish(ctx, topic, event)
		enqueueInvalidation(copyEvent(event))
		return nil
	}, analyticsRecorder.Record)
	mux.Handle("/rlaas/v1/policies", policiesHandler)
	mux.Handle("/rlaas/v1/policies/", policiesHandler)
	mux.Handle("/rlaas/v1/analytics/summary", analytics.SummaryHandler(analyticsRecorder))
	mux.Handle("/metrics", metrics.PrometheusHandler(metricsCollector))
	mux.Handle("/version", versionHandler())

	mw := httpadapter.NewMiddleware(client)
	mux.Handle("/demo", mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})))

	// Wrap the entire mux with production middleware: request-ID, auth, body-limit.
	authCfg := server.AuthMiddlewareConfig{
		Enabled:     cfg.Auth.Enabled,
		Mode:        server.AuthMode(cfg.Auth.Mode),
		JWTSecret:   []byte(cfg.Auth.JWTSecret),
		JWTIssuer:   cfg.Auth.JWTIssuer,
		JWTAudience: cfg.Auth.JWTAudience,
		AdminRole:   cfg.Auth.AdminRole,
		ReadRole:    cfg.Auth.ReadOnlyRole,
		ExemptPaths: cfg.Auth.ExemptPaths,
	}
	if len(cfg.Auth.APIKeys) > 0 {
		authCfg.APIKeys = make(map[string]bool, len(cfg.Auth.APIKeys))
		for _, k := range cfg.Auth.APIKeys {
			authCfg.APIKeys[k] = true
		}
	}
	httpServer.WrapHandler(func(h http.Handler) http.Handler {
		h = server.MaxBodyBytes(cfg.Server.MaxBodyBytes, h)
		h = server.AuthMiddleware(authCfg, h)
		h = SecurityHeadersMiddleware(h)
		h = RequestIDMiddleware(h)
		h = server.TraceContextMiddleware(h) // outermost: W3C traceparent propagation
		return h
	})

	// Build gRPC server with production hardening (keepalive, recovery, size limits, TLS).
	grpcSvc := grpcadapter.NewRateLimitService(client)
	grpcOpts := []server.GRPCOption{
		server.WithGRPCMaxBytes(cfg.Server.GRPCMaxRecvBytes, cfg.Server.GRPCMaxSendBytes),
	}
	if tlsCfg != nil {
		grpcOpts = append(grpcOpts, server.WithGRPCTLS(tlsCfg))
	}
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPCAddr, nil, grpcOpts...)
	rlaasv1.RegisterRateLimitServiceServer(grpcSrv.Server, grpcSvc)
	grpcSrv.Server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "grpc.health.v1.Health",
		HandlerType: (*interface{})(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "Check",
			Handler:    grpcHealthCheck,
		}},
		Streams: []grpc.StreamDesc{},
	}, &struct{}{})

	if err := grpcListenFn(cfg, grpcSrv); err != nil {
		return err
	}

	// Mark server ready after all routes are registered.
	httpServer.SetReady(true)
	slog.Info("rlaas server ready",
		"http", cfg.Server.HTTPAddr,
		"grpc", cfg.Server.GRPCAddr,
		"auth", cfg.Auth.Enabled,
		"tls", cfg.TLS.Enabled,
	)

	return listenFn(cfg, httpServer)
}

// RequestIDMiddleware injects a unique X-Request-Id header if one is not
// already present, enabling end-to-end request correlation.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = generateRequestID()
			r.Header.Set("X-Request-Id", id)
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r)
	})
}

// generateRequestID produces a random 32-hex-char identifier using crypto/rand.
func generateRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SecurityHeadersMiddleware injects standard security hardening headers
// recommended by OWASP for production HTTP services.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "0")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		next.ServeHTTP(w, r)
	})
}

// grpcHealthCheck implements a minimal gRPC health check (grpc.health.v1).
// Returns SERVING (1) unconditionally.
func grpcHealthCheck(_ interface{}, _ context.Context, _ func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	return &struct{ Status int32 }{Status: 1}, nil
}

// versionHandler returns build-time metadata as JSON.  The response is safe
// to expose publicly — it contains no secrets, only version/commit/build time.
func versionHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version":    version.Version,
			"commit":     version.Commit,
			"build_time": version.BuildTime,
		})
	})
}

// startInvalidationDispatcher spawns a bounded worker pool that pushes
// policy change events to configured sidecar invalidation targets.
func startInvalidationDispatcher(client *http.Client, targets []string, analyticsRecorder *analytics.Recorder) func(map[string]string) {
	if len(targets) == 0 || client == nil {
		return func(map[string]string) {}
	}
	const queueSize = 256
	workers := len(targets)
	if workers > 4 {
		workers = 4
	}
	queue := make(chan map[string]string, queueSize)
	for i := 0; i < workers; i++ {
		go func() {
			for event := range queue {
				publishInvalidation(context.Background(), client, targets, event)
			}
		}()
	}
	return func(event map[string]string) {
		select {
		case queue <- event:
		default:
			if analyticsRecorder != nil {
				analyticsRecorder.Record(context.Background(), "policy_invalidation_dropped", map[string]string{"reason": "queue_full"})
			}
		}
	}
}

// copyEvent creates a shallow copy of an event map to avoid data races when
// the same map is handed to multiple goroutines.
func copyEvent(event map[string]string) map[string]string {
	out := make(map[string]string, len(event))
	for k, v := range event {
		out[k] = v
	}
	return out
}

// defaultListen starts the HTTP server and blocks until a termination signal
// is received, then drains in-flight requests before returning.
func defaultListen(cfg config.Config, s *server.HTTPServer) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.ListenAndServe() }()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		// Server failed to start or stopped unexpectedly.
		return err
	case sig := <-quit:
		slog.Info("shutting down", "signal", sig.String())
	}

	timeout := cfg.Server.ShutdownTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	s.SetReady(false) // stop receiving new traffic from load-balancer
	if err := s.Shutdown(timeout); err != nil {
		slog.Error("http shutdown error", "error", err)
		return err
	}
	slog.Info("server stopped gracefully")
	return nil
}

// defaultGRPCListen starts the gRPC server in a background goroutine and
// registers a shutdown hook that gracefully drains RPCs on SIGTERM.
func defaultGRPCListen(cfg config.Config, s *server.GRPCServer) error {
	if s == nil || s.Server == nil {
		return fmt.Errorf("grpc server is not configured")
	}
	go func() {
		if err := s.ListenAndServe(); err != nil {
			slog.Error("grpc server stopped", "error", err)
		}
	}()
	// Register shutdown hook — gRPC is drained alongside HTTP via signal.
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
		<-quit
		slog.Info("stopping grpc server gracefully")
		s.GracefulStop()
	}()
	return nil
}

// loadInvalidationTargets reads RLAAS_INVALIDATION_TARGETS from the
// environment and returns the parsed list of sidecar base URLs.
func loadInvalidationTargets() []string {
	raw := strings.TrimSpace(os.Getenv("RLAAS_INVALIDATION_TARGETS"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// publishInvalidation fans out a policy-change event to all configured
// invalidation targets using a bounded worker pool.
func publishInvalidation(ctx context.Context, client *http.Client, targets []string, event map[string]string) {
	if len(targets) == 0 || client == nil {
		return
	}
	body, err := json.Marshal(event)
	if err != nil {
		return
	}
	workerCount := len(targets)
	if workerCount > 8 {
		workerCount = 8
	}
	jobs := make(chan string, len(targets))
	for _, t := range targets {
		jobs <- t
	}
	close(jobs)

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(target, "/")+"/rlaas/v1/agent/invalidate", bytes.NewReader(body))
				if err != nil {
					continue
				}
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err != nil {
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
	}
	wg.Wait()
}
