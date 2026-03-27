package fixedwindow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

type fwErrStore struct{ store.CounterStore }

func (fwErrStore) Get(context.Context, string) (int64, error) {
	return 0, errors.New("boom")
}

func TestFixedWindowAllowThenDeny(t *testing.T) {
	e := New(memory.New())
	e.Now = func() time.Time { return time.Unix(1000, 0) }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 2, Window: "1m"}, Action: model.ActionDeny}
	req := model.RequestContext{}

	d, err := e.Evaluate(context.Background(), p, req, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first request should be allowed")

	d, err = e.Evaluate(context.Background(), p, req, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "second request should be allowed")

	d, err = e.Evaluate(context.Background(), p, req, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "third request should be denied")
}

func TestFixedWindowErrorAndDefaultLimit(t *testing.T) {
	e := New(fwErrStore{})
	_, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Window: "1m"}}, model.RequestContext{}, "k")
	require.Error(t, err)

	e2 := New(memory.New())
	d, err := e2.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Window: "1m"}}, model.RequestContext{}, "k2")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "should allow with default limit")
}

func TestFixedWindow_SubSecondWindowUsesDistinctBuckets(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 100*int64(time.Millisecond))
	e.Now = func() time.Time { return now }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, Window: "100ms"}, Action: model.ActionDeny}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first request should be allowed")

	now = now.Add(100 * time.Millisecond)
	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "next sub-second window should use a new bucket")
}
