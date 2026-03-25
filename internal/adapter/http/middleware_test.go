package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/pkg/model"
)

type evalStub struct {
	decision model.Decision
	err      error
}

func (e evalStub) Evaluate(_ context.Context, _ model.RequestContext) (model.Decision, error) {
	return e.decision, e.err
}

func TestMiddlewareDenyReturns429(t *testing.T) {
	mw := NewMiddleware(evalStub{decision: model.Decision{Action: model.ActionDeny, RetryAfter: time.Second}})
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 got %d", rr.Code)
	}
}

func TestMiddlewareEvalErrorReturns500(t *testing.T) {
	mw := NewMiddleware(evalStub{err: errors.New("boom")})
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 got %d", rr.Code)
	}
}

func TestMiddlewareAllowPassesThrough(t *testing.T) {
	mw := NewMiddleware(evalStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow, Remaining: 9}})
	called := false
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if !called || rr.Code != http.StatusAccepted {
		t.Fatalf("expected next handler to run")
	}
}

func TestMiddlewareDelayPassesThrough(t *testing.T) {
	mw := NewMiddleware(evalStub{decision: model.Decision{Allowed: true, Action: model.ActionDelay, DelayFor: 1 * time.Millisecond, Remaining: 1}})
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected delayed pass through")
	}
}

func TestCheckHandlerCodes(t *testing.T) {
	h := CheckHandler(evalStub{decision: model.Decision{Allowed: true, Action: model.ActionAllow}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", bytes.NewBufferString("{")))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", rr.Code)
	}

	payload, _ := json.Marshal(model.RequestContext{SignalType: "http"})
	hErr := CheckHandler(evalStub{err: context.DeadlineExceeded})
	rr2 := httptest.NewRecorder()
	hErr.ServeHTTP(rr2, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", bytes.NewBuffer(payload)))
	if rr2.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 got %d", rr2.Code)
	}

	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", bytes.NewBuffer(payload)))
	if rr3.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr3.Code)
	}
}

func TestSourceIPAndInt64String(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	if got := sourceIP(r); got != "10.0.0.1:1234" {
		t.Fatalf("unexpected ip %s", got)
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 2.3.4.5")
	if got := sourceIP(r); got != "1.2.3.4" {
		t.Fatalf("unexpected forwarded ip %s", got)
	}
	if int64ToString(42) != "42" {
		t.Fatalf("unexpected int conversion")
	}
}

func TestSetRateLimitHeaders_WithPolicyAndReset(t *testing.T) {
	rec := httptest.NewRecorder()
	setRateLimitHeaders(rec, model.Decision{
		Remaining:       7,
		MatchedPolicyID: "p-1",
		ResetAt:         time.Now().Add(2 * time.Second),
	})

	h := rec.Header()
	if h.Get("X-RateLimit-Remaining") != "7" {
		t.Fatalf("expected X-RateLimit-Remaining")
	}
	if h.Get("RateLimit-Remaining") != "7" {
		t.Fatalf("expected RateLimit-Remaining")
	}
	if h.Get("RateLimit-Policy") != "p-1" {
		t.Fatalf("expected RateLimit-Policy")
	}
	if h.Get("RateLimit-Reset") == "" {
		t.Fatalf("expected RateLimit-Reset")
	}
}

func TestSetRateLimitHeaders_ResetClampedAtZero(t *testing.T) {
	rec := httptest.NewRecorder()
	setRateLimitHeaders(rec, model.Decision{
		Remaining: 1,
		ResetAt:   time.Now().Add(-1 * time.Second),
	})
	if rec.Header().Get("RateLimit-Reset") != "0" {
		t.Fatalf("expected reset clamped to 0, got %s", rec.Header().Get("RateLimit-Reset"))
	}
}

func TestMiddleware_DelayCapped(t *testing.T) {
	mw := NewMiddleware(evalStub{decision: model.Decision{Allowed: true, Action: model.ActionDelay, DelayFor: 40 * time.Second}})
	called := false
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called {
		t.Fatalf("next handler should not run when context canceled")
	}
}

func TestMiddleware_DelayCanceledByContext(t *testing.T) {
	mw := NewMiddleware(evalStub{decision: model.Decision{Allowed: true, Action: model.ActionDelay, DelayFor: 10 * time.Second}})
	called := false
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called {
		t.Fatalf("next handler should not run when context canceled")
	}
	if strings.TrimSpace(rec.Body.String()) != "" {
		t.Fatalf("expected empty response body for canceled request")
	}
}
