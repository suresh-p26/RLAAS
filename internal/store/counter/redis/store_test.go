package redis

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func TestRedisStoreOps(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis failed: %v", err)
	}
	defer mr.Close()

	s := New(mr.Addr(), "", 0)
	ctx := context.Background()

	v, err := s.Increment(ctx, "k", 1, time.Second)
	if err != nil || v != 1 {
		t.Fatalf("increment failed")
	}
	g, err := s.Get(ctx, "k")
	if err != nil || g != 1 {
		t.Fatalf("get failed")
	}
	if err := s.Set(ctx, "k", 3, time.Second); err != nil {
		t.Fatalf("set failed")
	}
	ok, err := s.CompareAndSwap(ctx, "k", 3, 4, time.Second)
	if err != nil || !ok {
		t.Fatalf("cas should pass")
	}
	ok, err = s.CompareAndSwap(ctx, "k", 3, 5, time.Second)
	if err != nil || ok {
		t.Fatalf("cas should fail")
	}
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatalf("delete failed")
	}
	if g0, err := s.Get(ctx, "missing"); err != nil || g0 != 0 {
		t.Fatalf("missing key should return zero")
	}
	if err := s.client.Set(ctx, "badint", "abc", time.Second).Err(); err != nil {
		t.Fatalf("setup parse failure failed")
	}
	if _, err := s.Get(ctx, "badint"); err == nil {
		t.Fatalf("expected parse error")
	}

	now := time.Now()
	if err := s.AddTimestamp(ctx, "ts", now.Add(-time.Second), time.Second); err != nil {
		t.Fatalf("add ts failed")
	}
	if err := s.AddTimestamp(ctx, "ts", now, time.Second); err != nil {
		t.Fatalf("add ts failed")
	}
	if c, err := s.CountAfter(ctx, "ts", now.Add(-500*time.Millisecond)); err != nil || c != 1 {
		t.Fatalf("count after failed")
	}
	if err := s.TrimBefore(ctx, "ts", now.Add(-500*time.Millisecond)); err != nil {
		t.Fatalf("trim failed")
	}

	ok, cur, err := s.AcquireLease(ctx, "lease", 1, time.Second)
	if err != nil || !ok || cur != 1 {
		t.Fatalf("lease should pass")
	}
	ok, _, err = s.AcquireLease(ctx, "lease", 1, time.Second)
	if err != nil || ok {
		t.Fatalf("lease should fail")
	}
	if err := s.ReleaseLease(ctx, "lease"); err != nil {
		t.Fatalf("release failed")
	}

	if asInt64(int64(2)) != 2 || asInt64(int(3)) != 3 || asInt64("4") != 4 || asInt64(struct{}{}) != 0 {
		t.Fatalf("asInt64 conversion failed")
	}
}

func TestRedisStorePingAndClose(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis failed: %v", err)
	}
	defer mr.Close()

	s := New(mr.Addr(), "", 0)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	stats := s.PoolStats()
	if stats == nil {
		t.Fatalf("expected non-nil pool stats")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestRedisStoreCheckAndAddTimestamps(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis failed: %v", err)
	}
	defer mr.Close()

	s := New(mr.Addr(), "", 0)
	ctx := context.Background()
	now := time.Now()

	// First add — should succeed.
	count, allowed, err := s.CheckAndAddTimestamps(ctx, "ts-lua", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || !allowed || count != 0 {
		t.Fatalf("first add should succeed: count=%d allowed=%v err=%v", count, allowed, err)
	}

	// Second add — should succeed.
	count, allowed, err = s.CheckAndAddTimestamps(ctx, "ts-lua", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || !allowed || count != 1 {
		t.Fatalf("second add should succeed: count=%d allowed=%v err=%v", count, allowed, err)
	}

	// Third add — should be denied.
	count, allowed, err = s.CheckAndAddTimestamps(ctx, "ts-lua", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || allowed || count != 2 {
		t.Fatalf("third add should deny: count=%d allowed=%v err=%v", count, allowed, err)
	}
}

func TestRedisStoreNewWithOptions(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis failed: %v", err)
	}
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
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("ping failed with full options: %v", err)
	}
	_ = s.Close()
}
