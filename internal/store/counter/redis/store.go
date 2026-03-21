package redis

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// ------------------------------------------------
// Unified Redis client interface
// ------------------------------------------------

// cmdable is the subset of go-redis methods used by the store.
// Both *goredis.Client, *goredis.ClusterClient, and *goredis.FailoverClient
// satisfy this interface via goredis.Cmdable.
type cmdable interface {
	goredis.Cmdable
	Close() error
}

// Store implements CounterStore using Redis commands and Lua scripts.
// It supports single-node, Redis Cluster, and Redis Sentinel topologies.
type Store struct {
	client cmdable
	cb     *CircuitBreaker
}

// Options exposes Redis connection tunables for production deployments.
type Options struct {
	Addr         string
	Password     string
	DB           int
	PoolSize     int
	MinIdleConns int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxRetries   int
	TLSEnabled   bool

	// Cluster mode
	ClusterAddrs []string

	// Sentinel mode
	SentinelAddrs  []string
	SentinelMaster string

	// Circuit breaker
	CBEnabled          bool
	CBOpenThreshold    int64         // errors before opening (default 5)
	CBHalfOpenAfter    time.Duration // time before half-open probe (default 10s)
	CBHalfOpenMaxProbe int64         // probes in half-open before close (default 2)
}

// New creates a Redis backed counter store with minimal defaults.
func New(addr, password string, db int) *Store {
	return NewWithOptions(Options{Addr: addr, Password: password, DB: db})
}

// NewWithOptions creates a Redis store with full connection pool configuration.
// Automatically selects single-node, cluster, or sentinel topology.
func NewWithOptions(opts Options) *Store {
	var client cmdable

	switch {
	case len(opts.ClusterAddrs) > 0:
		copts := &goredis.ClusterOptions{
			Addrs:    opts.ClusterAddrs,
			Password: opts.Password,
		}
		applyPoolOptions(nil, copts, opts)
		client = goredis.NewClusterClient(copts)

	case len(opts.SentinelAddrs) > 0 && opts.SentinelMaster != "":
		fopts := &goredis.FailoverOptions{
			MasterName:    opts.SentinelMaster,
			SentinelAddrs: opts.SentinelAddrs,
			Password:      opts.Password,
			DB:            opts.DB,
		}
		applyPoolOptionsFO(fopts, opts)
		client = goredis.NewFailoverClient(fopts)

	default:
		ropts := &goredis.Options{
			Addr:     opts.Addr,
			Password: opts.Password,
			DB:       opts.DB,
		}
		applyPoolOptions(ropts, nil, opts)
		client = goredis.NewClient(ropts)
	}

	s := &Store{client: client}
	if opts.CBEnabled {
		threshold := opts.CBOpenThreshold
		if threshold <= 0 {
			threshold = 5
		}
		halfOpen := opts.CBHalfOpenAfter
		if halfOpen <= 0 {
			halfOpen = 10 * time.Second
		}
		maxProbe := opts.CBHalfOpenMaxProbe
		if maxProbe <= 0 {
			maxProbe = 2
		}
		s.cb = NewCircuitBreaker(threshold, halfOpen, maxProbe)
	}
	return s
}

func applyPoolOptions(ropts *goredis.Options, copts *goredis.ClusterOptions, opts Options) {
	if ropts != nil {
		if opts.PoolSize > 0 {
			ropts.PoolSize = opts.PoolSize
		}
		if opts.MinIdleConns > 0 {
			ropts.MinIdleConns = opts.MinIdleConns
		}
		if opts.DialTimeout > 0 {
			ropts.DialTimeout = opts.DialTimeout
		}
		if opts.ReadTimeout > 0 {
			ropts.ReadTimeout = opts.ReadTimeout
		}
		if opts.WriteTimeout > 0 {
			ropts.WriteTimeout = opts.WriteTimeout
		}
		if opts.MaxRetries > 0 {
			ropts.MaxRetries = opts.MaxRetries
		}
	}
	if copts != nil {
		if opts.PoolSize > 0 {
			copts.PoolSize = opts.PoolSize
		}
		if opts.MinIdleConns > 0 {
			copts.MinIdleConns = opts.MinIdleConns
		}
		if opts.DialTimeout > 0 {
			copts.DialTimeout = opts.DialTimeout
		}
		if opts.ReadTimeout > 0 {
			copts.ReadTimeout = opts.ReadTimeout
		}
		if opts.WriteTimeout > 0 {
			copts.WriteTimeout = opts.WriteTimeout
		}
		if opts.MaxRetries > 0 {
			copts.MaxRetries = opts.MaxRetries
		}
	}
}

func applyPoolOptionsFO(fopts *goredis.FailoverOptions, opts Options) {
	if opts.PoolSize > 0 {
		fopts.PoolSize = opts.PoolSize
	}
	if opts.MinIdleConns > 0 {
		fopts.MinIdleConns = opts.MinIdleConns
	}
	if opts.DialTimeout > 0 {
		fopts.DialTimeout = opts.DialTimeout
	}
	if opts.ReadTimeout > 0 {
		fopts.ReadTimeout = opts.ReadTimeout
	}
	if opts.WriteTimeout > 0 {
		fopts.WriteTimeout = opts.WriteTimeout
	}
	if opts.MaxRetries > 0 {
		fopts.MaxRetries = opts.MaxRetries
	}
}

// ------------------------------------------------
// Circuit Breaker
// ------------------------------------------------

// CBState represents the circuit breaker state.
type CBState int32

const (
	CBClosed   CBState = 0
	CBOpen     CBState = 1
	CBHalfOpen CBState = 2
)

// CircuitBreaker protects the Redis store from cascading failures.
type CircuitBreaker struct {
	state         atomic.Int32
	failures      atomic.Int64
	threshold     int64
	halfOpenAfter time.Duration
	maxProbe      int64
	probeCount    atomic.Int64
	openedAt      atomic.Int64 // unix nano
	mu            sync.Mutex
}

// NewCircuitBreaker creates a circuit breaker with the given thresholds.
func NewCircuitBreaker(threshold int64, halfOpenAfter time.Duration, maxProbe int64) *CircuitBreaker {
	return &CircuitBreaker{
		threshold:     threshold,
		halfOpenAfter: halfOpenAfter,
		maxProbe:      maxProbe,
	}
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() CBState {
	return CBState(cb.state.Load())
}

// Allow returns true if the request should be attempted through the breaker.
func (cb *CircuitBreaker) Allow() bool {
	switch CBState(cb.state.Load()) {
	case CBClosed:
		return true
	case CBOpen:
		if time.Now().UnixNano()-cb.openedAt.Load() > cb.halfOpenAfter.Nanoseconds() {
			cb.mu.Lock()
			if CBState(cb.state.Load()) == CBOpen {
				cb.state.Store(int32(CBHalfOpen))
				cb.probeCount.Store(0)
			}
			cb.mu.Unlock()
			return CBState(cb.state.Load()) == CBHalfOpen
		}
		return false
	case CBHalfOpen:
		return cb.probeCount.Load() < cb.maxProbe
	}
	return true
}

// RecordSuccess records a successful operation.
func (cb *CircuitBreaker) RecordSuccess() {
	switch CBState(cb.state.Load()) {
	case CBHalfOpen:
		n := cb.probeCount.Add(1)
		if n >= cb.maxProbe {
			cb.mu.Lock()
			cb.state.Store(int32(CBClosed))
			cb.failures.Store(0)
			cb.probeCount.Store(0)
			cb.mu.Unlock()
		}
	case CBClosed:
		cb.failures.Store(0)
	}
}

// RecordFailure records a failed operation.
func (cb *CircuitBreaker) RecordFailure() {
	switch CBState(cb.state.Load()) {
	case CBClosed:
		n := cb.failures.Add(1)
		if n >= cb.threshold {
			cb.mu.Lock()
			if CBState(cb.state.Load()) == CBClosed {
				cb.state.Store(int32(CBOpen))
				cb.openedAt.Store(time.Now().UnixNano())
			}
			cb.mu.Unlock()
		}
	case CBHalfOpen:
		cb.mu.Lock()
		cb.state.Store(int32(CBOpen))
		cb.openedAt.Store(time.Now().UnixNano())
		cb.probeCount.Store(0)
		cb.mu.Unlock()
	}
}

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = fmt.Errorf("circuit breaker is open")

// do wraps an operation with circuit breaker protection if enabled.
func (s *Store) do(fn func() error) error {
	if s.cb == nil {
		return fn()
	}
	if !s.cb.Allow() {
		return ErrCircuitOpen
	}
	err := fn()
	if err != nil {
		s.cb.RecordFailure()
	} else {
		s.cb.RecordSuccess()
	}
	return err
}

// incrScript atomically increments and sets TTL on first creation.
var incrScript = goredis.NewScript(`
local val = redis.call('INCRBY', KEYS[1], tonumber(ARGV[1]))
local ttl = tonumber(ARGV[2])
if ttl > 0 then
  if val == tonumber(ARGV[1]) then
    redis.call('PEXPIRE', KEYS[1], ttl)
  end
end
return val
`)

// Increment atomically adds value to a key and sets TTL on first creation.
func (s *Store) Increment(ctx context.Context, key string, value int64, ttl time.Duration) (int64, error) {
	ms := int64(0)
	if ttl > 0 {
		ms = ttl.Milliseconds()
	}
	var res int64
	err := s.do(func() error {
		var e error
		res, e = incrScript.Run(ctx, s.client, []string{key}, value, ms).Int64()
		return e
	})
	return res, err
}

// Get returns key value or zero when key does not exist.
func (s *Store) Get(ctx context.Context, key string) (int64, error) {
	var out int64
	err := s.do(func() error {
		val, err := s.client.Get(ctx, key).Result()
		if err == goredis.Nil {
			out = 0
			return nil
		}
		if err != nil {
			return err
		}
		out, err = strconv.ParseInt(val, 10, 64)
		return err
	})
	return out, err
}

// Set writes key value with ttl.
func (s *Store) Set(ctx context.Context, key string, value int64, ttl time.Duration) error {
	return s.do(func() error {
		return s.client.Set(ctx, key, value, ttl).Err()
	})
}

// casScript performs an atomic compare-and-swap via Lua (cluster-safe).
var casScript = goredis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if cur == tonumber(ARGV[1]) then
  local ttl = tonumber(ARGV[3])
  if ttl > 0 then
    redis.call('SET', KEYS[1], ARGV[2], 'PX', ttl)
  else
    redis.call('SET', KEYS[1], ARGV[2])
  end
  return 1
end
return 0
`)

// CompareAndSwap performs atomic compare-and-swap via Lua (cluster-safe).
func (s *Store) CompareAndSwap(ctx context.Context, key string, oldVal, newVal int64, ttl time.Duration) (bool, error) {
	ms := int64(0)
	if ttl > 0 {
		ms = ttl.Milliseconds()
	}
	var ok bool
	err := s.do(func() error {
		res, e := casScript.Run(ctx, s.client, []string{key}, oldVal, newVal, ms).Int64()
		if e != nil {
			return e
		}
		ok = res == 1
		return nil
	})
	return ok, err
}

// Delete removes a key.
func (s *Store) Delete(ctx context.Context, key string) error {
	return s.do(func() error {
		return s.client.Del(ctx, key).Err()
	})
}

// addTSScript atomically adds a timestamp to a sorted set and sets TTL.
var addTSScript = goredis.NewScript(`
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[1])
local ttl = tonumber(ARGV[2])
if ttl > 0 then
  redis.call('PEXPIRE', KEYS[1], ttl)
end
return 1
`)

// AddTimestamp writes one timestamp into a sorted set key atomically.
func (s *Store) AddTimestamp(ctx context.Context, key string, ts time.Time, ttl time.Duration) error {
	ms := int64(0)
	if ttl > 0 {
		ms = ttl.Milliseconds()
	}
	return s.do(func() error {
		_, err := addTSScript.Run(ctx, s.client, []string{key}, ts.UnixNano(), ms).Result()
		return err
	})
}

// CountAfter returns sorted set member count after the given time.
func (s *Store) CountAfter(ctx context.Context, key string, after time.Time) (int64, error) {
	var res int64
	err := s.do(func() error {
		var e error
		res, e = s.client.ZCount(ctx, key, fmt.Sprintf("%d", after.UnixNano()), "+inf").Result()
		return e
	})
	return res, err
}

// TrimBefore removes sorted set members older than the given time.
func (s *Store) TrimBefore(ctx context.Context, key string, before time.Time) error {
	return s.do(func() error {
		return s.client.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", before.UnixNano())).Err()
	})
}

// checkAndAddTSScript atomically trims, counts, checks limit, and adds
// timestamps. Returns {count, 1} on success, {count, 0} on over-limit.
var checkAndAddTSScript = goredis.NewScript(`
local cutoff = ARGV[1]
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', cutoff)
local count = redis.call('ZCOUNT', KEYS[1], cutoff, '+inf')
local limit = tonumber(ARGV[2])
local cost  = tonumber(ARGV[3])
if count + cost > limit then
  return {count, 0}
end
local ts  = ARGV[4]
local ttl = tonumber(ARGV[5])
for i = 1, cost do
  redis.call('ZADD', KEYS[1], ts, ts .. ':' .. tostring(i) .. ':' .. tostring(math.random(1000000)))
end
if ttl > 0 then
  redis.call('PEXPIRE', KEYS[1], ttl)
end
return {count, 1}
`)

// CheckAndAddTimestamps atomically trims, counts, checks, and adds timestamps.
// This prevents TOCTOU races in sliding-log style algorithms.
func (s *Store) CheckAndAddTimestamps(ctx context.Context, key string, cutoff time.Time, limit, cost int64, ts time.Time, ttl time.Duration) (int64, bool, error) {
	ms := int64(0)
	if ttl > 0 {
		ms = ttl.Milliseconds()
	}
	var count int64
	var allowed bool
	err := s.do(func() error {
		res, e := checkAndAddTSScript.Run(ctx, s.client, []string{key}, cutoff.UnixNano(), limit, cost, ts.UnixNano(), ms).Result()
		if e != nil {
			return e
		}
		arr, ok := res.([]interface{})
		if !ok || len(arr) != 2 {
			return fmt.Errorf("unexpected lua response")
		}
		count = asInt64(arr[0])
		allowed = asInt64(arr[1]) == 1
		return nil
	})
	return count, allowed, err
}

// acquireLeaseScript atomically enforces concurrency limit and extends TTL.
var acquireLeaseScript = goredis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if current > tonumber(ARGV[1]) then
  redis.call('DECR', KEYS[1])
  return {0, current - 1}
end
local ttl = tonumber(ARGV[2])
if ttl > 0 then
  redis.call('PEXPIRE', KEYS[1], ttl)
end
return {1, current}
`)

// AcquireLease uses Lua to atomically enforce a concurrency limit.
// TTL is extended on every successful acquire to prevent premature expiry.
func (s *Store) AcquireLease(ctx context.Context, key string, limit int64, ttl time.Duration) (bool, int64, error) {
	ms := ttl.Milliseconds()
	var okVal, curVal int64
	err := s.do(func() error {
		res, e := acquireLeaseScript.Run(ctx, s.client, []string{key}, limit, ms).Result()
		if e != nil {
			return e
		}
		arr, ok := res.([]interface{})
		if !ok || len(arr) != 2 {
			return fmt.Errorf("unexpected lua response")
		}
		okVal = asInt64(arr[0])
		curVal = asInt64(arr[1])
		return nil
	})
	return okVal == 1, curVal, err
}

// ReleaseLease decrements active lease count safely.
func (s *Store) ReleaseLease(ctx context.Context, key string) error {
	script := goredis.NewScript(`
local current = redis.call('GET', KEYS[1])
if not current then
  return 0
end
current = tonumber(current)
if current <= 0 then
  redis.call('SET', KEYS[1], 0)
  return 0
end
return redis.call('DECR', KEYS[1])
`)
	return s.do(func() error {
		_, err := script.Run(ctx, s.client, []string{key}).Result()
		return err
	})
}

// Ping checks Redis connectivity.  Call during startup or from health-check
// endpoints to confirm the counter backend is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

// PoolStats exposes underlying connection pool metrics for operational
// dashboards and alerting (idle connections, stale connections, etc.).
// Returns nil when the client type does not expose pool stats.
func (s *Store) PoolStats() *goredis.PoolStats {
	type poolStatter interface {
		PoolStats() *goredis.PoolStats
	}
	if ps, ok := s.client.(poolStatter); ok {
		return ps.PoolStats()
	}
	return nil
}

// Close gracefully shuts down the Redis client and releases the connection pool.
func (s *Store) Close() error {
	return s.client.Close()
}

// asInt64 converts Lua response values into int64.
func asInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case string:
		i, _ := strconv.ParseInt(t, 10, 64)
		return i
	default:
		return 0
	}
}
