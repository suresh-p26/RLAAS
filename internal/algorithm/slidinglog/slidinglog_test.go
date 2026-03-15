package slidinglog

import (
	"context"
	"errors"
	"rlaas/pkg/model"
	"rlaas/internal/store"
	"rlaas/internal/store/counter/memory"
	"testing"
	"time"
)

type swlErrStore struct{ store.CounterStore }

func (swlErrStore) AddTimestamp(context.Context, string, time.Time, time.Duration) error {
	return errors.New("boom")
}

func TestSlidingLogAllowThenDeny(t *testing.T) {
	store := memory.New()
	e := New(store)
	e.Now = func() time.Time { return time.Unix(1000, 0) }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"}, Action: model.ActionDeny}
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || !d.Allowed {
		t.Fatalf("first should allow")
	}
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || d.Allowed {
		t.Fatalf("second should deny")
	}
}

func TestSlidingLogErrorPath(t *testing.T) {
	e := New(swlErrStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected error")
	}
}
