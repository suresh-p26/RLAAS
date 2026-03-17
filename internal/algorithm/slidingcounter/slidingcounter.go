package slidingcounter

import (
	"context"
	"fmt"
	"github.com/rlaas-io/rlaas/internal/algorithm/common"
	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
	"time"
)

// Evaluator applies approximate sliding window logic using sub buckets.
type Evaluator struct {
	Counter store.CounterStore
	Now     func() time.Time
}

// New creates a sliding window counter evaluator.
func New(counter store.CounterStore) *Evaluator {
	return &Evaluator{Counter: counter}
}

// Evaluate writes to current bucket and combines weighted current and prior counts.
func (e *Evaluator) Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error) {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	window := common.WindowDuration(policy.Algorithm)
	sub := policy.Algorithm.SubWindowCount
	if sub <= 0 {
		sub = 10
	}
	bucketDur := window / time.Duration(sub)
	curBucket := now.Truncate(bucketDur)
	prevBucket := curBucket.Add(-bucketDur)
	cost := common.Cost(req, policy.Algorithm)
	curKey := fmt.Sprintf("%s:swc:%d", key, curBucket.UnixNano())
	prevKey := fmt.Sprintf("%s:swc:%d", key, prevBucket.UnixNano())
	curCount, err := e.Counter.Increment(ctx, curKey, cost, window+bucketDur)
	if err != nil {
		return model.Decision{}, err
	}
	prevCount, err := e.Counter.Get(ctx, prevKey)
	if err != nil {
		return model.Decision{}, err
	}
	elapsed := now.Sub(curBucket)
	weight := float64(bucketDur-elapsed) / float64(bucketDur)
	estimated := float64(curCount) + (float64(prevCount) * weight)
	limit := float64(policy.Algorithm.Limit)
	if limit <= 0 {
		limit = 1
	}
	if estimated <= limit {
		return model.Decision{Allowed: true, Action: model.ActionAllow, Reason: "within_sliding_window", Remaining: int64(limit - estimated), ResetAt: curBucket.Add(bucketDur)}, nil
	}
	return common.OverLimitDecision(policy, bucketDur-elapsed, 0, "sliding_window_limit_exceeded"), nil
}
