package cache

import (
	"sync"
	"time"

	"github.com/rlaas-io/rlaas/pkg/model"
)

// PolicyCache stores recently loaded policies by namespace.
type PolicyCache interface {
	Get(namespace string) ([]model.Policy, bool)
	Set(namespace string, policies []model.Policy)
	Invalidate(namespace string)
}

// Loader is a callback used by GetOrLoad to populate a cache miss.
type Loader func(namespace string) ([]model.Policy, error)

type item struct {
	policies []model.Policy
	expiry   time.Time
	lastUsed time.Time
}

// MemoryPolicyCache is a lightweight in process cache with ttl expiration,
// max-entries cap, singleflight stampede protection, and background GC.
type MemoryPolicyCache struct {
	ttl      time.Duration
	maxItems int
	mu       sync.RWMutex
	items    map[string]item
	stop     chan struct{}

	// Singleflight: prevents thundering-herd on concurrent cache misses.
	sfMu    sync.Mutex
	sfCalls map[string]*sfCall
}

// sfCall represents an in-flight or completed singleflight call.
type sfCall struct {
	wg  sync.WaitGroup
	val []model.Policy
	err error
}

// NewMemoryPolicyCache creates a cache with default ttl when ttl is not set.
func NewMemoryPolicyCache(ttl time.Duration) *MemoryPolicyCache {
	return NewMemoryPolicyCacheWithLimits(ttl, 10000, time.Minute)
}

// NewMemoryPolicyCacheWithLimits creates a cache with configurable TTL, max
// entries, and background GC interval.
func NewMemoryPolicyCacheWithLimits(ttl time.Duration, maxItems int, gcInterval time.Duration) *MemoryPolicyCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if maxItems <= 0 {
		maxItems = 10000
	}
	if gcInterval <= 0 {
		gcInterval = time.Minute
	}
	c := &MemoryPolicyCache{
		ttl:      ttl,
		maxItems: maxItems,
		items:    map[string]item{},
		stop:     make(chan struct{}),
		sfCalls:  map[string]*sfCall{},
	}
	go c.gcLoop(gcInterval)
	return c
}

// Get returns cached policies when the entry is still fresh.
func (m *MemoryPolicyCache) Get(namespace string) ([]model.Policy, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	it, ok := m.items[namespace]
	if !ok || time.Now().After(it.expiry) {
		return nil, false
	}
	return it.policies, true
}

// Set updates cached policies for one namespace.
func (m *MemoryPolicyCache) Set(namespace string, policies []model.Policy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.items[namespace] = item{policies: policies, expiry: now.Add(m.ttl), lastUsed: now}
	// Evict oldest entries when over capacity.
	if m.maxItems > 0 && len(m.items) > m.maxItems {
		m.evictOldestLocked()
	}
}

// Invalidate clears one namespace entry.
func (m *MemoryPolicyCache) Invalidate(namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, namespace)
}

// Stop terminates the background GC goroutine.
func (m *MemoryPolicyCache) Stop() {
	if m.stop != nil {
		select {
		case <-m.stop:
		default:
			close(m.stop)
		}
	}
}

func (m *MemoryPolicyCache) gcLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.sweep()
		}
	}
}

func (m *MemoryPolicyCache) sweep() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, it := range m.items {
		if now.After(it.expiry) {
			delete(m.items, k)
		}
	}
}

func (m *MemoryPolicyCache) evictOldestLocked() {
	// Find and evict the least-recently-used entry.
	var oldestKey string
	var oldestTime time.Time
	first := true
	for k, it := range m.items {
		if first || it.lastUsed.Before(oldestTime) {
			oldestKey = k
			oldestTime = it.lastUsed
			first = false
		}
	}
	if oldestKey != "" {
		delete(m.items, oldestKey)
	}
}

// GetOrLoad returns cached policies or calls loader exactly once per namespace
// even under concurrent access (singleflight / stampede protection).
func (m *MemoryPolicyCache) GetOrLoad(namespace string, loader Loader) ([]model.Policy, error) {
	// Fast path: cache hit.
	if policies, ok := m.Get(namespace); ok {
		return policies, nil
	}

	// Singleflight: deduplicate concurrent cache misses.
	m.sfMu.Lock()
	if call, ok := m.sfCalls[namespace]; ok {
		m.sfMu.Unlock()
		call.wg.Wait()
		return call.val, call.err
	}
	call := &sfCall{}
	call.wg.Add(1)
	m.sfCalls[namespace] = call
	m.sfMu.Unlock()

	// Execute loader outside any lock.
	call.val, call.err = loader(namespace)
	if call.err == nil {
		m.Set(namespace, call.val)
	}
	call.wg.Done()

	// Clean up singleflight entry.
	m.sfMu.Lock()
	delete(m.sfCalls, namespace)
	m.sfMu.Unlock()

	return call.val, call.err
}
