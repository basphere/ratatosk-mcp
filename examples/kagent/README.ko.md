# Ratatosk MCP 서버

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

[Ratatosk](https://ratatosk.io)는 CNCF 프로젝트의 릴리스 노트를 읽고 변경을
하나씩 일곱 종류(보안 · 호환성 변경 · 폐기 예정 · 중요 수정 · 기능·개선 · 의존성
갱신 · 기타)로 나눕니다. 변경마다 누가 영향을 받는지와 근거가 된 릴리스 노트
줄이 붙습니다. 이 MCP 서버가 그 릴리스 정리를 클러스터 안 에이전트에게
제공합니다.

프라이버시: `check_stack`은 실행 중인 버전을 이 프로세스 안에서 비교하므로,
`check_stack` 호출이 ratatosk.io로 보내는 것은 프로젝트 슬러그뿐입니다. 예외는
`check_upgrade`와 `get_release`로, 이 둘은 지목한 버전을 보냅니다. 업스트림 API는 공개·읽기 전용·무자격증명입니다
(IP당 분당 1200회 제한).

## 도구

| 도구 | 용도 |
| --- | --- |
| `list_projects` | 추적 프로젝트와 슬러그. 슬러그는 먼저 여기서 확인 |
| `check_stack` | 실행 중 버전을 프로젝트 릴리스와 대조(로컬 비교): 업그레이드 전 확인, 결정이 필요한 변경, 버전 번호가 건너뛴 곳 |
| `check_upgrade` | 한 프로젝트의 한 번 업그레이드를 서버가 계산: `from` · `to`가 서버로 감 |
| `get_release` | 릴리스 하나 전체: 업그레이드 전 확인, 근거가 붙은 변경 전부 |
| `list_releases` | 최근 릴리스 한 줄 요약, 최신순 |
| `search` | 보안 공지 번호 · 플래그 · 글자로 릴리스 찾기 |

## kagent 버전에 맞는 묶음 고르기

kagent 1.x는 새 API(`api.kagent.dev/v1alpha3`)를 쓰고 0.x API(`kagent.dev/v1alpha2`)를
읽지 않습니다. 반대도 마찬가지라서 kagent 계열마다 매니페스트 묶음이 따로
있습니다. 클러스터가 어느 쪽을 제공하는지 먼저 확인하세요:

```bash
kubectl api-resources --api-group=api.kagent.dev   # agents가 나오면 kagent 1.x
kubectl api-resources --api-group=kagent.dev       # agents가 나오면 kagent 0.x
```

| kagent | 매니페스트 | 시작 전에 필요한 것 |
| --- | --- | --- |
| 0.x (0.10.x) | [`0.x/`](0.x/README.ko.md) | kagent 설치 |
| 1.x (1.0 정식 출시 전) | [`1.x/`](1.x/README.ko.md) | kagent 설치, 그리고 에이전트를 돌릴 **Harness** |

두 묶음 모두 서버는 공용 [`ratatosk-deploy.yaml`](ratatosk-deploy.yaml)
(Deployment + Service, RBAC 불필요)로 띄웁니다. kagent 0.x 지원은 kagent 1.x가
정식 출시되면 제거합니다.

저장소의 Helm 차트를 써도 됩니다. 차트가 kagent 계열을 스스로 알아봅니다(1.x에서는
`--set kagent.harness=<Harness 이름>`을 더하세요):

```bash
git clone https://github.com/basphere/ratatosk-mcp
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp -n kagent --set kagent.enabled=true
```

## 물어보기

> "이 클러스터에서 업그레이드 전에 조치할 게 있나요?"

에이전트는 kagent 내장 읽기 전용 클러스터 도구(기본 kagent 설치에 들어 있는
`kagent-tool-server`의 `k8s_get_resources`, `k8s_get_resource_yaml`)로
구동 중인 버전을 스스로 알아냅니다. 버전을 직접 알려줘도 됩니다:

> "kubernetes v1.36.0, cilium v1.17.18, envoy v1.38.3을 돌리는데,
> 업그레이드 전에 조치할 게 있나요?"

에이전트는 심각도 순으로 정리된 변경에 릴리스 노트 원문 인용과 소스 링크를
붙여 답합니다.
