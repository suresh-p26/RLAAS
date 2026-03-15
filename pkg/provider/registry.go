package provider

import (
	"fmt"
	"sync"
)

// Registry holds all registered provider adapters. It is safe for concurrent
// access and allows adapter lookup by name at runtime.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Adapter)}
}

// Register adds a provider adapter. Returns an error if the name collides.
func (r *Registry) Register(adapter Adapter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := adapter.Name()
	if _, exists := r.adapters[name]; exists {
		return fmt.Errorf("provider %q already registered", name)
	}
	r.adapters[name] = adapter
	return nil
}

// Get looks up a provider adapter by name.
func (r *Registry) Get(name string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[name]
	return a, ok
}

// List returns all registered adapter names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.adapters))
	for name := range r.adapters {
		names = append(names, name)
	}
	return names
}

// MustRegister panics if registration fails.
func (r *Registry) MustRegister(adapter Adapter) {
	if err := r.Register(adapter); err != nil {
		panic(err)
	}
}

// DefaultRegistry is the global registry used when providers self-register.
var DefaultRegistry = NewRegistry()
