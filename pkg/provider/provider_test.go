package provider

import (
	"context"
	"errors"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"testing"
)

type stubEvaluator struct {
	decision model.Decision
	err      error
}

func (s stubEvaluator) Evaluate(_ context.Context, _ model.RequestContext) (model.Decision, error) {
	return s.decision, s.err
}

func TestTelemetryRecordToRequestContext(t *testing.T) {
	rec := TelemetryRecord{
		Signal:      SignalLog,
		OrgID:       "acme",
		Service:     "payments",
		Operation:   "process",
		Severity:    "ERROR",
		Environment: "prod",
		Region:      "us-east-1",
		Endpoint:    "/charge",
		Method:      "POST",
		Tags:        map[string]string{"team": "pay"},
	}
	ctx := rec.ToRequestContext()
	if ctx.OrgID != "acme" || ctx.Service != "payments" || ctx.SignalType != "log" {
		t.Fatalf("unexpected conversion: %+v", ctx)
	}
	if ctx.Severity != "ERROR" || ctx.Environment != "prod" || ctx.Region != "us-east-1" {
		t.Fatalf("missing fields: %+v", ctx)
	}
	if ctx.Endpoint != "/charge" || ctx.Method != "POST" {
		t.Fatalf("missing http fields: %+v", ctx)
	}
}

func TestBatchProcessorAllowsAll(t *testing.T) {
	bp := BatchProcessor{
		Eval:     stubEvaluator{decision: model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "ok"}},
		Workers:  2,
		FailOpen: true,
	}
	records := []TelemetryRecord{
		{Signal: SignalLog, OrgID: "o", Service: "a"},
		{Signal: SignalSpan, OrgID: "o", Service: "b"},
		{Signal: SignalMetric, OrgID: "o", Service: "c"},
	}
	decisions := bp.Process(context.Background(), records)
	if len(decisions) != 3 {
		t.Fatalf("expected 3 decisions, got %d", len(decisions))
	}
	for i, d := range decisions {
		if !d.Allowed {
			t.Fatalf("decision %d not allowed", i)
		}
	}
}

func TestBatchProcessorDeniesAll(t *testing.T) {
	bp := BatchProcessor{
		Eval:     stubEvaluator{decision: model.Decision{Allowed: false, Action: model.ActionDeny, Reason: "limited"}},
		Workers:  1,
		FailOpen: false,
	}
	records := []TelemetryRecord{
		{Signal: SignalLog, OrgID: "o", Service: "a"},
	}
	decisions := bp.Process(context.Background(), records)
	if decisions[0].Allowed {
		t.Fatalf("expected denied")
	}
}

func TestBatchProcessorFailOpen(t *testing.T) {
	bp := BatchProcessor{
		Eval:     stubEvaluator{err: errors.New("engine down")},
		Workers:  1,
		FailOpen: true,
	}
	records := []TelemetryRecord{{Signal: SignalLog, OrgID: "o", Service: "a"}}
	decisions := bp.Process(context.Background(), records)
	if !decisions[0].Allowed {
		t.Fatalf("expected fail_open to allow")
	}
}

func TestBatchProcessorFailClosed(t *testing.T) {
	bp := BatchProcessor{
		Eval:     stubEvaluator{err: errors.New("engine down")},
		Workers:  1,
		FailOpen: false,
	}
	records := []TelemetryRecord{{Signal: SignalLog, OrgID: "o", Service: "a"}}
	decisions := bp.Process(context.Background(), records)
	if decisions[0].Allowed {
		t.Fatalf("expected fail_closed to deny")
	}
}

func TestFilterAllowed(t *testing.T) {
	decisions := []Decision{
		{Allowed: true, Record: TelemetryRecord{Service: "a"}},
		{Allowed: false, Record: TelemetryRecord{Service: "b"}},
		{Allowed: true, Record: TelemetryRecord{Service: "c"}},
	}
	kept := FilterAllowed(decisions)
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept, got %d", len(kept))
	}
	if kept[0].Service != "a" || kept[1].Service != "c" {
		t.Fatalf("wrong kept: %+v", kept)
	}
}

func TestBatchProcessorEmptyInput(t *testing.T) {
	bp := BatchProcessor{Eval: stubEvaluator{}, Workers: 2, FailOpen: true}
	if decisions := bp.Process(context.Background(), nil); decisions != nil {
		t.Fatalf("expected nil for empty input")
	}
}

func TestBatchProcessorZeroWorkers(t *testing.T) {
	bp := BatchProcessor{
		Eval:     stubEvaluator{decision: model.Decision{Allowed: true, Action: model.ActionAllow}},
		Workers:  0, // should default to 1
		FailOpen: true,
	}
	records := []TelemetryRecord{{Signal: SignalLog, OrgID: "o", Service: "a"}}
	decisions := bp.Process(context.Background(), records)
	if len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("unexpected: %+v", decisions)
	}
}
