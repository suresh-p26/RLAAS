package tokenbucket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
)

// tbSetErrStore: CAS succeeds but Set (timestamp write) fails.
type tbSetErrStore struct {
	store.CounterStore
}

func (tbSetErrStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (tbSetErrStore) CompareAndSwap(_ context.Context, _ string, _, _ int64, _ time.Duration) (bool, error) {
	return true, nil
}
func (tbSetErrStore) Set(context.Context, string, int64, time.Duration) error {
	return errors.New("set failed")
}

// tbCASAlwaysFalseStore: CAS always returns false (no error), simulating contention.
type tbCASAlwaysFalseStore struct {
	store.CounterStore
}

func (tbCASAlwaysFalseStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (tbCASAlwaysFalseStore) CompareAndSwap(_ context.Context, _ string, _, _ int64, _ time.Duration) (bool, error) {
	return false, nil
}

func TestTokenBucket_SetErrorAfterCAS(t *testing.T) {
	e := New(&tbSetErrStore{})
	p := tbPolicy(1, 1, 1)
	if _, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected set error after successful CAS")
	}
}

func TestTokenBucket_ContentionExhaustsRetries(t *testing.T) {
	e := New(&tbCASAlwaysFalseStore{})
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := tbPolicy(10, 10, 1)
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if err != nil {
		t.Fatalf("contention should not return error: %v", err)
	}
	if d.Allowed {
		t.Fatalf("contention exhaustion should deny: %+v", d)
	}
	if d.Reason != "token_bucket_contention" {
		t.Fatalf("reason should be contention, got %s", d.Reason)
	}
}
