package slidingcounter

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

type evalExpectation struct {
	allowed       bool
	hasRemaining  bool
	remaining     int64
	shadowMode    bool
	hasReason     bool
	reason        string
	retryPositive bool
}

type evalStep struct {
	req  model.RequestContext
	want evalExpectation
}

func TestSlidingCounter_TableDrivenScenarios(t *testing.T) {
	tests := []struct {
		name   string
		policy model.Policy
		key    string
		steps  []evalStep
	}{
		{
			name:   "allow then deny",
			policy: swcPolicy(2, "1m"),
			key:    "k",
			steps: []evalStep{
				{want: evalExpectation{allowed: true, hasRemaining: true, remaining: 1}},
				{want: evalExpectation{allowed: true, hasRemaining: true, remaining: 0}},
				{want: evalExpectation{allowed: false}},
			},
		},
		{
			name: "custom cost",
			policy: model.Policy{
				Algorithm: model.AlgorithmConfig{Limit: 5, Window: "1m", CostPerRequest: 3},
				Action:    model.ActionDeny,
			},
			key: "k",
			steps: []evalStep{
				{want: evalExpectation{allowed: true, hasRemaining: true, remaining: 2}},
				{want: evalExpectation{allowed: false}},
			},
		},
		{
			name:   "default limit fallback",
			policy: swcPolicy(0, "1m"),
			key:    "k",
			steps: []evalStep{
				{want: evalExpectation{allowed: true}},
				{want: evalExpectation{allowed: false}},
			},
		},
		{
			name: "shadow mode allows on limit exceed",
			policy: model.Policy{
				Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"},
				Action:    model.ActionShadowOnly,
			},
			key: "k",
			steps: []evalStep{
				{want: evalExpectation{allowed: true}},
				{want: evalExpectation{allowed: true, shadowMode: true}},
			},
		},
		{
			name:   "retry after set on deny",
			policy: swcPolicy(1, "1m"),
			key:    "k",
			steps: []evalStep{
				{want: evalExpectation{allowed: true}},
				{want: evalExpectation{allowed: false, retryPositive: true}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(memory.New())
			now := time.Unix(1000, 0)
			e.Now = func() time.Time { return now }
			for _, step := range tt.steps {
				got, err := e.Evaluate(context.Background(), tt.policy, step.req, tt.key)
				require.NoError(t, err)
				assert.Equal(t, step.want.allowed, got.Allowed)
				if step.want.hasRemaining {
					assert.Equal(t, step.want.remaining, got.Remaining)
				}
				assert.Equal(t, step.want.shadowMode, got.ShadowMode)
				if step.want.hasReason {
					assert.Equal(t, step.want.reason, got.Reason)
				}
				if step.want.retryPositive {
					assert.Positive(t, got.RetryAfter)
				}
			}
		})
	}
}

func TestSlidingCounter_WeightedPreviousWindow(t *testing.T) {
	e := New(memory.New())
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
		require.NoError(t, err)
		assert.True(t, d.Allowed, "request should be allowed (weight=0.5)")
	}

	// Now estimated = 6 + 8*0.5 = 10, next should deny.
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	assert.False(t, d.Allowed, "should deny when estimated reaches limit")
}

func TestSlidingCounter_WindowRollover(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(60, 0)
	e.Now = func() time.Time { return now }
	p := swcPolicy(2, "1m")

	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	e.Evaluate(context.Background(), p, model.RequestContext{}, "k")

	// Full window rollover: advance 2 full minutes so prev window has no data.
	now = time.Unix(180, 0)
	d, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	assert.True(t, d.Allowed, "after full rollover should allow")
}

func TestSlidingCounter_ErrorPath(t *testing.T) {
	e := New(swcErrStore{})
	_, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"}}, model.RequestContext{}, "k")
	require.Error(t, err)
}

func TestSlidingCounter_KeyIsolation(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(60, 0)
	e.Now = func() time.Time { return now }

	p := swcPolicy(1, "1m")
	d1, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key1")
	d2, _ := e.Evaluate(context.Background(), p, model.RequestContext{}, "key2")
	assert.True(t, d1.Allowed, "key1 should be independent")
	assert.True(t, d2.Allowed, "key2 should be independent")
}

func TestSlidingCounter_ContentionExhaustsRetries(t *testing.T) {
	e := New(swcCASAlwaysFalseStore{})
	now := time.Unix(60, 0)
	e.Now = func() time.Time { return now }

	p := swcPolicy(10, "1m")
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "contention exhaustion should deny")
	assert.Equal(t, "sliding_window_contention", d.Reason)
}
