package slidinglog

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
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := slPolicy(2, "1m")

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, int64(1), d.Remaining)

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, int64(0), d.Remaining)

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed)
}

func TestSlidingLog_NoPollutionOnDeny(t *testing.T) {
	s := memory.New()
	e := New(s)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := slPolicy(1, "1m")

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")

	count, err := s.CountAfter(context.Background(), "k:swl", now.Add(-time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "denied request should not pollute log")
}

func TestSlidingLog_WindowExpiry(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := slPolicy(1, "1m")

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")

	now = now.Add(61 * time.Second)
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "after window expiry should allow")
}

func TestSlidingLog_CostPerRequest(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 5, Window: "1m", CostPerRequest: 3},
		Action:    model.ActionDeny,
	}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, int64(2), d.Remaining, "first with cost=3 should have remaining=2")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "second with cost=3 should deny (6 > 5)")
}

func TestSlidingLog_Quantity(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := slPolicy(5, "1m")

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{Quantity: 4}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, int64(1), d.Remaining, "quantity=4 should leave remaining=1")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{Quantity: 4}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "quantity=4 should deny (8 > 5)")
}

func TestSlidingLog_RetryAfter(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := slPolicy(1, "1m")

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.Positive(t, d.RetryAfter, "RetryAfter should be positive on deny")
}

func TestSlidingLog_DefaultLimit(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := slPolicy(0, "1m") // limit=0 defaults to 1

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first should allow with default limit")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "second should deny with default limit")
}

func TestSlidingLog_ShadowMode(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"},
		Action:    model.ActionShadowOnly,
	}

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "shadow mode should still allow")
	assert.True(t, d.ShadowMode)
}

func TestSlidingLog_AddTimestampError(t *testing.T) {
	e := New(swlAddErrStore{})
	p := slPolicy(10, "1m")
	_, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.Error(t, err)
}

func TestSlidingLog_KeyIsolation(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := slPolicy(1, "1m")

	d1, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "key1")
	require.NoError(t, err)
	d2, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "key2")
	require.NoError(t, err)
	assert.True(t, d1.Allowed, "key1 should be independent")
	assert.True(t, d2.Allowed, "key2 should be independent")
}
