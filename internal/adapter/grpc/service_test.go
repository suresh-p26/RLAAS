package grpcadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	rlaasv1 "github.com/rlaas-io/rlaas/api/proto"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type leaseEvalStub struct {
	evaluateDecision model.Decision
	evaluateErr      error
	leaseDecision    model.Decision
	releaseFn        func() error
	leaseErr         error
}

func (s leaseEvalStub) Evaluate(_ context.Context, _ model.RequestContext) (model.Decision, error) {
	return s.evaluateDecision, s.evaluateErr
}

func (s leaseEvalStub) StartConcurrencyLease(_ context.Context, _ model.RequestContext) (model.Decision, func() error, error) {
	return s.leaseDecision, s.releaseFn, s.leaseErr
}

func TestRateLimitServiceCheckLimit(t *testing.T) {
	svc := NewRateLimitService(leaseEvalStub{evaluateDecision: model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "ok", Remaining: 12, RetryAfter: 2500 * time.Millisecond}})
	resp, err := svc.CheckLimit(context.Background(), &rlaasv1.CheckLimitRequest{RequestId: "r1"})
	if err != nil {
		t.Fatalf("expected no error: %v", err)
	}
	if !resp.GetAllowed() || resp.GetAction() != string(model.ActionAllow) || resp.GetRemaining() != 12 || resp.GetRetryAfterMs() != 2500 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestRateLimitServiceCheckLimitError(t *testing.T) {
	svc := NewRateLimitService(leaseEvalStub{evaluateErr: errors.New("boom")})
	if _, err := svc.CheckLimit(context.Background(), &rlaasv1.CheckLimitRequest{}); err == nil {
		t.Fatalf("expected evaluate error")
	}
}

func TestRateLimitServiceAcquireAndRelease(t *testing.T) {
	released := false
	svc := NewRateLimitService(leaseEvalStub{
		leaseDecision: model.Decision{Allowed: true, Reason: "ok"},
		releaseFn: func() error {
			released = true
			return nil
		},
	})

	acq, err := svc.Acquire(context.Background(), &rlaasv1.AcquireRequest{RequestId: "a1"})
	if err != nil {
		t.Fatalf("expected no acquire error: %v", err)
	}
	if !acq.GetAllowed() || acq.GetLeaseId() == "" {
		t.Fatalf("expected lease id for allowed acquire")
	}

	rel, err := svc.Release(context.Background(), &rlaasv1.ReleaseRequest{LeaseId: acq.GetLeaseId()})
	if err != nil {
		t.Fatalf("expected no release error: %v", err)
	}
	if !rel.GetReleased() || !released {
		t.Fatalf("expected released true")
	}
}

func TestRateLimitServiceAcquireDeniedAndReleaseMissing(t *testing.T) {
	svc := NewRateLimitService(leaseEvalStub{leaseDecision: model.Decision{Allowed: false, Reason: "denied"}})
	acq, err := svc.Acquire(context.Background(), &rlaasv1.AcquireRequest{})
	if err != nil {
		t.Fatalf("expected no acquire error: %v", err)
	}
	if acq.GetAllowed() || acq.GetLeaseId() != "" {
		t.Fatalf("expected denied acquire without lease id")
	}

	rel, err := svc.Release(context.Background(), &rlaasv1.ReleaseRequest{LeaseId: "missing"})
	if err != nil {
		t.Fatalf("expected no release error: %v", err)
	}
	if rel.GetReleased() {
		t.Fatalf("expected released false for missing lease")
	}
}

func TestRateLimitServiceAcquireAndReleaseErrors(t *testing.T) {
	svc := NewRateLimitService(leaseEvalStub{leaseErr: errors.New("lease failed")})
	if _, err := svc.Acquire(context.Background(), &rlaasv1.AcquireRequest{}); err == nil {
		t.Fatalf("expected acquire error")
	}

	svc = NewRateLimitService(leaseEvalStub{leaseDecision: model.Decision{Allowed: true}, releaseFn: func() error { return errors.New("release failed") }})
	acq, err := svc.Acquire(context.Background(), &rlaasv1.AcquireRequest{})
	if err != nil || acq.GetLeaseId() == "" {
		t.Fatalf("expected acquire with lease id")
	}
	rel, err := svc.Release(context.Background(), &rlaasv1.ReleaseRequest{LeaseId: acq.GetLeaseId()})
	if err != nil {
		t.Fatalf("expected no release error")
	}
	if rel.GetReleased() {
		t.Fatalf("expected released false when release fn fails")
	}
}

func TestNonEmpty(t *testing.T) {
	if got := nonEmpty("", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback")
	}
	if got := nonEmpty("value", "fallback"); got != "value" {
		t.Fatalf("expected value")
	}
}
