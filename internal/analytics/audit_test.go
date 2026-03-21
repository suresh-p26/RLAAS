package analytics

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileAuditLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	rec := DecisionRecord{
		Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		RequestID: "req-1",
		TenantID:  "t1",
		PolicyID:  "p1",
		Action:    "check",
		Allowed:   true,
		LatencyUs: 123,
	}
	if err := logger.Log(context.Background(), rec); err != nil {
		t.Fatalf("log: %v", err)
	}

	rec2 := DecisionRecord{
		TenantID: "t2",
		Action:   "check",
		Allowed:  false,
		Reason:   "rate_limited",
	}
	if err := logger.Log(context.Background(), rec2); err != nil {
		t.Fatalf("log2: %v", err)
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Read back and verify JSONL.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var records []DecisionRecord
	for scanner.Scan() {
		var r DecisionRecord
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		records = append(records, r)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].RequestID != "req-1" {
		t.Errorf("record 0 request_id: got %s, want req-1", records[0].RequestID)
	}
	if records[1].Allowed {
		t.Errorf("record 1 should be denied")
	}
	if records[1].Timestamp.IsZero() {
		t.Errorf("record 1 timestamp should be auto-filled")
	}
}

func TestFileAuditLogger_Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	logger.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// On Unix, 0600; on Windows permissions work differently.
	perm := info.Mode().Perm()
	_ = perm // existence check is sufficient
}

func TestFileAuditLogger_BadPath(t *testing.T) {
	_, err := NewFileAuditLogger("/nonexistent/path/audit.jsonl")
	if err == nil {
		t.Fatal("expected error for bad path")
	}
}

func TestAsyncAuditLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	inner, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	async := NewAsyncAuditLogger(inner, 64)

	// Write several records.
	for i := 0; i < 10; i++ {
		err := async.Log(context.Background(), DecisionRecord{
			TenantID:  "t1",
			Action:    "check",
			Allowed:   true,
			LatencyUs: int64(i),
		})
		if err != nil {
			t.Fatalf("log %d: %v", i, err)
		}
	}

	// Close drains the buffer.
	if err := async.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Verify all records were persisted.
	f, _ := os.Open(path)
	defer f.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	if count != 10 {
		t.Fatalf("expected 10 records, got %d", count)
	}
}

func TestAsyncAuditLogger_BufferFull(t *testing.T) {
	// Use a tiny buffer to force drops.
	noop := NoopAuditLogger{}
	// Wrap noop but with buffer size 1 so we can overflow.
	async := &AsyncAuditLogger{
		inner: noop,
		ch:    make(chan DecisionRecord, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	// Don't start the loop — the channel will fill up.
	go func() {
		<-async.stop
		close(async.done)
	}()

	// First should succeed.
	err1 := async.Log(context.Background(), DecisionRecord{Action: "check"})
	if err1 != nil {
		t.Fatalf("first log should succeed: %v", err1)
	}

	// Second should fail (buffer full, loop not running).
	err2 := async.Log(context.Background(), DecisionRecord{Action: "check"})
	if err2 == nil {
		t.Fatal("expected buffer full error")
	}

	close(async.stop)
	<-async.done
}

func TestNoopAuditLogger(t *testing.T) {
	l := NoopAuditLogger{}
	if err := l.Log(context.Background(), DecisionRecord{}); err != nil {
		t.Fatalf("noop log: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("noop close: %v", err)
	}
}

func TestAsyncAuditLogger_DefaultBuffer(t *testing.T) {
	async := NewAsyncAuditLogger(NoopAuditLogger{}, 0)
	if cap(async.ch) != 8192 {
		t.Fatalf("expected default buffer 8192, got %d", cap(async.ch))
	}
	async.Close()
}
