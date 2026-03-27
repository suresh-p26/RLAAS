# RLAAS Rust SDK

Async Rust client for the RLAAS HTTP API. Built on **reqwest** + **tokio**.

## Installation

```toml
[dependencies]
rlaas-sdk  = { path = "sdk/rust" }
tokio      = { version = "1", features = ["full"] }
```

## Quick start

```rust
use rlaas_sdk::{Client, CheckRequest};

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let client = Client::new("http://localhost:8080");

    let decision = client.check(&CheckRequest {
        request_id:  "r1".into(),
        org_id:      "acme".into(),
        tenant_id:   "retail".into(),
        signal_type: "http".into(),
        operation:   "charge".into(),
        endpoint:    "/v1/charge".into(),
        method:      "POST".into(),
        user_id:     "u1".into(),
        ..Default::default()
    }).await?;

    if decision.allowed {
        println!("allowed — {} remaining", decision.remaining);
    } else {
        eprintln!("denied — retry after {}ms", decision.retry_after_ms);
    }

    Ok(())
}
```

## Fail-open pattern

```rust
match client.check(&req).await {
    Ok(d) if d.allowed => { /* proceed */ }
    Ok(_)              => { /* rate limited */ }
    Err(e)             => {
        eprintln!("RLAAS unreachable: {e}, failing open");
        // allow the request through
    }
}
```

## API

| Method | Returns |
|--------|---------|
| `check(&req)` | `Decision` |
| `acquire(&body)` | `Value` |
| `release(lease_id)` | `Value` |
| `list_policies()` | `Vec<Policy>` |
| `get_policy(id)` | `Policy` |
| `create_policy(&policy)` | `Value` |
| `update_policy(id, &policy)` | `Value` |
| `delete_policy(id)` | `()` |
| `validate_policy(&policy)` | `Value` |
| `update_rollout(id, pct)` | `Value` |
| `rollback_policy(id, ver)` | `Value` |
| `list_policy_audit(id)` | `Vec<Value>` |
| `list_policy_versions(id)` | `Vec<Value>` |
| `analytics_summary(top)` | `AnalyticsSummary` |

## Types

- `CheckRequest` — all fields are `String` with `Default`; set only what you need.
- `Decision` — `allowed: bool`, `action`, `reason`, `remaining: i64`, `retry_after_ms: i64`, `policy_id`.
- `Policy` — mirrors the JSON policy schema; `scope` and `algorithm` are `serde_json::Value`.
- `RlaasError` — `Api { status, message }`, `Http`, or `Json` variant.

## Cloneability

`Client` is `Clone` — share it freely across tasks without `Arc`.
