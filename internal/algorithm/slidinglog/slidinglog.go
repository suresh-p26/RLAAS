package slidinglog

import (
	"context"
	"time"

	"github.com/rlaas-io/rlaas/internal/algorithm/common"
	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
)

// atomicTimestampStore is an optional interface that provides atomic
// check-and-add for timestamp-based rate limiting. Store implementations
// that support it (e.g., memory, Redis with Lua) avoid TOCTOU races between
// counting existing entries and adding new ones.
type atomicTimestampStore interface {
	CheckAndAddTimestamps(ctx context.Context, key string, cutoff time.Time, limit, cost int64, ts time.Time, ttl time.Duration) (count int64, allowed bool, err error)
}

// Evaluator enforces exact rolling limits using timestamp logs.
// Each request adds Cost entries to the log; denied requests do not pollute it.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a sliding log evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate checks whether adding the current request would exceed the limit
// within the rolling window. Only records timestamps for allowed requests.
// When the underlying store supports atomic check-and-add, uses it to prevent
// TOCTOU race conditions under concurrent access.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error) {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}

	window := common.WindowDuration(policy.Algorithm)
	logKey := key + ":swl"

	limit := policy.Algorithm.Limit
	if limit <= 0 {
		limit = 1
	}

	cost := common.Cost(req, policy.Algorithm)
	cutoff := now.Add(-window)

	// Prefer atomic path when the store supports it — avoids TOCTOU races.
	if ats, ok := e.Counter.(atomicTimestampStore); ok {
		count, allowed, err := ats.CheckAndAddTimestamps(ctx, logKey, cutoff, limit, cost, now, window)
		if err != nil {
			return model.Decision{}, err
		}
		if !allowed {
			retryAfter := computeRetryAfter(window, count, cost, limit)
			return common.OverLimitDecision(policy, retryAfter, limit-count, "sliding_log_limit_exceeded"), nil
		}
		return model.Decision{
			Allowed:   true,
			Action:    model.ActionAllow,
			Reason:    "within_sliding_log",
			Remaining: limit - (count + cost),
			ResetAt:   now.Add(window),
		}, nil
	}

	// Fallback: non-atomic path for stores without atomic support.
	if err := e.Counter.TrimBefore(ctx, logKey, cutoff); err != nil {
		return model.Decision{}, err
	}

	count, err := e.Counter.CountAfter(ctx, logKey, cutoff)
	if err != nil {
		return model.Decision{}, err
	}

	if count+cost > limit {
		retryAfter := computeRetryAfter(window, count, cost, limit)
		return common.OverLimitDecision(policy, retryAfter, limit-count, "sliding_log_limit_exceeded"), nil
	}

	for i := int64(0); i < cost; i++ {
		if err := e.Counter.AddTimestamp(ctx, logKey, now, window); err != nil {
			return model.Decision{}, err
		}
	}

	return model.Decision{
		Allowed:   true,
		Action:    model.ActionAllow,
		Reason:    "within_sliding_log",
		Remaining: limit - (count + cost),
		ResetAt:   now.Add(window),
	}, nil
}

// computeRetryAfter estimates when enough entries expire to allow the next request.
func computeRetryAfter(window time.Duration, count, cost, limit int64) time.Duration {
	if count <= 0 {
		return window
	}
	retryAfter := time.Duration(float64(window) * float64(count+cost-limit) / float64(count))
	if retryAfter <= 0 {
		retryAfter = time.Second
	}
	return retryAfter
}
