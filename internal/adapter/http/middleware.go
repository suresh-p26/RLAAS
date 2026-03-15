package httpadapter

import (
	"context"
	"encoding/json"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"net/http"
	"strconv"
	"strings"
	"time"
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
			// Retry-After is included when the algorithm provides backoff time.
			w.Header().Set("Retry-After", decision.RetryAfter.String())
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		case model.ActionDelay:
			if decision.DelayFor > 0 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(decision.DelayFor):
				}
			}
		}
		w.Header().Set("X-RateLimit-Remaining", int64ToString(decision.Remaining))
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		decision, err := eval.Evaluate(r.Context(), req)
		if err != nil {
			http.Error(w, "rate limiter error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(decision)
	})
}

func sourceIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	return r.RemoteAddr
}

func int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}
