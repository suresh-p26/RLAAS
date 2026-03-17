package envoy

import (
	"context"
	"github.com/rlaas-io/rlaas/pkg/model"
	"github.com/rlaas-io/rlaas/pkg/provider"
	"testing"
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
	if a.Name() != "envoy" {
		t.Fatalf("expected envoy, got %s", a.Name())
	}
	kinds := a.SignalKinds()
	if len(kinds) != 1 || kinds[0] != provider.SignalHTTP {
		t.Fatalf("expected [http], got %+v", kinds)
	}
}

func TestCheckRateLimitAllowed(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow, Remaining: 99}}, "fid", 2, true)
	descriptors := []Descriptor{
		{Entries: []DescriptorEntry{
			{Key: "service", Value: "risk-api"},
			{Key: "path", Value: "/v1/risk/assess"},
			{Key: "method", Value: "POST"},
		}},
	}
	resp := a.CheckRateLimit(context.Background(), "fidelity", descriptors)
	if resp.OverallCode != RateLimitOK {
		t.Fatalf("expected OK, got %d", resp.OverallCode)
	}
	if len(resp.Statuses) != 1 || resp.Statuses[0].Code != RateLimitOK {
		t.Fatalf("unexpected status: %+v", resp.Statuses)
	}
}

func TestCheckRateLimitDenied(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: false, Action: model.ActionDeny}}, "fid", 1, false)
	descriptors := []Descriptor{
		{Entries: []DescriptorEntry{{Key: "service", Value: "risk-api"}}},
	}
	resp := a.CheckRateLimit(context.Background(), "fidelity", descriptors)
	if resp.OverallCode != RateLimitOverLimit {
		t.Fatalf("expected OVER_LIMIT, got %d", resp.OverallCode)
	}
}

func TestCheckRateLimitEmpty(t *testing.T) {
	a := NewAdapter(stubEval{}, "fid", 1, true)
	resp := a.CheckRateLimit(context.Background(), "fidelity", nil)
	if resp.OverallCode != RateLimitOK {
		t.Fatalf("expected OK for empty descriptors")
	}
}

func TestCheckAuthAllowed(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, "fid", 1, true)
	resp := a.CheckAuth(context.Background(), AuthRequest{
		Method: "GET", Path: "/health", Service: "risk-api",
	})
	if !resp.Allowed || resp.StatusCode != 200 {
		t.Fatalf("expected allowed: %+v", resp)
	}
}

func TestCheckAuthDenied(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: false, Action: model.ActionDeny, Reason: "rate_limited"}}, "fid", 1, false)
	resp := a.CheckAuth(context.Background(), AuthRequest{
		Method: "POST", Path: "/v1/risk/assess", Service: "risk-api",
	})
	if resp.Allowed || resp.StatusCode != 429 {
		t.Fatalf("expected 429: %+v", resp)
	}
	if resp.Headers["x-rlaas-reason"] != "rate_limited" {
		t.Fatalf("expected x-rlaas-reason header")
	}
}

func TestProcessBatch(t *testing.T) {
	a := NewAdapter(stubEval{decision: model.Decision{Allowed: true, Action: model.ActionAllow}}, "fid", 1, true)
	records := []provider.TelemetryRecord{
		{Signal: provider.SignalHTTP, OrgID: "fid", Service: "risk-api"},
	}
	decisions, err := a.ProcessBatch(context.Background(), records)
	if err != nil || len(decisions) != 1 || !decisions[0].Allowed {
		t.Fatalf("unexpected: %v %+v", err, decisions)
	}
}
