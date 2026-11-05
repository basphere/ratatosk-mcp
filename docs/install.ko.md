# 설치와 사용법

[English](install.en.md) · [한국어](install.ko.md) · [日本語](install.ja.md)

이 페이지는 Ratatosk가 정리한 릴리스 변경 내역을 AI 에이전트에서 쓰는 네 가지
방법(설치 없이 호스팅 엔드포인트에 연결, 노트북에서 stdio 서버 실행, 쿠버네티스
클러스터에 Helm으로 배포, kagent 연동)을 설명합니다. 클라이언트마다 서버를 등록하는 방법은
[클라이언트에 엔드포인트 등록하기](#클라이언트에-엔드포인트-등록하기)와
[다른 stdio 클라이언트](#다른-stdio-클라이언트)에 정리되어 있습니다.

**MCP(Model Context Protocol)**는 AI 에이전트가 외부 도구를 호출할 때 쓰는
표준 프로토콜입니다. ratatosk-mcp는 이 프로토콜로 도구 6개를 제공합니다.
`check_stack`은 실행 중인 버전 목록을 받아 서버 프로세스 안에서 비교하고, 업그레이드
경로별로 업그레이드 전 확인 · 결정이 필요한 변경 · 버전 번호가 건너뛴 곳을
돌려줍니다. 나머지는 `check_upgrade`(한 프로젝트의 업그레이드 한 번을 서버가 계산),
`get_release`(릴리스 하나 전체), `list_releases`(최근 릴리스 요약),
`search`(보안 공지 번호 · 플래그 · 글자로 찾기), `list_projects`(추적 중인 프로젝트와
슬러그)입니다. Claude, ChatGPT, Grok, Claude Code, Codex, Gemini CLI,
Antigravity, kagent, 직접 만든 SDK 에이전트처럼 MCP 클라이언트라면 무엇이든
연결할 수 있습니다.

## 실행 방식 고르기

| 이런 상황이라면 | 방법 | 섹션 |
| --- | --- | --- |
| 아무것도 설치하지 않고 일단 써 보고 싶다 | 호스팅 엔드포인트 URL 등록 | [호스팅](#호스팅-엔드포인트) |
| 노트북에서 Claude Code · Claude Desktop · Codex · Gemini CLI 등 stdio MCP 클라이언트 사용 | `docker run` (stdio) | [로컬](#로컬-stdio) |
| 쿠버네티스에서 자체 에이전트 운용 (프레임워크 무관, CI 잡, SDK 클라이언트) | Helm 차트 | [클러스터 내 단독 배포](#클러스터-내-단독-배포-helm) |
| [kagent](https://kagent.dev) 사용 중(0.x 또는 1.x) | `kagent.enabled=true` Helm 옵션 또는 매니페스트 | [kagent 연동](#kagent-연동) |

네 방식 모두 같은 도구 6개로 같은 공개 데이터를 제공하며, 어느 모드든 계정도
API 키도 필요 없습니다. 방식에 따라 달라지는 것은 다음과 같습니다.

- **`check_stack`에 넘긴 실행 중인 버전이 어디까지 가는가.** 설치형은 버전
  비교가 사용자 프로세스 안에서 끝나 그 버전이 인프라를 떠나지 않습니다.
  호스팅은 버전이 서버 메모리를 지나가지만 어떤 로그에도 기록되지 않습니다.
- **어떤 요청 한도가 적용되는가.** 호스팅은 호출자마다 분당 MCP 요청 60회를
  허용하고, 서버가 업스트림 API를 부르는 몫은 호스팅을 쓰는 호출자 모두가 함께
  씁니다. 설치형에는 업스트림 API 한도(IP당 분당 1200회)가 내 IP를 기준으로
  적용됩니다.
- **감사 스트림을 남길 수 있는가.** 설치형만 가능합니다.

한 가지 공통점도 알아두세요. `get_release`처럼 특정 버전을 인자로 받는
도구는 어느 방식에서든 그 버전을 요청 경로에 넣어 업스트림에서 조회합니다.
조회 대상을 지목하는 값이기 때문입니다. 다만 그 경로도 서버 쪽 로그에는
남지 않습니다. 접근 로그를 쓰기 전에 쿼리 문자열을 지우고
`/v2/releases/…`·`/v2/upgrade/…` 경로는 앞부분만 남기기 때문입니다. 각
섹션에서 자세히 설명합니다.

## 사전 준비

- **호스팅**: 원격 MCP 서버(Streamable HTTP, 스트리밍 방식의 HTTP
  트랜스포트)를 지원하는 클라이언트만 있으면 됩니다. 그 밖에 준비할 것은
  없습니다.
- **로컬(stdio)**: Docker. 소스에서 빌드하려면 Go 1.26 이상.
- **예시의 `claude mcp add` 명령**: [Claude Code](https://claude.com/claude-code)
  CLI(`claude` 명령)가 설치되어 있어야 합니다. 다른 MCP 클라이언트를 쓴다면
  각자의 등록 방법을 따르면 되므로 필수는 아닙니다.
- **클러스터(Helm)**: 쿠버네티스 클러스터, Helm 3, 그리고 이 저장소를
  클론한 사본이 필요합니다. 차트는 별도 차트 저장소나 OCI 레지스트리로
  발행되지 않고 이 저장소 안에만 있습니다.
- **아웃바운드 HTTPS(egress)**: 설치형은 어느 방식이든 서버 프로세스가
  `ratatosk.io:443`으로 나가는 HTTPS 연결을 열 수 있어야 합니다.
  NetworkPolicy나 egress 프록시로 아웃바운드를 제한하는 클러스터라면 이
  목적지를 허용 목록에 추가하세요. 프록시나 미러를 경유해야 한다면
  `RATATOSK_URL`(차트 값: `ratatoskUrl`)로 업스트림 주소를 바꿀 수
  있습니다. 차단된 채로 실행하면 도구가 오류를 돌려줍니다(`check_stack`은
  "could not read the project list"). 일부 요청만 실패하면 `check_stack`은
  그대로 답하고, 영향받은 컴포넌트의 `note`에 `fetch failed: …`를 담습니다.
  [문제 해결](#문제-해결)을 보세요.
- **kagent 통합**: kagent가 이미 설치된 클러스터(kagent CRD 포함). kagent
  1.x라면 예제 에이전트를 돌릴 Harness도 필요합니다([kagent 1.x](#kagent-1x) 참조).

## 호스팅 엔드포인트

`https://ratatosk.io/mcp`는 설치 없이 바로 쓰는 원격 MCP 엔드포인트입니다.
Streamable HTTP로 동작하며 무상태(stateless)입니다: 세션이 없어
`Mcp-Session-Id` 왕복 없이 요청 하나하나가 독립적으로 처리됩니다.

### 클라이언트에 엔드포인트 등록하기

주소는 어느 클라이언트에서나 같고, 넣는 곳만 다릅니다.

| 클라이언트 | `https://ratatosk.io/mcp`를 등록하는 곳 |
|---|---|
| Claude(웹 · 데스크톱) | Customize → Connectors → + Add → Add custom connector. Team · Enterprise 플랜에서는 소유자가 Organization settings → Connectors에서 추가하고, 멤버는 Customize → Connectors에서 연결 ([공식 설명서](https://support.claude.com/en/articles/11175166-getting-started-with-custom-connectors-using-remote-mcp)) |
| ChatGPT(웹) | chatgpt.com/plugins에서 + → Add custom MCP server로 들어가 서버 URL에 주소를 넣고, 인증은 쓰지 않음 ([공식 설명서](https://developers.openai.com/api/docs/guides/custom-mcp-server)) |
| Grok | grok.com/connectors에서 New Connector → Custom ([공식 설명서](https://docs.x.ai/grok/connectors)) |
| Claude Code | `claude mcp add --transport http ratatosk https://ratatosk.io/mcp` ([공식 설명서](https://code.claude.com/docs/en/mcp)) |
| Grok Build | `grok mcp add --transport http ratatosk https://ratatosk.io/mcp` ([공식 설명서](https://docs.x.ai/build/features/mcp-servers)) |
| Codex | 설정 파일 `~/.codex/config.toml`: `[mcp_servers.ratatosk]` 테이블에 `url = "https://ratatosk.io/mcp"` ([공식 설명서](https://learn.chatgpt.com/docs/extend/mcp)) |
| Gemini CLI | 설정 파일 `~/.gemini/settings.json`: `mcpServers.ratatosk` 아래에 `"httpUrl": "https://ratatosk.io/mcp"` ([공식 설명서](https://geminicli.com/docs/tools/mcp-server/)) |
| Antigravity | 설정 파일 `~/.gemini/config/mcp_config.json`: `mcpServers.ratatosk` 아래에 `"serverUrl": "https://ratatosk.io/mcp"` ([공식 설명서](https://antigravity.google/docs/mcp)) |
| kagent | `RemoteMCPServer` 리소스로 등록. [kagent 연동](#kagent-연동) 참조 |

Gemini CLI와 Antigravity는 둘 다 `~/.gemini` 아래 파일을 읽지만, 주소를 적는
키가 다릅니다. Gemini CLI는 `httpUrl`, Antigravity는 `serverUrl`입니다.
Claude Code라면:

```bash
claude mcp add --transport http ratatosk https://ratatosk.io/mcp
```

### 확인

클라이언트 없이도 엔드포인트가 살아 있는지 curl로 확인할 수 있습니다:

```bash
curl -s -X POST https://ratatosk.io/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1.0"}}}'
```

응답은 SSE(Server-Sent Events) 프레이밍으로 옵니다. `Content-Type`이
`text/event-stream`이어도 실패가 아닙니다. `data:` 줄의 JSON에
`"serverInfo"`와 `"name":"ratatosk"`가 보이면 정상입니다:

```
event: message
data: {"jsonrpc":"2.0","id":1,"result":{…,"serverInfo":{"name":"ratatosk","version":"1.0.0"}}}
```

Claude Code라면 `claude mcp list` 출력에서 확인하세요:

```
ratatosk: https://ratatosk.io/mcp (HTTP) - ✔ Connected
```

### 알려진 동작과 제한

- **SSE GET은 405**: 무상태 모드는 서버가 먼저 보내는 알림 스트림(GET으로
  여는 SSE)을 제공하지 않으므로 405 응답이 정상 동작입니다. 브라우저
  주소창으로 열면 안내 페이지(`/docs/mcp`)로 리다이렉트됩니다.
- **적정 사용**: 호스팅 엔드포인트는 **호출자당 분당 요청 60회**를
  허용합니다. 한도를 호출자 주소별로 따로 세므로, 한 호출자가 요청을 많이
  보내도 다른 호출자에게는 영향이 없습니다. MCP 메시지는 모두 셉니다. 클라이언트는
  연결할 때 두세 번(`initialize`, `notifications/initialized`, `tools/list`)을
  쓰고, 그 뒤로는 도구 호출마다 한 번입니다. 넘기면 `Retry-After`가 붙은
  `429`가 돌아옵니다. 업스트림에서는 도구 호출 1회가 API 요청 여러 번이 될 수
  있지만(`check_stack`은 컴포넌트마다 그 프로젝트의 최근 릴리스를 모두
  읽습니다), 그 비용은 서버가 지고 내 60회에는 들어가지 않습니다. 직접
  `/v2`를 호출하면 자기 IP 몫으로 분당 1200회를 받습니다. 폴링처럼 사용량이
  많은 용도라면 설치형으로 전환하세요.

### 프라이버시: 호스팅 엔드포인트가 기록하는 것

호스팅에서는 `check_stack` 인자(실행 중인 버전 목록)가 서버 메모리를
지나가지만, 요청 내용은 어디에도 기록되지 않습니다:

- MCP 서버 로그에는 시작 줄과 업스트림 오류 줄만 남고, 정상 요청은
  기록되지 않습니다. 오류 줄에도 요청 인자는 담기지 않습니다.
- 업스트림 API 요청 로그는 호출자가 `traceparent`를 보낸 경우에만 한 줄
  남으며, 그 줄에도 정규화된 엔드포인트 라벨과 trace_id뿐입니다. 경로·쿼리는
  기록되지 않습니다.
- 서버 앞단의 접근 로그는 쿼리 문자열을 지우고, 슬러그·버전이 경로에
  실리는 `/v2/releases/…`·`/v2/upgrade/…` 경로는 앞부분만 남기며, IP를
  가리고 요청 본문은 기록 항목에 아예 없습니다. 서버 안에서 7일 안에
  순환 삭제되고, 사본 하나가 운영자의 중앙 로그 저장소로 가며 그곳에서도
  7일 동안 보관됩니다.
- 아래에서 설명할 감사 스트림은 호스팅에서 일부러 꺼 두었고, 앞으로도
  켜지 않습니다.

다만 Ratatosk가 통제할 수 없는 경계가 하나 있습니다. ratatosk.io 앞에는
Cloudflare가 있고, Cloudflare가 암호화를 풀었다가 다시 걸어 중계하므로 요청 내용 전체(호스팅
`check_stack` 호출에 담긴 버전 포함)가 Cloudflare를 지납니다. 그 처리는
Cloudflare의 정책을 따릅니다. 이 경계가 요건에 맞지 않으면 설치형을 쓰세요.
그러면 `check_stack`에 넘긴 버전이 프로세스 밖으로 나가지 않습니다.

## 로컬 (stdio)

stdio는 MCP 클라이언트가 서버를 자식 프로세스로 띄우고 표준 입출력으로
대화하는 방식입니다. 노트북에서 Claude Code, Claude Desktop, Codex, Gemini CLI,
Antigravity와 함께 쓸 때의 기본 경로입니다.

### Claude Code

```bash
claude mcp add ratatosk -- docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0
```

`1.0.0` 자리에는 원하는 [릴리스 태그](https://github.com/basphere/ratatosk-mcp/releases)를
쓰면 되고, 항상 최신을 따라가려면 `latest`를 쓰세요.
[버전 고정](#버전-고정)을 보세요.

### Claude Desktop

Claude Desktop은 CLI 대신 설정 파일(`claude_desktop_config.json`)로 MCP
서버를 등록합니다. `mcpServers`에 다음을 추가하세요:

```json
{
  "mcpServers": {
    "ratatosk": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "ghcr.io/basphere/ratatosk-mcp:1.0.0"]
    }
  }
}
```

설정 파일 위치는 운영체제마다 다르므로 Claude Desktop 문서를 참조하세요.
설치 없이 쓰려면 위 [호스팅 엔드포인트](#호스팅-엔드포인트)를 원격
커넥터로 등록해도 됩니다.

### 다른 stdio 클라이언트

아래 클라이언트는 모두 같은 명령
`docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0`을 실행하고, 그 명령을
넣는 곳만 다릅니다.

| 클라이언트 | 넣는 곳 |
|---|---|
| ChatGPT 데스크톱 앱 | 설정 → MCP servers → Add server에서 STDIO를 선택하고 명령 입력. Codex와 `~/.codex/config.toml`을 함께 씀 ([공식 설명서](https://learn.chatgpt.com/docs/extend/mcp)) |
| Codex | 설정 파일 `~/.codex/config.toml`: `[mcp_servers.ratatosk]`에 `command = "docker"`, `args = ["run", "--rm", "-i", "ghcr.io/basphere/ratatosk-mcp:1.0.0"]`, `startup_timeout_sec = 60`(첫 실행 때 이미지를 받기 때문) |
| Gemini CLI | 설정 파일 `~/.gemini/settings.json`: 위 Claude Desktop과 같은 `mcpServers` 블록 |
| Antigravity | 설정 파일 `~/.gemini/config/mcp_config.json`: 위 Claude Desktop과 같은 `mcpServers` 블록 |
| Grok Build | `grok mcp add ratatosk -- docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0` |

ChatGPT 웹과 grok.com은 내 컴퓨터에서 명령을 실행하지 못하고 URL로만
연결하므로, 호스팅 엔드포인트를 씁니다.

### 소스 빌드

Docker 없이 바이너리로 쓰려면:

```bash
go build -o ratatosk-mcp .
claude mcp add ratatosk -- /path/to/ratatosk-mcp
```

stdio를 지원하는 MCP 클라이언트라면 무엇이든 같은 방식입니다.

### 확인

```bash
claude mcp list
# ratatosk: docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0 - ✔ Connected
```

그리고 에이전트에게 이렇게 물어보세요:

> "envoy v1.36.8과 istio 1.30.1을 쓰는데, 업그레이드 전에 조치할 게
> 있어?"

에이전트가 `check_stack`을 호출해, 지금 버전 이후의 보안 수정과 호환성
변경(기능 제거 · 기본값 변경 등)을 근거 인용과 함께 알려 주면 성공입니다.

## 클러스터 내 단독 배포 (Helm)

`MCP_HTTP_ADDR`를 설정하면 같은 바이너리가 `/mcp`에서 Streamable HTTP로
MCP를 제공합니다(`/healthz` 헬스 체크 포함). 차트는 이를 ClusterIP Service로
배포하고 `MCP_HTTP_ADDR`를 `service.port`(기본 8080)에 맞춰 자동
설정합니다. 클러스터 안에서는 프로세스를 따로 띄울 필요 없이 URL로
접속하면 됩니다. SDK로 만든 자체 에이전트, 다른 에이전트 프레임워크,
업그레이드 전에 `check_stack`으로 배포를 막아 세우는 CI 잡까지 모두
마찬가지입니다.

### 설치

```bash
git clone https://github.com/basphere/ratatosk-mcp
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp
```

RBAC도 시크릿도 필요 없고, Pod Security Standards(PSS)의 `restricted`
프로파일에서도 경고 없이 동작합니다. 차트 값 전체는
[차트 README](../charts/ratatosk-mcp/README.ko.md)에 나와 있습니다.
다음 두 경우에는 `--set statelessHttp=true`를 켜세요. MCP 스펙 2026-07-28
리비전을 쓰는 최신 클라이언트(`Mcp-Protocol-Version: 2026-07-28` 헤더를
보내는 클라이언트)를 HTTP로 받을 때, 그리고 레플리카를 2개 이상으로 늘릴
때입니다. [설정 레퍼런스](#설정-레퍼런스)를 보세요.

### 확인

파드가 떠 있는지:

```bash
kubectl get pods -l app.kubernetes.io/name=ratatosk-mcp
# NAME                            READY   STATUS    RESTARTS   AGE
# ratatosk-mcp-6f7b9c8d4-x2m5q    1/1     Running   0          30s
```

시작 로그 한 줄에 수신 주소와 업스트림이 표시됩니다:

```bash
kubectl logs deploy/ratatosk-mcp
# {"time":"…","level":"INFO","msg":"listening","service":"mcp","transport":"http","addr":":8080/mcp","mode":"stateful","upstream":"https://ratatosk.io","version":"1.0.0","rate_limit":"off","max_inflight":"off"}
```

헬스 체크까지 확인하려면:

```bash
kubectl port-forward svc/ratatosk-mcp 8080:8080 &
sleep 2   # 포워드가 준비되기 전에 curl이 돌면 connection refused가 납니다
curl -i http://localhost:8080/healthz
# HTTP/1.1 200 OK
```

확인이 끝나면 백그라운드로 띄운 port-forward를 `kill %1`로 정리하세요.

### 접속

클러스터 안의 MCP 클라이언트를 다음 URL로 연결하세요:

```
http://ratatosk-mcp.<namespace>.svc:8080/mcp
```

### 업그레이드

```bash
git -C ratatosk-mcp pull
helm upgrade ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp
```

설치할 때 `--set`으로 값을 바꿨다면 업그레이드에도 같은 값을 다시
지정하거나 `--reuse-values`를 붙이세요.

## kagent 연동

kagent 1.x는 새 API(`api.kagent.dev/v1alpha3`)를 쓰고 0.x API(`kagent.dev/v1alpha2`)를
읽지 않습니다. 반대도 마찬가지입니다. Helm 차트는 맞는 쪽을 스스로 고르고,
매니페스트는 kagent 계열마다 따로 있습니다. 클러스터가 어느 계열인지는 이렇게
확인합니다:

```bash
kubectl api-resources --api-group=api.kagent.dev   # agents가 나오면 kagent 1.x
kubectl api-resources --api-group=kagent.dev       # agents가 나오면 kagent 0.x
```

kagent 0.x 지원은 kagent 1.x가 정식 출시되면 제거합니다.

### kagent 0.x

결과가 같은 두 가지 방법입니다. 하나만 고르세요.

**A. Helm 토글**: 설치 한 번에 서버, kagent 등록(RemoteMCPServer),
예제 에이전트 `ratatosk-agent`까지 설치됩니다:

```bash
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true
```

`kagent.modelConfig` 기본값은 `default-model-config`입니다. 환경이 다르면
덮어쓰고, 예제 에이전트 없이 등록만 원하면 `kagent.agent.enabled=false`로
설정하세요.

**B. 매니페스트**: 같은 세 조각을 Helm 없이 복사해 붙여 넣는 방식으로
설치합니다([상세](../examples/kagent/0.x/README.ko.md)):

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml
kubectl apply -f $BASE/0.x/ratatosk-remote-mcpserver.yaml
kubectl apply -f $BASE/0.x/ratatosk-agent.yaml
```

이 경로는 클론이 필요 없습니다. 매니페스트에는 `namespace: kagent`가 들어 있습니다.

### kagent 1.x

**먼저 Harness.** kagent 1.x는 모든 Agent를 Harness(런타임, Agent Substrate
WorkerPool, 스냅샷 위치) 위에서 돌리는데, kagent를 설치해도 Harness는 생기지
않습니다. 설치할 네임스페이스에 있는지 확인하세요:

```bash
kubectl get harnesses.api.kagent.dev -n kagent
```

없으면 하나 만드세요. 방법은 kagent의
[Agent harness](https://kagent.dev/docs/kagent/1.x/agents/agent-harness/)
안내에 있습니다. 이 에이전트에는 kagent 런타임(`spec.kagent: {}`)을 쓰세요.
이유는 아래 읽기 전용 안내에 있습니다.

**A. Helm 토글**, Harness 이름을 함께 지정:

```bash
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true --set kagent.harness=<Harness 이름>
```

차트는 클러스터의 1.x API를 보고 1.x 객체를 만듭니다. `kagent.harness`가
없으면 서버와 RemoteMCPServer만 설치하고 에이전트는 건너뛰며, 설치 후
안내(NOTES)에 나중에 추가하는 방법(`helm upgrade --reuse-values --set
kagent.harness=<이름>`)이 나옵니다.

**B. 매니페스트**([상세](../examples/kagent/1.x/README.ko.md)):

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml
kubectl apply -f $BASE/1.x/ratatosk-remote-mcpserver.yaml
curl -fsSLO $BASE/1.x/ratatosk-agent.yaml
# spec.harnessRef.name(자리표시자 your-harness)을 내 Harness 이름으로 고친 뒤:
kubectl apply -f ratatosk-agent.yaml
```

**Service 전체 이름.** RemoteMCPServer는 `ratatosk-mcp`가 아니라
`ratatosk-mcp.kagent.svc.cluster.local`을 가리킵니다. kagent 1.x 에이전트는
egress 정책에 적힌 호스트 이름만 풀리는 샌드박스에서 돌기 때문에, 짧은 Service
이름은 그 안에서 풀리지 않습니다. 차트는 릴리스 네임스페이스와
`clusterDomain`(기본값 `cluster.local`)으로 이 이름을 만듭니다. 매니페스트를 쓸
때 클러스터 도메인이 다르면 URL을 고치세요.

#### 에이전트의 클러스터 접근은 읽기 전용으로

kagent 도구 서버(`kagent-tool-server`)에는 `k8s_apply_manifest`,
`k8s_delete_resource`, `k8s_scale` 같은 쓰기 도구도 있습니다. 예제 에이전트는
`k8s_get_resources`와 `k8s_get_resource_yaml`만 지정하고, kagent 런타임은 이
목록을 지킵니다. Claude Harness(`spec.claude`)는 서버의 일부 도구만 내줄 수
없어서 도구 서버의 도구를 전부 에이전트에게 주고, Agent 상태에 경고만
남깁니다. 그러면 지시문의 "클러스터를 바꾸지 말라"는 문장 하나만 남습니다.
그래서 둘 중 하나를 하세요:

- 이 에이전트를 kagent 런타임 Harness에서 돌리거나,
- kagent 설치에서 도구 서버를 읽기 전용으로 만드세요.
  `--set 'kagent-tools.tools.args={--read-only}'`를 주면 도구 서버가 쓰기
  도구를 아예 등록하지 않습니다. `kagent-tools.rbac.readOnly=true`는 도구 서버의
  cluster-admin 권한을 읽기 전용 권한으로 바꾸지만, 그 권한에는 nodes와 커스텀
  리소스가 없습니다. `kagent-tools.rbac.additionalRules`로 더해 주지 않으면
  에이전트가 노드에서 읽는 버전(kubelet, 컨테이너 런타임)을 잃습니다.

도구 서버는 공유되므로 이 설정은 도구 서버를 쓰는 모든 에이전트에 적용됩니다.

### 확인

어느 계열이든 kagent UI에 `ratatosk-agent`가 나타납니다. kagent 1.x에서는
`kubectl describe agents.api.kagent.dev ratatosk-agent -n kagent`로 준비
상태를 볼 수 있습니다(리소스 이름은 전체로 쓰세요. 남아 있는 0.x CRD가
`kubectl get agent`에 대신 답할 수 있습니다). 이렇게 물어보세요:

> "이 클러스터에서 업그레이드 전에 조치할 게 있나요?"

에이전트는 kagent 내장 읽기 전용 클러스터 도구로 실행 중인 버전을 스스로
알아냅니다(`kagent.agent.k8sTools=false`로 끌 수 있음). 질문에 버전을
직접 적어도 됩니다. 에이전트가 UI에 나타나지 않으면
[문제 해결](#문제-해결)을 보세요.

> **모델 최소 요건**(kagent 0.x에서 측정): 에이전트를 한 번 실행하면 내부
> 모델 호출이 6회 이상 발생하고, kagent Go ADK는 429(요청 한도 초과) 응답을
> 재시도하지 않습니다. 따라서 분당 약 10요청 이상을 허용하는 모델 티어가 필요하며,
> 그보다 낮으면 실행할 때마다 실패합니다. 예를 들어 Gemini 무료
> 티어(2026-07 기준)에서는 분당 5요청인 full-flash 계열로는 실행이 끝까지
> 가지 못하고, flash-lite 계열이라야 에이전트를 돌릴 수 있는 한도가
> 나옵니다.

## 설정 레퍼런스

서버 동작을 바꾸는 설정은 모두 환경 변수이고, Helm 차트는 각 변수에 대응하는
값을 제공합니다. 차트에는 이 밖에 레플리카 수·리소스·서비스 타입처럼 배포
형태를 정하는 값도 있습니다.
[차트 README](../charts/ratatosk-mcp/README.ko.md)를 보세요.

| 환경 변수 | 차트 값 | 기본값 | 설명 |
|---|---|---|---|
| `RATATOSK_URL` | `ratatoskUrl` | `https://ratatosk.io` | 업스트림 API(`/v2`, 공개, 읽기 전용). egress를 프록시·미러로 우회할 때 변경 |
| `MCP_HTTP_ADDR` | *(차트가 `service.port`로 자동 설정)* | *(없음 = stdio)* | 설정 시(예: `:8080`) stdio 대신 `/mcp` Streamable HTTP로 동작, `/healthz` 포함 |
| `MCP_HTTP_STATELESS` | `statelessHttp` | *(꺼짐)* | `1`이면 세션 상태 없는 HTTP: `Mcp-Session-Id` 왕복이 없고, MCP 스펙 2026-07-28 리비전을 쓰는 최신 클라이언트를 HTTP로 받으려면 필요합니다. 레플리카 2개 이상 수평 확장에도 권장. 구형 클라이언트는 어느 쪽이든 동작합니다 |
| `MCP_LOG` | `logLevel` | `info` | 허용값 `info`(기본)·`debug`·`warn`·`error`. 인식하지 못한 값은 경고 없이 `info`로 처리됩니다. `debug`는 업스트림 호출별 소요 시간을 추가하고, `warn`·`error`는 로그를 줄입니다. 어느 레벨에서도 요청 인자는 기록되지 않습니다. [로그와 감사 스트림](#로그와-감사-스트림) 참조 |
| `MCP_AUDIT` | `auditMode` | *(꺼짐)* | `metadata` 또는 `full`: 도구 호출 감사 스트림. [감사 스트림 켜기](#감사-스트림-켜기) 참조 |
| `MCP_RATE_LIMIT_PER_MIN` | `rateLimitPerMin` | *(꺼짐)* | 호출자당 분당 요청 상한(MCP 메시지를 모두 세고, 도구 호출 하나가 1회). 내가 통제하지 않는 호출자를 대신 받는 서버용입니다. 호출자 주소마다 몫을 따로 주므로 한 명이 바쁘다고 나머지가 굶지 않고, 넘기면 `Retry-After`가 붙은 `429`가 돌아옵니다. 주소는 창이 유지되는 동안의 메모리 버킷 키일 뿐이고 기록하지도, 상류로 보내지도 않습니다. `CF-Connecting-IP` → `X-Forwarded-For` → `X-Real-IP` → 피어 주소 순으로 읽으므로, 앞단 프록시가 그중 하나를 채우고 **클라이언트가 보낸 값은 지워야** 합니다. 호출자가 조작할 수 있는 값을 키로 쓰면 제한이 아니니까요. 단일 이용자 설치라면 꺼 두세요 |
| `MCP_MAX_INFLIGHT` | `maxInflight` | *(꺼짐)* | 모든 호출자를 합쳐 서버가 동시에 처리하는 요청 수의 상한. 넘은 요청은 빈자리를 최대 10초 기다린 뒤 `Retry-After`가 붙은 `503`을 받습니다. [메모리와 동시 처리](#메모리와-동시-처리) 참조 |
| `MCP_CACHE_MAX_MB` | `cacheMaxMB` | `32` | 응답 캐시 예산(MiB). 응답은 5분 동안 캐시됩니다 |

docker로 HTTP 모드를 직접 띄우는 예:

```bash
docker run --rm -p 8080:8080 -e MCP_HTTP_ADDR=:8080 ghcr.io/basphere/ratatosk-mcp:1.0.0
```

### 메모리와 동시 처리

먼저 바닥나는 것은 메모리입니다. `check_stack` 호출이 도는 동안 서버는 그 스택을 위해 읽은 릴리스 문서를
메모리에 들고 있습니다. 구성 요소 10개로 잰 값은 `detail`을 기본값 `brief`로 두면 약 30 MiB,
`detail=full`이면 약 50 MiB이고, 동시에 도는 `full` 호출이 하나 늘 때마다 약 20 MiB씩 더 듭니다.
여기에 응답 캐시가 최대 `MCP_CACHE_MAX_MB`만큼 더해집니다.

차트 기본 상한 512Mi에서는 스택 전체를 `full`로 확인하는 호출을 동시에 16개 받아도(최고 376 MiB), 24개 받아도
(최고 495 MiB) 재시작이 없었습니다. 64Mi에서는 2개가 동시에 오자 컨테이너가 강제 종료됐습니다.
CPU는 버티느냐가 아니라 속도를 정합니다. 차트 기본값 100m에서 구성 요소 10개를 `full`로 한 번 확인하는 데
약 4초가 걸리고, 동시에 도는 호출은 이 CPU를 나눠 쓰므로 16개를 동시에 받았을 때는 46초가 걸렸습니다.

부하를 묶는 설정은 네 가지입니다.

| 설정 | 이럴 때 씁니다 |
|---|---|
| `resources.limits.memory` (차트) | 호출자가 스택 전체 확인을 병렬로 보내거나, 여유를 더 두고 싶을 때. 동시에 올 것으로 보는 `full` 호출 하나당 약 20 MiB를 잡으세요 |
| `MCP_MAX_INFLIGHT` | 메모리가 감당하는 것보다 많은 호출자가 한꺼번에 올 수 있을 때. 상한을 넘은 요청은 기다렸다가 `Retry-After`가 붙은 `503`을 받습니다. 컨테이너가 강제 종료돼 처리 중이던 응답이 모두 사라지는 것보다 낫습니다. 메모리 상한이 버티는 `full` 호출 수보다 작게 두세요 |
| `MCP_RATE_LIMIT_PER_MIN` | 내가 통제하지 않는 호출자를 대신 받는 서버일 때. 이 설정은 호출자 하나하나를, `MCP_MAX_INFLIGHT`는 호출자 전체를 묶습니다 |
| `MCP_CACHE_MAX_MB` | 메모리가 빠듯할 때. 캐시가 작으면 같은 호출이 업스트림으로 더 자주 갑니다 |

단일 이용자 설치를 차트 기본값으로 쓴다면 어느 것도 필요 없습니다.

## 로그와 감사 스트림

### 운영 로그

로그는 stderr에 JSON 한 줄씩 남습니다(stdout은 stdio 전송 전용). 기본
레벨(`info`)에서 남는 것은 수명주기 줄(시작 시 `listening` 한 줄, stdio
세션 종료 한 줄)과, 업스트림 연결에 문제가 생겼을 때 남는 경고·오류
줄뿐입니다. 정상 요청은 한 줄도 남기지 않습니다. 참고로 stdio 세션 종료
줄은 두 모양입니다: 클라이언트가 정리하고 끊으면
`"msg":"stdio session ended"`(INFO), 정리 없이 갑자기 끊으면
`"msg":"stdio session ended with error"`(ERROR)입니다. 후자도 장애가 아니라
종료 방식의 기록입니다. 오류가 나도 원래 오류 메시지를 그대로 옮기지 않습니다.
요청 URL에 실행 중인 버전이 들어 있기 때문입니다. 대신 엔드포인트 패턴과
오류 종류만으로 로그 문구를 새로 만들어 기록합니다:

```json
{"time":"…","level":"ERROR","msg":"upstream fetch failed","service":"mcp","upstream":"/v2/projects","kind":"connection_refused","tool":"check_stack"}
```

`MCP_LOG=debug`로 올리면 여기에 더해 업스트림 호출마다 엔드포인트 패턴·상태
코드·소요 시간이 한 줄씩 붙습니다. **어느 레벨에서도 요청 인자(버전 등)는
로그에 담기지 않습니다.**

### 감사 스트림 켜기

감사 스트림은 "어느 클라이언트가 어떤 도구를 불렀는가"를 남기는 별도
기록입니다. **기본은 꺼짐이고, 꺼진 동안에는 아무것도 기록되지 않습니다.**
스트림 자체가 존재하지 않습니다.

켜려면 `MCP_AUDIT`를 설정합니다:

```bash
# docker
docker run -i --rm -e MCP_AUDIT=metadata ghcr.io/basphere/ratatosk-mcp:1.0.0

# Helm
helm upgrade ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp --set auditMode=metadata
```

켜면 도구 호출마다 `event:"audit"` JSON 한 줄이 운영 로그와 같은
스트림(stderr)에 남습니다:

```json
{"argument_names":["components","detail"],"client_name":"my-agent","client_version":"1.0.0","event":"audit","level":"INFO","msg":"audit","outcome":"ok","service":"mcp","time":"2026-07-31T02:54:47.029169122Z","tool":"check_stack","transport":"stdio"}
```

서버가 상태 유지(stateful) HTTP 모드(차트 기본값)로 돌아갈 때는 HTTP 감사
레코드에 전송 세션 식별자인 `session_id` 필드가 함께
담깁니다(예: `"session_id":"T3E77BYZFDDA33SIUSORQ365ZL"`). 동시에 접속한
호출자들을 세션 단위로 구분하는 값입니다. `statelessHttp`(환경 변수
`MCP_HTTP_STATELESS`)를 켜면 이름 붙일 세션이 없으므로 `session_id`도
클라이언트가 보고한 `clientInfo`도 레코드에서 빠지고, 호출 단위를 이어
주는 키는 아래의 `trace_id`뿐입니다. 감사 스트림에서 호출자를 구분해야
한다면 상태 유지 모드로 돌리거나 호출자가 `traceparent`를 보내게 하세요.
위 예시 같은 stdio 레코드에도 `session_id`는 없습니다. 프로세스 하나에
호출자가 하나뿐이라 구분할 것이 없기 때문입니다.

두 모드의 차이:

- **`metadata`**: 도구 이름, 결과(`ok`·`error`·`tool_error`), 전송 방식,
  클라이언트가 보고한 `clientInfo`(stdio·상태 유지 HTTP에서), 그리고
  인자의 **이름**만. 인자 값은 이 모드에서 절대 기록되지 않습니다.
- **`full`**: 위에 더해 인자 값 전체(`arguments` 필드). `check_stack`에
  넘긴 버전 목록도 여기 담기므로, 그 값이 로그 시스템에 남아도 되는지
  판단한 뒤 켜세요.

이 스트림은 사용자 인프라 안에서 만들어져 자체 로그 콜렉터에 쌓입니다.
보존 기간과 위변조 방지는 로그 플랫폼에서 정하면 되고, `event` 필드가
`audit`인 레코드를 별도 싱크로 라우팅하면 운영 로그와 다른 보존 정책을
적용할 수 있습니다.

한계도 알아두세요. 이 서버에는 인증이 없으므로, 기록에 남는 "호출자"는
클라이언트가 스스로 보고한 `clientInfo`와 전송 계층이 드러내는 정보가
전부입니다. 어떤 사람이 에이전트에게 그 호출을 시켰는지는 에이전트 계층의
기록에서 찾아야 합니다.

호스팅 엔드포인트에는 감사 스트림이 없습니다. 요청 내용을 보관하지
않는다는 운영 원칙에 따라 항상 꺼져 있습니다. 감사 요건이 있다면 설치형을
쓰세요.

### 트레이스 연계 (traceparent)

에이전트 프레임워크가 W3C trace context(분산 트레이싱 표준의 요청 식별
헤더)를 지원하는 경우, 도구 호출의 `_meta`에 `traceparent`를 실어 보낼 수
있습니다:

```json
{"method":"tools/call","params":{"name":"check_stack","arguments":{"components":[…]},"_meta":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}}
```

그러면 그 호출이 남기는 로그 줄(오류·`debug` 레벨)과 감사 레코드에
`trace_id` 필드가 찍히고, 업스트림
`/v2` 요청에도 같은 `traceparent` 헤더가 전달되어 에이전트 → MCP 서버 →
업스트림까지 하나의 `trace_id`로 이을 수 있습니다. 감사 레코드 혼자서는
잇지 못하는 계층을 이어 주는 조인 키가 바로 이것입니다. 에이전트 플랫폼의
프롬프트와 이곳의 도구 호출을 연결해 줍니다. 형식이 잘못된 값은 버려지고
전달되지 않으며, 보내지 않으면 아무것도 기록되지 않습니다.

## 문제 해결

| 증상 | 확인 | 원인과 해결 |
|---|---|---|
| 도구 호출이 오류를 반환하고(`check_stack`은 "could not read the project list: …", 일부 요청만 실패하면 컴포넌트의 `note`에 `fetch failed: …`), 로그에 `"msg":"upstream fetch failed"`와 `kind: connection_refused`·`dns`·`timeout` | `kubectl logs deploy/ratatosk-mcp` (로컬은 클라이언트의 MCP 로그) | egress 차단. `ratatosk.io:443` 아웃바운드 HTTPS를 허용하거나, 미러를 두고 `RATATOSK_URL`(차트: `ratatoskUrl`)로 지정 |
| 로그에 `"msg":"upstream rate limited"`, `status: 429` | 위와 동일 | 업스트림 한도(분당 1200요청/IP) 초과. 프로젝트별로 나눠 부르는 대신 `check_stack` 하나로 묶어 호출하고, 잠시 후 재시도 |
| HTTP 클라이언트가 `400 Bad Request: protocol version "2026-07-28" is only supported on stateless HTTP servers`를 받음 | 클라이언트가 보내는 `Mcp-Protocol-Version` 헤더 | 클라이언트가 MCP 2026-07-28 리비전을 쓰는데 상태 유지 HTTP 모드는 이를 거절합니다. `--set statelessHttp=true`(환경 변수: `MCP_HTTP_STATELESS=1`)를 켜세요. 오류 메시지의 `StreamableHTTPOptions.Stateless`는 같은 스위치의 Go SDK 쪽 이름입니다 |
| kagent UI에 `ratatosk-agent`가 나타나지 않음 | `kubectl api-resources \| grep kagent` · `kubectl get pods -n kagent` | kagent CRD가 없는 클러스터에서는 `kagent.enabled=true` 설치가 실패합니다. kagent를 먼저 설치하세요. kagent 1.x에서 `kagent.harness`가 비어 있으면 차트가 에이전트를 건너뛰고(설치 후 안내에 나옵니다), 지정한 Harness가 없으면 `kubectl describe agents.api.kagent.dev ratatosk-agent`에 나타납니다. 0.x 매니페스트를 kagent 1.x에 적용하거나 그 반대로 하면 "no matches for kind"로 실패하니 계열에 맞는 묶음을 쓰세요. 매니페스트 경로는 `namespace: kagent`가 하드코딩되어 있으므로 kagent를 다른 네임스페이스에 설치했다면 매니페스트를 수정해야 합니다 |
| kagent 1.x: 에이전트의 ratatosk 도구 호출이 연결에 실패 | `kubectl get remotemcpservers.api.kagent.dev ratatosk-mcp -n kagent -o jsonpath='{.spec.url}'` | URL이 짧은 Service 이름입니다. kagent 1.x 에이전트는 전체 이름만 풉니다. `http://ratatosk-mcp.<namespace>.svc.cluster.local:8080/mcp`를 쓰세요(차트와 1.x 매니페스트는 이미 이렇게 합니다) |
| kagent 0.x 전용: kagent에서 에이전트 런타임을 Go ADK로 바꾸면 `ImagePullBackOff` | `kubectl describe pod` | 이 차트와 무관한 kagent 0.9.12 자체의 알려진 문제([#2247], 0.9.12 이후 수정): Go ADK 이미지는 `ghcr.io`에만 발행되는데 컨트롤러 기본값이 폐기된 `cr.kagent.dev`를 가리킵니다. 우회: kagent 설치에 `--set controller.agentImage.registry=ghcr.io` |
| 파드가 재시작되고 `kubectl describe pod`에 `Last State: Terminated, Reason: OOMKilled`가 보임 | `kubectl describe pod -l app.kubernetes.io/name=ratatosk-mcp` | 동시에 도는 스택 전체 확인이 메모리 상한을 넘었습니다. `resources.limits.memory`를 올리거나, `maxInflight`를 설정해 넘치는 호출이 기다리게 하세요. [메모리와 동시 처리](#메모리와-동시-처리) 참조 |
| kagent 에이전트가 실행할 때마다 실패 | 에이전트 로그의 429 | 모델 티어의 요청 한도 부족. [kagent 연동](#kagent-연동)의 모델 최소 요건 참조 |

[#2247]: https://github.com/kagent-dev/kagent/issues/2247

## 버전 고정

- **컨테이너 이미지**: 릴리스마다 버전 태그(예: `1.0.0`)로 자동 빌드되며
  멀티 아치(`linux/amd64`, `linux/arm64`)입니다. `latest`는 최신 릴리스를
  따라갑니다. 재현 가능한 배포에는 버전 태그를 고정하세요.
- **Helm**: 차트는 차트 저장소로 발행되지 않고 저장소 클론으로만
  설치합니다. 특정 버전에 고정하려면 클론 후 릴리스 태그를 체크아웃하세요:

  ```bash
  git -C ratatosk-mcp checkout v1.0.0
  ```

  이미지 태그는 기본적으로 차트의 `appVersion`을 따르고, `image.tag` 값으로
  덮어쓸 수 있습니다.

## 업스트림 API

이 서버는 공개 REST API의 얇은 클라이언트입니다. 직접 호출하고 싶다면
ratatosk.io의 `GET /v2`를 열어 보세요. 인덱스가 스스로를 설명합니다. API
키는 없고, IP당 분당 1200회 제한이 있습니다.

## 다음 단계

- 도구 6개의 파라미터·예시 호출·실제 응답: [도구 레퍼런스](tools.ko.md)
- 한눈에 보는 도구 표: [README의 도구 표](../README.ko.md#도구)
- 차트 값 전체와 kagent 토글: [차트 README](../charts/ratatosk-mcp/README.ko.md)
- kagent 매니페스트와 예제 에이전트: [kagent 예제](../examples/kagent/README.ko.md)
