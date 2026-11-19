# ratatosk-mcp Helm 차트

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

이 차트는 ratatosk MCP 서버를 Streamable HTTP 모드로 동작하는 ClusterIP Service로 배포합니다. 별도의 RBAC이나 시크릿이 필요 없으며, Pod Security Standards(PSS)의 `restricted` 프로파일에서도 경고 없이 동작합니다.

```bash
# 저장소 루트에서 실행
helm install ratatosk-mcp ./charts/ratatosk-mcp
# → MCP 엔드포인트: http://ratatosk-mcp.<namespace>.svc:8080/mcp
```

## values 설정

| 키 | 기본값 | 설명 |
| --- | --- | --- |
| `replicaCount` | `1` | 파드 레플리카 수(2개 이상으로 늘리기 전에 `statelessHttp` 활성화 필요) |
| `image.repository` | `ghcr.io/basphere/ratatosk-mcp` | 컨테이너 이미지 저장소 |
| `image.tag` | 차트 `appVersion` | 고정할 서버 이미지 태그 |
| `image.pullPolicy` | `IfNotPresent` | 컨테이너 이미지 풀 정책 |
| `ratatoskUrl` | `https://ratatosk.io` | 업스트림 API 주소로, 아웃바운드 통신(egress)을 프록시나 미러로 우회할 때 변경 |
| `statelessHttp` | `false` | 세션을 유지하지 않는 무상태 HTTP 모드 활성화(MCP 스펙 `2026-07-28` 리비전 클라이언트를 받거나 레플리카를 2개 이상으로 늘릴 때 필요) |
| `logLevel` | `""` (= info) | `MCP_LOG` 설정값(`info` · `debug` · `warn` · `error` 허용, 그 밖의 값은 경고 없이 `info`로 처리하며 어느 수준에서도 요청 인자 값은 기록하지 않음) |
| `auditMode` | `""` (= 꺼짐) | `MCP_AUDIT` 설정값으로, `metadata`는 도구 호출마다 인자 이름만 기록하고 `full`은 인자 값까지 함께 기록 |
| `rateLimitPerMin` | `0` (= 꺼짐) | `MCP_RATE_LIMIT_PER_MIN` 설정값으로, 직접 통제하지 않는 외부 호출자의 요청을 받는 서버에서 호출자 주소당 분당 요청 상한 지정(모든 MCP 메시지 합산) |
| `maxInflight` | `0` (= 꺼짐) | `MCP_MAX_INFLIGHT` 설정값으로, 호출이 한꺼번에 몰릴 때 메모리 사용량을 제한하기 위한 전체 동시 처리 요청 수 상한(초과 요청은 최대 10초 동안 빈자리를 기다리고 자리가 나지 않으면 `Retry-After`가 붙은 `503` 반환) |
| `cacheMaxMB` | `32` | `MCP_CACHE_MAX_MB` 설정값으로, 응답 캐시 크기 상한(MiB) 지정 |
| `metricsPort` | `0` (= 꺼짐) | `MCP_METRICS_ADDR` 설정값으로, 이 컨테이너 포트의 `GET /metrics`에서 요청 수 등 지표를 제공. Service에는 추가하지 않으므로 파드에서 수집. [지표](../../docs/install.ko.md#지표) 참고 |
| `service.port` | `8080` | Service 및 MCP 엔드포인트 포트 |
| `service.type` | `ClusterIP` | Service 유형 |
| `resources` | requests: CPU 10m, 메모리 16Mi; limits: CPU 100m, 메모리 512Mi | 메모리 설정에 따라 동시에 처리할 수 있는 무거운 호출 수가 달라지고, CPU 설정에 따라 처리 속도가 달라집니다. [메모리와 동시 처리](../../docs/install.ko.md#메모리와-동시-처리)를 참조합니다 |
| `nodeSelector` / `tolerations` / `affinity` | `{}` / `[]` / `{}` | 파드 스케줄링 제어 설정 |
| `clusterDomain` | `cluster.local` | kagent `RemoteMCPServer`에 등록할 Service 전체 도메인 이름의 클러스터 DNS 도메인 |
| `kagent.enabled` | `false` | kagent 연동 리소스 함께 설치(아래 절 참고) |
| `kagent.modelConfig` | `default-model-config` | 클러스터에 설치된 kagent의 `ModelConfig` 이름 |
| `kagent.harness` | `""` | kagent 1.x 전용 설정으로, 예제 에이전트를 실행할 Harness 이름(1.x에서 비워 두면 에이전트 설치 생략) |
| `kagent.agent.enabled` | `true` | 예제 에이전트를 설치합니다. `kagent.enabled`가 켜져 있을 때 적용됩니다 |
| `kagent.agent.name` | `ratatosk-agent` | 예제 에이전트 이름 |
| `kagent.agent.k8sTools` | `true` | 에이전트가 실행 중인 버전을 스스로 확인할 수 있도록 kagent 내장 읽기 전용 클러스터 도구 연결 |
| `kagent.agent.toolServer.name` | `kagent-tool-server` | kagent 도구 서버의 `RemoteMCPServer` 이름(`<kagent 릴리스>-tool-server`) |
| `kagent.agent.toolServer.namespace` | `""` | 도구 서버가 이 릴리스와 다른 네임스페이스에 있을 때 해당 네임스페이스 지정 |

## kagent 연동

```bash
# kagent 0.x
helm install ratatosk-mcp ./charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true
# kagent 1.x: 에이전트를 돌릴 Harness 이름을 지정
helm install ratatosk-mcp ./charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true --set kagent.harness=<이름>
```

차트는 `RemoteMCPServer`와 미리 구성한 `ratatosk-agent`를 함께 설치합니다. kagent는 서버의 도구를 자동으로 모두 찾아냅니다. 에이전트에는 kagent 내장 읽기 전용 클러스터 도구인 `k8s_get_resources`와 `k8s_get_resource_yaml`도 연결되어 있어, 실행 중인 버전을 확인할 수 있습니다. 이 도구를 사용하지 않으려면 `kagent.agent.k8sTools=false`로 설정합니다. kagent CRD가 설치된 클러스터에서만 이 연동을 켭니다.

차트는 클러스터가 제공하는 kagent API 버전을 감지해 kagent 1.x API(`api.kagent.dev/v1alpha3`)가 있으면 1.x 규격으로, 없으면 kagent 0.x(`kagent.dev/v1alpha2`) 규격으로 리소스를 생성합니다. 클러스터에 연결하지 않는 `helm template` 명령으로 1.x 규격 매니페스트를 렌더링할 때는 `--api-versions api.kagent.dev/v1alpha3` 플래그를 함께 전달합니다. kagent 1.x가 정식 출시되면 kagent 0.x 지원은 제거합니다.

kagent 1.x 환경에서는 아래 세 가지를 확인합니다.

- 모든 Agent는 Harness 위에서 실행되지만 kagent 설치 시 기본 Harness가 자동으로 생성되지 않으므로, `kagent.harness`에 해당 네임스페이스의 Harness를 지정하기 전까지 차트는 서버와 `RemoteMCPServer`만 설치하고 에이전트는 건너뜁니다. 추후 에이전트를 추가하는 방법은 설치 완료 안내(NOTES)에 출력됩니다.
- kagent 1.x 에이전트는 짧은 Service 이름을 해석하지 못하므로 `RemoteMCPServer` URL에는 Service 전체 도메인 이름(`<service>.<namespace>.svc.<clusterDomain>`)이 들어갑니다. 클러스터 도메인이 `cluster.local`이 아니면 `clusterDomain` 값을 변경합니다.
- 에이전트의 클러스터 접근은 읽기 전용으로 제한합니다. kagent 런타임에서는 위 도구 목록이 적용되지만, Claude Harness는 kagent 도구 서버의 도구를 쓰기 도구까지 모두 노출합니다. kagent 런타임 Harness를 사용하거나 도구 서버를 읽기 전용으로 구성합니다. [설치 안내](../../docs/install.ko.md#kagent-1x)를 참조합니다.

Helm을 사용하지 않고 설치하는 방법은 [`examples/kagent/`](../../examples/kagent/README.ko.md)를 참조합니다.

## 업그레이드

```bash
helm upgrade ratatosk-mcp ./charts/ratatosk-mcp
```

설치할 때 `--set`이나 `-f`로 지정했던 값은 업그레이드할 때 자동으로 이어지지 않으므로, 같은 값을 다시 지정하거나 `--reuse-values` 옵션을 붙입니다. `image.tag`로 버전을 고정하지 않았다면 컨테이너 이미지는 차트의 `appVersion`을 따릅니다.
