package concurrency

import (
	"context"
	"github.com/rlaas-io/rlaas/internal/algorithm/common"
	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
	"time"
)

// Evaluator limits the number of concurrent in flight operations.
type Evaluator struct {
	Counter store.CounterStore
}

// New creates a concurrency evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate tries to acquire one lease and denies when the limit is reached.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, _ model.RequestContext, key string) (model.Decision, error) {
	limit := policy.Algorithm.MaxConcurrency
	if limit <= 0 {
		limit = policy.Algorithm.Limit
	}
	if limit <= 0 {
		limit = 1
	}
	ok, current, err := e.Counter.AcquireLease(ctx, key+":concurrency", limit, 2*time.Minute)
	if err != nil {
		return model.Decision{}, err
	}
	if ok {
		return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "concurrency_slot_acquired", Remaining: limit - current}, nil
	}
	return common.OverLimitDecision(policy, 100*time.Millisecond, 0, "concurrency_limit_exceeded"), nil
}
