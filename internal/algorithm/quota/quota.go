package quota

import (
	"context"
	"fmt"
	"github.com/rlaas-io/rlaas/internal/algorithm/common"
	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
	"time"
)

// Evaluator enforces long window usage budgets such as day or month quotas.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a quota evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate increments current period usage and checks remaining budget.
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
	bucketStart := now.Truncate(window)
	bucketKey := fmt.Sprintf("%s:quota:%d", key, bucketStart.Unix())
	consumed, err := e.Counter.Increment(ctx, bucketKey, common.Cost(req, policy.Algorithm), window)
	if err != nil {
		return model.Decision{}, err
	}
	limit := policy.Algorithm.Limit
	if limit <= 0 {
		limit = 1
	}
	remaining := limit - consumed
	if remaining >= 0 {
		return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "within_quota", Remaining: remaining, ResetAt: bucketStart.Add(window)}, nil
	}
	return common.OverLimitDecision(policy, bucketStart.Add(window).Sub(now), 0, "quota_exceeded"), nil
}
