package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/server"
)

func TestRunBuildsServer(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies.json")
	_ = os.Setenv("RLAAS_POLICY_FILE", path)
	defer os.Unsetenv("RLAAS_POLICY_FILE")

	called := false
	err := run(func(s *server.HTTPServer) error {
		called = true
		if s == nil || s.Mux == nil {
			t.Fatalf("expected server")
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("expected run success: %v", err)
	}
}

func TestRunMissingPolicyFile(t *testing.T) {
	_ = os.Setenv("RLAAS_POLICY_FILE", filepath.Join(t.TempDir(), "missing.json"))
	defer os.Unsetenv("RLAAS_POLICY_FILE")
	if err := run(func(s *server.HTTPServer) error { return nil }); err == nil || !strings.Contains(err.Error(), "policy file not found") {
		t.Fatalf("expected missing file error")
	}
}

func TestDefaultListenInvalid(t *testing.T) {
	if err := defaultListen(&server.HTTPServer{Addr: ":-1", Mux: nil}); err == nil {
		t.Fatalf("expected default listen error")
	}
}

func TestMainServerReturnsOnStartupError(t *testing.T) {
	_ = os.Setenv("RLAAS_POLICY_FILE", filepath.Join(t.TempDir(), "missing.json"))
	defer os.Unsetenv("RLAAS_POLICY_FILE")
	main()
}

func TestRunUsesDefaultPolicyFile(t *testing.T) {
	os.Unsetenv("RLAAS_POLICY_FILE")
	old, _ := os.Getwd()
	defer func() { _ = os.Chdir(old) }()
	_ = os.Chdir(filepath.Join("..", ".."))
	called := false
	err := run(func(s *server.HTTPServer) error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("expected run success with default policy file")
	}
}

func TestRunListenError(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies.json")
	_ = os.Setenv("RLAAS_POLICY_FILE", path)
	defer os.Unsetenv("RLAAS_POLICY_FILE")
	err := run(func(s *server.HTTPServer) error { return errors.New("listen failed") })
	if err == nil || !strings.Contains(err.Error(), "listen failed") {
		t.Fatalf("expected listen error")
	}
}

func TestRunAllGRPCListenError(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies.json")
	_ = os.Setenv("RLAAS_POLICY_FILE", path)
	defer os.Unsetenv("RLAAS_POLICY_FILE")
	err := runAll(func(s *server.HTTPServer) error { return nil }, func(s *server.GRPCServer) error { return errors.New("grpc listen failed") })
	if err == nil || !strings.Contains(err.Error(), "grpc listen failed") {
		t.Fatalf("expected grpc listen error")
	}
}

func TestRunAllSuccess(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies.json")
	_ = os.Setenv("RLAAS_POLICY_FILE", path)
	defer os.Unsetenv("RLAAS_POLICY_FILE")
	httpCalled := false
	grpcCalled := false
	err := runAll(func(s *server.HTTPServer) error {
		httpCalled = s != nil
		return nil
	}, func(s *server.GRPCServer) error {
		grpcCalled = s != nil
		return nil
	})
	if err != nil || !httpCalled || !grpcCalled {
		t.Fatalf("expected runAll success")
	}
}

func TestDefaultGRPCListenNilServer(t *testing.T) {
	if err := defaultGRPCListen(&server.GRPCServer{Addr: ":0"}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected grpc configuration error")
	}
}

func TestLoadInvalidationTargets(t *testing.T) {
	os.Unsetenv("RLAAS_INVALIDATION_TARGETS")
	if got := loadInvalidationTargets(); len(got) != 0 {
		t.Fatalf("expected empty targets")
	}
	_ = os.Setenv("RLAAS_INVALIDATION_TARGETS", " http://a.local , ,http://b.local ")
	defer os.Unsetenv("RLAAS_INVALIDATION_TARGETS")
	targets := loadInvalidationTargets()
	if len(targets) != 2 || targets[0] != "http://a.local" || targets[1] != "http://b.local" {
		t.Fatalf("unexpected parsed targets: %+v", targets)
	}
}

func TestPublishInvalidation(t *testing.T) {
	var calls atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent/invalidate" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		calls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	publishInvalidation(context.Background(), &http.Client{}, []string{ts.URL}, map[string]string{"policy_id": "p1"})
	if calls.Load() != 1 {
		t.Fatalf("expected one webhook call")
	}

	publishInvalidation(context.Background(), &http.Client{}, nil, map[string]string{"policy_id": "p1"})
	publishInvalidation(context.Background(), nil, []string{ts.URL}, map[string]string{"policy_id": "p1"})
}

func TestStartInvalidationDispatcher(t *testing.T) {
	var calls atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/agent/invalidate" && r.Method == http.MethodPost {
			calls.Add(1)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	enqueue := startInvalidationDispatcher(&http.Client{}, []string{ts.URL}, nil)
	enqueue(map[string]string{"policy_id": "p1"})
	enqueue(map[string]string{"policy_id": "p2"})

	for i := 0; i < 40 && calls.Load() < 2; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected async dispatcher to deliver events")
	}
}

func TestCopyEvent(t *testing.T) {
	in := map[string]string{"policy_id": "p1"}
	out := copyEvent(in)
	out["policy_id"] = "p2"
	if in["policy_id"] != "p1" {
		t.Fatalf("expected source map unchanged")
	}
}
