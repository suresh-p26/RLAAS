package slidinglog

import (
	"context"
	"errors"
	"rlaas/pkg/model"
	"rlaas/internal/store"
	"testing"
	"time"
)

type swlCountErrStore struct{ store.CounterStore }

func (swlCountErrStore) AddTimestamp(context.Context, string, time.Time, time.Duration) error {
	return nil
}
func (swlCountErrStore) TrimBefore(context.Context, string, time.Time) error { return nil }
func (swlCountErrStore) CountAfter(context.Context, string, time.Time) (int64, error) {
	return 0, errors.New("count failed")
}

func TestSlidingLogCountErrorPath(t *testing.T) {
	e := New(swlCountErrStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected count error")
	}
}
