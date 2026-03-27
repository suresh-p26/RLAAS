"use strict";

const { RlaasClient } = require("./client");

/**
 * Express middleware factory.
 *
 * @param {RlaasClient} client
 * @param {function(req): Object} buildRequest  - maps Express req → CheckRequest fields
 * @param {Object} [options]
 * @param {boolean} [options.failOpen=true]     - allow on RLAAS error
 * @returns {function} Express middleware
 *
 * @example
 * const { rlaasExpress } = require('@rlaas/node-sdk/middleware');
 * app.use(rlaasExpress(client, (req) => ({
 *   org_id:      req.headers['x-org-id'],
 *   signal_type: 'http',
 *   endpoint:    req.path,
 *   method:      req.method,
 *   user_id:     req.user?.id,
 * })));
 */
function rlaasExpress(client, buildRequest, { failOpen = true } = {}) {
  return async function rlaasMiddleware(req, res, next) {
    try {
      const decision = await client.check(buildRequest(req));
      if (!decision.allowed) {
        return res.status(429).json({
          error:       "rate_limited",
          action:      decision.action,
          reason:      decision.reason,
          retry_after: decision.retry_after,
        });
      }
      next();
    } catch (err) {
      if (failOpen) {
        next();
      } else {
        next(err);
      }
    }
  };
}

/**
 * Fastify plugin factory.
 *
 * @param {RlaasClient} client
 * @param {function(request): Object} buildRequest  - maps Fastify request → CheckRequest fields
 * @param {Object} [options]
 * @param {boolean} [options.failOpen=true]
 * @returns {function} Fastify preHandler hook
 *
 * @example
 * const { rlaasPreHandler } = require('@rlaas/node-sdk/middleware');
 * fastify.addHook('preHandler', rlaasPreHandler(client, (req) => ({
 *   org_id:      req.headers['x-org-id'],
 *   signal_type: 'http',
 *   endpoint:    req.url,
 *   method:      req.method,
 * })));
 */
function rlaasPreHandler(client, buildRequest, { failOpen = true } = {}) {
  return async function rlaasHook(request, reply) {
    try {
      const decision = await client.check(buildRequest(request));
      if (!decision.allowed) {
        reply.code(429).send({
          error:       "rate_limited",
          action:      decision.action,
          reason:      decision.reason,
          retry_after: decision.retry_after,
        });
      }
    } catch (err) {
      if (!failOpen) throw err;
      // failOpen: swallow error and allow the request through
    }
  };
}

module.exports = { rlaasExpress, rlaasPreHandler };
