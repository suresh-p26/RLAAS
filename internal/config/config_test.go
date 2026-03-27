package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	assert.Equal(t, "server", c.Mode)
	assert.Equal(t, 5*time.Second, c.Server.ReadTimeout)
	assert.Equal(t, 10*time.Second, c.Server.WriteTimeout)
	assert.Equal(t, 1<<20, c.Server.MaxHeaderBytes)
	assert.Equal(t, "1.2", c.TLS.MinVersion)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Config)
		wantErr bool
	}{
		{"valid default config", func(_ *Config) {}, false},
		{"invalid mode", func(c *Config) { c.Mode = "invalid" }, true},
		{"TLS enabled without cert or key", func(c *Config) { c.TLS.Enabled = true }, true},
		{"auth apikey with empty key list", func(c *Config) { c.Auth.Enabled = true; c.Auth.Mode = "apikey" }, true},
		{"auth jwt without secret", func(c *Config) { c.Auth.Enabled = true; c.Auth.Mode = "jwt" }, true},
		{"cluster sentinel without master name", func(c *Config) { c.Cluster.Enabled = true; c.Cluster.RedisMode = "sentinel" }, true},
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

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("RLAAS_MODE", "sidecar")
	t.Setenv("RLAAS_HTTP_ADDR", ":9999")
	t.Setenv("RLAAS_TLS_ENABLED", "true")
	t.Setenv("RLAAS_TLS_CERT_FILE", "/etc/cert.pem")
	t.Setenv("RLAAS_TLS_KEY_FILE", "/etc/key.pem")
	t.Setenv("RLAAS_AUTH_ENABLED", "true")
	t.Setenv("RLAAS_AUTH_MODE", "apikey")
	t.Setenv("RLAAS_AUTH_API_KEYS", "key1,key2")
	t.Setenv("RLAAS_CLUSTER_ENABLED", "true")
	t.Setenv("RLAAS_CLUSTER_REDIS_MODE", "sentinel")
	t.Setenv("RLAAS_CLUSTER_SENTINEL_MASTER", "mymaster")
	t.Setenv("RLAAS_CLUSTER_SENTINEL_ADDRS", "host1:26379,host2:26379")
	t.Setenv("RLAAS_AUDIT_ENABLED", "true")
	t.Setenv("RLAAS_AUDIT_DRIVER", "file")
	t.Setenv("RLAAS_AUDIT_FILE", "/var/log/audit.jsonl")
	t.Setenv("RLAAS_LOG_LEVEL", "debug")

	defer func() {
		for _, key := range []string{
			"RLAAS_MODE", "RLAAS_HTTP_ADDR", "RLAAS_TLS_ENABLED",
			"RLAAS_TLS_CERT_FILE", "RLAAS_TLS_KEY_FILE",
			"RLAAS_AUTH_ENABLED", "RLAAS_AUTH_MODE", "RLAAS_AUTH_API_KEYS",
			"RLAAS_CLUSTER_ENABLED", "RLAAS_CLUSTER_REDIS_MODE",
			"RLAAS_CLUSTER_SENTINEL_MASTER", "RLAAS_CLUSTER_SENTINEL_ADDRS",
			"RLAAS_AUDIT_ENABLED", "RLAAS_AUDIT_DRIVER", "RLAAS_AUDIT_FILE",
			"RLAAS_LOG_LEVEL",
		} {
			os.Unsetenv(key)
		}
	}()

	c := LoadFromEnv()

	assert.Equal(t, "sidecar", c.Mode)
	assert.Equal(t, ":9999", c.Server.HTTPAddr)
	assert.True(t, c.TLS.Enabled, "tls should be enabled")
	assert.Equal(t, "/etc/cert.pem", c.TLS.CertFile)
	assert.True(t, c.Auth.Enabled, "auth should be enabled")
	assert.Equal(t, "apikey", c.Auth.Mode)
	assert.Len(t, c.Auth.APIKeys, 2, "expected 2 api keys")
	assert.True(t, c.Cluster.Enabled, "cluster should be enabled")
	assert.Equal(t, "sentinel", c.Cluster.RedisMode)
	assert.Equal(t, "mymaster", c.Cluster.RedisMasterName)
	assert.Len(t, c.Cluster.RedisSentinelAddrs, 2, "expected 2 sentinel addrs")
	assert.True(t, c.AuditLog.Enabled, "audit should be enabled")
	assert.Equal(t, "file", c.AuditLog.Driver)
	assert.Equal(t, "debug", c.Logging.Level)
}
