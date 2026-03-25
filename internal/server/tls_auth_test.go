package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rlaas-io/rlaas/internal/config"
)

// ---------- TLS ----------

func generateTestCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")

	cf, _ := os.Create(certFile)
	_ = pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	cf.Close()

	keyDER, _ := x509.MarshalECPrivateKey(priv)
	kf, _ := os.Create(keyFile)
	_ = pem.Encode(kf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	kf.Close()
	return
}

func TestNewTLSConfig_Disabled(t *testing.T) {
	tc, err := NewTLSConfig(config.TLSConfig{Enabled: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tc != nil {
		t.Fatal("expected nil TLS config when disabled")
	}
}

func TestNewTLSConfig_InvalidKeyPair(t *testing.T) {
	_, err := NewTLSConfig(config.TLSConfig{
		Enabled:  true,
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	})
	if err == nil {
		t.Fatal("expected error for invalid keypair")
	}
}

func TestNewTLSConfig_Success(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	tc, err := NewTLSConfig(config.TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tc == nil || tc.MinVersion != tls.VersionTLS12 {
		t.Fatal("expected TLS 1.2 config")
	}
}

func TestNewTLSConfig_MinVersion13(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	tc, err := NewTLSConfig(config.TLSConfig{
		Enabled:    true,
		CertFile:   certFile,
		KeyFile:    keyFile,
		MinVersion: "1.3",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tc.MinVersion != tls.VersionTLS13 {
		t.Fatal("expected TLS 1.3")
	}
}

func TestNewTLSConfig_WithCA(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	// Use the cert itself as CA for testing.
	tc, err := NewTLSConfig(config.TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
		CAFile:   certFile,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tc.ClientCAs == nil || tc.RootCAs == nil {
		t.Fatal("expected CA pools set")
	}
}

func TestNewTLSConfig_InvalidCAFile(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	_, err := NewTLSConfig(config.TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
		CAFile:   "/nonexistent/ca.pem",
	})
	if err == nil || !strings.Contains(err.Error(), "read ca file") {
		t.Fatalf("expected CA read error, got: %v", err)
	}
}

func TestNewTLSConfig_InvalidCACerts(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	badCA := filepath.Join(dir, "bad_ca.pem")
	os.WriteFile(badCA, []byte("not a cert"), 0644)

	_, err := NewTLSConfig(config.TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
		CAFile:   badCA,
	})
	if err == nil || !strings.Contains(err.Error(), "no valid certs") {
		t.Fatalf("expected invalid CA error, got: %v", err)
	}
}

func TestNewTLSConfig_ClientAuthModes(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	modes := map[string]tls.ClientAuthType{
		"request":            tls.RequestClientCert,
		"require":            tls.RequireAnyClientCert,
		"verify":             tls.VerifyClientCertIfGiven,
		"require_and_verify": tls.RequireAndVerifyClientCert,
		"none":               tls.NoClientCert,
		"":                   tls.NoClientCert,
	}
	for mode, expected := range modes {
		tc, err := NewTLSConfig(config.TLSConfig{
			Enabled:    true,
			CertFile:   certFile,
			KeyFile:    keyFile,
			ClientAuth: mode,
		})
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if tc.ClientAuth != expected {
			t.Fatalf("mode %q: got %v, want %v", mode, tc.ClientAuth, expected)
		}
	}
}

// ---------- Auth ----------

func TestAuthMiddleware_APIKeyFromBearer(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{
		Enabled: true,
		Mode:    AuthModeAPIKey,
		APIKeys: map[string]bool{"my-key": true},
	}
	h := AuthMiddleware(cfg, inner)

	req := httptest.NewRequest("GET", "/v1/policies", nil)
	req.Header.Set("Authorization", "Bearer my-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("API key from Bearer should pass, got %d", rec.Code)
	}
}

func TestAuthMiddleware_UnknownMode(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{
		Enabled: true,
		Mode:    AuthMode("saml"),
	}
	h := AuthMiddleware(cfg, inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/policies", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unknown mode should return 500, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWTNoExpClaim(t *testing.T) {
	secret := []byte("test-secret")
	token := makeTestJWT(map[string]interface{}{
		"sub": "user1",
		"iss": "rlaas",
		"aud": "rlaas-api",
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
	}
	h := AuthMiddleware(cfg, inner)

	req := httptest.NewRequest("GET", "/v1/policies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("JWT without exp should pass, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWTNoIssuerAudienceConfig(t *testing.T) {
	secret := []byte("test-secret")
	token := makeTestJWT(map[string]interface{}{
		"sub": "user1",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
		"iss": "any-issuer",
		"aud": "any-audience",
	}, secret)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := AuthMiddlewareConfig{
		Enabled:   true,
		Mode:      AuthModeJWT,
		JWTSecret: secret,
		// No issuer/audience configured = skip those checks.
	}
	h := AuthMiddleware(cfg, inner)

	req := httptest.NewRequest("GET", "/v1/policies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("JWT with no configured issuer/audience should pass, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWTReadOnlyGETWithNonAdmin(t *testing.T) {
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

	// GET is not mutable, so non-admin role should pass.
	req := httptest.NewRequest("GET", "/v1/policies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("non-admin GET should pass, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWTMissingToken(t *testing.T) {
	cfg := AuthMiddlewareConfig{
		Enabled:   true,
		Mode:      AuthModeJWT,
		JWTSecret: []byte("secret"),
	}
	h := AuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/policies", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing JWT should return 401, got %d", rec.Code)
	}
}

// ---------- Body limit ----------

func TestMaxBodyBytes_Zero(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := MaxBodyBytes(0, inner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("data")))
	if rec.Code != http.StatusOK {
		t.Fatalf("zero maxBytes should passthrough, got %d", rec.Code)
	}
}

func TestMaxBodyBytes_Negative(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := MaxBodyBytes(-1, inner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("data")))
	if rec.Code != http.StatusOK {
		t.Fatalf("negative maxBytes should passthrough, got %d", rec.Code)
	}
}

// ---------- gRPC ----------

func TestNewGRPCServer_NilSrv(t *testing.T) {
	s := NewGRPCServer(":0", nil)
	if s == nil || s.Server == nil {
		t.Fatal("expected grpc server built from scratch")
	}
}

func TestNewGRPCServer_WithOptions(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	tlsCfg, err := NewTLSConfig(config.TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
	})
	if err != nil {
		t.Fatalf("tls: %v", err)
	}

	s := NewGRPCServer(":0", nil,
		WithGRPCTLS(tlsCfg),
		WithGRPCMaxBytes(8<<20, 8<<20),
	)
	if s == nil || s.Server == nil {
		t.Fatal("expected grpc server with options")
	}
}

func TestGRPCGracefulStop_NilServer(t *testing.T) {
	s := &GRPCServer{Addr: ":0", Server: nil}
	s.GracefulStop() // should not panic
}

// ---------- HTTP ----------

func TestHTTPServer_Shutdown_NilServer(t *testing.T) {
	s := &HTTPServer{Addr: ":0"}
	if err := s.Shutdown(time.Second); err != nil {
		t.Fatalf("shutdown nil server should be nil: %v", err)
	}
}

func TestHTTPServer_WrapHandler(t *testing.T) {
	check := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	s := NewHTTPServer(":0", check)
	wrapCalled := false
	s.WrapHandler(func(h http.Handler) http.Handler {
		wrapCalled = true
		return h
	})
	if !wrapCalled {
		// WrapHandler just stores the function, it doesn't call it.
		// It's called during ListenAndServe. Let's verify it's stored.
		if s.wrapFn == nil {
			t.Fatal("expected wrap function to be stored")
		}
	}
}

func TestHTTPServer_Options(t *testing.T) {
	check := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	s := NewHTTPServer(":0", check,
		WithTimeouts(1*time.Second, 2*time.Second, 3*time.Second),
		WithMaxHeaderBytes(512),
		WithTLS(nil),
	)
	if s.readTimeout != time.Second || s.writeTimeout != 2*time.Second || s.idleTimeout != 3*time.Second {
		t.Fatal("timeout options not applied")
	}
	if s.maxHeaderBytes != 512 {
		t.Fatal("max header bytes not applied")
	}
}

// ---------- Validate helpers ----------

func TestIsMutatingMethod(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if !isMutatingMethod(m) {
			t.Fatalf("%s should be mutating", m)
		}
	}
	for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
		if isMutatingMethod(m) {
			t.Fatalf("%s should not be mutating", m)
		}
	}
}

func TestExtractBearerToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer abc123")
	if got := extractBearerToken(r); got != "abc123" {
		t.Fatalf("expected abc123, got %s", got)
	}

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("Authorization", "Basic xyz")
	if got := extractBearerToken(r2); got != "" {
		t.Fatalf("expected empty for non-Bearer, got %s", got)
	}
}

func TestValidateHS256JWT_InvalidStructure(t *testing.T) {
	// Less than 3 parts.
	if _, err := validateHS256JWT("a.b", nil, "", ""); err == nil {
		t.Fatal("expected error for 2-part token")
	}
	// Invalid base64 signature.
	if _, err := validateHS256JWT("a.b.!!!", nil, "", ""); err == nil {
		t.Fatal("expected error for bad base64")
	}
}
