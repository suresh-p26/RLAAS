package slidinglog

import (
	"context"
	"github.com/suresh-p26/RLAAS/internal/algorithm/common"
	"github.com/suresh-p26/RLAAS/internal/store"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"time"
)

// Evaluator enforces exact rolling limits using timestamp logs.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a sliding log evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate appends the current timestamp and counts recent events in window.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, _ model.RequestContext, key string) (model.Decision, error) {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	window := common.WindowDuration(policy.Algorithm)
	logKey := key + ":swl"
	if err := e.Counter.AddTimestamp(ctx, logKey, now, window); err != nil {
		return model.Decision{}, err
	}
	_ = e.Counter.TrimBefore(ctx, logKey, now.Add(-window))
	count, err := e.Counter.CountAfter(ctx, logKey, now.Add(-window))
	if err != nil {
		return model.Decision{}, err
	}
	limit := policy.Algorithm.Limit
	if limit <= 0 {
		limit = 1
	}
	if count <= limit {
		return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "within_sliding_log", Remaining: limit - count, ResetAt: now.Add(window)}, nil
	}
	return common.OverLimitDecision(policy, 100*time.Millisecond, 0, "sliding_log_limit_exceeded"), nil
}
