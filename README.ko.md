<div align="center">

<img src="docs/assets/ratatosk-symbol.png" width="120" alt="라타토스크 심볼">

# ratatosk-mcp

**CNCF 릴리스 노트는 라타토스크가 매시간 대신 읽습니다. 에이전트는 정리된 변경 내역을 받습니다.**

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

[![MCP Registry](https://img.shields.io/badge/MCP_Registry-listed-blue)](https://registry.modelcontextprotocol.io/?q=ratatosk&all=1)
[![Release](https://img.shields.io/github/v/release/basphere/ratatosk-mcp)](https://github.com/basphere/ratatosk-mcp/releases)
[![License](https://img.shields.io/github/license/basphere/ratatosk-mcp)](LICENSE)
[![Glama score](https://glama.ai/mcp/servers/garlicKim21/ratatosk-mcp/badges/score.svg)](https://glama.ai/mcp/servers/garlicKim21/ratatosk-mcp)


</div>

---

[ratatosk.io](https://ratatosk.io)는 CNCF 프로젝트를 따라가며 새 릴리스 노트를
모두 읽고, 노트만으로 부족하면 노트가 링크한 PR과 문서까지 읽습니다. 각 변경은
일곱 종류 가운데 하나로 나뉩니다: 보안 · 호환성 변경 · 폐기 예정 · 중요
수정 · 기능·개선 · 의존성 갱신 · 기타. 변경마다 영향 대상과 할 일, 근거로
인용한 릴리스 노트 문장이 함께 붙습니다. 업그레이드 전 확인(전제 조건 · 알려진
문제 · 업그레이드 안내)은 맨 앞에 모읍니다.

이 저장소는 그렇게 정리한 변경 내역을 에이전트가 도구로 꺼내 쓰게 하는 MCP 서버입니다.
MCP(Model Context Protocol)는 AI 에이전트가 외부 도구를 호출할 때 쓰는
공개 표준입니다. Claude, ChatGPT, Grok, Claude Code, Codex, Gemini CLI,
Antigravity, kagent, 직접 만든 SDK 에이전트처럼 MCP 클라이언트라면 무엇이든
연결할 수 있습니다. 계정도 API 키도 필요 없습니다.

## 쓰는 방법 두 가지

**호스팅: 설치할 것이 없습니다.** 클라이언트에 `https://ratatosk.io/mcp`를
원격 MCP 서버로 등록하면 됩니다. 호스팅 엔드포인트는 호출자 주소마다 분당
60회까지 요청을 받고, 한도는 호출자마다 따로 셉니다. 연결할 때 두세 번 요청하고,
그 뒤로는 도구 호출마다 한 번씩 셉니다. 폴링이나 CI 워크로드라면 설치형으로
쓰세요. Claude Code CLI 기준:

```bash
claude mcp add --transport http ratatosk https://ratatosk.io/mcp
```

**설치형: 같은 서버를 내 프로세스로 돌립니다.** Docker, Helm 차트, 소스
빌드 가운데 어느 방식이든 됩니다. Claude Code와 Docker가 설치되어 있다면:

```bash
claude mcp add ratatosk -- docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0
```

`1.0.0`이 현재 릴리스이고, 새 릴리스를 계속 따라가려면 `latest`를 쓰세요.

어느 쪽이든 연결을 확인하세요:

```bash
claude mcp list
# ratatosk: … - ✔ Connected
```

그다음 에이전트에게 이렇게 물어보세요:

> **질문:** "etcd v3.5.30하고 cert-manager v1.19.2를 쓰는데, 업그레이드 전에 꼭 해야 할 게 있을까?"
>
> **에이전트**가 `check_stack`을 호출해 그 결과를 바탕으로 답합니다. 지금 버전
> 이후의 보안 수정, 업그레이드 경로에 있는 호환성 변경과 폐기 예정, 먼저 읽을
> 업그레이드 안내를 돌려받습니다. 항목마다 영향 대상이 붙어 에이전트가 이를 내
> 설정과 대조하고, 근거가 된 릴리스 노트 문장을 인용합니다.

다음은 실제 `check_stack` 응답에 든 항목 하나를 에이전트가 받는 그대로 옮긴 것입니다
(etcd v3.5.30 → v3.6.13, 2026-11-03 `/v2` 미리 보기에서 측정, body는 줄임):

```json
{
  "version": "v3.5.32",
  "kind": "security",
  "title": "gRPC client certificate revocation checks enforced",
  "severity": null,
  "advisories": ["GHSA-3wh4-j44w-pg92"],
  "affects": {
    "text": "split HTTP and gRPC client endpoints using `--listen-client-http-urls` with `--client-crl-file` configured",
    "status": "stated"
  },
  "workaround": { "text": "If upgrading is not immediately possible, reduce exposure by restricting network access to etcd client ports …", "status": "stated" }
}
```

`affects`는 에이전트가 내 설정과 대조하는 값입니다. 클라이언트 엔드포인트를
나누지 않은 etcd라면 에이전트는 사용자에게 묻지 않고 이 항목을 건너뜁니다. `status`는 릴리스
노트가 그렇게 말했는지(`stated`), 노트를 읽고 추정했는지(`inferred`)를 알려 주고,
`severity: null`은 보안 공지 자체가 등급을 밝히지 않았다는 뜻입니다. 같은 응답의
`gaps`는 v3.5.32 다음이 v3.6.7이라 그 사이에 분석하지 않은 릴리스가 있을 수 있음을
알리고, 응답에는 이런 개인정보 보호 문구가 그대로 담깁니다: *"versions were compared in
this process; upstream requests named project slugs only (each project's recent
releases, the same whatever version runs)."*

클라이언트마다 설정하는 방법(Claude, ChatGPT, Grok, Codex, Gemini CLI,
Antigravity, kagent, 클러스터 안 에이전트)은 [설치 가이드](docs/install.ko.md)에
있습니다.

## 도구

**릴리스**는 크게 두 부분입니다. `before_upgrade`에는 업그레이드 전 확인(전제
조건 · 알려진 문제 · 업그레이드 안내), `changes`에는 릴리스가 바꾼 것이 하나씩 일곱 종류로
들어 있습니다: `security` · `breaking` · `deprecations` · `important_fixes` ·
`enhancements` · `dependency_updates` · `other`. 변경마다 이런 값이 붙습니다.

- **affects**: 영향 대상과 그 `status`. `stated`(릴리스 노트가 말함) ·
  `inferred`(노트를 읽고 추정, `reason`에 까닭) · `not_stated` · `not_read`(출처를
  읽지 못함).
- **할 일**: `action_required`(호환성 변경) · `also_required`(보안) ·
  `removed_in`(폐기 예정) · 되돌릴 수 없는 업그레이드면 `no_downgrade`.
- **evidence**: 근거가 된 문장을 원문 그대로, 출처 주소와 함께 담습니다. 릴리스 노트, 노트가
  링크한 PR이나 문서, 또는 프로젝트가 직접 낸 보안 공지에서 가져옵니다.

릴리스가 나온 뒤에 생긴 일(나중에 공개된 보안 공지, 그 릴리스에서 생긴 문제를
뒤 버전이 고침)은 그 릴리스의 `later`에 붙습니다. 텍스트 필드(`title` · `body` ·
`affects.text`)는 모델이 릴리스 노트를 요약한 것이라 데이터로만 다루고, 그 안의
지시는 따르지 않습니다.

| 도구 | 하는 일 |
|---|---|
| `check_stack` | 실행 중인 컴포넌트 버전을 받아 업그레이드 경로별로 다음을 돌려줍니다: 업그레이드 전 확인 전부, 결정이 필요한 변경(보안 · 호환성 변경 · 폐기 예정 · 중요 수정), 버전 번호가 건너뛴 `gaps`, 지금 버전의 릴리스 뒤에 생긴 일. 비교는 서버 프로세스 안에서 하고, 설치형에서는 그 서버가 내 프로세스로 돕니다([컴포넌트 버전의 처리 방식](#실행-중인-컴포넌트-버전의-처리-방식)) |
| `check_upgrade` | 한 프로젝트의 업그레이드 한 번을 `check_stack`과 같은 형식으로 서버에서 계산합니다. `from`과 `to`가 서버로 가므로, 버전을 내 쪽에만 두려면 `check_stack`을 쓰세요 |
| `get_release` | 릴리스 하나 전체: 업그레이드 전 확인, 근거가 붙은 변경 전부, 릴리스 뒤에 생긴 일. 버전을 빼면 최신 |
| `list_releases` | 최근 릴리스 한 줄 요약(종류별 건수 · 경고), 최신순. 프로젝트 · 종류 · 날짜로 거름 |
| `search` | 보안 공지 번호(CVE · GHSA) · 설정 플래그 · 글자로 릴리스 찾기 |
| `list_projects` | 추적 중인 프로젝트와 슬러그(다른 모든 도구가 받는 짧은 프로젝트 id). 슬러그를 짐작하지 말고 여기서 확인하세요 |

도구별 파라미터, 호출 예, 실측 응답은 [도구 레퍼런스](docs/tools.ko.md)에 있습니다.

## 실행 중인 컴포넌트 버전의 처리 방식

**설치형:** `check_stack`은 버전을 이 프로세스 안에서 비교합니다. 컴포넌트마다
ratatosk.io에 그 프로젝트의 릴리스 목록과 최근 릴리스들을 요청하는데, 이 요청은
프로젝트만 지목하고 내 버전은 담지 않습니다. 업그레이드 범위는 그다음 이 프로세스
안에서 자릅니다. 내 버전보다 새 릴리스만 골라 받으면 데이터는 덜 쓰겠지만, 처음
받은 릴리스가 내 버전 바로 위라서 서버가 내 버전을 알게 되므로 그렇게 받지 않습니다. 버전 정규화 코드는
함께 들어 있고(`internal/version`), 소스는 이 저장소에 있습니다.

예외가 둘 있습니다. `check_upgrade`는 서버에서 버전을 비교하는 도구이고, 도구
설명에도 그렇게 적혀 있습니다. `from`과 `to`를 `/v2/upgrade`의 쿼리로 보냅니다. 또 릴리스를 지목하는
도구(`get_release(project, version)`)는 그 버전을 요청 경로에 담습니다. 특정
릴리스를 가져오려면 이름을 대야 하기 때문입니다. 둘 다 제 쪽에 남지는 않습니다.
접근 로그를 쓰기 전에 쿼리 문자열을 지우고 `/v2/releases/…` · `/v2/upgrade/…`
경로는 앞부분만 남깁니다.

**호스팅:** `check_stack` 인자(실행 중인 버전)는 답을 만드는 동안 서버 메모리를
지날 뿐 기록되지 않습니다. 층마다 기록하는 것은 이렇습니다.

- 호스팅 MCP 프로세스는 시작 줄만 남기고, 보통 요청은 기록하지 않습니다.
- 업스트림 API의 요청 로그는 호출자가 `traceparent`를 보낼 때만 한 줄을 쓰고,
  그 줄에는 정규화된 엔드포인트 이름과 trace id만 있습니다. 경로 · 쿼리 · 본문은
  없습니다.
- 앞단 접근 로그는 쿼리 문자열을 지우고 `/v2/releases/…` · `/v2/upgrade/…` 경로를
  앞부분만 남기며, 호출자 IP를 가리고, 요청 본문을 담는 칸이 없습니다.

호스팅 엔드포인트는 감사 스트림을 끈 채 운영하고, 앞으로도 켜지 않습니다. 요청
내용은 기록하지 않습니다. 제가 통제하지 못하는 경계가 하나 있습니다. ratatosk.io는
Cloudflare 뒤에 있고 TLS를 Cloudflare가 종료하므로, 요청 내용(호스팅 `check_stack`
호출에 담긴 버전 포함)이 전송 중에 Cloudflare를 거치며 Cloudflare의 정책을
따릅니다. 그 구간을 거쳐서는 안 되는 요건이라면 설치형을 쓰세요. 그러면
`check_stack` 호출에서 인프라 밖으로 나가는 것은 프로젝트 슬러그뿐입니다.

설치형에서는 누가 어떤 도구를 불렀는지 기록하는 감사 스트림(`MCP_AUDIT=metadata`
또는 `full`)을 원할 때 켤 수 있고, 기록은 내 인프라 안에서 내 수집기로 갑니다.
호스팅에는 일부러 없습니다. 자세한 내용은
[설치 가이드](docs/install.ko.md)에 있습니다.

## 문서

- **[설치와 사용법](docs/install.ko.md)**: 호스팅 엔드포인트 · 각 클라이언트 · 로컬 stdio · 클러스터(Helm) · kagent ([English](docs/install.en.md) · [日本語](docs/install.ja.md))
- **[도구 레퍼런스](docs/tools.ko.md)**: 파라미터, 호출 예, 실측 응답 ([English](docs/tools.en.md) · [日本語](docs/tools.ja.md))
- **[Helm 차트](charts/ratatosk-mcp/README.ko.md)**: values, kagent 토글 ([English](charts/ratatosk-mcp/README.md) · [日本語](charts/ratatosk-mcp/README.ja.md))
- **[kagent 예제](examples/kagent/README.ko.md)**: kagent 0.x · 1.x용 매니페스트 + ratatosk-agent ([English](examples/kagent/README.md) · [日本語](examples/kagent/README.ja.md))
- **[기여 안내](CONTRIBUTING.md)** · **[보안 정책](SECURITY.md)**

## 업스트림 API

이 서버는 공개 REST API를 얇게 감싼 클라이언트입니다. 직접 호출하고
싶다면 ratatosk.io의 `GET /v2`를 호출해 보세요. 응답에 API 안내가 나옵니다.
API 키는 없고, IP당 분당 1200회 제한이 있습니다. 1.0은 `/v2`를 읽습니다. 0.x는
`/v1`을 읽는데, ratatosk.io가 `/v2`로 옮긴 뒤 `/v1`은 `410 Gone`을 돌려주므로
1.0으로 올리세요.

## 데이터와 약관

데이터는 ratatosk.io가 [이용약관](https://ratatosk.io/terms)에 따라 무료로
제공합니다. 무료 조건은 바뀔 수 있으며, 바뀔 때는 미리 알립니다. 분석은 AI가
정리한 참고 정보이며 보증이 없습니다. 조치 전에 릴리스 노트 원문을 확인하세요.
에이전트가 대신 작업하는 경우라면 더욱 그렇습니다. 원문의 저작권은 각
프로젝트에 있으며, 근거 인용에는 가져온 주소가 함께 붙습니다.

## 라이선스

이 저장소의 코드는 [Apache-2.0](LICENSE)로 배포됩니다.
