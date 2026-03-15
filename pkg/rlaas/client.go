package rlaas

import (
	"context"
	"github.com/suresh-p26/RLAAS/internal/algorithm"
	"github.com/suresh-p26/RLAAS/internal/algorithm/concurrency"
	"github.com/suresh-p26/RLAAS/internal/algorithm/fixedwindow"
	"github.com/suresh-p26/RLAAS/internal/algorithm/leakybucket"
	"github.com/suresh-p26/RLAAS/internal/algorithm/quota"
	"github.com/suresh-p26/RLAAS/internal/algorithm/slidingcounter"
	"github.com/suresh-p26/RLAAS/internal/algorithm/slidinglog"
	"github.com/suresh-p26/RLAAS/internal/algorithm/tokenbucket"
	"github.com/suresh-p26/RLAAS/internal/engine/evaluator"
	"github.com/suresh-p26/RLAAS/internal/engine/matcher"
	"github.com/suresh-p26/RLAAS/internal/key"
	"github.com/suresh-p26/RLAAS/internal/store"
	cache "github.com/suresh-p26/RLAAS/internal/store/cache"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"time"
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
func New(opts Options) *Client {
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
