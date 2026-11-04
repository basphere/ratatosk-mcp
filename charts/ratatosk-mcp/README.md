# ratatosk-mcp Helm chart

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

Deploys the ratatosk MCP server in streamable-HTTP mode as a ClusterIP
Service. No RBAC, no secrets; runs cleanly under the Pod Security Standards
(PSS) `restricted` profile.

```bash
# from the repo root
helm install ratatosk-mcp ./charts/ratatosk-mcp
# → MCP endpoint: http://ratatosk-mcp.<namespace>.svc:8080/mcp
```

## Values

| Key | Default | Meaning |
| --- | --- | --- |
| `replicaCount` | `1` | Number of replicas; turn on `statelessHttp` before going above 1 |
| `image.repository` | `ghcr.io/basphere/ratatosk-mcp` | Image |
| `image.tag` | chart `appVersion` | Pin a specific server version |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| `ratatoskUrl` | `https://ratatosk.io` | Upstream changes API; point at a mirror if you proxy egress |
| `statelessHttp` | `false` | Serve HTTP without per-session state; needed for clients that use the 2026-07-28 MCP revision and for more than one replica |
| `logLevel` | `""` (= info) | `MCP_LOG`: `debug`, `warn`, `error` also accepted; any other value silently falls back to info, and no level records request arguments |
| `auditMode` | `""` (= off) | `MCP_AUDIT`: `metadata` records one line per tool call (argument names only), `full` adds argument values |
| `rateLimitPerMin` | `0` (= off) | `MCP_RATE_LIMIT_PER_MIN`: requests per minute per caller address (every MCP message counts). Only for a server that fronts callers you do not control |
| `service.port` | `8080` | Service / MCP endpoint port |
| `service.type` | `ClusterIP` | Service type |
| `resources` | small requests/limits | Adjust for busy clusters |
| `nodeSelector` / `tolerations` / `affinity` | `{}` / `[]` / `{}` | Standard scheduling controls |
| `clusterDomain` | `cluster.local` | Cluster DNS domain in the full Service name the kagent RemoteMCPServer registers |
| `kagent.enabled` | `false` | Also install the kagent integration (below) |
| `kagent.modelConfig` | `default-model-config` | ModelConfig name in your kagent install |
| `kagent.harness` | `""` | kagent 1.x only: the Harness the example agent runs on. Empty on 1.x skips the agent |
| `kagent.agent.enabled` | `true` | Install the example agent (when kagent.enabled) |
| `kagent.agent.name` | `ratatosk-agent` | Example agent name |
| `kagent.agent.k8sTools` | `true` | Give the agent kagent's read-only cluster tools so it discovers running versions itself |
| `kagent.agent.toolServer.name` | `kagent-tool-server` | kagent's tool server RemoteMCPServer (`<kagent release>-tool-server`) |
| `kagent.agent.toolServer.namespace` | `""` | Its namespace, when it is not this release's |

## kagent integration

```bash
# kagent 0.x
helm install ratatosk-mcp ./charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true
# kagent 1.x: name the Harness the agent runs on
helm install ratatosk-mcp ./charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true --set kagent.harness=<name>
```

Adds a `RemoteMCPServer` (kagent discovers every tool the server exposes) and a
ready-made `ratatosk-agent`. The agent also gets kagent's built-in read-only
cluster tools (`k8s_get_resources`, `k8s_get_resource_yaml`) so it can find
running versions on its own; turn this off with `kagent.agent.k8sTools=false`.
Enable only where the kagent CRDs exist.

The chart renders whichever kagent API the cluster serves: kagent 1.x
(`api.kagent.dev/v1alpha3`) when it is there, otherwise kagent 0.x
(`kagent.dev/v1alpha2`). `helm template` sees no cluster, so pass
`--api-versions api.kagent.dev/v1alpha3` to it for the 1.x shape. Support for
kagent 0.x will be removed once kagent 1.x is generally available.

On kagent 1.x:

- Every Agent runs on a Harness, and kagent creates none. Until
  `kagent.harness` names one in this namespace, the chart installs the server
  and the RemoteMCPServer but skips the agent; the install notes say how to add
  it.
- The RemoteMCPServer URL is the full Service name
  (`<service>.<namespace>.svc.<clusterDomain>`), because kagent 1.x agents
  cannot resolve a short one. Set `clusterDomain` if yours is not
  `cluster.local`.
- Keep the agent's cluster access read-only. The tool list above is enforced
  by the kagent runtime, but a Claude Harness exposes every tool on kagent's
  tool server, write tools included. Use a kagent-runtime Harness, or make the
  tool server read-only; see the
  [install guide](../../docs/install.en.md#kagent-1x).

For plain manifests, see [`examples/kagent/`](../../examples/kagent/README.md).

## Upgrades

```bash
helm upgrade ratatosk-mcp ./charts/ratatosk-mcp
```

Values you set with `--set`/`-f` are not carried over automatically. Repeat
them, or add `--reuse-values`. The image follows the chart `appVersion`
unless `image.tag` pins it.
