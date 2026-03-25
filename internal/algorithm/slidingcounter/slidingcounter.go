package slidingcounter

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

// Evaluator implements the standard sliding window counter approximation.
// It uses two full-window-sized periods: the current window and the previous
// window, then estimates traffic as:
//
//	estimated = prev_count * (1 - elapsed/window) + cur_count
//
// Only allowed requests are counted, preventing denied-request counter
// inflation that would incorrectly restrict future windows.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a sliding window counter evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate reads current and previous window counters, checks the weighted
// estimate, and only increments via CAS when the request is allowed.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error) {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}

	window := common.WindowDuration(policy.Algorithm)
	if window <= 0 {
		window = time.Minute
	}

	cost := common.Cost(req, policy.Algorithm)

	// Current and previous full-window boundaries.
	curStart := common.WindowStart(now, policy.Algorithm)
	prevStart := curStart.Add(-window)

	curKey := fmt.Sprintf("%s:swc:%d", key, curStart.UnixNano())
	prevKey := fmt.Sprintf("%s:swc:%d", key, prevStart.UnixNano())

	limit := float64(policy.Algorithm.Limit)
	if limit <= 0 {
		limit = 1
	}

	// Weight: how much of the previous window is still "inside" the sliding view.
	elapsed := now.Sub(curStart)
	weight := float64(window-elapsed) / float64(window)
	if weight < 0 {
		weight = 0
	}

	// Read previous window count (read-only, stable value).
	prevCount, err := e.Counter.Get(ctx, prevKey)
	if err != nil {
		return model.Decision{}, err
	}

	retries := maxRetries(policy.Algorithm)
	for attempt := 0; attempt < retries; attempt++ {
		curCount, err := e.Counter.Get(ctx, curKey)
		if err != nil {
			return model.Decision{}, err
		}

		// Estimate with proposed cost included.
		estimated := float64(curCount+cost) + (float64(prevCount) * weight)

		if estimated > limit {
			retryAfter := curStart.Add(window).Sub(now)
			return common.OverLimitDecision(policy, retryAfter, 0, "sliding_window_limit_exceeded"), nil
		}

		// Only increment if allowed — prevents denied-request inflation.
		swapped, err := e.Counter.CompareAndSwap(ctx, curKey, curCount, curCount+cost, 2*window)
		if err != nil {
			return model.Decision{}, err
		}
		if swapped {
			remaining := int64(limit - estimated)
			return model.Decision{
				Allowed:   true,
				Action:    model.ActionAllow,
				Reason:    "within_sliding_window",
				Remaining: remaining,
				ResetAt:   curStart.Add(window),
			}, nil
		}
		// CAS failed — another goroutine incremented; retry.
	}

	// Exhausted retries — treat as temporary contention.
	retryAfter := curStart.Add(window).Sub(now)
	return common.OverLimitDecision(policy, retryAfter, 0, "sliding_window_contention"), nil
}
