package slidinglog

import (
	"context"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/store/counter/memory"
	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestEvaluator_EdgeCases(t *testing.T) {
	store := memory.New()
	e := New(store)

	// Policy with zero limit - gets normalized to 1
	policy := model.Policy{
		PolicyID: "p1",
		Name:     "test",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  0, // Zero limit gets normalized to 1
			Window: "1s",
		},
	}

	decision, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	if err != nil {
		t.Fatalf("evaluate with zero limit: %v", err)
	}
	if !decision.Allowed {
		t.Fatal("zero limit normalized to 1 should allow first request")
	}

	// Second request should be denied
	decision2, err := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	if err != nil {
		t.Fatalf("evaluate second request: %v", err)
	}
	if decision2.Allowed {
		t.Fatal("second request with limit 1 should be denied")
	}
}

func TestEvaluator_LargeWindow(t *testing.T) {
	store := memory.New()
	e := New(store)

	policy := model.Policy{
		PolicyID: "p1",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  10,
			Window: "24h", // Large window
		},
	}

	// First request should be allowed
	d1, _ := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	if !d1.Allowed {
		t.Fatal("first request should be allowed")
	}

	// Subsequent requests within large window
	for i := 0; i < 9; i++ {
		d, _ := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
		if !d.Allowed {
			t.Fatalf("request %d should be allowed", i+2)
		}
	}

	// 11th request should be denied
	d11, _ := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	if d11.Allowed {
		t.Fatal("11th request should be denied")
	}
}

func TestEvaluator_ExactLimit(t *testing.T) {
	store := memory.New()
	e := New(store)

	policy := model.Policy{
		PolicyID: "p1",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  3,
			Window: "1s",
		},
	}

	// Exactly 3 requests should pass
	for i := 0; i < 3; i++ {
		d, _ := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")
		if !d.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	// 4th should be denied
	d4, _ := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")
	if d4.Allowed {
		t.Fatal("4th request should be denied")
	}
}

func TestEvaluator_WindowExpiry(t *testing.T) {
	store := memory.New()
	e := New(store)

	policy := model.Policy{
		PolicyID: "p1",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  2,
			Window: "100ms",
		},
	}

	// Make 2 requests
	e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")
	e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")

	// 3rd request blocked
	d3, _ := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")
	if d3.Allowed {
		t.Fatal("3rd request should be blocked initially")
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Now should be allowed again
	d4, _ := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key")
	if !d4.Allowed {
		t.Fatal("after window expiry, should be allowed")
	}
}

func TestEvaluator_Quantity(t *testing.T) {
	store := memory.New()
	e := New(store)

	policy := model.Policy{
		PolicyID: "p1",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  10,
			Window: "1s",
		},
	}

	// First request with quantity 5
	d1, _ := e.Evaluate(context.Background(), policy, model.RequestContext{Quantity: 5}, "key")
	if !d1.Allowed {
		t.Fatal("request with quantity 5 should be allowed")
	}

	// Second request with quantity 6 (total 11, exceeds 10)
	d2, _ := e.Evaluate(context.Background(), policy, model.RequestContext{Quantity: 6}, "key")
	if d2.Allowed {
		t.Fatal("request exceeding limit should be denied")
	}
}

func TestEvaluator_MultipleKeys(t *testing.T) {
	store := memory.New()
	e := New(store)

	policy := model.Policy{
		PolicyID: "p1",
		Algorithm: model.AlgorithmConfig{
			Type:   model.AlgoSlidingWindowLog,
			Limit:  2,
			Window: "1s",
		},
	}

	// Key1: 2 requests allowed
	e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	d3, _ := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key1")
	if d3.Allowed {
		t.Fatal("key1 3rd request should be denied")
	}

	// Key2: should have independent limit
	d1, _ := e.Evaluate(context.Background(), policy, model.RequestContext{}, "key2")
	if !d1.Allowed {
		t.Fatal("key2 should have independent limit")
	}
}
