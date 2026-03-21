package memory

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryStoreCounterOps(t *testing.T) {
	s := New()
	if v, _ := s.Increment(context.Background(), "k", 2, time.Second); v != 2 {
		t.Fatalf("unexpected increment")
	}
	if v, _ := s.Get(context.Background(), "k"); v != 2 {
		t.Fatalf("unexpected get")
	}
	if ok, _ := s.CompareAndSwap(context.Background(), "k", 1, 5, 0); ok {
		t.Fatalf("cas should fail")
	}
	if ok, _ := s.CompareAndSwap(context.Background(), "k", 2, 5, 0); !ok {
		t.Fatalf("cas should pass")
	}
	_ = s.Set(context.Background(), "k2", 1, 0)
	_ = s.Delete(context.Background(), "k2")
}

func TestMemoryStoreTimestampOps(t *testing.T) {
	s := New()
	now := time.Now()
	_ = s.AddTimestamp(context.Background(), "ts", now.Add(-time.Second), time.Second)
	_ = s.AddTimestamp(context.Background(), "ts", now, time.Second)
	if c, _ := s.CountAfter(context.Background(), "ts", now.Add(-500*time.Millisecond)); c != 1 {
		t.Fatalf("unexpected count")
	}
	_ = s.TrimBefore(context.Background(), "ts", now.Add(-500*time.Millisecond))
	if c, _ := s.CountAfter(context.Background(), "ts", now.Add(-2*time.Second)); c != 1 {
		t.Fatalf("unexpected count after trim")
	}
}

func TestMemoryStoreLeaseOps(t *testing.T) {
	s := New()
	if _, _, err := s.AcquireLease(context.Background(), "l", 0, time.Second); err == nil {
		t.Fatalf("expected invalid limit error")
	}
	ok, cur, _ := s.AcquireLease(context.Background(), "l", 1, time.Second)
	if !ok || cur != 1 {
		t.Fatalf("expected first lease")
	}
	ok, _, _ = s.AcquireLease(context.Background(), "l", 1, time.Second)
	if ok {
		t.Fatalf("expected lease deny")
	}
	_ = s.ReleaseLease(context.Background(), "l")
}

func TestMemoryStoreExpiryHelpers(t *testing.T) {
	s := New()
	_ = s.Set(context.Background(), "exp", 1, 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if v, _ := s.Get(context.Background(), "exp"); v != 0 {
		t.Fatalf("expired value should reset")
	}
	_, _, _ = s.AcquireLease(context.Background(), "lexp", 1, 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	_ = s.ReleaseLease(context.Background(), "lexp")

	_ = s.AddTimestamp(context.Background(), "ts-exp", time.Now(), 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if c, _ := s.CountAfter(context.Background(), "ts-exp", time.Now().Add(-time.Hour)); c != 0 {
		t.Fatalf("expired timestamps should be cleaned")
	}
}

func TestNewShardedFallback(t *testing.T) {
	s := NewSharded(0)
	if s == nil || len(s.shards) != 1 {
		t.Fatalf("expected single shard fallback")
	}
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
	v, _ := s.Get(context.Background(), "hot-key")
	if v != goroutines*perG {
		t.Fatalf("unexpected concurrent increment value: %d", v)
	}
}

func TestMemoryStoreCheckAndAddTimestamps(t *testing.T) {
	s := New()
	now := time.Now()
	count, allowed, err := s.CheckAndAddTimestamps(context.Background(), "ts", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || !allowed || count != 0 {
		t.Fatalf("first add should succeed: count=%d allowed=%v err=%v", count, allowed, err)
	}
	count, allowed, err = s.CheckAndAddTimestamps(context.Background(), "ts", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || !allowed || count != 1 {
		t.Fatalf("second add should succeed: count=%d allowed=%v err=%v", count, allowed, err)
	}
	count, allowed, err = s.CheckAndAddTimestamps(context.Background(), "ts", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || allowed || count != 2 {
		t.Fatalf("third add should be denied: count=%d allowed=%v err=%v", count, allowed, err)
	}
}

func TestMemoryStoreBackgroundGC(t *testing.T) {
	s := NewWithGC(2 * time.Millisecond)
	defer s.Stop()

	_ = s.Set(context.Background(), "exp", 1, 1*time.Millisecond)
	_, _, _ = s.AcquireLease(context.Background(), "lexp", 1, 1*time.Millisecond)
	_ = s.AddTimestamp(context.Background(), "ts-exp", time.Now(), 1*time.Millisecond)

	time.Sleep(15 * time.Millisecond)

	if v, _ := s.Get(context.Background(), "exp"); v != 0 {
		t.Fatalf("expired value should be swept")
	}
	if c, _ := s.CountAfter(context.Background(), "ts-exp", time.Now().Add(-time.Hour)); c != 0 {
		t.Fatalf("expired timestamps should be swept")
	}
	ok, cur, _ := s.AcquireLease(context.Background(), "lexp", 1, time.Second)
	if !ok || cur != 1 {
		t.Fatalf("expired lease should be swept and re-acquirable")
	}
}
