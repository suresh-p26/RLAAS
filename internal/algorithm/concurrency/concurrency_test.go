package concurrency

import (
	"context"
	"errors"
	"rlaas/pkg/model"
	"rlaas/internal/store"
	"rlaas/internal/store/counter/memory"
	"testing"
	"time"
)

type errLeaseStore struct{ store.CounterStore }

func (e errLeaseStore) AcquireLease(context.Context, string, int64, time.Duration) (bool, int64, error) {
	return false, 0, errors.New("boom")
}

func TestConcurrencyAllowThenDeny(t *testing.T) {
	store := memory.New()
	e := New(store)
	policy := model.Policy{Algorithm: model.AlgorithmConfig{MaxConcurrency: 1}, Action: model.ActionDeny}
	if d, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "k"); err != nil || !d.Allowed {
		t.Fatalf("first should allow")
	}
	if d, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "k"); err != nil || d.Allowed {
		t.Fatalf("second should deny")
	}
}

func TestConcurrencyErrorPath(t *testing.T) {
	e := New(errLeaseStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestConcurrencyDefaultLimitFallback(t *testing.T) {
	store := memory.New()
	e := New(store)
	if d, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{}}, model.RequestContext{}, "k2"); err != nil || !d.Allowed {
		t.Fatalf("expected allow with default limit")
	}
}
