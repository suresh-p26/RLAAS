package fluentbit

import (
	"context"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/pkg/model"
	"github.com/rlaas-io/rlaas/pkg/provider"
)

type stubEval struct {
	decision model.Decision
	err      error
}

func (s stubEval) Evaluate(_ context.Context, _ model.RequestContext) (model.Decision, error) {
	return s.decision, s.err
}

func TestAdapterNameAndSignals(t *testing.T) {
	a := NewAdapter(stubEval{}, "org", 1, true)
	if a.Name() != "fluentbit" {
		t.Fatalf("expected fluentbit, got %s", a.Name())
	}
	kinds := a.SignalKinds()
	if len(kinds) != 1 || kinds[0] != provider.SignalLog {
		t.Fatalf("expected [log], got %+v", kinds)
	}
}

func TestFilterRecordsAllowed(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, "fid", 2, true)
	records := []Record{
		{Timestamp: time.Now(), Tag: "kube.payments", Fields: map[string]string{"service_name": "payments", "level": "info"}},
		{Timestamp: time.Now(), Tag: "kube.auth", Fields: map[string]string{"service_name": "auth", "level": "warn"}},
	}
	kept := a.FilterRecords(context.Background(), records)
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept, got %d", len(kept))
	}
}

func TestFilterRecordsDenied(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: false, Action: model.ActionDrop}}, "fid", 1, false)
	records := []Record{
		{Fields: map[string]string{"service_name": "notifications", "level": "info"}},
	}
	kept := a.FilterRecords(context.Background(), records)
	if len(kept) != 0 {
		t.Fatalf("expected 0 kept, got %d", len(kept))
	}
}

func TestFilterRecordsTagFallback(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, "fid", 1, true)
	// No service_name field — should fall back to tag.
	records := []Record{
		{Tag: "myservice", Fields: map[string]string{"level": "error"}},
	}
	kept := a.FilterRecords(context.Background(), records)
	if len(kept) != 1 {
		t.Fatalf("expected 1 kept, got %d", len(kept))
	}
}

func TestFilterRecordsEmpty(t *testing.T) {
	a := NewAdapter(stubEval{}, "fid", 1, true)
	if kept := a.FilterRecords(context.Background(), nil); kept != nil {
		t.Fatalf("expected nil")
	}
}

func TestProcessBatch(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, "fid", 1, true)
	records := []provider.TelemetryRecord{
		{Signal: provider.SignalLog, OrgID: "fid", Service: "payments"},
	}
	decisions, err := a.ProcessBatch(context.Background(), records)
	if err != nil || len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("unexpected: %v %+v", err, decisions)
	}
}
