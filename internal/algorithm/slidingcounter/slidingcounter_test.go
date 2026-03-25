package slidingcounter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type swcErrStore struct{ store.CounterStore }

func (swcErrStore) Get(context.Context, string) (int64, error) { return 0, errors.New("boom") }

type swcCASAlwaysFalseStore struct{ store.CounterStore }

func (swcCASAlwaysFalseStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (swcCASAlwaysFalseStore) CompareAndSwap(context.Context, string, int64, int64, time.Duration) (bool, error) {
	return false, nil
}

func swcPolicy(limit int64, window string) model.Policy {
	return model.Policy{Algorithm: model.AlgorithmConfig{Limit: limit, Window: window}, Action: model.ActionDeny}
}

func TestSlidingCounter_AllowThenDeny(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := swcPolicy(2, "1m")

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

func TestSlidingCounter_WeightedPreviousWindow(t *testing.T) {
	s := memory.New()
	e := New(s)

	// Start at the very beginning of a minute boundary.
	now := time.Unix(60, 0) // unix 60 = start of minute 1
	e.Now = func() time.Time { return now }
	p := swcPolicy(10, "1m")

	// Fill 8 requests in this window (minute 1: [60,120)).
	for i := 0; i < 8; i++ {
		e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	}

	// Move to 30s into the next window (minute 2: [120,180)).
	// Previous window (minute 1) had 8, weight = (60-30)/60 = 0.5.
	// estimated = 0 + 8*0.5 = 4, so 6 more should be allowed.
	now = time.Unix(150, 0) // 30s into minute 2
	for i := 0; i < 6; i++ {
		d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
		if err != nil || !d.Allowed {
			t.Fatalf("request %d should be allowed (weight=0.5): %+v err=%v", i+1, d, err)
		}
	}

	// Now estimated = 6 + 8*0.5 = 10, next should deny.
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("should deny when estimated reaches limit")
	}
}

func TestSlidingCounter_WindowRollover(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(60, 0)
	e.Now = func() time.Time { return now }
	p := swcPolicy(2, "1m")

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")

	// Full window rollover: advance 2 full minutes so prev window has no data.
	now = time.Unix(180, 0)
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed {
		t.Fatalf("after full rollover should allow: %+v", d)
	}
}

func TestSlidingCounter_CustomCost(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(60, 0)
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

func TestSlidingCounter_DefaultLimit(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(60, 0)
	e.Now = func() time.Time { return now }

	p := swcPolicy(0, "1m") // limit=0 defaults to 1
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed {
		t.Fatalf("first with default limit should allow")
	}
	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("second with default limit should deny")
	}
}

func TestSlidingCounter_RetryAfter(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(60, 0)
	e.Now = func() time.Time { return now }

	p := swcPolicy(1, "1m")
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.RetryAfter <= 0 {
		t.Fatalf("RetryAfter should be positive, got %v", d.RetryAfter)
	}
}

func TestSlidingCounter_ShadowMode(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(60, 0)
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

func TestSlidingCounter_ErrorPath(t *testing.T) {
	e := New(swcErrStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestSlidingCounter_KeyIsolation(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(60, 0)
	e.Now = func() time.Time { return now }

	p := swcPolicy(1, "1m")
	d1, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key1")
	d2, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key2")
	if !d1.Allowed || !d2.Allowed {
		t.Fatalf("different keys should be independent")
	}
}

func TestSlidingCounter_ContentionExhaustsRetries(t *testing.T) {
	e := New(swcCASAlwaysFalseStore{})
	now := time.Unix(60, 0)
	e.Now = func() time.Time { return now }

	p := swcPolicy(10, "1m")
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if err != nil {
		t.Fatalf("contention should not error: %v", err)
	}
	if d.Allowed {
		t.Fatalf("contention exhaustion should deny")
	}
	if d.Reason != "sliding_window_contention" {
		t.Fatalf("unexpected reason: %s", d.Reason)
	}
}
