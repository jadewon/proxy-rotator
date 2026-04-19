# proxy-rotator

A lightweight gateway that automatically collects, validates, and rotates
SOCKS5 proxies — exposing them as a single **HTTP forward proxy** endpoint.
Designed to help work around IP-based blocking on external sites with zero
application code changes: clients only need to set `HTTP_PROXY`.

- **Single static Go binary** (~15 MB distroless image, no runtime deps)
- **Plugin-based proxy sources** — adding a source is one file plus a one-line registration
- **Real-time feedback loop** — failed requests immediately quarantine the offending proxy and retry transparently on another
- **Power-of-two-choices selection** — prefers low-inflight proxies, avoids hot-spotting
- **Secure defaults** — loopback binding by default, optional `PROXY_USERNAME`/`PROXY_PASSWORD` Basic auth, SSRF guard blocks RFC1918 / cloud-metadata ranges
- **Runtime-agnostic** — works standalone (Docker), as a Kubernetes sidecar, or behind an Istio mesh

---

## Longer guides

- [docs/deployment/railway.md](docs/deployment/railway.md) — Railway deployment (TCP Proxy mode required)
- [docs/deployment/k8s-sidecar.md](docs/deployment/k8s-sidecar.md) — Kubernetes sidecar and cluster-wide patterns
- [docs/deployment/egress-restricted.md](docs/deployment/egress-restricted.md) — why this cannot run inside a pod whose VPC only allows 443 outbound, and how to structure the system then
- [docs/deployment/hosting-options.md](docs/deployment/hosting-options.md) — comparison of external hosts (Railway, Fly.io, Oracle Cloud, Lightsail, …)
- [docs/references.md](docs/references.md) — related open source projects

## Quick start

### Docker

```bash
docker run --rm -p 3128:3128 -p 8080:8080 \
  -e TEST_URL=https://www.cloudflare.com/cdn-cgi/trace \
  -e SOURCES=freeproxy \
  jadewon/proxy-rotator:latest
```

From another terminal:

```bash
# Wait a minute or two for the pool to fill, then:
curl -x http://localhost:3128 https://api.ipify.org
curl -s http://localhost:8080/pool   # JSON snapshot of the current pool
```

### Local build

```bash
go build -o proxy-rotator ./cmd/proxy-rotator
SOURCES=freeproxy ./proxy-rotator
```

### Kubernetes (no Istio)

```bash
kubectl apply -f deploy/examples/deployment.yaml
```

Client pods:

```yaml
env:
  - {name: HTTP_PROXY,  value: "http://proxy-rotator.default.svc:3128"}
  - {name: HTTPS_PROXY, value: "http://proxy-rotator.default.svc:3128"}
  - {name: NO_PROXY,    value: ".cluster.local,.svc,localhost"}
```

For per-pod sidecar deployment, merge the snippet in
`deploy/examples/sidecar.yaml` into your Deployment's `containers` list.

### Kubernetes + Istio (optional)

Transparent routing without app-side env vars:

```bash
kubectl apply -f deploy/examples/deployment.yaml
kubectl apply -f deploy/examples/istio.yaml    # ServiceEntry + VirtualService
```

---

## How clients send traffic

Three ways to decide which targets flow through the rotator. Pick one or
combine them.

| Method | App change | Requires | Best for |
|--------|------------|----------|----------|
| **A. `HTTP_PROXY` env var** | Add env vars | Nothing | Most deployments |
| **B. Internal match rules** (`MATCH_HOSTS` / `BYPASS_HOSTS`) | Add env vars | Nothing | App sends all traffic to the proxy; the rotator filters by host |
| **C. Istio VirtualService** | None (transparent) | Istio mesh | Istio users who want zero app changes |

All three work against the same image.

---

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `127.0.0.1` | HTTP proxy bind address. Using a non-loopback value requires `PROXY_USERNAME` and `PROXY_PASSWORD`. |
| `LISTEN_PORT` | `3128` | HTTP proxy port |
| `ADMIN_ADDR` | `0.0.0.0` | Admin bind address (`/healthz`, `/metrics`, `/pool`, …) |
| `ADMIN_PORT` | `8080` | Admin port |
| `PROXY_USERNAME` | (empty) | Enables required `Proxy-Authorization: Basic` when set |
| `PROXY_PASSWORD` | (empty) | |
| `ALLOW_PRIVATE_TARGETS` | `false` | Set to `true` to disable the SSRF guard. Only do this in trusted environments. |
| `MAX_REQUEST_BODY` | `10485760` | Max request body bytes (10 MiB) |
| `READ_HEADER_TIMEOUT` | `5s` | HTTP header read timeout (slowloris mitigation) |
| `IDLE_TIMEOUT` | `120s` | HTTP keep-alive idle timeout |
| `MAX_HEADER_BYTES` | `1048576` | Max request header bytes |
| `STARTUP_GRACE` | `30s` | Grace window before `/startupz` flips to 200 even if the pool is still empty |
| `TEST_URL` | `https://example.com` | URL used to validate candidate proxies. See the warning below. |
| `VERIFY_MATCH_BODY` | (empty) | If set, the validation response body must contain this substring |
| `TEST_TIMEOUT` | `8s` | Validation request timeout |
| `PER_PROXY_TIMEOUT` | `8s` | Upstream dial/read timeout per attempt |
| `TOTAL_TIMEOUT` | `30s` | Total time budget including retries |
| `MAX_RETRIES` | `2` | Retry count for idempotent requests (GET/HEAD/OPTIONS) |
| `POOL_MIN` | `5` | Below this, the `freeproxy` source switches to the accelerated fetch interval |
| `POOL_MAX` | `50` | Upper bound on pool size |
| `EJECT_CONSEC_FAILS` | `3` | Consecutive failures before quarantine |
| `QUARANTINE_DURATION` | `5m` | How long a quarantined proxy waits before revalidation |
| `MATCH_HOSTS` | (empty) | Comma-separated host patterns routed through SOCKS5 (supports `.domain` and `*.domain`) |
| `BYPASS_HOSTS` | `.cluster.local,.svc,localhost` | Host patterns dialed directly |
| `DEFAULT_ACTION` | `proxy` | Fallback action for unmatched hosts: `proxy` \| `direct` \| `reject` |
| `SOURCES` | `file` | Enabled source plugins (comma separated) |
| `SOURCE_FREEPROXY_URL` | TheSpeedX/PROXY-List socks5.txt | Source URL for the `freeproxy` plugin |
| `SOURCE_FREEPROXY_INTERVAL` | `10m` | Steady-state fetch interval |
| `SOURCE_FREEPROXY_INTERVAL_LOW` | `30s` | Accelerated interval when `active < POOL_MIN` |
| `SOURCE_FREEPROXY_CONCURRENCY` | `20` | Parallel validation workers |
| `SOURCE_FILE_PATH` | `/etc/proxy-rotator/proxies.txt` | Path for the `file` plugin |
| `SOURCE_FILE_INTERVAL` | `60s` | `file` plugin rescan interval |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

---

## Security defaults

The following are enabled out of the box so that an instance cannot
accidentally become an open relay:

- **`LISTEN_ADDR=127.0.0.1`** — loopback-only. Perfect for a sidecar (only the app container in the same pod can reach it). To serve other pods, set `LISTEN_ADDR=0.0.0.0`, **which requires setting `PROXY_USERNAME`/`PROXY_PASSWORD` as well** — the process refuses to start otherwise.
- **SSRF guard** — both the proxied and direct paths resolve the target host and reject addresses in RFC1918, loopback, link-local, CGN, `169.254.169.254` (AWS/GCP metadata), or `fd00:ec2::254` (AWS Nitro IPv6) ranges with HTTP 403. Because the guard also runs on the direct path, `.cluster.local` / `.svc` `BYPASS_HOSTS` entries are blocked unless `ALLOW_PRIVATE_TARGETS=true` is explicitly set.
- **DNS rebinding defence** — on the direct path the resolved IP is dialed directly, so a malicious DNS reply between the guard check and the dial cannot redirect the request.
- **Server hardening** — `ReadHeaderTimeout`, `IdleTimeout`, `MaxHeaderBytes`, and `MaxBytesReader` shut down slow/oversized clients.
- **Header stripping** — the `Proxy-Authorization` header is removed before upstream forwarding.
- A conservative `NetworkPolicy` example is provided at `deploy/examples/networkpolicy.yaml`.

### ⚠️ Do not point `TEST_URL` at the site you actually want to crawl

Every candidate in every fetch cycle hits `TEST_URL`. With a typical
public SOCKS5 list that's 10,000+ requests per cycle, arriving from
thousands of different IPs — which looks exactly like a distributed
attack to the target's WAF. Doing this **tightens** the blocking you
were trying to work around.

Use a neutral endpoint that doesn't mind being hit:

```env
TEST_URL=https://www.cloudflare.com/cdn-cgi/trace
```

Whether a given proxy works for your real target is decided at
**request time** by the built-in RequestTracker: failed real requests
increment `ConsecFails`, and crossing `EJECT_CONSEC_FAILS` quarantines
the proxy. Validation only needs to confirm "the SOCKS5 handshake and
an HTTPS GET are possible."

---

## Endpoints

| Path | Purpose | Behaviour |
|------|---------|-----------|
| `GET /livez` | K8s **livenessProbe** | Always 200 while the process is responsive (independent of pool state, so a drained pool does not cause restart loops) |
| `GET /readyz` | K8s **readinessProbe** | 200 if pool has ≥1 active entry, otherwise 503 |
| `GET /startupz` | K8s **startupProbe** | 200 once the first proxy has been accepted OR `STARTUP_GRACE` has elapsed |
| `GET /healthz` | Backward compatibility (= `/readyz`) | |
| `GET /pool` | Debug | JSON snapshot of the current pool |
| `GET /metrics` | Prometheus | |

The three-probe example lives in
[`deploy/examples/deployment.yaml`](./deploy/examples/deployment.yaml).

Prometheus metrics:

- `proxy_pool_size{state}` — active / quarantine gauge
- `proxy_pool_source_size{source}` — per-source active count
- `proxy_request_total{result}` — success / fail / rejected / direct
- `proxy_request_duration_seconds{result}` — histogram
- `proxy_upstream_total{result}` — upstream attempts (retries included)
- `proxy_verify_total{source,result}` — validation pass / fail counters

---

## Writing a proxy source plugin

1. Create `internal/sources/<name>/<name>.go`.
2. Implement the `Source` interface.
3. Register the factory in `init()` via `sources.Register`.
4. Add a blank import to `cmd/proxy-rotator/main.go`.

### Interface

```go
type Source interface {
    Name() string
    Run(ctx context.Context, pool *pool.Pool, v validator.Validator) error
}
```

### Minimal example

```go
package mysource

import (
    "context"
    "time"

    "github.com/jadewon/proxy-rotator/internal/pool"
    "github.com/jadewon/proxy-rotator/internal/sources"
    "github.com/jadewon/proxy-rotator/internal/validator"
)

const Name = "mysource"

func init() {
    sources.Register(Name, func() (sources.Source, error) {
        return &Source{Interval: 10 * time.Minute}, nil
    })
}

type Source struct{ Interval time.Duration }

func (s *Source) Name() string { return Name }

func (s *Source) Run(ctx context.Context, p *pool.Pool, v validator.Validator) error {
    t := time.NewTicker(s.Interval)
    defer t.Stop()
    for {
        for _, addr := range fetchFromSomewhere() {
            raw := validator.RawProxy{Addr: addr}
            if err := v.Validate(ctx, raw); err != nil { continue }
            p.Add(pool.AddInput{Addr: addr, Source: Name})
        }
        select {
        case <-ctx.Done(): return nil
        case <-t.C:
        }
    }
}
```

Then in `main.go`:

```go
import _ "github.com/jadewon/proxy-rotator/internal/sources/mysource"
```

Enable with `SOURCES=mysource`.

---

## How it works

```
   HTTP client                 proxy-rotator                   external site
       │                         (:3128)                             ▲
       │  CONNECT / HTTP           │                                 │
       ├────────────────────────►  │                                 │
       │                           ▼                                 │
       │                      [ Router ]                             │
       │                  MATCH? BYPASS? DEFAULT?                    │
       │                           │                                 │
       │                           ▼                                 │
       │                     [ Pool.Select ]                         │
       │                  P2C (pick lowest inflight)                 │
       │                           │                                 │
       │                           ▼                                 │
       │                      [ SOCKS5 dial ]────────────────────────┤
       │                           │                                 │
       │                   success / failure ┐                       │
       │                           ▼          ▼                      │
       │                   EWMA / ConsecFails++                      │
       │              (quarantine over threshold)                    │
       │                           │                                 │
       ◄───────────────────────────┘                                 │
                                                                     │
   ┌── Source plugin: freeproxy ──┐                                  │
   │ HTTP fetch → parse lines →   │  Accelerated interval when       │
   │ parallel validate →          │  active < POOL_MIN               │
   │ pool.Add                     │                                  │
   └──────────────────────────────┘                                  │
```

- **The pool is the single source of truth**. Source plugins only `Add`; removal is driven by the RequestTracker (live feedback) and the ejection logic.
- **Power-of-two-choices**: pick two random entries and choose the one with lower `Inflight`. Prevents hot-spotting on a single proxy.
- **Transparent retries**: idempotent methods are retried on a different proxy up to `MAX_RETRIES` times.

---

## Limitations

- Free public proxies are inherently unreliable. The real-time feedback loop softens this but cannot eliminate it.
- Public SOCKS5 proxies rarely ship with authentication; the `Auth` field is reserved for paid sources.
- The pool is per-instance and in-memory. Replicas do not share state (Redis backend is a possible future addition).

---

## Development

```bash
go build ./...
go test ./...
go build -o /tmp/proxy-rotator ./cmd/proxy-rotator
```

Docker image:

```bash
docker build -f deploy/Dockerfile -t proxy-rotator .
```

Contribution guide: [CONTRIBUTING.md](./CONTRIBUTING.md).

---

## Releasing

### Automated (recommended)

Pushing a `v*` tag triggers GitHub Actions to build multi-arch
(amd64 + arm64) images, push them to Docker Hub
(`jadewon/proxy-rotator`), and create a GitHub Release.

```bash
git tag v0.1.0
git push origin v0.1.0
```

Required GitHub Secrets:
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN` (Docker Hub Access Token)

### Local (bypassing CI)

```bash
docker login -u <username>
VERSION=0.1.0
docker buildx build --platform linux/amd64,linux/arm64 \
  -f deploy/Dockerfile \
  -t jadewon/proxy-rotator:$VERSION \
  -t jadewon/proxy-rotator:latest \
  --push .
```

---

## License

MIT — see [LICENSE](./LICENSE).
