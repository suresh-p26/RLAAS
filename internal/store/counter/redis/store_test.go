package redis

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisStoreOps(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err, "miniredis failed")
	defer mr.Close()

	s := New(mr.Addr(), "", 0)
	ctx := context.Background()

	v, err := s.Increment(ctx, "k", 1, time.Second)
	require.NoError(t, err, "increment failed")
	assert.Equal(t, int64(1), v, "increment value")

	g, err := s.Get(ctx, "k")
	require.NoError(t, err, "get failed")
	assert.Equal(t, int64(1), g, "get value")

	require.NoError(t, s.Set(ctx, "k", 3, time.Second), "set failed")

	ok, err := s.CompareAndSwap(ctx, "k", 3, 4, time.Second)
	require.NoError(t, err, "cas should pass")
	assert.True(t, ok, "cas should return ok")

	ok, err = s.CompareAndSwap(ctx, "k", 3, 5, time.Second)
	require.NoError(t, err, "cas should fail cleanly")
	assert.False(t, ok, "cas should not swap on mismatch")

	require.NoError(t, s.Delete(ctx, "k"), "delete failed")

	g0, getErr := s.Get(ctx, "missing")
	require.NoError(t, getErr, "missing key should not error")
	assert.Equal(t, int64(0), g0, "missing key should return zero")

	require.NoError(t, s.client.Set(ctx, "badint", "abc", time.Second).Err(), "setup parse failure")
	_, err = s.Get(ctx, "badint")
	require.Error(t, err, "expected parse error")

	now := time.Now()
	require.NoError(t, s.AddTimestamp(ctx, "ts", now.Add(-time.Second), time.Second), "add ts failed")
	require.NoError(t, s.AddTimestamp(ctx, "ts", now, time.Second), "add ts failed")

	c, err := s.CountAfter(ctx, "ts", now.Add(-500*time.Millisecond))
	require.NoError(t, err, "count after failed")
	assert.Equal(t, int64(1), c, "count after value")

	require.NoError(t, s.TrimBefore(ctx, "ts", now.Add(-500*time.Millisecond)), "trim failed")

	ok, cur, err := s.AcquireLease(ctx, "lease", 1, time.Second)
	require.NoError(t, err, "lease should pass")
	assert.True(t, ok, "lease should be acquired")
	assert.Equal(t, int64(1), cur, "lease current value")

	ok, _, err = s.AcquireLease(ctx, "lease", 1, time.Second)
	require.NoError(t, err, "lease should fail cleanly")
	assert.False(t, ok, "lease should not be acquired twice")

	require.NoError(t, s.ReleaseLease(ctx, "lease"), "release failed")

	assert.Equal(t, int64(2), asInt64(int64(2)), "asInt64 int64")
	assert.Equal(t, int64(3), asInt64(int(3)), "asInt64 int")
	assert.Equal(t, int64(4), asInt64("4"), "asInt64 string")
	assert.Equal(t, int64(0), asInt64(struct{}{}), "asInt64 unknown type")
}

func TestRedisStorePingAndClose(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err, "miniredis failed")
	defer mr.Close()

	s := New(mr.Addr(), "", 0)
	require.NoError(t, s.Ping(context.Background()), "ping failed")

	stats := s.PoolStats()
	assert.NotNil(t, stats, "expected non-nil pool stats")

	require.NoError(t, s.Close(), "close failed")
}

func TestRedisStoreCheckAndAddTimestamps(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err, "miniredis failed")
	defer mr.Close()

	s := New(mr.Addr(), "", 0)
	ctx := context.Background()
	now := time.Now()

	// First add — should succeed.
	count, allowed, err := s.CheckAndAddTimestamps(ctx, "ts-lua", now.Add(-time.Minute), 2, 1, now, time.Minute)
	require.NoError(t, err, "first add should not error")
	assert.True(t, allowed, "first add should be allowed")
	assert.Equal(t, int64(0), count, "first add count")

	// Second add — should succeed.
	count, allowed, err = s.CheckAndAddTimestamps(ctx, "ts-lua", now.Add(-time.Minute), 2, 1, now, time.Minute)
	require.NoError(t, err, "second add should not error")
	assert.True(t, allowed, "second add should be allowed")
	assert.Equal(t, int64(1), count, "second add count")

	// Third add — should be denied.
	count, allowed, err = s.CheckAndAddTimestamps(ctx, "ts-lua", now.Add(-time.Minute), 2, 1, now, time.Minute)
	require.NoError(t, err, "third add should not error")
	assert.False(t, allowed, "third add should be denied")
	assert.Equal(t, int64(2), count, "third add count")
}

func TestRedisStoreNewWithOptions(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err, "miniredis failed")
	defer mr.Close()

	s := NewWithOptions(Options{
		Addr:         mr.Addr(),
		PoolSize:     10,
		MinIdleConns: 2,
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		MaxRetries:   3,
	})
	require.NoError(t, s.Ping(context.Background()), "ping failed with full options")
	_ = s.Close()
}
