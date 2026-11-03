# Ratatosk MCP 서버

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

[Ratatosk](https://ratatosk.io)는 CNCF 졸업 · 인큐베이팅 단계 프로젝트의 릴리스
노트를 읽고 변경을 하나씩 일곱 종류(보안 · 호환성 변경 · 폐기 예정 · 중요 수정 ·
기능·개선 · 의존성 갱신 · 기타)로 나눕니다. 변경마다 누가 영향을 받는지와 근거가
된 릴리스 노트 줄이 붙습니다. 이 MCP 서버가 그 릴리스 정리를 클러스터 안
에이전트에게 제공합니다.

프라이버시: `check_stack` 툴은 실행 중인 버전을 이 프로세스 안에서 로컬로
비교합니다. ratatosk 서버로는 프로젝트 슬러그만 전송되고, 버전은 클러스터
밖으로 나가지 않습니다. 업스트림 API는 공개·읽기 전용·무자격증명입니다
(IP당 분당 1200회 제한).

## 도구

| 도구 | 용도 |
| --- | --- |
| `list_projects` | 추적 프로젝트 전목록 — 슬러그는 먼저 여기서 확인 |
| `check_stack` | 실행 중 버전을 프로젝트 릴리스와 대조(로컬 비교): 업그레이드 전 확인, 결정이 필요한 변경, 버전 번호가 건너뛴 곳 |
| `check_upgrade` | 한 프로젝트의 한 번 업그레이드를 서버가 계산 — `from` · `to`가 서버로 감 |
| `get_release` | 릴리스 하나 전체: 업그레이드 전 확인, 근거가 붙은 변경 전부 |
| `list_releases` | 최근 릴리스 한 줄 요약, 최신순 |
| `search` | 보안 공지 번호 · 플래그 · 글자로 릴리스 찾기 |

## 설치

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml            # Deployment + Service (RBAC 불필요)
kubectl apply -f $BASE/ratatosk-remote-mcpserver.yaml  # kagent에 등록
kubectl apply -f $BASE/ratatosk-agent.yaml             # 선택: 예제 에이전트 ratatosk-agent
```

매니페스트에는 `namespace: kagent`가 들어 있습니다. 이 디렉토리에서 로컬 파일로
적용해도 동일합니다.

헬름을 쓴다면 차트의 kagent 토글 하나로도 같은 구성이 됩니다:

```bash
helm install ratatosk-mcp ../../charts/ratatosk-mcp -n kagent --set kagent.enabled=true
```

예제 에이전트에게 이렇게 물어보세요:

> "이 클러스터에서 업그레이드 전에 조치할 게 있나요?"

에이전트는 kagent 내장 읽기 전용 클러스터 도구(`kagent-tool-server`의
`k8s_get_resources`, `k8s_get_resource_yaml` — 기본 kagent 설치에 포함)로
구동 중인 버전을 스스로 알아냅니다. 버전을 직접 알려줘도 됩니다:

> "kubernetes v1.36.0, cilium v1.17.18, envoy v1.38.3을 돌리는데,
> 업그레이드 전에 조치할 게 있나요?"

에이전트는 심각도 순으로 정리된 변경에 릴리스 노트 원문 인용과 소스 링크를
붙여 답합니다.
