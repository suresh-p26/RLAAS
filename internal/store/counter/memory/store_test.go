package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStoreCounterOps(t *testing.T) {
	s := New()
	v, err := s.Increment(context.Background(), "k", 2, time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(2), v, "unexpected increment")
	v, err = s.Get(context.Background(), "k")
	require.NoError(t, err)
	assert.Equal(t, int64(2), v, "unexpected get")
	ok, err := s.CompareAndSwap(context.Background(), "k", 1, 5, 0)
	require.NoError(t, err)
	assert.False(t, ok, "cas should fail")
	ok, err = s.CompareAndSwap(context.Background(), "k", 2, 5, 0)
	require.NoError(t, err)
	assert.True(t, ok, "cas should pass")
	_ = s.Set(context.Background(), "k2", 1, 0)
	_ = s.Delete(context.Background(), "k2")
}

func TestMemoryStoreTimestampOps(t *testing.T) {
	s := New()
	now := time.Now()
	_ = s.AddTimestamp(context.Background(), "ts", now.Add(-time.Second), time.Second)
	_ = s.AddTimestamp(context.Background(), "ts", now, time.Second)
	c, err := s.CountAfter(context.Background(), "ts", now.Add(-500*time.Millisecond))
	require.NoError(t, err)
	assert.Equal(t, int64(1), c, "unexpected count")
	_ = s.TrimBefore(context.Background(), "ts", now.Add(-500*time.Millisecond))
	c, err = s.CountAfter(context.Background(), "ts", now.Add(-2*time.Second))
	require.NoError(t, err)
	assert.Equal(t, int64(1), c, "unexpected count after trim")
}

func TestMemoryStoreLeaseOps(t *testing.T) {
	s := New()
	_, _, err := s.AcquireLease(context.Background(), "l", 0, time.Second)
	require.Error(t, err, "expected invalid limit error")
	ok, cur, err := s.AcquireLease(context.Background(), "l", 1, time.Second)
	require.NoError(t, err)
	assert.True(t, ok, "expected first lease")
	assert.Equal(t, int64(1), cur, "expected cur=1")
	ok, _, err = s.AcquireLease(context.Background(), "l", 1, time.Second)
	require.NoError(t, err)
	assert.False(t, ok, "expected lease deny")
	_ = s.ReleaseLease(context.Background(), "l")
}

func TestMemoryStoreExpiryHelpers(t *testing.T) {
	s := New()
	_ = s.Set(context.Background(), "exp", 1, 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	v, err := s.Get(context.Background(), "exp")
	require.NoError(t, err)
	assert.Equal(t, int64(0), v, "expired value should reset")
	_, _, _ = s.AcquireLease(context.Background(), "lexp", 1, 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	_ = s.ReleaseLease(context.Background(), "lexp")

	_ = s.AddTimestamp(context.Background(), "ts-exp", time.Now(), 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	c, err := s.CountAfter(context.Background(), "ts-exp", time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(0), c, "expired timestamps should be cleaned")
}

func TestNewShardedFallback(t *testing.T) {
	s := NewSharded(0)
	require.NotNil(t, s, "expected single shard fallback")
	assert.Len(t, s.shards, 1, "expected single shard")
}

func TestMemoryStoreConcurrentIncrement(t *testing.T) {
	s := NewSharded(32)
	const goroutines = 20
	const perG = 50
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				_, _ = s.Increment(context.Background(), "hot-key", 1, 0)
			}
		}()
	}
	wg.Wait()
	v, err := s.Get(context.Background(), "hot-key")
	require.NoError(t, err)
	assert.Equal(t, int64(goroutines*perG), v, "unexpected concurrent increment value")
}

func TestMemoryStoreCheckAndAddTimestamps(t *testing.T) {
	s := New()
	now := time.Now()
	count, allowed, err := s.CheckAndAddTimestamps(context.Background(), "ts", now.Add(-time.Minute), 2, 1, now, time.Minute)
	require.NoError(t, err)
	assert.True(t, allowed, "first add should be allowed")
	assert.Equal(t, int64(0), count, "first add count should be 0")
	count, allowed, err = s.CheckAndAddTimestamps(context.Background(), "ts", now.Add(-time.Minute), 2, 1, now, time.Minute)
	require.NoError(t, err)
	assert.True(t, allowed, "second add should be allowed")
	assert.Equal(t, int64(1), count, "second add count should be 1")
	count, allowed, err = s.CheckAndAddTimestamps(context.Background(), "ts", now.Add(-time.Minute), 2, 1, now, time.Minute)
	require.NoError(t, err)
	assert.False(t, allowed, "third add should be denied")
	assert.Equal(t, int64(2), count, "third add count should be 2")
}

func TestMemoryStoreBackgroundGC(t *testing.T) {
	s := NewWithGC(2 * time.Millisecond)
	defer s.Stop()

	_ = s.Set(context.Background(), "exp", 1, 1*time.Millisecond)
	_, _, _ = s.AcquireLease(context.Background(), "lexp", 1, 1*time.Millisecond)
	_ = s.AddTimestamp(context.Background(), "ts-exp", time.Now(), 1*time.Millisecond)

	time.Sleep(15 * time.Millisecond)

	v, err := s.Get(context.Background(), "exp")
	require.NoError(t, err)
	assert.Equal(t, int64(0), v, "expired value should be swept")
	c, err := s.CountAfter(context.Background(), "ts-exp", time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(0), c, "expired timestamps should be swept")
	ok, cur, err := s.AcquireLease(context.Background(), "lexp", 1, time.Second)
	require.NoError(t, err)
	assert.True(t, ok, "expired lease should be swept and re-acquirable")
	assert.Equal(t, int64(1), cur, "cur should be 1")
}
