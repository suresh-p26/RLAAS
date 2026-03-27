package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// agentConfig holds runtime settings for the sidecar process.
type agentConfig struct {
	ListenAddr   string
	UpstreamBase string
	SyncInterval time.Duration
}

// syncState tracks policy synchronisation health for the status endpoint.
type syncState struct {
	mu          sync.RWMutex
	lastSuccess time.Time
	lastError   string
	runs        int64
}

// markSuccess records a successful sync at the given time.
func (s *syncState) markSuccess(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSuccess = t
	s.lastError = ""
	s.runs++
}

// markError records a failed sync with the given error message.
func (s *syncState) markError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = err
	s.runs++
}

// snapshot returns a read-only copy of sync health for JSON serialisation.
func (s *syncState) snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := map[string]any{"sync_runs": s.runs, "last_error": s.lastError}
	if !s.lastSuccess.IsZero() {
		status["last_success_unix"] = s.lastSuccess.Unix()
	}
	return status
}

// main starts the sidecar scaffold with local proxy and sync loop foundations.
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(defaultListen, startSyncLoop); err != nil {
		slog.Error("rlaas-agent startup failed", "error", err)
	}
}

// run wires the agent: loads config, starts the sync loop, and blocks on
// the HTTP server.
func run(listenFn func(*http.Server) error, syncFn func(context.Context, agentConfig, *http.Client, *syncState, <-chan string)) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	state := &syncState{}
	client := &http.Client{Timeout: 5 * time.Second}
	invalidations := make(chan string, 32)
	// Use a cancellable context so the sync loop stops on shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go syncFn(ctx, cfg, client, state, invalidations)
	srv := buildServer(cfg, client, state, invalidations)
	return listenFn(srv)
}

// loadConfig reads agent environment variables and returns the parsed config.
func loadConfig() (agentConfig, error) {
	listen := os.Getenv("RLAAS_AGENT_LISTEN")
	if strings.TrimSpace(listen) == "" {
		listen = ":18080"
	}
	upstream := os.Getenv("RLAAS_UPSTREAM_HTTP")
	if strings.TrimSpace(upstream) == "" {
		upstream = "http://localhost:8080"
	}
	if _, err := url.ParseRequestURI(upstream); err != nil {
		return agentConfig{}, fmt.Errorf("invalid RLAAS_UPSTREAM_HTTP: %w", err)
	}
	intervalSecs := int64(30)
	if raw := strings.TrimSpace(os.Getenv("RLAAS_AGENT_SYNC_SECS")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			return agentConfig{}, fmt.Errorf("invalid RLAAS_AGENT_SYNC_SECS: %s", raw)
		}
		intervalSecs = v
	}
	return agentConfig{ListenAddr: listen, UpstreamBase: strings.TrimRight(upstream, "/"), SyncInterval: time.Duration(intervalSecs) * time.Second}, nil
}

// buildServer constructs the agent HTTP mux and http.Server with
// production timeouts.
func buildServer(cfg agentConfig, client *http.Client, state *syncState, invalidations chan<- string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/rlaas/v1/check", func(w http.ResponseWriter, r *http.Request) {
		proxyCheck(w, r, cfg, client)
	})
	mux.HandleFunc("/rlaas/v1/agent/invalidate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Limit invalidation payloads to 64 KB.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			PolicyID string `json:"policy_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.PolicyID) == "" {
			http.Error(w, "policy_id is required", http.StatusBadRequest)
			return
		}
		select {
		case invalidations <- req.PolicyID:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"queued": true, "policy_id": req.PolicyID})
	})
	mux.HandleFunc("/rlaas/v1/agent/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state.snapshot())
	})
	return &http.Server{
		Addr:           cfg.ListenAddr,
		Handler:        panicRecovery(mux),
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
}

// panicRecovery wraps a handler so a single panicking request doesn't crash
// the agent process.
func panicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("agent panic recovered", "error", rec, "path", r.URL.Path)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// proxyCheck forwards rate-limit check requests to the upstream server.
func proxyCheck(w http.ResponseWriter, r *http.Request, cfg agentConfig, client *http.Client) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	const maxProxyBody = 1 << 20
	limited := io.LimitReader(r.Body, maxProxyBody+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > maxProxyBody {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, cfg.UpstreamBase+"/rlaas/v1/check", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "upstream request error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// startSyncLoop periodically fetches policies from the upstream server and
// reacts to invalidation signals from the control plane.
func startSyncLoop(ctx context.Context, cfg agentConfig, client *http.Client, state *syncState, invalidations <-chan string) {
	ticker := time.NewTicker(cfg.SyncInterval)
	defer ticker.Stop()
	fetchPolicySnapshot(ctx, cfg, client, state)
	for {
		select {
		case <-ctx.Done():
			return
		case policyID := <-invalidations:
			for {
				select {
				case <-invalidations:
				default:
					goto drained
				}
			}
		drained:
			fetchPolicySnapshot(ctx, cfg, client, state)
			slog.Info("processed invalidation", "policy_id", policyID)
		case <-ticker.C:
			fetchPolicySnapshot(ctx, cfg, client, state)
		}
	}
}

// fetchPolicySnapshot retrieves the full policy list from the upstream
// server and updates sync state accordingly.
func fetchPolicySnapshot(ctx context.Context, cfg agentConfig, client *http.Client, state *syncState) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.UpstreamBase+"/rlaas/v1/policies", nil)
	if err != nil {
		state.markError(err.Error())
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		state.markError(err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		state.markSuccess(time.Now().UTC())
		return
	}
	state.markError(fmt.Sprintf("policy sync status: %d", resp.StatusCode))
}

// defaultListen starts the agent HTTP server and blocks until a termination
// signal is received, then gracefully drains connections.
func defaultListen(s *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		slog.Info("rlaas-agent sidecar listening", "addr", s.Addr)
		errCh <- s.ListenAndServe()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-quit:
		slog.Info("shutting down agent", "signal", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		slog.Error("agent shutdown error", "error", err)
		return err
	}
	slog.Info("agent stopped gracefully")
	return nil
}
