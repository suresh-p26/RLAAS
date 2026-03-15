package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/suresh-p26/RLAAS/pkg/model"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBufferString("{")))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d", rr.Code)
	}

	payload, _ := json.Marshal(model.RequestContext{SignalType: "http"})
	hErr := CheckHandler(evalStub{err: context.DeadlineExceeded})
	rr2 := httptest.NewRecorder()
	hErr.ServeHTTP(rr2, httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBuffer(payload)))
	if rr2.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 got %d", rr2.Code)
	}

	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, httptest.NewRequest(http.MethodPost, "/v1/check", bytes.NewBuffer(payload)))
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
