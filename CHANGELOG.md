# Changelog

All notable changes to RLAAS are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0-alpha] — 2026-03-14

### Core Engine
- **7 rate limiting algorithms**: Fixed Window, Sliding Window Log, Sliding Window Counter, Token Bucket, Leaky Bucket, Concurrency (in-flight), Quota
- **Policy engine** with 20+ matching dimensions, specificity scoring, priority-based tie breaking
- **Match expressions** (`match_expr`) for boolean conditions in policy metadata
- **Shadow mode** enforcement for safe policy testing without affecting traffic
- **Gradual rollout** via `rollout_percent` with deterministic FNV hashing
- **Fail-open / fail-closed** configurable per policy
- **Policy lifecycle**: CRUD, versioning, audit trail, rollback, validation

### Multi-Provider Architecture (NEW)
- **Provider SPI** (`internal/provider/`) — provider-agnostic `TelemetryRecord` + `BatchProcessor` + `Adapter` interface + `Registry`
- **OpenTelemetry adapter** (`internal/adapter/otel/`) — log and span batch processing with concurrent workers
- **Datadog adapter** (`internal/adapter/datadog/`) — log and metric filtering for Datadog Agent pipelines
- **Fluent Bit adapter** (`internal/adapter/fluentbit/`) — log record filtering for Fluent Bit/Fluentd pipelines
- **Envoy adapter** (`internal/adapter/envoy/`) — rate limit service and ext_authz integration for service mesh
- **Kafka adapter** (`internal/adapter/kafka/`) — per-topic, per-consumer-group event stream throttling

### APIs
- **HTTP REST API**: `/v1/check`, `/v1/acquire`, `/v1/release`, `/v1/policies`, `/v1/analytics/summary`
- **gRPC API**: `RateLimitService` with proto definitions
- **HTTP Middleware**: `X-RateLimit-*` headers, automatic enforcement
- **gRPC Interceptor**: Unary server interceptor for gRPC services

### Storage Backends
- **Counter stores**: In-memory, Redis, PostgreSQL, Oracle
- **Policy stores**: File (JSON), PostgreSQL, Oracle
- **Policy cache**: TTL-based in-memory cache with configurable refresh

### Multi-Region
- **Weighted allocation** across regions with remainder correction
- **Overflow detection** when per-region counters exceed allocated quota
- **Region-scoped policies** via `scope.region` matching

### Observability
- Prometheus-compatible `/metrics` endpoint
- Decision logging with policy and key context
- Shadow mode metrics for what-if analysis
- OTEL Processor stats (allowed/dropped/errors)

### SDKs
- **Go embedded SDK** (`pkg/rlaas/`) — zero-network-hop integration
- **HTTP client libraries** documented for Python, TypeScript, Java, .NET

### Infrastructure
- **Sidecar agent** (`cmd/rlaas-agent/`) with configurable upstream
- **Control plane** invalidation broker with push-based cache busting
- **Analytics recorder** for decision event tracking

### Examples
- `examples/policies.json` — basic single-service policy
- `examples/fidelity-policies.json` — enterprise multi-service, multi-provider policy set (12 policies)

### Documentation
- Static docs site (`docs/`) with Medium-style design
- Pages: Landing, Design, Features, API Reference, SDKs, Getting Started
- GitHub Pages deployment workflow

### Testing
- 36 test packages, all passing
- Unit tests for every algorithm, adapter, store, and provider
- Benchmarks for HTTP API, evaluation engine, and memory store
- Branch-level coverage tests for edge cases

---

_This is an alpha release. APIs may change. Not yet recommended for production without review._
