# RLAAS Ruby SDK

Ruby client for the RLAAS HTTP API. Zero runtime dependencies — uses only Ruby stdlib (`net/http`, `json`).

## Installation

```ruby
# Gemfile
gem 'rlaas-sdk', path: 'sdk/ruby'
```

Or install from the published gem:

```bash
gem install rlaas-sdk
```

## Quick start

```ruby
require 'rlaas_sdk'

client = Rlaas::Client.new('http://localhost:8080')

decision = client.check(
  request_id:  'r1',
  org_id:      'acme',
  tenant_id:   'retail',
  signal_type: 'http',
  operation:   'charge',
  endpoint:    '/v1/charge',
  method:      'POST',
  user_id:     'u1'
)

if decision.allowed?
  puts "allowed — #{decision.remaining} remaining"
else
  puts "denied — #{decision.action}: #{decision.reason}"
end
```

## Rails integration

```ruby
# config/initializers/rlaas.rb
RLAAS = Rlaas::Client.new(ENV.fetch('RLAAS_URL', 'http://localhost:8080'))

# app/controllers/application_controller.rb
before_action :enforce_rate_limit

private

def enforce_rate_limit
  decision = RLAAS.check(
    org_id:      current_org.id,
    signal_type: 'http',
    endpoint:    request.path,
    method:      request.method,
    user_id:     current_user&.id
  )
  render json: { error: 'rate_limited', retry_after: decision.retry_after },
         status: :too_many_requests unless decision.allowed?
rescue Rlaas::Error => e
  Rails.logger.warn "RLAAS error (#{e.status_code}): #{e.message}, failing open"
end
```

## Fail-open pattern

```ruby
begin
  decision = client.check(req)
rescue Rlaas::Error => e
  Rails.logger.warn "RLAAS unreachable: #{e.message}"
  return # allow the request through
end
```

## API

| Method | Description |
|--------|-------------|
| `check(**req)` | Returns a `Decision` struct |
| `acquire(body)` | Start a concurrency lease |
| `release(lease_id)` | End a concurrency lease |
| `list_policies` | Array of policy hashes |
| `get_policy(id)` | Single policy hash |
| `create_policy(policy)` | Create a new policy |
| `update_policy(id, policy)` | Replace a policy |
| `delete_policy(id)` | Delete a policy |
| `validate_policy(policy)` | Validate without saving |
| `update_rollout(id, pct)` | Set rollout percentage |
| `rollback_policy(id, ver)` | Roll back to a version |
| `list_policy_audit(id)` | Audit history array |
| `list_policy_versions(id)` | Version list array |
| `analytics_summary(top:)` | Top-N policy stats hash |
| `close` | Closes the persistent HTTP connection |

## Decision struct

```ruby
decision.allowed?       # => true / false
decision.action         # => "allow" / "deny" / "throttle"
decision.reason         # => human-readable string
decision.remaining      # => Integer
decision.retry_after    # => String (e.g. "1s")
decision.policy_id      # => String
```

## Thread safety

Each `Client` instance holds a single persistent `Net::HTTP` connection.
For multi-threaded servers (Puma, Falcon) create a client per thread, or wrap access in a `Mutex`.
