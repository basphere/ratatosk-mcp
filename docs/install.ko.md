# 설치와 사용법

[English](install.en.md) · [한국어](install.ko.md) · [日本語](install.ja.md)

이 문서는 Ratatosk가 정리한 릴리스 변경 내역을 AI 에이전트에 연결하는 네 가지 방법(호스팅 엔드포인트 연결, 로컬 stdio 서버 실행, 쿠버네티스 클러스터 Helm 배포, kagent 연동)을 설명합니다. 클라이언트별 설정 방법은 [클라이언트별 등록 방법](#클라이언트별-등록-방법)과 [기타 stdio 클라이언트](#기타-stdio-클라이언트)에서 확인할 수 있습니다.

**MCP(Model Context Protocol)**는 AI 에이전트가 외부 도구를 호출할 때 사용하는 표준 프로토콜입니다. ratatosk-mcp는 MCP를 통해 도구 6개를 제공합니다. `check_stack`은 실행 중인 버전 목록을 서버 프로세스 안에서 비교해 업그레이드 경로별 업그레이드 전 확인, 결정이 필요한 변경, 버전 번호가 건너뛴 구간을 반환합니다. 나머지 도구는 한 프로젝트의 업그레이드를 계산하는 `check_upgrade`, 릴리스 전체 내용을 가져오는 `get_release`, 최근 릴리스를 요약하는 `list_releases`, 보안 공지 번호나 플래그, 검색어로 찾는 `search`, 추적 중인 프로젝트와 슬러그를 보여 주는 `list_projects`입니다. Claude, ChatGPT, Grok, Claude Code, Codex, Gemini CLI, Antigravity, kagent를 비롯해 직접 만든 SDK 에이전트 등 MCP 클라이언트라면 모두 연결할 수 있습니다.

## 실행 방식 선택

| 사용 환경 | 실행 방식 | 안내 절 |
| --- | --- | --- |
| 별도 설치 없이 바로 사용 | 호스팅 엔드포인트 URL 등록 | [호스팅](#호스팅-엔드포인트) |
| 로컬 PC에서 Claude Code, Claude Desktop, Codex, Gemini CLI 등 stdio 클라이언트 사용 | `docker run` (stdio) | [로컬](#로컬-stdio) |
| 쿠버네티스 클러스터에서 자체 에이전트, CI 잡, SDK 클라이언트 운용 | Helm 차트 배포 | [클러스터 내 단독 배포](#클러스터-내-단독-배포-helm) |
| 쿠버네티스에서 [kagent](https://kagent.dev)(0.x 또는 1.x) 운용 | `kagent.enabled=true` Helm 옵션 또는 매니페스트 배포 | [kagent 연동](#kagent-연동) |

네 가지 방식 모두 계정이나 API 키 없이 동일한 도구 6개와 공개 데이터를 제공하며, 실행 방식에 따라 아래 세 가지가 달라집니다.

- **실행 중인 버전의 전달 범위**: 설치형에서 `check_stack`을 호출하면 사용자 환경의 프로세스 안에서 버전 비교가 끝나므로 실행 중인 버전이 인프라 밖으로 나가지 않습니다. 호스팅에서는 버전이 서버 메모리를 거쳐 처리되지만 어떤 로그에도 기록되지 않습니다.
- **적용되는 요청 한도**: 호스팅은 호출자마다 분당 60회의 MCP 요청을 허용하며, 서버가 업스트림 API를 호출하는 한도는 호스팅 이용자 전체가 공유합니다. 설치형은 실행 환경의 공인 IP를 기준으로 업스트림 API 한도(IP당 분당 1200회)를 적용받습니다.
- **감사 스트림 기록 여부**: 도구 호출 내역을 남기는 감사 스트림은 설치형에서만 사용할 수 있습니다.

다만 `get_release`처럼 특정 버전을 인자로 받는 도구는 실행 방식과 관계없이 조회 대상을 지목하기 위해 해당 버전을 업스트림 요청 경로에 담아 보냅니다. 이 경우에도 서버는 접근 로그를 기록하기 전에 쿼리 문자열을 모두 지우고 `/v2/releases/…`와 `/v2/upgrade/…` 경로의 앞부분만 남기므로 서버 로그에 버전이 기록되지 않습니다.

## 사전 준비

- **호스팅**: 원격 MCP 서버(Streamable HTTP 전송 방식)를 지원하는 클라이언트만 있으면 별도로 준비할 것은 없습니다.
- **로컬(stdio)**: Docker가 필요하며, 소스 코드에서 직접 빌드하려면 Go 1.26 이상이 필요합니다.
- **문서 예시의 `claude mcp add` 명령**: [Claude Code](https://claude.com/claude-code) CLI(`claude` 명령)가 설치되어 있어야 하며, 다른 MCP 클라이언트를 사용한다면 해당 클라이언트의 등록 방법을 따르면 됩니다.
- **클러스터(Helm)**: 쿠버네티스 클러스터와 Helm 3, 그리고 이 저장소를 클론한 사본이 필요합니다. 차트는 별도 차트 저장소나 OCI 레지스트리에 발행하지 않고 이 저장소 안에서만 제공합니다.
- **아웃바운드 HTTPS(egress)**: 설치형은 서버 프로세스가 외부의 `ratatosk.io:443`으로 나가는 HTTPS 연결을 열 수 있어야 하므로, NetworkPolicy나 egress 프록시로 아웃바운드 통신을 제한하는 클러스터에서는 이 목적지를 허용 목록에 추가합니다. 내부 프록시나 미러를 경유해야 하는 환경에서는 `RATATOSK_URL`(차트 값 `ratatoskUrl`)로 업스트림 주소를 바꿀 수 있습니다. 연결이 완전히 차단된 상태로 실행하면 도구가 오류(`check_stack`의 경우 `"could not read the project list"`)를 반환하고, 일부 요청만 실패하면 조회된 결과와 함께 실패한 컴포넌트의 `note` 필드에 `fetch failed: …`를 담아 반환합니다([문제 해결](#문제-해결) 참고).
- **kagent 연동**: kagent와 관련 CRD가 이미 설치된 클러스터가 필요하며, kagent 1.x에서 예제 에이전트를 실행하려면 Harness도 함께 준비되어 있어야 합니다([kagent 1.x](#kagent-1x) 참고).

## 호스팅 엔드포인트

`https://ratatosk.io/mcp`는 별도 설치 없이 바로 연결할 수 있는 원격 MCP 엔드포인트입니다. 세션을 유지하지 않는 무상태(stateless) Streamable HTTP 방식으로 동작하므로, `Mcp-Session-Id` 헤더를 주고받지 않고 요청마다 독립적으로 처리됩니다.

### 클라이언트별 등록 방법

모든 클라이언트에서 동일한 엔드포인트 주소를 사용하며, 클라이언트별 설정 위치는 아래와 같습니다.

| 클라이언트 | `https://ratatosk.io/mcp` 등록 방법 |
|---|---|
| Claude(웹 · 데스크톱) | Customize → Connectors → + Add → Add custom connector에서 등록. Team · Enterprise 플랜은 조직 소유자가 Organization settings → Connectors에 추가한 뒤 구성원이 Customize → Connectors에서 연결 ([공식 설명서](https://support.claude.com/en/articles/11175166-getting-started-with-custom-connectors-using-remote-mcp)) |
| ChatGPT(웹) | chatgpt.com/plugins에서 + → Add custom MCP server를 선택한 뒤 서버 URL에 주소를 입력합니다. 인증은 사용하지 않습니다 ([공식 설명서](https://developers.openai.com/api/docs/guides/custom-mcp-server)) |
| Grok | grok.com/connectors에서 New Connector → Custom 선택 후 주소 입력 ([공식 설명서](https://docs.x.ai/grok/connectors)) |
| Claude Code | `claude mcp add --transport http ratatosk https://ratatosk.io/mcp` ([공식 설명서](https://code.claude.com/docs/en/mcp)) |
| Grok Build | `grok mcp add --transport http ratatosk https://ratatosk.io/mcp` ([공식 설명서](https://docs.x.ai/build/features/mcp-servers)) |
| Codex | 설정 파일 `~/.codex/config.toml`의 `[mcp_servers.ratatosk]` 테이블에 `url = "https://ratatosk.io/mcp"` 추가 ([공식 설명서](https://learn.chatgpt.com/docs/extend/mcp)) |
| Gemini CLI | 설정 파일 `~/.gemini/settings.json`의 `mcpServers.ratatosk` 아래에 `"httpUrl": "https://ratatosk.io/mcp"`를 입력 ([공식 설명서](https://geminicli.com/docs/tools/mcp-server/)) |
| Antigravity | 설정 파일 `~/.gemini/config/mcp_config.json`의 `mcpServers.ratatosk` 아래에 `"serverUrl": "https://ratatosk.io/mcp"` 추가 ([공식 설명서](https://antigravity.google/docs/mcp)) |
| kagent | 클러스터에 `RemoteMCPServer` 리소스로 등록 ([kagent 연동](#kagent-연동) 참고) |

Gemini CLI와 Antigravity는 모두 `~/.gemini` 아래의 설정 파일을 읽지만, 주소를 적는 필드 이름이 Gemini CLI는 `httpUrl`, Antigravity는 `serverUrl`로 다릅니다. Claude Code에서는 아래 명령으로 등록합니다.

```bash
claude mcp add --transport http ratatosk https://ratatosk.io/mcp
```

### 연결 확인

MCP 클라이언트 없이 엔드포인트 동작 여부만 바로 확인하려면 아래 `curl` 명령을 실행합니다.

```bash
curl -s -X POST https://ratatosk.io/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1.0"}}}'
```

응답은 SSE(Server-Sent Events) 형식으로 반환되므로 `Content-Type`이 `text/event-stream`으로 표시되는 것이 정상이며, `data:` 줄의 JSON에 `"serverInfo"`와 `"name":"ratatosk"`가 포함되어 있으면 정상 동작하는 상태입니다.

```
event: message
data: {"jsonrpc":"2.0","id":1,"result":{…,"serverInfo":{"name":"ratatosk","version":"1.0.0"}}}
```

Claude Code에서는 `claude mcp list` 명령의 출력 결과로 연결 상태를 확인합니다.

```
ratatosk: https://ratatosk.io/mcp (HTTP) - ✔ Connected
```

### 동작 특성과 요청 한도

- **SSE GET 요청에 대한 405 응답**: 무상태 모드는 서버가 먼저 여는 GET 방식의 SSE 알림 스트림을 제공하지 않으므로 `405` 응답이 반환되는 것이 정상이며, 브라우저 주소창에서 직접 열면 안내 페이지(`/docs/mcp`)로 리다이렉트됩니다.
- **요청 한도**: 호스팅 엔드포인트는 호출자마다 분당 요청 60회를 허용합니다. 호출자 주소별로 한도를 따로 적용하므로 한 호출자의 요청이 많아도 다른 호출자의 한도에는 영향을 주지 않습니다. MCP 메시지는 모두 횟수에 포함됩니다. 클라이언트는 연결할 때 `initialize`, `notifications/initialized`, `tools/list` 등으로 두세 번 요청하고, 이후에는 도구를 호출할 때마다 한 번 요청합니다. 한도를 넘으면 `Retry-After`가 포함된 `429` 응답을 받습니다. 도구 호출 한 번이 업스트림 API 요청 여러 번으로 이어질 수 있습니다. 예를 들어 `check_stack`은 컴포넌트마다 해당 프로젝트의 최근 릴리스를 모두 조회하지만, 이 요청은 서버가 처리하므로 호출자의 60회 한도에는 포함되지 않습니다. `/v2`를 직접 호출하면 자기 IP에 분당 1200회 한도가 적용됩니다. 폴링 등 요청량이 많은 용도에는 설치형을 사용합니다.

### 호스팅 로그 정책과 버전 보호

호스팅 엔드포인트에서는 `check_stack`에 전달한 실행 중인 버전 목록이 서버 메모리를 거쳐 처리되지만, 요청 내용은 어디에도 기록되지 않습니다.

- MCP 서버 로그에는 서버 시작 줄과 업스트림 오류 줄만 남고 정상 요청은 기록되지 않으며, 오류 줄에도 요청 인자 값은 포함되지 않습니다.
- MCP 서버는 요청 수를 호출 방식, 도구 이름, 결과, 그리고 운영자 자신의 호출인지를 나타내는 한 비트로만 셉니다. 도구별로 호출에 걸린 시간, 응답 크기, 업스트림에 보낸 요청 수도 합계로만 남깁니다([지표](#지표) 참고). 인자, 주소, 세션은 세지 않습니다.
- 업스트림 API 요청 로그는 호출자가 `traceparent`를 전달한 경우에만 한 줄을 남기며, 경로와 쿼리 없이 정규화된 엔드포인트 라벨과 `trace_id`만 기록합니다.
- 서버 앞단의 접근 로그에서는 쿼리 문자열을 제거합니다. 슬러그나 버전이 경로에 들어가는 `/v2/releases/…`·`/v2/upgrade/…` 경로는 앞부분만 남기고 IP는 가립니다. 요청 본문은 기록하지 않습니다. 접근 로그는 서버 안에서 7일 이내에 순환 삭제되며, 사본은 운영자의 중앙 로그 저장소에 전송되어 그곳에서도 7일간 보관됩니다.
- 도구 호출 내역을 남기는 감사 스트림은 호스팅 환경에서 비활성화되어 있으며 앞으로도 켜지 않습니다.

다만 Ratatosk가 통제할 수 없는 구간이 하나 있습니다. ratatosk.io 앞단에는 Cloudflare가 있고 TLS 암호화를 해제했다가 다시 암호화해 중계하므로, 호스팅 `check_stack` 호출에 담긴 버전을 포함해 요청 내용 전체가 전송 중에 Cloudflare를 지나며 그 처리는 Cloudflare의 정책을 따릅니다. 보안 요건상 버전 정보가 이 구간을 지나면 안 되는 환경에서는 `check_stack`에 넘긴 버전이 로컬 프로세스 밖으로 나가지 않는 설치형을 사용합니다.

## 로컬 (stdio)

stdio는 MCP 클라이언트가 서버를 자식 프로세스로 직접 띄우고 표준 입출력으로 통신하는 방식이며, 로컬 PC에서 Claude Code, Claude Desktop, Codex, Gemini CLI, Antigravity를 사용할 때의 기본 연결 경로입니다.

### Claude Code

```bash
claude mcp add ratatosk -- docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0
```

이미지 태그 자리에는 `1.0.0` 대신 원하는 [릴리스 태그](https://github.com/basphere/ratatosk-mcp/releases)를 지정할 수 있으며, 항상 최신 릴리스를 따라가려면 `latest`를 사용합니다([버전 고정](#버전-고정) 참고).

### Claude Desktop

Claude Desktop은 CLI 대신 설정 파일(`claude_desktop_config.json`)로 MCP 서버를 등록하므로, `mcpServers`에 아래 설정을 추가합니다.

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

설정 파일 위치는 운영체제마다 다르므로 Claude Desktop 공식 문서를 참고하며, 컨테이너 설치 없이 사용하려면 위 [호스팅 엔드포인트](#호스팅-엔드포인트)를 원격 커넥터로 등록할 수도 있습니다.

### 기타 stdio 클라이언트

아래 클라이언트들은 모두 동일한 `docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0` 명령을 실행하며, 설정을 등록하는 위치만 다릅니다.

| 클라이언트 | 등록 위치 및 설정 방법 |
|---|---|
| ChatGPT 데스크톱 앱 | 설정 → MCP servers → Add server에서 STDIO를 선택하고 명령 입력 (Codex와 `~/.codex/config.toml` 설정 공유, [공식 설명서](https://learn.chatgpt.com/docs/extend/mcp)) |
| Codex | 설정 파일 `~/.codex/config.toml`의 `[mcp_servers.ratatosk]`에 `command = "docker"`, `args = ["run", "--rm", "-i", "ghcr.io/basphere/ratatosk-mcp:1.0.0"]`, `startup_timeout_sec = 60`(첫 실행 시 이미지 다운로드 대기 시간) 추가 |
| Gemini CLI | 설정 파일 `~/.gemini/settings.json`에 위 Claude Desktop과 동일한 `mcpServers` 블록 추가 |
| Antigravity | 설정 파일 `~/.gemini/config/mcp_config.json`에 위 Claude Desktop과 동일한 `mcpServers` 블록 추가 |
| Grok Build | `grok mcp add ratatosk -- docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0` |

ChatGPT 웹 버전과 grok.com은 로컬 컴퓨터의 명령을 실행할 수 없고 원격 URL로만 연결하므로 호스팅 엔드포인트를 사용합니다.

### 소스 빌드

Docker 없이 바이너리를 직접 빌드해 사용하려면 아래 명령을 실행합니다.

```bash
go build -o ratatosk-mcp .
claude mcp add ratatosk -- /path/to/ratatosk-mcp
```

stdio를 지원하는 다른 MCP 클라이언트에서도 동일하게 빌드한 바이너리 경로를 등록해 사용합니다.

### 연결 확인

```bash
claude mcp list
# ratatosk: docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0 - ✔ Connected
```

연결 상태가 확인되면 에이전트에게 아래와 같이 질문해 동작을 시험합니다.

> "envoy v1.36.8과 istio 1.30.1을 쓰는데, 업그레이드 전에 조치할 게
> 있어?"

에이전트가 `check_stack`을 호출해 실행 중인 버전 이후의 보안 변경과 호환성 변경(기능 제거, 기본값 변경 등)을 원문 근거와 함께 알려 주면 정상적으로 연결된 것입니다.

## 클러스터 내 단독 배포 (Helm)

`MCP_HTTP_ADDR` 환경 변수를 설정하면 동일한 바이너리가 `/mcp` 경로에서 Streamable HTTP 방식으로 MCP 서버를 제공하며 `/healthz` 헬스 체크도 함께 지원합니다. Helm 차트는 이를 ClusterIP Service로 배포하고 `service.port`(기본값 8080)에 맞춰 `MCP_HTTP_ADDR`를 자동으로 설정하므로, 클러스터 안의 자체 제작 SDK 에이전트나 다른 에이전트 프레임워크, 업그레이드 전에 `check_stack`으로 변경 사항을 확인하는 CI 잡에서 별도 프로세스를 띄우지 않고 내부 URL로 바로 접속할 수 있습니다.

### 설치

```bash
git clone https://github.com/basphere/ratatosk-mcp
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp
```

별도의 RBAC이나 시크릿이 필요 없으며, Pod Security Standards(PSS)의 `restricted` 프로파일에서도 경고 없이 동작합니다. 전체 차트 설정값은 [차트 README](../charts/ratatosk-mcp/README.ko.md)에서 확인할 수 있습니다. 다만 MCP 스펙 `2026-07-28` 리비전을 쓰는 최신 클라이언트(`Mcp-Protocol-Version: 2026-07-28` 헤더를 보내는 클라이언트)를 HTTP로 받거나 레플리카를 2개 이상으로 늘릴 때는 `--set statelessHttp=true` 옵션을 켭니다([설정 레퍼런스](#설정-레퍼런스) 참고).

### 배포 확인

아래 명령으로 파드가 정상 실행 중인지 확인합니다.

```bash
kubectl get pods -l app.kubernetes.io/name=ratatosk-mcp
# NAME                            READY   STATUS    RESTARTS   AGE
# ratatosk-mcp-6f7b9c8d4-x2m5q    1/1     Running   0          30s
```

컨테이너 시작 로그 한 줄에 수신 주소와 업스트림 API 주소가 표시됩니다.

```bash
kubectl logs deploy/ratatosk-mcp
# {"time":"…","level":"INFO","msg":"listening","service":"mcp","transport":"http","addr":":8080/mcp","mode":"stateful","upstream":"https://ratatosk.io","version":"1.0.0","rate_limit":"off","max_inflight":"off"}
```

포트 포워딩을 열어 헬스 체크 엔드포인트까지 확인하려면 아래 명령을 실행합니다.

```bash
kubectl port-forward svc/ratatosk-mcp 8080:8080 &
sleep 2   # 포워드가 준비되기 전에 curl이 돌면 connection refused가 납니다
curl -i http://localhost:8080/healthz
# HTTP/1.1 200 OK
```

확인이 끝나면 백그라운드로 띄운 포트 포워딩 프로세스를 `kill %1` 명령으로 종료합니다.

### 클러스터 내부 접속

클러스터 안의 MCP 클라이언트는 아래 Service URL로 연결합니다.

```
http://ratatosk-mcp.<namespace>.svc:8080/mcp
```

### 업그레이드

```bash
git -C ratatosk-mcp pull
helm upgrade ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp
```

설치할 때 `--set`으로 설정값을 변경했다면 업그레이드할 때도 같은 값을 다시 지정하거나 `--reuse-values` 옵션을 함께 붙입니다.

## kagent 연동

kagent 1.x는 `api.kagent.dev/v1alpha3` API를 사용하고 0.x는 `kagent.dev/v1alpha2` API를 사용하며, 두 버전 계열은 서로 호환되지 않습니다. Helm 차트는 클러스터의 API 버전을 감지해 알맞은 리소스를 생성하고, 매니페스트는 버전 계열별로 따로 제공됩니다. 현재 클러스터에 설치된 kagent 계열은 아래 명령으로 확인합니다.

```bash
kubectl api-resources --api-group=api.kagent.dev   # agents가 나오면 kagent 1.x
kubectl api-resources --api-group=kagent.dev       # agents가 나오면 kagent 0.x
```

kagent 1.x가 정식 출시되면 kagent 0.x 지원은 제거합니다.

### kagent 0.x

아래 두 가지 설치 방법 중 하나를 선택하면 동일한 리소스가 배포됩니다.

**A. Helm 옵션 활성화**: 명령 한 번으로 MCP 서버, kagent 등록 리소스(`RemoteMCPServer`), 예제 에이전트(`ratatosk-agent`)를 함께 설치합니다.

```bash
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true
```

모델 설정(`kagent.modelConfig`)의 기본값은 `default-model-config`이며 클러스터 환경에 맞게 변경할 수 있습니다. 예제 에이전트는 빼고 서버 등록만 하려면 `kagent.agent.enabled=false`를 지정합니다.

**B. 매니페스트 적용**: Helm 없이 동일한 세 가지 리소스를 매니페스트로 바로 설치합니다([상세 안내](../examples/kagent/0.x/README.ko.md) 참고).

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml
kubectl apply -f $BASE/0.x/ratatosk-remote-mcpserver.yaml
kubectl apply -f $BASE/0.x/ratatosk-agent.yaml
```

매니페스트 방식은 저장소를 클론할 필요가 없으며, 기본 네임스페이스로 `namespace: kagent`가 지정되어 있습니다.

### kagent 1.x

**Harness 사전 확인**: kagent 1.x는 모든 Agent를 Harness(런타임, Agent Substrate WorkerPool, 스냅샷 위치) 위에서 실행하지만, kagent를 설치해도 기본 Harness가 자동으로 생성되지 않습니다. 아래 명령으로 설치할 네임스페이스에 Harness가 있는지 먼저 확인합니다.

```bash
kubectl get harnesses.api.kagent.dev -n kagent
```

Harness가 없다면 kagent 공식 문서의 [Agent harness](https://kagent.dev/docs/kagent/1.x/agents/agent-harness/) 안내를 따라 먼저 생성하며, 아래 클러스터 읽기 전용 설정 절에서 설명하는 이유에 따라 kagent 런타임(`spec.kagent: {}`)을 사용합니다.

**A. Helm 옵션 활성화**: 위에서 확인한 Harness 이름을 함께 지정해 설치합니다.

```bash
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true --set kagent.harness=<Harness 이름>
```

차트는 클러스터의 1.x API를 보고 1.x 객체를 만듭니다. `kagent.harness`가
없으면 서버와 RemoteMCPServer만 설치하고 에이전트는 건너뛰며, 설치 후
안내(NOTES)에 나중에 추가하는 방법(`helm upgrade --reuse-values --set
kagent.harness=<이름>`)이 나옵니다.

**B. 매니페스트 적용**: Helm 없이 매니페스트로 직접 설치합니다([상세 안내](../examples/kagent/1.x/README.ko.md) 참고).

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml
kubectl apply -f $BASE/1.x/ratatosk-remote-mcpserver.yaml
curl -fsSLO $BASE/1.x/ratatosk-agent.yaml
# spec.harnessRef.name(자리표시자 your-harness)을 내 Harness 이름으로 고친 뒤:
kubectl apply -f ratatosk-agent.yaml
```

**Service 전체 도메인 이름(FQDN) 사용**: kagent 1.x 에이전트는 egress 정책에 적힌 호스트 이름만 해석하는 샌드박스에서 실행되므로 짧은 Service 이름(`ratatosk-mcp`)은 해석하지 못하며, `RemoteMCPServer`가 `ratatosk-mcp.kagent.svc.cluster.local`을 가리켜야 합니다. Helm 차트는 릴리스 네임스페이스와 `clusterDomain`(기본값 `cluster.local`)을 조합해 이 이름을 자동으로 생성하며, 매니페스트를 사용할 때 클러스터 도메인이 기본값과 다르면 URL을 직접 수정합니다.

#### 클러스터 읽기 전용 권한 설정

kagent 도구 서버(`kagent-tool-server`)에는 `k8s_apply_manifest`, `k8s_delete_resource`, `k8s_scale` 같은 클러스터 쓰기 도구도 포함되어 있습니다. 예제 에이전트는 읽기 전용 도구인 `k8s_get_resources`와 `k8s_get_resource_yaml`만 지정하며, kagent 런타임은 이 허용 목록을 그대로 적용합니다. 반면 Claude Harness(`spec.claude`)는 도구 서버의 일부 도구만 선택적으로 내줄 수 없어 Agent 상태에 경고만 남긴 채 전체 도구를 에이전트에게 넘기므로, 프롬프트 지시문의 "클러스터를 바꾸지 말라"는 문장 하나에만 의존하게 됩니다. 따라서 쓰기 작업을 확실히 차단하려면 아래 두 가지 방법 중 하나를 적용합니다.

- 예제 에이전트를 kagent 런타임 Harness에서 실행합니다.
- kagent 설치 시 `--set 'kagent-tools.tools.args={--read-only}'`를 지정해 도구 서버가 쓰기 도구를 아예 등록하지 않도록 설정합니다. 참고로 `kagent-tools.rbac.readOnly=true` 옵션은 도구 서버의 `cluster-admin` 권한을 기본 읽기 전용 권한으로 바꾸지만, 이 권한에는 `nodes`와 커스텀 리소스가 빠져 있어 `kagent-tools.rbac.additionalRules`로 권한을 추가하지 않으면 에이전트가 노드에서 kubelet이나 컨테이너 런타임 버전을 읽지 못합니다.

도구 서버는 클러스터 안에서 공유되므로, 이 설정은 도구 서버를 사용하는 모든 에이전트에 함께 적용됩니다.

### 연동 확인

설치가 끝나면 어느 버전 계열이든 kagent UI에 `ratatosk-agent`가 나타납니다. kagent 1.x에서는 클러스터에 남아 있는 0.x CRD가 단축 이름(`kubectl get agent`) 요청에 대신 응답할 수 있으므로, 리소스 전체 이름을 쓴 `kubectl describe agents.api.kagent.dev ratatosk-agent -n kagent` 명령으로 준비 상태를 확인한 뒤 아래와 같이 질문합니다.

> "이 클러스터에서 업그레이드 전에 조치할 게 있나요?"

에이전트는 kagent에 내장된 읽기 전용 클러스터 도구로 실행 중인 버전을 스스로 확인하며(`kagent.agent.k8sTools=false`로 비활성화 가능), 질문에 버전을 직접 적어도 됩니다. UI에 에이전트가 나타나지 않으면 [문제 해결](#문제-해결)을 참고합니다.

> **모델 최소 요건**(kagent 0.x에서 측정): 에이전트를 한 번 실행하면 내부 모델 호출이 6회 이상 발생하는데, kagent Go ADK는 `429`(요청 한도 초과) 응답을 재시도하지 않습니다. 따라서 분당 약 10회 이상의 요청을 허용하는 모델 등급이 필요하며, 한도가 그보다 낮으면 실행할 때마다 도중에 실패합니다. 예를 들어 Gemini 무료 티어(2026-07 기준)에서는 분당 5회로 제한되는 full-flash 계열로는 실행이 끝까지 가지 못하고, flash-lite 계열을 써야 에이전트를 끝까지 실행할 수 있습니다.

## 설정 레퍼런스

서버 동작을 제어하는 설정은 모두 환경 변수이며, Helm 차트는 각 환경 변수에 대응하는 설정값을 제공합니다. 차트에는 이 밖에도 레플리카 수, 리소스 요청·상한, Service 타입처럼 배포 형태를 정하는 설정값이 있으며, 전체 목록은 [차트 README](../charts/ratatosk-mcp/README.ko.md)에서 확인할 수 있습니다.

| 환경 변수 | 차트 값 | 기본값 | 설명 |
|---|---|---|---|
| `RATATOSK_URL` | `ratatoskUrl` | `https://ratatosk.io` | 읽기 전용 공개 업스트림 API(`/v2`) 주소로, 아웃바운드 통신(egress)을 프록시나 미러로 우회할 때 변경 |
| `MCP_HTTP_ADDR` | *(차트가 `service.port`로 자동 설정)* | *(없음 = stdio)* | 설정 시(예: `:8080`) stdio 대신 `/mcp` 경로의 Streamable HTTP 서버로 동작하며 `/healthz` 헬스 체크 포함 |
| `MCP_HTTP_STATELESS` | `statelessHttp` | *(꺼짐)* | `1`이면 `Mcp-Session-Id` 왕복이 없는 무상태 HTTP 모드로 동작. MCP 스펙 `2026-07-28` 리비전을 쓰는 최신 클라이언트를 HTTP로 받거나 레플리카를 2개 이상으로 수평 확장할 때 활성화하며, 구형 클라이언트는 어느 모드에서든 동작함 |
| `MCP_LOG` | `logLevel` | `info` | 로그 출력 수준(`info` · `debug` · `warn` · `error`, 인식하지 못한 값은 경고 없이 `info`로 처리). `debug`는 업스트림 호출별 소요 시간을 추가하고 `warn` · `error`는 로그 양을 줄이며, 어느 수준에서도 요청 인자 값은 기록하지 않음([로그와 감사 스트림](#로그와-감사-스트림) 참고) |
| `MCP_AUDIT` | `auditMode` | *(꺼짐)* | 도구 호출 감사 스트림 모드(`metadata` 또는 `full`, [감사 스트림 설정](#감사-스트림-설정) 참고) |
| `MCP_RATE_LIMIT_PER_MIN` | `rateLimitPerMin` | *(꺼짐)* | 직접 통제하지 않는 외부 호출자의 요청을 받는 서버에서 적용하는 호출자당 분당 요청 상한(모든 MCP 메시지를 세며 도구 호출 하나가 1회, 단일 사용자 설치에서는 불필요). 호출자 주소별로 한도를 따로 집계하므로 특정 호출자가 요청을 많이 보내도 다른 호출자에게 영향이 없으며, 상한 초과 시 `Retry-After`가 붙은 `429`를 반환함. 주소는 윈도우 유지 시간 동안 메모리 버킷 키로만 쓰이고 기록하거나 업스트림으로 보내지 않으며, `CF-Connecting-IP` → `X-Forwarded-For` → `X-Real-IP` → 피어 주소 순으로 읽으므로 앞단 프록시가 그중 하나를 채우고 **클라이언트가 임의로 보낸 헤더 값은 반드시 제거**해야 함 |
| `MCP_MAX_INFLIGHT` | `maxInflight` | *(꺼짐)* | 모든 호출자를 합쳐 서버가 동시에 처리하는 요청 수의 상한. 상한을 넘은 요청은 최대 10초 동안 빈자리를 기다리고, 그 안에 자리가 나지 않으면 `Retry-After`가 붙은 `503`을 받음([메모리와 동시 처리](#메모리와-동시-처리) 참고) |
| `MCP_CACHE_MAX_MB` | `cacheMaxMB` | `32` | 응답 캐시 크기 상한(MiB)이며 응답은 5분 동안 캐시됨 |
| `MCP_METRICS_ADDR` | `metricsPort` | *(꺼짐)* | HTTP 모드에서만 동작. 설정 시(예: `:9090`) 그 주소의 `GET /metrics`에서 요청 수 등 지표를 제공하며, `/mcp`와 다른 별도 리스너를 씀([지표](#지표) 참고) |

Docker로 HTTP 모드를 직접 실행하는 예시는 다음과 같습니다.

```bash
docker run --rm -p 8080:8080 -e MCP_HTTP_ADDR=:8080 ghcr.io/basphere/ratatosk-mcp:1.0.0
```

### 메모리와 동시 처리

CPU보다 메모리가 먼저 한계에 도달합니다. `check_stack`을 실행하는 동안 서버는 조회한 릴리스 데이터를 메모리에 보관합니다. 구성 요소 10개인 스택에서 `detail` 기본값인 `brief`를 사용하면 메모리 사용량은 약 30 MiB, `detail=full`이면 약 50 MiB입니다. 동시에 실행하는 `full` 호출이 하나 늘 때마다 약 20 MiB가 추가됩니다. 여기에 응답 캐시가 최대 `MCP_CACHE_MAX_MB`만큼 더해집니다.

차트 기본 메모리 상한인 `512Mi`에서는 `detail=full`로 설정한 `check_stack` 호출을 동시에 16개(최대 376 MiB)와 24개(최대 495 MiB) 처리했을 때 모두 강제 종료(OOMKilled)되지 않았으나, `64Mi`에서는 `full` 호출 2개를 동시에 처리하자 메모리 상한을 넘어 컨테이너가 강제 종료(OOMKilled)되었습니다. CPU는 응답이 끝나는 속도를 결정하며, 차트 기본 CPU 값인 `100m`에서 컴포넌트 10개를 `full`로 한 번 확인하는 데 약 4초가 걸렸고 16개를 동시에 처리했을 때는 CPU를 나눠 써서 46초가 걸렸습니다.

부하를 제어하는 설정은 네 가지입니다.

| 설정 | 적용 상황 및 기준 |
|---|---|
| `resources.limits.memory` (차트) | 호출자가 `check_stack` 호출을 동시에 보내거나 메모리 여유를 더 확보할 때 사용하며, 예상되는 동시 `full` 호출 하나당 약 20 MiB를 추가 할당 |
| `MCP_MAX_INFLIGHT` | 메모리로 감당할 수 있는 수보다 많은 요청이 동시에 몰려 컨테이너가 강제 종료(OOMKilled)되고 처리 중인 요청이 모두 끊기는 상황을 방지할 때 사용. 메모리 상한 안에서 처리할 수 있는 동시 `full` 호출 수보다 작게 설정하며, 상한을 넘은 요청은 대기 후 `Retry-After`가 붙은 `503`을 받음 |
| `MCP_RATE_LIMIT_PER_MIN` | 직접 통제하지 않는 외부 호출자의 요청을 받는 서버일 때 사용하며, 이 설정은 호출자별 요청 수를 제한하고 `MCP_MAX_INFLIGHT`는 호출자 전체의 동시 요청 수를 제한함 |
| `MCP_CACHE_MAX_MB` | 메모리가 부족할 때 줄여서 사용하며, 캐시가 작아지면 동일한 요청이 업스트림에 더 자주 전달됨 |

혼자 사용하는 환경에 차트 기본값으로 배포했다면 위 설정은 건드리지 않아도 됩니다.

## 로그와 감사 스트림

### 운영 로그

운영 로그는 표준 오류(stderr)에 JSON 한 줄씩 남습니다(표준 출력인 stdout은 stdio 전송 전용). 기본 수준(`info`)에서는 수명주기 로그(시작 시 `listening` 한 줄, stdio 세션 종료 한 줄)와 업스트림 연결에 문제가 생겼을 때의 경고·오류 줄만 남고, 정상 요청은 기록하지 않습니다. stdio 세션 종료 로그는 클라이언트가 정상적으로 연결을 정리하고 끊으면 `"msg":"stdio session ended"`(INFO), 정리 없이 갑자기 끊으면 `"msg":"stdio session ended with error"`(ERROR)로 남는데, 두 경우 모두 장애가 아니라 종료 방식을 기록한 것입니다. 또한 업스트림 요청 URL에는 실행 중인 버전이 들어 있으므로, 통신 오류가 발생하더라도 원래 오류 메시지를 그대로 옮기지 않고 엔드포인트 패턴과 오류 종류만으로 로그 문구를 새로 만들어 기록합니다.

```json
{"time":"…","level":"ERROR","msg":"upstream fetch failed","service":"mcp","upstream":"/v2/projects","kind":"connection_refused","tool":"check_stack"}
```

`MCP_LOG=debug`로 높이면 여기에 더해 업스트림 호출마다 엔드포인트 패턴, 상태 코드, 소요 시간이 한 줄씩 추가되며, **어느 로그 수준에서도 버전 등의 요청 인자 값은 로그에 담기지 않습니다.**

### 감사 스트림 설정

감사 스트림은 어느 클라이언트가 어떤 도구를 호출했는지 남기는 별도 기록이며, **기본값은 꺼짐이어서 비활성화된 동안에는 어떠한 감사 기록도 생성되지 않습니다.**

감사 스트림을 켜려면 `MCP_AUDIT` 환경 변수를 설정합니다.

```bash
# docker
docker run -i --rm -e MCP_AUDIT=metadata ghcr.io/basphere/ratatosk-mcp:1.0.0

# Helm
helm upgrade ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp --set auditMode=metadata
```

감사 스트림을 켜면 도구 호출마다 `event:"audit"` JSON 한 줄이 운영 로그와 같은 stderr에 기록됩니다:

```json
{"argument_names":["components","detail"],"client_name":"my-agent","client_version":"1.0.0","event":"audit","level":"INFO","msg":"audit","outcome":"ok","service":"mcp","time":"2026-07-31T02:54:47.029169122Z","tool":"check_stack","transport":"stdio"}
```

서버가 차트 기본값인 상태 유지(stateful) HTTP 모드로 동작할 때는 동시에 접속한 호출자들을 세션 단위로 구분할 수 있도록 HTTP 감사 레코드에 전송 세션 식별자인 `session_id` 필드(예: `"session_id":"T3E77BYZFDDA33SIUSORQ365ZL"`)가 함께 담깁니다. 반면 `statelessHttp`(환경 변수 `MCP_HTTP_STATELESS`)를 켜면 유지되는 세션이 없으므로 `session_id`와 클라이언트가 보고한 `clientInfo`가 모두 빠지고, 아래에서 설명할 `trace_id`로만 호출 단위를 이을 수 있습니다. 감사 스트림에서 호출자를 구분해야 한다면 상태 유지 모드로 실행하거나 호출자가 `traceparent`를 보내도록 구성합니다. 한편 stdio 모드에서는 프로세스 하나에 호출자가 하나뿐이라 구분할 대상이 없으므로 `session_id`가 남지 않습니다.

두 모드의 기록 범위 차이는 다음과 같습니다.

- **`metadata`**: 도구 이름, 처리 결과(`ok` · `error` · `tool_error`), 전송 방식, 클라이언트가 보고한 `clientInfo`(stdio 및 상태 유지 HTTP 모드), 그리고 전달된 인자의 **이름**만 기록하며 인자 값은 절대 기록하지 않습니다.
- **`full`**: 위에 더해 인자 값 전체(`arguments` 필드). `check_stack`에
  넘긴 버전 목록도 여기 담기므로, 그 값이 로그 시스템에 남아도 되는지
  판단한 뒤 켜세요.

감사 스트림은 사용자 인프라 안에서 생성되어 자체 로그 수집기에 쌓이므로 보존 기간과 위변조 방지는 로그 플랫폼에서 정하면 되며, `event` 필드가 `audit`인 레코드를 별도 싱크로 라우팅하면 운영 로그와 다른 보존 정책을 적용할 수 있습니다.

다만 이 서버에는 별도 인증이 없으므로 감사 기록에 남는 호출자 정보는 클라이언트가 스스로 보고한 `clientInfo`와 전송 계층이 드러내는 정보가 전부이며, 어떤 사용자가 에이전트에게 해당 호출을 지시했는지는 상위 에이전트 계층의 기록에서 확인해야 합니다.

호스팅 엔드포인트에는 요청 내용을 보관하지 않는다는 운영 원칙에 따라 감사 스트림이 항상 꺼져 있으므로, 감사 기록이 필요하다면 설치형을 사용합니다.

### 트레이스 연계 (traceparent)

에이전트 프레임워크가 분산 추적 표준 헤더인 W3C Trace Context를 지원한다면, 도구 호출의 `_meta`에 아래와 같이 `traceparent`를 실어 보낼 수 있습니다.

```json
{"method":"tools/call","params":{"name":"check_stack","arguments":{"components":[…]},"_meta":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}}
```

유효한 `traceparent`를 보내면 해당 호출이 남기는 오류·`debug` 로그와 감사 레코드에 `trace_id` 필드가 찍히고, 업스트림 `/v2` 요청에도 같은 `traceparent` 헤더가 전달되어 에이전트 플랫폼의 프롬프트부터 MCP 서버의 도구 호출과 업스트림 요청까지 하나의 `trace_id`로 연결할 수 있습니다. 형식이 잘못된 값은 버려져 전달되지 않으며, 보내지 않으면 아무것도 기록되지 않습니다.

## 지표

지표는 기본적으로 꺼져 있습니다. HTTP 모드에서 `MCP_METRICS_ADDR`(차트 값 `metricsPort`)을 설정하면 그 주소의 `GET /metrics`에서 Prometheus 텍스트 형식으로 지표를 제공합니다. `/mcp`와 다른 별도 리스너이므로 MCP 포트를 프록시로 열어도 지표는 노출되지 않습니다. stdio에서는 이 변수를 무시하고 경고를 남깁니다.

```bash
helm upgrade ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp --set metricsPort=9090
```

| 지표 | 타입 | 라벨 | 설명 |
|---|---|---|---|
| `ratatosk_mcp_requests_total` | counter | `method`, `tool`, `outcome`, `self` | `/mcp`로 들어온 요청 수. POST 하나가 요청 하나 |
| `ratatosk_mcp_upstream_requests_total` | counter | `tool`, `outcome` | 업스트림 `/v2` API로 보낸 요청 수. 응답 캐시에서 꺼낸 답은 세지 않음 |
| `ratatosk_mcp_request_duration_seconds` | histogram | `tool` | 서버가 도구 호출 요청을 받아 답할 때까지 걸린 시간 |
| `ratatosk_mcp_response_bytes` | histogram | `tool` | 에이전트가 받는 도구 응답 글의 크기(바이트) |
| `ratatosk_mcp_build_info` | gauge | `version` | 서버 버전. 값은 항상 1 |

라벨 값은 모두 정해진 목록에서만 나옵니다.

- `method`: `initialize`, `tools/list`, `tools/call`, 그 밖의 메서드(알림, `ping` 등)는 `other`.
- `tool`: 도구 여섯 개의 이름. 호출자가 그 밖의 이름을 보내면 `unknown`, 메서드가 `tools/call`이 아니면 빈 값. 서버가 읽기 전에 거절한 요청(`rate_limited`, `busy`)은 본문의 처음 4 KiB로 이름을 붙입니다. 도구 이름이 그보다 뒤에 있으면 `unknown`이고, `100 Continue`를 기다리는 요청은 본문을 읽지 않습니다(`method`는 `other`).
- 요청의 `outcome`: `ok`, `error`(형식이 잘못되어 거절된 요청, 프로토콜 오류, 또는 모르는 슬러그나 업스트림 요청 실패처럼 `isError`로 표시된 도구 응답), `rate_limited`(`MCP_RATE_LIMIT_PER_MIN`에 걸린 `429`), `busy`(`MCP_MAX_INFLIGHT` 아래에서 10초 안에 빈자리가 나지 않았거나 호출자가 기다리다 끊은 요청).
- 업스트림 요청의 `outcome`: `ok`(`200`), `error`(그 밖의 상태 코드. 기록에 없는 버전의 `404` 포함), `timeout`, `transport`(응답 없음: 연결 거부, DNS, 연결 끊김), `canceled`(호출이 먼저 끝남).
- `self`: 호출자 주소가 `MCP_SELF_CALLERS`(쉼표로 구분, `.`으로 끝나는 항목은 앞부분 일치)에 있으면 `true`. 서버가 업스트림에 `X-Ratatosk-Caller`로 보내는 판정과 같습니다. 설정하지 않으면 모든 호출자가 `false`입니다.

호출자가 보낸 내용은 라벨이 되지 않습니다. 인자, 프로젝트 이름, 주소, User-Agent, 세션은 어디에도 없습니다. 지표 계열의 목록은 서버가 시작할 때 정해지고 모든 계열이 0에서 시작하므로, 재시작 시점부터 재는 구간에서도 첫 호출이 빠지지 않습니다.

## 문제 해결

| 증상 | 확인 방법 | 원인과 해결 방법 |
|---|---|---|
| 도구 호출이 오류를 반환하고(`check_stack` 전체 실패 시 `"could not read the project list: …"`, 일부 요청만 실패 시 컴포넌트 `note`에 `fetch failed: …`), 로그에 `"msg":"upstream fetch failed"`와 `kind: connection_refused` · `dns` · `timeout` 출력 | `kubectl logs deploy/ratatosk-mcp` (로컬은 클라이언트의 MCP 로그) | 아웃바운드 통신(egress) 차단. `ratatosk.io:443` 아웃바운드 HTTPS를 허용하거나, 미러를 두고 `RATATOSK_URL`(차트 값 `ratatoskUrl`)로 지정 |
| 로그에 `"msg":"upstream rate limited"`, `status: 429`가 표시됨 | 위와 동일 | 업스트림 한도인 IP당 분당 1200회를 초과했습니다. 프로젝트별 요청을 여러 번 보내는 대신 `check_stack` 한 번으로 묶어 호출하고 잠시 후 다시 시도합니다 |
| HTTP 클라이언트가 `400 Bad Request: protocol version "2026-07-28" is only supported on stateless HTTP servers` 수신 | 클라이언트가 보내는 `Mcp-Protocol-Version` 헤더 | 클라이언트가 MCP `2026-07-28` 리비전을 사용하는데 상태 유지 HTTP 모드가 이를 거부함. `--set statelessHttp=true`(환경 변수 `MCP_HTTP_STATELESS=1`)를 활성화(오류 메시지의 `StreamableHTTPOptions.Stateless`는 같은 스위치의 Go SDK 내부 이름) |
| kagent UI에 `ratatosk-agent`가 표시되지 않음 | `kubectl api-resources \| grep kagent` · `kubectl get pods -n kagent` | kagent CRD가 없는 클러스터에서는 `kagent.enabled=true` 설치가 실패합니다. 먼저 kagent를 설치합니다. kagent 1.x에서 `kagent.harness`가 비어 있으면 차트가 에이전트 생성을 생략하며, 이 내용은 설치 후 안내에 표시됩니다. 지정한 Harness가 없으면 `kubectl describe agents.api.kagent.dev ratatosk-agent`에서 확인할 수 있습니다. 0.x 매니페스트를 1.x에 적용하거나 그 반대로 적용하면 "no matches for kind" 오류가 발생하므로 클러스터에 맞는 매니페스트를 사용합니다. 매니페스트의 네임스페이스는 `kagent`로 지정되어 있으므로 kagent를 다른 네임스페이스에 설치했다면 매니페스트를 수정합니다 |
| kagent 1.x에서 에이전트의 ratatosk 도구 호출 연결 실패 | `kubectl get remotemcpservers.api.kagent.dev ratatosk-mcp -n kagent -o jsonpath='{.spec.url}'` | URL에 짧은 Service 이름이 지정된 상태. kagent 1.x 에이전트는 전체 도메인 이름만 해석하므로 차트 및 1.x 매니페스트 기본값과 같이 `http://ratatosk-mcp.<namespace>.svc.cluster.local:8080/mcp`를 지정 |
| kagent 0.x에서 에이전트 런타임을 Go ADK로 변경 시 `ImagePullBackOff` 발생 | `kubectl describe pod` | 차트와 무관한 kagent 0.9.12 자체의 알려진 문제([#2247], 0.9.12 이후 수정)로, Go ADK 이미지는 `ghcr.io`에만 발행되는데 컨트롤러 기본값이 폐기된 `cr.kagent.dev`를 가리켜 발생함. kagent 설치 시 `--set controller.agentImage.registry=ghcr.io`를 지정해 우회 |
| 파드가 재시작되고 `kubectl describe pod`에 `Last State: Terminated, Reason: OOMKilled` 표시 | `kubectl describe pod -l app.kubernetes.io/name=ratatosk-mcp` | 동시에 처리한 `check_stack` 호출이 메모리 상한을 초과함. `resources.limits.memory`를 늘리거나 `maxInflight`를 설정해 상한을 넘은 요청이 대기하도록 제한([메모리와 동시 처리](#메모리와-동시-처리) 참고) |
| kagent 에이전트가 실행할 때마다 실패 | 에이전트 로그의 `429` 응답 | 모델 등급의 분당 요청 한도 부족([kagent 연동](#kagent-연동)의 모델 최소 요건 참고) |

[#2247]: https://github.com/kagent-dev/kagent/issues/2247

## 버전 고정

- **컨테이너 이미지**: 릴리스마다 버전 태그(예: `1.0.0`)가 붙은 멀티 아키텍처(`linux/amd64`, `linux/arm64`) 이미지가 자동 빌드되며, `latest`는 최신 릴리스를 따라갑니다. 재현 가능한 배포 환경에서는 버전 태그를 고정합니다.
- **Helm 차트**: 별도 차트 저장소로 발행하지 않고 저장소를 클론해 설치하므로, 특정 버전에 고정하려면 클론 후 아래 명령으로 원하는 릴리스 태그를 체크아웃합니다.

  ```bash
  git -C ratatosk-mcp checkout v1.0.0
  ```

  이미지 태그는 기본적으로 차트의 `appVersion`을 따르며, `image.tag` 설정값으로 덮어쓸 수 있습니다.

## 업스트림 API

이 서버는 공개 REST API를 호출하는 얇은 클라이언트입니다. REST API를 직접 호출하려면 `ratatosk.io`의 `GET /v2`를 조회해 전체 엔드포인트 색인을 확인할 수 있으며, 별도의 API 키 없이 IP당 분당 1200회까지 호출할 수 있습니다.

## 다음 단계

- 도구 6개의 파라미터, 호출 예시, 실제 응답: [도구 레퍼런스](tools.ko.md)
- 한눈에 보는 도구 요약표: [README의 도구 표](../README.ko.md#도구)
- 전체 차트 설정값과 kagent 옵션: [차트 README](../charts/ratatosk-mcp/README.ko.md)
- kagent 매니페스트와 예제 에이전트 구성: [kagent 예제](../examples/kagent/README.ko.md)
