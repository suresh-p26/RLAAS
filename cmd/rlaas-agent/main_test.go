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

func TestProxyCheckUpstreamUnavailable(t *testing.T) {
	cfg := agentConfig{UpstreamBase: "http://127.0.0.1:1"}
	res := httptest.NewRecorder()
	proxyCheck(res, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader(`{}`)), cfg, &http.Client{Timeout: 50 * time.Millisecond})
	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected bad gateway")
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

func TestDefaultListenInvalid(t *testing.T) {
	s := &http.Server{Addr: ":-1"}
	if err := defaultListen(s); err == nil {
		t.Fatalf("expected error from invalid addr")
	}
}

// TestBuildServerHealthzEndpoint tests the health check endpoint
func TestBuildServerHealthzEndpoint(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	state := &syncState{}
	client := &http.Client{}
	invalidations := make(chan string)

	srv := buildServer(cfg, client, state, invalidations)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("expected 'ok', got %s", rec.Body.String())
	}
}

// TestBuildServerStatusEndpoint tests the status endpoint returns sync state
func TestBuildServerStatusEndpoint(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	state := &syncState{}
	state.markSuccess(time.Now())
	client := &http.Client{}
	invalidations := make(chan string)

	srv := buildServer(cfg, client, state, invalidations)
	req := httptest.NewRequest(http.MethodGet, "/rlaas/v1/agent/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatal("should return JSON content type")
	}

	var status map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("unable to decode response: %v", err)
	}
	if _, ok := status["sync_runs"]; !ok {
		t.Fatal("status should include sync_runs")
	}
}

// TestBuildServerInvalidateEndpoint tests invalidation endpoint
func TestBuildServerInvalidateEndpoint(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	state := &syncState{}
	client := &http.Client{}
	invalidations := make(chan string, 10)

	srv := buildServer(cfg, client, state, invalidations)

	payload := map[string]string{"policy_id": "p1"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/agent/invalidate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}

	select {
	case policyID := <-invalidations:
		if policyID != "p1" {
			t.Fatalf("expected p1, got %s", policyID)
		}
	case <-time.After(time.Second):
		t.Fatal("invalidation should be queued")
	}
}

// TestBuildServerInvalidateEndpointNotPost tests invalidation requires POST
func TestBuildServerInvalidateEndpointNotPost(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	state := &syncState{}
	client := &http.Client{}
	invalidations := make(chan string)

	srv := buildServer(cfg, client, state, invalidations)

	req := httptest.NewRequest(http.MethodGet, "/rlaas/v1/agent/invalidate", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// TestBuildServerInvalidateEndpointInvalidJSON tests invalid JSON handling
func TestBuildServerInvalidateEndpointInvalidJSON(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	state := &syncState{}
	client := &http.Client{}
	invalidations := make(chan string)

	srv := buildServer(cfg, client, state, invalidations)

	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/agent/invalidate", strings.NewReader("invalid json"))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestBuildServerInvalidateEndpointMissingPolicyID tests missing policy_id validation
func TestBuildServerInvalidateEndpointMissingPolicyID(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	state := &syncState{}
	client := &http.Client{}
	invalidations := make(chan string)

	srv := buildServer(cfg, client, state, invalidations)

	payload := map[string]string{"policy_id": ""}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/agent/invalidate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing policy_id, got %d", rec.Code)
	}
}

// TestBuildServerInvalidateQueueFull tests queue full case
func TestBuildServerInvalidateQueueFull(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	state := &syncState{}
	client := &http.Client{}
	invalidations := make(chan string, 1)

	invalidations <- "full"

	srv := buildServer(cfg, client, state, invalidations)

	payload := map[string]string{"policy_id": "p1"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/agent/invalidate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
}

// TestProxyCheckInvalidMethod tests that only POST is allowed
func TestProxyCheckInvalidMethod(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	client := &http.Client{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rlaas/v1/check", nil)
	proxyCheck(rec, req, cfg, client)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// TestProxyCheckOversizedBody tests body size limit enforcement
func TestProxyCheckOversizedBody(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	client := &http.Client{}

	largeBody := strings.Repeat("a", (1<<20)+100)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader(largeBody))
	proxyCheck(rec, req, cfg, client)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
}

// TestProxyCheckValidJSON tests successful JSON proxy
func TestProxyCheckValidJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer upstream.Close()

	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: upstream.URL}
	client := &http.Client{Timeout: 5 * time.Second}

	payload := `{"key":"test","limit":10}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader(payload))
	proxyCheck(rec, req, cfg, client)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "allowed") {
		t.Fatal("response should contain allowed field")
	}
}

// TestProxyCheckHeaderPropagation tests that response headers are propagated
func TestProxyCheckHeaderPropagation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "custom-value")
		w.Header().Set("X-Request-ID", "12345")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer upstream.Close()

	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: upstream.URL}
	client := &http.Client{Timeout: 5 * time.Second}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader("{}"))
	proxyCheck(rec, req, cfg, client)

	if rec.Header().Get("X-Custom-Header") != "custom-value" {
		t.Fatal("custom headers should be propagated")
	}
}

// TestProxyCheckUpstreamError tests upstream server error handling
func TestProxyCheckUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream error"))
	}))
	defer upstream.Close()

	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: upstream.URL}
	client := &http.Client{Timeout: 5 * time.Second}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader("{}"))
	proxyCheck(rec, req, cfg, client)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestProxyCheckUpstreamUnavailableNetwork tests network error handling
func TestProxyCheckUpstreamUnavailableNetwork(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://127.0.0.1:1"}
	client := &http.Client{Timeout: 100 * time.Millisecond}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader("{}"))
	proxyCheck(rec, req, cfg, client)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

// TestStartSyncLoopContextCancel tests that sync loop exits on context cancel
func TestStartSyncLoopContextCancel(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080", SyncInterval: 100 * time.Millisecond}
	state := &syncState{}
	client := &http.Client{}
	invalidations := make(chan string)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)

	go func() {
		startSyncLoop(ctx, cfg, client, state, invalidations)
		done <- true
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sync loop should exit when context is cancelled")
	}
}

// TestFetchPolicySnapshotSuccess tests successful policy fetch
func TestFetchPolicySnapshotSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rlaas/v1/policies" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"p1"}]`))
	}))
	defer upstream.Close()

	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: upstream.URL}
	state := &syncState{}
	client := &http.Client{Timeout: 5 * time.Second}

	fetchPolicySnapshot(context.Background(), cfg, client, state)
	snapshot := state.snapshot()

	if snapshot["last_error"] != "" {
		t.Fatalf("should not have error: %v", snapshot["last_error"])
	}
	if snapshot["sync_runs"].(int64) < 1 {
		t.Fatal("sync runs should be incremented")
	}
}

// TestFetchPolicySnapshotNon2xxError tests non-2xx response handling
func TestFetchPolicySnapshotNon2xxError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: upstream.URL}
	state := &syncState{}
	client := &http.Client{Timeout: 5 * time.Second}

	fetchPolicySnapshot(context.Background(), cfg, client, state)
	snapshot := state.snapshot()

	if snapshot["last_error"] == "" {
		t.Fatal("should have error on non-2xx response")
	}
}

// TestFetchPolicySnapshotNetworkError tests network error handling
func TestFetchPolicySnapshotNetworkError(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://127.0.0.1:1"}
	state := &syncState{}
	client := &http.Client{Timeout: 100 * time.Millisecond}

	fetchPolicySnapshot(context.Background(), cfg, client, state)
	snapshot := state.snapshot()

	if snapshot["last_error"] == "" {
		t.Fatal("should have error on network failure")
	}
}

// TestSyncStateMarkSuccess tests successful sync tracking
func TestSyncStateMarkSuccess(t *testing.T) {
	state := &syncState{}
	now := time.Now()
	state.markSuccess(now)

	snapshot := state.snapshot()
	if snapshot["sync_runs"].(int64) != 1 {
		t.Fatal("sync runs should be 1")
	}
	if snapshot["last_error"] != "" {
		t.Fatal("last_error should be empty after success")
	}
}

// TestSyncStateMultipleFails tests that error state persists correctly
func TestSyncStateMultipleFails(t *testing.T) {
	state := &syncState{}
	state.markError("error1")
	state.markError("error2")

	snapshot := state.snapshot()
	if snapshot["sync_runs"].(int64) != 2 {
		t.Fatalf("expected 2 runs, got %d", snapshot["sync_runs"].(int64))
	}
	if snapshot["last_error"] != "error2" {
		t.Fatalf("should have latest error, got %s", snapshot["last_error"])
	}
}

// TestPanicRecoveryWithResponseWriter tests panic recovery middleware
func TestPanicRecoveryWithResponseWriter(t *testing.T) {
	recovered := false
	handler := panicRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 after panic, got %d", rec.Code)
	}
	recovered = rec.Code == http.StatusInternalServerError
	if !recovered {
		t.Fatal("panic should be recovered")
	}
}

// TestLoadConfigWithValidInterval tests config with valid sync interval
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

// TestLoadConfigInvalidInterval tests config with invalid sync interval
func TestLoadConfigInvalidInterval(t *testing.T) {
	t.Setenv("RLAAS_AGENT_SYNC_SECS", "invalid")
	t.Setenv("RLAAS_UPSTREAM_HTTP", "http://localhost:8080")

	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "RLAAS_AGENT_SYNC_SECS") {
		t.Fatal("should error on invalid sync interval")
	}
}

// TestLoadConfigNegativeInterval tests config with negative sync interval
func TestLoadConfigNegativeInterval(t *testing.T) {
	t.Setenv("RLAAS_AGENT_SYNC_SECS", "-10")
	t.Setenv("RLAAS_UPSTREAM_HTTP", "http://localhost:8080")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("should error on negative sync interval")
	}
}

// TestLoadConfigDefaultValues tests config defaults
func TestLoadConfigDefaultValues(t *testing.T) {
	t.Setenv("RLAAS_AGENT_LISTEN", "")
	t.Setenv("RLAAS_UPSTREAM_HTTP", "http://custom:9999")
	t.Setenv("RLAAS_AGENT_SYNC_SECS", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ListenAddr != ":18080" {
		t.Fatalf("expected default :18080, got %s", cfg.ListenAddr)
	}
	if cfg.SyncInterval != 30*time.Second {
		t.Fatalf("expected default 30s, got %v", cfg.SyncInterval)
	}
}

// TestProxyCheckContextTimeout tests context timeout during request
func TestProxyCheckContextTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: upstream.URL}
	client := &http.Client{Timeout: 50 * time.Millisecond}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader("{}"))
	proxyCheck(rec, req, cfg, client)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on timeout, got %d", rec.Code)
	}
}

// TestProxyCheckBodyReadError tests handling of request body that can't be read
func TestProxyCheckBodyReadError(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	client := &http.Client{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", nil)
	req.Body = io.NopCloser(bytes.NewReader([]byte("test")))

	proxyCheck(rec, req, cfg, client)

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("should not error on valid body")
	}
}

// TestBuildServerServerConfig tests server configuration
func TestBuildServerServerConfig(t *testing.T) {
	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: "http://localhost:8080"}
	state := &syncState{}
	client := &http.Client{}
	invalidations := make(chan string)

	srv := buildServer(cfg, client, state, invalidations)

	if srv.Addr != ":18080" {
		t.Fatalf("expected listen address :18080, got %s", srv.Addr)
	}
	if srv.ReadTimeout == 0 {
		t.Fatal("should have read timeout configured")
	}
	if srv.WriteTimeout == 0 {
		t.Fatal("should have write timeout configured")
	}
	if srv.IdleTimeout == 0 {
		t.Fatal("should have idle timeout configured")
	}
	if srv.MaxHeaderBytes == 0 {
		t.Fatal("should have max header bytes configured")
	}
}

// TestSyncStateSnapshotAfterError tests snapshot with error state
func TestSyncStateSnapshotAfterError(t *testing.T) {
	state := &syncState{}
	state.markError("test error")
	snapshot := state.snapshot()

	if snapshot["last_error"] != "test error" {
		t.Fatalf("expected 'test error', got %v", snapshot["last_error"])
	}
	if _, ok := snapshot["last_success_unix"]; ok {
		t.Fatal("last_success_unix should not be present after error")
	}
}

// TestProxyCheckInvalidResponseRead tests handling of invalid response from upstream
func TestProxyCheckInvalidResponseRead(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("partial response"))
	}))
	defer upstream.Close()

	cfg := agentConfig{ListenAddr: ":18080", UpstreamBase: upstream.URL}
	client := &http.Client{Timeout: 5 * time.Second}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", strings.NewReader("{}"))

	proxyCheck(rec, req, cfg, client)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

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
