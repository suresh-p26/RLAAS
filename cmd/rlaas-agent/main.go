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

type agentConfig struct {
	ListenAddr   string
	UpstreamBase string
	SyncInterval time.Duration
}

type syncState struct {
	mu          sync.RWMutex
	lastSuccess time.Time
	lastError   string
	runs        int64
}

func (s *syncState) markSuccess(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSuccess = t
	s.lastError = ""
	s.runs++
}

func (s *syncState) markError(err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = err
	s.runs++
}

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

func run(listenFn func(*http.Server) error, syncFn func(context.Context, agentConfig, *http.Client, *syncState, <-chan string)) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	state := &syncState{}
	client := &http.Client{Timeout: 5 * time.Second}
	invalidations := make(chan string, 32)
	go syncFn(context.Background(), cfg, client, state, invalidations)
	srv := buildServer(cfg, client, state, invalidations)
	return listenFn(srv)
}

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

func buildServer(cfg agentConfig, client *http.Client, state *syncState, invalidations chan<- string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/v1/check", func(w http.ResponseWriter, r *http.Request) {
		proxyCheck(w, r, cfg, client)
	})
	mux.HandleFunc("/v1/agent/invalidate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
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
	mux.HandleFunc("/v1/agent/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state.snapshot())
	})
	return &http.Server{Addr: cfg.ListenAddr, Handler: mux}
}

func proxyCheck(w http.ResponseWriter, r *http.Request, cfg agentConfig, client *http.Client) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, cfg.UpstreamBase+"/v1/check", bytes.NewReader(body))
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
	defer resp.Body.Close()
	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

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

func fetchPolicySnapshot(ctx context.Context, cfg agentConfig, client *http.Client, state *syncState) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.UpstreamBase+"/v1/policies", nil)
	if err != nil {
		state.markError(err.Error())
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		state.markError(err.Error())
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		state.markSuccess(time.Now().UTC())
		return
	}
	state.markError(fmt.Sprintf("policy sync status: %d", resp.StatusCode))
}

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
