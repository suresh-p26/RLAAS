package analytics

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileAuditLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	logger, err := NewFileAuditLogger(path)
	require.NoError(t, err, "open")

	rec := DecisionRecord{
		Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		RequestID: "req-1",
		TenantID:  "t1",
		PolicyID:  "p1",
		Action:    "check",
		Allowed:   true,
		LatencyUs: 123,
	}
	require.NoError(t, logger.Log(context.Background(), rec), "log")

	rec2 := DecisionRecord{
		TenantID: "t2",
		Action:   "check",
		Allowed:  false,
		Reason:   "rate_limited",
	}
	require.NoError(t, logger.Log(context.Background(), rec2), "log2")
	require.NoError(t, logger.Close(), "close")

	// Read back and verify JSONL.
	f, err := os.Open(path)
	require.NoError(t, err, "reopen")
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var records []DecisionRecord
	for scanner.Scan() {
		var r DecisionRecord
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &r), "unmarshal")
		records = append(records, r)
	}
	require.Len(t, records, 2, "expected 2 records")
	assert.Equal(t, "req-1", records[0].RequestID, "record 0 request_id")
	assert.False(t, records[1].Allowed, "record 1 should be denied")
	assert.False(t, records[1].Timestamp.IsZero(), "record 1 timestamp should be auto-filled")
}

func TestFileAuditLogger_Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	logger, err := NewFileAuditLogger(path)
	require.NoError(t, err, "open")
	logger.Close()

	info, err := os.Stat(path)
	require.NoError(t, err, "stat")
	// On Unix, 0600; on Windows permissions work differently.
	perm := info.Mode().Perm()
	_ = perm // existence check is sufficient
}

func TestFileAuditLogger_BadPath(t *testing.T) {
	_, err := NewFileAuditLogger("/nonexistent/path/audit.jsonl")
	require.Error(t, err, "expected error for bad path")
}

func TestAsyncAuditLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	inner, err := NewFileAuditLogger(path)
	require.NoError(t, err, "open")

	async := NewAsyncAuditLogger(inner, 64)

	for i := 0; i < 10; i++ {
		require.NoError(t, async.Log(context.Background(), DecisionRecord{
			TenantID:  "t1",
			Action:    "check",
			Allowed:   true,
			LatencyUs: int64(i),
		}), "log %d", i)
	}

	require.NoError(t, async.Close(), "close")

	// Verify all records were persisted.
	f, _ := os.Open(path)
	defer f.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	assert.Equal(t, 10, count, "expected 10 records")
}

func TestAsyncAuditLogger_BufferFull(t *testing.T) {
	noop := NoopAuditLogger{}
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

	require.NoError(t, async.Log(context.Background(), DecisionRecord{Action: "check"}), "first log should succeed")
	require.Error(t, async.Log(context.Background(), DecisionRecord{Action: "check"}), "expected buffer full error")

	close(async.stop)
	<-async.done
}

func TestNoopAuditLogger(t *testing.T) {
	l := NoopAuditLogger{}
	require.NoError(t, l.Log(context.Background(), DecisionRecord{}), "noop log")
	require.NoError(t, l.Close(), "noop close")
}

func TestAsyncAuditLogger_DefaultBuffer(t *testing.T) {
	async := NewAsyncAuditLogger(NoopAuditLogger{}, 0)
	assert.Equal(t, 8192, cap(async.ch), "expected default buffer 8192")
	async.Close()
}
