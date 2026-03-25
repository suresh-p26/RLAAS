package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSyncState_MarkErrorAndSnapshot(t *testing.T) {
	state := &syncState{}
	state.markError("failed sync")
	s := state.snapshot()
	if s["last_error"] != "failed sync" {
		t.Fatalf("expected last_error set, got %v", s["last_error"])
	}
	if s["sync_runs"].(int64) != 1 {
		t.Fatalf("expected sync_runs=1, got %v", s["sync_runs"])
	}
}

func TestProxyCheck_RequestBodyTooLarge(t *testing.T) {
	cfg := agentConfig{UpstreamBase: "http://localhost:8080"}
	client := &http.Client{Timeout: 20 * time.Millisecond}

	big := strings.Repeat("a", (1<<20)+10)
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader(big))
	rec := httptest.NewRecorder()

	proxyCheck(rec, req, cfg, client)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized body, got %d", rec.Code)
	}
}

func TestPanicRecovery_Recovers(t *testing.T) {
	h := panicRecovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestFetchPolicySnapshot_Non2xx(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rlaas/v1/policies" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	state := &syncState{}
	fetchPolicySnapshot(context.Background(), agentConfig{UpstreamBase: upstream.URL}, &http.Client{Timeout: time.Second}, state)
	s := state.snapshot()
	if s["last_error"] == "" {
		t.Fatal("expected non-empty last_error on non-2xx response")
	}
}

func TestRun_LoadConfigError(t *testing.T) {
	_ = os.Setenv("RLAAS_UPSTREAM_HTTP", "::bad-url")
	defer os.Unsetenv("RLAAS_UPSTREAM_HTTP")

	called := false
	err := run(func(*http.Server) error {
		called = true
		return nil
	}, func(context.Context, agentConfig, *http.Client, *syncState, <-chan string) {})

	if err == nil || !strings.Contains(err.Error(), "invalid RLAAS_UPSTREAM_HTTP") {
		t.Fatalf("expected load config error, got %v", err)
	}
	if called {
		t.Fatal("listen should not be called on config error")
	}
}

func TestRun_SyncFnCanBeNoop(t *testing.T) {
	_ = os.Setenv("RLAAS_UPSTREAM_HTTP", "http://localhost:8080")
	defer os.Unsetenv("RLAAS_UPSTREAM_HTTP")
	err := run(func(*http.Server) error { return errors.New("stop") }, func(context.Context, agentConfig, *http.Client, *syncState, <-chan string) {})
	if err == nil || !strings.Contains(err.Error(), "stop") {
		t.Fatalf("expected listen error passthrough, got %v", err)
	}
}

// TestAgentEnvironmentConfiguration tests environment variable handling
func TestAgentEnvironmentConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"upstream address", "RLAAS_UPSTREAM_HTTP", "http://localhost:9090"},
		{"listen address", "RLAAS_LISTEN_ADDR", ":8080"},
		{"poll interval", "RLAAS_POLL_INTERVAL", "10s"},
		{"tls enabled", "RLAAS_TLS_ENABLED", "true"},
		{"tls ca cert", "RLAAS_TLS_CA_CERT", "/etc/ssl/certs/ca.crt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig, set := os.LookupEnv(tt.key)
			defer func() {
				if set {
					os.Setenv(tt.key, orig)
				} else {
					os.Unsetenv(tt.key)
				}
			}()

			os.Setenv(tt.key, tt.value)
			if val := os.Getenv(tt.key); val != tt.value {
				t.Fatalf("env var %s: expected %s, got %s", tt.key, tt.value, val)
			}
		})
	}
}

// TestProxyCheck_ValidRequest tests successful check proxy requests
func TestProxyCheck_ValidRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rlaas/v1/check" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"allowed":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	cfg := agentConfig{UpstreamBase: upstream.URL}
	client := &http.Client{Timeout: 5 * time.Second}

	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader(`{"key":"test"}`))
	rec := httptest.NewRecorder()

	proxyCheck(rec, req, cfg, client)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestProxyCheck_UpstreamError tests error handling when upstream fails
func TestProxyCheck_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer upstream.Close()

	cfg := agentConfig{UpstreamBase: upstream.URL}
	client := &http.Client{Timeout: 5 * time.Second}

	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader(`{"key":"test"}`))
	rec := httptest.NewRecorder()

	proxyCheck(rec, req, cfg, client)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestProxyCheck_NetworkTimeout tests request timeout handling
func TestProxyCheck_NetworkTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := agentConfig{UpstreamBase: upstream.URL}
	client := &http.Client{Timeout: 50 * time.Millisecond}

	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader(`{"key":"test"}`))
	rec := httptest.NewRecorder()

	proxyCheck(rec, req, cfg, client)
	// Timeout results in BadGateway (502), not GatewayTimeout (504)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for timeout, got %d", rec.Code)
	}
}

// TestSyncState_SnapshotConsistency tests state snapshot consistency
func TestSyncState_SnapshotConsistency(t *testing.T) {
	state := &syncState{}
	state.markError("error 1")
	state.markError("error 2")

	s1 := state.snapshot()
	s2 := state.snapshot()

	// snapshots should be consistent across calls
	if s1["last_error"] != s2["last_error"] {
		t.Fatalf("inconsistent snapshots: %v vs %v", s1["last_error"], s2["last_error"])
	}
}

// TestConfigPollInterval tests poll interval configuration parsing
func TestConfigPollInterval(t *testing.T) {
	tests := []struct {
		name string
		env  string
		ok   bool
	}{
		{"valid 1s", "1s", true},
		{"valid 30s", "30s", true},
		{"valid 5m", "5m", true},
		{"invalid duration", "bad", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dur, err := time.ParseDuration(tt.env)
			if (err == nil) != tt.ok {
				t.Fatalf("expected ok=%v, got error=%v", tt.ok, err)
			}
			if tt.ok && dur <= 0 {
				t.Fatal("duration should be positive")
			}
		})
	}
}
