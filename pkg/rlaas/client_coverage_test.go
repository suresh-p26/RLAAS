package rlaas

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	filestore "github.com/rlaas-io/rlaas/internal/store/policy/file"
	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestNewFromPolicyFile(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies.json")
	c := NewFromPolicyFile(path)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	d, err := c.Evaluate(context.Background(), model.RequestContext{
		SignalType: "http", Endpoint: "/v1/charge", Method: "POST",
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if d.Action == "" {
		t.Fatal("expected decision action")
	}
}

func TestNewWithConfig(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies.json")
	c := NewWithConfig(path, "test-prefix", 5*time.Second)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	d, err := c.Evaluate(context.Background(), model.RequestContext{
		SignalType: "http", Endpoint: "/v1/charge", Method: "POST",
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if d.Action == "" {
		t.Fatal("expected decision action")
	}
}

func TestNew_PanicsOnNilPolicyStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil PolicyStore")
		}
	}()
	New(Options{PolicyStore: nil, CounterStore: memory.New()})
}

func TestNew_PanicsOnNilCounterStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil CounterStore")
		}
	}()
	path := filepath.Join("..", "..", "examples", "policies.json")
	New(Options{PolicyStore: filestore.New(path), CounterStore: nil})
}

func TestNew_DefaultCacheTTL(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies.json")
	c := New(Options{
		PolicyStore:  filestore.New(path),
		CounterStore: memory.New(),
		CacheTTL:     0, // should use default 30s
	})
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestEvaluate_WithPresetTimestamp(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies.json")
	c := NewFromPolicyFile(path)
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	d, err := c.Evaluate(context.Background(), model.RequestContext{
		SignalType: "http", Endpoint: "/v1/charge", Method: "POST",
		Timestamp: ts,
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if d.Action == "" {
		t.Fatal("expected decision action")
	}
}

func TestStartConcurrencyLease_WithPresetTimestamp(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies.json")
	c := NewFromPolicyFile(path)
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	d, release, err := c.StartConcurrencyLease(context.Background(), model.RequestContext{
		SignalType: "job",
		Timestamp:  ts,
	})
	if err != nil || release == nil || d.Action == "" {
		t.Fatal("expected lease path")
	}
	_ = release()
}
