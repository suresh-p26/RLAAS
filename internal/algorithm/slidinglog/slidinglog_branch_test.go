package slidinglog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type swlCountErrStore struct{ store.CounterStore }

func (swlCountErrStore) TrimBefore(context.Context, string, time.Time) error { return nil }
func (swlCountErrStore) CountAfter(context.Context, string, time.Time) (int64, error) {
	return 0, errors.New("count failed")
}

type swlTrimErrStore struct{ store.CounterStore }

func (swlTrimErrStore) TrimBefore(context.Context, string, time.Time) error {
	return errors.New("trim failed")
}

func TestSlidingLog_CountErrorPath(t *testing.T) {
	e := New(swlCountErrStore{})
	_, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"}}, model.RequestContext{}, "k")
	require.Error(t, err)
}

func TestSlidingLog_TrimErrorPath(t *testing.T) {
	e := New(swlTrimErrStore{})
	_, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"}}, model.RequestContext{}, "k")
	require.Error(t, err)
}
