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
[실행 중인 컴포넌트 버전의 처리 방식](../../README.ko.md#실행-중인-컴포넌트-버전-처리-방식)을
참고합니다.

에이전트가 사용하는 여섯 가지 도구의 상세 파라미터와 응답 예시는
[도구 레퍼런스](../../docs/tools.ko.md)에서 확인할 수 있습니다.


## kagent 버전별 매니페스트 선택

kagent 1.x(`api.kagent.dev/v1alpha3`)와 0.x(`kagent.dev/v1alpha2`)는 API가 서로 호환되지 않아 버전 계열마다 매니페스트 디렉터리가 따로 준비되어 있습니다. 아래 명령으로 클러스터에 설치된 kagent API 버전을 먼저 확인합니다.

```bash
kubectl api-resources --api-group=api.kagent.dev   # agents가 나오면 kagent 1.x
kubectl api-resources --api-group=kagent.dev       # agents가 나오면 kagent 0.x
```

| kagent 버전 | 매니페스트 경로 | 사전 준비 사항 |
| --- | --- | --- |
| 0.x (0.10.x) | [`0.x/`](0.x/README.ko.md) | kagent 설치 |
| 1.x (1.0 정식 출시 전) | [`1.x/`](1.x/README.ko.md) | kagent 설치 및 에이전트를 실행할 **Harness** 구성 |

두 버전 계열 모두 MCP 서버는 공용 [`ratatosk-deploy.yaml`](ratatosk-deploy.yaml)(Deployment 및 Service 포함, RBAC 불필요)로 배포합니다. kagent 1.x가 정식 출시되면 kagent 0.x 지원은 제거합니다.

매니페스트 대신 저장소의 Helm 차트로 설치하면 차트가 클러스터의 kagent 버전 계열을 스스로 감지하며, kagent 1.x 환경에서는 `--set kagent.harness=<Harness 이름>` 옵션을 함께 지정합니다.

```bash
git clone https://github.com/basphere/ratatosk-mcp
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp -n kagent --set kagent.enabled=true
```

## 질문 예시

> "이 클러스터에서 업그레이드 전에 조치해야 할 사항이 있나요?"

에이전트는 기본 kagent 설치에 포함된 `kagent-tool-server`의 읽기 전용 클러스터 도구(`k8s_get_resources`, `k8s_get_resource_yaml`)로 실행 중인 버전을 스스로 확인하며, 아래와 같이 질문에 버전을 직접 적어서 물어볼 수도 있습니다.

> "kubernetes v1.36.0, cilium v1.17.18, envoy v1.38.3을 실행 중인데, 업그레이드 전에 조치해야 할 사항이 있나요?"

에이전트는 심각도 순으로 정리된 변경 내역에 릴리스 노트 원문 인용구와 출처 링크를 덧붙여 답합니다.
