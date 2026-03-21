package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestPostgresCounterStore_Integration runs the full counter-store contract
// against a real PostgreSQL instance. Set RLAAS_PG_DSN to enable.
//
//	RLAAS_PG_DSN="postgres://user:pass@localhost:5432/rlaas_test?sslmode=disable" go test ./internal/store/counter/postgres/ -v
func TestPostgresCounterStore_Integration(t *testing.T) {
	dsn := os.Getenv("RLAAS_PG_DSN")
	if dsn == "" {
		t.Skip("RLAAS_PG_DSN not set, skipping Postgres integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	s := New(db)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Clean slate.
	_ = s.Delete(ctx, "pg-test")

	// Increment
	v, err := s.Increment(ctx, "pg-test", 3, time.Minute)
	if err != nil || v != 3 {
		t.Fatalf("increment: v=%d err=%v", v, err)
	}
	v, err = s.Increment(ctx, "pg-test", 2, time.Minute)
	if err != nil || v != 5 {
		t.Fatalf("increment: v=%d err=%v", v, err)
	}

	// Get
	g, err := s.Get(ctx, "pg-test")
	if err != nil || g != 5 {
		t.Fatalf("get: v=%d err=%v", g, err)
	}

	// Set + CAS
	if err := s.Set(ctx, "pg-test", 10, time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	ok, err := s.CompareAndSwap(ctx, "pg-test", 10, 20, time.Minute)
	if err != nil || !ok {
		t.Fatalf("cas pass: ok=%v err=%v", ok, err)
	}
	ok, err = s.CompareAndSwap(ctx, "pg-test", 10, 30, time.Minute)
	if err != nil || ok {
		t.Fatalf("cas fail: ok=%v err=%v", ok, err)
	}

	// Timestamps
	now := time.Now()
	if err := s.AddTimestamp(ctx, "pg-ts", now, time.Minute); err != nil {
		t.Fatalf("add ts: %v", err)
	}
	cnt, err := s.CountAfter(ctx, "pg-ts", now.Add(-time.Second))
	if err != nil || cnt != 1 {
		t.Fatalf("count after: cnt=%d err=%v", cnt, err)
	}
	if err := s.TrimBefore(ctx, "pg-ts", now.Add(time.Second)); err != nil {
		t.Fatalf("trim: %v", err)
	}

	// CheckAndAddTimestamps
	_ = s.Delete(ctx, "pg-cat")
	cnt2, allowed, err := s.CheckAndAddTimestamps(ctx, "pg-cat", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || !allowed || cnt2 != 0 {
		t.Fatalf("check-and-add 1: cnt=%d ok=%v err=%v", cnt2, allowed, err)
	}
	cnt2, allowed, err = s.CheckAndAddTimestamps(ctx, "pg-cat", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("check-and-add 2: cnt=%d ok=%v err=%v", cnt2, allowed, err)
	}
	cnt2, allowed, err = s.CheckAndAddTimestamps(ctx, "pg-cat", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || allowed {
		t.Fatalf("check-and-add 3 should deny: cnt=%d ok=%v err=%v", cnt2, allowed, err)
	}

	// Leases
	ok, cur, err := s.AcquireLease(ctx, "pg-lease", 1, time.Minute)
	if err != nil || !ok || cur != 1 {
		t.Fatalf("acquire 1: ok=%v cur=%d err=%v", ok, cur, err)
	}
	ok, _, err = s.AcquireLease(ctx, "pg-lease", 1, time.Minute)
	if err != nil || ok {
		t.Fatalf("acquire 2 should fail: ok=%v err=%v", ok, err)
	}
	if err := s.ReleaseLease(ctx, "pg-lease"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Invalid limit
	if _, _, err := s.AcquireLease(ctx, "pg-lease", 0, time.Minute); err == nil {
		t.Fatalf("expected limit error")
	}

	// PurgeExpired
	_ = s.Set(ctx, "pg-exp", 1, time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	purged, err := s.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	t.Logf("purged %d expired rows", purged)

	// Delete
	_ = s.Delete(ctx, "pg-test")
	_ = s.Delete(ctx, "pg-ts")
	_ = s.Delete(ctx, "pg-cat")
	_ = s.Delete(ctx, "pg-lease")
	_ = s.Delete(ctx, "pg-exp")
}

// TestPostgresNewFromDSN_BadDriver verifies error path for invalid driver.
func TestPostgresNewFromDSN_BadDriver(t *testing.T) {
	_, err := NewFromDSN("nonexistent-driver", "fake-dsn")
	if err == nil {
		t.Fatalf("expected error for bad driver")
	}
}
