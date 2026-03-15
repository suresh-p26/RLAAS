package slidingcounter

import (
	"context"
	"errors"
	"rlaas/internal/store"
	"rlaas/pkg/model"
	"testing"
	"time"
)

type swcGetErrStore struct{ store.CounterStore }

func (swcGetErrStore) Increment(context.Context, string, int64, time.Duration) (int64, error) {
	return 1, nil
}
func (swcGetErrStore) Get(context.Context, string) (int64, error) { return 0, errors.New("get failed") }

func TestSlidingCounterGetErrorPath(t *testing.T) {
	e := New(swcGetErrStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected get error")
	}
}
