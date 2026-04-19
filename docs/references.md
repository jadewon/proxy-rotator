# Related projects

Other open source proxy pool / rotator projects worth looking at.
proxy-rotator borrows ideas from most of them and tries to stay
smaller and single-binary.

## Large, established

- **[jhao104/proxy_pool](https://github.com/jhao104/proxy_pool)**
  (Python, 23k ⭐) — the reference implementation of the
  collector/server split with Redis as shared storage. Run
  `proxyPool.py schedule` and `proxyPool.py server` as separate
  processes. Well-documented Chinese community around it.
- **[Python3WebSpider/ProxyPool](https://github.com/Python3WebSpider/ProxyPool)**
  (Python, 6k ⭐) — Getter / Tester / Server three-layer split, also
  Redis-backed.
- **[constverum/ProxyBroker](https://github.com/constverum/ProxyBroker)**
  (Python, 4k ⭐) — async proxy finder/checker/server with three CLI
  commands: `find`, `grab`, `serve`. Easy to run the serve component
  against a pre-built list.

## Go ecosystem

- **[pingc0y/go_proxy_pool](https://github.com/pingc0y/go_proxy_pool)**
  (Go, 780 ⭐) — "无环境依赖" ("no environment dependency"), closest
  philosophical match to proxy-rotator.
- **[yukkcat/socks5-proxy](https://github.com/yukkcat/socks5-proxy)**
  (Go, 190 ⭐) — small and readable. `scraper.go` / `checker.go` /
  `pool.go` / `server.go` clean separation.

## Creative approaches

- **[Ge0rg3/requests-ip-rotator](https://github.com/Ge0rg3/requests-ip-rotator)**
  (Python, 1.7k ⭐) — uses AWS API Gateway endpoints as a rotating IP
  pool. No SOCKS5 involved. Effective only when the target is not
  already blocking AWS IP ranges.

## Public proxy list sources

Not rotators themselves but useful as input for the `freeproxy` source
plugin:

- [TheSpeedX/PROXY-List](https://github.com/TheSpeedX/PROXY-List) — the
  default source URL baked into proxy-rotator.
- [clarketm/proxy-list](https://github.com/clarketm/proxy-list)
- [ProxyScraper/ProxyScraper](https://github.com/ProxyScraper/ProxyScraper)

## Patterns borrowed

What proxy-rotator does that was learned from the above:

- **Plugin sources + central pool** — shape ultimately resembles
  jhao104's Fetcher / Tester / Api split collapsed into one process
  with pluggable fetchers
- **Power-of-two-choices selection** — not from these projects
  specifically but common in load-balancer literature; trades worst-case
  concentration for slightly worse best-case
- **Real-time feedback loop** — few public projects do this; most only
  validate on a fixed interval. proxy-rotator's RequestTracker prunes
  proxies on actual usage failures, independent of periodic validation
- **Single-binary deployment** — yukkcat and pingc0y share this
  preference; contrasts with Python projects that assume Redis in the
  mix

## What proxy-rotator deliberately doesn't borrow

- **Redis central pool** — simplicity first. For multi-replica
  deployments, Redis support is listed as a future requirement in the
  [PRD](../PRD.md), but v0.1.x stays in-memory.
- **Large proxy source lists** — more sources does not mean a better
  pool; noise dominates. One or two high-quality sources with good
  validation is the design bet.
- **Web dashboard / UI** — `/pool` JSON and `/metrics` (Prometheus)
  are the observability contract. UIs belong in monitoring stacks
  that already exist, not in the rotator.
