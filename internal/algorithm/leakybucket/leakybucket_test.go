package leakybucket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type lbSetErrStore struct {
	store.CounterStore
}

func (lbSetErrStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (lbSetErrStore) CompareAndSwap(context.Context, string, int64, int64, time.Duration) (bool, error) {
	return true, nil
}
func (lbSetErrStore) Set(_ context.Context, _ string, _ int64, _ time.Duration) error {
	return errors.New("set failed")
}

type lbGetErrStore struct{ store.CounterStore }

func (lbGetErrStore) Get(context.Context, string) (int64, error) { return 0, errors.New("get error") }

type lbCASErrStore struct{ store.CounterStore }

func (lbCASErrStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (lbCASErrStore) CompareAndSwap(context.Context, string, int64, int64, time.Duration) (bool, error) {
	return false, errors.New("cas error")
}

type lbCASAlwaysFalseStore struct{ store.CounterStore }

func (lbCASAlwaysFalseStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (lbCASAlwaysFalseStore) CompareAndSwap(context.Context, string, int64, int64, time.Duration) (bool, error) {
	return false, nil
}

func lbPolicy(limit int64, window string, leakRate float64) model.Policy {
	return model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: limit, Window: window, LeakRate: leakRate},
		Action:    model.ActionDeny,
	}
}

func TestLeakyBucket_AllowThenDeny(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := lbPolicy(2, "1s", 1.0)
	req := model.RequestContext{}

	d, err := e.Evaluate(context.Background(), p, req, "k")
	if err != nil || !d.Allowed {
		t.Fatalf("first should allow: %+v err=%v", d, err)
	}
	if d.Remaining != 1 {
		t.Fatalf("remaining should be 1, got %d", d.Remaining)
	}

	d, err = e.Evaluate(context.Background(), p, req, "k")
	if err != nil || !d.Allowed {
		t.Fatalf("second should allow: %+v err=%v", d, err)
	}

	d, err = e.Evaluate(context.Background(), p, req, "k")
	if err != nil || d.Allowed {
		t.Fatalf("third should deny: %+v err=%v", d, err)
	}
	if d.RetryAfter <= 0 {
		t.Fatalf("RetryAfter should be positive, got %v", d.RetryAfter)
	}
}

func TestLeakyBucket_TimeBasedLeak(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := lbPolicy(2, "1s", 2.0)
	req := model.RequestContext{}

	e.Evaluate(context.Background(), p, req, "k")
	e.Evaluate(context.Background(), p, req, "k")

	d, _ := e.Evaluate(context.Background(), p, req, "k")
	if d.Allowed {
		t.Fatalf("should deny when full at same time")
	}

	now = now.Add(500 * time.Millisecond)
	d, err := e.Evaluate(context.Background(), p, req, "k")
	if err != nil || !d.Allowed {
		t.Fatalf("after 0.5s leak should allow: %+v err=%v", d, err)
	}

	now = now.Add(time.Second)
	d, err = e.Evaluate(context.Background(), p, req, "k")
	if err != nil || !d.Allowed {
		t.Fatalf("after full leak should allow: %+v err=%v", d, err)
	}
}

func TestLeakyBucket_CustomCost(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 5, Window: "1s", LeakRate: 1.0, CostPerRequest: 3},
		Action:    model.ActionDeny,
	}

	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed || d.Remaining != 2 {
		t.Fatalf("first should allow with remaining 2: %+v", d)
	}

	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("second should deny (overflow)")
	}
}

func TestLeakyBucket_Quantity(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := lbPolicy(5, "1s", 1.0)

	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{Quantity: 4}, "k")
	if !d.Allowed {
		t.Fatalf("should allow cost=4")
	}

	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{Quantity: 4}, "k")
	if d.Allowed {
		t.Fatalf("should deny")
	}
}

func TestLeakyBucket_DefaultLeakRate(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := lbPolicy(1, "1s", 0) // defaults to 1
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if err != nil || !d.Allowed {
		t.Fatalf("should allow with default leak rate: %+v err=%v", d, err)
	}
}

func TestLeakyBucket_DefaultLimit(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := lbPolicy(0, "1s", 1)
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed {
		t.Fatalf("first should allow with default limit")
	}
	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("second should deny with default limit")
	}
}

func TestLeakyBucket_ShadowMode(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1s", LeakRate: 1.0},
		Action:    model.ActionShadowOnly,
	}
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed || !d.ShadowMode {
		t.Fatalf("shadow mode should still allow: %+v", d)
	}
}

func TestLeakyBucket_SetError(t *testing.T) {
	e := New(&lbSetErrStore{})
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(10, "1s", 1.0)
	if _, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected set error")
	}
}

func TestLeakyBucket_GetError(t *testing.T) {
	e := New(&lbGetErrStore{})
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(10, "1s", 1.0)
	if _, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected get error")
	}
}

func TestLeakyBucket_CASError(t *testing.T) {
	e := New(&lbCASErrStore{})
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(10, "1s", 1.0)
	if _, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected CAS error")
	}
}

func TestLeakyBucket_ContentionExhaustsRetries(t *testing.T) {
	e := New(&lbCASAlwaysFalseStore{})
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(10, "1s", 1.0)
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if err != nil {
		t.Fatalf("contention should not error: %v", err)
	}
	if d.Allowed {
		t.Fatalf("contention exhaustion should deny")
	}
	if d.Reason != "leaky_bucket_contention" {
		t.Fatalf("reason should be contention, got %s", d.Reason)
	}
}

func TestLeakyBucket_KeyIsolation(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := lbPolicy(1, "1s", 1.0)
	d1, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key1")
	d2, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key2")
	if !d1.Allowed || !d2.Allowed {
		t.Fatalf("different keys should be independent")
	}
}
