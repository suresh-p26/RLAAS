package rlaas

import (
	"context"
	"path/filepath"
	"rlaas/internal/store/counter/memory"
	filestore "rlaas/internal/store/policy/file"
	"rlaas/pkg/model"
	"testing"
)

func TestClientEvaluateAndLease(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies.json")
	c := New(Options{PolicyStore: filestore.New(path), CounterStore: memory.New(), KeyPrefix: "rlaas"})
	d, err := c.Evaluate(context.Background(), model.RequestContext{OrgID: "acme", TenantID: "retail", Service: "payments", SignalType: "http", Endpoint: "/v1/charge", Method: "POST", UserID: "u1"})
	if err != nil {
		t.Fatalf("evaluate failed: %v", err)
	}
	if d.Action == "" {
		t.Fatalf("expected decision")
	}

	d2, release, err := c.StartConcurrencyLease(context.Background(), model.RequestContext{SignalType: "job"})
	if err != nil || release == nil || d2.Action == "" {
		t.Fatalf("expected lease path")
	}
	_ = release()
}
