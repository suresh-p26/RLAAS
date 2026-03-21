package rlaas

import (
	"context"
	"errors"
	"time"

	"github.com/rlaas-io/rlaas/internal/algorithm"
	"github.com/rlaas-io/rlaas/internal/algorithm/concurrency"
	"github.com/rlaas-io/rlaas/internal/algorithm/fixedwindow"
	"github.com/rlaas-io/rlaas/internal/algorithm/leakybucket"
	"github.com/rlaas-io/rlaas/internal/algorithm/quota"
	"github.com/rlaas-io/rlaas/internal/algorithm/slidingcounter"
	"github.com/rlaas-io/rlaas/internal/algorithm/slidinglog"
	"github.com/rlaas-io/rlaas/internal/algorithm/tokenbucket"
	"github.com/rlaas-io/rlaas/internal/engine/evaluator"
	"github.com/rlaas-io/rlaas/internal/engine/matcher"
	"github.com/rlaas-io/rlaas/internal/key"
	"github.com/rlaas-io/rlaas/internal/store"
	cache "github.com/rlaas-io/rlaas/internal/store/cache"
	memory "github.com/rlaas-io/rlaas/internal/store/counter/memory"
	file "github.com/rlaas-io/rlaas/internal/store/policy/file"
	"github.com/rlaas-io/rlaas/pkg/model"
)

// Evaluator is the public interface exposed by the SDK client.
type Evaluator interface {
	// Evaluate runs policy matching and returns a decision for one request context.
	Evaluate(ctx context.Context, req model.RequestContext) (model.Decision, error)
	// StartConcurrencyLease is used for in flight limits and returns a release function.
	StartConcurrencyLease(ctx context.Context, req model.RequestContext) (model.Decision, func() error, error)
}

// Options configures policy and counter backends for the SDK client.
type Options struct {
	// PolicyStore provides policy definitions.
	PolicyStore store.PolicyStore
	// CounterStore stores runtime counters used by rate limiting algorithms.
	CounterStore store.CounterStore
	// CacheTTL controls local policy cache freshness.
	CacheTTL time.Duration
	// KeyPrefix is used for counter key namespace separation.
	KeyPrefix string
}

// Client is the SDK entrypoint used by applications.
type Client struct {
	engine evaluator.Engine
}

// New builds a client with default matcher, key builder, cache, and algorithms.
// Returns an error if required stores are nil.
func New(opts Options) *Client {
	if opts.PolicyStore == nil {
		panic(errors.New("rlaas: PolicyStore must not be nil"))
	}
	if opts.CounterStore == nil {
		panic(errors.New("rlaas: CounterStore must not be nil"))
	}
	cacheTTL := opts.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = 30 * time.Second
	}
	eng := &evaluator.DefaultEngine{
		PolicyStore:  opts.PolicyStore,
		CounterStore: opts.CounterStore,
		Matcher:      matcher.New(),
		KeyBuilder:   key.New(opts.KeyPrefix),
		PolicyCache:  cache.NewMemoryPolicyCache(cacheTTL),
		Algorithms: map[model.AlgorithmType]algorithm.Evaluator{
			model.AlgoFixedWindow:      fixedwindow.New(opts.CounterStore),
			model.AlgoTokenBucket:      tokenbucket.New(opts.CounterStore),
			model.AlgoSlidingWindowCnt: slidingcounter.New(opts.CounterStore),
			model.AlgoConcurrency:      concurrency.New(opts.CounterStore),
			model.AlgoQuota:            quota.New(opts.CounterStore),
			model.AlgoLeakyBucket:      leakybucket.New(opts.CounterStore),
			model.AlgoSlidingWindowLog: slidinglog.New(opts.CounterStore),
		},
	}
	return &Client{engine: eng}
}

// Evaluate sets request time if missing and returns the final decision.
func (c *Client) Evaluate(ctx context.Context, req model.RequestContext) (model.Decision, error) {
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}
	return c.engine.Evaluate(ctx, req)
}

// StartConcurrencyLease acquires a concurrency slot and returns a release callback.
func (c *Client) StartConcurrencyLease(ctx context.Context, req model.RequestContext) (model.Decision, func() error, error) {
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}
	return c.engine.StartConcurrencyLease(ctx, req)
}

// NewFromPolicyFile creates a Client with an in-memory counter store and a
// file-based policy store loaded from policyPath. This is the simplest way to
// integrate RLAAS from an external module without importing internal packages.
func NewFromPolicyFile(policyPath string) *Client {
	return NewWithConfig(policyPath, "", 30*time.Second)
}

// NewWithConfig creates a Client with in-memory counters, a file-based policy
// store, and custom key prefix and cache TTL settings.
func NewWithConfig(policyPath, keyPrefix string, cacheTTL time.Duration) *Client {
	return New(Options{
		PolicyStore:  file.New(policyPath),
		CounterStore: memory.New(),
		CacheTTL:     cacheTTL,
		KeyPrefix:    keyPrefix,
	})
}
