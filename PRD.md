# proxy-rotator PRD

## 개요

SOCKS5 프록시를 다중 소스에서 수집·검증하여 **중앙 풀**로 관리하고, HTTP 포워드 프록시로 제공하는 경량 게이트웨이.
Docker 단독, K8s 사이드카, (옵션) Istio 메쉬 어느 환경에서도 동일한 Go 단일 바이너리로 동작한다.
외부 사이트의 IP 차단 우회를 위해 서비스 코드 변경 없이 `HTTP_PROXY` 설정만으로 사용 가능하다.

## 배경

- 외부 사이트 크롤링 시 클라우드 사업자 IP 차단으로 데이터 수집 불가한 경우가 빈번
- 서비스 코드에 SOCKS5 에이전트를 직접 의존시키면 런타임 호환성/복잡도 증가
- 프록시 수집/검증 로직은 서비스와 분리하여 범용 게이트웨이로 재사용해야 함

## 목표

1. 서비스 코드에서 프록시 관련 의존성 완전 제거
2. 프록시 소스를 **플러그인**으로 추가할 수 있는 구조
3. **중앙 풀**을 단일 진실로 관리하고, 실시간 피드백으로 장애 프록시 자동 격리
4. 어떤 런타임에서도 동작 (Docker 단독 / plain K8s / Istio 메쉬)
5. 단일 Go 정적 바이너리, 추가 런타임 의존성 없음

## 아키텍처

```
┌──────────────────────────────────────────────────────────────┐
│  K8s Pod                                                     │
│                                                              │
│  [앱 컨테이너]                                                │
│    │ HTTP_PROXY=localhost:3128                              │
│    │ (Istio 사용 시 VirtualService로 투명 라우팅 대체 가능)   │
│    ▼                                                         │
│  ┌──────────────────────────────────────────────────────┐    │
│  │  proxy-rotator (Go 단일 바이너리)                     │    │
│  │                                                      │    │
│  │  :3128 HTTP Forward Proxy (CONNECT + HTTP)           │    │
│  │    │                                                 │    │
│  │    ▼  select()                                       │    │
│  │  ┌────────────────────────────────────────────────┐  │    │
│  │  │ 중앙 Pool (sync.RWMutex, 단일 배열)             │  │    │
│  │  │                                                │  │    │
│  │  │  [{addr, source, ewmaLatency, successRate,     │  │    │
│  │  │    consecFails, inflight, lastOk, metadata},   │  │    │
│  │  │   ...]                                         │  │    │
│  │  │                                                │  │    │
│  │  │  Select:  Power-of-Two-Choices                 │  │    │
│  │  │  Eject:   consecFails ≥ 3 → quarantine         │  │    │
│  │  │  Revive:  quarantine TTL 후 재검증              │  │    │
│  │  └────────────────────────────────────────────────┘  │    │
│  │       ▲                         ▲                    │    │
│  │       │ Add/Remove              │ per-request        │    │
│  │       │                         │ feedback           │    │
│  │  ┌────┴──────────────┐    ┌─────┴──────────────┐     │    │
│  │  │ Source Plugins     │    │ RequestTracker     │     │    │
│  │  │  (goroutine 각각)   │    │                    │     │    │
│  │  │                    │    │ success → EWMA     │     │    │
│  │  │  ┌──────────────┐  │    │ fail    → consec++ │     │    │
│  │  │  │ FreeProxy    │  │    │ timeout → fail     │     │    │
│  │  │  └──────────────┘  │    └────────────────────┘     │    │
│  │  │  ┌──────────────┐  │                               │    │
│  │  │  │ Geonode      │  │    ┌────────────────────┐     │    │
│  │  │  └──────────────┘  │    │ Validator (공통)    │     │    │
│  │  │  ┌──────────────┐  │◄───│ TEST_URL 요청 검증  │     │    │
│  │  │  │ Static/File  │  │    └────────────────────┘     │    │
│  │  │  └──────────────┘  │                               │    │
│  │  │  ┌──────────────┐  │                               │    │
│  │  │  │ (사용자 추가)  │  │                               │    │
│  │  │  └──────────────┘  │                               │    │
│  │  └────────────────────┘                               │    │
│  │                                                       │    │
│  │  :8080  /healthz  /metrics  /pool (debug)             │    │
│  └───────────────────────────────────────────────────────┘    │
│                             │                                 │
│                             ▼ SOCKS5                          │
│                        외부 사이트                             │
└──────────────────────────────────────────────────────────────┘
```

## 구성 요소

### 1. HTTP 포워드 프록시

| 항목 | 내용 |
|------|------|
| 구현 | Go `net/http/httputil` 기반 CONNECT 터널 + HTTP forward |
| 포트 | 3128 (localhost only) |
| 업스트림 다이얼 | SOCKS5 (`golang.org/x/net/proxy`) |
| 투명 재시도 | 멱등 메서드(GET/HEAD/OPTIONS) 한해 최대 `MAX_RETRIES`회, 다른 프록시로 |
| 타임아웃 | `PER_PROXY_TIMEOUT` 단위, 재시도 포함 `TOTAL_TIMEOUT` 상한 |

### 2. 중앙 풀 (Pool)

**단일 진실 소스**. 모든 소스 플러그인이 이 배열에 추가하고, HTTP 프록시는 여기서만 선택한다.

```go
type Entry struct {
    Addr         string             // "host:port"
    Source       string             // 어느 플러그인이 추가했는지 (관측용)
    Auth         *Auth              // SOCKS5 user/pass (optional)
    EwmaLatency  time.Duration      // 지수가중평균 지연시간
    SuccessRate  float64            // 최근 윈도우 성공률
    ConsecFails  int                // 연속 실패 횟수 (격리 판단)
    Inflight     int32              // 현재 처리 중 요청 수 (P2C 선택)
    LastOk       time.Time          // 최근 성공 시점
    Metadata     map[string]string  // country, tier 등 확장용
    State        State              // active | quarantine
}
```

**선택 전략**: Power-of-Two-Choices — 랜덤 2개 뽑아 `Inflight` 낮은 쪽. 단일 프록시에 요청 집중 방지.

**격리**: `ConsecFails ≥ EJECT_CONSEC_FAILS`이면 `quarantine` 상태로 전환, `QUARANTINE_DURATION` 후 검증기로 재시도.

**중복 처리**: 여러 소스가 동일 `Addr`를 추가하면 무시 (먼저 등록된 엔트리 유지, `Source` 필드는 첫 등록자).

### 3. 소스 플러그인

각 소스는 독립 goroutine으로 실행되며 자기 수집 주기와 백오프 전략을 가진다. 공통 `Validator`를 주입받아 검증 후 풀에 추가한다.

#### 인터페이스

```go
type Source interface {
    Name() string
    Run(ctx context.Context, pool *Pool, validator Validator) error
}

type Validator interface {
    // 프록시가 TEST_URL에 정상 응답하면 nil, 아니면 error
    Validate(ctx context.Context, proxy RawProxy) error
}

type RawProxy struct {
    Addr     string
    Auth     *Auth
    Metadata map[string]string
}
```

#### 표준 동작 패턴

```go
func (s *MySource) Run(ctx context.Context, pool *Pool, v Validator) error {
    tick := time.NewTicker(s.interval)
    for {
        select {
        case <-ctx.Done(): return nil
        case <-tick.C:
        }
        raws, err := s.fetch(ctx)          // 소스별 수집 로직
        if err != nil { continue }         // 자체 로깅/백오프
        for _, r := range raws {
            if err := v.Validate(ctx, r); err != nil { continue }
            pool.Add(Entry{Addr: r.Addr, Source: s.Name(), ...})
        }
    }
}
```

**핵심 원칙**:
- 플러그인은 자기 수집 주기·에러 처리·재시도 정책에 대해 자율적
- 검증은 공통 Validator에 위임 (일관된 품질 기준)
- 풀에 추가만 하고 제거는 안 한다. 제거는 RequestTracker(실사용 피드백)와 Pool의 격리 로직이 담당

#### 초기 제공 플러그인

| 이름 | 설명 |
|------|------|
| `freeproxy` | iplocate/free-proxy-list의 socks5.txt 크롤 |
| `file` | `/etc/proxy-rotator/proxies.txt` 정적 목록 (테스트/수동 관리용) |

새 소스는 `internal/sources/*`에 파일 추가 + `cmd/main.go`에서 `sources.Register()` 한 줄로 등록.

### 4. Validator (공통)

| 항목 | 내용 |
|------|------|
| 검증 방식 | SOCKS5 프록시 경유로 `TEST_URL` 요청, HTTP 200 확인 |
| 타임아웃 | `TEST_TIMEOUT` (기본 8초) |
| 동시성 | Source가 자체 goroutine pool로 호출 (Validator는 호출 단위) |

### 5. RequestTracker (피드백 루프)

HTTP 프록시 핸들러가 매 요청 결과를 Pool에 반영한다.

```
성공: EwmaLatency 업데이트, ConsecFails=0, LastOk=now
실패: ConsecFails++, 임계 초과 시 Pool.Quarantine()
```

배치 검증이 놓치는 "지금 이 순간 죽은 프록시"를 실시간으로 반영한다.

## 라우팅 규칙

프록시 경유 대상 결정은 **3개 레이어에서 지원**. 환경에 맞게 하나 또는 조합해 사용.

### Layer A: 앱 환경변수 (가장 일반적)

런타임 종류와 무관하게 동작. 언어별 HTTP 클라이언트가 표준적으로 지원.

```
HTTP_PROXY=http://localhost:3128
HTTPS_PROXY=http://localhost:3128
NO_PROXY=.cluster.local,.svc,localhost,127.0.0.1
```

### Layer B: proxy-rotator 내부 매치 규칙

앱은 모든 트래픽을 프록시로 보내고, proxy-rotator가 host 기반으로 선별.

```
MATCH_HOSTS=target-site.com,*.another.com   # 매칭 시 SOCKS5 경유
BYPASS_HOSTS=.cluster.local,.internal       # 직접 접속
DEFAULT_ACTION=direct                       # reject | direct | proxy
```

### Layer C: Istio VirtualService (Istio 사용자만)

앱 코드/환경변수 완전 무변경. Layer A/B와 달리 sidecar 투명 라우팅.

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

세 레이어 모두 동작 가능하도록 proxy-rotator는 "받은 모든 요청을 지정된 규칙에 따라 처리"하는 단순 모델로 설계.

## 환경변수

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `LISTEN_PORT` | `3128` | HTTP 프록시 포트 |
| `ADMIN_PORT` | `8080` | `/healthz`, `/metrics`, `/pool` |
| `TEST_URL` | `https://example.com` | 검증용 요청 대상 (실 타겟 URL 권장) |
| `TEST_TIMEOUT` | `8s` | 검증 타임아웃 |
| `PER_PROXY_TIMEOUT` | `8s` | 실 요청 업스트림 타임아웃 |
| `TOTAL_TIMEOUT` | `30s` | 재시도 포함 총 타임아웃 |
| `MAX_RETRIES` | `2` | 멱등 요청 재시도 횟수 |
| `POOL_MIN` | `5` | 이하로 떨어지면 소스 플러그인이 가속 주기로 전환하라는 신호 |
| `POOL_MAX` | `50` | 풀 상한 |
| `EJECT_CONSEC_FAILS` | `3` | 격리 임계값 |
| `QUARANTINE_DURATION` | `5m` | 격리 후 재검증까지 대기 |
| `MATCH_HOSTS` | (비어있음) | 프록시 경유 host 패턴 (콤마 구분) |
| `BYPASS_HOSTS` | `.cluster.local,.svc,localhost` | 직접 접속 host |
| `DEFAULT_ACTION` | `proxy` | `proxy` \| `direct` \| `reject` |
| `SOURCES` | `freeproxy` | 활성화 소스 (콤마 구분) |
| `SOURCE_FREEPROXY_URL` | TheSpeedX/PROXY-List socks5.txt | freeproxy 플러그인 소스 URL |
| `SOURCE_FREEPROXY_INTERVAL` | `10m` | 평시 수집 주기 |
| `SOURCE_FREEPROXY_INTERVAL_LOW` | `30s` | `POOL_MIN` 이하일 때의 수집 주기 |
| `SOURCE_FREEPROXY_CONCURRENCY` | `20` | 검증 동시성 |
| `SOURCE_FREEPROXY_HTTP_TIMEOUT` | `30s` | 소스 HTTP GET 타임아웃 |
| `SOURCE_FILE_PATH` | `/etc/proxy-rotator/proxies.txt` | file 플러그인 경로 |
| `SOURCE_FILE_INTERVAL` | `60s` | file 플러그인 재스캔 주기 |
| `SOURCE_FILE_CONCURRENCY` | `10` | file 플러그인 검증 동시성 |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

## 배포 시나리오

### 1. Docker 단독

```bash
docker run --rm -p 3128:3128 -p 8080:8080 \
  -e TEST_URL=https://example.com \
  -e SOURCES=freeproxy \
  jadewon/proxy-rotator:latest
```

앱에서 `HTTP_PROXY=http://host.docker.internal:3128` (또는 Compose 네트워크의 서비스명).

### 2. K8s Plain (Istio 없음)

`deploy/examples/deployment.yaml` 사용. 독립 `Deployment + Service`로 배포하고 클러스터 내 여러 워크로드가 공유 사용.

```yaml
# 클라이언트 Deployment
env:
  - name: HTTP_PROXY
    value: "http://proxy-rotator.default.svc:3128"
  - name: HTTPS_PROXY
    value: "http://proxy-rotator.default.svc:3128"
  - name: NO_PROXY
    value: ".cluster.local,.svc,localhost"
```

또는 `deploy/examples/sidecar.yaml`을 Deployment의 `containers`에 병합하여 파드당 사이드카 모델로.

### 3. K8s + Istio (옵션)

`deploy/examples/istio.yaml`의 `ServiceEntry` + `VirtualService`를 추가하면 앱 환경변수 없이 투명 라우팅. 위 2번과 독립 선택 가능.

## 헬스체크

| 엔드포인트 | 응답 기준 |
|-----------|-----------|
| `GET /healthz` | 풀 크기 ≥ 1이면 200, 아니면 503 |
| `GET /metrics` | Prometheus 포맷 |
| `GET /pool` | 현재 풀 상태 JSON (디버그용) |

## 관측

**메트릭 (Prometheus)**:
- `proxy_pool_size{state="active|quarantine"}`
- `proxy_pool_source_size{source="freeproxy|file|..."}`
- `proxy_request_total{result="success|fail|retry"}`
- `proxy_request_duration_seconds` (histogram)
- `proxy_upstream_total{proxy="host:port", result="success|fail"}`
- `proxy_verify_total{source, result}`

**트레이싱**: Istio 환경에선 Envoy가 W3C Trace Context를 자동 전파. 메쉬 없는 환경에서는 필요 시 `otelhttp` 미들웨어로 수동 계측 (초기 버전 미포함).

## 프로젝트 구조

```
proxy-rotator/
├── PRD.md
├── go.mod / go.sum
├── cmd/proxy-rotator/main.go          # 소스 등록, 의존성 와이어링
├── internal/
│   ├── pool/
│   │   ├── pool.go                    # 중앙 풀, 선택 전략
│   │   └── entry.go
│   ├── proxy/
│   │   └── handler.go                 # HTTP CONNECT + forward, 재시도
│   ├── validator/
│   │   └── validator.go               # TEST_URL 검증
│   ├── tracker/
│   │   └── tracker.go                 # 요청 결과 피드백
│   ├── router/
│   │   └── router.go                  # MATCH/BYPASS/DEFAULT 규칙
│   ├── sources/
│   │   ├── source.go                  # Source 인터페이스
│   │   ├── freeproxy/freeproxy.go
│   │   └── file/file.go
│   └── metrics/metrics.go
├── deploy/
│   ├── Dockerfile                     # 멀티스테이지, distroless
│   └── examples/                      # deployment / sidecar / Istio 예시
├── README.md
└── LICENSE
```

## 마일스톤

| 단계 | 내용 |
|------|------|
| M1 | Pool + HTTP 프록시 + `file` 소스로 로컬 동작 (Docker) |
| M2 | `freeproxy` 소스 + Validator + RequestTracker + 격리/재시도 |
| M3 | 메트릭 + 배포 예시(Docker/K8s/Istio) |
| M4 | README + 공개 리포 배포 (OSS) |
| M5 | 실제 서비스 통합/운영 |

## 제약사항

- 무료 프록시 의존 시 가용성/품질 불안정 → 투명 재시도 + 실시간 피드백으로 완화하되 완전 해결은 불가
- SOCKS5 공개 프록시는 인증 미지원이 일반적 (`Auth` 필드는 유료 소스 확장용)
- 인스턴스별 독립 풀 전제 (여러 레플리카 간 공유 없음)

## 미래 요구사항 (낮은 우선순위)

초기 구현에 포함하지 않지만, **인터페이스 설계 시 확장 여지를 남겨둘** 항목들.

### 라우팅/선택
- Geographic 라우팅 (국가별 프록시 선택) — `Entry.Metadata["country"]` 필드로 스텁만
- Sticky session (쿠키/헤더 해시 기반 동일 프록시 재선택)
- 타겟별 전용 풀 (named pool 분리)
- 우선순위 티어 (유료 프록시 우선 사용) — `Entry.Metadata["tier"]` 스텁만

### 프록시 소스
- 유료 프록시 API 플러그인 (Bright Data, Smartproxy 등)
- 자체 소유 프록시 플러그인 (고정 고품질)
- SOCKS5 인증 (username/password)
- SOCKS4 / HTTP 프록시 백엔드 수용

### 품질/제어
- 응답 내용 검증 (CAPTCHA/차단 페이지 패턴 감지)
- 프록시당 rate limit (token bucket)
- 타겟 사이트별 circuit breaker
- Warm start (재시작 시 이전 풀 복원, 디스크 캐시)
- 응답시간 기반 가중 선택

### 관측
- Admin API (수동 ban/추가)
- ConfigMap hot-reload (재시작 없이 환경변수 반영)

### 보안
- 호스트 allowlist (오남용 방지)
- 자체 인증 (Basic/mTLS) — 여러 namespace 공유 시 필요
- User-Agent 로테이션/헤더 주입

### 배포/스케일
- DaemonSet 모드 (노드 단위 공유)
- 레플리카 간 풀 공유 (Redis 백엔드)
- HPA (풀 고갈률 기반)

### 프로토콜
- HTTP/2 upstream
- SOCKS5 remote DNS (업스트림에서 DNS 해석)
- HTTP/3 (QUIC)
