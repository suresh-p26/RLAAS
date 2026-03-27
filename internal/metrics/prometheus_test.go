package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordDecision(t *testing.T) {
	c := New()
	c.RecordDecision("tenant-1", "policy-a", true, 100*time.Microsecond)
	c.RecordDecision("tenant-1", "policy-a", false, 500*time.Microsecond)
	c.RecordDecision("tenant-2", "policy-b", true, 50*time.Microsecond)

	assert.Equal(t, int64(3), c.DecisionsTotal.Load(), "total")
	assert.Equal(t, int64(2), c.DecisionsAllowed.Load(), "allowed")
	assert.Equal(t, int64(1), c.DecisionsDenied.Load(), "denied")
}

func TestLatencyHistogram(t *testing.T) {
	h := NewHistogram([]float64{10, 50, 100, 500, 1000})
	for i := 0; i < 100; i++ {
		h.Observe(float64(i))
	}
	p50 := h.Percentile(50)
	assert.GreaterOrEqual(t, p50, float64(10), "p50 should be >= 10")
	assert.LessOrEqual(t, p50, float64(100), "p50 should be <= 100")
	p99 := h.Percentile(99)
	assert.GreaterOrEqual(t, p99, float64(100), "p99 should be >= 100")
}

func TestLatencyPercentile_Empty(t *testing.T) {
	c := New()
	assert.Equal(t, float64(0), c.LatencyPercentile(50), "empty histogram p50 should be 0")
}

func TestPrometheusHandler(t *testing.T) {
	c := New()
	c.RecordDecision("t1", "p1", true, 100*time.Microsecond)
	c.RecordDecision("t1", "p1", false, 500*time.Microsecond)
	c.PolicyCacheHit.Add(5)
	c.PolicyCacheMiss.Add(1)

	h := PrometheusHandler(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	require.Equal(t, 200, rec.Code)
	body := rec.Body.String()
	expectedLines := []string{
		"rlaas_decisions_total 2",
		"rlaas_decisions_allowed_total 1",
		"rlaas_decisions_denied_total 1",
		"rlaas_policy_cache_hits_total 5",
		"rlaas_policy_cache_misses_total 1",
		"rlaas_decision_latency_us_bucket",
		"rlaas_decision_latency_us_count 2",
		`rlaas_tenant_decisions_allowed_total{tenant_id="t1"} 1`,
		`rlaas_tenant_decisions_denied_total{tenant_id="t1"} 1`,
		`rlaas_policy_decisions_allowed_total{policy_id="p1"} 1`,
	}
	for _, line := range expectedLines {
		assert.True(t, strings.Contains(body, line), "missing in prometheus output: %s", line)
	}
	assert.True(t, strings.Contains(rec.Header().Get("Content-Type"), "text/plain"), "expected text/plain content-type")
}

func TestPerTenantCounters(t *testing.T) {
	c := New()
	for i := 0; i < 100; i++ {
		c.RecordDecision("t1", "", true, time.Microsecond)
	}
	c.tenantMu.RLock()
	v := c.tenantAllowed["t1"].Load()
	c.tenantMu.RUnlock()
	assert.Equal(t, int64(100), v, "expected 100 allowed for t1")
}
