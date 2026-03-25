package config

import (
	"os"
	"testing"
)

func TestValidate_AuthOIDCNoIssuer(t *testing.T) {
	c := DefaultConfig()
	c.Auth.Enabled = true
	c.Auth.Mode = "oidc"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for oidc without issuer url")
	}
}

func TestValidate_AuthOIDCValid(t *testing.T) {
	c := DefaultConfig()
	c.Auth.Enabled = true
	c.Auth.Mode = "oidc"
	c.Auth.OIDCIssuerURL = "https://accounts.google.com"
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid oidc config: %v", err)
	}
}

func TestValidate_AuthJWTValid(t *testing.T) {
	c := DefaultConfig()
	c.Auth.Enabled = true
	c.Auth.Mode = "jwt"
	c.Auth.JWTSecret = "supersecret"
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid jwt config: %v", err)
	}
}

func TestValidate_AuthInvalidMode(t *testing.T) {
	c := DefaultConfig()
	c.Auth.Enabled = true
	c.Auth.Mode = "kerberos"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for invalid auth mode")
	}
}

func TestValidate_ClusterInvalidRedisMode(t *testing.T) {
	c := DefaultConfig()
	c.Cluster.Enabled = true
	c.Cluster.RedisMode = "banana"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for invalid redis mode")
	}
}

func TestValidate_ClusterValidModes(t *testing.T) {
	for _, mode := range []string{"", "single", "cluster"} {
		c := DefaultConfig()
		c.Cluster.Enabled = true
		c.Cluster.RedisMode = mode
		if err := c.Validate(); err != nil {
			t.Fatalf("expected valid cluster mode %q: %v", mode, err)
		}
	}
}

func TestValidate_SidecarMode(t *testing.T) {
	c := DefaultConfig()
	c.Mode = "sidecar"
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid sidecar mode: %v", err)
	}
}

func TestValidate_SDKMode(t *testing.T) {
	c := DefaultConfig()
	c.Mode = "sdk"
	if err := c.Validate(); err != nil {
		t.Fatalf("expected valid sdk mode: %v", err)
	}
}

func TestLoadFromEnv_AllRemainingVars(t *testing.T) {
	// Set all the env vars not yet tested in the existing TestLoadFromEnv.
	t.Setenv("RLAAS_GRPC_ADDR", ":7070")
	t.Setenv("RLAAS_READ_TIMEOUT", "3s")
	t.Setenv("RLAAS_WRITE_TIMEOUT", "7s")
	t.Setenv("RLAAS_MAX_BODY_BYTES", "2097152")
	t.Setenv("RLAAS_TLS_CA_FILE", "/etc/ca.pem")
	t.Setenv("RLAAS_AUTH_JWT_SECRET", "secret123")
	t.Setenv("RLAAS_AUTH_JWT_ISSUER", "rlaas-issuer")
	t.Setenv("RLAAS_AUTH_JWT_AUDIENCE", "rlaas-api")
	t.Setenv("RLAAS_AUTH_ADMIN_ROLE", "admin")
	t.Setenv("RLAAS_AUTH_READONLY_ROLE", "reader")
	t.Setenv("RLAAS_AUTH_OIDC_ISSUER_URL", "https://auth.example.com")
	t.Setenv("RLAAS_COUNTER_BACKEND", "redis")
	t.Setenv("RLAAS_REDIS_ADDR", "redis.local:6379")
	t.Setenv("RLAAS_REDIS_PASSWORD", "pass123")
	t.Setenv("RLAAS_POLICY_BACKEND", "postgres")
	t.Setenv("RLAAS_POLICY_DSN", "postgres://localhost/rlaas")
	t.Setenv("RLAAS_CLUSTER_NODE_ID", "node-1")
	t.Setenv("RLAAS_CLUSTER_REGION", "us-east-1")
	t.Setenv("RLAAS_INVALIDATION_DRIVER", "redis")
	t.Setenv("RLAAS_INVALIDATION_ADDR", "redis.local:6379")
	t.Setenv("RLAAS_AUDIT_DSN", "postgres://localhost/audit")
	t.Setenv("RLAAS_LOG_FORMAT", "text")

	// Cleanup happens via t.Setenv's automatic restore.

	c := LoadFromEnv()

	if c.Server.GRPCAddr != ":7070" {
		t.Errorf("grpc addr: got %s", c.Server.GRPCAddr)
	}
	if c.Server.ReadTimeout.String() != "3s" {
		t.Errorf("read timeout: got %v", c.Server.ReadTimeout)
	}
	if c.Server.WriteTimeout.String() != "7s" {
		t.Errorf("write timeout: got %v", c.Server.WriteTimeout)
	}
	if c.Server.MaxBodyBytes != 2097152 {
		t.Errorf("max body bytes: got %d", c.Server.MaxBodyBytes)
	}
	if c.TLS.CAFile != "/etc/ca.pem" {
		t.Errorf("ca file: got %s", c.TLS.CAFile)
	}
	if c.Auth.JWTSecret != "secret123" {
		t.Errorf("jwt secret: got %s", c.Auth.JWTSecret)
	}
	if c.Auth.JWTIssuer != "rlaas-issuer" {
		t.Errorf("jwt issuer: got %s", c.Auth.JWTIssuer)
	}
	if c.Auth.JWTAudience != "rlaas-api" {
		t.Errorf("jwt audience: got %s", c.Auth.JWTAudience)
	}
	if c.Auth.AdminRole != "admin" {
		t.Errorf("admin role: got %s", c.Auth.AdminRole)
	}
	if c.Auth.ReadOnlyRole != "reader" {
		t.Errorf("readonly role: got %s", c.Auth.ReadOnlyRole)
	}
	if c.Auth.OIDCIssuerURL != "https://auth.example.com" {
		t.Errorf("oidc issuer: got %s", c.Auth.OIDCIssuerURL)
	}
	if c.CounterBackend.Driver != "redis" {
		t.Errorf("counter backend: got %s", c.CounterBackend.Driver)
	}
	if c.CounterBackend.Address != "redis.local:6379" {
		t.Errorf("redis addr: got %s", c.CounterBackend.Address)
	}
	if c.CounterBackend.Password != "pass123" {
		t.Errorf("redis password: got %s", c.CounterBackend.Password)
	}
	if c.PolicyBackend.Driver != "postgres" {
		t.Errorf("policy backend: got %s", c.PolicyBackend.Driver)
	}
	if c.PolicyBackend.DSN != "postgres://localhost/rlaas" {
		t.Errorf("policy dsn: got %s", c.PolicyBackend.DSN)
	}
	if c.Cluster.NodeID != "node-1" {
		t.Errorf("node id: got %s", c.Cluster.NodeID)
	}
	if c.Cluster.RegionName != "us-east-1" {
		t.Errorf("region: got %s", c.Cluster.RegionName)
	}
	if c.Cluster.InvalidationDriver != "redis" {
		t.Errorf("invalidation driver: got %s", c.Cluster.InvalidationDriver)
	}
	if c.Cluster.InvalidationAddr != "redis.local:6379" {
		t.Errorf("invalidation addr: got %s", c.Cluster.InvalidationAddr)
	}
	if c.AuditLog.DSN != "postgres://localhost/audit" {
		t.Errorf("audit dsn: got %s", c.AuditLog.DSN)
	}
	if c.Logging.Format != "text" {
		t.Errorf("log format: got %s", c.Logging.Format)
	}
}

func TestLoadFromEnv_InvalidDurations(t *testing.T) {
	// Invalid duration values should be ignored (default preserved).
	t.Setenv("RLAAS_READ_TIMEOUT", "bad")
	t.Setenv("RLAAS_WRITE_TIMEOUT", "also-bad")
	t.Setenv("RLAAS_MAX_BODY_BYTES", "not-a-number")

	c := LoadFromEnv()
	def := DefaultConfig()

	if c.Server.ReadTimeout != def.Server.ReadTimeout {
		t.Errorf("expected default read timeout preserved for invalid value")
	}
	if c.Server.WriteTimeout != def.Server.WriteTimeout {
		t.Errorf("expected default write timeout preserved for invalid value")
	}
	if c.Server.MaxBodyBytes != def.Server.MaxBodyBytes {
		t.Errorf("expected default max body bytes preserved for invalid value")
	}
}

func TestLoadFromEnv_EmptyStringsIgnored(t *testing.T) {
	// Ensure empty env vars don't clobber defaults.
	_ = os.Unsetenv("RLAAS_MODE")
	_ = os.Unsetenv("RLAAS_HTTP_ADDR")
	c := LoadFromEnv()
	def := DefaultConfig()
	if c.Mode != def.Mode {
		t.Errorf("mode should use default when env is empty")
	}
	if c.Server.HTTPAddr != def.Server.HTTPAddr {
		t.Errorf("http addr should use default when env is empty")
	}
}
