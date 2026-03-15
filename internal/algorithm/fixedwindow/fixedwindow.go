package fixedwindow

import (
	"context"
	"fmt"
	"rlaas/internal/algorithm/common"
	"rlaas/pkg/model"
	"rlaas/internal/store"
	"time"
)

// Evaluator applies fixed window rate limiting.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a fixed window evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate increments the current window counter and checks the limit.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error) {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	window := common.WindowDuration(policy.Algorithm)
	limit := policy.Algorithm.Limit
	if limit <= 0 {
		limit = 1
	}
	windowStart := now.Truncate(window)
	windowKey := fmt.Sprintf("%s:%d", key, windowStart.Unix())
	count, err := e.Counter.Increment(ctx, windowKey, common.Cost(req, policy.Algorithm), window)
	if err != nil {
		return model.Decision{}, err
	}
	remaining := limit - count
	if remaining >= 0 {
		return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "within_fixed_window", Remaining: remaining, ResetAt: windowStart.Add(window)}, nil
	}
	return common.OverLimitDecision(policy, windowStart.Add(window).Sub(now), 0, "fixed_window_limit_exceeded"), nil
}
