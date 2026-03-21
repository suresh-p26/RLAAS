package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// DecisionRecord holds a single per-request decision for audit/compliance.
type DecisionRecord struct {
	Timestamp time.Time         `json:"timestamp"`
	RequestID string            `json:"request_id,omitempty"`
	TenantID  string            `json:"tenant_id,omitempty"`
	PolicyID  string            `json:"policy_id,omitempty"`
	Action    string            `json:"action"`
	Allowed   bool              `json:"allowed"`
	Reason    string            `json:"reason,omitempty"`
	SourceIP  string            `json:"source_ip,omitempty"`
	Endpoint  string            `json:"endpoint,omitempty"`
	LatencyUs int64             `json:"latency_us"`
	Tags      map[string]string `json:"tags,omitempty"`
}

// AuditLogger writes immutable decision records to a persistent backend.
type AuditLogger interface {
	Log(ctx context.Context, record DecisionRecord) error
	Close() error
}

// ------------------------------------------------
// File-based audit logger (append-only JSONL)
// ------------------------------------------------

// FileAuditLogger writes decision records as newline-delimited JSON to a file.
type FileAuditLogger struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// NewFileAuditLogger opens (or creates) the file at path for append-only writes.
func NewFileAuditLogger(path string) (*FileAuditLogger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	return &FileAuditLogger{f: f, enc: json.NewEncoder(f)}, nil
}

// Log writes one record.
func (l *FileAuditLogger) Log(_ context.Context, rec DecisionRecord) error {
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enc.Encode(rec)
}

// Close flushes and closes the underlying file.
func (l *FileAuditLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// ------------------------------------------------
// Async buffered audit logger (non-blocking hot path)
// ------------------------------------------------

// AsyncAuditLogger wraps any AuditLogger with a buffered channel so that
// the hot path (decision evaluation) is never blocked by I/O.
type AsyncAuditLogger struct {
	inner AuditLogger
	ch    chan DecisionRecord
	stop  chan struct{}
	done  chan struct{}
}

// NewAsyncAuditLogger wraps inner with an async buffer of the given size.
func NewAsyncAuditLogger(inner AuditLogger, bufSize int) *AsyncAuditLogger {
	if bufSize <= 0 {
		bufSize = 8192
	}
	a := &AsyncAuditLogger{
		inner: inner,
		ch:    make(chan DecisionRecord, bufSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go a.loop()
	return a
}

// Log enqueues a record without blocking. Drops if buffer is full.
func (a *AsyncAuditLogger) Log(_ context.Context, rec DecisionRecord) error {
	select {
	case a.ch <- rec:
		return nil
	default:
		slog.Warn("audit: buffer full, dropping record")
		return fmt.Errorf("audit buffer full")
	}
}

// Close drains remaining records and shuts down.
func (a *AsyncAuditLogger) Close() error {
	close(a.stop)
	<-a.done
	return a.inner.Close()
}

func (a *AsyncAuditLogger) loop() {
	defer close(a.done)
	for {
		select {
		case rec := <-a.ch:
			if err := a.inner.Log(context.Background(), rec); err != nil {
				slog.Error("audit: write failed", "error", err)
			}
		case <-a.stop:
			// Drain remaining.
			for {
				select {
				case rec := <-a.ch:
					_ = a.inner.Log(context.Background(), rec)
				default:
					return
				}
			}
		}
	}
}

// ------------------------------------------------
// Noop audit logger (for when audit is disabled)
// ------------------------------------------------

// NoopAuditLogger discards all records.
type NoopAuditLogger struct{}

func (NoopAuditLogger) Log(context.Context, DecisionRecord) error { return nil }
func (NoopAuditLogger) Close() error                              { return nil }
