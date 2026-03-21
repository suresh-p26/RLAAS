package cache

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestGetOrLoad_CacheHit(t *testing.T) {
	c := NewMemoryPolicyCache(10 * time.Second)
	defer c.Stop()

	c.Set("ns1", []model.Policy{{PolicyID: "cached"}})

	callCount := 0
	got, err := c.GetOrLoad("ns1", func(ns string) ([]model.Policy, error) {
		callCount++
		return []model.Policy{{PolicyID: "loaded"}}, nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if callCount != 0 {
		t.Fatalf("loader should not be called on cache hit")
	}
	if got[0].PolicyID != "cached" {
		t.Fatalf("expected cached value, got %s", got[0].PolicyID)
	}
}

func TestGetOrLoad_CacheMiss(t *testing.T) {
	c := NewMemoryPolicyCache(10 * time.Second)
	defer c.Stop()

	got, err := c.GetOrLoad("ns1", func(ns string) ([]model.Policy, error) {
		return []model.Policy{{PolicyID: "loaded"}}, nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0].PolicyID != "loaded" {
		t.Fatalf("expected loaded value, got %s", got[0].PolicyID)
	}

	// Should be cached now.
	cached, ok := c.Get("ns1")
	if !ok {
		t.Fatal("expected cache hit after GetOrLoad")
	}
	if cached[0].PolicyID != "loaded" {
		t.Fatal("cached value mismatch")
	}
}

func TestGetOrLoad_LoaderError(t *testing.T) {
	c := NewMemoryPolicyCache(10 * time.Second)
	defer c.Stop()

	_, err := c.GetOrLoad("ns1", func(ns string) ([]model.Policy, error) {
		return nil, fmt.Errorf("load failed")
	})
	if err == nil {
		t.Fatal("expected error from loader")
	}

	// Cache should NOT be populated on error.
	if _, ok := c.Get("ns1"); ok {
		t.Fatal("cache should be empty after loader error")
	}
}

func TestGetOrLoad_Singleflight(t *testing.T) {
	c := NewMemoryPolicyCache(10 * time.Second)
	defer c.Stop()

	var callCount atomic.Int32
	var barrier sync.WaitGroup
	barrier.Add(1)

	loader := func(ns string) ([]model.Policy, error) {
		callCount.Add(1)
		barrier.Wait() // Block until released.
		return []model.Policy{{PolicyID: "result"}}, nil
	}

	// Launch 10 concurrent requests.
	var wg sync.WaitGroup
	results := make([][]model.Policy, 10)
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = c.GetOrLoad("ns1", loader)
		}(i)
	}

	// Small delay to ensure all goroutines are waiting.
	time.Sleep(20 * time.Millisecond)
	barrier.Done() // Release the loader.
	wg.Wait()

	// Loader should have been called exactly once (singleflight).
	if count := callCount.Load(); count != 1 {
		t.Fatalf("loader called %d times, expected 1 (singleflight dedup)", count)
	}

	// All goroutines should get the same result.
	for i := 0; i < 10; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d error: %v", i, errs[i])
		}
		if results[i][0].PolicyID != "result" {
			t.Fatalf("goroutine %d got wrong result: %s", i, results[i][0].PolicyID)
		}
	}
}

func TestMemoryPolicyCache_MaxItems(t *testing.T) {
	c := NewMemoryPolicyCacheWithLimits(10*time.Second, 3, time.Minute)
	defer c.Stop()

	c.Set("a", []model.Policy{{PolicyID: "a"}})
	c.Set("b", []model.Policy{{PolicyID: "b"}})
	c.Set("c", []model.Policy{{PolicyID: "c"}})
	c.Set("d", []model.Policy{{PolicyID: "d"}})

	// Should have evicted to stay at 3.
	c.mu.RLock()
	count := len(c.items)
	c.mu.RUnlock()
	if count > 3 {
		t.Fatalf("expected max 3 items, got %d", count)
	}
}
