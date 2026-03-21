package algorithm_test

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/algorithm/concurrency"
	"github.com/rlaas-io/rlaas/internal/algorithm/fixedwindow"
	"github.com/rlaas-io/rlaas/internal/algorithm/leakybucket"
	"github.com/rlaas-io/rlaas/internal/algorithm/quota"
	"github.com/rlaas-io/rlaas/internal/algorithm/slidingcounter"
	"github.com/rlaas-io/rlaas/internal/algorithm/slidinglog"
	"github.com/rlaas-io/rlaas/internal/algorithm/tokenbucket"
	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

// --------------------------------------------------------------------------
// Shared helpers
// --------------------------------------------------------------------------

type algorithmFactory struct {
	name   string
	policy model.Policy
	create func(s *memory.MemoryStore) evaluator
}

type evaluator interface {
	Evaluate(ctx context.Context, policy model.Policy, req model.RequestContext, key string) (model.Decision, error)
}

func allAlgorithms() []algorithmFactory {
	return []algorithmFactory{
		{
			name: "fixed_window",
			policy: model.Policy{
				Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow, Limit: 1000, Window: "1s"},
				Action:    model.ActionDeny,
			},
			create: func(s *memory.MemoryStore) evaluator { return fixedwindow.New(s) },
		},
		{
			name: "sliding_counter",
			policy: model.Policy{
				Algorithm: model.AlgorithmConfig{Type: model.AlgoSlidingWindowCnt, Limit: 1000, Window: "1s"},
				Action:    model.ActionDeny,
			},
			create: func(s *memory.MemoryStore) evaluator { return slidingcounter.New(s) },
		},
		{
			name: "sliding_log",
			policy: model.Policy{
				Algorithm: model.AlgorithmConfig{Type: model.AlgoSlidingWindowLog, Limit: 1000, Window: "1s"},
				Action:    model.ActionDeny,
			},
			create: func(s *memory.MemoryStore) evaluator { return slidinglog.New(s) },
		},
		{
			name: "token_bucket",
			policy: model.Policy{
				Algorithm: model.AlgorithmConfig{Type: model.AlgoTokenBucket, Limit: 1000, Burst: 1000, RefillRate: 1000},
				Action:    model.ActionDeny,
			},
			create: func(s *memory.MemoryStore) evaluator { return tokenbucket.New(s) },
		},
		{
			name: "leaky_bucket",
			policy: model.Policy{
				Algorithm: model.AlgorithmConfig{Type: model.AlgoLeakyBucket, Limit: 1000, Window: "1s", LeakRate: 1000},
				Action:    model.ActionDeny,
			},
			create: func(s *memory.MemoryStore) evaluator { return leakybucket.New(s) },
		},
		{
			name: "quota",
			policy: model.Policy{
				Algorithm: model.AlgorithmConfig{Type: model.AlgoQuota, Limit: 10000, QuotaPeriod: "day"},
				Action:    model.ActionDeny,
			},
			create: func(s *memory.MemoryStore) evaluator { return quota.New(s) },
		},
	}
}

// --------------------------------------------------------------------------
// 1. Stress tests: high concurrency, no panics, no data races
// --------------------------------------------------------------------------

func TestStress_AllAlgorithms_NoPanicsNorRaces(t *testing.T) {
	const goroutines = 100
	const iterPerG = 200

	for _, alg := range allAlgorithms() {
		alg := alg
		t.Run(alg.name, func(t *testing.T) {
			t.Parallel()
			s := memory.NewSharded(64)
			e := alg.create(s)
			ctx := context.Background()
			req := model.RequestContext{}
			var wg sync.WaitGroup
			var errCount atomic.Int64
			var panicCount atomic.Int64

			for g := 0; g < goroutines; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() {
						if r := recover(); r != nil {
							panicCount.Add(1)
						}
					}()
					for i := 0; i < iterPerG; i++ {
						_, err := e.Evaluate(ctx, alg.policy, req, "stress-key")
						if err != nil {
							errCount.Add(1)
						}
					}
				}()
			}
			wg.Wait()

			if p := panicCount.Load(); p > 0 {
				t.Fatalf("detected %d panics during stress test", p)
			}
			if ec := errCount.Load(); ec > 0 {
				t.Fatalf("detected %d errors during stress test", ec)
			}
		})
	}
}

// --------------------------------------------------------------------------
// 2. Contention correctness: verify no over-admission beyond limit
// --------------------------------------------------------------------------

func TestContention_FixedWindow_NoOverAdmission(t *testing.T) {
	const limit = 50
	const goroutines = 100
	const iterPerG = 10

	s := memory.NewSharded(64)
	e := fixedwindow.New(s)
	frozenNow := time.Unix(1000, 0)
	e.Now = func() time.Time { return frozenNow }

	pol := model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow, Limit: limit, Window: "10s"},
		Action:    model.ActionDeny,
	}

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterPerG; i++ {
				d, err := e.Evaluate(context.Background(), pol, model.RequestContext{}, "fw-contention")
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if d.Allowed {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if a := allowed.Load(); a > limit {
		t.Fatalf("over-admission detected: allowed %d > limit %d", a, limit)
	}
}

func TestContention_TokenBucket_NoOverAdmission(t *testing.T) {
	const burst = 50
	const goroutines = 100
	const iterPerG = 10

	s := memory.NewSharded(64)
	e := tokenbucket.New(s)
	frozenNow := time.Unix(1000, 0)
	e.Now = func() time.Time { return frozenNow }

	pol := model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoTokenBucket, Limit: burst, Burst: burst, RefillRate: 0.001},
		Action:    model.ActionDeny,
	}

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterPerG; i++ {
				d, err := e.Evaluate(context.Background(), pol, model.RequestContext{}, "tb-contention")
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if d.Allowed {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if a := allowed.Load(); a > burst {
		t.Fatalf("over-admission detected: allowed %d > burst %d", a, burst)
	}
}

func TestContention_LeakyBucket_NoOverAdmission(t *testing.T) {
	const limit = 50
	const goroutines = 100
	const iterPerG = 10

	s := memory.NewSharded(64)
	e := leakybucket.New(s)
	frozenNow := time.Unix(1000, 0)
	e.Now = func() time.Time { return frozenNow }

	pol := model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoLeakyBucket, Limit: limit, Window: "10s", LeakRate: 0.001},
		Action:    model.ActionDeny,
	}

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterPerG; i++ {
				d, err := e.Evaluate(context.Background(), pol, model.RequestContext{}, "lb-contention")
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if d.Allowed {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if a := allowed.Load(); a > limit {
		t.Fatalf("over-admission detected: allowed %d > limit %d", a, limit)
	}
}

func TestContention_SlidingLog_NoOverAdmission(t *testing.T) {
	const limit = 50
	const goroutines = 100
	const iterPerG = 10

	s := memory.NewSharded(64)
	e := slidinglog.New(s)
	frozenNow := time.Unix(1000, 0)
	e.Now = func() time.Time { return frozenNow }

	pol := model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoSlidingWindowLog, Limit: limit, Window: "10s"},
		Action:    model.ActionDeny,
	}

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterPerG; i++ {
				d, err := e.Evaluate(context.Background(), pol, model.RequestContext{}, "sl-contention")
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if d.Allowed {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if a := allowed.Load(); a > limit {
		t.Fatalf("over-admission detected: allowed %d > limit %d", a, limit)
	}
}

func TestContention_SlidingCounter_NoOverAdmission(t *testing.T) {
	const limit = 50
	const goroutines = 100
	const iterPerG = 10

	s := memory.NewSharded(64)
	e := slidingcounter.New(s)
	frozenNow := time.Unix(1000, 0)
	e.Now = func() time.Time { return frozenNow }

	pol := model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoSlidingWindowCnt, Limit: limit, Window: "10s"},
		Action:    model.ActionDeny,
	}

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterPerG; i++ {
				d, err := e.Evaluate(context.Background(), pol, model.RequestContext{}, "sc-contention")
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if d.Allowed {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if a := allowed.Load(); a > limit {
		t.Fatalf("over-admission detected: allowed %d > limit %d", a, limit)
	}
}

func TestContention_Quota_NoOverAdmission(t *testing.T) {
	const limit = 50
	const goroutines = 100
	const iterPerG = 10

	s := memory.NewSharded(64)
	e := quota.New(s)
	frozenNow := time.Unix(1000, 0)
	e.Now = func() time.Time { return frozenNow }

	pol := model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoQuota, Limit: limit, QuotaPeriod: "day"},
		Action:    model.ActionDeny,
	}

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterPerG; i++ {
				d, err := e.Evaluate(context.Background(), pol, model.RequestContext{}, "q-contention")
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if d.Allowed {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if a := allowed.Load(); a > limit {
		t.Fatalf("over-admission detected: allowed %d > limit %d", a, limit)
	}
}

func TestContention_Concurrency_NoOverAdmission(t *testing.T) {
	const limit = 10
	const goroutines = 100

	s := memory.NewSharded(64)
	e := concurrency.New(s)
	pol := model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoConcurrency, MaxConcurrency: limit},
		Action:    model.ActionDeny,
	}

	var maxConcurrent atomic.Int64
	var current atomic.Int64
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := e.Evaluate(context.Background(), pol, model.RequestContext{}, "conc-contention")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if d.Allowed {
				c := current.Add(1)
				// Record peak.
				for {
					old := maxConcurrent.Load()
					if c <= old || maxConcurrent.CompareAndSwap(old, c) {
						break
					}
				}
				time.Sleep(time.Millisecond) // simulate work
				current.Add(-1)
				_ = e.Release(context.Background(), "conc-contention")
			}
		}()
	}
	wg.Wait()

	if mc := maxConcurrent.Load(); mc > limit {
		t.Fatalf("peak concurrency %d exceeded limit %d", mc, limit)
	}
}

// --------------------------------------------------------------------------
// 3. Latency budget tests: p50, p95, p99
// --------------------------------------------------------------------------

func TestLatencyBudget_AllAlgorithms(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping latency budget test in short mode")
	}

	const iterations = 5000
	// Conservative budgets for in-memory store. Real production targets
	// would be higher due to network latency with Redis/Postgres.
	budgets := struct {
		p50 time.Duration
		p95 time.Duration
		p99 time.Duration
	}{
		p50: 50 * time.Microsecond,
		p95: 200 * time.Microsecond,
		p99: 1 * time.Millisecond,
	}

	for _, alg := range allAlgorithms() {
		alg := alg
		t.Run(alg.name, func(t *testing.T) {
			t.Parallel()
			s := memory.NewSharded(64)
			e := alg.create(s)
			ctx := context.Background()
			req := model.RequestContext{}

			// Warm-up: prime cache, trigger any lazy init.
			for i := 0; i < 100; i++ {
				_, _ = e.Evaluate(ctx, alg.policy, req, "latency-warmup")
			}

			latencies := make([]time.Duration, iterations)
			for i := 0; i < iterations; i++ {
				start := time.Now()
				_, _ = e.Evaluate(ctx, alg.policy, req, "latency-key")
				latencies[i] = time.Since(start)
			}
			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

			p50 := latencies[int(float64(iterations)*0.50)]
			p95 := latencies[int(float64(iterations)*0.95)]
			p99 := latencies[int(math.Min(float64(iterations)*0.99, float64(iterations-1)))]

			t.Logf("p50=%v  p95=%v  p99=%v", p50, p95, p99)

			if p50 > budgets.p50 {
				t.Errorf("p50 latency %v exceeds budget %v", p50, budgets.p50)
			}
			if p95 > budgets.p95 {
				t.Errorf("p95 latency %v exceeds budget %v", p95, budgets.p95)
			}
			if p99 > budgets.p99 {
				t.Errorf("p99 latency %v exceeds budget %v", p99, budgets.p99)
			}
		})
	}
}

// --------------------------------------------------------------------------
// 4. Soak test: sustained load, detect memory leaks
// --------------------------------------------------------------------------

func TestSoak_MemoryStability(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping soak test in short mode")
	}

	const soakDuration = 3 * time.Second
	const goroutines = 50

	for _, alg := range allAlgorithms() {
		alg := alg
		t.Run(alg.name, func(t *testing.T) {
			s := memory.NewShardedWithGC(64, 100*time.Millisecond)
			defer s.Stop()
			e := alg.create(s)
			ctx := context.Background()
			req := model.RequestContext{}

			// Baseline memory.
			runtime.GC()
			var memBefore runtime.MemStats
			runtime.ReadMemStats(&memBefore)

			deadline := time.Now().Add(soakDuration)
			var ops atomic.Int64
			var wg sync.WaitGroup
			for g := 0; g < goroutines; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for time.Now().Before(deadline) {
						_, _ = e.Evaluate(ctx, alg.policy, req, fmt.Sprintf("soak-key-%d", ops.Add(1)%100))
					}
				}()
			}
			wg.Wait()

			runtime.GC()
			var memAfter runtime.MemStats
			runtime.ReadMemStats(&memAfter)

			totalOps := ops.Load()
			// Use signed arithmetic to handle GC reclaiming more than was allocated.
			heapGrowthMB := float64(int64(memAfter.HeapInuse)-int64(memBefore.HeapInuse)) / (1024 * 1024)

			t.Logf("ops=%d  heapGrowth=%.2fMB", totalOps, heapGrowthMB)

			// Generous ceiling: 50MB heap growth for a soak test is a leak indicator.
			if heapGrowthMB > 50 {
				t.Errorf("possible memory leak: heap grew %.2fMB over %d ops", heapGrowthMB, totalOps)
			}
		})
	}
}

// --------------------------------------------------------------------------
// 5. Multi-key isolation under concurrent load
// --------------------------------------------------------------------------

func TestStress_MultiKeyIsolation(t *testing.T) {
	const keys = 20
	const limitPerKey = 10
	const goroutines = 80
	const iterPerG = 5

	s := memory.NewSharded(64)
	e := fixedwindow.New(s)
	frozenNow := time.Unix(1000, 0)
	e.Now = func() time.Time { return frozenNow }

	pol := model.Policy{
		Algorithm: model.AlgorithmConfig{Type: model.AlgoFixedWindow, Limit: limitPerKey, Window: "10s"},
		Action:    model.ActionDeny,
	}

	allowedPerKey := make([]atomic.Int64, keys)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			keyIdx := g % keys
			key := fmt.Sprintf("iso-key-%d", keyIdx)
			for i := 0; i < iterPerG; i++ {
				d, err := e.Evaluate(context.Background(), pol, model.RequestContext{}, key)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if d.Allowed {
					allowedPerKey[keyIdx].Add(1)
				}
			}
		}(g)
	}
	wg.Wait()

	for k := 0; k < keys; k++ {
		a := allowedPerKey[k].Load()
		if a > limitPerKey {
			t.Errorf("key %d: over-admitted %d > limit %d", k, a, limitPerKey)
		}
	}
}

// --------------------------------------------------------------------------
// 6. Context cancellation resilience
// --------------------------------------------------------------------------

func TestStress_ContextCancellation(t *testing.T) {
	const goroutines = 50

	for _, alg := range allAlgorithms() {
		alg := alg
		t.Run(alg.name, func(t *testing.T) {
			t.Parallel()
			s := memory.NewSharded(64)
			e := alg.create(s)
			req := model.RequestContext{}

			var wg sync.WaitGroup
			var panicCount atomic.Int64
			for g := 0; g < goroutines; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() {
						if r := recover(); r != nil {
							panicCount.Add(1)
						}
					}()
					ctx, cancel := context.WithCancel(context.Background())
					cancel() // already cancelled
					_, _ = e.Evaluate(ctx, alg.policy, req, "ctx-cancel-key")
				}()
			}
			wg.Wait()

			if p := panicCount.Load(); p > 0 {
				t.Fatalf("detected %d panics with cancelled context", p)
			}
		})
	}
}
