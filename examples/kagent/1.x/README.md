# Ratatosk on kagent 1.x

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

These manifests are for kagent 1.x (1.0 pre-release, API
`api.kagent.dev/v1alpha3`). On kagent 0.x use [`../0.x/`](../0.x/README.md)
instead; what the server and the agent do is in [`../README.md`](../README.md).

## Before you start: a Harness

kagent 1.x runs every Agent on a **Harness**: which runtime executes it, the
Agent Substrate WorkerPool it runs on, and where its snapshots are stored. A
kagent install creates no Harness, and the example agent names one, so check
first:

```bash
kubectl get harnesses.api.kagent.dev -n kagent
```

If the list is empty, create one; kagent's
[Agent harness](https://kagent.dev/docs/kagent/1.x/agents/agent-harness/)
guide has the details. For this agent, use the kagent runtime:

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
      name: kagent-default        # the WorkerPool `kagent install` provisions
    snapshotPolicy:
      location: s3://<bucket>/kagent/
```

Why the kagent runtime: it gives the agent only the two read-only cluster
tools the manifest names. A Claude Harness (`spec.claude`) cannot enforce a
tool list and exposes every tool on kagent's tool server, write tools such as
`k8s_apply_manifest` and `k8s_delete_resource` included. If you use one anyway,
make the tool server read-only first, as the
[install guide](../../../docs/install.en.md#kagent-1x) describes.

## Install

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml                # Deployment + Service (no RBAC needed)
kubectl apply -f $BASE/1.x/ratatosk-remote-mcpserver.yaml  # register with kagent
curl -fsSLO $BASE/1.x/ratatosk-agent.yaml                  # optional: the ratatosk-agent example agent
# edit spec.harnessRef.name (the placeholder is your-harness) to your Harness, then:
kubectl apply -f ratatosk-agent.yaml
```

The manifests carry `namespace: kagent`; the Harness has to be in the same
namespace as the Agent. If kagent lives elsewhere, change `namespace:` in all
three files and the namespace inside the RemoteMCPServer URL.

The RemoteMCPServer URL is the full Service name,
`ratatosk-mcp.kagent.svc.cluster.local`, on purpose: kagent 1.x agents run in a
sandbox that resolves only the hostnames its egress policy names, so a short
name such as `ratatosk-mcp` never resolves there. If your cluster's DNS domain
is not `cluster.local`, edit it.

## Verify

```bash
kubectl describe agents.api.kagent.dev ratatosk-agent -n kagent
```

Use the full resource name: if the cluster still has the kagent 0.x CRDs,
`kubectl get agent` may answer from those. A wrong Harness name shows up in the
Agent's conditions. Changes to the agent prepare a new revision; open a new
session to use it.
