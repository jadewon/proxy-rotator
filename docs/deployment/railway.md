# Deploy proxy-rotator on Railway

Railway is a convenient host for the external rotator role because
outbound traffic is unrestricted — SOCKS5 candidates on arbitrary
high ports (1080, 4145, 9050, …) can be validated and dialed. Clients
(including workloads inside restrictive Kubernetes clusters) reach the
rotator over a single public TCP endpoint.

This guide assumes Railway's Hobby plan or free trial.

## 1. Create the service

1. [railway.app](https://railway.app) → **New Project** → **Deploy a service**
2. Choose **Deploy from Docker image**
3. Image: `jadewon/proxy-rotator:latest` (or pin to a specific version like `:0.1.1`)

## 2. Set environment variables

Only five variables must be set; the rest have sensible defaults.

```env
LISTEN_ADDR=0.0.0.0
PROXY_USERNAME=<random 16-hex-char string>
PROXY_PASSWORD=<random 40+ char URL-safe string>
SOURCES=freeproxy
TEST_URL=https://www.cloudflare.com/cdn-cgi/trace
```

Generate strong random credentials:

```bash
openssl rand -hex 8                               # username
openssl rand -base64 36 | tr -d '+/=' | head -c 40  # password (URL-safe)
```

Recommended additional tuning (throttles how aggressively the rotator
hits free-proxy list hosts and your `TEST_URL`):

```env
SOURCE_FREEPROXY_INTERVAL=30m
SOURCE_FREEPROXY_INTERVAL_LOW=5m
SOURCE_FREEPROXY_CONCURRENCY=10
EJECT_CONSEC_FAILS=2
```

### ⚠️ Do not set TEST_URL to the site you actually want to crawl

Every candidate in every fetch cycle hits `TEST_URL`. With a typical
public SOCKS5 list this means 10,000+ requests per cycle arriving from
thousands of different IPs — which looks like a distributed attack to
the target's WAF and encourages them to tighten blocking on the exact
site you care about. Point `TEST_URL` at a neutral endpoint that does
not mind being hit (Cloudflare, Google, etc.), and let the built-in
**real-time feedback loop** prune proxies that fail against the real
target during actual use.

See the main [README](../../README.md) §Security for the full list of
security-relevant env vars (`PROXY_USERNAME`, `ALLOW_PRIVATE_TARGETS`,
request/body/header limits).

## 3. Expose the proxy port

Railway's **HTTPS public domain cannot be used** as an HTTP forward
proxy. Railway's edge is an L7 reverse proxy that routes by `Host`
header and rejects `CONNECT` tunnelling:

- Direct `GET /` returns `407 Proxy Authentication Required` (the
  request reaches the container, which responds as a forward proxy)
- An `absolute-form` proxy request (`GET http://foo.example/ HTTP/1.1`)
  returns Railway's own `404 Application not found` — the edge reads
  the `Host: foo.example` header and fails to match a Railway app

Use **TCP Proxy** instead:

**Settings → Networking → Public Networking → Add TCP Proxy**
- Target port: `3128`
- Railway allocates a stable endpoint of the form
  `<hostname>.proxy.rlwy.net:<port>`

The admin port (`8080`) stays internal unless you add a second TCP
proxy for it. Avoid exposing `/pool` publicly — it leaks the validated
proxy list.

## 4. Verify

```bash
export PROXY='http://USER:PASS@<hostname>.proxy.rlwy.net:<port>'

# Service reachable?
nc -zv <hostname>.proxy.rlwy.net <port>

# Proxy authenticating?
curl -sx "$PROXY" https://api.ipify.org
# → expected: an IP that is *not* your own (it's a SOCKS5 proxy's IP)

# Rotation working?
for i in 1 2 3 4 5; do curl -sx "$PROXY" https://api.ipify.org; echo; done
# → expected: multiple distinct IPs across calls
```

The first successful response may take 1-3 minutes after boot while
the pool validates against `TEST_URL`. During that time expect HTTP
503s and empty responses.

## 5. Use it from a client

The proxy speaks plain HTTP proxy protocol (`HTTP CONNECT` for HTTPS
targets, `absolute-form` for HTTP targets). Any client that honours
`HTTP_PROXY`/`HTTPS_PROXY` just works:

```bash
export HTTP_PROXY='http://USER:PASS@<hostname>.proxy.rlwy.net:<port>'
export HTTPS_PROXY="$HTTP_PROXY"
curl https://your-target.example
```

For Kubernetes integration, see
[k8s-sidecar.md](k8s-sidecar.md).

## Operational notes

- **Region**: Railway runs workloads in a few regions (US East, US West,
  EU West, Singapore). Pick the one closest to your clients *and*
  crawl target. Every request hops:
  `client → Railway region → SOCKS5 proxy (global) → target`.
- **Cost**: a single service with the above config runs well under the
  Hobby plan's $5/month credit at low to moderate traffic. Egress
  bandwidth dominates; a busy scraper can easily consume ~10 GB/day.
- **Availability**: Railway will redeploy the container on config
  changes and occasional platform updates. The pool is in-memory,
  so the first few minutes after a restart return 503. Clients must
  tolerate transient `5xx` and retry.
- **Rotating credentials**: Railway Variables tab lets you regenerate
  `PROXY_PASSWORD` without a rebuild. Roll it periodically.

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| `HTTP 407` on every call | Missing / wrong `Proxy-Authorization` |
| `HTTP 503 "no proxies available"` | Pool still warming up, or TEST_URL is unreachable from Railway (check egress from Railway side) |
| `HTTP 503 "starting"` on `/startupz` | Within `STARTUP_GRACE` window and pool still empty |
| Connection reset / exit code 56 in curl | Pool drained momentarily; retry |
| `HTTP 000` (curl) persistently | Railway service down, wrong endpoint, or client blocked from the TCP proxy port |

## See also

- [egress-restricted.md](egress-restricted.md) — why this Railway
  pattern exists in the first place
- [hosting-options.md](hosting-options.md) — alternatives to Railway
  (Fly.io Tokyo, Oracle Cloud Seoul, etc.)
