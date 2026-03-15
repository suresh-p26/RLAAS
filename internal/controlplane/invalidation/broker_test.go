package invalidation

import (
	"context"
	"sync"
	"testing"
)

func TestNewBrokerIsEmpty(t *testing.T) {
	b := NewBroker()
	if b == nil {
		t.Fatal("expected non-nil broker")
	}
	// Publishing to an empty topic should not panic.
	if err := b.Publish(context.Background(), "noop", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPublishDelivers(t *testing.T) {
	b := NewBroker()
	var received map[string]string
	b.Subscribe("policy.changed", func(event map[string]string) {
		received = event
	})
	evt := map[string]string{"policy_id": "p1", "action": "create"}
	_ = b.Publish(context.Background(), "policy.changed", evt)
	if received == nil || received["policy_id"] != "p1" || received["action"] != "create" {
		t.Fatalf("expected event delivery, got %v", received)
	}
}

func TestPublishMultipleSubscribers(t *testing.T) {
	b := NewBroker()
	var mu sync.Mutex
	count := 0
	for i := 0; i < 5; i++ {
		b.Subscribe("topic", func(_ map[string]string) {
			mu.Lock()
			count++
			mu.Unlock()
		})
	}
	_ = b.Publish(context.Background(), "topic", map[string]string{})
	mu.Lock()
	defer mu.Unlock()
	if count != 5 {
		t.Fatalf("expected 5 callbacks, got %d", count)
	}
}

func TestPublishDifferentTopicsIsolated(t *testing.T) {
	b := NewBroker()
	called := false
	b.Subscribe("alpha", func(_ map[string]string) { called = true })
	_ = b.Publish(context.Background(), "beta", map[string]string{})
	if called {
		t.Fatal("alpha subscriber should not receive beta events")
	}
}

func TestConcurrentSubscribePublish(t *testing.T) {
	b := NewBroker()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			b.Subscribe("t", func(_ map[string]string) {})
		}()
		go func() {
			defer wg.Done()
			_ = b.Publish(context.Background(), "t", map[string]string{})
		}()
	}
	wg.Wait()
}
