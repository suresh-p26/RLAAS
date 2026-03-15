package tokenbucket

import (
	"context"
	"errors"
	"rlaas/pkg/model"
	"rlaas/internal/store"
	"testing"
	"time"
)

type tbSecondSetErrStore struct {
	store.CounterStore
	setCount int
}

func (s *tbSecondSetErrStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (s *tbSecondSetErrStore) Set(context.Context, string, int64, time.Duration) error {
	s.setCount++
	if s.setCount == 2 {
		return errors.New("second set failed")
	}
	return nil
}

func TestTokenBucketSecondSetErrorAndRefillDefaults(t *testing.T) {
	errStore := &tbSecondSetErrStore{}
	e := New(errStore)
	p := model.Policy{Algorithm: model.AlgorithmConfig{Type: model.AlgoTokenBucket, Limit: 1, Burst: 1, RefillRate: 1}, Action: model.ActionDeny}
	if _, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected second set error")
	}

	e2 := New(errStore)
	e2.Now = func() time.Time { return time.Unix(1000, 0) }
	p2 := model.Policy{Algorithm: model.AlgorithmConfig{Type: model.AlgoTokenBucket, Limit: 0, Burst: 0, RefillRate: 0, Window: "bad"}, Action: model.ActionDeny}
	if d, err := e2.Evaluate(context.Background(), p2, model.RequestContext{}, "k2"); err != nil || !d.Allowed {
		t.Fatalf("expected allow with fallback capacity/refill")
	}
}
