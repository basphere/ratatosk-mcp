# Ratatosk MCP server

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

[Ratatosk](https://ratatosk.io) reads the release notes of the CNCF projects
and sorts each change into seven kinds: security, breaking, deprecations,
important fixes, enhancements, dependency updates, other. Each change says who
it affects and which release-note lines it came from. This MCP server
exposes those releases to in-cluster agents.

Privacy: `check_stack` compares your running versions inside this process, so
a `check_stack` call sends only project slugs to ratatosk.io. `check_upgrade`
and `get_release` are the exception: they send the versions they name. The
upstream API is public, read-only, and needs no
credentials (rate limit 1200 req/min per IP).

## Tools

| Tool | Purpose |
| --- | --- |
| `list_projects` | The tracked projects and their slugs; resolve slugs here first |
| `check_stack` | Compare running versions against each project's releases (local comparison): before-upgrade checks, the changes that ask for a decision, version gaps |
| `check_upgrade` | One project, one hop, computed by the server: `from` and `to` are sent upstream |
| `get_release` | One release in full: before-upgrade checks, every change with its evidence |
| `list_releases` | Recent releases as one-line summaries, newest first |
| `search` | Find releases by advisory id, flag or text |

## Pick the set for your kagent

kagent 1.x serves a new API (`api.kagent.dev/v1alpha3`) and does not read the
0.x one (`kagent.dev/v1alpha2`), nor the other way round, so there is one set of
manifests per kagent line. Ask the cluster which one it serves:

```bash
kubectl api-resources --api-group=api.kagent.dev   # lists agents: kagent 1.x
kubectl api-resources --api-group=kagent.dev       # lists agents: kagent 0.x
```

| kagent | Manifests | Before you start |
| --- | --- | --- |
| 0.x (0.10.x) | [`0.x/`](0.x/README.md) | kagent installed |
| 1.x (1.0 pre-release) | [`1.x/`](1.x/README.md) | kagent installed, and a **Harness** for the agent to run on |

Both sets deploy the server with the shared
[`ratatosk-deploy.yaml`](ratatosk-deploy.yaml) (Deployment + Service, no RBAC
needed). Support for kagent 0.x will be removed once kagent 1.x is generally
available.

Or with the Helm chart from the repository, which detects the kagent line
itself (on 1.x, add `--set kagent.harness=<your Harness>`):

```bash
git clone https://github.com/basphere/ratatosk-mcp
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp -n kagent --set kagent.enabled=true
```

## Ask it

> "Anything in this cluster that needs action before we upgrade?"

The agent discovers running versions itself through kagent's built-in
read-only cluster tools (`k8s_get_resources`, `k8s_get_resource_yaml` from
`kagent-tool-server`, present in a default kagent install). You can also just
tell it what you run:

> "We run kubernetes v1.36.0, cilium v1.17.18 and envoy v1.38.3. Anything
> that needs action before we upgrade?"

The agent answers with severity-ranked changes, each backed by a quote from the
release note and a link to the source.
