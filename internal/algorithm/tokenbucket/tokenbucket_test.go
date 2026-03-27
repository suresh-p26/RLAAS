package tokenbucket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := tbPolicy(1, 3, 1)

	for i := 0; i < 3; i++ {
		d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
		require.NoError(t, err)
		assert.True(t, d.Allowed, "request %d should be allowed within burst", i+1)
	}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "should deny after burst")
	assert.Positive(t, d.RetryAfter, "RetryAfter should be positive")
}

func TestTokenBucket_RefillOverTime(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := tbPolicy(1, 2, 1) // refill=1/s

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "should be depleted")

	now = now.Add(time.Second)
	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "after 1s refill should allow")
}

func TestTokenBucket_RefillCapsAtCapacity(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := tbPolicy(1, 2, 10) // fast refill

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")

	now = now.Add(10 * time.Second)
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "should allow after refill")
	assert.LessOrEqual(t, d.Remaining, int64(2), "remaining should be capped at capacity")
}

func TestTokenBucket_CustomCost(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 5, Burst: 5, RefillRate: 1, CostPerRequest: 3},
		Action:    model.ActionDeny,
	}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, int64(2), d.Remaining, "first with cost=3 should leave remaining=2")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "second with cost=3 should deny (only 2 tokens left)")
}

func TestTokenBucket_DefaultCapacityAndRefill(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoTokenBucket, Limit: 0, Burst: 0, RefillRate: 0, Window: "bad"},
		Action:    model.ActionDeny,
	}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first with default capacity should allow")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "second with default capacity should deny")
}

func TestTokenBucket_ShadowMode(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 1, Burst: 1, RefillRate: 1},
		Action:    model.ActionShadowOnly,
	}

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "shadow mode should still allow")
	assert.True(t, d.ShadowMode)
}

func TestTokenBucket_ErrorPaths(t *testing.T) {
	tests := []struct {
		name  string
		store store.CounterStore
	}{
		{"cas error", tbCASErrStore{}},
		{"get error", tbGetErrStore{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(tt.store)
			p := tbPolicy(1, 1, 1)
			_, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
			require.Error(t, err)
		})
	}
}

func TestTokenBucket_KeyIsolation(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := tbPolicy(1, 1, 1)

	d1, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "key1")
	require.NoError(t, err)
	d2, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "key2")
	require.NoError(t, err)
	assert.True(t, d1.Allowed, "key1 should be independent")
	assert.True(t, d2.Allowed, "key2 should be independent")
}
