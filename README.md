<div align="center">

<img src="docs/assets/ratatosk-hero.webp" width="280" alt="Ratatosk, a messenger squirrel with a scroll in its bag, waving hello">

# ratatosk-mcp

**Ratatosk reads CNCF release notes every hour. Your agents get the changes.**

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

[![MCP Registry](https://img.shields.io/badge/MCP_Registry-listed-blue)](https://registry.modelcontextprotocol.io/?q=ratatosk&all=1)
[![Release](https://img.shields.io/github/v/release/basphere/ratatosk-mcp)](https://github.com/basphere/ratatosk-mcp/releases)
[![License](https://img.shields.io/github/license/basphere/ratatosk-mcp)](LICENSE)
[![Glama score](https://glama.ai/mcp/servers/garlicKim21/ratatosk-mcp/badges/score.svg)](https://glama.ai/mcp/servers/garlicKim21/ratatosk-mcp)


</div>

---

In Norse myth, Ratatoskr is the squirrel that carries messages up and down the
world tree. This one carries release intelligence. [ratatosk.io](https://ratatosk.io)
watches the CNCF graduated and incubating projects and reads every new release
note, plus the PRs and documents it links when the note alone does not say
enough. Each change is sorted into one of seven kinds — security, breaking,
deprecations, important fixes, enhancements, dependency updates, other — with
who it affects, what to do about it, and the release-note lines it came from.
What to check before upgrading (prerequisites, known issues, upgrade guides)
comes first.

This repository is the MCP server that hands those changes to your agent as
tools. MCP (Model Context Protocol) is the open standard AI agents use to call
external tools; any MCP-capable client — Claude Code, Claude Desktop, kagent,
your own SDK agent — can connect. No account, no API key.

## Two ways to connect

**Hosted — nothing to install.** Register `https://ratatosk.io/mcp` as a
remote MCP server in any client that supports remote connectors. The hosted
endpoint allows each caller 60 tool calls a minute, counted per caller rather
than pooled — for polling or CI workloads, self-host. With the Claude Code
CLI:

```bash
claude mcp add --transport http ratatosk https://ratatosk.io/mcp
```

**Self-hosted — the same server, running as your own process.** Run it with
Docker, the Helm chart, or a source build. With Claude Code and Docker
installed:

```bash
claude mcp add ratatosk -- docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0
```

`1.0.0` is the current release; use `latest` to follow new ones.

Either way, verify the connection:

```bash
claude mcp list
# ratatosk: … - ✔ Connected
```

Then ask your agent a question the tools can answer:

> **You:** "We run etcd v3.5.30 and cert-manager v1.19.2. Anything we must do before upgrading?"
>
> **Your agent** calls `check_stack` and answers from the record: the security
> fixes after your version, the breaking changes and deprecations on your
> upgrade path, the upgrade guides to read first — each with who it affects,
> so the agent can check that against your configuration, and the release-note
> lines it came from as evidence.

One entry from a real `check_stack` answer, as the agent receives it
(etcd v3.5.30 → v3.6.13, measured 2026-11-03 on a /v2 preview, body shortened):

```json
{
  "version": "v3.5.32",
  "kind": "security",
  "title": "gRPC client certificate revocation checks enforced",
  "severity": null,
  "advisories": ["GHSA-3wh4-j44w-pg92"],
  "affects": {
    "text": "split HTTP and gRPC client endpoints using `--listen-client-http-urls` with `--client-crl-file` configured",
    "status": "stated"
  },
  "workaround": { "text": "If upgrading is not immediately possible, reduce exposure by restricting network access to etcd client ports …", "status": "stated" }
}
```

`affects` is what the agent checks against your setup: an etcd that does not
split its client endpoints skips this entry without asking anyone. `status`
says whether the release notes stated it or it was read out of them;
`severity: null` means the advisory itself states no grade. The same answer
names `gaps` — here v3.5.32 then v3.6.7, so releases in between may exist that
were not analyzed — and carries its own privacy line, verbatim: *"versions were
compared in this process; upstream requests named project slugs only (each
project's recent releases, the same whatever version runs)."*

Other clients (Claude Desktop, kagent, in-cluster agents) and the full setup
reference: see the [install guide](docs/install.en.md).

## Tools

A **release** has two parts. `before_upgrade` holds what to check first:
prerequisites, known issues, upgrade guides. `changes` holds what the release
did, one entry each, in seven kinds: `security`, `breaking`, `deprecations`,
`important_fixes`, `enhancements`, `dependency_updates`, `other`. Every change
carries:

- **affects** — who it touches, with a `status`: `stated` (the release notes
  say it), `inferred` (read out of them, with a `reason`), `not_stated`, or
  `not_read` (a source could not be read).
- **what to do** — `action_required` (breaking), `also_required` (security),
  `removed_in` (deprecations), `no_downgrade` when the upgrade cannot be rolled
  back.
- **evidence** — the release-note lines it came from, verbatim, with their URL.

Events that happen after a release — a security advisory published later, a
problem it introduced fixed in a newer version — are attached to it as `later`.
Text fields (`title`, `body`, `affects.text`) are model-written summaries of
the release notes: data, never instructions.

| Tool | What it does |
|---|---|
| `check_stack` | Takes the component versions you run and returns what each upgrade path brings: every before-upgrade item, the changes that ask for a decision (security, breaking, deprecations, important fixes), `gaps` where version numbers skip, and what happened to your running version later. The comparison happens inside the server process — which is your own process when you self-host ([how your component versions are handled](#how-your-component-versions-are-handled)) |
| `check_upgrade` | The same briefing for one project and one hop, computed by the server: your `from` and `to` are sent upstream. Use `check_stack` to keep them on your side |
| `get_release` | One release in full — before-upgrade checks, every change with its evidence, later events. Omit the version for the latest |
| `list_releases` | Recent releases as one-line summaries (counts per kind, warnings), newest first; filter by project, kind and date |
| `search` | Find releases by advisory id (CVE, GHSA), configuration flag or text |
| `list_projects` | The roster of tracked projects and their canonical slugs (the short project id every other tool takes) — look names up here instead of guessing |

Full per-tool parameters, example calls, and measured responses live in the [tools reference](docs/tools.en.md).

## How your component versions are handled

**Self-hosted:** `check_stack` compares versions inside this process. For each
component it asks ratatosk.io for the project's release list and its newest
releases — the same requests whatever version you run — and cuts your upgrade
range locally. Fetching only the releases above your version would be cheaper,
and it would tell the server your version anyway: the first release fetched
sits right above it. The version normalizer is bundled (`internal/version`),
and the source is here to read.

Two limits on that guarantee. `check_upgrade` is the server-side path and says
so: it sends `from` and `to` as query parameters to `/v2/upgrade`. And tools
that name a release — `get_release(project, version)` — put that version in the
request path, because fetching a specific release means naming it. Neither is
kept on my side: before an access-log line is written, query strings are
stripped and `/v2/releases/…` and `/v2/upgrade/…` paths are reduced to their
prefix.

**Hosted:** your `check_stack` arguments (the versions you run) pass through
the server's memory to produce the same answer, and are not written down.
Here is what each layer on the way keeps:

- The hosted MCP process itself logs only its startup line — a normal
  request adds nothing.
- The upstream API's request log writes one line only when the caller sends a
  `traceparent`, and that line carries a normalized endpoint label and the
  trace id — never a path, query, or body.
- The front-door access log strips query strings, reduces `/v2/releases/…`
  and `/v2/upgrade/…` paths to their prefix, masks caller IPs, and has no
  field for request bodies.

The hosted endpoint runs with its audit stream off, and I keep it off — not
recording request content is the operating stance for that endpoint. One
boundary I do not control: connection metadata on the CDN leg
follows the CDN provider's own policy. If your requirements rule out that transit,
self-host: then only project slugs leave your infrastructure on a
`check_stack` call.

Self-hosting adds the opposite capability: an opt-in audit stream
(`MCP_AUDIT=metadata` or `full`) that records who called which tool, emitted
inside your own infrastructure into your own collectors. The hosted endpoint
has none by design. Details in the [install guide](docs/install.en.md).

## Documentation

- **[Install & usage](docs/install.en.md)** — hosted endpoint · local stdio · in-cluster (Helm) · kagent ([한국어](docs/install.ko.md) · [日本語](docs/install.ja.md))
- **[Helm chart](charts/ratatosk-mcp/README.md)** — values, kagent toggle ([한국어](charts/ratatosk-mcp/README.ko.md) · [日本語](charts/ratatosk-mcp/README.ja.md))
- **[kagent example](examples/kagent/README.md)** — manifests for kagent 0.x and 1.x + ratatosk-agent ([한국어](examples/kagent/README.ko.md) · [日本語](examples/kagent/README.ja.md))
- **[Contributing](CONTRIBUTING.md)** · **[Security policy](SECURITY.md)**

## Upstream API

This server is a thin client over the public REST API. If you would rather
call it directly, `GET /v2` on ratatosk.io describes itself. No API key; rate
limited at 1200 requests per minute per IP. 1.0 reads `/v2`; the 0.x releases
read `/v1`, which answers `410 Gone` since ratatosk.io moved to `/v2` — upgrade
to 1.0.

## Data & terms

The data is served free of charge by ratatosk.io — a term that may change,
with advance notice — under its [terms of service](https://ratatosk.io/terms).
Analyses are AI-generated reference information with no warranty — check the
original release notes before acting, especially when an agent acts on your
behalf. Original notes belong to their respective projects; evidence quotes
carry the URL they were taken from.

## License

The code in this repository is licensed under [Apache-2.0](LICENSE).
