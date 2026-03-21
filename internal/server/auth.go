package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// AuthMode selects the authentication strategy.
type AuthMode string

const (
	AuthModeAPIKey AuthMode = "apikey"
	AuthModeJWT    AuthMode = "jwt"
)

// AuthMiddlewareConfig controls authentication behavior.
type AuthMiddlewareConfig struct {
	Enabled bool
	Mode    AuthMode
	// APIKeys is the set of valid API keys accepted for apikey auth mode.
	APIKeys     map[string]bool
	JWTSecret   []byte
	JWTIssuer   string
	JWTAudience string
	// AdminRole is the JWT role required for mutating control-plane endpoints.
	AdminRole string
	// ReadRole is the JWT role that grants read-only access.
	ReadRole string
	// ExemptPaths lists URL paths that bypass authentication (e.g. /healthz).
	ExemptPaths []string
}

// AuthMiddleware enforces authentication on all non-exempt paths.
// Returns 401 Unauthorized when credentials are missing or invalid.
// Returns 403 Forbidden when the caller lacks the required role.
func AuthMiddleware(cfg AuthMiddlewareConfig, next http.Handler) http.Handler {
	if !cfg.Enabled {
		return next
	}
	exempt := make(map[string]bool, len(cfg.ExemptPaths))
	for _, p := range cfg.ExemptPaths {
		exempt[p] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exempt[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		switch cfg.Mode {
		case AuthModeAPIKey:
			key := r.Header.Get("X-Api-Key")
			if key == "" {
				key = r.Header.Get("Authorization")
				key = strings.TrimPrefix(key, "Bearer ")
			}
			if !cfg.APIKeys[key] {
				slog.Warn("auth: invalid api key", "path", r.URL.Path, "ip", r.RemoteAddr)
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		case AuthModeJWT:
			token := extractBearerToken(r)
			if token == "" {
				http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
				return
			}
			claims, err := validateHS256JWT(token, cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience)
			if err != nil {
				slog.Warn("auth: invalid jwt", "error", err, "path", r.URL.Path)
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			// Check role-based access for mutating operations.
			if isMutatingMethod(r.Method) && cfg.AdminRole != "" {
				role, _ := claims["role"].(string)
				if role != cfg.AdminRole {
					http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
					return
				}
			}
		default:
			http.Error(w, `{"error":"auth not configured"}`, http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// extractBearerToken reads the Authorization header and strips the "Bearer " prefix.
func extractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// isMutatingMethod returns true for HTTP methods that modify server state.
func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// validateHS256JWT performs minimal HS256 JWT validation.
// Returns decoded claims or an error.  This avoids pulling a full JWT library
// for the simple HMAC-SHA256 case.
func validateHS256JWT(tokenStr string, secret []byte, issuer, audience string) (map[string]interface{}, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, errInvalidToken
	}

	// Verify signature.
	payload := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errInvalidToken
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(sig, expected) != 1 {
		return nil, errInvalidSignature
	}

	// Decode claims.
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errInvalidToken
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, errInvalidToken
	}

	// Check expiry.
	if exp, ok := claims["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			return nil, errTokenExpired
		}
	}

	// Check issuer.
	if issuer != "" {
		if iss, _ := claims["iss"].(string); iss != issuer {
			return nil, errInvalidIssuer
		}
	}

	// Check audience.
	if audience != "" {
		if aud, _ := claims["aud"].(string); aud != audience {
			return nil, errInvalidAudience
		}
	}

	return claims, nil
}

// authError is a sentinel error type for JWT validation failures.
type authError string

func (e authError) Error() string { return string(e) }

// Sentinel errors returned by validateHS256JWT.
const (
	errInvalidToken     = authError("invalid token")
	errInvalidSignature = authError("invalid signature")
	errTokenExpired     = authError("token expired")
	errInvalidIssuer    = authError("invalid issuer")
	errInvalidAudience  = authError("invalid audience")
)
