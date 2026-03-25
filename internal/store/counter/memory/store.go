package memory

import (
	"context"
	"errors"
	"hash/fnv"
	"sort"
	"sync"
	"time"
)

type valueItem struct {
	value  int64
	expiry time.Time
}

// MemoryStore is an in process counter store for local or development use.
type MemoryStore struct {
	shards []memoryShard
	stop   chan struct{} // nil when GC is not active
}

type memoryShard struct {
	mu         sync.Mutex
	values     map[string]valueItem
	timestamps map[string][]time.Time
	tsExpiry   map[string]time.Time
	leases     map[string]valueItem
}

// New creates an empty memory store.
func New() *MemoryStore {
	return NewSharded(64)
}

// NewSharded creates a lock-sharded memory store for higher concurrency.
func NewSharded(shardCount int) *MemoryStore {
	if shardCount <= 0 {
		shardCount = 1
	}
	shards := make([]memoryShard, shardCount)
	for i := range shards {
		shards[i] = memoryShard{
			values:     map[string]valueItem{},
			timestamps: map[string][]time.Time{},
			tsExpiry:   map[string]time.Time{},
			leases:     map[string]valueItem{},
		}
	}
	return &MemoryStore{shards: shards}
}

// Increment adds value to a key and applies ttl when provided.
func (m *MemoryStore) Increment(_ context.Context, key string, value int64, ttl time.Duration) (int64, error) {
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	it := getValueLocked(shard, key)
	it.value += value
	if ttl > 0 {
		it.expiry = time.Now().Add(ttl)
	}
	shard.values[key] = it
	return it.value, nil
}

// Get reads a counter value.
func (m *MemoryStore) Get(_ context.Context, key string) (int64, error) {
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	it := getValueLocked(shard, key)
	return it.value, nil
}

// Set writes a counter value.
func (m *MemoryStore) Set(_ context.Context, key string, value int64, ttl time.Duration) error {
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	it := valueItem{value: value}
	if ttl > 0 {
		it.expiry = time.Now().Add(ttl)
	}
	shard.values[key] = it
	return nil
}

// CompareAndSwap updates value when old value matches current value.
func (m *MemoryStore) CompareAndSwap(_ context.Context, key string, oldVal, newVal int64, ttl time.Duration) (bool, error) {
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	it := getValueLocked(shard, key)
	if it.value != oldVal {
		return false, nil
	}
	it.value = newVal
	if ttl > 0 {
		it.expiry = time.Now().Add(ttl)
	}
	shard.values[key] = it
	return true, nil
}

// Delete removes all stored data for one key.
func (m *MemoryStore) Delete(_ context.Context, key string) error {
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	delete(shard.values, key)
	delete(shard.timestamps, key)
	delete(shard.tsExpiry, key)
	delete(shard.leases, key)
	return nil
}

// AddTimestamp appends a timestamp entry used by log style algorithms.
func (m *MemoryStore) AddTimestamp(_ context.Context, key string, ts time.Time, ttl time.Duration) error {
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	arr := append(shard.timestamps[key], ts)
	sort.Slice(arr, func(i, j int) bool { return arr[i].Before(arr[j]) })
	shard.timestamps[key] = arr
	if ttl > 0 {
		shard.tsExpiry[key] = time.Now().Add(ttl)
	}
	return nil
}

// CountAfter counts timestamps newer than or equal to the provided time.
func (m *MemoryStore) CountAfter(_ context.Context, key string, after time.Time) (int64, error) {
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	cleanTSLocked(shard, key)
	var cnt int64
	for _, t := range shard.timestamps[key] {
		if !t.Before(after) {
			cnt++
		}
	}
	return cnt, nil
}

// TrimBefore removes timestamps older than the provided time.
func (m *MemoryStore) TrimBefore(_ context.Context, key string, before time.Time) error {
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	arr := shard.timestamps[key]
	j := 0
	for _, t := range arr {
		if !t.Before(before) {
			arr[j] = t
			j++
		}
	}
	shard.timestamps[key] = arr[:j]
	return nil
}

// AcquireLease reserves one concurrency slot.
func (m *MemoryStore) AcquireLease(_ context.Context, key string, limit int64, ttl time.Duration) (bool, int64, error) {
	if limit <= 0 {
		return false, 0, errors.New("limit must be positive")
	}
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	it := getLeaseLocked(shard, key)
	if it.value >= limit {
		return false, it.value, nil
	}
	it.value++
	if ttl > 0 {
		it.expiry = time.Now().Add(ttl)
	}
	shard.leases[key] = it
	return true, it.value, nil
}

// ReleaseLease frees one concurrency slot.
func (m *MemoryStore) ReleaseLease(_ context.Context, key string) error {
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	it := getLeaseLocked(shard, key)
	if it.value > 0 {
		it.value--
	}
	shard.leases[key] = it
	return nil
}

// getValueLocked reads a counter value under the shard lock, returning a
// zero item when the key is missing or expired.
func getValueLocked(shard *memoryShard, key string) valueItem {
	it := shard.values[key]
	if !it.expiry.IsZero() && time.Now().After(it.expiry) {
		delete(shard.values, key)
		return valueItem{}
	}
	return it
}

// getLeaseLocked reads a lease value under the shard lock, returning a
// zero item when the key is missing or expired.
func getLeaseLocked(shard *memoryShard, key string) valueItem {
	it := shard.leases[key]
	if !it.expiry.IsZero() && time.Now().After(it.expiry) {
		delete(shard.leases, key)
		return valueItem{}
	}
	return it
}

// cleanTSLocked removes the timestamp array when the key's TTL has expired.
func cleanTSLocked(shard *memoryShard, key string) {
	expiry := shard.tsExpiry[key]
	if expiry.IsZero() || time.Now().Before(expiry) {
		return
	}
	delete(shard.timestamps, key)
	delete(shard.tsExpiry, key)
}

// shardFor selects the shard responsible for the given key using FNV-1a hashing.
func (m *MemoryStore) shardFor(key string) *memoryShard {
	if len(m.shards) == 1 {
		return &m.shards[0]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	idx := int(h.Sum32() % uint32(len(m.shards)))
	return &m.shards[idx]
}

// NewWithGC creates a memory store with periodic background cleanup of expired
// entries. Call Stop() to terminate the GC goroutine.
func NewWithGC(gcInterval time.Duration) *MemoryStore {
	return NewShardedWithGC(64, gcInterval)
}

// NewShardedWithGC creates a lock-sharded memory store with background GC.
func NewShardedWithGC(shardCount int, gcInterval time.Duration) *MemoryStore {
	m := NewSharded(shardCount)
	if gcInterval <= 0 {
		gcInterval = time.Minute
	}
	m.stop = make(chan struct{})
	go m.gcLoop(gcInterval)
	return m
}

// Stop terminates the background GC goroutine. Safe to call on stores
// created with New() (no-op).
func (m *MemoryStore) Stop() {
	if m.stop != nil {
		select {
		case <-m.stop:
			// Already stopped.
		default:
			close(m.stop)
		}
	}
}

func (m *MemoryStore) gcLoop(interval time.Duration) {
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

// sweep iterates all shards and removes expired values, leases, and timestamps.
func (m *MemoryStore) sweep() {
	now := time.Now()
	for i := range m.shards {
		shard := &m.shards[i]
		shard.mu.Lock()
		for k, it := range shard.values {
			if !it.expiry.IsZero() && now.After(it.expiry) {
				delete(shard.values, k)
			}
		}
		for k, it := range shard.leases {
			if !it.expiry.IsZero() && now.After(it.expiry) {
				delete(shard.leases, k)
			}
		}
		for k, exp := range shard.tsExpiry {
			if now.After(exp) {
				delete(shard.timestamps, k)
				delete(shard.tsExpiry, k)
			}
		}
		shard.mu.Unlock()
	}
}

// CheckAndAddTimestamps atomically trims, counts, checks limit, and adds
// timestamps in a single shard-locked operation. This prevents TOCTOU races
// in sliding-log style algorithms.
func (m *MemoryStore) CheckAndAddTimestamps(_ context.Context, key string, cutoff time.Time, limit, cost int64, ts time.Time, ttl time.Duration) (int64, bool, error) {
	shard := m.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	// Trim expired entries.
	arr := shard.timestamps[key]
	j := 0
	for _, t := range arr {
		if !t.Before(cutoff) {
			arr[j] = t
			j++
		}
	}
	arr = arr[:j]

	count := int64(len(arr))

	// Check limit.
	if count+cost > limit {
		shard.timestamps[key] = arr
		return count, false, nil
	}

	// Add cost entries.
	for i := int64(0); i < cost; i++ {
		arr = append(arr, ts)
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].Before(arr[j]) })
	shard.timestamps[key] = arr
	if ttl > 0 {
		shard.tsExpiry[key] = time.Now().Add(ttl)
	}
	return count, true, nil
}

// Ping always returns nil for the in-memory store (no network backend).
func (m *MemoryStore) Ping(_ context.Context) error { return nil }

// Close stops the GC goroutine and releases resources.
func (m *MemoryStore) Close() error { m.Stop(); return nil }
