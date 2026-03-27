package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_AuthModes(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Config)
		wantErr bool
	}{
		{"oidc without issuer url", func(c *Config) { c.Auth.Enabled = true; c.Auth.Mode = "oidc" }, true},
		{"oidc with valid issuer url", func(c *Config) {
			c.Auth.Enabled = true
			c.Auth.Mode = "oidc"
			c.Auth.OIDCIssuerURL = "https://accounts.google.com"
		}, false},
		{"jwt with secret valid", func(c *Config) {
			c.Auth.Enabled = true
			c.Auth.Mode = "jwt"
			c.Auth.JWTSecret = "supersecret"
		}, false},
		{"invalid auth mode", func(c *Config) { c.Auth.Enabled = true; c.Auth.Mode = "kerberos" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultConfig()
			tt.setup(&c)
			err := c.Validate()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidate_ClusterModes(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Config)
		wantErr bool
	}{
		{"invalid redis mode", func(c *Config) { c.Cluster.Enabled = true; c.Cluster.RedisMode = "banana" }, true},
		{"empty redis mode", func(c *Config) { c.Cluster.Enabled = true; c.Cluster.RedisMode = "" }, false},
		{"single redis mode", func(c *Config) { c.Cluster.Enabled = true; c.Cluster.RedisMode = "single" }, false},
		{"cluster redis mode", func(c *Config) { c.Cluster.Enabled = true; c.Cluster.RedisMode = "cluster" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultConfig()
			tt.setup(&c)
			err := c.Validate()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidate_ServerModes(t *testing.T) {
	tests := []struct {
		name string
		mode string
	}{
		{"sidecar mode", "sidecar"},
		{"sdk mode", "sdk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultConfig()
			c.Mode = tt.mode
			require.NoError(t, c.Validate())
		})
	}
}

func TestLoadFromEnv_AllRemainingVars(t *testing.T) {
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

	c := LoadFromEnv()

	assert.Equal(t, ":7070", c.Server.GRPCAddr)
	assert.Equal(t, "3s", c.Server.ReadTimeout.String())
	assert.Equal(t, "7s", c.Server.WriteTimeout.String())
	assert.Equal(t, int64(2097152), c.Server.MaxBodyBytes)
	assert.Equal(t, "/etc/ca.pem", c.TLS.CAFile)
	assert.Equal(t, "secret123", c.Auth.JWTSecret)
	assert.Equal(t, "rlaas-issuer", c.Auth.JWTIssuer)
	assert.Equal(t, "rlaas-api", c.Auth.JWTAudience)
	assert.Equal(t, "admin", c.Auth.AdminRole)
	assert.Equal(t, "reader", c.Auth.ReadOnlyRole)
	assert.Equal(t, "https://auth.example.com", c.Auth.OIDCIssuerURL)
	assert.Equal(t, "redis", c.CounterBackend.Driver)
	assert.Equal(t, "redis.local:6379", c.CounterBackend.Address)
	assert.Equal(t, "pass123", c.CounterBackend.Password)
	assert.Equal(t, "postgres", c.PolicyBackend.Driver)
	assert.Equal(t, "postgres://localhost/rlaas", c.PolicyBackend.DSN)
	assert.Equal(t, "node-1", c.Cluster.NodeID)
	assert.Equal(t, "us-east-1", c.Cluster.RegionName)
	assert.Equal(t, "redis", c.Cluster.InvalidationDriver)
	assert.Equal(t, "redis.local:6379", c.Cluster.InvalidationAddr)
	assert.Equal(t, "postgres://localhost/audit", c.AuditLog.DSN)
	assert.Equal(t, "text", c.Logging.Format)
}

func TestLoadFromEnv_InvalidDurations(t *testing.T) {
	t.Setenv("RLAAS_READ_TIMEOUT", "bad")
	t.Setenv("RLAAS_WRITE_TIMEOUT", "also-bad")
	t.Setenv("RLAAS_MAX_BODY_BYTES", "not-a-number")

	c := LoadFromEnv()
	def := DefaultConfig()

	assert.Equal(t, def.Server.ReadTimeout, c.Server.ReadTimeout, "expected default read timeout preserved for invalid value")
	assert.Equal(t, def.Server.WriteTimeout, c.Server.WriteTimeout, "expected default write timeout preserved for invalid value")
	assert.Equal(t, def.Server.MaxBodyBytes, c.Server.MaxBodyBytes, "expected default max body bytes preserved for invalid value")
}

func TestLoadFromEnv_EmptyStringsIgnored(t *testing.T) {
	_ = os.Unsetenv("RLAAS_MODE")
	_ = os.Unsetenv("RLAAS_HTTP_ADDR")
	c := LoadFromEnv()
	def := DefaultConfig()
	assert.Equal(t, def.Mode, c.Mode, "mode should use default when env is empty")
	assert.Equal(t, def.Server.HTTPAddr, c.Server.HTTPAddr, "http addr should use default when env is empty")
}
