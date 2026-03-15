package tokenbucket

import (
	"context"
	"errors"
	"rlaas/pkg/model"
	"rlaas/internal/store"
	"rlaas/internal/store/counter/memory"
	"testing"
	"time"
)

type tbErrStore struct{ store.CounterStore }

func (tbErrStore) Get(context.Context, string) (int64, error)              { return 0, nil }
func (tbErrStore) Set(context.Context, string, int64, time.Duration) error { return errors.New("boom") }

func TestTokenBucketBurstThenThrottle(t *testing.T) {
	store := memory.New()
	e := New(store)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Type: model.AlgoTokenBucket, Limit: 1, Burst: 2, RefillRate: 1}, Action: model.ActionDeny}
	req := model.RequestContext{}
	if d, err := e.Evaluate(context.Background(), p, req, "k"); err != nil || !d.Allowed {
		t.Fatalf("first should pass: %+v err=%v", d, err)
	}
	if d, err := e.Evaluate(context.Background(), p, req, "k"); err != nil || !d.Allowed {
		t.Fatalf("second should pass with burst: %+v err=%v", d, err)
	}
	if d, err := e.Evaluate(context.Background(), p, req, "k"); err != nil || d.Allowed {
		t.Fatalf("third should throttle: %+v err=%v", d, err)
	}
	now = now.Add(time.Second)
	if d, err := e.Evaluate(context.Background(), p, req, "k"); err != nil || !d.Allowed {
		t.Fatalf("after refill should pass: %+v err=%v", d, err)
	}
}

func TestTokenBucketSetError(t *testing.T) {
	e := New(tbErrStore{})
	p := model.Policy{Algorithm: model.AlgorithmConfig{Type: model.AlgoTokenBucket, Limit: 1, Burst: 1, RefillRate: 1}, Action: model.ActionDeny}
	if _, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected set error")
	}
}
