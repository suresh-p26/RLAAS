package redis

import (
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedByDefault(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond, 2)
	if cb.State() != CBClosed {
		t.Fatalf("expected closed, got %d", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("closed breaker should allow")
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond, 2)

	// Record 3 failures to meet threshold.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CBClosed {
		t.Fatal("should still be closed after 2 failures")
	}
	cb.RecordFailure()
	if cb.State() != CBOpen {
		t.Fatalf("should be open after 3 failures, got %d", cb.State())
	}
	if cb.Allow() {
		t.Fatal("open breaker should not allow immediately")
	}
}

func TestCircuitBreaker_ClosedToOpen_SuccessResets(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond, 2)

	cb.RecordFailure()
	cb.RecordFailure()
	// A success resets the failure counter.
	cb.RecordSuccess()

	// Two more failures should not open (counter was reset).
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CBClosed {
		t.Fatal("should still be closed after reset + 2 failures")
	}
}

func TestCircuitBreaker_HalfOpenTransition(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond, 2)

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CBOpen {
		t.Fatal("should be open")
	}

	// Wait for half-open timer.
	time.Sleep(60 * time.Millisecond)

	// Allow should transition to half-open.
	if !cb.Allow() {
		t.Fatal("should allow probe after half-open period")
	}
	if cb.State() != CBHalfOpen {
		t.Fatalf("should be half-open, got %d", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToClosedOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker(2, 10*time.Millisecond, 2)

	// Open the breaker.
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for half-open.
	time.Sleep(20 * time.Millisecond)
	cb.Allow()

	if cb.State() != CBHalfOpen {
		t.Fatalf("expected half-open, got %d", cb.State())
	}

	// Two successes should close it.
	cb.RecordSuccess()
	cb.RecordSuccess()

	if cb.State() != CBClosed {
		t.Fatalf("expected closed after probe successes, got %d", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToOpenOnFailure(t *testing.T) {
	cb := NewCircuitBreaker(2, 10*time.Millisecond, 2)

	// Open the breaker.
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for half-open.
	time.Sleep(20 * time.Millisecond)
	cb.Allow()

	if cb.State() != CBHalfOpen {
		t.Fatalf("expected half-open, got %d", cb.State())
	}

	// A failure in half-open should re-open.
	cb.RecordFailure()
	if cb.State() != CBOpen {
		t.Fatalf("expected open after half-open failure, got %d", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenProbeLimited(t *testing.T) {
	cb := NewCircuitBreaker(1, 10*time.Millisecond, 1)

	cb.RecordFailure()
	time.Sleep(20 * time.Millisecond)

	// First probe allowed.
	if !cb.Allow() {
		t.Fatal("first probe should be allowed")
	}

	// Increment probe count to maxProbe.
	cb.probeCount.Store(1)
	if cb.Allow() {
		t.Fatal("should not allow more probes than maxProbe")
	}
}

func TestStoreWithCircuitBreaker_DoWrapper(t *testing.T) {
	// Test the do() wrapper with a nil circuit breaker (passthrough).
	s := &Store{cb: nil}
	called := false
	err := s.do(func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("do without CB: %v", err)
	}
	if !called {
		t.Fatal("function should have been called")
	}
}
