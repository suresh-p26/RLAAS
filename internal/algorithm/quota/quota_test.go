package quota

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

type quotaErrStore struct{ store.CounterStore }

func (quotaErrStore) Get(context.Context, string) (int64, error) {
	return 0, errors.New("boom")
}

type quotaCASFalseStore struct{ store.CounterStore }

func (quotaCASFalseStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (quotaCASFalseStore) CompareAndSwap(context.Context, string, int64, int64, time.Duration) (bool, error) {
	return false, nil
}

func TestQuotaAllowThenDeny(t *testing.T) {
	e := New(memory.New())
	e.Now = func() time.Time { return time.Unix(1000, 0) }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, QuotaPeriod: "day"}, Action: model.ActionDeny}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first should allow")

	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "second should deny")
}

func TestQuotaErrorPath(t *testing.T) {
	e := New(quotaErrStore{})
	_, err := e.Evaluate(context.Background(), model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1}}, model.RequestContext{}, "k")
	require.Error(t, err)
}

func TestQuota_SubSecondPeriodUsesDistinctBuckets(t *testing.T) {
	e := New(memory.New())
	now := time.Unix(1000, 100*int64(time.Millisecond))
	e.Now = func() time.Time { return now }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 1, QuotaPeriod: "100ms"}, Action: model.ActionDeny}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "first request should be allowed")

	now = now.Add(100 * time.Millisecond)
	d, err = e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "next sub-second period should use a new bucket")
}

func TestQuota_ContentionExhaustsRetries(t *testing.T) {
	e := New(quotaCASFalseStore{})
	e.Now = func() time.Time { return time.Unix(1000, 0) }
	p := model.Policy{Algorithm: model.AlgorithmConfig{Limit: 10, QuotaPeriod: "day"}, Action: model.ActionDeny}

	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "contention exhaustion should deny")
	assert.Equal(t, "quota_contention", d.Reason)
}
