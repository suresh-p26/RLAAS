package leakybucket

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
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(2, "1s", 1.0)
	req := model.RequestContext{}

	d, err := e.Evaluate(context.Background(), p, req, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first should allow")
	assert.Equal(t, int64(1), d.Remaining)

	d, err = e.Evaluate(context.Background(), p, req, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "second should allow")

	d, err = e.Evaluate(context.Background(), p, req, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "third should deny")
	assert.Positive(t, d.RetryAfter, "RetryAfter should be positive")
}

func TestLeakyBucket_TimeBasedLeak(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(2, "1s", 2.0)
	req := model.RequestContext{}

	e.Evaluate(context.Background(), p, req, "k")
	e.Evaluate(context.Background(), p, req, "k")

	d, err := e.Evaluate(context.Background(), p, req, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "should deny when full at same time")

	now = now.Add(500 * time.Millisecond)
	d, err = e.Evaluate(context.Background(), p, req, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "after 0.5s leak should allow")

	now = now.Add(time.Second)
	d, err = e.Evaluate(context.Background(), p, req, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "after full leak should allow")
}

func TestLeakyBucket_CustomCost(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 5, Window: "1s", LeakRate: 1.0, CostPerRequest: 3},
		Action:    model.ActionDeny,
	}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, int64(2), d.Remaining, "first should allow with remaining 2")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "second should deny (overflow)")
}

func TestLeakyBucket_Quantity(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(5, "1s", 1.0)

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{Quantity: 4}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "should allow cost=4")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{Quantity: 4}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "should deny")
}

func TestLeakyBucket_DefaultLeakRate(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(1, "1s", 0) // defaults to 1

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "should allow with default leak rate")
}

func TestLeakyBucket_DefaultLimit(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(0, "1s", 1)

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first should allow with default limit")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "second should deny with default limit")
}

func TestLeakyBucket_ShadowMode(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1s", LeakRate: 1.0},
		Action:    model.ActionShadowOnly,
	}

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "shadow mode should still allow")
	assert.True(t, d.ShadowMode)
}

func TestLeakyBucket_ErrorPaths(t *testing.T) {
	tests := []struct {
		name  string
		store store.CounterStore
	}{
		{"set error", &lbSetErrStore{}},
		{"get error", &lbGetErrStore{}},
		{"cas error", &lbCASErrStore{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(tt.store)
			now := time.Unix(1000, 0)
			e.Now = func() time.Time { return now }
			p := lbPolicy(10, "1s", 1.0)
			_, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
			require.Error(t, err)
		})
	}
}

func TestLeakyBucket_ContentionExhaustsRetries(t *testing.T) {
	e := New(&lbCASAlwaysFalseStore{})
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(10, "1s", 1.0)

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "contention exhaustion should deny")
	assert.Equal(t, "leaky_bucket_contention", d.Reason)
}

func TestLeakyBucket_KeyIsolation(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := lbPolicy(1, "1s", 1.0)

	d1, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "key1")
	require.NoError(t, err)
	d2, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "key2")
	require.NoError(t, err)
	assert.True(t, d1.Allowed, "key1 should be independent")
	assert.True(t, d2.Allowed, "key2 should be independent")
}
