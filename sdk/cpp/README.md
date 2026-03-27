# RLAAS C++ SDK

C++17 client for the RLAAS HTTP API. Uses **libcurl** for transport and **nlohmann/json** for serialisation.

## Requirements

- CMake 3.16+
- libcurl (development headers)
- A C++17-capable compiler (GCC 9+, Clang 10+, MSVC 19.17+)

## Integration

```cmake
include(FetchContent)
FetchContent_Declare(rlaas_sdk
  GIT_REPOSITORY https://github.com/rlaas-io/rlaas
  GIT_TAG        main
  SOURCE_SUBDIR  sdk/cpp
)
FetchContent_MakeAvailable(rlaas_sdk)

target_link_libraries(my_app PRIVATE rlaas::sdk)
```

## Quick start

```cpp
#include "rlaas/client.h"

int main() {
    rlaas::Client client("http://localhost:8080");

    rlaas::CheckRequest req;
    req.request_id  = "r1";
    req.org_id      = "acme";
    req.tenant_id   = "retail";
    req.signal_type = "http";
    req.operation   = "charge";
    req.endpoint    = "/v1/charge";
    req.method      = "POST";
    req.user_id     = "u1";

    try {
        auto d = client.check(req);
        if (d.allowed) {
            // proceed
        } else {
            // d.retry_after_ms gives backoff hint
        }
    } catch (const rlaas::RlaasException& e) {
        // fail open — e.status_code, e.what()
    }
}
```

## API

| Method | Description |
|--------|-------------|
| `check(req)` | Rate-limit decision → `Decision` |
| `acquire(json)` | Start a concurrency lease |
| `release(lease_id)` | End a concurrency lease |
| `list_policies()` | Returns JSON array string |
| `get_policy(id)` | Returns JSON object string |
| `create_policy(json)` | Create a new policy |
| `update_policy(id, json)` | Replace a policy |
| `delete_policy(id)` | Delete a policy |
| `validate_policy(json)` | Validate without saving |
| `update_rollout(id, pct)` | Set rollout percentage |
| `rollback_policy(id, ver)` | Roll back to a version |
| `list_policy_audit(id)` | Audit history |
| `list_policy_versions(id)` | Version list |
| `analytics_summary(top)` | Top-N policy stats |

Policy management methods return raw JSON strings — parse with nlohmann/json or your preferred library.

## Thread safety

A single `Client` instance may be used from multiple threads for concurrent `check()` calls.
Each call acquires the curl handle via an internal lock; for maximum throughput create one `Client` per thread.
