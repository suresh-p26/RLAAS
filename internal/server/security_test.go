package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthMiddleware_Disabled(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{Enabled: false}
	h := AuthMiddleware(cfg, inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/policies", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled auth should pass through, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ExemptPath(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{
		Enabled:     true,
		Mode:        AuthModeAPIKey,
		APIKeys:     map[string]bool{"secret": true},
		ExemptPaths: []string{"/healthz"},
	}
	h := AuthMiddleware(cfg, inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("exempt path should pass, got %d", rec.Code)
	}
}

func TestAuthMiddleware_APIKey_Valid(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{
		Enabled: true,
		Mode:    AuthModeAPIKey,
		APIKeys: map[string]bool{"my-secret-key": true},
	}
	h := AuthMiddleware(cfg, inner)

	req := httptest.NewRequest("GET", "/v1/policies", nil)
	req.Header.Set("X-Api-Key", "my-secret-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid API key should pass, got %d", rec.Code)
	}
}

func TestAuthMiddleware_APIKey_Invalid(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{
		Enabled: true,
		Mode:    AuthModeAPIKey,
		APIKeys: map[string]bool{"my-secret-key": true},
	}
	h := AuthMiddleware(cfg, inner)

	req := httptest.NewRequest("GET", "/v1/policies", nil)
	req.Header.Set("X-Api-Key", "wrong-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid API key should get 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_APIKey_Missing(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{
		Enabled: true,
		Mode:    AuthModeAPIKey,
		APIKeys: map[string]bool{"my-secret-key": true},
	}
	h := AuthMiddleware(cfg, inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/policies", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing API key should get 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWT_Valid(t *testing.T) {
	secret := []byte("test-secret")
	token := makeTestJWT(map[string]interface{}{
		"sub":  "user1",
		"role": "admin",
		"iss":  "rlaas",
		"aud":  "rlaas-api",
		"exp":  float64(time.Now().Add(time.Hour).Unix()),
	}, secret)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{
		Enabled:     true,
		Mode:        AuthModeJWT,
		JWTSecret:   secret,
		JWTIssuer:   "rlaas",
		JWTAudience: "rlaas-api",
		AdminRole:   "admin",
	}
	h := AuthMiddleware(cfg, inner)

	req := httptest.NewRequest("POST", "/v1/policies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid JWT should pass, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWT_Expired(t *testing.T) {
	secret := []byte("test-secret")
	token := makeTestJWT(map[string]interface{}{
		"sub": "user1",
		"exp": float64(time.Now().Add(-time.Hour).Unix()),
	}, secret)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{
		Enabled:   true,
		Mode:      AuthModeJWT,
		JWTSecret: secret,
	}
	h := AuthMiddleware(cfg, inner)

	req := httptest.NewRequest("GET", "/v1/policies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired JWT should get 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWT_WrongRole(t *testing.T) {
	secret := []byte("test-secret")
	token := makeTestJWT(map[string]interface{}{
		"sub":  "user1",
		"role": "viewer",
		"exp":  float64(time.Now().Add(time.Hour).Unix()),
	}, secret)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{
		Enabled:   true,
		Mode:      AuthModeJWT,
		JWTSecret: secret,
		AdminRole: "admin",
	}
	h := AuthMiddleware(cfg, inner)

	req := httptest.NewRequest("DELETE", "/v1/policies/123", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong role should get 403, got %d", rec.Code)
	}
}

func TestPanicRecovery(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	})
	h := PanicRecovery(panicking)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic should return 500, got %d", rec.Code)
	}
}

func TestMaxBodyBytes(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		_, err := r.Body.Read(buf)
		if err != nil {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := MaxBodyBytes(10, inner)

	body := strings.NewReader(strings.Repeat("x", 100))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", body))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body should be rejected, got %d", rec.Code)
	}
}

func TestHTTPServer_ReadinessProbe(t *testing.T) {
	check := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	s := NewHTTPServer(":0", check)

	// Not ready initially.
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before ready, got %d", rec.Code)
	}

	// Set ready.
	s.SetReady(true)
	rec = httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after ready, got %d", rec.Code)
	}
}

// makeTestJWT creates a minimal HS256 JWT for testing.
func makeTestJWT(claims map[string]interface{}, secret []byte) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claimsJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(fmt.Sprintf("%s.%s", header, payload)))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s.%s.%s", header, payload, sig)
}
