# kagent 연동 예제

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

이 디렉터리의 매니페스트는 클러스터에 ratatosk-mcp 서버를 배포하고 kagent에 MCP
서버(`RemoteMCPServer`)로 등록한 뒤, 예제 에이전트인 `ratatosk-agent`를 생성합니다.
kagent 0.x용과 1.x용 매니페스트가 각각 따로 준비되어 있으며, Ratatosk와 이 서버의 전체
개요는 저장소 [README](../../README.ko.md)에 있습니다.

`check_stack`은 실행 중인 버전을 서버 프로세스 안에서 비교하므로, 호출 시 ratatosk.io로
전송되는 정보는 프로젝트 슬러그뿐입니다. `check_upgrade`와 `get_release`는 지정한 버전을
ratatosk.io로 전송합니다. 계정이나 API 키는 필요하지 않으며, 매니페스트에 시크릿도
없습니다. 자세한 내용은
[실행 중인 컴포넌트 버전의 처리 방식](../../README.ko.md#실행-중인-컴포넌트-버전의-처리-방식)을
참고합니다.

에이전트가 사용하는 여섯 가지 도구의 상세 파라미터와 응답 예시는
[도구 레퍼런스](../../docs/tools.ko.md)에서 확인할 수 있습니다.

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
