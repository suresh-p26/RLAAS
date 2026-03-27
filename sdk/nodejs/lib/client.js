"use strict";

const https = require("https");
const http  = require("http");
const { URL } = require("url");

/**
 * @typedef {Object} CheckRequest
 * @property {string}  request_id
 * @property {string}  org_id
 * @property {string}  tenant_id
 * @property {string}  signal_type
 * @property {string}  operation
 * @property {string}  endpoint
 * @property {string}  method
 * @property {string}  [user_id]
 * @property {string}  [api_key]
 * @property {string}  [client_id]
 * @property {string}  [source_ip]
 * @property {string}  [region]
 * @property {string}  [environment]
 * @property {Object}  [tags]
 */

/**
 * @typedef {Object} Decision
 * @property {boolean} allowed
 * @property {string}  action
 * @property {string}  reason
 * @property {number}  remaining
 * @property {string}  retry_after
 * @property {string}  policy_id
 */

class RlaasError extends Error {
  /**
   * @param {number} statusCode
   * @param {string} message
   */
  constructor(statusCode, message) {
    super(`RLAAS API error (${statusCode}): ${message}`);
    this.name = "RlaasError";
    this.statusCode = statusCode;
  }
}

class RlaasClient {
  /**
   * @param {string} baseUrl      - e.g. "http://localhost:8080"
   * @param {Object} [options]
   * @param {number} [options.timeoutMs=5000]
   */
  constructor(baseUrl, { timeoutMs = 5000 } = {}) {
    this._base    = baseUrl.replace(/\/+$/, "");
    this._timeout = timeoutMs;
  }

  // ── Decision API ───────────────────────────────────────────────────────────

  /** @param {CheckRequest} req @returns {Promise<Decision>} */
  async check(req) {
    const body = await this._post("/rlaas/v1/check", compact(req));
    return {
      allowed:     Boolean(body.allowed),
      action:      body.action      ?? "",
      reason:      body.reason      ?? "",
      remaining:   body.remaining   ?? 0,
      retry_after: body.retry_after ?? "",
      policy_id:   body.policy_id   ?? "",
    };
  }

  /** @param {Object} body @returns {Promise<Object>} */
  acquire(body) { return this._post("/rlaas/v1/acquire", body); }

  /** @param {string} leaseId @returns {Promise<Object>} */
  release(leaseId) { return this._post("/rlaas/v1/release", { lease_id: leaseId }); }

  // ── Policy management ──────────────────────────────────────────────────────

  listPolicies()                      { return this._get("/rlaas/v1/policies"); }
  getPolicy(id)                       { return this._get(`/rlaas/v1/policies/${id}`); }
  createPolicy(policy)                { return this._post("/rlaas/v1/policies", policy); }
  updatePolicy(id, policy)            { return this._put(`/rlaas/v1/policies/${id}`, policy); }
  deletePolicy(id)                    { return this._del(`/rlaas/v1/policies/${id}`); }
  validatePolicy(policy)              { return this._post("/rlaas/v1/policies/validate", policy); }

  // ── Lifecycle ──────────────────────────────────────────────────────────────

  updateRollout(id, pct)              { return this._post(`/rlaas/v1/policies/${id}/rollout`, { rollout_percent: pct }); }
  rollbackPolicy(id, version)         { return this._post(`/rlaas/v1/policies/${id}/rollback`, { version }); }

  // ── History ────────────────────────────────────────────────────────────────

  listPolicyAudit(id)                 { return this._get(`/rlaas/v1/policies/${id}/audit`); }
  listPolicyVersions(id)              { return this._get(`/rlaas/v1/policies/${id}/versions`); }

  // ── Analytics ──────────────────────────────────────────────────────────────

  /** @param {number} [top] @returns {Promise<Object>} */
  analyticsSummary(top) {
    const qs = typeof top === "number" ? `?top=${top}` : "";
    return this._get(`/rlaas/v1/analytics/summary${qs}`);
  }

  // ── HTTP helpers ───────────────────────────────────────────────────────────

  _get(path)         { return this._request("GET",    path); }
  _post(path, body)  { return this._request("POST",   path, body); }
  _put(path, body)   { return this._request("PUT",    path, body); }
  _del(path)         { return this._request("DELETE", path); }

  _request(method, path, body) {
    return new Promise((resolve, reject) => {
      const url     = new URL(this._base + path);
      const payload = body != null ? JSON.stringify(body) : null;
      const lib     = url.protocol === "https:" ? https : http;

      const options = {
        hostname: url.hostname,
        port:     url.port || (url.protocol === "https:" ? 443 : 80),
        path:     url.pathname + url.search,
        method,
        headers: {
          "Accept": "application/json",
          ...(payload && {
            "Content-Type":   "application/json",
            "Content-Length": Buffer.byteLength(payload),
          }),
        },
      };

      const req = lib.request(options, (res) => {
        let data = "";
        res.setEncoding("utf8");
        res.on("data", (chunk) => { data += chunk; });
        res.on("end", () => {
          if (res.statusCode >= 400) {
            let msg = data;
            try {
              const parsed = JSON.parse(data);
              if (parsed && parsed.error) msg = parsed.error;
            } catch (_) {}
            return reject(new RlaasError(res.statusCode, msg));
          }
          if (!data.trim()) return resolve(null);
          try { resolve(JSON.parse(data)); }
          catch (e) { reject(e); }
        });
      });

      req.setTimeout(this._timeout, () => {
        req.destroy(new Error(`RLAAS request timed out after ${this._timeout}ms`));
      });

      req.on("error", reject);
      if (payload) req.write(payload);
      req.end();
    });
  }
}

/** Remove null/undefined values from a flat object before sending. */
function compact(obj) {
  return Object.fromEntries(
    Object.entries(obj).filter(([, v]) => v != null && v !== "")
  );
}

module.exports = { RlaasClient, RlaasError };
