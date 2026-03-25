package leakybucket

import (
	"context"
	"math"
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

// Evaluator smooths bursts by draining accumulated load over time.
// It tracks the current water level and the last-evaluated timestamp so that
// leak is computed proportionally to elapsed real time.
// Uses CompareAndSwap retry loop for safe concurrent access.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a leaky bucket evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate applies time-proportional leak, adds request cost, and checks
// whether the bucket overflows. Uses a CAS retry loop on the level key to
// prevent concurrent over-admission.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error) {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}

	levelKey := key + ":lb:level"
	tsKey := key + ":lb:ts"

	limit := policy.Algorithm.Limit
	if limit <= 0 {
		limit = 1
	}

	leakRate := policy.Algorithm.LeakRate
	if leakRate <= 0 {
		leakRate = 1
	}

	window := common.WindowDuration(policy.Algorithm)
	ttl := 2 * window
	if ttl < 2*time.Hour {
		ttl = 2 * time.Hour
	}

	cost := common.Cost(req, policy.Algorithm)

	retries := maxRetries(policy.Algorithm)
	for attempt := 0; attempt < retries; attempt++ {
		oldLevelMilli, err := e.Counter.Get(ctx, levelKey)
		if err != nil {
			return model.Decision{}, err
		}
		lastNanos, err := e.Counter.Get(ctx, tsKey)
		if err != nil {
			return model.Decision{}, err
		}

		curLevel := float64(oldLevelMilli) / 1000.0

		// On first request, initialize empty bucket.
		if lastNanos == 0 {
			lastNanos = now.UnixNano()
		}

		// Compute time-proportional leak.
		elapsed := float64(now.UnixNano()-lastNanos) / float64(time.Second)
		if elapsed < 0 {
			elapsed = 0 // guard against clock skew
		}
		leaked := elapsed * leakRate
		curLevel = math.Max(0, curLevel-leaked)

		// Add request cost.
		curLevel += float64(cost)

		// Check overflow.
		if curLevel > float64(limit) {
			overflow := curLevel - float64(limit)
			retry := time.Duration((overflow / leakRate) * float64(time.Second))
			return common.OverLimitDecision(policy, retry, 0, "leaky_bucket_overflow"), nil
		}

		// CAS: only commit if level hasn't changed since we read it.
		newLevelMilli := int64(math.Round(curLevel * 1000))
		swapped, err := e.Counter.CompareAndSwap(ctx, levelKey, oldLevelMilli, newLevelMilli, ttl)
		if err != nil {
			return model.Decision{}, err
		}
		if !swapped {
			continue // Another goroutine modified level; retry.
		}

		// Update timestamp.
		if err := e.Counter.Set(ctx, tsKey, now.UnixNano(), ttl); err != nil {
			return model.Decision{}, err
		}

		remaining := int64(float64(limit) - curLevel)
		return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "within_leaky_bucket", Remaining: remaining}, nil
	}

	// Exhausted retries — treat as temporary contention, deny with short retry.
	return common.OverLimitDecision(policy, 10*time.Millisecond, 0, "leaky_bucket_contention"), nil
}
