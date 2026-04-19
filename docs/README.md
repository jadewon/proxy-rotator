# proxy-rotator documentation

Extended guides that don't fit in the main [README](../README.md).

## Deployment

- [deployment/railway.md](deployment/railway.md) — one-click deploy on Railway (HTTPS or TCP Proxy)
- [deployment/k8s-sidecar.md](deployment/k8s-sidecar.md) — Kubernetes sidecar patterns (with and without Istio)
- [deployment/egress-restricted.md](deployment/egress-restricted.md) — why `proxy-rotator` cannot run inside a pod that can only reach 80/443 outbound, and how to structure the system for that case
- [deployment/hosting-options.md](deployment/hosting-options.md) — comparison of hosts for the external rotator role (Railway, Fly.io, Oracle, EC2, Lightsail, self-hosted)

## Reference

- [references.md](references.md) — related open source proxy pool / rotator projects and the patterns they established

## Internal

If you operate a private deployment, create `docs/internal/` for your own
gitignored notes (endpoint URLs, shared credentials, runbooks). That
directory is excluded from version control by default.
