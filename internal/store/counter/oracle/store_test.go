package oracle

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestOracleCounterStore_Integration runs the full counter-store contract
// against a real Oracle instance. Set RLAAS_ORACLE_DSN to enable.
//
//	RLAAS_ORACLE_DSN="user/pass@localhost:1521/FREEPDB1" go test ./internal/store/counter/oracle/ -v
func TestOracleCounterStore_Integration(t *testing.T) {
	dsn := os.Getenv("RLAAS_ORACLE_DSN")
	if dsn == "" {
		t.Skip("RLAAS_ORACLE_DSN not set, skipping Oracle integration test")
	}
	db, err := sql.Open("godror", dsn)
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
	_ = s.Delete(ctx, "ora-test")

	// Increment
	v, err := s.Increment(ctx, "ora-test", 3, time.Minute)
	if err != nil || v != 3 {
		t.Fatalf("increment: v=%d err=%v", v, err)
	}
	v, err = s.Increment(ctx, "ora-test", 2, time.Minute)
	if err != nil || v != 5 {
		t.Fatalf("increment: v=%d err=%v", v, err)
	}

	// Get
	g, err := s.Get(ctx, "ora-test")
	if err != nil || g != 5 {
		t.Fatalf("get: v=%d err=%v", g, err)
	}

	// Set + CAS
	if err := s.Set(ctx, "ora-test", 10, time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	ok, err := s.CompareAndSwap(ctx, "ora-test", 10, 20, time.Minute)
	if err != nil || !ok {
		t.Fatalf("cas pass: ok=%v err=%v", ok, err)
	}
	ok, err = s.CompareAndSwap(ctx, "ora-test", 10, 30, time.Minute)
	if err != nil || ok {
		t.Fatalf("cas fail: ok=%v err=%v", ok, err)
	}

	// Timestamps
	now := time.Now()
	if err := s.AddTimestamp(ctx, "ora-ts", now, time.Minute); err != nil {
		t.Fatalf("add ts: %v", err)
	}
	cnt, err := s.CountAfter(ctx, "ora-ts", now.Add(-time.Second))
	if err != nil || cnt != 1 {
		t.Fatalf("count after: cnt=%d err=%v", cnt, err)
	}
	if err := s.TrimBefore(ctx, "ora-ts", now.Add(time.Second)); err != nil {
		t.Fatalf("trim: %v", err)
	}

	// CheckAndAddTimestamps
	_ = s.Delete(ctx, "ora-cat")
	cnt2, allowed, err := s.CheckAndAddTimestamps(ctx, "ora-cat", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || !allowed || cnt2 != 0 {
		t.Fatalf("check-and-add 1: cnt=%d ok=%v err=%v", cnt2, allowed, err)
	}
	cnt2, allowed, err = s.CheckAndAddTimestamps(ctx, "ora-cat", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("check-and-add 2: cnt=%d ok=%v err=%v", cnt2, allowed, err)
	}
	cnt2, allowed, err = s.CheckAndAddTimestamps(ctx, "ora-cat", now.Add(-time.Minute), 2, 1, now, time.Minute)
	if err != nil || allowed {
		t.Fatalf("check-and-add 3 should deny: cnt=%d ok=%v err=%v", cnt2, allowed, err)
	}

	// Leases
	ok, cur, err := s.AcquireLease(ctx, "ora-lease", 1, time.Minute)
	if err != nil || !ok || cur != 1 {
		t.Fatalf("acquire 1: ok=%v cur=%d err=%v", ok, cur, err)
	}
	ok, _, err = s.AcquireLease(ctx, "ora-lease", 1, time.Minute)
	if err != nil || ok {
		t.Fatalf("acquire 2 should fail: ok=%v err=%v", ok, err)
	}
	if err := s.ReleaseLease(ctx, "ora-lease"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Invalid limit
	if _, _, err := s.AcquireLease(ctx, "ora-lease", 0, time.Minute); err == nil {
		t.Fatalf("expected limit error")
	}

	// PurgeExpired
	_ = s.Set(ctx, "ora-exp", 1, time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	purged, err := s.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	t.Logf("purged %d expired rows", purged)

	// Cleanup
	_ = s.Delete(ctx, "ora-test")
	_ = s.Delete(ctx, "ora-ts")
	_ = s.Delete(ctx, "ora-cat")
	_ = s.Delete(ctx, "ora-lease")
	_ = s.Delete(ctx, "ora-exp")
}

// TestOracleNewFromDSN_BadDriver verifies error path for invalid driver.
func TestOracleNewFromDSN_BadDriver(t *testing.T) {
	_, err := NewFromDSN("nonexistent-driver", "fake-dsn")
	if err == nil {
		t.Fatalf("expected error for bad driver")
	}
}

// TestOracleContains checks the helper string function.
func TestOracleContains(t *testing.T) {
	if !contains("ORA-00955: name is already used", "ORA-00955") {
		t.Fatal("should match")
	}
	if contains("hello", "world") {
		t.Fatal("should not match")
	}
}
