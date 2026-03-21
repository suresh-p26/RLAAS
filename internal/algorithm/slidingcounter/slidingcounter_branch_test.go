package slidingcounter

import (
	"context"
	"errors"
	"testing"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type swcGetErrStore struct{ store.CounterStore }

func (swcGetErrStore) Get(context.Context, string) (int64, error) { return 0, errors.New("get failed") }

func TestSlidingCounter_GetErrorPath(t *testing.T) {
	e := New(swcGetErrStore{})
	if _, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "1m"}}, model.RequestContext{}, "k"); err == nil {
		t.Fatalf("expected get error")
	}
}
