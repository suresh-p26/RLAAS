package concurrency

import (
	"context"
	"time"

	"github.com/rlaas-io/rlaas/internal/algorithm/common"
	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
)

// DefaultLeaseTTL is the safety-net expiry if the caller never releases.
const DefaultLeaseTTL = 2 * time.Minute

// Evaluator limits the number of concurrent in-flight operations.
// It exposes a Release method so callers can free slots when work completes,
// rather than relying solely on TTL expiry.
type Evaluator struct {
	Counter store.CounterStore
}

// New creates a concurrency evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate tries to acquire one lease and denies when the limit is reached.
// The lease key is returned in the decision's Reason field so the caller
// can pass it to Release.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, _ model.RequestContext, key string) (model.Decision, error) {
	limit := policy.Algorithm.MaxConcurrency
	if limit <= 0 {
		limit = policy.Algorithm.Limit
	}
	if limit <= 0 {
		limit = 1
	}

	leaseTTL := DefaultLeaseTTL
	if policy.Algorithm.LeaseTTL > 0 {
		leaseTTL = time.Duration(policy.Algorithm.LeaseTTL) * time.Second
	}

	leaseKey := key + ":concurrency"
	ok, current, err := e.Counter.AcquireLease(ctx, leaseKey, limit, leaseTTL)
	if err != nil {
		return model.Decision{}, err
	}
	if ok {
		return model.Decision{
			Allowed:   true,
			Action:    model.ActionAllow,
			Reason:    "concurrency_slot_acquired",
			Remaining: limit - current,
		}, nil
	}
	return common.OverLimitDecision(policy, 100*time.Millisecond, 0, "concurrency_limit_exceeded"), nil
}

// Release frees a concurrency slot. Should be called when the in-flight
// operation completes.
func (e *Evaluator) Release(ctx context.Context, key string) error {
	return e.Counter.ReleaseLease(ctx, key+":concurrency")
}
