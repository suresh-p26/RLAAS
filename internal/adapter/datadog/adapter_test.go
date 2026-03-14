package datadog

import (
	"context"
	"rlaas/internal/model"
	"rlaas/internal/provider"
	"testing"
)

type stubEval struct {
	decision model.Decision
	err      error
}

func (s stubEval) Evaluate(_ context.Context, _ model.RequestContext) (model.Decision, error) {
	return s.decision, s.err
}

func TestAdapterName(t *testing.T) {
	a := NewAdapter(stubEval{}, "org", 1, true)
	if a.Name() != "datadog" {
		t.Fatalf("expected datadog, got %s", a.Name())
	}
}

func TestAdapterSignalKinds(t *testing.T) {
	a := NewAdapter(stubEval{}, "org", 1, true)
	kinds := a.SignalKinds()
	if len(kinds) != 2 {
		t.Fatalf("expected 2 kinds, got %d", len(kinds))
	}
}

func TestFilterLogsAllowed(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, "fid", 2, true)
	logs := []LogEntry{
		{Service: "payments", Status: "info", Message: "tx ok"},
		{Service: "auth", Status: "warn", Message: "slow"},
	}
	kept := a.FilterLogs(context.Background(), logs)
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept, got %d", len(kept))
	}
}

func TestFilterLogsDenied(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: false, Action: model.ActionDeny}}, "fid", 1, false)
	logs := []LogEntry{{Service: "payments", Status: "info"}}
	kept := a.FilterLogs(context.Background(), logs)
	if len(kept) != 0 {
		t.Fatalf("expected 0 kept, got %d", len(kept))
	}
}

func TestFilterMetricsAllowed(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, "fid", 1, true)
	metrics := []MetricSample{
		{Metric: "cpu.user", Service: "market-data", Type: "gauge"},
	}
	kept := a.FilterMetrics(context.Background(), metrics)
	if len(kept) != 1 {
		t.Fatalf("expected 1 kept, got %d", len(kept))
	}
}

func TestFilterLogsEmpty(t *testing.T) {
	a := NewAdapter(stubEval{}, "fid", 1, true)
	if kept := a.FilterLogs(context.Background(), nil); kept != nil {
		t.Fatalf("expected nil for empty input")
	}
}

func TestFilterMetricsEmpty(t *testing.T) {
	a := NewAdapter(stubEval{}, "fid", 1, true)
	if kept := a.FilterMetrics(context.Background(), nil); kept != nil {
		t.Fatalf("expected nil for empty input")
	}
}

func TestProcessBatch(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, "fid", 2, true)
	records := []provider.TelemetryRecord{
		{Signal: provider.SignalLog, OrgID: "fid", Service: "auth"},
	}
	decisions, err := a.ProcessBatch(context.Background(), records)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("unexpected: %+v", decisions)
	}
}

func TestMergeTagSlice(t *testing.T) {
	tags := []string{"env:prod", "service:payments", "novalue"}
	attrs := map[string]string{"extra": "val"}
	m := mergeTagSlice(tags, attrs)
	if m["env"] != "prod" || m["service"] != "payments" || m["extra"] != "val" {
		t.Fatalf("unexpected merge: %+v", m)
	}
}
