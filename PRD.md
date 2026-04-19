# proxy-rotator PRD

## Overview

A lightweight gateway that collects and validates SOCKS5 proxies from
multiple sources into a **central pool** and exposes them through a
single HTTP forward proxy endpoint.
One Go binary runs unchanged across Docker, Kubernetes sidecars, or
Kubernetes behind an Istio mesh.
Service code stays untouched — clients just set `HTTP_PROXY` to route
through the rotating pool.

## Background

- External-site crawling frequently breaks because the target blocks
  cloud-provider IP ranges
- Pulling a SOCKS5 agent library directly into service code creates
  runtime-compatibility and complexity issues
- The collect/validate/rotate logic should be extracted from services
  so it can be reused as a general-purpose gateway

## Goals

1. Remove any proxy-related dependency from service code
2. Make proxy sources **pluggable** so new ones can be added without
   touching the core
3. Keep a **central pool** as the single source of truth and let a
   real-time feedback loop quarantine failing proxies
4. Run in any runtime (standalone Docker / plain Kubernetes / Istio)
5. Ship as a single static Go binary with no runtime dependencies

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  K8s Pod                                                     │
│                                                              │
│  [app container]                                             │
│    │ HTTP_PROXY=localhost:3128                               │
│    │ (or transparent routing via Istio VirtualService)       │
│    ▼                                                         │
│  ┌──────────────────────────────────────────────────────┐    │
│  │  proxy-rotator (single Go binary)                    │    │
│  │                                                      │    │
│  │  :3128 HTTP forward proxy (CONNECT + plain HTTP)     │    │
│  │    │                                                 │    │
│  │    ▼  select()                                       │    │
│  │  ┌────────────────────────────────────────────────┐  │    │
│  │  │ Central Pool (sync.RWMutex, single array)      │  │    │
│  │  │                                                │  │    │
│  │  │  [{addr, source, ewmaLatency, successRate,     │  │    │
│  │  │    consecFails, inflight, lastOk, metadata},   │  │    │
│  │  │   ...]                                         │  │    │
│  │  │                                                │  │    │
│  │  │  Select:  power-of-two-choices                 │  │    │
│  │  │  Eject:   consecFails ≥ 3 → quarantine         │  │    │
│  │  │  Revive:  revalidate after quarantine TTL      │  │    │
│  │  └────────────────────────────────────────────────┘  │    │
│  │       ▲                         ▲                    │    │
│  │       │ Add/Remove              │ per-request        │    │
│  │       │                         │ feedback           │    │
│  │  ┌────┴──────────────┐    ┌─────┴──────────────┐     │    │
│  │  │ Source plugins     │    │ RequestTracker     │     │    │
│  │  │ (own goroutines)   │    │                    │     │    │
│  │  │                    │    │ success → EWMA     │     │    │
│  │  │  ┌──────────────┐  │    │ fail    → consec++ │     │    │
│  │  │  │ freeproxy    │  │    │ timeout → fail     │     │    │
│  │  │  └──────────────┘  │    └────────────────────┘     │    │
│  │  │  ┌──────────────┐  │                               │    │
│  │  │  │ file         │  │    ┌────────────────────┐     │    │
│  │  │  └──────────────┘  │    │ Validator (shared) │     │    │
│  │  │  ┌──────────────┐  │◄───│ Check each candidate│    │    │
│  │  │  │ (user-added) │  │    │ against TEST_URL   │     │    │
│  │  │  └──────────────┘  │    └────────────────────┘     │    │
│  │  └────────────────────┘                               │    │
│  │                                                       │    │
│  │  :8080  /livez /readyz /startupz /pool /metrics       │    │
│  └───────────────────────────────────────────────────────┘    │
│                             │                                 │
│                             ▼ SOCKS5                          │
│                     external target site                      │
└──────────────────────────────────────────────────────────────┘
```

## Components

### 1. HTTP forward proxy

| Item | Details |
|------|---------|
| Implementation | Go `net/http/httputil` — CONNECT tunnel + plain HTTP forward |
| Port | 3128 (loopback by default) |
| Upstream dial | SOCKS5 via `golang.org/x/net/proxy` |
| Transparent retry | Up to `MAX_RETRIES` on a different proxy, for idempotent methods only (GET/HEAD/OPTIONS) |
| Timeouts | `PER_PROXY_TIMEOUT` per attempt, `TOTAL_TIMEOUT` including retries |

### 2. Central Pool

**Single source of truth.** All source plugins write into this array;
the HTTP proxy always selects from it.

```go
type Entry struct {
    Addr         string             // "host:port"
    Source       string             // which plugin added it (for observability)
    Auth         *Auth              // SOCKS5 user/pass (optional)
    EwmaLatency  time.Duration      // exponentially weighted moving avg latency
    SuccessRate  float64            // recent-window success rate
    ConsecFails  int                // consecutive failures (drives quarantine)
    Inflight     int32              // in-flight requests (for P2C)
    LastOk       time.Time          // last success timestamp
    Metadata     map[string]string  // country, tier, etc. for future use
    State        State              // active | quarantine
}
```

**Selection strategy**: power-of-two-choices — pick two entries at
random, send the request to the one with lower `Inflight`. Avoids
concentrating load on a single proxy.

**Quarantine**: once `ConsecFails ≥ EJECT_CONSEC_FAILS`, the entry
transitions to `quarantine` and is revalidated after
`QUARANTINE_DURATION`.

**Deduplication**: if multiple sources add the same `Addr`, the later
adds are ignored. The first registrant keeps the `Source` field.

### 3. Source plugins

Each source runs in its own goroutine with its own fetch cadence and
backoff. It receives the shared `Validator` by dependency injection
and writes validated entries into the pool.

#### Interface

```go
type Source interface {
    Name() string
    Run(ctx context.Context, pool *Pool, validator Validator) error
}

type Validator interface {
    // Nil if the proxy correctly responds to TEST_URL; error otherwise.
    Validate(ctx context.Context, proxy RawProxy) error
}

type RawProxy struct {
    Addr     string
    Auth     *Auth
    Metadata map[string]string
}
```

#### Canonical pattern

```go
func (s *MySource) Run(ctx context.Context, pool *Pool, v Validator) error {
    tick := time.NewTicker(s.interval)
    for {
        select {
        case <-ctx.Done(): return nil
        case <-tick.C:
        }
        raws, err := s.fetch(ctx)          // source-specific fetch
        if err != nil { continue }         // log/backoff internally
        for _, r := range raws {
            if err := v.Validate(ctx, r); err != nil { continue }
            pool.Add(Entry{Addr: r.Addr, Source: s.Name(), ...})
        }
    }
}
```

**Key principles**:
- Plugins are autonomous over their own cadence, error handling, and
  retry policy
- Validation is delegated to the shared `Validator` for a consistent
  quality bar
- Plugins only add to the pool; removal is handled by the
  RequestTracker (live feedback) and the quarantine logic

#### Built-in plugins

| Name | Description |
|------|-------------|
| `freeproxy` | Fetches `socks5.txt` from a public proxy list (TheSpeedX/PROXY-List by default) |
| `file` | Reads a static list from `/etc/proxy-rotator/proxies.txt` (testing / manual override) |

A new source is one file under `internal/sources/<name>/` plus a
single `sources.Register()` call in `cmd/main.go`.

### 4. Validator (shared)

| Item | Details |
|------|---------|
| Check | GET `TEST_URL` through the candidate SOCKS5; accept HTTP 200 |
| Timeout | `TEST_TIMEOUT` (default 8s) |
| Concurrency | Sources run their own goroutine pool; the validator is per-call stateless |

### 5. RequestTracker (feedback loop)

The HTTP proxy handler reflects every request outcome back into the
pool.

```
success: update EwmaLatency, ConsecFails=0, LastOk=now
failure: ConsecFails++, quarantine if over threshold
```

Catches proxies that pass periodic validation but die during real use
— something a purely periodic approach misses.

## Routing rules

Three layers are supported. Pick one or combine them.

### Layer A: app env vars (most common)

Runtime-agnostic; every language's HTTP client respects these.

```
HTTP_PROXY=http://localhost:3128
HTTPS_PROXY=http://localhost:3128
NO_PROXY=.cluster.local,.svc,localhost,127.0.0.1
```

### Layer B: proxy-rotator's own match rules

Apps send everything to the proxy; the rotator filters per-host.

```
MATCH_HOSTS=target-site.com,*.another.com   # these go through SOCKS5
BYPASS_HOSTS=.cluster.local,.internal       # these go direct
DEFAULT_ACTION=direct                       # reject | direct | proxy
```

### Layer C: Istio VirtualService (Istio users only)

No app-side env vars. Istio transparently routes selected hosts to the
rotator.

```yaml
apiVersion: networking.istio.io/v1beta1
kind: ServiceEntry
metadata: {name: target-external}
spec:
  hosts: ["target-site.com"]
  ports: [{number: 443, name: https, protocol: TLS}]
  resolution: DNS
  location: MESH_EXTERNAL
---
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata: {name: target-via-rotator}
spec:
  hosts: ["target-site.com"]
  http:
  - route:
    - destination:
        host: proxy-rotator.default.svc.cluster.local
        port: {number: 3128}
```

Because all three layers must work with the same image, the rotator
stays simple: "handle any request I receive according to the
configured rules."

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_PORT` | `3128` | HTTP proxy port |
| `ADMIN_PORT` | `8080` | `/livez`, `/readyz`, `/startupz`, `/healthz`, `/metrics`, `/pool` |
| `TEST_URL` | `https://example.com` | Validation target (set to a neutral endpoint — see Constraints) |
| `TEST_TIMEOUT` | `8s` | Validation timeout |
| `PER_PROXY_TIMEOUT` | `8s` | Per-attempt upstream timeout |
| `TOTAL_TIMEOUT` | `30s` | Total budget including retries |
| `MAX_RETRIES` | `2` | Retry count for idempotent methods |
| `POOL_MIN` | `5` | Below this, source plugins may switch to accelerated fetch |
| `POOL_MAX` | `50` | Upper pool bound |
| `EJECT_CONSEC_FAILS` | `3` | Quarantine threshold |
| `QUARANTINE_DURATION` | `5m` | Wait before revalidation |
| `MATCH_HOSTS` | (empty) | Host patterns routed via SOCKS5 (comma separated) |
| `BYPASS_HOSTS` | `.cluster.local,.svc,localhost` | Host patterns dialed directly |
| `DEFAULT_ACTION` | `proxy` | `proxy` \| `direct` \| `reject` |
| `SOURCES` | `freeproxy` | Enabled source plugins (comma separated) |
| `SOURCE_FREEPROXY_URL` | TheSpeedX/PROXY-List socks5.txt | Upstream URL for `freeproxy` |
| `SOURCE_FREEPROXY_INTERVAL` | `10m` | Steady fetch interval |
| `SOURCE_FREEPROXY_INTERVAL_LOW` | `30s` | Accelerated interval when `POOL_MIN` is not met |
| `SOURCE_FREEPROXY_CONCURRENCY` | `20` | Parallel validations |
| `SOURCE_FREEPROXY_HTTP_TIMEOUT` | `30s` | HTTP GET timeout for the source fetch |
| `SOURCE_FILE_PATH` | `/etc/proxy-rotator/proxies.txt` | Path for the `file` plugin |
| `SOURCE_FILE_INTERVAL` | `60s` | Rescan interval for the `file` plugin |
| `SOURCE_FILE_CONCURRENCY` | `10` | Parallel validations for the `file` plugin |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

## Deployment scenarios

### 1. Docker standalone

```bash
docker run --rm -p 3128:3128 -p 8080:8080 \
  -e TEST_URL=https://www.cloudflare.com/cdn-cgi/trace \
  -e SOURCES=freeproxy \
  jadewon/proxy-rotator:latest
```

Apps on the host set `HTTP_PROXY=http://host.docker.internal:3128` (or
the corresponding Compose service name).

### 2. Kubernetes without Istio

Use `deploy/examples/deployment.yaml`. Deploy as a standalone
`Deployment + Service` shared across workloads in the cluster:

```yaml
# client Deployment
env:
  - name: HTTP_PROXY
    value: "http://proxy-rotator.default.svc:3128"
  - name: HTTPS_PROXY
    value: "http://proxy-rotator.default.svc:3128"
  - name: NO_PROXY
    value: ".cluster.local,.svc,localhost"
```

Alternatively merge `deploy/examples/sidecar.yaml` into a Deployment's
`containers` list for the per-pod sidecar model.

### 3. Kubernetes + Istio (optional)

Apply `deploy/examples/istio.yaml` (`ServiceEntry` + `VirtualService`)
to route selected hosts through the rotator without any app-side
config. Independent of, and compatible with, pattern 2.

## Probes

| Endpoint | Kubernetes probe | Returns |
|----------|------------------|---------|
| `GET /livez` | livenessProbe | Always 200 while the server is running |
| `GET /readyz` | readinessProbe | 200 if pool has ≥1 active entry, 503 otherwise |
| `GET /startupz` | startupProbe | 200 once the first proxy arrives or `STARTUP_GRACE` elapses |
| `GET /healthz` | (legacy alias for `/readyz`) | |
| `GET /pool` | debug | JSON snapshot of the pool |
| `GET /metrics` | Prometheus | — |

## Observability

**Prometheus metrics**:
- `proxy_pool_size{state="active|quarantine"}`
- `proxy_pool_source_size{source="freeproxy|file|..."}`
- `proxy_request_total{result="success|fail|rejected|direct"}`
- `proxy_request_duration_seconds` (histogram)
- `proxy_upstream_total{result="success|fail"}`
- `proxy_verify_total{source, result}`

**Tracing**: Under Istio, Envoy propagates W3C Trace Context
automatically. Without a mesh, add an `otelhttp` middleware if
tracing is needed (not shipped in the initial release).

## Project layout

```
proxy-rotator/
├── PRD.md
├── README.md
├── LICENSE
├── go.mod / go.sum
├── cmd/proxy-rotator/main.go          # source registration + dependency wiring
├── internal/
│   ├── pool/
│   │   ├── pool.go                    # central pool, selection strategy
│   │   └── entry.go
│   ├── proxy/
│   │   └── handler.go                 # HTTP CONNECT / forward, retries
│   ├── validator/
│   │   └── validator.go               # TEST_URL validation
│   ├── router/
│   │   └── router.go                  # MATCH / BYPASS / DEFAULT rules
│   ├── auth/
│   │   └── auth.go                    # Proxy-Authorization handling
│   ├── guard/
│   │   └── guard.go                   # SSRF / DNS-rebinding guard
│   ├── sources/
│   │   ├── source.go                  # Source interface
│   │   ├── freeproxy/freeproxy.go
│   │   └── file/file.go
│   ├── metrics/metrics.go
│   └── envutil/envutil.go
├── docs/
│   ├── deployment/                    # railway / k8s-sidecar / egress-restricted / hosting-options
│   └── references.md
├── deploy/
│   ├── Dockerfile                     # multi-stage, distroless
│   └── examples/                      # deployment / sidecar / Istio / NetworkPolicy
└── .github/workflows/                 # CI + Release
```

## Milestones

| Stage | Content |
|-------|---------|
| M1 | Pool + HTTP proxy + `file` source running locally (Docker) |
| M2 | `freeproxy` source + validator + RequestTracker + quarantine/retry |
| M3 | Metrics + deployment examples (Docker / K8s / Istio) |
| M4 | README + public OSS release |
| M5 | Integration into real services / production operation |

## Constraints

- Free public proxy reliability is inherently noisy. Transparent
  retries plus real-time feedback soften this but cannot eliminate it.
- Public SOCKS5 proxies usually don't support authentication. The
  `Auth` field exists for future paid-source extensions.
- The pool is per-instance and in-memory; replicas do not share state.
- **proxy-rotator cannot run inside a network segment whose egress is
  restricted to 443.** SOCKS5 dials require arbitrary high ports
  (1080, 4145, 9050, …), so the serving component must live where
  outbound is unrestricted. See
  `docs/deployment/egress-restricted.md`.
- **`TEST_URL` must not be pointed at the site you actually want to
  crawl.** Every candidate in every fetch cycle hits this URL, so a
  full cycle looks like a distributed attack coming from thousands of
  IPs — which hardens the target's blocking instead of working around
  it. Use a neutral endpoint (e.g.
  `https://www.cloudflare.com/cdn-cgi/trace`); RequestTracker takes
  care of pruning proxies that actually fail against the real target
  during live usage.

## Future requirements (low priority)

Not in the initial implementation, but **interface stubs and extension
points are preserved** for these.

### Routing / selection
- Geographic routing (select by country) — `Entry.Metadata["country"]` stub
- Sticky sessions (same cookie/header hash → same proxy)
- Named pools per target
- Priority tiers (prefer paid proxies) — `Entry.Metadata["tier"]` stub

### Proxy sources
- Paid proxy API plugins (Bright Data, Smartproxy, …)
- Privately owned proxy plugins (known-good upstreams)
- SOCKS5 authentication (username/password)
- SOCKS4 / HTTP-proxy backends
- **`SKIP_VALIDATION` option** (per source) — skip redundant validation
  when a deployment only pulls an already-validated list (e.g. in a
  collector/server split)
- **`pool-mirror` source plugin** — mirrors another proxy-rotator
  instance's `/pool` JSON. Combined with `SKIP_VALIDATION`, this
  officially supports the "external validation node + internal
  serving node" architecture

### Quality / control
- Content-based validation (detect CAPTCHA / block pages)
- Per-proxy rate limiting (token bucket)
- Per-target circuit breaker
- Warm start (persist the pool to disk; restore on restart)
- Weighted selection by response time

### Observability
- Admin API (manual ban / add)
- ConfigMap hot-reload (pick up env-var changes without restart)

### Security
- Host allowlist (prevent abuse)
- Proxy-level auth (Basic / mTLS) for multi-tenant deployments
- User-Agent rotation / header injection

### Deployment / scale
- DaemonSet mode (per-node sharing)
- Pool sharing across replicas (Redis backend)
- HPA driven by pool-depletion rate

### Protocols
- HTTP/2 upstream
- SOCKS5 remote DNS (resolve at the upstream)
- HTTP/3 (QUIC)
