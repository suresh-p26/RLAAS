package cache

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestGetOrLoad(t *testing.T) {
	tests := []struct {
		name         string
		preload      []model.Policy
		loader       func(string) ([]model.Policy, error)
		wantErr      bool
		wantPolicyID string
		wantCached   bool
	}{
		{
			name:    "cache hit skips loader",
			preload: []model.Policy{{PolicyID: "cached"}},
			loader: func(_ string) ([]model.Policy, error) {
				return []model.Policy{{PolicyID: "loaded"}}, nil
			},
			wantPolicyID: "cached",
			wantCached:   true,
		},
		{
			name: "cache miss calls loader and caches result",
			loader: func(_ string) ([]model.Policy, error) {
				return []model.Policy{{PolicyID: "loaded"}}, nil
			},
			wantPolicyID: "loaded",
			wantCached:   true,
		},
		{
			name: "loader error returns error and does not cache",
			loader: func(_ string) ([]model.Policy, error) {
				return nil, fmt.Errorf("load failed")
			},
			wantErr:    true,
			wantCached: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewMemoryPolicyCache(10 * time.Second)
			defer c.Stop()

			if tt.preload != nil {
				c.Set("ns1", tt.preload)
			}

			got, err := c.GetOrLoad("ns1", tt.loader)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantPolicyID, got[0].PolicyID)
			}

			_, cached := c.Get("ns1")
			assert.Equal(t, tt.wantCached, cached)
		})
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
	assert.Equal(t, int32(1), callCount.Load(), "loader called more than once, singleflight not working")

	// All goroutines should get the same result.
	for i := 0; i < 10; i++ {
		assert.NoError(t, errs[i], "goroutine %d error", i)
		assert.Equal(t, "result", results[i][0].PolicyID, "goroutine %d got wrong result", i)
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
	assert.LessOrEqual(t, count, 3, "expected max 3 items")
}
