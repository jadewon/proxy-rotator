# Running proxy-rotator under a 443-only egress policy

Many enterprise Kubernetes environments (managed EKS, GKE with strict
VPC controls, on-prem clusters with restrictive firewalls) allow
outbound traffic on only a handful of ports — most commonly 80 and
443. Sometimes further narrowed by FQDN allowlist or forced through
a corporate proxy.

**In that environment, proxy-rotator cannot run inside the restricted
pod as-is.** This document explains why, and what architectures do
work.

## Why the SOCKS5 dial cannot happen inside the pod

proxy-rotator's job is to accept an HTTP forward-proxy request from
the app and replay it through a rotating SOCKS5 upstream. The SOCKS5
servers it reaches are almost always on non-HTTP ports:

| Port | Protocol typical on public SOCKS5 lists |
|------|----------------------------------------|
| 1080 | Canonical SOCKS5 |
| 4145 | Common alt |
| 9050 | Tor client default |
| 5678, 1085, 31280, … | Various |

If the pod can only open outbound TCP to `*:443` (and maybe `*:80`),
the SOCKS5 handshake can never reach any of these. Every validation
attempt silently times out; the pool never fills.

This is **not a proxy-rotator bug and not solvable with code
changes** — it's a property of the network boundary. No matter
whether the validated proxy list comes from the pod itself, from a
central Redis, or from a pre-published HTTPS file, the serving
component still needs to open a TCP connection to a non-443 port on
the internet to actually *use* a SOCKS5 proxy.

## What the pod *can* do

Only one thing: forward traffic over an allowed egress path to a
component that runs somewhere else. That "somewhere else" is where
proxy-rotator actually runs and dials SOCKS5.

```
┌──────────── pod (443-only egress) ────────────┐
│                                               │
│  [app]  HTTP_PROXY=…                          │
│    │                                          │
│    ▼  443 TCP only                            │
└────┼──────────────────────────────────────────┘
     │
     ▼
 ┌──────────────── somewhere with unrestricted egress ────────────────┐
 │                                                                    │
 │  [proxy-rotator]  ─── SOCKS5 (any port) ──▶  public proxy ──▶ target│
 │                                                                    │
 └────────────────────────────────────────────────────────────────────┘
```

The pod holds a minimal config — one env var pointing at the external
rotator. The app never needs code changes.

## Valid architectures

### A. External rotator reachable on 443 (or 80)

The rotator runs on a host that:
- has unrestricted outbound egress (to dial SOCKS5 candidates)
- can be reached from the pod on 443

Most hosting platforms default to exposing services on 443 (HTTPS
termination at a load balancer). proxy-rotator's HTTP proxy protocol
can pass through a TLS-terminating ALB or similar, as long as the
edge is **not an L7 HTTP reverse proxy** (which would re-route by
`Host` header and break `CONNECT`).

**Compatible edges**:
- AWS ALB in TCP/TLS passthrough mode
- nginx configured with `stream {}` TLS passthrough
- Cloudflare Tunnel (L4 when used with `cloudflared access tcp`)

**Incompatible edges** (L7 reverse proxies):
- Railway HTTPS domain (use Railway's TCP Proxy feature instead)
- Cloudflare HTTPS proxied records (use Tunnel or TCP Spectrum)
- Vercel / Netlify
- GCP Cloud Run's HTTPS URL
- Fly.io's anycast HTTPS

If the only egress is HTTPS and the chosen host only offers an L7
HTTPS endpoint, you need option B.

### B. Tunnel wrapper (443-friendly transport)

A thin tunnel sidecar in the pod establishes a single outbound 443
connection to the tunnel provider, and inside that connection carries
arbitrary TCP. The pod's app sees the rotator at `localhost:<port>`.

```
pod:
  [app]  HTTP_PROXY=http://localhost:3128
            │
            ▼
  [cloudflared access tcp]  ──── single 443 connection ──▶
                                 Cloudflare edge
                                   │
                                   ▼
  ┌────────── origin host ──────────┐
  │  [cloudflared tunnel]          │
  │      │                          │
  │      ▼                          │
  │  [proxy-rotator :3128]          │
  │      │                          │
  │      ▼ SOCKS5                   │
  │  public proxy ──▶ target        │
  └─────────────────────────────────┘
```

Concrete tools:

- **Cloudflare Tunnel + Cloudflare Access** — free, tunnel runs on
  the origin host, `cloudflared access tcp` runs as sidecar in the
  pod. Service tokens provide auth.
- **WireGuard over TCP/443** — more DIY; the tunnel runs between a
  pod sidecar and a public WireGuard endpoint.
- **SSH tunnel** (`ssh -L 3128:localhost:3128 …`) — works in a pinch
  but brittle as production infra.

Of these, Cloudflare Tunnel is the most common choice because it's
free, survives restarts, and provides an auth layer via Access.

### C. Crawl from outside the pod entirely

Reframe the problem: the pod doesn't need to crawl synchronously.
Instead:

```
pod ──▶ SQS / WebSocket / S3 queue  ──▶  external worker
                                              │
                                              ▼
                                       crawls through proxy-rotator
                                              │
                                              ▼
                                         S3 results
pod ◀──── S3 / polling / webhook ────────────┘
```

This is the most secure for the pod (zero external egress beyond
AWS-internal SQS/S3) but requires the biggest application-layer
change.

## Patterns that do NOT solve the problem

Despite appearances, the following do *not* let proxy-rotator work
inside a restricted pod:

- **Central shared pool (Redis, HTTPS list)** — the pool metadata
  arrives fine, but the pod still can't open port 1080 to actually
  use those proxies.
- **HTTP/HTTPS-only proxy sources** — public HTTP proxies on 443 are
  rare and quickly blocked. And once connected, they still need to
  make their own outbound on arbitrary ports to the target.
- **AWS API Gateway rotation** (e.g. `requests-ip-rotator`) — works
  for "appear from many AWS IPs" but does not rotate through
  non-AWS proxies. Useful only if the target is not specifically
  blocking AWS.

## Matrix: which option fits your constraint

| Can pod reach external TCP on arbitrary port? | Pick |
|--|--|
| Yes | proxy-rotator as sidecar ([k8s-sidecar.md](k8s-sidecar.md) §Pattern 1) |
| No, but 443 to any public host | External rotator on 443 (Railway TCP Proxy / AWS ALB / etc.) |
| No, only 443 to specific hosts | Tunnel wrapper (Cloudflare Tunnel most common) |
| No, only AWS-internal services | Queue-based off-pod crawling |

See [hosting-options.md](hosting-options.md) for where to put the
rotator itself.
