package tokenbucket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/internal/store"
	"github.com/rlaas-io/rlaas/pkg/model"
)

// tbSetErrStore: CAS succeeds but Set (timestamp write) fails.
type tbSetErrStore struct {
	store.CounterStore
}

func (tbSetErrStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (tbSetErrStore) CompareAndSwap(_ context.Context, _ string, _, _ int64, _ time.Duration) (bool, error) {
	return true, nil
}
func (tbSetErrStore) Set(context.Context, string, int64, time.Duration) error {
	return errors.New("set failed")
}

// tbCASAlwaysFalseStore: CAS always returns false (no error), simulating contention.
type tbCASAlwaysFalseStore struct {
	store.CounterStore
}

func (tbCASAlwaysFalseStore) Get(context.Context, string) (int64, error) { return 0, nil }
func (tbCASAlwaysFalseStore) CompareAndSwap(_ context.Context, _ string, _, _ int64, _ time.Duration) (bool, error) {
	return false, nil
}

func TestTokenBucket_SetErrorAfterCAS(t *testing.T) {
	e := New(&tbSetErrStore{})
	p := tbPolicy(1, 1, 1)
	_, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.Error(t, err)
}

func TestTokenBucket_ContentionExhaustsRetries(t *testing.T) {
	e := New(&tbCASAlwaysFalseStore{})
	now := time.Unix(1000, 0)
	e.Now = func() time.Time { return now }
	p := tbPolicy(10, 10, 1)
	d, err := e.Evaluate(context.Background(), p, model.RequestContext{}, "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "contention exhaustion should deny")
	assert.Equal(t, "token_bucket_contention", d.Reason)
}
