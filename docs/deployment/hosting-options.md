# Where to host proxy-rotator

When [your primary cluster cannot run the rotator itself](egress-restricted.md),
you need somewhere with unrestricted outbound that acts as the
"serving" end of the pool.

This page compares common options. Pricing and features drift — treat
the numbers as order-of-magnitude, not authoritative.

## Quick comparison

| Host | Cost (low-to-mid traffic) | Regions near Korea/JP | L4 TCP passthrough | Managed |
|------|--------------------------|-----------------------|--------------------|---------|
| Railway | ~$5/month | Singapore | ✓ (TCP Proxy add-on) | ✓ |
| Fly.io | Free tier / ~$5/month | Tokyo (NRT) | ✓ (via `flyctl proxy` / dedicated IP) | ✓ |
| Oracle Cloud Free Tier | $0 (permanent free) | Seoul, Tokyo | ✓ (you run the OS) | Self-managed |
| AWS Lightsail | $5/month (fixed) | Seoul | ✓ | Self-managed |
| Hetzner VPS | ~€4/month | EU only | ✓ | Self-managed |
| Vultr/Linode/DigitalOcean | ~$5/month | Tokyo, Singapore | ✓ | Self-managed |

Excluded from this list: anything that does L7 HTTPS termination
without a TCP/raw-passthrough mode (Cloudflare Workers, Vercel,
Netlify, GCP Cloud Run default). They cannot carry HTTP forward-proxy
traffic. See [egress-restricted.md](egress-restricted.md) §"Compatible
edges".

## When to pick which

### Railway

Best for: fastest PoC, no infra to manage. See the dedicated
[railway.md](railway.md) guide.

- ✓ Docker-image deploy in minutes
- ✓ TCP Proxy feature gives a stable raw TCP endpoint
- ✗ Singapore is the closest region for Korean workloads (≈80 ms RTT)
- ✗ Egress bandwidth counts against your plan

### Fly.io

Best for: lower latency to Asia-based clients.

- ✓ Tokyo region (≈30 ms from Seoul)
- ✓ Generous free allocation for single small instance
- ✓ Docker-image deploy
- ✓ `fly.toml` service config supports raw TCP services
- ✗ Deploy UX is CLI-first (some find Railway friendlier)

### Oracle Cloud Free Tier

Best for: permanent free compute in the right region.

- ✓ Seoul and Tokyo regions available
- ✓ 2× ARM VM (Ampere A1) with 24 GB RAM total — massive overkill for
  proxy-rotator but free forever
- ✓ 10 TB/month egress (extremely generous)
- ✗ Self-managed OS, harder onboarding
- ✗ Free tier instances occasionally reclaimed if idle (keep something
  running)

This is the lowest-cost long-term option if you're willing to run an
OS yourself. One VM hosting proxy-rotator in Docker is straightforward.

### AWS Lightsail Seoul

Best for: staying inside the AWS ecosystem without EKS egress
constraints.

- ✓ Seoul region (≈5 ms from Seoul EKS)
- ✓ Fixed $5/month, 2 TB transfer included
- ✓ Simple Docker host
- ✗ Egress pricing steep beyond included 2 TB

### Self-hosted (home server, Mac mini, Raspberry Pi)

Best for: zero recurring cost and you already have hardware + a
network that lets you receive inbound or you're willing to use
Cloudflare Tunnel.

- ✓ $0 ongoing
- ✗ Home internet uplink may bottleneck crawling
- ✗ Availability depends on your ISP / UPS / patience

Pair with Cloudflare Tunnel if your home network doesn't allow
inbound ports. See [egress-restricted.md](egress-restricted.md).

## Typical total cost sketches

Scenario A — one scraper, ~100 k requests / day, 100 KB avg response

- Railway (Singapore): ~$5/month + ~$3 egress ≈ $8
- Fly.io Tokyo: free tier covers it
- Oracle Cloud Seoul: $0
- AWS Lightsail Seoul: $5 (includes 2 TB, well above need)

Scenario B — pipeline-scale, ~5 M requests / day, 200 KB avg

- Railway: $20-30 in egress dominates
- Fly.io: ~$10 + egress
- Oracle Free: still $0 (within 10 TB/month)
- AWS Lightsail: egress overage likely → upgrade instance or migrate

## Migration between hosts

proxy-rotator is stateless (pool is in-memory; sources re-fetch on
boot). Migration is:

1. Deploy to the new host with the same env vars
2. Update your client's `HTTP_PROXY` to the new endpoint
3. Retire the old instance

No data to move. This makes it cheap to start on Railway for PoC and
move to Oracle Cloud Seoul (or similar) once latency/cost matter.
