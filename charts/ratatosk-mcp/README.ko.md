# ratatosk-mcp Helm 차트

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

ratatosk MCP 서버를 Streamable HTTP 모드의 ClusterIP Service로 배포합니다.
RBAC도 시크릿도 없고, Pod Security Standards(PSS)의 `restricted`
프로파일에서도 경고 없이 동작합니다.

```bash
# 저장소 루트에서 실행
helm install ratatosk-mcp ./charts/ratatosk-mcp
# → MCP 엔드포인트: http://ratatosk-mcp.<namespace>.svc:8080/mcp
```

## values 설정 항목

| 키 | 기본값 | 의미 |
| --- | --- | --- |
| `replicaCount` | `1` | 레플리카 수. 2개 이상으로 늘리기 전에 `statelessHttp`를 켜세요 |
| `image.repository` | `ghcr.io/basphere/ratatosk-mcp` | 이미지 |
| `image.tag` | 차트 `appVersion` | 특정 서버 버전 고정 |
| `ratatoskUrl` | `https://ratatosk.io` | 업스트림 changes API. egress를 프록시·미러로 우회하면 변경 |
| `statelessHttp` | `false` | 세션 상태 없는 HTTP 제공. MCP 스펙 2026-07-28 리비전 클라이언트와 레플리카 2개 이상에 필요 |
| `logLevel` | `""` (= info) | `MCP_LOG`: `debug`·`warn`·`error`도 허용, 그 밖의 값은 경고 없이 info로 처리. 어느 레벨에서도 요청 인자는 기록되지 않음 |
| `auditMode` | `""` (= 꺼짐) | `MCP_AUDIT`: `metadata`는 도구 호출마다 한 줄(인자 이름만), `full`은 인자 값까지 |
| `service.port` | `8080` | Service / MCP 엔드포인트 포트 |
| `resources` | 소형 requests/limits | 부하가 큰 클러스터라면 조정 |
| `nodeSelector` / `tolerations` / `affinity` | `{}` / `[]` / `{}` | 표준 스케줄링 제어 |
| `clusterDomain` | `cluster.local` | kagent RemoteMCPServer에 등록하는 Service 전체 이름의 클러스터 DNS 도메인 |
| `kagent.enabled` | `false` | kagent 연동까지 함께 설치 (아래 참조) |
| `kagent.modelConfig` | `default-model-config` | kagent 설치의 ModelConfig 이름 |
| `kagent.harness` | `""` | kagent 1.x 전용: 예제 에이전트를 돌릴 Harness. 1.x에서 비워 두면 에이전트를 건너뜀 |
| `kagent.agent.enabled` | `true` | 예제 에이전트 설치 (kagent.enabled일 때) |
| `kagent.agent.name` | `ratatosk-agent` | 예제 에이전트 이름 |
| `kagent.agent.k8sTools` | `true` | kagent 내장 읽기 전용 클러스터 도구를 에이전트에 붙여, 실행 중인 버전을 스스로 알아내게 함 |
| `kagent.agent.toolServer.name` | `kagent-tool-server` | kagent 도구 서버의 RemoteMCPServer 이름(`<kagent 릴리스>-tool-server`) |
| `kagent.agent.toolServer.namespace` | `""` | 도구 서버가 이 릴리스와 다른 네임스페이스에 있을 때 그 네임스페이스 |

## kagent 연동

```bash
# kagent 0.x
helm install ratatosk-mcp ./charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true
# kagent 1.x: 에이전트를 돌릴 Harness 이름을 지정
helm install ratatosk-mcp ./charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true --set kagent.harness=<이름>
```

`RemoteMCPServer`(kagent가 서버의 도구를 모두 자동으로 찾아냅니다)와 미리
구성해 둔 `ratatosk-agent`가 함께 설치됩니다. 에이전트에는 kagent 내장
읽기 전용 클러스터 도구(`k8s_get_resources`, `k8s_get_resource_yaml`)도
붙어서 실행 중인 버전을 스스로 알아냅니다. 끄려면
`kagent.agent.k8sTools=false`로 설정하세요. kagent CRD가 있는
클러스터에서만 켜세요.

차트는 클러스터가 제공하는 kagent API에 맞춰 만듭니다. kagent 1.x
API(`api.kagent.dev/v1alpha3`)가 있으면 1.x 모양으로, 없으면 kagent 0.x
(`kagent.dev/v1alpha2`) 모양으로 만듭니다. `helm template`은 클러스터를 보지
못하므로 1.x 모양이 필요하면 `--api-versions api.kagent.dev/v1alpha3`을 넘기세요.
kagent 0.x 지원은 kagent 1.x가 정식 출시되면 제거합니다.

kagent 1.x에서는:

- 모든 Agent가 Harness 위에서 돌고, kagent는 Harness를 만들어 주지 않습니다.
  `kagent.harness`에 이 네임스페이스의 Harness를 지정하기 전까지 차트는 서버와
  RemoteMCPServer만 설치하고 에이전트는 건너뜁니다. 에이전트를 추가하는 방법은
  설치 후 안내(NOTES)에 나옵니다.
- RemoteMCPServer URL은 Service의 전체 이름
  (`<service>.<namespace>.svc.<clusterDomain>`)입니다. kagent 1.x 에이전트는
  짧은 이름을 풀지 못하기 때문입니다. 클러스터 도메인이 `cluster.local`이
  아니면 `clusterDomain`을 지정하세요.
- 에이전트의 클러스터 접근은 읽기 전용으로 유지하세요. 위 도구 목록은 kagent
  런타임에서는 지켜지지만, Claude Harness는 kagent 도구 서버의 도구를 쓰기
  도구까지 전부 노출합니다. kagent 런타임 Harness를 쓰거나 도구 서버를 읽기
  전용으로 만드세요. [설치 안내](../../docs/install.ko.md#kagent-1x)를 보세요.

Helm 없이 설치하려면 [`examples/kagent/`](../../examples/kagent/README.ko.md)를
보세요.

## 업그레이드

```bash
helm upgrade ratatosk-mcp ./charts/ratatosk-mcp
```

`--set`·`-f`로 지정했던 값은 자동으로 이어지지 않습니다. 같은 값을 다시
지정하거나 `--reuse-values`를 붙이세요. `image.tag`로 고정하지 않았다면
이미지는 차트 `appVersion`을 따릅니다.
