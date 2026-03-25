// Package postgres implements a production-ready CounterStore backed by
// PostgreSQL. It uses row-level locking and advisory locks to guarantee
// atomicity for every operation big companies rely on (CAS, leases, timestamp
// sorted sets). Compatible with any database/sql driver (pgx, lib/pq, etc.).
//
// Required schema (run Migrate to auto-create):
//
//	rlaas_counters  (key TEXT PK, value BIGINT, expires_at TIMESTAMPTZ)
//	rlaas_timestamps(key TEXT, ts TIMESTAMPTZ, expires_at TIMESTAMPTZ)
//	rlaas_leases    (key TEXT PK, value BIGINT, expires_at TIMESTAMPTZ)
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store implements store.CounterStore over PostgreSQL.
type Store struct {
	db *sql.DB
}

// New creates a Postgres counter store. The caller must supply an already-opened
// *sql.DB configured with the appropriate driver and connection pool settings
// (e.g. SetMaxOpenConns, SetConnMaxLifetime).
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// NewFromDSN is a convenience constructor that opens a connection pool.
// driverName is typically "pgx" or "postgres".
func NewFromDSN(driverName, dsn string) (*Store, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres counter store: %w", err)
	}
	return &Store{db: db}, nil
}

// Migrate creates tables and indices if they do not exist.
func (s *Store) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS rlaas_counters (
			key        TEXT PRIMARY KEY,
			value      BIGINT    NOT NULL DEFAULT 0,
			expires_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS rlaas_timestamps (
			key TEXT        NOT NULL,
			ts  TIMESTAMPTZ NOT NULL,
			expires_at TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rlaas_ts_key_ts ON rlaas_timestamps (key, ts)`,
		`CREATE TABLE IF NOT EXISTS rlaas_leases (
			key        TEXT PRIMARY KEY,
			value      BIGINT    NOT NULL DEFAULT 0,
			expires_at TIMESTAMPTZ
		)`,
	}
	for _, ddl := range stmts {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close releases the connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// expiresAt returns the expiry time given a TTL, or nil for no-expiry.
func expiresAt(ttl time.Duration) *time.Time {
	if ttl <= 0 {
		return nil
	}
	t := time.Now().Add(ttl)
	return &t
}

// Increment atomically adds value to a key and applies TTL.
func (s *Store) Increment(ctx context.Context, key string, value int64, ttl time.Duration) (int64, error) {
	exp := expiresAt(ttl)
	var result int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO rlaas_counters (key, value, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE
			SET value = CASE
				WHEN rlaas_counters.expires_at IS NOT NULL AND rlaas_counters.expires_at < NOW()
				THEN $2
				ELSE rlaas_counters.value + $2
			END,
			expires_at = $3
		RETURNING value
	`, key, value, exp).Scan(&result)
	if err != nil {
		return 0, fmt.Errorf("increment %q: %w", key, err)
	}
	return result, nil
}

// Get reads a counter value. Returns 0 for missing or expired keys.
func (s *Store) Get(ctx context.Context, key string) (int64, error) {
	var val int64
	err := s.db.QueryRowContext(ctx, `
		SELECT value FROM rlaas_counters
		WHERE key = $1
		  AND (expires_at IS NULL OR expires_at >= NOW())
	`, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get %q: %w", key, err)
	}
	return val, nil
}

// Set writes a counter value with TTL.
func (s *Store) Set(ctx context.Context, key string, value int64, ttl time.Duration) error {
	exp := expiresAt(ttl)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rlaas_counters (key, value, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = $2, expires_at = $3
	`, key, value, exp)
	if err != nil {
		return fmt.Errorf("set %q: %w", key, err)
	}
	return nil
}

// CompareAndSwap atomically updates value only if current equals oldVal.
// Uses SELECT … FOR UPDATE to guarantee serialization.
func (s *Store) CompareAndSwap(ctx context.Context, key string, oldVal, newVal int64, ttl time.Duration) (bool, error) {
	exp := expiresAt(ttl)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("cas begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var cur int64
	err = tx.QueryRowContext(ctx, `
		SELECT value FROM rlaas_counters
		WHERE key = $1
		  AND (expires_at IS NULL OR expires_at >= NOW())
		FOR UPDATE
	`, key).Scan(&cur)

	if errors.Is(err, sql.ErrNoRows) {
		if oldVal != 0 {
			return false, nil
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO rlaas_counters (key, value, expires_at) VALUES ($1, $2, $3)
			ON CONFLICT (key) DO UPDATE SET value = $2, expires_at = $3
		`, key, newVal, exp)
		if err != nil {
			return false, fmt.Errorf("cas insert: %w", err)
		}
		return tx.Commit() == nil, nil
	}
	if err != nil {
		return false, fmt.Errorf("cas select: %w", err)
	}

	if cur != oldVal {
		return false, nil
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE rlaas_counters SET value = $1, expires_at = $2 WHERE key = $3
	`, newVal, exp, key)
	if err != nil {
		return false, fmt.Errorf("cas update: %w", err)
	}
	return tx.Commit() == nil, nil
}

// Delete removes all data for one key across all tables.
func (s *Store) Delete(ctx context.Context, key string) error {
	for _, tbl := range []string{"rlaas_counters", "rlaas_timestamps", "rlaas_leases"} {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE key = $1`, tbl), key); err != nil {
			return fmt.Errorf("delete %q from %s: %w", key, tbl, err)
		}
	}
	return nil
}

// AddTimestamp appends a timestamp entry used by log-style algorithms.
func (s *Store) AddTimestamp(ctx context.Context, key string, ts time.Time, ttl time.Duration) error {
	exp := expiresAt(ttl)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rlaas_timestamps (key, ts, expires_at) VALUES ($1, $2, $3)
	`, key, ts, exp)
	if err != nil {
		return fmt.Errorf("add timestamp %q: %w", key, err)
	}
	return nil
}

// CountAfter returns timestamp count newer than or equal to after.
func (s *Store) CountAfter(ctx context.Context, key string, after time.Time) (int64, error) {
	var cnt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM rlaas_timestamps
		WHERE key = $1 AND ts >= $2
		  AND (expires_at IS NULL OR expires_at >= NOW())
	`, key, after).Scan(&cnt)
	if err != nil {
		return 0, fmt.Errorf("count after %q: %w", key, err)
	}
	return cnt, nil
}

// TrimBefore removes timestamps older than the given time.
func (s *Store) TrimBefore(ctx context.Context, key string, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM rlaas_timestamps WHERE key = $1 AND ts < $2
	`, key, before)
	if err != nil {
		return fmt.Errorf("trim before %q: %w", key, err)
	}
	return nil
}

// CheckAndAddTimestamps atomically trims, counts, checks limit, and adds
// timestamps in a single transaction. Prevents TOCTOU races.
func (s *Store) CheckAndAddTimestamps(ctx context.Context, key string, cutoff time.Time, limit, cost int64, ts time.Time, ttl time.Duration) (int64, bool, error) {
	exp := expiresAt(ttl)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("check-and-add begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Advisory lock scoped to this key to serialize concurrent callers.
	_, _ = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key)

	// Trim expired entries.
	_, _ = tx.ExecContext(ctx, `
		DELETE FROM rlaas_timestamps WHERE key = $1 AND ts < $2
	`, key, cutoff)

	// Count remaining.
	var count int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM rlaas_timestamps WHERE key = $1 AND ts >= $2
	`, key, cutoff).Scan(&count); err != nil {
		return 0, false, fmt.Errorf("check-and-add count: %w", err)
	}

	if count+cost > limit {
		_ = tx.Commit()
		return count, false, nil
	}

	// Add cost entries.
	for i := int64(0); i < cost; i++ {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO rlaas_timestamps (key, ts, expires_at) VALUES ($1, $2, $3)
		`, key, ts, exp); err != nil {
			return 0, false, fmt.Errorf("check-and-add insert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return count, true, nil
}

// AcquireLease reserves one concurrency slot. Uses SELECT … FOR UPDATE.
func (s *Store) AcquireLease(ctx context.Context, key string, limit int64, ttl time.Duration) (bool, int64, error) {
	if limit <= 0 {
		return false, 0, errors.New("limit must be positive")
	}
	exp := expiresAt(ttl)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("acquire lease begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var cur int64
	err = tx.QueryRowContext(ctx, `
		SELECT value FROM rlaas_leases
		WHERE key = $1
		  AND (expires_at IS NULL OR expires_at >= NOW())
		FOR UPDATE
	`, key).Scan(&cur)

	if errors.Is(err, sql.ErrNoRows) {
		cur = 0
	} else if err != nil {
		return false, 0, fmt.Errorf("acquire lease select: %w", err)
	}

	if cur >= limit {
		return false, cur, nil
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO rlaas_leases (key, value, expires_at) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = $2, expires_at = $3
	`, key, cur+1, exp)
	if err != nil {
		return false, 0, fmt.Errorf("acquire lease upsert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return true, cur + 1, nil
}

// ReleaseLease frees one concurrency slot.
func (s *Store) ReleaseLease(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE rlaas_leases SET value = GREATEST(value - 1, 0)
		WHERE key = $1
	`, key)
	if err != nil {
		return fmt.Errorf("release lease %q: %w", key, err)
	}
	return nil
}

// PurgeExpired removes expired rows from all tables. Call periodically from
// a background job (e.g. every minute) to keep table sizes bounded.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	var total int64
	for _, tbl := range []string{"rlaas_counters", "rlaas_timestamps", "rlaas_leases"} {
		res, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE expires_at IS NOT NULL AND expires_at < NOW()`, tbl))
		if err != nil {
			return total, fmt.Errorf("purge %s: %w", tbl, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
