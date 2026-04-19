# proxy-rotator

SOCKS5 프록시를 자동 수집·검증·로테이션하여 **HTTP 포워드 프록시**로 제공하는 경량 게이트웨이.
외부 사이트의 IP 차단 우회용으로, 클라이언트는 `HTTP_PROXY`만 지정하면 된다.

- **단일 Go 정적 바이너리** (distroless 이미지 ~15MB, 의존성 없음)
- **플러그인 기반 프록시 소스** — 소스 추가 = 파일 하나 + 한 줄 등록
- **실시간 피드백 루프** — 요청 실패 시 해당 프록시 즉시 격리 + 다른 프록시로 투명 재시도
- **Power-of-Two-Choices 선택** — inflight 낮은 프록시 우선, 특정 프록시 과적재 방지
- **기본 보안** — 기본 loopback 바인딩, `PROXY_USERNAME/PROXY_PASSWORD` Basic auth, RFC1918/metadata 대역 SSRF 차단
- **어떤 런타임에서도 동작** — Docker 단독 / Kubernetes / (옵션) Istio 메쉬

---

## 더 긴 가이드

- [docs/deployment/railway.md](docs/deployment/railway.md) — Railway 배포 (TCP Proxy 모드 필수)
- [docs/deployment/k8s-sidecar.md](docs/deployment/k8s-sidecar.md) — Kubernetes 사이드카 / 클러스터 공용 배포
- [docs/deployment/egress-restricted.md](docs/deployment/egress-restricted.md) — VPC egress가 443만 허용될 때 왜 pod 내부에서 돌릴 수 없고 어떻게 구성해야 하는지
- [docs/deployment/hosting-options.md](docs/deployment/hosting-options.md) — Railway / Fly.io / Oracle Cloud / Lightsail 등 호스팅 선택 가이드
- [docs/references.md](docs/references.md) — 관련 오픈소스 프로젝트 비교

## 빠른 시작

### Docker

```bash
docker run --rm -p 3128:3128 -p 8080:8080 \
  -e TEST_URL=https://example.com \
  -e SOURCES=freeproxy \
  jadewon/proxy-rotator:latest
```

다른 터미널에서:

```bash
# 풀이 채워질 때까지 잠시 기다린 후
curl -x http://localhost:3128 https://api.ipify.org
curl -s http://localhost:8080/pool   # 현재 풀 상태 JSON
```

### 로컬 빌드

```bash
go build -o proxy-rotator ./cmd/proxy-rotator
SOURCES=freeproxy ./proxy-rotator
```

### Kubernetes (Istio 없음)

```bash
kubectl apply -f deploy/examples/deployment.yaml
```

클라이언트 파드에서:

```yaml
env:
  - {name: HTTP_PROXY,  value: "http://proxy-rotator.default.svc:3128"}
  - {name: HTTPS_PROXY, value: "http://proxy-rotator.default.svc:3128"}
  - {name: NO_PROXY,    value: ".cluster.local,.svc,localhost"}
```

파드당 사이드카로 붙이는 경우 `deploy/examples/sidecar.yaml` 스니펫을 Deployment의 `containers`에 병합.

### Kubernetes + Istio (옵션)

앱 환경변수 없이 투명 라우팅:

```bash
kubectl apply -f deploy/examples/deployment.yaml
kubectl apply -f deploy/examples/istio.yaml    # ServiceEntry + VirtualService
```

---

## 사용 방법

클라이언트가 프록시 경유 대상을 결정하는 방법 3가지. 환경에 맞게 선택.

| 방식 | 앱 변경 | 필요 조건 | 추천 대상 |
|------|---------|-----------|-----------|
| **A. `HTTP_PROXY` 환경변수** | 환경변수 추가 | 없음 | 대부분의 경우 |
| **B. 내부 매치 규칙** (`MATCH_HOSTS`/`BYPASS_HOSTS`) | 환경변수 추가 | 없음 | 앱이 모든 트래픽을 프록시로 보낼 때 host별 선별 |
| **C. Istio VirtualService** | 없음 (투명) | Istio 메쉬 | 앱 무변경을 원하는 Istio 사용자 |

세 방식 모두 같은 이미지로 동작한다.

---

## 환경변수

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `LISTEN_ADDR` | `127.0.0.1` | HTTP 프록시 바인드 주소. 비-loopback으로 바꾸려면 `PROXY_USERNAME/PROXY_PASSWORD` 필수 |
| `LISTEN_PORT` | `3128` | HTTP 프록시 포트 |
| `ADMIN_ADDR` | `0.0.0.0` | admin(`/healthz`,`/metrics`,`/pool`) 바인드 주소 |
| `ADMIN_PORT` | `8080` | admin 포트 |
| `PROXY_USERNAME` | (비어있음) | 설정 시 `Proxy-Authorization: Basic` 필수 |
| `PROXY_PASSWORD` | (비어있음) | |
| `ALLOW_PRIVATE_TARGETS` | `false` | `true`면 SSRF 가드 비활성. 신뢰된 환경에서만 |
| `MAX_REQUEST_BODY` | `10485760` | 요청 바디 최대 바이트 (10 MiB) |
| `READ_HEADER_TIMEOUT` | `5s` | HTTP 서버 헤더 읽기 타임아웃 (slowloris 방지) |
| `IDLE_TIMEOUT` | `120s` | HTTP keepalive idle 타임아웃 |
| `MAX_HEADER_BYTES` | `1048576` | HTTP 헤더 최대 바이트 |
| `STARTUP_GRACE` | `30s` | 이 시간 내 풀에 active 엔트리가 생기지 않아도 `/startupz`를 200으로 전환 |
| `TEST_URL` | `https://example.com` | 검증용 GET 대상 (실제 타겟 URL **강권장**) |
| `VERIFY_MATCH_BODY` | (비어있음) | 응답 본문에 이 문자열이 포함돼야 유효 |
| `TEST_TIMEOUT` | `8s` | 검증 타임아웃 |
| `PER_PROXY_TIMEOUT` | `8s` | 업스트림 타임아웃 |
| `TOTAL_TIMEOUT` | `30s` | 재시도 포함 총 타임아웃 |
| `MAX_RETRIES` | `2` | 멱등 메서드(GET/HEAD/OPTIONS) 재시도 횟수 |
| `POOL_MIN` | `5` | 이하로 떨어지면 freeproxy 소스가 가속 주기로 전환 |
| `POOL_MAX` | `50` | 풀 상한 |
| `EJECT_CONSEC_FAILS` | `3` | 연속 실패 임계값 (격리) |
| `QUARANTINE_DURATION` | `5m` | 격리 후 재시도까지 대기 |
| `MATCH_HOSTS` | (비어있음) | 프록시 경유 host 패턴 (콤마, `.domain`/`*.domain` 지원) |
| `BYPASS_HOSTS` | `.cluster.local,.svc,localhost` | 직접 접속 host 패턴 |
| `DEFAULT_ACTION` | `proxy` | `proxy` \| `direct` \| `reject` |
| `SOURCES` | `file` | 활성화 소스 이름 (콤마 구분) |
| `SOURCE_FREEPROXY_URL` | TheSpeedX/PROXY-List socks5.txt | freeproxy 소스 URL |
| `SOURCE_FREEPROXY_INTERVAL` | `10m` | 평시 수집 주기 |
| `SOURCE_FREEPROXY_INTERVAL_LOW` | `30s` | `POOL_MIN` 이하일 때 가속 주기 |
| `SOURCE_FREEPROXY_CONCURRENCY` | `20` | 검증 동시성 |
| `SOURCE_FILE_PATH` | `/etc/proxy-rotator/proxies.txt` | file 소스 경로 |
| `SOURCE_FILE_INTERVAL` | `60s` | file 소스 재스캔 주기 |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

---

## 보안 기본값

공개 오픈 릴레이로 악용되지 않도록 다음 기본값을 적용한다:

- **`LISTEN_ADDR=127.0.0.1`** — 사이드카로 붙을 때 앱 컨테이너만 접근. 다른 파드에서 접근하려면 `LISTEN_ADDR=0.0.0.0`으로 열되 **반드시 `PROXY_USERNAME/PASSWORD`를 함께 설정해야 부팅됨** (인증 없이 비-loopback 바인드는 거부).
- **SSRF 가드** — 프록시 경유·직접 두 경로 모두에서, 타겟이 RFC1918, 루프백, 링크로컬, CGN, `169.254.169.254` (AWS/GCP metadata), `fd00:ec2::254` (AWS Nitro IPv6) 등으로 해석되면 403. `BYPASS_HOSTS` 매치도 가드를 거치므로, `.cluster.local`·`.svc` 같은 내부 대상을 허용하려면 **`ALLOW_PRIVATE_TARGETS=true`** 를 함께 설정해야 한다.
- **DNS rebinding 방지** — 직접 경로에서는 가드가 리졸브한 IP로 직접 다이얼. 가드 시점과 연결 시점 사이의 DNS 변경으로 우회되지 않는다.

### ⚠️ `TEST_URL`을 실제 크롤링 타겟으로 지정하지 말 것

매 수집 사이클마다 **모든 후보 프록시**가 `TEST_URL`을 호출합니다. 공개 프록시 리스트
크기(수천~만 개) × 서로 다른 IP × 짧은 시간대 = 타겟에게 **분산 공격 패턴으로 보입니다**.
결과적으로 타겟의 WAF가 탐지를 강화하고 IP 차단 범위를 넓혀서, **원래 해결하려던 IP 차단
문제를 오히려 악화**시킵니다.

`TEST_URL`은 중립적이고 부하에 안전한 엔드포인트로 지정하세요:

```env
TEST_URL=https://www.cloudflare.com/cdn-cgi/trace
```

실제 타겟에서 특정 프록시가 차단됐는지는 **RequestTracker**(실시간 피드백 루프)가 실사용 중
자동으로 판별하여 격리합니다. 검증 단계는 "SOCKS5 핸드셰이크 + HTTPS 요청이 가능한가"만
보면 충분합니다.
- **HTTP 서버 타임아웃** — `ReadHeaderTimeout`, `IdleTimeout`, `MaxHeaderBytes`, `MaxBytesReader`로 slow/oversized 요청 차단.
- **프록시 인증 헤더는 업스트림에 전달되지 않음** (검증 후 strip).
- `deploy/examples/networkpolicy.yaml` — 보수적 ingress/egress NetworkPolicy 예시.

---

## 엔드포인트

| Path | 용도 | 응답 기준 |
|------|------|-----------|
| `GET /livez` | K8s **livenessProbe** | 프로세스가 살아있으면 항상 200. 풀 상태와 무관 (재시작 루프 방지) |
| `GET /readyz` | K8s **readinessProbe** | 풀에 active 엔트리 ≥ 1이면 200, 아니면 503 |
| `GET /startupz` | K8s **startupProbe** | 첫 프록시가 풀에 들어왔거나 `STARTUP_GRACE` 경과 시 200 |
| `GET /healthz` | 과거 호환 (= `/readyz`) | |
| `GET /pool` | 디버그 | 현재 풀 상태 JSON |
| `GET /metrics` | Prometheus | |

K8s 3-probe 예시는 [`deploy/examples/deployment.yaml`](./deploy/examples/deployment.yaml) 참조.

주요 메트릭:

- `proxy_pool_size{state}` — active / quarantine 게이지
- `proxy_pool_source_size{source}` — 소스별 active 수
- `proxy_request_total{result}` — success / fail / rejected / direct
- `proxy_request_duration_seconds{result}` — histogram
- `proxy_upstream_total{result}` — 업스트림 attempt (재시도 포함)
- `proxy_verify_total{source,result}` — 검증 pass / fail

---

## 프록시 소스 플러그인 만들기

1. `internal/sources/<name>/<name>.go` 파일 생성
2. `Source` 인터페이스 구현
3. `init()`에서 `sources.Register` 호출
4. `cmd/proxy-rotator/main.go`에 blank import 한 줄 추가

### 인터페이스

```go
type Source interface {
    Name() string
    Run(ctx context.Context, pool *pool.Pool, v validator.Validator) error
}
```

### 최소 예시

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

그리고 `main.go`:

```go
import _ "github.com/jadewon/proxy-rotator/internal/sources/mysource"
```

활성화: `SOURCES=mysource`

---

## 동작 원리

```
   HTTP client                 proxy-rotator                  external site
       │                         (:3128)                           ▲
       │  CONNECT / HTTP           │                               │
       ├────────────────────────►  │                               │
       │                           ▼                               │
       │                      [ Router ]                           │
       │                  MATCH? BYPASS? DEFAULT?                  │
       │                           │                               │
       │                           ▼                               │
       │                      [ Pool.Select ]                      │
       │                    P2C (inflight 낮은 쪽)                 │
       │                           │                               │
       │                           ▼                               │
       │                      [ SOCKS5 dial ]──────────────────────┤
       │                           │                               │
       │                    성공 / 실패 ┐                           │
       │                           ▼    ▼                           │
       │                   EWMA / ConsecFails++                     │
       │                  (실패 임계 초과 시 격리)                   │
       │                           │                               │
       ◄───────────────────────────┘                               │
                                                                   │
   ┌── Source plugin: freeproxy ──┐                                │
   │ URL GET → line 파싱 → 병렬   │                                │
   │ Validator → pool.Add         │  POOL_MIN 이하면 가속 interval │
   └──────────────────────────────┘                                │
```

- **Pool은 단일 진실**. 소스 플러그인은 `Add`만, 제거는 RequestTracker(실사용 피드백)와 격리 로직이 담당
- **P2C 선택**: 랜덤 2개 뽑아 `Inflight` 낮은 쪽 → 특정 프록시 과적재 방지
- **투명 재시도**: 멱등 메서드는 실패 시 다른 프록시로 최대 `MAX_RETRIES`회 재시도

---

## 제약사항

- 무료 프록시는 본질적으로 품질 변동성이 크다. 실시간 피드백으로 완화하지만 완전 해결은 불가능
- SOCKS5 공개 프록시는 인증 미지원이 일반적 (`Auth` 필드는 유료 소스용)
- 현재 인스턴스별 독립 풀. 레플리카 간 풀 공유 없음 (향후 Redis 백엔드 고려 가능)

---

## 개발

```bash
go build ./...
go test ./...
go build -o /tmp/proxy-rotator ./cmd/proxy-rotator
```

Docker 이미지:

```bash
docker build -f deploy/Dockerfile -t proxy-rotator .
```

기여 가이드는 [CONTRIBUTING.md](./CONTRIBUTING.md) 참고.

---

## 릴리스

### 자동 (권장)

`v*` 태그를 푸시하면 GitHub Actions가 multi-arch(amd64+arm64) 이미지를 빌드해
Docker Hub (`jadewon/proxy-rotator`)에 push하고 GitHub Release를 생성한다.

```bash
git tag v0.1.0
git push origin v0.1.0
```

필요한 GitHub Secrets:
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN` (Docker Hub Access Token)

### 로컬 (CI 우회)

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

## 라이선스

MIT — [LICENSE](./LICENSE)
