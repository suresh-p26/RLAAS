package concurrency

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type errLeaseStore struct{ store.CounterStore }

func (errLeaseStore) AcquireLease(context.Context, string, int64, time.Duration) (bool, int64, error) {
	return false, 0, errors.New("boom")
}

func cPolicy(maxConc int64) model.Policy {
	return model.Policy{Algorithm: model.AlgorithmConfig{MaxConcurrency: maxConc}, Action: model.ActionDeny}
}

func TestConcurrency_AllowThenDeny(t *testing.T) {
	s := memory.New()
	e := New(s)
	p := cPolicy(2)

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if err != nil || !d.Allowed {
		t.Fatalf("first should allow: %+v err=%v", d, err)
	}
	if d.Remaining != 1 {
		t.Fatalf("remaining should be 1, got %d", d.Remaining)
	}

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if err != nil || !d.Allowed {
		t.Fatalf("second should allow: %+v err=%v", d, err)
	}

	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("third should deny: %+v", d)
	}
}

func TestConcurrency_Release(t *testing.T) {
	s := memory.New()
	e := New(s)
	p := cPolicy(1)

	// Acquire the single slot.
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed {
		t.Fatalf("first should allow")
	}

	// Should deny (all slots taken).
	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("second should deny when slot taken")
	}

	// Release the slot.
	if err := e.Release(context.Background(), "k"); err != nil {
		t.Fatalf("release error: %v", err)
	}

	// Should allow again after release.
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if err != nil || !d.Allowed {
		t.Fatalf("after release should allow: %+v err=%v", d, err)
	}
}

func TestConcurrency_CustomLeaseTTL(t *testing.T) {
	s := memory.New()
	e := New(s)
	// LeaseTTL in seconds.
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{MaxConcurrency: 1, LeaseTTL: 300},
		Action:    model.ActionDeny,
	}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if err != nil || !d.Allowed {
		t.Fatalf("should allow: %+v err=%v", d, err)
	}

	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("should deny (slot used with custom TTL)")
	}
}

func TestConcurrency_DefaultLimitFallback(t *testing.T) {
	s := memory.New()
	e := New(s)
	// Both MaxConcurrency and Limit are 0 -> defaults to 1.
	p := model.Policy{Algorithm: model.AlgorithmConfig{}, Action: model.ActionDeny}

	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed {
		t.Fatalf("first with default limit should allow")
	}
	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("second with default limit should deny")
	}
}

func TestConcurrency_ShadowMode(t *testing.T) {
	s := memory.New()
	e := New(s)
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{MaxConcurrency: 1},
		Action:    model.ActionShadowOnly,
	}
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed || !d.ShadowMode {
		t.Fatalf("shadow mode should still allow: %+v", d)
	}
}

func TestConcurrency_ErrorPath(t *testing.T) {
	e := New(errLeaseStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestConcurrency_KeyIsolation(t *testing.T) {
	s := memory.New()
	e := New(s)
	p := cPolicy(1)

	d1, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key1")
	d2, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key2")
	if !d1.Allowed || !d2.Allowed {
		t.Fatalf("different keys should be independent")
	}
}
