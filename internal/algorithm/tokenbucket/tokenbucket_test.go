package tokenbucket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type tbCASErrStore struct{ store.CounterStore }

func (tbCASErrStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (tbCASErrStore) CompareAndSwap(context.Context, string, int64, int64, time.Duration) (bool, error) {
	return false, errors.New("cas error")
}

type tbGetErrStore struct{ store.CounterStore }

func (tbGetErrStore) Get(context.Context, string) (int64, error) { return 0, errors.New("get error") }

func tbPolicy(limit, burst int64, refill float64) model.Policy {
	return model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoTokenBucket, Limit: limit, Burst: burst, RefillRate: refill},
		Action:    model.ActionDeny,
	}
}

func TestTokenBucket_BurstThenThrottle(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := tbPolicy(1, 3, 1)

	for i := 0; i < 3; i++ {
		d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
		if err != nil || !d.Allowed {
			t.Fatalf("request %d should be allowed within burst: %+v err=%v", i+1, d, err)
		}
	}

	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("should deny after burst: %+v", d)
	}
	if d.RetryAfter <= 0 {
		t.Fatalf("RetryAfter should be positive: %v", d.RetryAfter)
	}
}

func TestTokenBucket_RefillOverTime(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := tbPolicy(1, 2, 1) // refill=1/s

	// Consume all tokens.
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("should be depleted")
	}

	// Advance 1 second, refill 1 token.
	now = now.Add(time.Second)
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if err != nil || !d.Allowed {
		t.Fatalf("after 1s refill should allow: %+v err=%v", d, err)
	}
}

func TestTokenBucket_RefillCapsAtCapacity(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := tbPolicy(1, 2, 10) // fast refill

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")

	// Advance 10 seconds, refill = 100 but should cap at burst=2.
	now = now.Add(10 * time.Second)
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed {
		t.Fatalf("should allow after refill: %+v", d)
	}
	// remaining should be burst-1 = 1 at most.
	if d.Remaining > 2 {
		t.Fatalf("remaining should be capped at capacity, got %d", d.Remaining)
	}
}

func TestTokenBucket_CustomCost(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 5, Burst: 5, RefillRate: 1, CostPerRequest: 3},
		Action:    model.ActionDeny,
	}

	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed || d.Remaining != 2 {
		t.Fatalf("first with cost=3 should allow remaining=2: %+v", d)
	}

	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("second with cost=3 should deny (only 2 tokens left): %+v", d)
	}
}

func TestTokenBucket_DefaultCapacityAndRefill(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	// All zeros: capacity defaults to limit=0 -> 1, refill defaults to 1.
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoTokenBucket, Limit: 0, Burst: 0, RefillRate: 0, Window: "bad"},
		Action:    model.ActionDeny,
	}
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed {
		t.Fatalf("first with default capacity should allow: %+v", d)
	}
	d, _ = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if d.Allowed {
		t.Fatalf("second with default capacity should deny: %+v", d)
	}
}

func TestTokenBucket_ShadowMode(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 1, Burst: 1, RefillRate: 1},
		Action:    model.ActionShadowOnly,
	}
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	if !d.Allowed || !d.ShadowMode {
		t.Fatalf("shadow mode should still allow: %+v", d)
	}
}

func TestTokenBucket_CASError(t *testing.T) {
	e := New(tbCASErrStore{})
	p := tbPolicy(1, 1, 1)
	if _, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected CAS error")
	}
}

func TestTokenBucket_GetError(t *testing.T) {
	e := New(tbGetErrStore{})
	p := tbPolicy(1, 1, 1)
	if _, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected get error")
	}
}

func TestTokenBucket_KeyIsolation(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }

	p := tbPolicy(1, 1, 1)
	d1, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key1")
	d2, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key2")
	if !d1.Allowed || !d2.Allowed {
		t.Fatalf("different keys should be independent")
	}
}
