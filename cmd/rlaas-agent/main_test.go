package main

import (
	"context"
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
	s.Handler.ServeHTTP(r2, httptest.NewRequest(http.MethodGet, "/v1/agent/status", nil))
	if r2.Code != http.StatusOK || !strings.Contains(r2.Body.String(), "sync_runs") {
		t.Fatalf("expected status json")
	}

	r3 := httptest.NewRecorder()
	s.Handler.ServeHTTP(r3, httptest.NewRequest(http.MethodGet, "/v1/agent/invalidate", nil))
	if r3.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected invalidate method not allowed")
	}

	r4 := httptest.NewRecorder()
	s.Handler.ServeHTTP(r4, httptest.NewRequest(http.MethodPost, "/v1/agent/invalidate", strings.NewReader(`{"policy_id":"p1"}`)))
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
		if r.URL.Path != "/v1/check" || r.Method != http.MethodPost {
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
	proxyCheck(methodRes, httptest.NewRequest(http.MethodGet, "/v1/check", nil), cfg, client)
	if methodRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method not allowed")
	}

	okRes := httptest.NewRecorder()
	proxyCheck(okRes, httptest.NewRequest(http.MethodPost, "/v1/check", strings.NewReader(`{"request_id":"r1"}`)), cfg, client)
	if okRes.Code != http.StatusOK || !strings.Contains(okRes.Body.String(), "allowed") {
		t.Fatalf("expected proxied response")
	}
}

func TestProxyCheckUpstreamUnavailable(t *testing.T) {
	cfg := agentConfig{UpstreamBase: "http://127.0.0.1:1"}
	res := httptest.NewRecorder()
	proxyCheck(res, httptest.NewRequest(http.MethodPost, "/v1/check", strings.NewReader(`{}`)), cfg, &http.Client{Timeout: 50 * time.Millisecond})
	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected bad gateway")
	}
}

func TestFetchPolicySnapshotAndSyncLoop(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/policies" {
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
