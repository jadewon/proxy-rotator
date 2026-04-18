# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - TBD

Initial public release.

### Added
- HTTP forward proxy (CONNECT + plain HTTP) with SOCKS5 upstream dialing
- Central in-memory proxy pool with Power-of-Two-Choices selection
- Real-time feedback loop: consecutive failures → quarantine + transparent retry for idempotent methods
- Pluggable proxy source system (`Source` interface) with two built-in sources:
  - `file` — static list at `/etc/proxy-rotator/proxies.txt`
  - `freeproxy` — public SOCKS5 list (TheSpeedX/PROXY-List) with adaptive fetch interval when pool falls below `POOL_MIN`
- Routing rules: `MATCH_HOSTS`, `BYPASS_HOSTS`, `DEFAULT_ACTION` (proxy | direct | reject)
- Prometheus metrics (`/metrics`) for pool size, request/upstream results, durations, verification results
- Admin endpoints: `/healthz`, `/pool`
- Distroless Docker image with multi-stage build
- Deployment examples for plain Kubernetes and Istio
