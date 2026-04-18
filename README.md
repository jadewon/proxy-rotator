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
- **SSRF 가드** — 프록시 경유 요청의 타겟이 RFC1918, 루프백, 링크로컬, CGN, `169.254.169.254` (AWS/GCP metadata) 등으로 해석되면 403. 내부망 전용 환경에서 `ALLOW_PRIVATE_TARGETS=true`로 해제 가능.
- **HTTP 서버 타임아웃** — `ReadHeaderTimeout`, `IdleTimeout`, `MaxHeaderBytes`, `MaxBytesReader`로 slow/oversized 요청 차단.
- **프록시 인증 헤더는 업스트림에 전달되지 않음** (검증 후 strip).
- `deploy/examples/networkpolicy.yaml` — 보수적 ingress/egress NetworkPolicy 예시.

---

## 엔드포인트

| Path | 설명 |
|------|------|
| `GET /healthz` | 풀에 active 엔트리 ≥ 1이면 200, 아니면 503 |
| `GET /pool` | 현재 풀 상태 JSON (디버그) |
| `GET /metrics` | Prometheus 포맷 |

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

Git 태그를 푸시하면 GitHub Actions가 자동으로 멀티아키(linux/amd64, linux/arm64) 이미지를 빌드해
Docker Hub (`jadewon/proxy-rotator`)에 push하고 GitHub Release를 생성한다.

```bash
git tag v0.1.0
git push origin v0.1.0
```

필요한 secret:
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN` (Docker Hub Access Token)

---

## 라이선스

MIT — [LICENSE](./LICENSE)
