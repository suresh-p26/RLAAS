package leakybucket

import (
	"context"
	"errors"
	"rlaas/internal/store"
	"rlaas/internal/store/counter/memory"
	"rlaas/pkg/model"
	"testing"
	"time"
)

type lbErrStore struct{ store.CounterStore }

func (lbErrStore) Get(context.Context, string) (int64, error) { return 0, errors.New("boom") }

type lbSetErrStore struct{ store.CounterStore }

func (lbSetErrStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (lbSetErrStore) Set(context.Context, string, int64, time.Duration) error {
	return errors.New("boom")
}

func TestLeakyBucketAllowThenDeny(t *testing.T) {
	store := memory.New()
	e := New(store)
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1s", LeakRate: 0.1}, Action: model.ActionDeny}
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || !d.Allowed {
		t.Fatalf("first should allow")
	}
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || d.Allowed {
		t.Fatalf("second should deny")
	}
}

func TestLeakyBucketErrorPath(t *testing.T) {
	e := New(lbErrStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1s"}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected error")
	}
	e2 := New(lbSetErrStore{})
	if _, err := e2.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1s"}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected set error")
	}
}
