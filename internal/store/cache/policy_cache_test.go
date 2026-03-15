package cache

import (
	"github.com/suresh-p26/RLAAS/pkg/model"
	"testing"
	"time"
)

func TestMemoryPolicyCacheLifecycle(t *testing.T) {
	c := NewMemoryPolicyCache(10 * time.Millisecond)
	if _, ok := c.Get("n1"); ok {
		t.Fatalf("expected miss")
	}
	c.Set("n1", []model.Policy{{PolicyID: "p1"}})
	if got, ok := c.Get("n1"); !ok || len(got) != 1 {
		t.Fatalf("expected hit")
	}
	c.Invalidate("n1")
	if _, ok := c.Get("n1"); ok {
		t.Fatalf("expected miss after invalidate")
	}
	c.Set("n2", []model.Policy{{PolicyID: "p2"}})
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get("n2"); ok {
		t.Fatalf("expected expired entry")
	}
}

func TestMemoryPolicyCacheDefaultTTL(t *testing.T) {
	c := NewMemoryPolicyCache(0)
	if c.ttl <= 0 {
		t.Fatalf("expected default ttl")
	}
}
