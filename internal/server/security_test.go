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

	"github.com/stretchr/testify/assert"
)

func TestAuthMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	apiKeyCfg := AuthMiddlewareConfig{
		Enabled: true,
		Mode:    AuthModeAPIKey,
		APIKeys: map[string]bool{"my-secret-key": true},
	}

	tests := []struct {
		name       string
		cfg        AuthMiddlewareConfig
		method     string
		path       string
		setupReq   func(*http.Request)
		wantStatus int
	}{
		{
			name:       "disabled auth passes through",
			cfg:        AuthMiddlewareConfig{Enabled: false},
			method:     "GET",
			path:       "/rlaas/v1/policies",
			wantStatus: http.StatusOK,
		},
		{
			name: "exempt path skips auth check",
			cfg: AuthMiddlewareConfig{
				Enabled:     true,
				Mode:        AuthModeAPIKey,
				APIKeys:     map[string]bool{"secret": true},
				ExemptPaths: []string{"/healthz"},
			},
			method:     "GET",
			path:       "/healthz",
			wantStatus: http.StatusOK,
		},
		{
			name:   "valid API key passes",
			cfg:    apiKeyCfg,
			method: "GET",
			path:   "/rlaas/v1/policies",
			setupReq: func(r *http.Request) {
				r.Header.Set("X-Api-Key", "my-secret-key")
			},
			wantStatus: http.StatusOK,
		},
		{
			name:   "invalid API key rejected",
			cfg:    apiKeyCfg,
			method: "GET",
			path:   "/rlaas/v1/policies",
			setupReq: func(r *http.Request) {
				r.Header.Set("X-Api-Key", "wrong-key")
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing API key rejected",
			cfg:        apiKeyCfg,
			method:     "GET",
			path:       "/rlaas/v1/policies",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:   "API key via Bearer header passes",
			cfg:    apiKeyCfg,
			method: "GET",
			path:   "/rlaas/v1/policies",
			setupReq: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer my-secret-key")
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "unknown auth mode returns 500",
			cfg:        AuthMiddlewareConfig{Enabled: true, Mode: "unknown"},
			method:     "GET",
			path:       "/rlaas/v1/policies",
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := AuthMiddleware(tt.cfg, inner)
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.setupReq != nil {
				tt.setupReq(req)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestAuthMiddleware_JWT(t *testing.T) {
	secret := []byte("test-secret")

	tests := []struct {
		name       string
		cfg        AuthMiddlewareConfig
		claims     map[string]interface{}
		method     string
		path       string
		noToken    bool
		wantStatus int
	}{
		{
			name: "valid JWT passes",
			cfg: AuthMiddlewareConfig{
				Enabled:     true,
				Mode:        AuthModeJWT,
				JWTSecret:   secret,
				JWTIssuer:   "rlaas",
				JWTAudience: "rlaas-api",
				AdminRole:   "admin",
			},
			claims: map[string]interface{}{
				"sub":  "user1",
				"role": "admin",
				"iss":  "rlaas",
				"aud":  "rlaas-api",
				"exp":  float64(time.Now().Add(time.Hour).Unix()),
			},
			method:     "POST",
			path:       "/rlaas/v1/policies",
			wantStatus: http.StatusOK,
		},
		{
			name: "expired JWT rejected",
			cfg: AuthMiddlewareConfig{
				Enabled:   true,
				Mode:      AuthModeJWT,
				JWTSecret: secret,
			},
			claims: map[string]interface{}{
				"sub": "user1",
				"exp": float64(time.Now().Add(-time.Hour).Unix()),
			},
			method:     "GET",
			path:       "/rlaas/v1/policies",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong role denied on write",
			cfg: AuthMiddlewareConfig{
				Enabled:   true,
				Mode:      AuthModeJWT,
				JWTSecret: secret,
				AdminRole: "admin",
			},
			claims: map[string]interface{}{
				"sub":  "user1",
				"role": "viewer",
				"exp":  float64(time.Now().Add(time.Hour).Unix()),
			},
			method:     "DELETE",
			path:       "/rlaas/v1/policies/123",
			wantStatus: http.StatusForbidden,
		},
		{
			name: "JWT without exp claim passes",
			cfg: AuthMiddlewareConfig{
				Enabled:   true,
				Mode:      AuthModeJWT,
				JWTSecret: secret,
			},
			claims:     map[string]interface{}{"sub": "user1"},
			method:     "GET",
			path:       "/rlaas/v1/policies",
			wantStatus: http.StatusOK,
		},
		{
			name: "JWT with no issuer or audience config passes",
			cfg: AuthMiddlewareConfig{
				Enabled:   true,
				Mode:      AuthModeJWT,
				JWTSecret: secret,
			},
			claims: map[string]interface{}{
				"sub": "user1",
				"exp": float64(time.Now().Add(time.Hour).Unix()),
			},
			method:     "GET",
			path:       "/rlaas/v1/policies",
			wantStatus: http.StatusOK,
		},
		{
			name: "non-admin role passes on read-only GET",
			cfg: AuthMiddlewareConfig{
				Enabled:   true,
				Mode:      AuthModeJWT,
				JWTSecret: secret,
				AdminRole: "admin",
			},
			claims: map[string]interface{}{
				"sub":  "user1",
				"role": "viewer",
				"exp":  float64(time.Now().Add(time.Hour).Unix()),
			},
			method:     "GET",
			path:       "/rlaas/v1/policies",
			wantStatus: http.StatusOK,
		},
		{
			name: "missing JWT token returns 401",
			cfg: AuthMiddlewareConfig{
				Enabled:   true,
				Mode:      AuthModeJWT,
				JWTSecret: secret,
			},
			noToken:    true,
			method:     "GET",
			path:       "/rlaas/v1/policies",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			h := AuthMiddleware(tt.cfg, inner)
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if !tt.noToken {
				req.Header.Set("Authorization", "Bearer "+makeTestJWT(tt.claims, secret))
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestPanicRecovery(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	})
	h := PanicRecovery(panicking)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "panic should return 500")
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
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "oversized body should be rejected")
}

func TestHTTPServer_ReadinessProbe(t *testing.T) {
	check := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	s := NewHTTPServer(":0", check)

	// Not ready initially.
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "expected 503 before ready")

	// Set ready.
	s.SetReady(true)
	rec = httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "expected 200 after ready")
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
