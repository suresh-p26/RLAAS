package kafka

import (
	"context"
	"rlaas/pkg/model"
	"rlaas/pkg/provider"
	"testing"
	"time"
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
	if a.Name() != "kafka" {
		t.Fatalf("expected kafka, got %s", a.Name())
	}
	kinds := a.SignalKinds()
	if len(kinds) != 1 || kinds[0] != provider.SignalEvent {
		t.Fatalf("expected [event], got %+v", kinds)
	}
}

func TestFilterMessagesAllowed(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, "fid", 2, true)
	msgs := []Message{
		{Topic: "orders.placed", Service: "order-service", Key: "o-123", Timestamp: time.Now()},
		{Topic: "orders.placed", Service: "order-service", Key: "o-456", Timestamp: time.Now(), ConsumerGroup: "order-processor"},
	}
	kept := a.FilterMessages(context.Background(), msgs)
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept, got %d", len(kept))
	}
}

func TestFilterMessagesDenied(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: false, Action: model.ActionDeny}}, "fid", 1, false)
	msgs := []Message{{Topic: "orders.placed", Service: "order-service", Key: "o-1"}}
	kept := a.FilterMessages(context.Background(), msgs)
	if len(kept) != 0 {
		t.Fatalf("expected 0 kept, got %d", len(kept))
	}
}

func TestFilterMessagesEmpty(t *testing.T) {
	a := NewAdapter(stubEval{}, "fid", 1, true)
	if kept := a.FilterMessages(context.Background(), nil); kept != nil {
		t.Fatalf("expected nil for empty input")
	}
}

func TestProcessBatch(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, "fid", 1, true)
	records := []provider.TelemetryRecord{
		{Signal: provider.SignalEvent, OrgID: "fid", Service: "order-service", Operation: "orders.placed"},
	}
	decisions, err := a.ProcessBatch(context.Background(), records)
	if err != nil || len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("unexpected: %v %+v", err, decisions)
	}
}
