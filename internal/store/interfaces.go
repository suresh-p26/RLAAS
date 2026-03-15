package store

import (
	"context"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"time"
)

// CounterStore provides primitives used by different rate limit algorithms.
type CounterStore interface {
	Increment(ctx context.Context, key string, value int64, ttl time.Duration) (int64, error)
	Get(ctx context.Context, key string) (int64, error)
	Set(ctx context.Context, key string, value int64, ttl time.Duration) error
	CompareAndSwap(ctx context.Context, key string, oldVal, newVal int64, ttl time.Duration) (bool, error)
	Delete(ctx context.Context, key string) error

	AddTimestamp(ctx context.Context, key string, ts time.Time, ttl time.Duration) error
	CountAfter(ctx context.Context, key string, after time.Time) (int64, error)
	TrimBefore(ctx context.Context, key string, before time.Time) error

	AcquireLease(ctx context.Context, key string, limit int64, ttl time.Duration) (bool, int64, error)
	ReleaseLease(ctx context.Context, key string) error
}

// PolicyStore loads and manages policy definitions.
type PolicyStore interface {
	LoadPolicies(ctx context.Context, tenantOrOrg string) ([]model.Policy, error)
	GetPolicyByID(ctx context.Context, policyID string) (*model.Policy, error)
	UpsertPolicy(ctx context.Context, p model.Policy) error
	DeletePolicy(ctx context.Context, policyID string) error
	ListPolicies(ctx context.Context, filter map[string]string) ([]model.Policy, error)
}
