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

// Evaluator smooths bursts by draining accumulated load at a constant leak rate.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a leaky bucket evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate applies time-proportional leak, adds request cost, and checks overflow.
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
		if lastNanos == 0 {
			lastNanos = now.UnixNano()
		}

		elapsed := float64(now.UnixNano()-lastNanos) / float64(time.Second)
		if elapsed < 0 {
			elapsed = 0 // guard against clock skew
		}
		curLevel = math.Max(0, curLevel-(elapsed*leakRate))
		curLevel += float64(cost)

		if curLevel > float64(limit) {
			overflow := curLevel - float64(limit)
			retry := time.Duration((overflow / leakRate) * float64(time.Second))
			return common.OverLimitDecision(policy, retry, 0, "leaky_bucket_overflow"), nil
		}

		newLevelMilli := int64(math.Round(curLevel * 1000))
		swapped, err := e.Counter.CompareAndSwap(ctx, levelKey, oldLevelMilli, newLevelMilli, ttl)
		if err != nil {
			return model.Decision{}, err
		}
		if !swapped {
			continue
		}

		// Timestamp update is best-effort: the CAS already committed the level change.
		// A failed Set leaves a stale refill base (slightly generous next leak).
		for i := 0; i < 3; i++ {
			if e.Counter.Set(ctx, tsKey, now.UnixNano(), ttl) == nil {
				break
			}
		}

		remaining := int64(float64(limit) - curLevel)
		return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "within_leaky_bucket", Remaining: remaining}, nil
	}

	return common.OverLimitDecision(policy, 10*time.Millisecond, 0, "leaky_bucket_contention"), nil
}
