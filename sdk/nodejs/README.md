# RLAAS Node.js SDK

Pure JavaScript client for the RLAAS HTTP API. **Zero dependencies** — uses only Node.js stdlib (`http`/`https`). Works with `require()` and `import`. Includes Express and Fastify middleware out of the box.

> **TypeScript users:** use [`@rlaas/sdk`](../typescript/) instead — it ships `.d.ts` declarations and is built for TypeScript-first projects.

## Requirements

Node.js **16+** (no build step needed)

## Installation

```bash
npm install @rlaas/node-sdk
# or
npm install --save sdk/nodejs   # local path
```

## Quick start

```js
const { RlaasClient } = require('@rlaas/node-sdk');

const client = new RlaasClient('http://localhost:8080');

const decision = await client.check({
  request_id:  'r1',
  org_id:      'acme',
  tenant_id:   'retail',
  signal_type: 'http',
  operation:   'charge',
  endpoint:    '/v1/charge',
  method:      'POST',
  user_id:     'u1',
});

if (decision.allowed) {
  console.log(`allowed — ${decision.remaining} remaining`);
} else {
  console.log(`denied — retry after ${decision.retry_after}`);
}
```

ESM / `import` syntax also works:

```js
import { RlaasClient } from '@rlaas/node-sdk';
```

## Express middleware

```js
const express = require('express');
const { RlaasClient } = require('@rlaas/node-sdk');
const { rlaasExpress } = require('@rlaas/node-sdk/middleware');

const app = express();
const client = new RlaasClient(process.env.RLAAS_URL ?? 'http://localhost:8080');

// Apply globally
app.use(rlaasExpress(client, (req) => ({
  org_id:      req.headers['x-org-id'] ?? 'default',
  signal_type: 'http',
  endpoint:    req.path,
  method:      req.method,
  user_id:     req.user?.id,
  api_key:     req.headers['x-api-key'],
})));

// Or on a specific router
app.post('/v1/charge', rlaasExpress(client, (req) => ({
  org_id:      req.body.org_id,
  signal_type: 'http',
  endpoint:    '/v1/charge',
  method:      'POST',
})), chargeHandler);
```

## Fastify middleware

```js
const Fastify = require('fastify');
const { RlaasClient } = require('@rlaas/node-sdk');
const { rlaasPreHandler } = require('@rlaas/node-sdk/middleware');

const fastify = Fastify();
const client  = new RlaasClient('http://localhost:8080');

fastify.addHook('preHandler', rlaasPreHandler(client, (req) => ({
  org_id:      req.headers['x-org-id'],
  signal_type: 'http',
  endpoint:    req.url,
  method:      req.method,
  user_id:     req.user?.id,
})));
```

## Fail-open / fail-closed

```js
// Middleware options
rlaasExpress(client, buildReq, { failOpen: false });  // deny on error
rlaasPreHandler(client, buildReq, { failOpen: true }); // allow on error (default)

// Manual
try {
  const d = await client.check(req);
} catch (err) {
  if (err instanceof RlaasError) {
    console.warn(`RLAAS ${err.statusCode}: ${err.message}`);
  }
  // fail open — allow the request
}
```

## API

| Method | Returns |
|--------|---------|
| `check(req)` | `Promise<Decision>` |
| `acquire(body)` | `Promise<Object>` |
| `release(leaseId)` | `Promise<Object>` |
| `listPolicies()` | `Promise<Array>` |
| `getPolicy(id)` | `Promise<Object>` |
| `createPolicy(policy)` | `Promise<Object>` |
| `updatePolicy(id, policy)` | `Promise<Object>` |
| `deletePolicy(id)` | `Promise<void>` |
| `validatePolicy(policy)` | `Promise<Object>` |
| `updateRollout(id, pct)` | `Promise<Object>` |
| `rollbackPolicy(id, version)` | `Promise<Object>` |
| `listPolicyAudit(id)` | `Promise<Array>` |
| `listPolicyVersions(id)` | `Promise<Array>` |
| `analyticsSummary(top?)` | `Promise<Object>` |

## Decision object

```js
{
  allowed:     true,         // boolean
  action:      'allow',      // 'allow' | 'deny' | 'throttle' | 'drop' | 'shadow'
  reason:      '...',        // human-readable
  remaining:   42,           // requests left in window
  retry_after: '1s',         // hint for 429 responses
  policy_id:   'my-policy',  // matched policy
}
```

## Constructor options

```js
new RlaasClient('http://localhost:8080', {
  timeoutMs: 3000,  // default: 5000
});
```
