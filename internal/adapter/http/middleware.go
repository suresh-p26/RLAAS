package httpadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rlaas-io/rlaas/pkg/model"
)

// Evaluator is the interface required by HTTP middleware and check handler.
type Evaluator interface {
	Evaluate(ctx context.Context, req model.RequestContext) (model.Decision, error)
}

// Middleware applies rate limit decisions to inbound HTTP requests.
type Middleware struct {
	eval Evaluator
}

// NewMiddleware builds HTTP middleware that evaluates each request.
func NewMiddleware(eval Evaluator) *Middleware {
	return &Middleware{eval: eval}
}

// Handler maps inbound HTTP fields to RequestContext and enforces decisions.
// Response behavior:
// 500 when evaluation fails
// 429 for deny or drop style actions
// pass through for allow and delayed allow
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := model.RequestContext{
			RequestID:   r.Header.Get("X-Request-Id"),
			OrgID:       r.Header.Get("X-Org-Id"),
			TenantID:    r.Header.Get("X-Tenant-Id"),
			Application: r.Header.Get("X-Application"),
			Service:     r.Header.Get("X-Service"),
			SignalType:  "http",
			Operation:   r.URL.Path,
			Endpoint:    r.URL.Path,
			Method:      r.Method,
			UserID:      r.Header.Get("X-User-Id"),
			APIKey:      r.Header.Get("X-Api-Key"),
			ClientID:    r.Header.Get("X-Client-Id"),
			SourceIP:    sourceIP(r),
			Tags:        map[string]string{"host": r.Host},
		}
		decision, err := m.eval.Evaluate(r.Context(), req)
		if err != nil {
			http.Error(w, "rate limiter error", http.StatusInternalServerError)
			return
		}
		switch decision.Action {
		case model.ActionDeny, model.ActionDrop, model.ActionDropLowPriority:
			// RFC draft rate limit headers.
			setRateLimitHeaders(w, decision)
			retrySeconds := int64(decision.RetryAfter.Seconds())
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(retrySeconds, 10))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		case model.ActionDelay:
			const maxDelay = 30 * time.Second
			delay := decision.DelayFor
			if delay > maxDelay {
				delay = maxDelay
			}
			if delay > 0 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(delay):
				}
			}
		}
		setRateLimitHeaders(w, decision)
		next.ServeHTTP(w, r)
	})
}

// CheckHandler exposes a decision API endpoint.
// Response codes:
// 200 with JSON decision on success
// 400 for invalid JSON payload
// 500 when evaluation fails
func CheckHandler(eval Evaluator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req model.RequestContext
		// Body is capped by MaxBodyBytes middleware at the server layer.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
			return
		}
		decision, err := eval.Evaluate(r.Context(), req)
		if err != nil {
			http.Error(w, `{"error":"rate limiter error"}`, http.StatusInternalServerError)
			return
		}
		setRateLimitHeaders(w, decision)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(decision)
	})
}

// sourceIP extracts the client IP from X-Forwarded-For or falls back to
// the connection's RemoteAddr.
func sourceIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	return r.RemoteAddr
}

// int64ToString formats an int64 as a base-10 string.
func int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}

// setRateLimitHeaders writes both RFC draft standard and legacy X- headers.
func setRateLimitHeaders(w http.ResponseWriter, d model.Decision) {
	// Legacy X- headers for backwards compatibility.
	w.Header().Set("X-RateLimit-Remaining", int64ToString(d.Remaining))

	// RFC draft: https://datatracker.ietf.org/doc/draft-ietf-httpapi-ratelimit-headers/
	if d.MatchedPolicyID != "" {
		w.Header().Set("RateLimit-Policy", d.MatchedPolicyID)
	}
	w.Header().Set("RateLimit-Remaining", int64ToString(d.Remaining))
	if !d.ResetAt.IsZero() {
		delta := time.Until(d.ResetAt).Seconds()
		if delta < 0 {
			delta = 0
		}
		w.Header().Set("RateLimit-Reset", strconv.FormatInt(int64(delta), 10))
	}
}
