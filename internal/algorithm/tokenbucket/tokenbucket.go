package tokenbucket

import (
	"context"
	"math"
	"time"

	"github.com/rlaas-io/rlaas/internal/algorithm/common"
	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
)

const defaultMaxCASRetries = 5

// Evaluator applies token bucket rate limiting with continuous refill over time.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

func maxRetries(cfg model.AlgorithmConfig) int {
	if cfg.MaxRetries > 0 {
		return cfg.MaxRetries
	}
	return defaultMaxCASRetries
}

// New creates a token bucket evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate refills tokens based on elapsed time and consumes request cost.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error) {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}

	capacity := policy.Algorithm.Burst
	if capacity <= 0 {
		capacity = policy.Algorithm.Limit
	}
	if capacity <= 0 {
		capacity = 1
	}

	refillRate := policy.Algorithm.RefillRate
	if refillRate <= 0 {
		window := common.WindowDuration(policy.Algorithm)
		refillRate = float64(policy.Algorithm.Limit) / window.Seconds()
		if refillRate <= 0 {
			refillRate = 1
		}
	}

	tokensKey := key + ":tb:tokens"
	tsKey := key + ":tb:ts"
	cost := float64(common.Cost(req, policy.Algorithm))
	ttl := 2 * time.Hour

	retries := maxRetries(policy.Algorithm)
	for attempt := 0; attempt < retries; attempt++ {
		oldTokenMilli, err := e.Counter.Get(ctx, tokensKey)
		if err != nil {
			return model.Decision{}, err
		}
		lastNanos, err := e.Counter.Get(ctx, tsKey)
		if err != nil {
			return model.Decision{}, err
		}

		curTokens := float64(oldTokenMilli) / 1000.0
		if lastNanos == 0 {
			curTokens = float64(capacity)
			lastNanos = now.UnixNano()
		}

		elapsed := float64(now.UnixNano()-lastNanos) / float64(time.Second)
		if elapsed < 0 {
			elapsed = 0 // guard against clock skew
		}
		curTokens = math.Min(float64(capacity), curTokens+(elapsed*refillRate))

		if curTokens < cost {
			missing := cost - curTokens
			retry := time.Duration((missing / refillRate) * float64(time.Second))
			return common.OverLimitDecision(policy, retry, int64(curTokens), "token_bucket_depleted"), nil
		}

		newTokens := curTokens - cost
		newTokenMilli := int64(math.Round(newTokens * 1000))

		swapped, err := e.Counter.CompareAndSwap(ctx, tokensKey, oldTokenMilli, newTokenMilli, ttl)
		if err != nil {
			return model.Decision{}, err
		}
		if !swapped {
			continue
		}

		// Timestamp update is best-effort: the CAS already committed the deduction.
		// A failed Set leaves a stale refill base (slightly generous next refill).
		for i := 0; i < 3; i++ {
			if e.Counter.Set(ctx, tsKey, now.UnixNano(), ttl) == nil {
				break
			}
		}

		return model.Decision{
			Allowed:   true,
			Action:    model.ActionAllow,
			Reason:    "token_available",
			Remaining: int64(newTokens),
		}, nil
	}

	return common.OverLimitDecision(policy, 10*time.Millisecond, 0, "token_bucket_contention"), nil
}
