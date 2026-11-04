# Install & usage

[English](install.en.md) · [한국어](install.ko.md) · [日本語](install.ja.md)

This page covers the four ways to give an AI agent access to Ratatosk's
release changes: the hosted endpoint, with nothing to install; a stdio server on your
laptop; a Helm deployment inside a Kubernetes cluster; and the kagent
integration. How to register the server with each client is under
[Register the endpoint with your client](#register-the-endpoint-with-your-client)
and [Other stdio clients](#other-stdio-clients).

**MCP (Model Context Protocol)** is the standard protocol AI agents use to
call external tools. ratatosk-mcp is a server that speaks this protocol and
provides six tools: `check_stack` (takes the component versions you run,
compares them inside the server process, and returns each upgrade path's
before-upgrade checks, changes that ask for a decision, and version gaps),
`check_upgrade` (the same for one project and one hop, computed by the server),
`get_release` (one release in full), `list_releases` (recent releases as
summaries), `search` (by advisory id, flag or text), and `list_projects`
(tracked projects and their slugs). Any MCP client can connect: Claude,
ChatGPT, Grok, Claude Code, Codex, Gemini CLI, Antigravity, kagent, or your
own SDK agent.

## Choosing a deployment mode

| Your situation | Use | Section |
| --- | --- | --- |
| Want to try it without installing anything | Register the hosted endpoint URL | [Hosted](#hosted-endpoint) |
| On a laptop with Claude Code, Claude Desktop, Codex, Gemini CLI or any stdio MCP client | `docker run` (stdio) | [Local](#local-stdio) |
| Running your own agents in Kubernetes (any framework, CI jobs, SDK clients) | Helm chart | [In-cluster](#in-cluster-deployment-without-kagent-helm) |
| Using [kagent](https://kagent.dev) (0.x or 1.x) | `kagent.enabled=true` Helm toggle, or plain manifests | [kagent integration](#kagent-integration) |

All four modes serve the same public data through the same six tools, and no
mode needs an account or an API key. Three things differ:

- **How far the versions you pass to `check_stack` travel.** Self-hosted,
  the comparison finishes inside your own process, so the versions never
  leave your infrastructure. Hosted, they pass through server memory but are
  written to no log.
- **Which request limits apply.** Hosted gives each caller 60 requests
  a minute, and every hosted caller shares the server's upstream allowance;
  self-hosted gets its own IP's 1200 upstream requests a minute.
- **Whether you can keep an audit stream.** Self-hosted only.

In every mode, tools that take a specific version as an argument, like
`get_release`, put that version in the upstream request path, because naming
the release is how it is fetched. Even that path does not land in server-side
logs, though: the access log strips query strings and reduces `/v2/releases/…` and
`/v2/upgrade/…` paths to their prefix before writing. Each section below has
the details.

## Prerequisites

- **Hosted**: any client that supports remote MCP servers (streamable HTTP).
  Nothing else.
- **Local (stdio)**: Docker. Go 1.26 or later to build from source.
- **The `claude mcp add` commands in the examples**: the
  [Claude Code](https://claude.com/claude-code) CLI (the `claude` command)
  must be installed. Not required if you use another MCP client; follow its
  registration procedure instead.
- **In-cluster (Helm)**: a Kubernetes cluster, Helm 3, and a clone of this
  repository. The chart is not published to a chart repository or an OCI
  registry; it lives only in the repo.
- **Outbound HTTPS (egress)**: in any self-hosted mode, the server process
  must be able to open outbound HTTPS connections to `ratatosk.io:443`. If
  your cluster restricts egress with NetworkPolicy or an egress proxy, add
  this destination to the allowlist. If you have to go through a proxy or a
  mirror, `RATATOSK_URL` (chart value: `ratatoskUrl`) changes the upstream
  address. If egress is blocked, tools return errors (`check_stack`: "could
  not read the project list"). When only some fetches fail, `check_stack`
  still answers and puts `fetch failed: …` in the affected component's
  `note`. See [Troubleshooting](#troubleshooting).
- **kagent integration**: a cluster with kagent already installed (kagent
  CRDs included). On kagent 1.x, also a Harness for the example agent to run
  on; see [kagent 1.x](#kagent-1x).

## Hosted endpoint

`https://ratatosk.io/mcp` is a remote MCP endpoint you use right away, with
nothing to install. It is served over streamable HTTP and is stateless: there
are no sessions, so each request is handled independently, without an
`Mcp-Session-Id` round trip.

### Register the endpoint with your client

The URL is the same everywhere; only where you enter it differs.

| Client | Where to register `https://ratatosk.io/mcp` |
|---|---|
| Claude (web, desktop) | Customize → Connectors → + Add → Add custom connector. On Team and Enterprise an owner adds it under Organization settings → Connectors, and members connect it under Customize → Connectors ([docs](https://support.claude.com/en/articles/11175166-getting-started-with-custom-connectors-using-remote-mcp)) |
| ChatGPT (web) | chatgpt.com/plugins → + → Add custom MCP server, enter the URL as the server URL, no authentication ([docs](https://developers.openai.com/api/docs/guides/custom-mcp-server)) |
| Grok | grok.com/connectors → New Connector → Custom ([docs](https://docs.x.ai/grok/connectors)) |
| Claude Code | `claude mcp add --transport http ratatosk https://ratatosk.io/mcp` ([docs](https://code.claude.com/docs/en/mcp)) |
| Grok Build | `grok mcp add --transport http ratatosk https://ratatosk.io/mcp` ([docs](https://docs.x.ai/build/features/mcp-servers)) |
| Codex | `~/.codex/config.toml`: a `[mcp_servers.ratatosk]` table with `url = "https://ratatosk.io/mcp"` ([docs](https://learn.chatgpt.com/docs/extend/mcp)) |
| Gemini CLI | `~/.gemini/settings.json`: `"httpUrl": "https://ratatosk.io/mcp"` under `mcpServers.ratatosk` ([docs](https://geminicli.com/docs/tools/mcp-server/)) |
| Antigravity | `~/.gemini/config/mcp_config.json`: `"serverUrl": "https://ratatosk.io/mcp"` under `mcpServers.ratatosk` ([docs](https://antigravity.google/docs/mcp)) |
| kagent | A `RemoteMCPServer` resource; see [kagent integration](#kagent-integration) |

Gemini CLI takes the address as `httpUrl` and Antigravity as `serverUrl`,
although both read files under `~/.gemini`. With Claude Code:

```bash
claude mcp add --transport http ratatosk https://ratatosk.io/mcp
```

### Verify

You can check that the endpoint is alive with curl, no client needed:

```bash
curl -s -X POST https://ratatosk.io/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1.0"}}}'
```

The response comes with SSE (Server-Sent Events) framing: a `Content-Type`
of `text/event-stream` is expected, not an error. If the JSON on the `data:`
line shows `"serverInfo"` with `"name":"ratatosk"`, the endpoint is up and
answering:

```
event: message
data: {"jsonrpc":"2.0","id":1,"result":{…,"serverInfo":{"name":"ratatosk","version":"1.0.0"}}}
```

With Claude Code, check the `claude mcp list` output:

```
ratatosk: https://ratatosk.io/mcp (HTTP) - ✔ Connected
```

### Limits and expected behavior

- **GET for SSE returns 405**: stateless mode serves no server-initiated
  notification stream (the SSE opened with GET), so a 405 response is normal
  behavior. Opening the URL in a browser redirects to an explainer page
  (`/docs/mcp`).
- **Fair use**: the hosted endpoint allows **60 requests per minute per
  caller**, counted per caller address rather than pooled, so one busy caller
  cannot starve the rest. Every MCP message counts: a client spends two or
  three connecting (`initialize`, `notifications/initialized`, `tools/list`),
  then one per tool call. Exceeding it returns `429` with `Retry-After`.
  Upstream, one tool call can be many API requests (`check_stack` reads every
  recent release of each component's project); that cost is the server's, not
  part of your 60. Calling `/v2` directly instead gives you 1200
  requests/minute against your own IP. For heavy use such as polling,
  self-host.

### Privacy: what the hosted endpoint records

On the hosted endpoint, your `check_stack` arguments (the list of versions
you run) pass through server memory, but no request content is recorded
anywhere:

- The MCP server log keeps only the startup line and upstream error lines;
  normal requests are not logged. Error lines carry no request arguments
  either.
- The upstream API's request log writes one line only when the caller sends a
  `traceparent`, and even that line holds only a normalized endpoint label
  and the trace_id. Paths and queries are not recorded.
- The access log in front of the server strips query strings, reduces the
  `/v2/releases/…` and `/v2/upgrade/…` paths (where slugs and versions ride)
  to their prefix, masks IPs, and has no field for request bodies at all. It
  rotates locally within 7 days, and a copy goes to the operator's central log
  store, which also keeps it 7 days.
- The audit stream described below is off on the hosted endpoint, and stays
  off.

One boundary lies outside Ratatosk's control: ratatosk.io sits behind
Cloudflare, which terminates TLS, so request contents (including the versions
in a hosted `check_stack` call) pass through Cloudflare in transit and follow
its policy. If that boundary does not meet your requirements, self-host: the
versions you pass to `check_stack` then never leave your process.

## Local (stdio)

stdio is the mode where an MCP client spawns the server as a child process
and talks to it over standard input/output. It is the usual choice on a
laptop with Claude Code, Claude Desktop, Codex, Gemini CLI or Antigravity.

### Claude Code

```bash
claude mcp add ratatosk -- docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0
```

In place of `1.0.0` you can use any
[release tag](https://github.com/basphere/ratatosk-mcp/releases), or
`latest` to always track the newest release. See
[Version pinning](#version-pinning).

### Claude Desktop

Claude Desktop registers MCP servers through a config file
(`claude_desktop_config.json`) instead of a CLI. Add this under
`mcpServers`:

```json
{
  "mcpServers": {
    "ratatosk": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "ghcr.io/basphere/ratatosk-mcp:1.0.0"]
    }
  }
}
```

The config file's location differs per operating system; see the Claude
Desktop documentation. If you would rather install nothing, register the
[hosted endpoint](#hosted-endpoint) above as a remote connector instead.

### Other stdio clients

Every client below runs the same command,
`docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0`; only where you put
it differs.

| Client | Where |
|---|---|
| Codex | `~/.codex/config.toml`: `[mcp_servers.ratatosk]` with `command = "docker"`, `args = ["run", "--rm", "-i", "ghcr.io/basphere/ratatosk-mcp:1.0.0"]` and `startup_timeout_sec = 60` (the first run pulls the image) |
| Gemini CLI | `~/.gemini/settings.json`: the same `mcpServers` block as Claude Desktop above |
| Antigravity | `~/.gemini/config/mcp_config.json`: the same `mcpServers` block as Claude Desktop above |
| Grok Build | `grok mcp add ratatosk -- docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0` |

ChatGPT and grok.com connect to a URL, not to a command on your machine, so
they take the hosted endpoint.

### Build from source

To use a binary without Docker:

```bash
go build -o ratatosk-mcp .
claude mcp add ratatosk -- /path/to/ratatosk-mcp
```

Any MCP client that speaks stdio works the same way.

### Verify

```bash
claude mcp list
# ratatosk: docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0 - ✔ Connected
```

Then ask your agent:

> "We run envoy v1.36.8 and istio 1.30.1. Anything we should act on before
> upgrading?"

If it is working, the agent calls `check_stack` and answers with the
security fixes, removals, and changed defaults released after your versions,
each with its evidence quote.

## In-cluster deployment without kagent (Helm)

Set `MCP_HTTP_ADDR` and the same binary serves MCP over streamable HTTP at
`/mcp` (with a `/healthz` health check). The chart deploys this as a
ClusterIP Service and sets `MCP_HTTP_ADDR` automatically to match
`service.port` (default 8080). Anything in the cluster connects to a URL
instead of spawning a process: your own SDK-built agents, other agent
frameworks, even a CI job that gates upgrades on `check_stack`.

### Install

```bash
git clone https://github.com/basphere/ratatosk-mcp
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp
```

It needs no RBAC and no secrets, and runs without warnings under the Pod
Security Standards (PSS) `restricted` profile. Every chart value is listed
in the [chart README](../charts/ratatosk-mcp/README.md). Turn on
`--set statelessHttp=true` in two cases: when clients use the 2026-07-28
revision of the MCP spec over HTTP (clients that send
`Mcp-Protocol-Version: 2026-07-28`), and when scaling to two or more
replicas. See the [Configuration reference](#configuration-reference).

### Verify

Check that the pod is up:

```bash
kubectl get pods -l app.kubernetes.io/name=ratatosk-mcp
# NAME                            READY   STATUS    RESTARTS   AGE
# ratatosk-mcp-6f7b9c8d4-x2m5q    1/1     Running   0          30s
```

One startup log line tells you the serving address and the upstream:

```bash
kubectl logs deploy/ratatosk-mcp
# {"time":"…","level":"INFO","msg":"listening","service":"mcp","transport":"http","addr":":8080/mcp","mode":"stateful","upstream":"https://ratatosk.io","version":"1.0.0","rate_limit":"off"}
```

To check health as well:

```bash
kubectl port-forward svc/ratatosk-mcp 8080:8080 &
sleep 2   # curl gets connection refused if it runs before the forward is ready
curl -i http://localhost:8080/healthz
# HTTP/1.1 200 OK
```

When you are done, stop the background port-forward with `kill %1`.

### Connect

Point MCP clients inside the cluster at:

```
http://ratatosk-mcp.<namespace>.svc:8080/mcp
```

### Upgrade

```bash
git -C ratatosk-mcp pull
helm upgrade ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp
```

If you changed values with `--set` at install time, pass the same values
again on upgrade, or add `--reuse-values`.

## kagent integration

kagent 1.x serves a new API (`api.kagent.dev/v1alpha3`) and does not read the
0.x one (`kagent.dev/v1alpha2`), nor the other way round. The Helm chart picks
the right one by itself; the plain manifests come in one set per kagent line.
To see which line a cluster runs:

```bash
kubectl api-resources --api-group=api.kagent.dev   # lists agents: kagent 1.x
kubectl api-resources --api-group=kagent.dev       # lists agents: kagent 0.x
```

Support for kagent 0.x will be removed once kagent 1.x is generally available.

### kagent 0.x

Two equivalent routes; pick exactly one.

**A. Helm toggle**: one install brings the server, the kagent registration
(RemoteMCPServer), and the example agent `ratatosk-agent`:

```bash
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true
```

`kagent.modelConfig` defaults to `default-model-config`. Override it if your
environment differs; if you want the registration without the example agent,
set `kagent.agent.enabled=false`.

**B. Plain manifests**: the same three pieces as copy-paste files, no Helm
([details](../examples/kagent/0.x/README.md)):

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml
kubectl apply -f $BASE/0.x/ratatosk-remote-mcpserver.yaml
kubectl apply -f $BASE/0.x/ratatosk-agent.yaml
```

No clone is needed on this route. The manifests carry `namespace: kagent`.

### kagent 1.x

**First, a Harness.** kagent 1.x runs every Agent on a Harness (the runtime,
the Agent Substrate WorkerPool, and the snapshot location), and a kagent
install creates none. Check for one in the namespace you install into:

```bash
kubectl get harnesses.api.kagent.dev -n kagent
```

If there is none, create one; see kagent's
[Agent harness](https://kagent.dev/docs/kagent/1.x/agents/agent-harness/)
guide. Use the kagent runtime (`spec.kagent: {}`) for this agent; the
read-only note below says why.

**A. Helm toggle**, naming the Harness:

```bash
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true --set kagent.harness=<your Harness>
```

The chart sees the 1.x API on the cluster and renders 1.x objects. Without
`kagent.harness` it installs the server and the RemoteMCPServer but skips the
agent, and its install notes say how to add it later (`helm upgrade
--reuse-values --set kagent.harness=<name>`).

**B. Plain manifests** ([details](../examples/kagent/1.x/README.md)):

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml
kubectl apply -f $BASE/1.x/ratatosk-remote-mcpserver.yaml
curl -fsSLO $BASE/1.x/ratatosk-agent.yaml
# set spec.harnessRef.name (placeholder: your-harness) to your Harness, then:
kubectl apply -f ratatosk-agent.yaml
```

**Full Service name.** The RemoteMCPServer points at
`ratatosk-mcp.kagent.svc.cluster.local`, not `ratatosk-mcp`: kagent 1.x agents
run in a sandbox that resolves only the hostnames its egress policy names, so
a short Service name never resolves there. The chart builds the name from the
release namespace and `clusterDomain` (default `cluster.local`); in the
manifests, edit the URL if your cluster domain differs.

#### Keep the agent's cluster access read-only

kagent's tool server (`kagent-tool-server`) also carries write tools, such as
`k8s_apply_manifest`, `k8s_delete_resource` and `k8s_scale`. The example agent
names only `k8s_get_resources` and `k8s_get_resource_yaml`, and the kagent
runtime holds it to that list. A Claude Harness (`spec.claude`) cannot expose
part of a server: it hands the agent every tool on the tool server and only
records a warning in the Agent's status, which leaves the prompt's "never
modify the cluster" as the only guard. So either:

- run this agent on a kagent-runtime Harness, or
- make the tool server read-only on the kagent install:
  `--set 'kagent-tools.tools.args={--read-only}'` stops it from registering
  write tools at all. `kagent-tools.rbac.readOnly=true` also swaps its
  cluster-admin role for a read-only one, but that role covers neither nodes
  nor custom resources; add them through `kagent-tools.rbac.additionalRules`,
  or the agent loses the node versions (kubelet, container runtime) it reads.

The tool server is shared, so these settings apply to every agent that uses
it.

### Verify

On either line, `ratatosk-agent` appears in the kagent UI. On kagent 1.x,
`kubectl describe agents.api.kagent.dev ratatosk-agent -n kagent` shows whether
it is ready (use the full resource name: leftover 0.x CRDs can answer a plain
`kubectl get agent`). Ask it:

> "Anything in this cluster that needs action before we upgrade?"

The agent finds the running versions itself through kagent's built-in
read-only cluster tools (disable with `kagent.agent.k8sTools=false`). You can
also name the versions in the question. If the agent does not appear in the
UI, see [Troubleshooting](#troubleshooting).

> **Model minimum** (measured on kagent 0.x): one agent run makes six or
> more internal model calls, and the kagent Go ADK does not retry 429
> (rate-limited) responses. You therefore need a model tier that allows
> roughly 10+ requests per minute; below that, every run fails. For example,
> on the Gemini free tier (as of 2026-07) the full-flash models, capped at 5
> requests per minute, never complete a run; only the flash-lite models have
> limits high enough.

## Configuration reference

Every setting that changes server behavior is an environment variable, and
the Helm chart provides a value for each. The chart also has values that
shape the deployment itself (replica count, resources, service type); see
the [chart README](../charts/ratatosk-mcp/README.md).

| Env | Chart value | Default | What it does |
|---|---|---|---|
| `RATATOSK_URL` | `ratatoskUrl` | `https://ratatosk.io` | Upstream API (`/v2`, public, read-only). Change it to route egress through a proxy or a mirror |
| `MCP_HTTP_ADDR` | *(set automatically by the chart from `service.port`)* | *(empty = stdio)* | When set (e.g. `:8080`), serve streamable HTTP at `/mcp` instead of stdio, with `/healthz` |
| `MCP_HTTP_STATELESS` | `statelessHttp` | *(off)* | `1` serves HTTP without session state: no `Mcp-Session-Id` round trip, and required to serve current clients that use the 2026-07-28 MCP spec revision over HTTP. Also recommended for horizontal scaling to two or more replicas. Older clients work either way |
| `MCP_LOG` | `logLevel` | `info` | Accepted values `info` (default), `debug`, `warn`, `error`; unrecognized values fall back to `info` without a warning. `debug` adds per-call upstream timing; `warn` and `error` reduce output. No level records request arguments; see [Logs and the audit stream](#logs-and-the-audit-stream) |
| `MCP_AUDIT` | `auditMode` | *(off)* | `metadata` or `full`: the tool-call audit stream. See [Enabling the audit stream](#enabling-the-audit-stream) |
| `MCP_RATE_LIMIT_PER_MIN` | `rateLimitPerMin` | *(off)* | Per-caller ceiling on requests per minute (every MCP message counts, one per tool call), for a server that fronts callers you do not control. Each caller address gets its own budget, so one busy caller cannot starve the rest; over the limit answers `429` with `Retry-After`. The address is an in-memory bucket key for the length of the window. It is never logged and never sent upstream. It is read from `CF-Connecting-IP`, then `X-Forwarded-For`, then `X-Real-IP`, then the peer address; the proxy in front must set one of those **and strip whatever the client sent**, since a limiter keyed on a caller-controlled value is not a limiter. Leave off for a single-tenant install |

Running HTTP mode directly with Docker:

```bash
docker run --rm -p 8080:8080 -e MCP_HTTP_ADDR=:8080 ghcr.io/basphere/ratatosk-mcp:1.0.0
```

## Logs and the audit stream

### Operational logs

Logs are one-line JSON on stderr (stdout is reserved for the stdio
transport). At the default level (`info`) you get lifecycle lines (one
`listening` line at start, one line when a stdio session ends) plus
warn/error lines when the upstream connection is unhealthy. A normal request
logs nothing at all. Note that the stdio session end line comes in two
shapes: `"msg":"stdio session ended"` (INFO) when the client cleans up and
disconnects, `"msg":"stdio session ended with error"` (ERROR) when it drops
without cleanup. The latter records how the session ended, not a failure.
On errors, the original error message is never copied, because request URLs
can carry the running versions. The log text is instead reconstructed from
just the endpoint pattern and the kind of error:

```json
{"time":"…","level":"ERROR","msg":"upstream fetch failed","service":"mcp","upstream":"/v2/projects","kind":"connection_refused","tool":"check_stack"}
```

Raising to `MCP_LOG=debug` adds one line per upstream call with the endpoint
pattern, status code, and duration. **No level puts request arguments
(versions and the like) in the log.**

### Enabling the audit stream

The audit stream is a separate record of which client called which tool.
**It is off by default. While it is off, nothing is emitted at all:** the
stream does not exist.

Enable it with `MCP_AUDIT`:

```bash
# docker
docker run -i --rm -e MCP_AUDIT=metadata ghcr.io/basphere/ratatosk-mcp:1.0.0

# Helm
helm upgrade ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp --set auditMode=metadata
```

When on, every tool call emits one `event:"audit"` JSON line on the same
stream as the operational logs (stderr):

```json
{"argument_names":["components","detail"],"client_name":"my-agent","client_version":"1.0.0","event":"audit","level":"INFO","msg":"audit","outcome":"ok","service":"mcp","time":"2026-07-31T02:54:47.029169122Z","tool":"check_stack","transport":"stdio"}
```

When the server runs in stateful HTTP mode (the chart default), audit records
on the HTTP transport also carry a `session_id` field, the transport session
identifier (e.g. `"session_id":"T3E77BYZFDDA33SIUSORQ365ZL"`), which separates
concurrent callers by session. With `statelessHttp` (env `MCP_HTTP_STATELESS`) on,
there is no session to name, so `session_id` and the client's self-reported
`clientInfo` are absent from the record, and `trace_id` (below) is the only
per-call join key. If the audit stream must separate callers, run stateful,
or have callers send `traceparent`. stdio records like the example above have
no `session_id` either: one process serves one caller, so there is nothing
to separate.

The two modes differ as follows:

- **`metadata`**: the tool name, the outcome (`ok`, `error`, `tool_error`),
  the transport, the client's self-reported `clientInfo` (on stdio and
  stateful HTTP), and the **names** of the arguments only. Argument values
  are never recorded in this mode.
- **`full`**: all of the above plus the full argument values (the
  `arguments` field). The version list you pass to `check_stack` is
  recorded here too, so decide whether it belongs in your log system before
  enabling this mode.

The stream is produced inside your infrastructure and lands in your own log
collectors. Retention and tamper-evidence are your log platform's job; route
records with `event: audit` to a separate sink, and they can have their own
retention policy.

One limit: the server has no authentication, so a record identifies the
caller only by the client's self-reported `clientInfo` and whatever the
transport exposes. To find out which person prompted the call, look in your
agent platform's own records.

The hosted endpoint has no audit stream. It is always off, because that
endpoint retains no request content. If you have audit requirements,
self-host.

### Trace correlation (traceparent)

If your agent framework supports W3C trace context (the standard header that
carries a request's trace identity), it can send `traceparent` in
the `_meta` of a tool call:

```json
{"method":"tools/call","params":{"name":"check_stack","arguments":{"components":[…]},"_meta":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}}
```

The log lines that call produces (errors, `debug` level) and its audit
record are then stamped with a `trace_id` field, and the same `traceparent`
header is forwarded on the upstream `/v2` request, so one `trace_id` connects
agent → MCP server → upstream. This is what joins the layers the audit
record cannot: it links a prompt in your agent platform to the tool call
here. Malformed values are dropped and not forwarded; send nothing and
nothing is recorded.

## Troubleshooting

| Symptom | Check | Cause and fix |
|---|---|---|
| Tool calls return errors (`check_stack`: "could not read the project list: …"; if only some fetches fail, `fetch failed: …` in a component's `note`); the log shows `"msg":"upstream fetch failed"` with `kind: connection_refused`, `dns`, or `timeout` | `kubectl logs deploy/ratatosk-mcp` (locally: your client's MCP logs) | Egress is blocked. Allow outbound HTTPS to `ratatosk.io:443`, or set up a mirror and point `RATATOSK_URL` (chart: `ratatoskUrl`) at it |
| The log shows `"msg":"upstream rate limited"`, `status: 429` | Same as above | Upstream limit exceeded (1200 requests/minute per IP). Prefer one `check_stack` call for a whole stack over many per-project calls, and retry after a pause |
| An HTTP client gets `400 Bad Request: protocol version "2026-07-28" is only supported on stateless HTTP servers` | The `Mcp-Protocol-Version` header the client sends | The client speaks the 2026-07-28 MCP revision, which stateful HTTP mode rejects. Turn on `--set statelessHttp=true` (env: `MCP_HTTP_STATELESS=1`). The `StreamableHTTPOptions.Stateless` named in the error is the Go SDK's name for the same switch |
| `ratatosk-agent` does not appear in the kagent UI | `kubectl api-resources \| grep kagent` · `kubectl get pods -n kagent` | Installing with `kagent.enabled=true` fails on a cluster without the kagent CRDs; install kagent first. On kagent 1.x the chart skips the agent while `kagent.harness` is empty (the install notes say so), and a set Harness that does not exist shows in `kubectl describe agents.api.kagent.dev ratatosk-agent`. Applying the 0.x manifests to kagent 1.x, or the reverse, fails with "no matches for kind"; use the set for your line. The manifest route hardcodes `namespace: kagent`, so if kagent lives in another namespace, edit the manifests |
| kagent 1.x: the agent's ratatosk tool calls fail to connect | `kubectl get remotemcpservers.api.kagent.dev ratatosk-mcp -n kagent -o jsonpath='{.spec.url}'` | The URL uses a short Service name. kagent 1.x agents resolve only full names: use `http://ratatosk-mcp.<namespace>.svc.cluster.local:8080/mcp` (the chart and the 1.x manifests already do) |
| kagent 0.x only: `ImagePullBackOff` after switching the kagent agent runtime to the Go ADK | `kubectl describe pod` | A known issue in kagent 0.9.12 itself, unrelated to this chart ([#2247], fixed after 0.9.12): the Go ADK image is published only to `ghcr.io` while the controller default points at the retired `cr.kagent.dev`. Workaround: `--set controller.agentImage.registry=ghcr.io` on the kagent install |
| The kagent agent fails every run | 429 in the agent logs | The model tier's request limit is too low. See the model minimum under [kagent integration](#kagent-integration) |

[#2247]: https://github.com/kagent-dev/kagent/issues/2247

## Version pinning

- **Container image**: built automatically for every release under its
  version tag (e.g. `1.0.0`), multi-arch (`linux/amd64`, `linux/arm64`).
  `latest` tracks the newest release; pin a version tag for reproducible
  deployments.
- **Helm**: the chart is not published to a chart repository; you install
  from a clone. To pin a specific version, check out the release tag after
  cloning:

  ```bash
  git -C ratatosk-mcp checkout v1.0.0
  ```

  The image tag follows the chart's `appVersion` by default and can be
  overridden with the `image.tag` value.

## Upstream API

This server is a thin client over the public REST API. If you would rather
call it directly, `GET /v2` on ratatosk.io describes itself. No API key; rate
limited at 1200 requests per minute per IP.

## Next steps

- Per-tool parameters, example calls, and measured responses: [tools reference](tools.en.md)
- The tools table at a glance: [the tools table in the README](../README.md#tools)
- Full chart values and the kagent toggle: [chart README](../charts/ratatosk-mcp/README.md)
- kagent manifests and the example agent: [kagent example](../examples/kagent/README.md)
