package fixedwindow

import (
	"context"
	"fmt"
	"time"

	"github.com/rlaas-io/rlaas/internal/algorithm/common"
	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
)

const defaultMaxCASRetries = 5

func maxRetries(cfg model.AlgorithmConfig) int {
	if cfg.MaxRetries > 0 {
		return cfg.MaxRetries
	}
	return defaultMaxCASRetries
}

// Evaluator applies fixed window rate limiting.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a fixed window evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate checks the current window counter via CAS and only increments
// when the request is allowed, preventing denied-request counter inflation.
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
	windowStart := common.WindowStart(now, policy.Algorithm)
	windowEnd := common.WindowEnd(now, policy.Algorithm)
	windowKey := fmt.Sprintf("%s:%d", key, windowStart.UnixNano())
	cost := common.Cost(req, policy.Algorithm)

	retries := maxRetries(policy.Algorithm)
	for attempt := 0; attempt < retries; attempt++ {
		curCount, err := e.Counter.Get(ctx, windowKey)
		if err != nil {
			return model.Decision{}, err
		}

		if curCount+cost > limit {
			return common.OverLimitDecision(policy, windowEnd.Sub(now), 0, "fixed_window_limit_exceeded"), nil
		}

		swapped, err := e.Counter.CompareAndSwap(ctx, windowKey, curCount, curCount+cost, window)
		if err != nil {
			return model.Decision{}, err
		}
		if swapped {
			remaining := limit - (curCount + cost)
			return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "within_fixed_window", Remaining: remaining, ResetAt: windowEnd}, nil
		}
		// CAS failed — another goroutine incremented; retry.
	}

	// Exhausted retries — treat as temporary contention.
	return common.OverLimitDecision(policy, windowEnd.Sub(now), 0, "fixed_window_contention"), nil
}
