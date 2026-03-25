package fixedwindow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type fwErrStore struct{ store.CounterStore }

func (fwErrStore) Get(context.Context, string) (int64, error) {
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

func TestFixedWindow_SubSecondWindowUsesDistinctBuckets(t *testing.T) {
	store := memory.New()
	e := New(store)
	now := time.Unix(1000, 100*int64(time.Millisecond))
	e.Now = func() time.Time { return now }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "100ms"}, Action: model.ActionDeny}

	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || !d.Allowed {
		t.Fatalf("first request should be allowed: %+v err=%v", d, err)
	}
	now = now.Add(100 * time.Millisecond)
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || !d.Allowed {
		t.Fatalf("next sub-second window should use a new bucket: %+v err=%v", d, err)
	}
}
