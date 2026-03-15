# Rate Limiting As A Service (RLAAS)

> **📖 Official Documentation:** [https://suresh-p26.github.io/RLAAS/](https://suresh-p26.github.io/RLAAS/) — features, design document, API reference, SDK guides, and getting started.

RLAAS (Rate Limiting as a Service) is a policy-driven platform for enforcing limits, quotas, and traffic control across APIs and service workloads.

It supports three deployment models:
- Go SDK (embedded, low latency)
- Centralized HTTP/gRPC decision service
- Sidecar local proxy model

## Completed capabilities

### Core rate limiting

- Algorithms: fixed window, token bucket, sliding window counter, concurrency limiter, quota limiter
- Actions: allow, deny, delay, sample, drop, shadow-only
- Policy matching dimensions: org, tenant, app, service, operation, endpoint, method, user, API key, client, region, resource, severity, topic, tags

### Control plane APIs

- Decision:
  - `POST /v1/check`
  - `POST /v1/acquire`
  - `POST /v1/release`
- Policy management:
  - `GET /v1/policies`
  - `POST /v1/policies`
  - `GET /v1/policies/{id}`
  - `PUT /v1/policies/{id}`
  - `DELETE /v1/policies/{id}`
  - `GET /v1/policies/{id}/audit`
  - `GET /v1/policies/{id}/versions`
  - `POST /v1/policies/{id}/rollout`
  - `POST /v1/policies/validate`
  - `POST /v1/policies/{id}/rollback`

### Service and runtime features

- gRPC decision service (proto in `api/proto/rlaas.proto`): `CheckLimit`, `Acquire`, `Release`
- Sidecar endpoints:
  - `POST /v1/check`
  - `GET /healthz`
  - `GET /v1/agent/status`
  - `POST /v1/agent/invalidate`
- Analytics:
  - `GET /v1/analytics/summary` (event and tag aggregation, optional `top`)
- Invalidation:
  - in-process broker
  - async push fanout to sidecars using bounded workers
- Performance features:
  - lock-sharded in-memory counters
  - invalidation burst coalescing in sidecar sync loop
- Multi-region support:
  - weighted proportional allocation of global limits across regions
  - automatic remainder correction for exact total distribution
  - per-region overflow detection (usage vs allocation)
  - OTEL processor primitives for batch log/span filtering with regional awareness
  - advanced `match_expr` support for region-scoped policy expressions
- Non-Go SDKs:
  - Python SDK (`sdk/python`)
  - TypeScript SDK (`sdk/typescript`)
  - Java SDK (`sdk/java`)
  - .NET SDK (`sdk/dotnet`)


### Available backends

- Policy store: JSON file store
- Counter stores: in-memory and Redis

## Quick start (local)

### Prerequisites

- Go 1.25+

### 1) Install dependencies

```bash
go mod tidy
```

### 2) Run server

```bash
go run ./cmd/rlaas-server
```

Optional environment variables:
- `RLAAS_POLICY_FILE` (default: `examples/policies.json`)
- `RLAAS_GRPC_ADDR` (default: `:9090`)
- `RLAAS_INVALIDATION_TARGETS` (comma-separated sidecar base URLs)

### 3) Optional: run sidecar

```bash
go run ./cmd/rlaas-agent
```

Optional environment variables:
- `RLAAS_AGENT_LISTEN` (default: `:18080`)
- `RLAAS_UPSTREAM_HTTP` (default: `http://localhost:8080`)
- `RLAAS_AGENT_SYNC_SECS` (default: `30`)

### 4) Test one decision

```powershell
$body = @{
  request_id = "req-1"
  org_id = "acme"
  tenant_id = "retail"
  signal_type = "http"
  operation = "charge"
  endpoint = "/v1/charge"
  method = "POST"
  user_id = "u1"
} | ConvertTo-Json

Invoke-RestMethod -Method Post -Uri "http://localhost:8080/v1/check" -ContentType "application/json" -Body $body
```

### 5) Run tests and benchmarks

```bash
go test ./...
go test ./benchmarks -run ^$ -bench . -benchmem
```

## Customer integration guide

### Option A: Centralized HTTP

1. Send `RequestContext` to `POST /v1/check`.
2. Read `allowed`, `action`, `reason`, `remaining`, `retry_after`.
3. Enforce behavior in your service.

### Option B: Centralized gRPC

1. Generate stubs from `api/proto/rlaas.proto`.
2. Call `CheckLimit` before protected work.
3. Use `Acquire`/`Release` for concurrency-limited sections.

### Option C: Sidecar local mode

1. Run app and sidecar together.
2. Call sidecar `POST /v1/check` locally.
3. Let sidecar handle sync and invalidation updates.

### Option D: Non-Go SDK clients

- Python SDK: see `sdk/python`
- TypeScript SDK: see `sdk/typescript`
- Java SDK: see `sdk/java`
- .NET SDK: see `sdk/dotnet`

Both SDKs support `check`, `acquire/release`, policy CRUD, validate/rollout/rollback, audit/version APIs, and analytics summary.

## API examples (copy/paste)

Base URL: `http://localhost:8080`

### 1) Check decision

Request:

```bash
curl -X POST http://localhost:8080/v1/check \
  -H "Content-Type: application/json" \
  -d '{
    "request_id":"r1",
    "org_id":"acme",
    "tenant_id":"retail",
    "signal_type":"http",
    "operation":"charge",
    "endpoint":"/v1/charge",
    "method":"POST",
    "user_id":"u1"
  }'
```

Typical response:

```json
{
  "allowed": true,
  "action": "allow",
  "reason": "within_limit",
  "remaining": 99
}
```

### 2) Create policy

Request:

```bash
curl -X POST http://localhost:8080/v1/policies \
  -H "Content-Type: application/json" \
  -d '{
    "policy_id":"payments-limit",
    "name":"Payments limit",
    "enabled":true,
    "priority":100,
    "scope":{"org_id":"acme","signal_type":"http","operation":"charge"},
    "algorithm":{"type":"fixed_window","limit":100,"window":"1m"},
    "action":"deny",
    "failure_mode":"fail_open",
    "enforcement_mode":"enforce",
    "rollout_percent":100
  }'
```

### 3) Validate policy before rollout

Request:

```bash
curl -X POST http://localhost:8080/v1/policies/validate \
  -H "Content-Type: application/json" \
  -d '{
    "name":"Validation sample",
    "enabled":true,
    "scope":{"signal_type":"http","operation":"charge"},
    "algorithm":{"type":"fixed_window","limit":10,"window":"1m"},
    "action":"deny",
    "rollout_percent":50
  }'
```

Response:

```json
{"valid":true}
```

### 4) Update rollout percentage

Request:

```bash
curl -X POST http://localhost:8080/v1/policies/payments-limit/rollout \
  -H "Content-Type: application/json" \
  -d '{"rollout_percent":25}'
```

### 5) Roll back to a previous policy version

Request:

```bash
curl -X POST http://localhost:8080/v1/policies/payments-limit/rollback \
  -H "Content-Type: application/json" \
  -d '{"version":1}'
```

### 6) Query policy history and versions

```bash
curl http://localhost:8080/v1/policies/payments-limit/audit
curl http://localhost:8080/v1/policies/payments-limit/versions
```

### 7) Query analytics summary

```bash
curl http://localhost:8080/v1/analytics/summary
curl "http://localhost:8080/v1/analytics/summary?top=5"
```

## Production readiness checklist

- Run behind TLS termination (ingress/proxy)
- Use Redis for distributed counters
- Configure health probes on `/healthz`
- Use sidecar invalidation targets for faster policy propagation
- Monitor decision and analytics endpoints
- Run benchmark suite for baseline before rollout

## Remaining roadmap (not completed yet)

- Production-grade PostgreSQL policy and counter stores
- Production-grade Oracle policy and counter stores
- Admin/operator UX and policy governance workflows (deferred by plan)

## Project stage

RLAAS is ready for customer integration in controlled production environments, with remaining roadmap focused on enterprise persistence backends and broader SDK/UX surface.
