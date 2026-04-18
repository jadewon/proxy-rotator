# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
- CI/Release workflows use least-privilege `contents: read` by default; release job scopes `contents: write` narrowly.
