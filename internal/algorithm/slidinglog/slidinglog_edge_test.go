package slidinglog

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestEvaluator_EdgeCases(t *testing.T) {
	e := New(memory.New())
	policy := model.Policy{
		PolicyID: "p1",
		Name:     "test",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  0,
			Window: "1s",
		},
	}

	decision, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	require.NoError(t, err)
	assert.True(t, decision.Allowed, "zero limit normalized to 1 should allow first request")

	decision2, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	require.NoError(t, err)
	assert.False(t, decision2.Allowed, "second request with limit 1 should be denied")
}

func TestEvaluator_LargeWindow(t *testing.T) {
	e := New(memory.New())
	policy := model.Policy{
		PolicyID: "p1",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  10,
			Window: "24h",
		},
	}

	d1, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	require.NoError(t, err)
	assert.True(t, d1.Allowed, "first request should be allowed")

	for i := 0; i < 9; i++ {
		d, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
		require.NoError(t, err)
		assert.True(t, d.Allowed, "request %d should be allowed", i+2)
	}

	d11, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	require.NoError(t, err)
	assert.False(t, d11.Allowed, "11th request should be denied")
}

func TestEvaluator_ExactLimit(t *testing.T) {
	e := New(memory.New())
	policy := model.Policy{
		PolicyID: "p1",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  3,
			Window: "1s",
		},
	}

	for i := 0; i < 3; i++ {
		d, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")
		require.NoError(t, err)
		assert.True(t, d.Allowed, "request %d should be allowed", i+1)
	}

	d4, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")
	require.NoError(t, err)
	assert.False(t, d4.Allowed, "4th request should be denied")
}

func TestEvaluator_WindowExpiry(t *testing.T) {
	e := New(memory.New())
	policy := model.Policy{
		PolicyID: "p1",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  2,
			Window: "100ms",
		},
	}

	e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")
	e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")

	d3, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")
	require.NoError(t, err)
	assert.False(t, d3.Allowed, "3rd request should be blocked initially")

	time.Sleep(150 * time.Millisecond)

	d4, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")
	require.NoError(t, err)
	assert.True(t, d4.Allowed, "after window expiry, should be allowed")
}

func TestEvaluator_Quantity(t *testing.T) {
	e := New(memory.New())
	policy := model.Policy{
		PolicyID: "p1",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  10,
			Window: "1s",
		},
	}

	d1, err := e.Evaluate(context.Background(), policy, model.RequestContext{Quantity: 5}, "key")
	require.NoError(t, err)
	assert.True(t, d1.Allowed, "request with quantity 5 should be allowed")

	d2, err := e.Evaluate(context.Background(), policy, model.RequestContext{Quantity: 6}, "key")
	require.NoError(t, err)
	assert.False(t, d2.Allowed, "request exceeding limit should be denied")
}

func TestEvaluator_MultipleKeys(t *testing.T) {
	e := New(memory.New())
	policy := model.Policy{
		PolicyID: "p1",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  2,
			Window: "1s",
		},
	}

	e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	d3, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	require.NoError(t, err)
	assert.False(t, d3.Allowed, "key1 3rd request should be denied")

	d1, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key2")
	require.NoError(t, err)
	assert.True(t, d1.Allowed, "key2 should have independent limit")
}
