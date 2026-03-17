package quota

import (
	"context"
	"errors"
	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
	"testing"
	"time"
)

type quotaErrStore struct{ store.CounterStore }

func (quotaErrStore) Increment(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errors.New("boom")
}

func TestQuotaAllowThenDeny(t *testing.T) {
	store := memory.New()
	e := New(store)
	e.Now = func() time.Time { return time.Unix(1000, 0) }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, QuotaPeriod: "day"}, Action: model.ActionDeny}
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || !d.Allowed {
		t.Fatalf("first should allow")
	}
	if d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k"); err != nil || d.Allowed {
		t.Fatalf("second should deny")
	}
}

func TestQuotaErrorPath(t *testing.T) {
	e := New(quotaErrStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected error")
	}
}
