package slidingcounter

import (
	"context"
	"errors"
	"rlaas/internal/store"
	"rlaas/internal/store/counter/memory"
	"rlaas/pkg/model"
	"testing"
	"time"
)

type swcErrStore struct{ store.CounterStore }

func (swcErrStore) Increment(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errors.New("boom")
}

func TestSlidingCounterAllowThenDeny(t *testing.T) {
	store := memory.New()
	e := New(store)
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m", SubWindowCount: 2}, Action: model.ActionDeny}
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || !d.Allowed {
		t.Fatalf("first should allow")
	}
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || d.Allowed {
		t.Fatalf("second should deny")
	}
}

func TestSlidingCounterErrorPath(t *testing.T) {
	e := New(swcErrStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected error")
	}
}
