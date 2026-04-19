# Kubernetes sidecar integration

There are two practical ways to use proxy-rotator from a Kubernetes
workload:

1. **proxy-rotator runs as a sidecar next to your app** — works when
   the pod's network policy allows outbound traffic on arbitrary ports
   (1080, 4145, 9050, …) to reach SOCKS5 candidates.
2. **proxy-rotator runs somewhere else; the pod is a pure client** —
   required when the pod is in a VPC / namespace that only permits
   outbound 80/443 traffic (typical for enterprise AWS EKS, GKE,
   hardened clusters). See
   [egress-restricted.md](egress-restricted.md) for the full rationale.

## Pattern 1: proxy-rotator as sidecar

The pod contains two containers that share a network namespace. The
app sets `HTTP_PROXY=http://localhost:3128`.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
        # ── your app ──────────────────────────────────
        - name: app
          image: your/app:latest
          env:
            - name: HTTP_PROXY
              value: "http://localhost:3128"
            - name: HTTPS_PROXY
              value: "http://localhost:3128"
            - name: NO_PROXY
              value: ".cluster.local,.svc,localhost,127.0.0.1"

        # ── proxy-rotator sidecar ─────────────────────
        - name: proxy-rotator
          image: jadewon/proxy-rotator:0.1.1
          ports:
            - {name: proxy, containerPort: 3128}
            - {name: admin, containerPort: 8080}
          env:
            - {name: LISTEN_ADDR, value: "127.0.0.1"}   # only the app container reaches it
            - {name: SOURCES, value: "freeproxy"}
            - {name: TEST_URL, value: "https://www.cloudflare.com/cdn-cgi/trace"}
            - {name: MATCH_HOSTS, value: "target-site.com"}
            - {name: DEFAULT_ACTION, value: "direct"}
          resources:
            requests: {cpu: 50m, memory: 64Mi}
            limits:   {cpu: 200m, memory: 128Mi}

          # Three-probe configuration prevents an empty pool from
          # triggering restart loops. See README §Endpoints.
          startupProbe:
            httpGet: {path: /startupz, port: admin}
            periodSeconds: 5
            failureThreshold: 24
          livenessProbe:
            httpGet: {path: /livez, port: admin}
            periodSeconds: 30
          readinessProbe:
            httpGet: {path: /readyz, port: admin}
            periodSeconds: 5
```

Notes:

- `LISTEN_ADDR=127.0.0.1` is fine in a sidecar (containers share the
  network namespace) and avoids exposing the proxy to the cluster. No
  `PROXY_USERNAME`/`PROXY_PASSWORD` required in loopback mode.
- `MATCH_HOSTS` + `DEFAULT_ACTION=direct` tells the rotator to only
  route specific hosts through SOCKS5 and forward the rest directly.
  The app can then send *all* traffic via `HTTP_PROXY` without
  penalising internal calls.
- For Istio environments you can alternatively omit app-side
  `HTTP_PROXY` and use a `VirtualService` to route specific external
  hosts to the rotator. See `deploy/examples/istio.yaml`.

## Pattern 2: pod is a pure client

When VPC egress is restricted to 80/443 you cannot run proxy-rotator
itself inside the pod — the SOCKS5 dial would fail. The serving
component must live somewhere with unrestricted outbound, typically:

- Railway ([railway.md](railway.md))
- Fly.io Tokyo / Oracle Cloud Seoul / Lightsail
  ([hosting-options.md](hosting-options.md))
- A self-hosted VM

The pod then just points `HTTP_PROXY` at that external endpoint:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
        - name: app
          image: your/app:latest
          env:
            - name: HTTP_PROXY
              valueFrom: {secretKeyRef: {name: proxy-endpoint, key: url}}
            - name: HTTPS_PROXY
              valueFrom: {secretKeyRef: {name: proxy-endpoint, key: url}}
            - name: NO_PROXY
              value: ".cluster.local,.svc,localhost,127.0.0.1"
```

Where the `proxy-endpoint` secret stores a URL of the form:

```
http://USER:PASS@<public-rotator-endpoint>:<port>
```

### Egress consideration

If your cluster can reach the public rotator endpoint's TCP port
directly, that's enough. If egress is strictly 443-only, wrap the
connection in a 443-friendly tunnel (for example a Cloudflare Tunnel
sidecar). See [egress-restricted.md](egress-restricted.md) for
detailed patterns.

### Cluster-wide deployment

You can also run proxy-rotator as a standalone `Deployment + Service`
inside the cluster and let multiple workloads share it:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata: {name: proxy-rotator}
spec:
  replicas: 1
  selector: {matchLabels: {app: proxy-rotator}}
  template:
    metadata: {labels: {app: proxy-rotator}}
    spec:
      containers:
        - name: proxy-rotator
          image: jadewon/proxy-rotator:0.1.1
          env:
            - {name: LISTEN_ADDR, value: "0.0.0.0"}
            - {name: PROXY_USERNAME, valueFrom: {secretKeyRef: {name: proxy-auth, key: user}}}
            - {name: PROXY_PASSWORD, valueFrom: {secretKeyRef: {name: proxy-auth, key: pass}}}
            - {name: SOURCES, value: "freeproxy"}
            - {name: TEST_URL, value: "https://www.cloudflare.com/cdn-cgi/trace"}
          ports:
            - {name: proxy, containerPort: 3128}
            - {name: admin, containerPort: 8080}
          # (probes as in Pattern 1)
---
apiVersion: v1
kind: Service
metadata: {name: proxy-rotator}
spec:
  selector: {app: proxy-rotator}
  ports:
    - {name: proxy, port: 3128, targetPort: proxy}
```

Clients use `HTTP_PROXY=http://USER:PASS@proxy-rotator.<namespace>.svc:3128`.

### NetworkPolicy

For either pattern, restrict who can talk to the rotator. See
[`deploy/examples/networkpolicy.yaml`](../../deploy/examples/networkpolicy.yaml)
for a conservative default.

## See also

- Main [README](../../README.md) §Endpoints for probe semantics
- [egress-restricted.md](egress-restricted.md) — the "why" behind
  Pattern 2
