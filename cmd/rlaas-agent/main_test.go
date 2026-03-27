package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	os.Unsetenv("RLAAS_AGENT_LISTEN")
	os.Unsetenv("RLAAS_UPSTREAM_HTTP")
	os.Unsetenv("RLAAS_AGENT_SYNC_SECS")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("expected config load: %v", err)
	}
	if cfg.ListenAddr != ":18080" || cfg.UpstreamBase != "http://localhost:8080" || cfg.SyncInterval != 30*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	_ = os.Setenv("RLAAS_UPSTREAM_HTTP", "::bad-url")
	defer os.Unsetenv("RLAAS_UPSTREAM_HTTP")
	if _, err := loadConfig(); err == nil {
		t.Fatalf("expected invalid upstream error")
	}

	_ = os.Setenv("RLAAS_UPSTREAM_HTTP", "http://localhost:8080")
	_ = os.Setenv("RLAAS_AGENT_SYNC_SECS", "0")
	defer os.Unsetenv("RLAAS_AGENT_SYNC_SECS")
	if _, err := loadConfig(); err == nil {
		t.Fatalf("expected invalid sync secs error")
	}
}

func TestLoadConfigWithValidInterval(t *testing.T) {
	t.Setenv("RLAAS_AGENT_SYNC_SECS", "60")
	t.Setenv("RLAAS_UPSTREAM_HTTP", "http://localhost:8080")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SyncInterval != 60*time.Second {
		t.Fatalf("expected 60s, got %v", cfg.SyncInterval)
	}
}

func TestLoadConfigInvalidInterval(t *testing.T) {
	t.Setenv("RLAAS_AGENT_SYNC_SECS", "invalid")
	t.Setenv("RLAAS_UPSTREAM_HTTP", "http://localhost:8080")
	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "RLAAS_AGENT_SYNC_SECS") {
		t.Fatal("should error on invalid sync interval")
	}
}

func TestLoadConfigNegativeInterval(t *testing.T) {
	t.Setenv("RLAAS_AGENT_SYNC_SECS", "-10")
	t.Setenv("RLAAS_UPSTREAM_HTTP", "http://localhost:8080")
	if _, err := loadConfig(); err == nil {
		t.Fatal("should error on negative sync interval")
	}
}

func TestBuildServerHealthAndStatus(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":0", UpstreamBase: "http://localhost:8080", SyncInterval: time.Second}
	state := &syncState{}
	invalidations := make(chan string, 4)
	s := buildServer(cfg, &http.Client{Timeout: time.Second}, state, invalidations)

	r1 := httptest.NewRecorder()
	s.Handler.ServeHTTP(r1, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if r1.Code != http.StatusOK || strings.TrimSpace(r1.Body.String()) != "ok" {
		t.Fatalf("expected health ok")
	}

	r2 := httptest.NewRecorder()
	s.Handler.ServeHTTP(r2, httptest.NewRequest(http.MethodGet, "/rlaas/v1/agent/status", nil))
	if r2.Code != http.StatusOK || !strings.Contains(r2.Body.String(), "sync_runs") {
		t.Fatalf("expected status json")
	}

	r3 := httptest.NewRecorder()
	s.Handler.ServeHTTP(r3, httptest.NewRequest(http.MethodGet, "/rlaas/v1/agent/invalidate", nil))
	if r3.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected invalidate method not allowed")
	}

	r4 := httptest.NewRecorder()
	s.Handler.ServeHTTP(r4, httptest.NewRequest(http.MethodPost, "/rlaas/v1/agent/invalidate", strings.NewReader(`{"policy_id":"p1"}`)))
	if r4.Code != http.StatusAccepted || !strings.Contains(r4.Body.String(), "queued") {
		t.Fatalf("expected invalidate accepted response")
	}
	select {
	case id := <-invalidations:
		if id != "p1" {
			t.Fatalf("unexpected policy id: %s", id)
		}
	default:
		t.Fatalf("expected queued invalidation event")
	}
}

func TestBuildServerInvalidateEndpointInvalidJSON(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	srv := buildServer(cfg, &http.Client{}, &syncState{}, make(chan string))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rlaas/v1/agent/invalidate", strings.NewReader("invalid json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestBuildServerInvalidateEndpointMissingPolicyID(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	srv := buildServer(cfg, &http.Client{}, &syncState{}, make(chan string))
	body, _ := json.Marshal(map[string]string{"policy_id": ""})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rlaas/v1/agent/invalidate", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing policy_id, got %d", rec.Code)
	}
}

func TestBuildServerInvalidateQueueFull(t *testing.T) {
	invalidations := make(chan string, 1)
	invalidations <- "full"
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	srv := buildServer(cfg, &http.Client{}, &syncState{}, invalidations)
	body, _ := json.Marshal(map[string]string{"policy_id": "p1"})
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rlaas/v1/agent/invalidate", bytes.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
}

func TestBuildServerServerConfig(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	srv := buildServer(cfg, &http.Client{}, &syncState{}, make(chan string))
	if srv.Addr != ":18080" {
		t.Fatalf("expected :18080, got %s", srv.Addr)
	}
	if srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 || srv.MaxHeaderBytes == 0 {
		t.Fatal("server should have timeouts and MaxHeaderBytes configured")
	}
}

func TestProxyCheckSuccessAndMethod(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rlaas/v1/check" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "request_id") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer upstream.Close()

	cfg := agentConfig{UpstreamBase: upstream.URL}
	client := &http.Client{Timeout: time.Second}

	methodRes := httptest.NewRecorder()
	proxyCheck(methodRes, httptest.NewRequest(http.MethodGet, "/rlaas/v1/check", nil), cfg, client)
	if methodRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method not allowed")
	}

	okRes := httptest.NewRecorder()
	proxyCheck(okRes, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader(`{"request_id":"r1"}`)), cfg, client)
	if okRes.Code != http.StatusOK || !strings.Contains(okRes.Body.String(), "allowed") {
		t.Fatalf("expected proxied response")
	}
}

func TestProxyCheckOversizedBody(t *testing.T) {
	cfg := agentConfig{UpstreamBase: "http://localhost:8080"}
	rec := httptest.NewRecorder()
	proxyCheck(rec, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader(strings.Repeat("a", (1<<20)+100))), cfg, &http.Client{})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
}

func TestProxyCheckHeaderPropagation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "custom-value")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer upstream.Close()

	rec := httptest.NewRecorder()
	proxyCheck(rec, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader("{}")), agentConfig{UpstreamBase: upstream.URL}, &http.Client{Timeout: time.Second})
	if rec.Header().Get("X-Custom-Header") != "custom-value" {
		t.Fatal("custom headers should be propagated")
	}
}

func TestProxyCheckUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	rec := httptest.NewRecorder()
	proxyCheck(rec, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader("{}")), agentConfig{UpstreamBase: upstream.URL}, &http.Client{Timeout: time.Second})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestProxyCheckUpstreamUnavailable(t *testing.T) {
	res := httptest.NewRecorder()
	proxyCheck(res, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader(`{}`)), agentConfig{UpstreamBase: "http://127.0.0.1:1"}, &http.Client{Timeout: 50 * time.Millisecond})
	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected bad gateway")
	}
}

func TestProxyCheckContextTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	rec := httptest.NewRecorder()
	proxyCheck(rec, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader("{}")), agentConfig{UpstreamBase: upstream.URL}, &http.Client{Timeout: 50 * time.Millisecond})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on timeout, got %d", rec.Code)
	}
}

func TestPanicRecoveryWithResponseWriter(t *testing.T) {
	handler := panicRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 after panic, got %d", rec.Code)
	}
}

func TestFetchPolicySnapshotAndSyncLoop(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rlaas/v1/policies" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	state := &syncState{}
	cfg := agentConfig{UpstreamBase: upstream.URL, SyncInterval: 20 * time.Millisecond}
	client := &http.Client{Timeout: time.Second}

	fetchPolicySnapshot(context.Background(), cfg, client, state)
	s := state.snapshot()
	if s["sync_runs"].(int64) == 0 {
		t.Fatalf("expected sync runs")
	}

	ctx, cancel := context.WithCancel(context.Background())
	invalidations := make(chan string, 4)
	go startSyncLoop(ctx, cfg, client, state, invalidations)
	invalidations <- "p1"
	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	after := state.snapshot()
	if after["sync_runs"].(int64) < s["sync_runs"].(int64) {
		t.Fatalf("expected sync loop progress")
	}
}

func TestFetchPolicySnapshotNon2xxError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	state := &syncState{}
	fetchPolicySnapshot(context.Background(), agentConfig{UpstreamBase: upstream.URL}, &http.Client{Timeout: time.Second}, state)
	if state.snapshot()["last_error"] == "" {
		t.Fatal("should have error on non-2xx response")
	}
}

func TestFetchPolicySnapshotNetworkError(t *testing.T) {
	state := &syncState{}
	fetchPolicySnapshot(context.Background(), agentConfig{UpstreamBase: "http://127.0.0.1:1"}, &http.Client{Timeout: 100 * time.Millisecond}, state)
	if state.snapshot()["last_error"] == "" {
		t.Fatal("should have error on network failure")
	}
}

func TestStartSyncLoopContextCancel(t *testing.T) {
	cfg := agentConfig{UpstreamBase: "http://localhost:8080", SyncInterval: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		startSyncLoop(ctx, cfg, &http.Client{}, &syncState{}, make(chan string))
		done <- true
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sync loop should exit when context is cancelled")
	}
}

func TestSyncStateMarkSuccess(t *testing.T) {
	state := &syncState{}
	state.markSuccess(time.Now())
	s := state.snapshot()
	if s["sync_runs"].(int64) != 1 {
		t.Fatal("sync runs should be 1")
	}
	if s["last_error"] != "" {
		t.Fatal("last_error should be empty after success")
	}
	if _, ok := s["last_success_unix"]; !ok {
		t.Fatal("last_success_unix should be present after success")
	}
}

func TestSyncStateMultipleFails(t *testing.T) {
	state := &syncState{}
	state.markError("error1")
	state.markError("error2")
	s := state.snapshot()
	if s["sync_runs"].(int64) != 2 {
		t.Fatalf("expected 2 runs, got %d", s["sync_runs"].(int64))
	}
	if s["last_error"] != "error2" {
		t.Fatalf("should have latest error, got %s", s["last_error"])
	}
	if _, ok := s["last_success_unix"]; ok {
		t.Fatal("last_success_unix should not be present after only errors")
	}
}

func TestRunWiring(t *testing.T) {
	_ = os.Setenv("RLAAS_UPSTREAM_HTTP", "http://localhost:8080")
	_ = os.Setenv("RLAAS_AGENT_LISTEN", ":0")
	defer os.Unsetenv("RLAAS_UPSTREAM_HTTP")
	defer os.Unsetenv("RLAAS_AGENT_LISTEN")

	listenCalled := false
	syncCalled := false
	syncDone := make(chan struct{}, 1)
	err := run(func(s *http.Server) error {
		listenCalled = s != nil
		return nil
	}, func(ctx context.Context, cfg agentConfig, client *http.Client, state *syncState, invalidations <-chan string) {
		syncCalled = cfg.UpstreamBase != "" && client != nil && state != nil
		_ = invalidations
		syncDone <- struct{}{}
	})
	select {
	case <-syncDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected sync callback")
	}
	if err != nil || !listenCalled || !syncCalled {
		t.Fatalf("expected run wiring success")
	}
}

func TestRunListenError(t *testing.T) {
	_ = os.Setenv("RLAAS_UPSTREAM_HTTP", "http://localhost:8080")
	defer os.Unsetenv("RLAAS_UPSTREAM_HTTP")
	err := run(func(s *http.Server) error { return errors.New("listen failed") }, func(ctx context.Context, cfg agentConfig, client *http.Client, state *syncState, invalidations <-chan string) {
		_ = invalidations
	})
	if err == nil || !strings.Contains(err.Error(), "listen failed") {
		t.Fatalf("expected listen error")
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

func TestDefaultListenInvalid(t *testing.T) {
	s := &http.Server{Addr: ":-1"}
	if err := defaultListen(s); err == nil {
		t.Fatalf("expected error from invalid addr")
	}
}
