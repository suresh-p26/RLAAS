package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type quotaErrStore struct{ store.CounterStore }

func (quotaErrStore) Get(context.Context, string) (int64, error) {
	return 0, errors.New("boom")
}

type quotaCASFalseStore struct{ store.CounterStore }

func (quotaCASFalseStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (quotaCASFalseStore) CompareAndSwap(context.Context, string, int64, int64, time.Duration) (bool, error) {
	return false, nil
}

func TestQuotaAllowThenDeny(t *testing.T) {
	store := memory.New()
	e := New(store)
	e.Now = func() time.Time { return time.Unix(1000, 0) }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, QuotaPeriod: "day"}, Action: model.ActionDeny}
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || !d.Allowed {
		t.Fatalf("first should allow")
	}
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || d.Allowed {
		t.Fatalf("second should deny")
	}
}

func TestQuotaErrorPath(t *testing.T) {
	e := New(quotaErrStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestQuota_SubSecondPeriodUsesDistinctBuckets(t *testing.T) {
	store := memory.New()
	e := New(store)
	now := time.Unix(1000, 100*int64(time.Millisecond))
	e.Now = func() time.Time { return now }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, QuotaPeriod: "100ms"}, Action: model.ActionDeny}

	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || !d.Allowed {
		t.Fatalf("first request should be allowed: %+v err=%v", d, err)
	}
	now = now.Add(100 * time.Millisecond)
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || !d.Allowed {
		t.Fatalf("next sub-second period should use a new bucket: %+v err=%v", d, err)
	}
}

func TestQuota_ContentionExhaustsRetries(t *testing.T) {
	e := New(quotaCASFalseStore{})
	e.Now = func() time.Time { return time.Unix(1000, 0) }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 10, QuotaPeriod: "day"}, Action: model.ActionDeny}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if err != nil {
		t.Fatalf("contention should not error: %v", err)
	}
	if d.Allowed {
		t.Fatalf("contention exhaustion should deny")
	}
	if d.Reason != "quota_contention" {
		t.Fatalf("expected quota_contention, got %s", d.Reason)
	}
}
