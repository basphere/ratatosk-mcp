# Ratatosk on kagent 0.x

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

These manifests are for kagent 0.x (0.10.x, API `kagent.dev/v1alpha2`). On
kagent 1.x use [`../1.x/`](../1.x/README.md) instead; what the server and the
agent do is in [`../README.md`](../README.md). Support for kagent 0.x will be
removed once kagent 1.x is generally available.

## Install

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml                # Deployment + Service (no RBAC needed)
kubectl apply -f $BASE/0.x/ratatosk-remote-mcpserver.yaml  # register with kagent
kubectl apply -f $BASE/0.x/ratatosk-agent.yaml             # optional: the ratatosk-agent example agent
```

The manifests carry `namespace: kagent`. Applying local copies works the same
from a clone. If kagent lives in another namespace, change `namespace:` in all
three files and the namespace inside the RemoteMCPServer URL.

The RemoteMCPServer URL is the full Service name,
`ratatosk-mcp.kagent.svc.cluster.local`. If your cluster's DNS domain is not
`cluster.local`, edit it.

## Verify

`ratatosk-agent` appears in the kagent UI. It finds running versions itself
through kagent's built-in read-only cluster tools (`k8s_get_resources`,
`k8s_get_resource_yaml` from `kagent-tool-server`, present in a default kagent
install).
