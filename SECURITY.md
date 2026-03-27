# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest (`main`) | Yes |
| older releases | No — upgrade to latest |

## Reporting a Vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Report security issues by emailing **security@rlaas.io** with:

1. A clear description of the vulnerability and its impact
2. Steps to reproduce (proof-of-concept code is welcome)
3. The affected component(s) and version(s)
4. Any mitigations you are aware of

You will receive an acknowledgement within **48 hours** and a triage decision
within **7 days**. Critical vulnerabilities (CVSS ≥ 9.0) are patched and
released within **72 hours** of confirmation.

## Disclosure Policy

We follow coordinated disclosure. Once a fix is released, a CVE will be
requested and the vulnerability details published in the
[GitHub Security Advisories](https://github.com/rlaas-io/rlaas/security/advisories)
tab. We request that reporters honour a **90-day embargo** from the date of
acknowledgement, or until a fix ships — whichever comes first.

## Scope

The following are **in scope**:

- Authentication and authorisation bypass in the HTTP/gRPC API
- Denial-of-service via crafted requests (resource exhaustion)
- Memory/data leakage between tenants (multi-tenancy isolation)
- Privilege escalation via JWT or API key handling
- Supply-chain issues (dependency confusion, malicious package substitution)

The following are **out of scope**:

- Vulnerabilities in third-party dependencies not yet patched upstream
- Attacks requiring valid admin credentials
- Theoretical vulnerabilities without a working proof-of-concept
- Issues in the SDK example code under `examples/`

## Security Design Notes

- All secrets (JWT keys, API keys) are loaded from environment variables or
  Kubernetes Secrets — never stored in source code or config files.
- JWTs are validated with constant-time HMAC comparison to prevent timing
  attacks (`crypto/subtle`).
- The HTTP server enforces OWASP-recommended security headers on every
  response (CSP, HSTS, X-Frame-Options, etc.).
- mTLS is supported for both HTTP and gRPC endpoints.
- The container image runs as a non-root user (`rlaas`, UID 65534) with a
  read-only root filesystem and all Linux capabilities dropped.
- Dependency vulnerability scanning (`govulncheck`) runs on every CI build.
