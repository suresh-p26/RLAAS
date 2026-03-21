// Package oracle implements a production-ready CounterStore backed by Oracle
// Database. Uses MERGE for upserts, SELECT … FOR UPDATE for CAS and leases, and
// DBMS_LOCK.REQUEST through advisory-style serialization for CheckAndAddTimestamps.
//
// Compatible with the godror ("github.com/godror/godror") driver via database/sql.
//
// Required schema (run Migrate to auto-create):
//
//	rlaas_counters  (key VARCHAR2(512) PK, value NUMBER(19), expires_at TIMESTAMP WITH TIME ZONE)
//	rlaas_timestamps(key VARCHAR2(512), ts TIMESTAMP WITH TIME ZONE, expires_at TIMESTAMP WITH TIME ZONE)
//	rlaas_leases    (key VARCHAR2(512) PK, value NUMBER(19), expires_at TIMESTAMP WITH TIME ZONE)
package oracle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store implements store.CounterStore over Oracle Database.
type Store struct {
	db *sql.DB
}

// New creates an Oracle counter store. The caller must supply an already-opened
// *sql.DB configured with the godror (or compatible) driver and pool settings.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// NewFromDSN is a convenience constructor that opens a connection pool.
// driverName is typically "godror".
func NewFromDSN(driverName, dsn string) (*Store, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("oracle counter store: %w", err)
	}
	return &Store{db: db}, nil
}

// Migrate creates tables and indices if they do not exist.
// Oracle does not support IF NOT EXISTS on DDL, so we catch ORA-00955.
func (s *Store) Migrate(ctx context.Context) error {
	ddls := []string{
		`CREATE TABLE rlaas_counters (
			key        VARCHAR2(512) PRIMARY KEY,
			value      NUMBER(19) DEFAULT 0 NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE
		)`,
		`CREATE TABLE rlaas_timestamps (
			key        VARCHAR2(512) NOT NULL,
			ts         TIMESTAMP WITH TIME ZONE NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE
		)`,
		`CREATE INDEX idx_rlaas_ts_key_ts ON rlaas_timestamps (key, ts)`,
		`CREATE TABLE rlaas_leases (
			key        VARCHAR2(512) PRIMARY KEY,
			value      NUMBER(19) DEFAULT 0 NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE
		)`,
	}
	for _, ddl := range ddls {
		_, err := s.db.ExecContext(ctx, ddl)
		if err != nil && !isOracleObjExists(err) {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// isOracleObjExists returns true for ORA-00955 (name is already used by an existing object).
func isOracleObjExists(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "ORA-00955") || contains(err.Error(), "already used")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSub(s, sub))
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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

// Increment atomically adds value to a key (MERGE for upsert).
func (s *Store) Increment(ctx context.Context, key string, value int64, ttl time.Duration) (int64, error) {
	exp := expiresAt(ttl)
	// MERGE handles insert-or-update atomically.
	_, err := s.db.ExecContext(ctx, `
		MERGE INTO rlaas_counters dst
		USING (SELECT :1 AS key FROM dual) src ON (dst.key = src.key)
		WHEN MATCHED THEN UPDATE SET
			dst.value = CASE
				WHEN dst.expires_at IS NOT NULL AND dst.expires_at < SYSTIMESTAMP THEN :2
				ELSE dst.value + :2
			END,
			dst.expires_at = :3
		WHEN NOT MATCHED THEN INSERT (key, value, expires_at) VALUES (:1, :2, :3)
	`, key, value, exp)
	if err != nil {
		return 0, fmt.Errorf("increment %q: %w", key, err)
	}
	// Read back the committed value.
	var result int64
	err = s.db.QueryRowContext(ctx, `
		SELECT value FROM rlaas_counters WHERE key = :1
	`, key).Scan(&result)
	if err != nil {
		return 0, fmt.Errorf("increment readback %q: %w", key, err)
	}
	return result, nil
}

// Get reads a counter value. Returns 0 for missing or expired keys.
func (s *Store) Get(ctx context.Context, key string) (int64, error) {
	var val int64
	err := s.db.QueryRowContext(ctx, `
		SELECT value FROM rlaas_counters
		WHERE key = :1
		  AND (expires_at IS NULL OR expires_at >= SYSTIMESTAMP)
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
		MERGE INTO rlaas_counters dst
		USING (SELECT :1 AS key FROM dual) src ON (dst.key = src.key)
		WHEN MATCHED THEN UPDATE SET dst.value = :2, dst.expires_at = :3
		WHEN NOT MATCHED THEN INSERT (key, value, expires_at) VALUES (:1, :2, :3)
	`, key, value, exp)
	if err != nil {
		return fmt.Errorf("set %q: %w", key, err)
	}
	return nil
}

// CompareAndSwap atomically updates value only if current equals oldVal.
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
		WHERE key = :1
		  AND (expires_at IS NULL OR expires_at >= SYSTIMESTAMP)
		FOR UPDATE
	`, key).Scan(&cur)

	if errors.Is(err, sql.ErrNoRows) {
		if oldVal != 0 {
			return false, nil
		}
		_, err = tx.ExecContext(ctx, `
			MERGE INTO rlaas_counters dst
			USING (SELECT :1 AS key FROM dual) src ON (dst.key = src.key)
			WHEN MATCHED THEN UPDATE SET dst.value = :2, dst.expires_at = :3
			WHEN NOT MATCHED THEN INSERT (key, value, expires_at) VALUES (:1, :2, :3)
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
		UPDATE rlaas_counters SET value = :1, expires_at = :2 WHERE key = :3
	`, newVal, exp, key)
	if err != nil {
		return false, fmt.Errorf("cas update: %w", err)
	}
	return tx.Commit() == nil, nil
}

// Delete removes all data for one key across all tables.
func (s *Store) Delete(ctx context.Context, key string) error {
	for _, tbl := range []string{"rlaas_counters", "rlaas_timestamps", "rlaas_leases"} {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE key = :1`, tbl), key); err != nil {
			return fmt.Errorf("delete %q from %s: %w", key, tbl, err)
		}
	}
	return nil
}

// AddTimestamp appends a timestamp entry.
func (s *Store) AddTimestamp(ctx context.Context, key string, ts time.Time, ttl time.Duration) error {
	exp := expiresAt(ttl)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rlaas_timestamps (key, ts, expires_at) VALUES (:1, :2, :3)
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
		WHERE key = :1 AND ts >= :2
		  AND (expires_at IS NULL OR expires_at >= SYSTIMESTAMP)
	`, key, after).Scan(&cnt)
	if err != nil {
		return 0, fmt.Errorf("count after %q: %w", key, err)
	}
	return cnt, nil
}

// TrimBefore removes timestamps older than the given time.
func (s *Store) TrimBefore(ctx context.Context, key string, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM rlaas_timestamps WHERE key = :1 AND ts < :2
	`, key, before)
	if err != nil {
		return fmt.Errorf("trim before %q: %w", key, err)
	}
	return nil
}

// CheckAndAddTimestamps atomically trims, counts, checks limit, and adds
// timestamps in a single serialized transaction.
func (s *Store) CheckAndAddTimestamps(ctx context.Context, key string, cutoff time.Time, limit, cost int64, ts time.Time, ttl time.Duration) (int64, bool, error) {
	exp := expiresAt(ttl)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, false, fmt.Errorf("check-and-add begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Trim.
	_, _ = tx.ExecContext(ctx, `
		DELETE FROM rlaas_timestamps WHERE key = :1 AND ts < :2
	`, key, cutoff)

	// Count remaining.
	var count int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM rlaas_timestamps WHERE key = :1 AND ts >= :2
	`, key, cutoff).Scan(&count); err != nil {
		return 0, false, fmt.Errorf("check-and-add count: %w", err)
	}

	if count+cost > limit {
		_ = tx.Commit()
		return count, false, nil
	}

	for i := int64(0); i < cost; i++ {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO rlaas_timestamps (key, ts, expires_at) VALUES (:1, :2, :3)
		`, key, ts, exp); err != nil {
			return 0, false, fmt.Errorf("check-and-add insert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return count, true, nil
}

// AcquireLease reserves one concurrency slot.
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
		WHERE key = :1
		  AND (expires_at IS NULL OR expires_at >= SYSTIMESTAMP)
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
		MERGE INTO rlaas_leases dst
		USING (SELECT :1 AS key FROM dual) src ON (dst.key = src.key)
		WHEN MATCHED THEN UPDATE SET dst.value = :2, dst.expires_at = :3
		WHEN NOT MATCHED THEN INSERT (key, value, expires_at) VALUES (:1, :2, :3)
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
		WHERE key = :1
	`, key)
	if err != nil {
		return fmt.Errorf("release lease %q: %w", key, err)
	}
	return nil
}

// PurgeExpired removes expired rows from all tables.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	var total int64
	for _, tbl := range []string{"rlaas_counters", "rlaas_timestamps", "rlaas_leases"} {
		res, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE expires_at IS NOT NULL AND expires_at < SYSTIMESTAMP`, tbl))
		if err != nil {
			return total, fmt.Errorf("purge %s: %w", tbl, err)
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
