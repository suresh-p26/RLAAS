package metrics

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxCardinalityPerMap limits per-dimension maps to prevent memory exhaustion
// under high-cardinality traffic patterns.
const maxCardinalityPerMap = 10_000

// Collector stores lightweight in process counters for core limiter events.
// All counters are safe for concurrent use.
type Collector struct {
	DecisionsTotal     atomic.Int64
	DecisionsAllowed   atomic.Int64
	DecisionsDenied    atomic.Int64
	DecisionsShadow    atomic.Int64
	PolicyCacheHit     atomic.Int64
	PolicyCacheMiss    atomic.Int64
	BackendFailOpen    atomic.Int64
	BackendFailClosed  atomic.Int64
	CounterStoreErrors atomic.Int64

	// Latency histogram for evaluation decisions (microseconds).
	latency *Histogram

	// Per-tenant counters.
	tenantMu      sync.RWMutex
	tenantAllowed map[string]*atomic.Int64
	tenantDenied  map[string]*atomic.Int64

	// Per-policy counters.
	policyMu      sync.RWMutex
	policyAllowed map[string]*atomic.Int64
	policyDenied  map[string]*atomic.Int64
}

// New returns an empty metrics collector with latency histogram.
func New() *Collector {
	return &Collector{
		latency: NewHistogram([]float64{
			50, 100, 250, 500, 1000, 2500, 5000, 10000, 25000, 50000, 100000,
		}),
		tenantAllowed: map[string]*atomic.Int64{},
		tenantDenied:  map[string]*atomic.Int64{},
		policyAllowed: map[string]*atomic.Int64{},
		policyDenied:  map[string]*atomic.Int64{},
	}
}

// RecordDecision records a full decision event with latency and dimensions.
func (c *Collector) RecordDecision(tenantID, policyID string, allowed bool, latency time.Duration) {
	c.DecisionsTotal.Add(1)
	c.latency.Observe(float64(latency.Microseconds()))

	if allowed {
		c.DecisionsAllowed.Add(1)
		if tenantID != "" {
			c.tenantCounter(tenantID, true).Add(1)
		}
		if policyID != "" {
			c.policyCounter(policyID, true).Add(1)
		}
	} else {
		c.DecisionsDenied.Add(1)
		if tenantID != "" {
			c.tenantCounter(tenantID, false).Add(1)
		}
		if policyID != "" {
			c.policyCounter(policyID, false).Add(1)
		}
	}
}

// tenantCounter returns or creates the atomic counter for the given tenant
// and direction (allowed/denied). Returns a throwaway counter when the
// cardinality cap is reached.
func (c *Collector) tenantCounter(id string, allowed bool) *atomic.Int64 {
	m := c.tenantAllowed
	if !allowed {
		m = c.tenantDenied
	}
	c.tenantMu.RLock()
	v, ok := m[id]
	c.tenantMu.RUnlock()
	if ok {
		return v
	}
	c.tenantMu.Lock()
	defer c.tenantMu.Unlock()
	if v, ok = m[id]; ok {
		return v
	}
	if len(m) >= maxCardinalityPerMap {
		return &atomic.Int64{}
	}
	v = &atomic.Int64{}
	m[id] = v
	return v
}

// policyCounter returns or creates the atomic counter for the given policy
// and direction (allowed/denied). Returns a throwaway counter when the
// cardinality cap is reached.
func (c *Collector) policyCounter(id string, allowed bool) *atomic.Int64 {
	m := c.policyAllowed
	if !allowed {
		m = c.policyDenied
	}
	c.policyMu.RLock()
	v, ok := m[id]
	c.policyMu.RUnlock()
	if ok {
		return v
	}
	c.policyMu.Lock()
	defer c.policyMu.Unlock()
	if v, ok = m[id]; ok {
		return v
	}
	if len(m) >= maxCardinalityPerMap {
		return &atomic.Int64{}
	}
	v = &atomic.Int64{}
	m[id] = v
	return v
}

// LatencyPercentile returns the approximate p-th percentile of recorded
// decision latencies in microseconds (p in 0..100).
func (c *Collector) LatencyPercentile(p float64) float64 {
	return c.latency.Percentile(p)
}

// ------------------------------------------------
// Histogram — a simple lock-free histogram that supports Prometheus exposition.
// ------------------------------------------------

// Histogram records observations in fixed buckets.
type Histogram struct {
	bounds []float64
	counts []atomic.Int64 // len = len(bounds)+1 ; last = +Inf
	sum    atomic.Int64
	total  atomic.Int64
}

// NewHistogram creates a histogram with the given upper bounds.
func NewHistogram(bounds []float64) *Histogram {
	sort.Float64s(bounds)
	h := &Histogram{
		bounds: bounds,
		counts: make([]atomic.Int64, len(bounds)+1),
	}
	return h
}

// Observe records one value.
func (h *Histogram) Observe(v float64) {
	idx := sort.SearchFloat64s(h.bounds, v)
	h.counts[idx].Add(1)
	h.sum.Add(int64(v))
	h.total.Add(1)
}

// Percentile returns the approximate value at the p-th percentile (0-100).
func (h *Histogram) Percentile(p float64) float64 {
	total := h.total.Load()
	if total == 0 {
		return 0
	}
	target := int64(math.Ceil(float64(total) * p / 100.0))
	var cum int64
	for i, b := range h.bounds {
		cum += h.counts[i].Load()
		if cum >= target {
			return b
		}
	}
	if len(h.bounds) > 0 {
		return h.bounds[len(h.bounds)-1]
	}
	return 0
}

// ------------------------------------------------
// Prometheus / OpenMetrics exposition handler
// ------------------------------------------------

// PrometheusHandler returns an http.Handler that serves metrics in
// Prometheus text exposition format at /metrics.
func PrometheusHandler(c *Collector) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var b strings.Builder

		// Counters
		writeCounter(&b, "rlaas_decisions_total", c.DecisionsTotal.Load())
		writeCounter(&b, "rlaas_decisions_allowed_total", c.DecisionsAllowed.Load())
		writeCounter(&b, "rlaas_decisions_denied_total", c.DecisionsDenied.Load())
		writeCounter(&b, "rlaas_decisions_shadow_total", c.DecisionsShadow.Load())
		writeCounter(&b, "rlaas_policy_cache_hits_total", c.PolicyCacheHit.Load())
		writeCounter(&b, "rlaas_policy_cache_misses_total", c.PolicyCacheMiss.Load())
		writeCounter(&b, "rlaas_backend_fail_open_total", c.BackendFailOpen.Load())
		writeCounter(&b, "rlaas_backend_fail_closed_total", c.BackendFailClosed.Load())
		writeCounter(&b, "rlaas_counter_store_errors_total", c.CounterStoreErrors.Load())

		// Latency histogram
		b.WriteString("# HELP rlaas_decision_latency_us Decision evaluation latency in microseconds.\n")
		b.WriteString("# TYPE rlaas_decision_latency_us histogram\n")
		var cum int64
		for i, bound := range c.latency.bounds {
			cum += c.latency.counts[i].Load()
			fmt.Fprintf(&b, "rlaas_decision_latency_us_bucket{le=\"%.0f\"} %d\n", bound, cum)
		}
		cum += c.latency.counts[len(c.latency.bounds)].Load()
		fmt.Fprintf(&b, "rlaas_decision_latency_us_bucket{le=\"+Inf\"} %d\n", cum)
		fmt.Fprintf(&b, "rlaas_decision_latency_us_sum %d\n", c.latency.sum.Load())
		fmt.Fprintf(&b, "rlaas_decision_latency_us_count %d\n", c.latency.total.Load())

		// Per-tenant dimensional counters
		writeDimensional(&b, &c.tenantMu, c.tenantAllowed, "rlaas_tenant_decisions_allowed_total", "tenant_id")
		writeDimensional(&b, &c.tenantMu, c.tenantDenied, "rlaas_tenant_decisions_denied_total", "tenant_id")

		// Per-policy dimensional counters
		writeDimensional(&b, &c.policyMu, c.policyAllowed, "rlaas_policy_decisions_allowed_total", "policy_id")
		writeDimensional(&b, &c.policyMu, c.policyDenied, "rlaas_policy_decisions_denied_total", "policy_id")

		_, _ = w.Write([]byte(b.String()))
	})
}

// writeCounter writes a single Prometheus TYPE+value line.
func writeCounter(b *strings.Builder, name string, val int64) {
	fmt.Fprintf(b, "# TYPE %s counter\n%s %d\n", name, name, val)
}

// writeDimensional writes a set of labelled Prometheus counter lines.
func writeDimensional(b *strings.Builder, mu *sync.RWMutex, m map[string]*atomic.Int64, name, label string) {
	mu.RLock()
	defer mu.RUnlock()
	if len(m) == 0 {
		return
	}
	fmt.Fprintf(b, "# TYPE %s counter\n", name)
	for k, v := range m {
		fmt.Fprintf(b, "%s{%s=%q} %d\n", name, label, k, v.Load())
	}
}
