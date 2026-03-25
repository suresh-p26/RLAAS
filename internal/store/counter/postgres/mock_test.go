package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	return New(db), mock
}

func TestNew(t *testing.T) {
	db, _, _ := sqlmock.New()
	s := New(db)
	if s == nil || s.db != db {
		t.Fatal("expected store with db set")
	}
}

func TestNewFromDSN_Error(t *testing.T) {
	_, err := NewFromDSN("nonexistent_driver", "fake-dsn")
	if err == nil {
		t.Fatal("expected error for unknown driver")
	}
}

func TestMigrate_Success(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS rlaas_counters").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS rlaas_timestamps").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS rlaas_leases").WillReturnResult(sqlmock.NewResult(0, 0))

	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrate_Error(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS rlaas_counters").WillReturnError(errors.New("ddl fail"))

	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("expected migrate error")
	}
}

func TestPing(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectPing()
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestClose(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectClose()
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestIncrement_Success(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("INSERT INTO rlaas_counters").
		WithArgs("key1", int64(5), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(int64(5)))

	val, err := s.Increment(context.Background(), "key1", 5, time.Minute)
	if err != nil {
		t.Fatalf("increment: %v", err)
	}
	if val != 5 {
		t.Fatalf("expected 5, got %d", val)
	}
}

func TestIncrement_Error(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("INSERT INTO rlaas_counters").
		WillReturnError(errors.New("db error"))

	_, err := s.Increment(context.Background(), "key1", 5, time.Minute)
	if err == nil {
		t.Fatal("expected increment error")
	}
}

func TestGet_Success(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("SELECT value FROM rlaas_counters").
		WithArgs("key1").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(int64(42)))

	val, err := s.Get(context.Background(), "key1")
	if err != nil || val != 42 {
		t.Fatalf("get: val=%d err=%v", val, err)
	}
}

func TestGet_NoRows(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("SELECT value FROM rlaas_counters").
		WithArgs("key1").
		WillReturnError(sql.ErrNoRows)

	val, err := s.Get(context.Background(), "key1")
	if err != nil || val != 0 {
		t.Fatalf("get no rows: val=%d err=%v", val, err)
	}
}

func TestGet_Error(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("SELECT value FROM rlaas_counters").
		WithArgs("key1").
		WillReturnError(errors.New("query fail"))

	_, err := s.Get(context.Background(), "key1")
	if err == nil {
		t.Fatal("expected get error")
	}
}

func TestSet_Success(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("INSERT INTO rlaas_counters").
		WithArgs("key1", int64(10), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.Set(context.Background(), "key1", 10, time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
}

func TestSet_Error(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("INSERT INTO rlaas_counters").
		WillReturnError(errors.New("set fail"))

	if err := s.Set(context.Background(), "key1", 10, time.Minute); err == nil {
		t.Fatal("expected set error")
	}
	_ = mock
}

func TestCompareAndSwap_Match(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM rlaas_counters").
		WithArgs("k1").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(int64(10)))
	mock.ExpectExec("UPDATE rlaas_counters SET value").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ok, err := s.CompareAndSwap(context.Background(), "k1", 10, 20, time.Minute)
	if err != nil || !ok {
		t.Fatalf("cas match: ok=%v err=%v", ok, err)
	}
}

func TestCompareAndSwap_Mismatch(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM rlaas_counters").
		WithArgs("k1").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(int64(99)))
	mock.ExpectRollback()

	ok, err := s.CompareAndSwap(context.Background(), "k1", 10, 20, time.Minute)
	if err != nil || ok {
		t.Fatalf("cas mismatch: ok=%v err=%v", ok, err)
	}
}

func TestCompareAndSwap_NoRows_OldZero(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM rlaas_counters").
		WithArgs("k1").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO rlaas_counters").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ok, err := s.CompareAndSwap(context.Background(), "k1", 0, 1, time.Minute)
	if err != nil || !ok {
		t.Fatalf("cas no rows old=0: ok=%v err=%v", ok, err)
	}
}

func TestCompareAndSwap_NoRows_OldNonZero(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM rlaas_counters").
		WithArgs("k1").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	ok, err := s.CompareAndSwap(context.Background(), "k1", 5, 10, time.Minute)
	if err != nil || ok {
		t.Fatalf("cas no rows old!=0: ok=%v err=%v", ok, err)
	}
}

func TestCompareAndSwap_BeginError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("begin fail"))

	_, err := s.CompareAndSwap(context.Background(), "k1", 0, 1, time.Minute)
	if err == nil {
		t.Fatal("expected begin error")
	}
}

func TestCompareAndSwap_SelectError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM rlaas_counters").
		WillReturnError(errors.New("select fail"))
	mock.ExpectRollback()

	_, err := s.CompareAndSwap(context.Background(), "k1", 0, 1, time.Minute)
	if err == nil {
		t.Fatal("expected select error")
	}
}

func TestDelete_Success(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("DELETE FROM rlaas_counters").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM rlaas_timestamps").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM rlaas_leases").WillReturnResult(sqlmock.NewResult(0, 0))

	if err := s.Delete(context.Background(), "key1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestDelete_Error(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("DELETE FROM rlaas_counters").WillReturnError(errors.New("del fail"))

	if err := s.Delete(context.Background(), "key1"); err == nil {
		t.Fatal("expected delete error")
	}
}

func TestAddTimestamp_Success(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("INSERT INTO rlaas_timestamps").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.AddTimestamp(context.Background(), "key1", time.Now(), time.Minute); err != nil {
		t.Fatalf("add timestamp: %v", err)
	}
}

func TestAddTimestamp_Error(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("INSERT INTO rlaas_timestamps").
		WillReturnError(errors.New("ts fail"))

	if err := s.AddTimestamp(context.Background(), "key1", time.Now(), time.Minute); err == nil {
		t.Fatal("expected add timestamp error")
	}
}

func TestCountAfter_Success(t *testing.T) {
	s, mock := newMock(t)
	after := time.Now().Add(-time.Minute)
	mock.ExpectQuery("SELECT COUNT").
		WithArgs("key1", after).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(7)))

	cnt, err := s.CountAfter(context.Background(), "key1", after)
	if err != nil || cnt != 7 {
		t.Fatalf("count after: cnt=%d err=%v", cnt, err)
	}
}

func TestCountAfter_Error(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("count fail"))

	_, err := s.CountAfter(context.Background(), "key1", time.Now())
	if err == nil {
		t.Fatal("expected count error")
	}
}

func TestTrimBefore_Success(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("DELETE FROM rlaas_timestamps").
		WillReturnResult(sqlmock.NewResult(0, 3))

	if err := s.TrimBefore(context.Background(), "key1", time.Now()); err != nil {
		t.Fatalf("trim: %v", err)
	}
}

func TestTrimBefore_Error(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("DELETE FROM rlaas_timestamps").
		WillReturnError(errors.New("trim fail"))

	if err := s.TrimBefore(context.Background(), "key1", time.Now()); err == nil {
		t.Fatal("expected trim error")
	}
}

func TestCheckAndAddTimestamps_Allow(t *testing.T) {
	s, mock := newMock(t)
	cutoff := time.Now().Add(-time.Minute)
	ts := time.Now()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM rlaas_timestamps").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(3)))
	mock.ExpectExec("INSERT INTO rlaas_timestamps").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	count, allowed, err := s.CheckAndAddTimestamps(context.Background(), "k1", cutoff, 10, 1, ts, time.Minute)
	if err != nil || !allowed || count != 3 {
		t.Fatalf("check-and-add: count=%d allowed=%v err=%v", count, allowed, err)
	}
}

func TestCheckAndAddTimestamps_OverLimit(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM rlaas_timestamps").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(10)))
	mock.ExpectCommit()

	count, allowed, err := s.CheckAndAddTimestamps(context.Background(), "k1", time.Now(), 10, 1, time.Now(), time.Minute)
	if err != nil || allowed || count != 10 {
		t.Fatalf("check-and-add over limit: count=%d allowed=%v err=%v", count, allowed, err)
	}
}

func TestCheckAndAddTimestamps_BeginError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("begin fail"))

	_, _, err := s.CheckAndAddTimestamps(context.Background(), "k1", time.Now(), 10, 1, time.Now(), time.Minute)
	if err == nil {
		t.Fatal("expected begin error")
	}
}

func TestCheckAndAddTimestamps_CountError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM rlaas_timestamps").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("count fail"))
	mock.ExpectRollback()

	_, _, err := s.CheckAndAddTimestamps(context.Background(), "k1", time.Now(), 10, 1, time.Now(), time.Minute)
	if err == nil {
		t.Fatal("expected count error")
	}
}

func TestAcquireLease_Success(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM rlaas_leases").
		WithArgs("k1").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(int64(2)))
	mock.ExpectExec("INSERT INTO rlaas_leases").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ok, cur, err := s.AcquireLease(context.Background(), "k1", 5, time.Minute)
	if err != nil || !ok || cur != 3 {
		t.Fatalf("acquire: ok=%v cur=%d err=%v", ok, cur, err)
	}
}

func TestAcquireLease_AtLimit(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM rlaas_leases").
		WithArgs("k1").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(int64(5)))
	mock.ExpectRollback()

	ok, cur, err := s.AcquireLease(context.Background(), "k1", 5, time.Minute)
	if err != nil || ok {
		t.Fatalf("acquire at limit: ok=%v cur=%d err=%v", ok, cur, err)
	}
}

func TestAcquireLease_NoRows(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM rlaas_leases").
		WithArgs("k1").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO rlaas_leases").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ok, cur, err := s.AcquireLease(context.Background(), "k1", 5, time.Minute)
	if err != nil || !ok || cur != 1 {
		t.Fatalf("acquire no rows: ok=%v cur=%d err=%v", ok, cur, err)
	}
}

func TestAcquireLease_ZeroLimit(t *testing.T) {
	s, _ := newMock(t)
	_, _, err := s.AcquireLease(context.Background(), "k1", 0, time.Minute)
	if err == nil {
		t.Fatal("expected error for zero limit")
	}
}

func TestAcquireLease_BeginError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("begin fail"))

	_, _, err := s.AcquireLease(context.Background(), "k1", 5, time.Minute)
	if err == nil {
		t.Fatal("expected begin error")
	}
}

func TestAcquireLease_SelectError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM rlaas_leases").
		WillReturnError(errors.New("select fail"))
	mock.ExpectRollback()

	_, _, err := s.AcquireLease(context.Background(), "k1", 5, time.Minute)
	if err == nil {
		t.Fatal("expected select error")
	}
}

func TestReleaseLease_Success(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("UPDATE rlaas_leases").WillReturnResult(sqlmock.NewResult(0, 1))

	if err := s.ReleaseLease(context.Background(), "k1"); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestReleaseLease_Error(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("UPDATE rlaas_leases").WillReturnError(errors.New("release fail"))

	if err := s.ReleaseLease(context.Background(), "k1"); err == nil {
		t.Fatal("expected release error")
	}
}

func TestPurgeExpired_Success(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("DELETE FROM rlaas_counters WHERE expires_at").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("DELETE FROM rlaas_timestamps WHERE expires_at").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM rlaas_leases WHERE expires_at").
		WillReturnResult(sqlmock.NewResult(0, 0))

	total, err := s.PurgeExpired(context.Background())
	if err != nil || total != 3 {
		t.Fatalf("purge: total=%d err=%v", total, err)
	}
}

func TestPurgeExpired_Error(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec("DELETE FROM rlaas_counters WHERE expires_at").
		WillReturnError(errors.New("purge fail"))

	_, err := s.PurgeExpired(context.Background())
	if err == nil {
		t.Fatal("expected purge error")
	}
}

func TestExpiresAt_ZeroTTL(t *testing.T) {
	if got := expiresAt(0); got != nil {
		t.Fatal("expected nil for zero TTL")
	}
	if got := expiresAt(-time.Second); got != nil {
		t.Fatal("expected nil for negative TTL")
	}
	if got := expiresAt(time.Minute); got == nil {
		t.Fatal("expected non-nil for positive TTL")
	}
}
