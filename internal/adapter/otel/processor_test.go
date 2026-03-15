package otel

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/suresh-p26/RLAAS/pkg/model"
	"github.com/suresh-p26/RLAAS/pkg/provider"
)

type processorEvalStub struct {
	decision model.Decision
	err      error
}

func (s processorEvalStub) Evaluate(_ context.Context, _ model.RequestContext) (model.Decision, error) {
	return s.decision, s.err
}

func TestProcessorProcessLogsAndSpans(t *testing.T) {
	p := NewProcessor(processorEvalStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, 4, true)
	logs := []LogRecord{{OrgID: "o", Service: "s", Severity: "info"}, {OrgID: "o", Service: "s", Severity: "warn"}}
	outLogs := p.ProcessLogs(context.Background(), logs)
	if len(outLogs) != 2 {
		t.Fatalf("expected all logs allowed")
	}
	spans := []SpanRecord{{OrgID: "o", Service: "s", Name: "span1"}}
	outSpans := p.ProcessSpans(context.Background(), spans)
	if len(outSpans) != 1 {
		t.Fatalf("expected span allowed")
	}
	stats := p.Stats()
	if stats.Allowed != 3 || stats.Dropped != 0 || stats.Errors != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestProcessorDropsDeniedAndFailClosedErrors(t *testing.T) {
	p := NewProcessor(processorEvalStub{decision: model.Decision{Allowed: false, Action: model.ActionDeny}}, 2, false)
	logs := []LogRecord{{OrgID: "o", Service: "s", Severity: "info"}}
	if out := p.ProcessLogs(context.Background(), logs); len(out) != 0 {
		t.Fatalf("expected denied logs to drop")
	}

	p2 := NewProcessor(processorEvalStub{err: errors.New("boom")}, 1, false)
	if out := p2.ProcessSpans(context.Background(), []SpanRecord{{OrgID: "o", Service: "s", Name: "n"}}); len(out) != 0 {
		t.Fatalf("expected fail-closed error drops")
	}
	stats := p2.Stats()
	if stats.Errors != 1 || stats.Dropped != 1 {
		t.Fatalf("unexpected fail-closed stats: %+v", stats)
	}
}

func TestDecisionFilter(t *testing.T) {
	if !DecisionFilter(model.Decision{Allowed: true, Action: model.ActionAllow}) {
		t.Fatalf("allow should pass")
	}
	if DecisionFilter(model.Decision{Allowed: false, Action: model.ActionAllow}) {
		t.Fatalf("not allowed should drop")
	}
	if DecisionFilter(model.Decision{Allowed: true, Action: model.ActionDrop}) {
		t.Fatalf("drop action should drop")
	}
}

// --- Additional coverage tests ---

func TestProcessorEmptyBatch(t *testing.T) {
	p := NewProcessor(processorEvalStub{decision: model.Decision{Allowed: true}}, 2, true)
	if out := p.ProcessLogs(context.Background(), nil); out != nil {
		t.Fatalf("expected nil for empty logs")
	}
	if out := p.ProcessSpans(context.Background(), nil); out != nil {
		t.Fatalf("expected nil for empty spans")
	}
}

func TestProcessorFailOpenOnError(t *testing.T) {
	// fail-open: error occurs but decision.Allowed is true → record kept
	p := NewProcessor(processorEvalStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}, err: errors.New("boom")}, 1, true)
	logs := []LogRecord{{OrgID: "o", Service: "s", Severity: "info"}}
	out := p.ProcessLogs(context.Background(), logs)
	if len(out) != 1 {
		t.Fatalf("expected fail-open to keep log, got %d", len(out))
	}
	stats := p.Stats()
	if stats.Errors != 1 || stats.Allowed != 1 {
		t.Fatalf("unexpected fail-open stats: %+v", stats)
	}

	// fail-open: error occurs and decision.Allowed is false → still dropped by allowed check
	p2 := NewProcessor(processorEvalStub{err: errors.New("boom")}, 1, true)
	logs2 := []LogRecord{{OrgID: "o", Service: "s", Severity: "info"}}
	out2 := p2.ProcessLogs(context.Background(), logs2)
	if len(out2) != 0 {
		t.Fatalf("expected fail-open with Allowed=false to drop, got %d", len(out2))
	}
}

func TestProcessorFailOpenSpans(t *testing.T) {
	p := NewProcessor(processorEvalStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}, err: errors.New("boom")}, 2, true)
	spans := []SpanRecord{{OrgID: "o", Service: "s", Name: "sp"}}
	out := p.ProcessSpans(context.Background(), spans)
	if len(out) != 1 {
		t.Fatalf("expected fail-open to keep span, got %d", len(out))
	}
}

func TestProcessorMixedDecisions(t *testing.T) {
	callCount := 0
	eval := &alternatingEval{}
	p := NewProcessor(eval, 1, false)
	logs := []LogRecord{
		{OrgID: "o", Service: "s", Severity: "info"},
		{OrgID: "o", Service: "s", Severity: "warn"},
		{OrgID: "o", Service: "s", Severity: "error"},
	}
	_ = callCount
	out := p.ProcessLogs(context.Background(), logs)
	stats := p.Stats()
	if stats.Allowed+stats.Dropped != int64(len(logs)) {
		t.Fatalf("expected all logs accounted for: %+v", stats)
	}
	if len(out) > len(logs) {
		t.Fatalf("more output than input")
	}
}

type alternatingEval struct {
	mu    sync.Mutex
	count int
}

func (a *alternatingEval) Evaluate(_ context.Context, _ model.RequestContext) (model.Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.count++
	if a.count%2 == 0 {
		return model.Decision{Allowed: false, Action: model.ActionDeny}, nil
	}
	return model.Decision{Allowed: true, Action: model.ActionAllow}, nil
}

func TestProcessorMultipleWorkers(t *testing.T) {
	p := NewProcessor(processorEvalStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, 8, true)
	logs := make([]LogRecord, 100)
	for i := range logs {
		logs[i] = LogRecord{OrgID: "o", Service: "s", Severity: "info"}
	}
	out := p.ProcessLogs(context.Background(), logs)
	if len(out) != 100 {
		t.Fatalf("expected 100, got %d", len(out))
	}
}

func TestProviderAdapterName(t *testing.T) {
	a := NewProviderAdapter(processorEvalStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, 2, true)
	if a.Name() != "otel" {
		t.Fatalf("expected otel, got %s", a.Name())
	}
	kinds := a.SignalKinds()
	if len(kinds) != 2 {
		t.Fatalf("expected 2 signal kinds, got %d", len(kinds))
	}
}

func TestProviderAdapterProcessBatch(t *testing.T) {
	a := NewProviderAdapter(processorEvalStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, 2, true)
	records := []provider.TelemetryRecord{
		{Signal: provider.SignalLog, OrgID: "o", Service: "s", Severity: "info"},
		{Signal: provider.SignalSpan, OrgID: "o", Service: "s", Operation: "span1"},
		{Signal: provider.SignalKind("unknown"), OrgID: "o", Service: "s"},
	}
	results, err := a.ProcessBatch(context.Background(), records)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// Log and span should be allowed
	if !results[0].Allowed || !results[1].Allowed {
		t.Fatal("expected allowed for log and span")
	}
	// Unknown signal passes through
	if !results[2].Allowed || results[2].Reason != "unsupported_signal" {
		t.Fatal("expected unsupported_signal passthrough")
	}
}

func TestProviderAdapterDeniedBatch(t *testing.T) {
	a := NewProviderAdapter(processorEvalStub{decision: model.Decision{Allowed: false, Action: model.ActionDeny}}, 2, false)
	records := []provider.TelemetryRecord{
		{Signal: provider.SignalLog, OrgID: "o", Service: "s", Severity: "info"},
		{Signal: provider.SignalSpan, OrgID: "o", Service: "s", Operation: "span1"},
	}
	results, err := a.ProcessBatch(context.Background(), records)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, r := range results {
		if r.Allowed {
			t.Fatalf("expected denied for record %d", i)
		}
	}
}

func TestDecisionFilterDropLowPriority(t *testing.T) {
	if DecisionFilter(model.Decision{Allowed: true, Action: model.ActionDropLowPriority}) {
		t.Fatal("drop_low_priority should drop")
	}
	if DecisionFilter(model.Decision{Allowed: true, Action: model.ActionDeny}) {
		t.Fatal("deny should drop")
	}
}
