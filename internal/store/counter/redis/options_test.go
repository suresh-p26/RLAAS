package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func TestApplyPoolOptions_AllFields(t *testing.T) {
	ropts := &goredis.Options{}
	copts := &goredis.ClusterOptions{}
	opts := Options{
		PoolSize:     64,
		MinIdleConns: 8,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 4 * time.Second,
		MaxRetries:   9,
	}

	applyPoolOptions(ropts, copts, opts)

	if ropts.PoolSize != 64 || ropts.MinIdleConns != 8 || ropts.DialTimeout != 2*time.Second || ropts.ReadTimeout != 3*time.Second || ropts.WriteTimeout != 4*time.Second || ropts.MaxRetries != 9 {
		t.Fatalf("single-node options not applied: %+v", ropts)
	}
	if copts.PoolSize != 64 || copts.MinIdleConns != 8 || copts.DialTimeout != 2*time.Second || copts.ReadTimeout != 3*time.Second || copts.WriteTimeout != 4*time.Second || copts.MaxRetries != 9 {
		t.Fatalf("cluster options not applied: %+v", copts)
	}
}

func TestApplyPoolOptionsFO_AllFields(t *testing.T) {
	fopts := &goredis.FailoverOptions{}
	applyPoolOptionsFO(fopts, Options{
		PoolSize:     32,
		MinIdleConns: 4,
		DialTimeout:  time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 3 * time.Second,
		MaxRetries:   7,
	})

	if fopts.PoolSize != 32 || fopts.MinIdleConns != 4 || fopts.DialTimeout != time.Second || fopts.ReadTimeout != 2*time.Second || fopts.WriteTimeout != 3*time.Second || fopts.MaxRetries != 7 {
		t.Fatalf("failover options not applied: %+v", fopts)
	}
}

func TestNewWithOptions_ClusterAndSentinel(t *testing.T) {
	clusterStore := NewWithOptions(Options{ClusterAddrs: []string{"127.0.0.1:6379"}})
	if clusterStore == nil || clusterStore.client == nil {
		t.Fatal("expected cluster client")
	}
	_ = clusterStore.Close()

	sentinelStore := NewWithOptions(Options{SentinelAddrs: []string{"127.0.0.1:26379"}, SentinelMaster: "mymaster"})
	if sentinelStore == nil || sentinelStore.client == nil {
		t.Fatal("expected sentinel client")
	}
	_ = sentinelStore.Close()
}

func TestNewWithOptions_CircuitBreakerDefaults(t *testing.T) {
	s := NewWithOptions(Options{
		Addr:               "127.0.0.1:6379",
		CBEnabled:          true,
		CBOpenThreshold:    0,
		CBHalfOpenAfter:    0,
		CBHalfOpenMaxProbe: 0,
	})
	if s.cb == nil {
		t.Fatal("expected circuit breaker enabled")
	}
	if s.cb.threshold != 5 || s.cb.halfOpenAfter != 10*time.Second || s.cb.maxProbe != 2 {
		t.Fatalf("unexpected cb defaults: threshold=%d halfOpenAfter=%v maxProbe=%d", s.cb.threshold, s.cb.halfOpenAfter, s.cb.maxProbe)
	}
	_ = s.Close()
}

func TestStoreDo_CircuitOpenAndFailureSuccessPaths(t *testing.T) {
	cb := NewCircuitBreaker(1, time.Hour, 1)
	cb.state.Store(int32(CBOpen))
	cb.openedAt.Store(time.Now().UnixNano())

	s := &Store{cb: cb}
	if err := s.do(func() error { return nil }); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}

	cb2 := NewCircuitBreaker(2, time.Second, 1)
	s2 := &Store{cb: cb2}
	if err := s2.do(func() error { return errors.New("boom") }); err == nil {
		t.Fatal("expected function error")
	}
	if cb2.failures.Load() != 1 {
		t.Fatalf("expected failure count increment, got %d", cb2.failures.Load())
	}

	if err := s2.do(func() error { return nil }); err != nil {
		t.Fatalf("unexpected success error: %v", err)
	}
}

func TestStoreDo_NoCircuitBreakerPassThrough(t *testing.T) {
	s := &Store{}
	called := false
	err := s.do(func() error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("expected passthrough call, err=%v called=%v", err, called)
	}
}

func TestStoreCloseAndPing_WithBrokenClient(t *testing.T) {
	s := NewWithOptions(Options{Addr: "127.0.0.1:0", DialTimeout: 20 * time.Millisecond})
	defer s.Close()

	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("expected ping error for invalid endpoint")
	}
}
