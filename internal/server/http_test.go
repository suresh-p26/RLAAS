package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHTTPServerRoutes(t *testing.T) {
	check := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	s := NewHTTPServer(":0", check)

	r1 := httptest.NewRecorder()
	s.Mux.ServeHTTP(r1, httptest.NewRequest(http.MethodPost, "/rlaas/v1/check", nil))
	if r1.Code != http.StatusCreated {
		t.Fatalf("expected check route to be registered")
	}

	r2 := httptest.NewRecorder()
	s.Mux.ServeHTTP(r2, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if r2.Code != http.StatusOK || r2.Body.String() != "ok" {
		t.Fatalf("unexpected healthz response")
	}
}
