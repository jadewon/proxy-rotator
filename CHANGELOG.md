# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Documentation
- New `docs/` tree with deployment and reference guides:
  - `docs/deployment/railway.md` — Railway-specific caveats (HTTPS domain
    incompatible, TCP Proxy required)
  - `docs/deployment/k8s-sidecar.md` — sidecar vs cluster-wide vs external
    patterns
  - `docs/deployment/egress-restricted.md` — architectural guidance for
    443-only VPC egress (the serving component cannot run inside such a
    pod; must be external)
  - `docs/deployment/hosting-options.md` — comparison of hosts (Railway,
    Fly.io, Oracle Cloud, Lightsail, etc.)
  - `docs/references.md` — related open source proxy rotator projects
- `.env.example` — template for all environment variables with inline
  guidance on which are required.
- README: added a strong warning against pointing `TEST_URL` at the
  real crawl target, and linked the new deployment guides.
- PRD: future requirements list extended with `SKIP_VALIDATION` source
  option and `pool-mirror` source plugin for collector/server split
  deployments. Constraints section documents the egress-restricted
  limitation and the `TEST_URL` gotcha.

### Added
- Separate probe endpoints for the three Kubernetes probe kinds:
  `/livez` (always 200 while process is up), `/readyz` (pool has an active
  entry), `/startupz` (first proxy or `STARTUP_GRACE` elapsed). `/healthz`
  remains as a backward-compatible alias for `/readyz`. Deployment/sidecar
  examples now wire `startupProbe`, `livenessProbe`, `readinessProbe`
  distinctly so a drained pool no longer triggers restart loops.

### Security
- Guard also runs on the `ActionDirect` path, so `BYPASS_HOSTS` entries in
  private ranges are blocked unless `ALLOW_PRIVATE_TARGETS=true` is set.
- Direct path pre-resolves the target via the guard and dials the IP,
  closing the DNS-rebinding TOCTOU between guard check and dial.
- `forward()` now honours `TOTAL_TIMEOUT` via `context.WithTimeout`.
- Explicit block for AWS Nitro IPv6 metadata (`fd00:ec2::254`) in addition
  to the generic ULA check.

### Fixed
- Removed dead `-1` branch in `auth.constEqual` (returns false on length
  mismatch).
- Replaced hand-rolled `containsString` with `bytes.Contains` in the
  validator's body-match path.

## [0.1.0] - TBD

Initial public release.

### Added
- HTTP forward proxy (CONNECT + plain HTTP) with SOCKS5 upstream dialing
- Central in-memory proxy pool with Power-of-Two-Choices selection
- Real-time feedback loop: consecutive failures → quarantine + transparent retry for idempotent methods with empty bodies
- Pluggable proxy source system (`Source` interface) with two built-in sources:
  - `file` — static list at `/etc/proxy-rotator/proxies.txt`
  - `freeproxy` — public SOCKS5 list (TheSpeedX/PROXY-List) with adaptive fetch interval when pool falls below `POOL_MIN`
- Routing rules: `MATCH_HOSTS`, `BYPASS_HOSTS`, `DEFAULT_ACTION` (proxy | direct | reject)
- Prometheus metrics (`/metrics`) for pool size, request/upstream results, durations, verification results
- Admin endpoints: `/healthz`, `/pool`
- Distroless Docker image with multi-stage build
- Deployment examples for plain Kubernetes, Istio, and NetworkPolicy

### Security
- Default `LISTEN_ADDR=127.0.0.1` (loopback). Binding beyond loopback requires `PROXY_USERNAME` + `PROXY_PASSWORD`.
- Proxy-Authorization Basic authentication with constant-time comparison.
- SSRF guard rejects targets resolving to RFC1918, loopback, link-local, CGN, and cloud metadata IPs.
- HTTP server hardening: `ReadHeaderTimeout`, `IdleTimeout`, `MaxHeaderBytes`, `MaxBytesReader`.
- CONNECT tunnels carry an idle deadline (`TOTAL_TIMEOUT`) to prevent zombie tunnels.
- Validator supports `VERIFY_MATCH_BODY` for content-based validation; a warning is logged when `TEST_URL` is the example.com placeholder.
- Proxy-Authorization header is stripped before forwarding upstream.
- CI/Release workflows use least-privilege `contents: read` by default; release job scopes `contents: write` narrowly. Releases can also be cut locally via `docker buildx ... --push` when bypassing CI.
