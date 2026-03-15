package tokenbucket

import (
	"context"
	"github.com/suresh-p26/RLAAS/internal/algorithm/common"
	"github.com/suresh-p26/RLAAS/internal/store"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"math"
	"time"
)

// Evaluator applies token bucket rate limiting with refill over time.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
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
	curTokenMilli, _ := e.Counter.Get(ctx, tokensKey)
	lastNanos, _ := e.Counter.Get(ctx, tsKey)
	curTokens := float64(curTokenMilli) / 1000
	if lastNanos == 0 {
		curTokens = float64(capacity)
		lastNanos = now.UnixNano()
	}
	elapsed := float64(now.UnixNano()-lastNanos) / float64(time.Second)
	curTokens = math.Min(float64(capacity), curTokens+(elapsed*refillRate))
	cost := float64(common.Cost(req, policy.Algorithm))
	if curTokens < cost {
		missing := cost - curTokens
		retry := time.Duration((missing / refillRate) * float64(time.Second))
		return common.OverLimitDecision(policy, retry, int64(curTokens), "token_bucket_depleted"), nil
	}
	curTokens -= cost
	if err := e.Counter.Set(ctx, tokensKey, int64(curTokens*1000), 2*time.Hour); err != nil {
		return model.Decision{}, err
	}
	if err := e.Counter.Set(ctx, tsKey, now.UnixNano(), 2*time.Hour); err != nil {
		return model.Decision{}, err
	}
	return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "token_available", Remaining: int64(curTokens)}, nil
}
