# kagent 0.x에서 Ratatosk 쓰기

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

이 매니페스트는 kagent 0.x(0.10.x, API `kagent.dev/v1alpha2`)용입니다. kagent
1.x라면 [`../1.x/`](../1.x/README.ko.md)를 쓰세요. 서버와 에이전트가 하는 일은
[`../README.ko.md`](../README.ko.md)에 있습니다. kagent 0.x 지원은 kagent 1.x가
정식 출시되면 제거합니다.

## 설치

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml                # Deployment + Service (RBAC 불필요)
kubectl apply -f $BASE/0.x/ratatosk-remote-mcpserver.yaml  # kagent에 등록
kubectl apply -f $BASE/0.x/ratatosk-agent.yaml             # 선택: 예제 에이전트 ratatosk-agent
```

매니페스트에는 `namespace: kagent`가 들어 있습니다. 클론해서 로컬 파일로 적용해도
같습니다. kagent가 다른 네임스페이스에 있다면 세 파일의 `namespace:`와
RemoteMCPServer URL 안의 네임스페이스를 함께 바꾸세요.

RemoteMCPServer URL은 Service의 전체 이름
`ratatosk-mcp.kagent.svc.cluster.local`입니다. 클러스터 DNS 도메인이
`cluster.local`이 아니면 고쳐 쓰세요.

## 확인

kagent UI에 `ratatosk-agent`가 나타납니다. 에이전트는 kagent 내장 읽기 전용
클러스터 도구(`kagent-tool-server`의 `k8s_get_resources`,
`k8s_get_resource_yaml` — 기본 kagent 설치에 포함)로 구동 중인 버전을 스스로
알아냅니다.
