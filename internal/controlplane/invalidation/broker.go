package invalidation

import (
	"context"
	"sync"
)

// Broker is a lightweight in-process event bus for phase 3 cache invalidation.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string][]func(event map[string]string)
}

// NewBroker creates an empty invalidation broker.
func NewBroker() *Broker {
	return &Broker{subscribers: map[string][]func(event map[string]string){}}
}

// Subscribe registers a callback for a topic.
func (b *Broker) Subscribe(topic string, fn func(event map[string]string)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[topic] = append(b.subscribers[topic], fn)
}

// Publish sends one event to all subscribers of the topic.
func (b *Broker) Publish(_ context.Context, topic string, event map[string]string) error {
	b.mu.RLock()
	handlers := append([]func(event map[string]string){}, b.subscribers[topic]...)
	b.mu.RUnlock()
	for _, h := range handlers {
		h(event)
	}
	return nil
}
