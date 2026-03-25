package server

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/rlaas-io/rlaas/internal/config"
)

// NewTLSConfig builds a *tls.Config from the application TLS settings.
// Returns nil when TLS is disabled.
func NewTLSConfig(cfg config.TLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("tls: load keypair: %w", err)
	}

	tc := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	switch cfg.MinVersion {
	case "1.3":
		tc.MinVersion = tls.VersionTLS13
	default:
		tc.MinVersion = tls.VersionTLS12
	}

	if cfg.CAFile != "" {
		caPEM, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("tls: read ca file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("tls: no valid certs in ca file")
		}
		tc.ClientCAs = pool
		tc.RootCAs = pool
	}

	switch cfg.ClientAuth {
	case "request":
		tc.ClientAuth = tls.RequestClientCert
	case "require":
		tc.ClientAuth = tls.RequireAnyClientCert
	case "verify":
		tc.ClientAuth = tls.VerifyClientCertIfGiven
	case "require_and_verify":
		tc.ClientAuth = tls.RequireAndVerifyClientCert
	default:
		tc.ClientAuth = tls.NoClientCert
	}

	return tc, nil
}
