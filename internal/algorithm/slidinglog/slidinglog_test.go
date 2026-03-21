package slidinglog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type swlAddErrStore struct{ store.CounterStore }

func (swlAddErrStore) TrimBefore(context.Context, string, time.Time) error { return nil }
func (swlAddErrStore) CountAfter(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}
func (swlAddErrStore) AddTimestamp(context.Context, string, time.Time, time.Duration) error {
	return errors.New("boom")
}

func slPolicy(limit int64, window string) model.Policy {
	return model.Policy{Algorithm: model.AlgorithmConfig{Limit: limit, Window: window}, Action: model.ActionDeny}
}

func TestSlidingLog_AllowThenDeny(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := slPolicy(2, "1m")

	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed || d.Remaining != 1 {
		t.Fatalf("first should allow remaining=1: %+v", d)
	}
	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed || d.Remaining != 0 {
		t.Fatalf("second should allow remaining=0: %+v", d)
	}
	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("third should deny: %+v", d)
	}
}

func TestSlidingLog_NoPollutionOnDeny(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := slPolicy(1, "1m")

	// Allow first.
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	// Deny second (should NOT add timestamp).
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")

	// Count entries - should be exactly 1.
	count, _ := s.CountAfter(context.Background(), "k:swl", now.Add(-time.Minute))
	if count != 1 {
		t.Fatalf("denied request should not pollute log: count=%d", count)
	}
}

func TestSlidingLog_WindowExpiry(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := slPolicy(1, "1m")
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")

	// Move past the window.
	now = now.Add(61 * time.Second)
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed {
		t.Fatalf("after window expiry should allow: %+v", d)
	}
}

func TestSlidingLog_CostPerRequest(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 5, Window: "1m", CostPerRequest: 3},
		Action:    model.ActionDeny,
	}

	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed || d.Remaining != 2 {
		t.Fatalf("first with cost=3 should allow remaining=2: %+v", d)
	}

	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("second with cost=3 should deny (6 > 5): %+v", d)
	}
}

func TestSlidingLog_Quantity(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := slPolicy(5, "1m")

	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{Quantity: 4}, "k")
	if !d.Allowed || d.Remaining != 1 {
		t.Fatalf("quantity=4 should allow remaining=1: %+v", d)
	}

	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{Quantity: 4}, "k")
	if d.Allowed {
		t.Fatalf("quantity=4 should deny (8 > 5): %+v", d)
	}
}

func TestSlidingLog_RetryAfter(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := slPolicy(1, "1m")
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")

	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.RetryAfter <= 0 {
		t.Fatalf("RetryAfter should be positive: %v", d.RetryAfter)
	}
}

func TestSlidingLog_DefaultLimit(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := slPolicy(0, "1m") // limit=0 defaults to 1
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed {
		t.Fatalf("first should allow with default limit")
	}
	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("second should deny with default limit")
	}
}

func TestSlidingLog_ShadowMode(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"},
		Action:    model.ActionShadowOnly,
	}
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed || !d.ShadowMode {
		t.Fatalf("shadow mode should still allow: %+v", d)
	}
}

func TestSlidingLog_AddTimestampError(t *testing.T) {
	e := New(swlAddErrStore{})
	p := slPolicy(10, "1m")
	if _, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected add timestamp error")
	}
}

func TestSlidingLog_KeyIsolation(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := slPolicy(1, "1m")
	d1, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key1")
	d2, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key2")
	if !d1.Allowed || !d2.Allowed {
		t.Fatalf("different keys should be independent")
	}
}
