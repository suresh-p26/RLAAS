package concurrency

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

type errLeaseStore struct{ store.CounterStore }

func (errLeaseStore) AcquireLease(context.Context, string, int64, time.Duration) (bool, int64, error) {
	return false, 0, errors.New("boom")
}

func cPolicy(maxConc int64) model.Policy {
	return model.Policy{Algorithm: model.AlgorithmConfig{MaxConcurrency: maxConc}, Action: model.ActionDeny}
}

func TestConcurrency_AllowThenDeny(t *testing.T) {
	e := New(memory.New())
	p := cPolicy(2)

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first should allow")
	assert.Equal(t, int64(1), d.Remaining)

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "second should allow")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "third should deny")
}

func TestConcurrency_Release(t *testing.T) {
	e := New(memory.New())
	p := cPolicy(1)

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first should allow")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "second should deny when slot taken")

	err = e.Release(context.Background(), "k")
	require.NoError(t, err, "release should not error")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "after release should allow")
}

func TestConcurrency_CustomLeaseTTL(t *testing.T) {
	e := New(memory.New())
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{MaxConcurrency: 1, LeaseTTL: 300},
		Action:    model.ActionDeny,
	}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "should allow")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "should deny (slot used with custom TTL)")
}

func TestConcurrency_DefaultLimitFallback(t *testing.T) {
	e := New(memory.New())
	p := model.Policy{Algorithm: model.AlgorithmConfig{}, Action: model.ActionDeny}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first with default limit should allow")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "second with default limit should deny")
}

func TestConcurrency_ShadowMode(t *testing.T) {
	e := New(memory.New())
	p := model.Policy{
		Algorithm: model.AlgorithmConfig{MaxConcurrency: 1},
		Action:    model.ActionShadowOnly,
	}

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "shadow mode should still allow")
	assert.True(t, d.ShadowMode)
}

func TestConcurrency_ErrorPath(t *testing.T) {
	e := New(errLeaseStore{})
	_, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1}}, model.RequestContext{}, "k")
	require.Error(t, err)
}

func TestConcurrency_KeyIsolation(t *testing.T) {
	e := New(memory.New())
	p := cPolicy(1)

	d1, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "key1")
	require.NoError(t, err)
	d2, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "key2")
	require.NoError(t, err)
	assert.True(t, d1.Allowed, "key1 should be independent")
	assert.True(t, d2.Allowed, "key2 should be independent")
}
