package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRecordDecision(t *testing.T) {
	c := New()
	c.RecordDecision("tenant-1", "policy-a", true, 100*time.Microsecond)
	c.RecordDecision("tenant-1", "policy-a", false, 500*time.Microsecond)
	c.RecordDecision("tenant-2", "policy-b", true, 50*time.Microsecond)

	if c.DecisionsTotal.Load() != 3 {
		t.Fatalf("total: got %d, want 3", c.DecisionsTotal.Load())
	}
	if c.DecisionsAllowed.Load() != 2 {
		t.Fatalf("allowed: got %d, want 2", c.DecisionsAllowed.Load())
	}
	if c.DecisionsDenied.Load() != 1 {
		t.Fatalf("denied: got %d, want 1", c.DecisionsDenied.Load())
	}
}

func TestLatencyHistogram(t *testing.T) {
	h := NewHistogram([]float64{10, 50, 100, 500, 1000})
	for i := 0; i < 100; i++ {
		h.Observe(float64(i))
	}
	p50 := h.Percentile(50)
	if p50 < 10 || p50 > 100 {
		t.Fatalf("p50 should be between 10 and 100, got %f", p50)
	}
	p99 := h.Percentile(99)
	if p99 < 100 {
		t.Fatalf("p99 should be >= 100, got %f", p99)
	}
}

func TestLatencyPercentile_Empty(t *testing.T) {
	c := New()
	if p := c.LatencyPercentile(50); p != 0 {
		t.Fatalf("empty histogram p50 should be 0, got %f", p)
	}
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

	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
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
		if !strings.Contains(body, line) {
			t.Errorf("missing in prometheus output: %s", line)
		}
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain content-type, got %s", ct)
	}
}

func TestPerTenantCounters(t *testing.T) {
	c := New()
	// Concurrent writes to same tenant.
	for i := 0; i < 100; i++ {
		c.RecordDecision("t1", "", true, time.Microsecond)
	}
	c.tenantMu.RLock()
	v := c.tenantAllowed["t1"].Load()
	c.tenantMu.RUnlock()
	if v != 100 {
		t.Fatalf("expected 100 allowed for t1, got %d", v)
	}
}
