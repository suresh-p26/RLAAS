# RLAAS

RLAAS (Rate Limiting as a Service) is a policy-driven platform for enforcing limits, quotas, and traffic control across APIs and services.

It supports hybrid usage:
- embedded in Go services (low latency)
- centralized HTTP/gRPC decision service
- sidecar-style local proxy mode

## What customers can do today

- Define and manage policies with scoped dimensions (org, tenant, service, endpoint, user, and tags).
- Enforce limits using fixed window, token bucket, sliding window counter, concurrency, and quota algorithms.
- Apply actions such as allow, deny, delay, sample, drop, and shadow-only.
- Use policy safety workflows: validate policy shape and rollback to an earlier version.
- Track policy history (audit + versions) and control rollout percentage.
- Consume runtime decisions through HTTP and gRPC APIs.
- Run sidecar-style local proxy with background policy sync and invalidation intake.

## Current implementation status

### Available now

- Decision APIs
  - `POST /v1/check`
  - `POST /v1/acquire`
  - `POST /v1/release`
- Policy APIs
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
- Analytics & invalidation
  - `GET /v1/analytics/summary` (event + tag aggregation, optional `top` query)
  - in-process invalidation broker + optional push fanout to sidecars
- gRPC runtime
  - `CheckLimit`, `Acquire`, `Release` (from `api/proto/rlaas.proto`)
- Sidecar runtime
  - local proxy `POST /v1/check`
  - health `GET /healthz`
  - status `GET /v1/agent/status`
  - invalidation intake `POST /v1/agent/invalidate`

### Backends available now

- Policy store: JSON file store
- Counter stores: in-memory and Redis

## Local run guide

### Prerequisites

- Go 1.22+

### 1) Install dependencies

```bash
go mod tidy
```

### 2) Run RLAAS server

```bash
go run ./cmd/rlaas-server
```

Optional environment variables:
- `RLAAS_POLICY_FILE` (default: `examples/policies.json`)
- `RLAAS_GRPC_ADDR` (default: `:9090`)
- `RLAAS_INVALIDATION_TARGETS` (comma-separated sidecar base URLs)

### 3) Run sidecar (optional)

```bash
go run ./cmd/rlaas-agent
```

Optional environment variables:
- `RLAAS_AGENT_LISTEN` (default: `:18080`)
- `RLAAS_UPSTREAM_HTTP` (default: `http://localhost:8080`)
- `RLAAS_AGENT_SYNC_SECS` (default: `30`)

### 4) Verify with one HTTP decision call

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

### 5) Run tests

```bash
go test ./...
```

## Customer integration patterns

### Pattern A: Centralized HTTP decisioning

1. Your service sends request context to `POST /v1/check`.
2. RLAAS returns `allowed`, `action`, `reason`, and remaining/retry metadata.
3. Your service enforces the returned action.

### Pattern B: Centralized gRPC decisioning

1. Generate client stubs from `api/proto/rlaas.proto`.
2. Call `CheckLimit` before executing protected operations.
3. Use `Acquire`/`Release` for concurrency workflows.

### Pattern C: Sidecar local proxy

1. Deploy app + sidecar together.
2. App calls sidecar `POST /v1/check` on localhost.
3. Sidecar syncs policies from upstream and accepts invalidation pushes.

## Future scope

- Production PostgreSQL and Oracle policy/counter persistence
- Expanded OTEL processors and production observability pack
- Multi-region control/decision deployment strategy
- Admin/operator UX and policy governance workflows
- Additional SDKs for non-Go ecosystems
- Advanced policy expressions and richer action routing

## Project stage

RLAAS is in early platform stage and ready for local development, integration testing, and controlled adoption.
