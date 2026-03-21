package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime settings for sdk, server, and sidecar modes.
type Config struct {
	Mode                 string
	PolicyBackend        PolicyBackendConfig
	CounterBackend       CounterBackendConfig
	CacheTTL             time.Duration
	RefreshInterval      time.Duration
	DefaultFailureMode   string
	MetricsEnabled       bool
	ShadowMetricsEnabled bool
	DecisionLogEnabled   bool

	// Server and security settings.
	Server  ServerConfig
	TLS     TLSConfig
	Auth    AuthConfig
	Logging LoggingConfig

	// Cluster and high-availability settings.
	Cluster ClusterConfig

	// Audit log settings.
	AuditLog AuditLogConfig
}

// ServerConfig holds HTTP/gRPC server tuning parameters.
type ServerConfig struct {
	HTTPAddr         string
	GRPCAddr         string
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	IdleTimeout      time.Duration
	MaxHeaderBytes   int
	MaxBodyBytes     int64
	ShutdownTimeout  time.Duration
	GRPCMaxRecvBytes int
	GRPCMaxSendBytes int
}

// TLSConfig enables TLS / mTLS for HTTP, gRPC, and Redis connections.
type TLSConfig struct {
	Enabled      bool
	CertFile     string
	KeyFile      string
	CAFile       string   // trusted CA bundle for mTLS client verification
	ClientAuth   string   // "none", "request", "require", "verify", "require_and_verify"
	MinVersion   string   // "1.2" or "1.3"; default "1.2"
	CipherSuites []string // empty = Go defaults
	RedisTLS     bool     // enable TLS for Redis connections
}

// AuthConfig configures API authentication and authorization.
type AuthConfig struct {
	Enabled       bool
	Mode          string   // "apikey", "jwt", "oidc"
	APIKeys       []string // allowed API keys (for mode=apikey)
	JWTSecret     string   // HMAC secret (for mode=jwt)
	JWTIssuer     string
	JWTAudience   string
	OIDCIssuerURL string // OIDC discovery URL
	OIDCClientID  string
	AdminRole     string   // role/claim required for control plane mutations
	ReadOnlyRole  string   // role/claim for read-only access
	ExemptPaths   []string // paths that skip auth (e.g., /healthz)
}

// ClusterConfig holds settings for multi-instance / HA deployment.
type ClusterConfig struct {
	Enabled            bool
	NodeID             string
	PeerAddresses      []string
	RedisMode          string // "single", "cluster", "sentinel"
	RedisSentinelAddrs []string
	RedisMasterName    string
	RegionName         string
	PeerRegions        []string
	InvalidationDriver string // "redis", "nats" — driver for distributed invalidation
	InvalidationAddr   string // address for the invalidation transport
}

// AuditLogConfig configures the persistent decision/audit log.
type AuditLogConfig struct {
	Enabled   bool
	Driver    string // "file", "database"
	FilePath  string
	DSN       string
	TableName string
	Retention time.Duration // auto-purge entries older than this
}

// LoggingConfig controls structured logging output.
type LoggingConfig struct {
	Level  string // "debug", "info", "warn", "error"
	Format string // "json", "text"
}

// PolicyBackendConfig selects and configures the policy storage backend.
type PolicyBackendConfig struct {
	Driver           string
	DSN              string
	TableName        string
	UseLegacyAdapter bool
}

// CounterBackendConfig selects and configures the hot path counter backend.
type CounterBackendConfig struct {
	Driver   string
	Address  string
	Password string
	DB       int
	Prefix   string
}

// DefaultConfig returns a Config with safe production defaults.
func DefaultConfig() Config {
	return Config{
		Mode:               "server",
		CacheTTL:           30 * time.Second,
		RefreshInterval:    10 * time.Second,
		DefaultFailureMode: "fail_open",
		MetricsEnabled:     true,
		Server: ServerConfig{
			HTTPAddr:         ":8080",
			GRPCAddr:         ":9090",
			ReadTimeout:      5 * time.Second,
			WriteTimeout:     10 * time.Second,
			IdleTimeout:      120 * time.Second,
			MaxHeaderBytes:   1 << 20,
			MaxBodyBytes:     1 << 20,
			ShutdownTimeout:  15 * time.Second,
			GRPCMaxRecvBytes: 4 << 20,
			GRPCMaxSendBytes: 4 << 20,
		},
		TLS: TLSConfig{
			MinVersion: "1.2",
		},
		Auth: AuthConfig{
			ExemptPaths: []string{"/healthz", "/readyz"},
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// Validate checks required fields and returns descriptive errors.
func (c Config) Validate() error {
	switch c.Mode {
	case "sdk", "server", "sidecar":
	default:
		return fmt.Errorf("config: invalid mode %q (must be sdk, server, or sidecar)", c.Mode)
	}
	if c.TLS.Enabled {
		if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
			return fmt.Errorf("config: tls.cert_file and tls.key_file are required when tls is enabled")
		}
	}
	if c.Auth.Enabled {
		switch c.Auth.Mode {
		case "apikey":
			if len(c.Auth.APIKeys) == 0 {
				return fmt.Errorf("config: at least one api_key is required for apikey auth mode")
			}
		case "jwt":
			if c.Auth.JWTSecret == "" {
				return fmt.Errorf("config: jwt_secret is required for jwt auth mode")
			}
		case "oidc":
			if c.Auth.OIDCIssuerURL == "" {
				return fmt.Errorf("config: oidc_issuer_url is required for oidc auth mode")
			}
		default:
			return fmt.Errorf("config: invalid auth mode %q", c.Auth.Mode)
		}
	}
	if c.Cluster.Enabled {
		switch c.Cluster.RedisMode {
		case "", "single", "cluster", "sentinel":
		default:
			return fmt.Errorf("config: invalid cluster.redis_mode %q", c.Cluster.RedisMode)
		}
		if c.Cluster.RedisMode == "sentinel" && c.Cluster.RedisMasterName == "" {
			return fmt.Errorf("config: cluster.redis_master_name required for sentinel mode")
		}
	}
	return nil
}

// LoadFromEnv populates config fields from environment variables.
// Environment variables use the prefix RLAAS_ with underscores for nesting.
func LoadFromEnv() Config {
	c := DefaultConfig()

	if v := os.Getenv("RLAAS_MODE"); v != "" {
		c.Mode = v
	}
	// Server
	if v := os.Getenv("RLAAS_HTTP_ADDR"); v != "" {
		c.Server.HTTPAddr = v
	}
	if v := os.Getenv("RLAAS_GRPC_ADDR"); v != "" {
		c.Server.GRPCAddr = v
	}
	if v := os.Getenv("RLAAS_READ_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Server.ReadTimeout = d
		}
	}
	if v := os.Getenv("RLAAS_WRITE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Server.WriteTimeout = d
		}
	}
	if v := os.Getenv("RLAAS_MAX_BODY_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.Server.MaxBodyBytes = n
		}
	}

	// TLS
	if os.Getenv("RLAAS_TLS_ENABLED") == "true" {
		c.TLS.Enabled = true
	}
	if v := os.Getenv("RLAAS_TLS_CERT_FILE"); v != "" {
		c.TLS.CertFile = v
	}
	if v := os.Getenv("RLAAS_TLS_KEY_FILE"); v != "" {
		c.TLS.KeyFile = v
	}
	if v := os.Getenv("RLAAS_TLS_CA_FILE"); v != "" {
		c.TLS.CAFile = v
	}

	// Auth
	if os.Getenv("RLAAS_AUTH_ENABLED") == "true" {
		c.Auth.Enabled = true
	}
	if v := os.Getenv("RLAAS_AUTH_MODE"); v != "" {
		c.Auth.Mode = v
	}
	if v := os.Getenv("RLAAS_AUTH_API_KEYS"); v != "" {
		c.Auth.APIKeys = strings.Split(v, ",")
	}
	if v := os.Getenv("RLAAS_AUTH_JWT_SECRET"); v != "" {
		c.Auth.JWTSecret = v
	}
	if v := os.Getenv("RLAAS_AUTH_JWT_ISSUER"); v != "" {
		c.Auth.JWTIssuer = v
	}
	if v := os.Getenv("RLAAS_AUTH_JWT_AUDIENCE"); v != "" {
		c.Auth.JWTAudience = v
	}
	if v := os.Getenv("RLAAS_AUTH_ADMIN_ROLE"); v != "" {
		c.Auth.AdminRole = v
	}
	if v := os.Getenv("RLAAS_AUTH_READONLY_ROLE"); v != "" {
		c.Auth.ReadOnlyRole = v
	}
	if v := os.Getenv("RLAAS_AUTH_OIDC_ISSUER_URL"); v != "" {
		c.Auth.OIDCIssuerURL = v
	}

	// Counter backend
	if v := os.Getenv("RLAAS_COUNTER_BACKEND"); v != "" {
		c.CounterBackend.Driver = v
	}
	if v := os.Getenv("RLAAS_REDIS_ADDR"); v != "" {
		c.CounterBackend.Address = v
	}
	if v := os.Getenv("RLAAS_REDIS_PASSWORD"); v != "" {
		c.CounterBackend.Password = v
	}

	// Policy backend
	if v := os.Getenv("RLAAS_POLICY_BACKEND"); v != "" {
		c.PolicyBackend.Driver = v
	}
	if v := os.Getenv("RLAAS_POLICY_DSN"); v != "" {
		c.PolicyBackend.DSN = v
	}

	// Cluster / HA
	if os.Getenv("RLAAS_CLUSTER_ENABLED") == "true" {
		c.Cluster.Enabled = true
	}
	if v := os.Getenv("RLAAS_CLUSTER_NODE_ID"); v != "" {
		c.Cluster.NodeID = v
	}
	if v := os.Getenv("RLAAS_CLUSTER_REDIS_MODE"); v != "" {
		c.Cluster.RedisMode = v
	}
	if v := os.Getenv("RLAAS_CLUSTER_SENTINEL_ADDRS"); v != "" {
		c.Cluster.RedisSentinelAddrs = strings.Split(v, ",")
	}
	if v := os.Getenv("RLAAS_CLUSTER_SENTINEL_MASTER"); v != "" {
		c.Cluster.RedisMasterName = v
	}
	if v := os.Getenv("RLAAS_CLUSTER_REGION"); v != "" {
		c.Cluster.RegionName = v
	}
	if v := os.Getenv("RLAAS_INVALIDATION_DRIVER"); v != "" {
		c.Cluster.InvalidationDriver = v
	}
	if v := os.Getenv("RLAAS_INVALIDATION_ADDR"); v != "" {
		c.Cluster.InvalidationAddr = v
	}

	// Audit
	if os.Getenv("RLAAS_AUDIT_ENABLED") == "true" {
		c.AuditLog.Enabled = true
	}
	if v := os.Getenv("RLAAS_AUDIT_DRIVER"); v != "" {
		c.AuditLog.Driver = v
	}
	if v := os.Getenv("RLAAS_AUDIT_FILE"); v != "" {
		c.AuditLog.FilePath = v
	}
	if v := os.Getenv("RLAAS_AUDIT_DSN"); v != "" {
		c.AuditLog.DSN = v
	}

	// Logging
	if v := os.Getenv("RLAAS_LOG_LEVEL"); v != "" {
		c.Logging.Level = v
	}
	if v := os.Getenv("RLAAS_LOG_FORMAT"); v != "" {
		c.Logging.Format = v
	}

	return c
}
