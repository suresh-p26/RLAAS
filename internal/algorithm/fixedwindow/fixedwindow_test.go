package fixedwindow

import (
	"context"
	"errors"
	"rlaas/pkg/model"
	"rlaas/internal/store"
	"rlaas/internal/store/counter/memory"
	"testing"
	"time"
)

type fwErrStore struct{ store.CounterStore }

func (fwErrStore) Increment(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errors.New("boom")
}

func TestFixedWindowAllowThenDeny(t *testing.T) {
	store := memory.New()
	e := New(store)
	e.Now = func() time.Time { return time.Unix(1000, 0) }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 2, Window: "1m"}, Action: model.ActionDeny}
	req := model.RequestContext{}
	if d, err := e.Evaluate(context.Background(), p, req, "k"); err != nil || !d.Allowed {
		t.Fatalf("first request should be allowed: %+v err=%v", d, err)
	}
	if d, err := e.Evaluate(context.Background(), p, req, "k"); err != nil || !d.Allowed {
		t.Fatalf("second request should be allowed: %+v err=%v", d, err)
	}
	if d, err := e.Evaluate(context.Background(), p, req, "k"); err != nil || d.Allowed {
		t.Fatalf("third request should be denied: %+v err=%v", d, err)
	}
}

func TestFixedWindowErrorAndDefaultLimit(t *testing.T) {
	e := New(fwErrStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Window: "1m"}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected error")
	}
	e2 := New(memory.New())
	if d, err := e2.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Window: "1m"}}, model.RequestContext{}, "k2"); err != nil || !d.Allowed {
		t.Fatalf("expected allow with default limit")
	}
}
