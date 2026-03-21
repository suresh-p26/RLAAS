package config

import (
	"os"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.Mode != "server" {
		t.Fatalf("expected mode=server, got %s", c.Mode)
	}
	if c.Server.ReadTimeout != 5*time.Second {
		t.Fatalf("expected read timeout 5s, got %v", c.Server.ReadTimeout)
	}
	if c.Server.WriteTimeout != 10*time.Second {
		t.Fatalf("expected write timeout 10s, got %v", c.Server.WriteTimeout)
	}
	if c.Server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("expected max header bytes 1MB, got %d", c.Server.MaxHeaderBytes)
	}
	if c.TLS.MinVersion != "1.2" {
		t.Fatalf("expected TLS 1.2 min, got %s", c.TLS.MinVersion)
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
}

func TestValidate_InvalidMode(t *testing.T) {
	c := DefaultConfig()
	c.Mode = "invalid"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestValidate_TLSMissingCert(t *testing.T) {
	c := DefaultConfig()
	c.TLS.Enabled = true
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing cert/key")
	}
}

func TestValidate_AuthAPIKeyEmpty(t *testing.T) {
	c := DefaultConfig()
	c.Auth.Enabled = true
	c.Auth.Mode = "apikey"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for empty api keys")
	}
}

func TestValidate_AuthJWTNoSecret(t *testing.T) {
	c := DefaultConfig()
	c.Auth.Enabled = true
	c.Auth.Mode = "jwt"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing jwt secret")
	}
}

func TestValidate_ClusterSentinelNoMaster(t *testing.T) {
	c := DefaultConfig()
	c.Cluster.Enabled = true
	c.Cluster.RedisMode = "sentinel"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for sentinel without master name")
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

	// Need to ensure clean env after test
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

	if c.Mode != "sidecar" {
		t.Errorf("mode: got %s, want sidecar", c.Mode)
	}
	if c.Server.HTTPAddr != ":9999" {
		t.Errorf("http addr: got %s, want :9999", c.Server.HTTPAddr)
	}
	if !c.TLS.Enabled {
		t.Error("tls should be enabled")
	}
	if c.TLS.CertFile != "/etc/cert.pem" {
		t.Errorf("cert file: got %s", c.TLS.CertFile)
	}
	if !c.Auth.Enabled || c.Auth.Mode != "apikey" {
		t.Error("auth should be enabled with apikey mode")
	}
	if len(c.Auth.APIKeys) != 2 {
		t.Errorf("expected 2 api keys, got %d", len(c.Auth.APIKeys))
	}
	if !c.Cluster.Enabled || c.Cluster.RedisMode != "sentinel" {
		t.Error("cluster should be enabled with sentinel mode")
	}
	if c.Cluster.RedisMasterName != "mymaster" {
		t.Errorf("master name: got %s", c.Cluster.RedisMasterName)
	}
	if len(c.Cluster.RedisSentinelAddrs) != 2 {
		t.Errorf("expected 2 sentinel addrs, got %d", len(c.Cluster.RedisSentinelAddrs))
	}
	if !c.AuditLog.Enabled || c.AuditLog.Driver != "file" {
		t.Error("audit should be enabled with file driver")
	}
	if c.Logging.Level != "debug" {
		t.Errorf("log level: got %s", c.Logging.Level)
	}
}
