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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rlaas-io/rlaas/internal/config"
)

// ---------- TLS ----------

func generateTestCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err, "gen key")
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
	require.NoError(t, err, "create cert")
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

func TestNewTLSConfig(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)
	badCA := filepath.Join(dir, "bad_ca.pem")
	os.WriteFile(badCA, []byte("not a cert"), 0644)

	tests := []struct {
		name       string
		cfg        config.TLSConfig
		wantErr    bool
		wantNil    bool
		wantMinVer uint16
		check      func(t *testing.T, tc *tls.Config)
	}{
		{
			name:    "disabled returns nil config",
			cfg:     config.TLSConfig{Enabled: false},
			wantNil: true,
		},
		{
			name:    "invalid key pair returns error",
			cfg:     config.TLSConfig{Enabled: true, CertFile: "/nonexistent/cert.pem", KeyFile: "/nonexistent/key.pem"},
			wantErr: true,
		},
		{
			name:       "valid cert/key defaults to TLS 1.2",
			cfg:        config.TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile},
			wantMinVer: tls.VersionTLS12,
		},
		{
			name:       "min version 1.3 applied",
			cfg:        config.TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile, MinVersion: "1.3"},
			wantMinVer: tls.VersionTLS13,
		},
		{
			name: "with CA sets client and root CA pools",
			cfg:  config.TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile, CAFile: certFile},
			check: func(t *testing.T, tc *tls.Config) {
				require.NotNil(t, tc.ClientCAs, "expected ClientCAs set")
				require.NotNil(t, tc.RootCAs, "expected RootCAs set")
			},
		},
		{
			name:    "non-existent CA file returns error",
			cfg:     config.TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile, CAFile: "/nonexistent/ca.pem"},
			wantErr: true,
		},
		{
			name:    "invalid CA cert content returns error",
			cfg:     config.TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile, CAFile: badCA},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc, err := NewTLSConfig(tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				require.Nil(t, tc)
				return
			}
			require.NotNil(t, tc)
			if tt.wantMinVer != 0 {
				assert.Equal(t, tt.wantMinVer, tc.MinVersion)
			}
			if tt.check != nil {
				tt.check(t, tc)
			}
		})
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
		require.NoError(t, err, "mode %q", mode)
		assert.Equal(t, expected, tc.ClientAuth, "mode %q", mode)
	}
}

// ---------- Auth ----------

func TestAuthMiddleware_MaxBodyPassthroughs(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	tests := []struct {
		name     string
		maxBytes int64
	}{
		{"zero maxBytes passes through", 0},
		{"negative maxBytes passes through", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := MaxBodyBytes(tt.maxBytes, inner)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("data")))
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}

// ---------- gRPC ----------

func TestNewGRPCServer_NilSrv(t *testing.T) {
	s := NewGRPCServer(":0", nil)
	require.NotNil(t, s)
	require.NotNil(t, s.Server, "expected grpc server built from scratch")
}

func TestNewGRPCServer_WithOptions(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCert(t, dir)

	tlsCfg, err := NewTLSConfig(config.TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
	})
	require.NoError(t, err, "tls")

	s := NewGRPCServer(":0", nil,
		WithGRPCTLS(tlsCfg),
		WithGRPCMaxBytes(8<<20, 8<<20),
	)
	require.NotNil(t, s)
	require.NotNil(t, s.Server, "expected grpc server with options")
}

func TestGRPCGracefulStop_NilServer(t *testing.T) {
	s := &GRPCServer{Addr: ":0", Server: nil}
	s.GracefulStop() // should not panic
}

// ---------- HTTP ----------

func TestHTTPServer_Shutdown_NilServer(t *testing.T) {
	s := &HTTPServer{Addr: ":0"}
	err := s.Shutdown(time.Second)
	require.NoError(t, err, "shutdown nil server should be nil")
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
		require.NotNil(t, s.wrapFn, "expected wrap function to be stored")
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
	assert.Equal(t, time.Second, s.readTimeout, "readTimeout not applied")
	assert.Equal(t, 2*time.Second, s.writeTimeout, "writeTimeout not applied")
	assert.Equal(t, 3*time.Second, s.idleTimeout, "idleTimeout not applied")
	assert.Equal(t, 512, s.maxHeaderBytes, "max header bytes not applied")
}

// ---------- Validate helpers ----------

func TestIsMutatingMethod(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		assert.True(t, isMutatingMethod(m), "%s should be mutating", m)
	}
	for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
		assert.False(t, isMutatingMethod(m), "%s should not be mutating", m)
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"valid Bearer token extracted", "Bearer abc123", "abc123"},
		{"non-Bearer header returns empty", "Basic xyz", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Authorization", tt.header)
			assert.Equal(t, tt.want, extractBearerToken(r))
		})
	}
}

func TestValidateHS256JWT_InvalidStructure(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"fewer than 3 parts", "a.b"},
		{"invalid base64 signature", "a.b.!!!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateHS256JWT(tt.token, nil, "", "")
			require.Error(t, err)
		})
	}
}
