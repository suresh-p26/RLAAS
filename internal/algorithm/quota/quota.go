package quota

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

// Evaluator enforces long-window usage budgets (day, week, month quotas).
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a quota evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate increments current period usage via CAS and checks remaining budget.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error) {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	period := policy.Algorithm.QuotaPeriod
	if period == "" {
		period = "day"
	}
	window := common.WindowDuration(model.AlgorithmConfig{Window: period})
	bucketStart := common.WindowStart(now, model.AlgorithmConfig{Window: period})
	bucketEnd := common.WindowEnd(now, model.AlgorithmConfig{Window: period})
	bucketKey := fmt.Sprintf("%s:quota:%d", key, bucketStart.UnixNano())
	limit := policy.Algorithm.Limit
	if limit <= 0 {
		limit = 1
	}
	cost := common.Cost(req, policy.Algorithm)

	retries := maxRetries(policy.Algorithm)
	for attempt := 0; attempt < retries; attempt++ {
		consumed, err := e.Counter.Get(ctx, bucketKey)
		if err != nil {
			return model.Decision{}, err
		}

		if consumed+cost > limit {
			return common.OverLimitDecision(policy, bucketEnd.Sub(now), 0, "quota_exceeded"), nil
		}

		swapped, err := e.Counter.CompareAndSwap(ctx, bucketKey, consumed, consumed+cost, window)
		if err != nil {
			return model.Decision{}, err
		}
		if swapped {
			remaining := limit - (consumed + cost)
			return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "within_quota", Remaining: remaining, ResetAt: bucketEnd}, nil
		}
	}

	return common.OverLimitDecision(policy, bucketEnd.Sub(now), 0, "quota_contention"), nil
}
