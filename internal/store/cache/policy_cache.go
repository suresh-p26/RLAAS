package cache

import (
	"github.com/rlaas-io/rlaas/pkg/model"
	"sync"
	"time"
)

// PolicyCache stores recently loaded policies by namespace.
type PolicyCache interface {
	Get(namespace string) ([]model.Policy, bool)
	Set(namespace string, policies []model.Policy)
	Invalidate(namespace string)
}

type item struct {
	policies []model.Policy
	expiry   time.Time
}

// MemoryPolicyCache is a lightweight in process cache with ttl expiration.
type MemoryPolicyCache struct {
	ttl   time.Duration
	mu    sync.RWMutex
	items map[string]item
}

// NewMemoryPolicyCache creates a cache with default ttl when ttl is not set.
func NewMemoryPolicyCache(ttl time.Duration) *MemoryPolicyCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &MemoryPolicyCache{ttl: ttl, items: map[string]item{}}
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
	m.items[namespace] = item{policies: policies, expiry: time.Now().Add(m.ttl)}
}

// Invalidate clears one namespace entry.
func (m *MemoryPolicyCache) Invalidate(namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, namespace)
}
