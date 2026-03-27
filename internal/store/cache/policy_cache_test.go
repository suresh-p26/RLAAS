package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/pkg/model"
)

func TestMemoryPolicyCacheLifecycle(t *testing.T) {
	c := NewMemoryPolicyCache(10 * time.Millisecond)
	_, ok := c.Get("n1")
	assert.False(t, ok, "expected miss")

	c.Set("n1", []model.Policy{{PolicyID: "p1"}})
	got, ok := c.Get("n1")
	require.True(t, ok, "expected hit")
	assert.Len(t, got, 1)

	c.Invalidate("n1")
	_, ok = c.Get("n1")
	assert.False(t, ok, "expected miss after invalidate")

	c.Set("n2", []model.Policy{{PolicyID: "p2"}})
	time.Sleep(20 * time.Millisecond)
	_, ok = c.Get("n2")
	assert.False(t, ok, "expected expired entry")
}

func TestMemoryPolicyCacheDefaultTTL(t *testing.T) {
	c := NewMemoryPolicyCache(0)
	assert.Greater(t, c.ttl, time.Duration(0), "expected default ttl")
}
