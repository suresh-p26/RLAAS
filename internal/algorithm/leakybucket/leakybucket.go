package leakybucket

import (
	"context"
	"github.com/rlaas-io/rlaas/internal/algorithm/common"
	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
	"time"
)

// Evaluator smooths bursts by draining accumulated load over time.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a leaky bucket evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate applies leak rate and then checks if current level exceeds limit.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error) {
	stateKey := key + ":lb"
	current, err := e.Counter.Get(ctx, stateKey)
	if err != nil {
		return model.Decision{}, err
	}
	leakRate := policy.Algorithm.LeakRate
	if leakRate <= 0 {
		leakRate = 1
	}
	window := common.WindowDuration(policy.Algorithm)
	leaked := int64((window.Seconds()) * leakRate)
	if leaked > 0 {
		current -= leaked
		if current < 0 {
			current = 0
		}
	}
	current += common.Cost(req, policy.Algorithm)
	limit := policy.Algorithm.Limit
	if limit <= 0 {
		limit = 1
	}
	if err := e.Counter.Set(ctx, stateKey, current, 2*window); err != nil {
		return model.Decision{}, err
	}
	if current <= limit {
		return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "within_leaky_bucket", Remaining: limit - current}, nil
	}
	return common.OverLimitDecision(policy, time.Second, 0, "leaky_bucket_overflow"), nil
}
