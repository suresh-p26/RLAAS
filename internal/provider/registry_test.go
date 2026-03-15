package provider

import (
	"context"
	"testing"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	a := &mockAdapter{name: "test_provider"}
	if err := r.Register(a); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	got, ok := r.Get("test_provider")
	if !ok || got.Name() != "test_provider" {
		t.Fatalf("expected test_provider, got %v", got)
	}
}

func TestRegistryDuplicateReturnsError(t *testing.T) {
	r := NewRegistry()
	a := &mockAdapter{name: "dup"}
	_ = r.Register(a)
	if err := r.Register(a); err == nil {
		t.Fatalf("expected error on duplicate register")
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&mockAdapter{name: "a"})
	_ = r.Register(&mockAdapter{name: "b"})
	names := r.List()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nope")
	if ok {
		t.Fatalf("expected not found")
	}
}

func TestMustRegisterPanicsOnDuplicate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&mockAdapter{name: "panic_test"})
	defer func() {
		if rec := recover(); rec == nil {
			t.Fatalf("expected panic")
		}
	}()
	r.MustRegister(&mockAdapter{name: "panic_test"})
}

type mockAdapter struct {
	name string
}

func (m *mockAdapter) Name() string              { return m.name }
func (m *mockAdapter) SignalKinds() []SignalKind { return []SignalKind{SignalLog} }
func (m *mockAdapter) ProcessBatch(_ context.Context, records []TelemetryRecord) ([]Decision, error) {
	return nil, nil
}

// compile-time check
var _ Adapter = (*mockAdapter)(nil)
