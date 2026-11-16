# kagent 1.x에서 Ratatosk 쓰기

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

이 매니페스트는 kagent 1.x(1.0 정식 출시 전, API `api.kagent.dev/v1alpha3`)용입니다.
kagent 0.x라면 [`../0.x/`](../0.x/README.ko.md)를 쓰세요. 서버와 에이전트가 하는
일은 [`../README.ko.md`](../README.ko.md)에 있습니다.

## 시작 전에: Harness

kagent 1.x는 모든 Agent를 **Harness** 위에서 돌립니다. Harness는 어떤 런타임이
에이전트를 실행하는지, 어느 Agent Substrate WorkerPool에서 도는지, 스냅샷을 어디에
두는지를 정합니다. kagent를 설치해도 Harness는 하나도 생기지 않고, 예제
에이전트는 Harness 이름을 가리키므로 먼저 확인하세요:

```bash
kubectl get harnesses.api.kagent.dev -n kagent
```

목록이 비어 있으면 하나 만드세요. 방법은 kagent의
[Agent harness](https://kagent.dev/docs/kagent/1.x/agents/agent-harness/) 안내에
있습니다. 이 에이전트에는 kagent 런타임을 쓰세요:

```yaml
apiVersion: api.kagent.dev/v1alpha3
kind: Harness
metadata:
  name: kagent
  namespace: kagent
spec:
  kagent: {}
  substrate:
    workerPoolRef:
      name: kagent-default        # `kagent install`이 만드는 WorkerPool
    snapshotPolicy:
      location: s3://<bucket>/kagent/
```

kagent 런타임을 권하는 이유: 매니페스트에 적힌 읽기 전용 클러스터 도구 두 개만
에이전트에게 줍니다. Claude Harness(`spec.claude`)는 도구 목록을 지키지 못해서
kagent 도구 서버의 도구를 전부 노출합니다. `k8s_apply_manifest`,
`k8s_delete_resource` 같은 쓰기 도구도 포함됩니다. 그래도 Claude Harness를 쓴다면
먼저 도구 서버를 읽기 전용으로 만드세요. [설치 안내](../../../docs/install.ko.md#kagent-1x)의
읽기 전용 안내를 보세요.

## 설치

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml                # Deployment + Service (RBAC 불필요)
kubectl apply -f $BASE/1.x/ratatosk-remote-mcpserver.yaml  # kagent에 등록
curl -fsSLO $BASE/1.x/ratatosk-agent.yaml                  # 선택: 예제 에이전트 ratatosk-agent
# spec.harnessRef.name(자리표시자 your-harness)을 내 Harness 이름으로 고친 뒤:
kubectl apply -f ratatosk-agent.yaml
```

매니페스트에는 `namespace: kagent`가 들어 있고, Harness는 Agent와 같은
네임스페이스에 있어야 합니다. kagent가 다른 네임스페이스에 있다면 세 파일의
`namespace:`와 RemoteMCPServer URL 안의 네임스페이스를 함께 바꾸세요.

RemoteMCPServer URL이 Service의 전체 이름
`ratatosk-mcp.kagent.svc.cluster.local`인 것은 일부러입니다. kagent 1.x
에이전트는 egress 정책에 적힌 호스트 이름만 풀리는 샌드박스에서 돌기 때문에
`ratatosk-mcp` 같은 짧은 이름은 그 안에서 풀리지 않습니다. 클러스터 DNS 도메인이
`cluster.local`이 아니면 고쳐 쓰세요.

## 확인

```bash
kubectl describe agents.api.kagent.dev ratatosk-agent -n kagent
```

리소스 이름은 전체로 쓰세요. 클러스터에 kagent 0.x CRD가 남아 있으면
`kubectl get agent`가 그쪽에서 답할 수 있습니다. Harness 이름이 틀리면 Agent의
conditions에 나타납니다. 에이전트를 바꾸면 새 리비전이 만들어지니, 바뀐 내용을
쓰려면 새 세션을 여세요.
