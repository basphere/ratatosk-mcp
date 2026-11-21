# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately via GitHub:
**[Report a vulnerability](https://github.com/basphere/ratatosk-mcp/security/advisories/new)** —
do not open a public issue for security problems.

You can expect an initial response within a few days. Fixes ship as a patch
release; every release is built and published automatically from a tag.

## Scope

This repository contains the MCP server only. It is a read-only client of the
public ratatosk.io API with no credentials and no cluster permissions. The
`check_stack` tool compares versions inside the server process: on a
self-hosted server, running versions passed to `check_stack` never leave that
process, whereas on the hosted endpoint, the process runs on Ratatosk's
server, so those versions are sent to Ratatosk, used only in memory, and
omitted from logs. Reports about the ratatosk.io service itself are also
welcome through the same channel.

## Supported versions

Only the latest release is supported. Upgrade before reporting if possible.
