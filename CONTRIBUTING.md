# Contributing

Issues and pull requests are welcome — bug reports, tool UX feedback from
real agent usage, and documentation fixes especially.

## Build & test

No local Go toolchain needed; everything runs through Docker:

```bash
docker run --rm -v $PWD:/src -w /src -e GOFLAGS=-buildvcs=false golang:1.26 \
  sh -c "go build ./... && go test ./..."
```

The Helm chart lints with:

```bash
docker run --rm -v $PWD/charts:/charts alpine/helm:3 lint /charts/ratatosk-mcp
```

## Code layout

`main.go` is the command: it reads the configuration, builds the server and
serves it over stdio or streamable HTTP. The rest is under `internal/`, which
nothing outside this module can import; the repository offers no Go API.

| Package | What it does |
|---|---|
| `internal/config` | Reads the environment variables once into typed values, and decides what an unset or malformed value means |
| `internal/logging` | The JSON logger, what a request carries through the process (tool, trace context, caller class), and the rule that no request argument reaches a log line |
| `internal/audit` | The opt-in audit stream (`MCP_AUDIT`) |
| `internal/metrics` | The request counters and their `/metrics` exposition, counted at the HTTP and the MCP layer |
| `internal/httpfront` | What an HTTP request passes before the MCP server: caller class, rate limit, batch refusal, in-flight cap, `/healthz`, server timeouts |
| `internal/mcpserver` | Builds the MCP server: instructions, the tools, and the audit, deadline and metrics middleware in their order |
| `internal/tools` | The six tools, one file each: definition (name, description, input schema), arguments, handler |
| `internal/upstream` | The read-only client of the ratatosk `/v2` API and its response cache, the only package that talks to the network |
| `internal/upstream/upstreamtest` | Tests only: a fake `/v2` API, and a client of it built as the server builds one |
| `internal/metrics/metricstest` | Tests only: reads the `/metrics` exposition, builds tools/call bodies |
| `internal/httpfront/httpfronttest` | Tests only: the refusal set-up (a limiter that refuses, the bodies refusals are measured with) |
| `internal/mcpserver/mcpservertest` | Tests only: connects a client to an MCP server in memory |
| `internal/briefing` | Turns the release documents of an upgrade range into the briefing check_stack and check_upgrade answer |
| `internal/releaseline` | Release lines, version ranges, version gaps and image build suffixes |
| `internal/advisory` | Counts the distinct advisories of a range |
| `internal/release` | The `/v2` release shapes |
| `internal/version` | Version keys and release lines, a copy of the ratatosk worker's package |

Imports point one way:

- `main` imports `config`, `logging`, `audit`, `metrics`, `upstream`,
  `httpfront` and `mcpserver`.
- `mcpserver` imports `tools`, `audit`, `metrics` and `upstream`.
  `httpfront` imports `metrics` and `logging`.
- `tools` imports `briefing`, `releaseline`, `release`, `upstream`, `logging`
  and `version`.
- `briefing` imports `advisory`, `releaseline`, `release` and `version`, and
  never `upstream`: a briefing is computed from the documents it is given, so
  what check_stack sends upstream cannot depend on the running version.
- `upstream` imports `metrics`, `logging` and `release`. `metrics` and `audit`
  import `logging`. `releaseline` imports `release` and `version`;
  `advisory` imports `release`.
- `config`, `logging`, `release` and `version` import nothing from this
  module.
- The four `…test` packages are imported by `_test.go` files only, so none
  of them is in the binary (`go list -deps .`). Each sits beside the package
  it helps test. `metricstest` and `mcpservertest` import nothing from this
  module, so the metrics and server packages' own tests can use them;
  `httpfronttest` imports `httpfront` and `upstreamtest` imports `upstream`,
  so the tests of those two packages that use them are external test
  packages (`package httpfront_test`, `package upstream_test`), with what
  they need of the internals in `export_test.go`.

Each package starts with a comment that says why it exists and what it must
not depend on. No package keeps state in package-level variables: what a
package needs (the client, the metrics, the audit stream, the limits) is
passed in when it is built. `main.buildVersion` is the exception: the release
build sets it with `-ldflags "-X main.buildVersion=<tag>"`, and it has to
stay in `main` for that. (`briefing`'s per-kind caps are a variable only so
that the regression harness can lift them in a test.)

## Tests and test data

- A test sits beside the code it tests, in a `_test.go` file of the same
  folder. The file name says what is tested (`check_stack_test.go`,
  `ranges_test.go`); the comment above a test names the review, measurement
  or report it replays.
- Test function names do not change: a restructuring is checked by comparing
  the list of test names before and after.
- Handler-level tests are in `internal/tools`. Tests that call through the MCP
  SDK (schema validation, descriptions as a client lists them, middleware)
  are in `internal/mcpserver`. Counting through the HTTP chain and the server
  is in `internal/metrics/counting_test.go`. `docs_test.go` and
  `docs_live_test.go` at the root check the docs against the tools as a
  client lists them.
- A helper used by the tests of more than one package lives once, in the
  `…test` package beside the package it helps test, not copied into each.
  A helper used by one package's tests stays in that package's `_test.go`
  files.
- Test data lives in the `testdata` folder of the package whose tests read
  it. Each bundle is one folder and one row of
  [`testdata/README.md`](testdata/README.md): what it is, how and when it was
  made, which tests read it, what goes with it and when it is removed.
  `TestTestdataTableMatchesTheFiles` fails when the table and the files
  disagree. A bundle is removed with its tests, the files that go with it and
  its row, in one commit.
- `internal/briefing/frozen100_test.go` is the 1.0.0 briefing kept as code,
  and `internal/briefing/ranges_test.go` compares the current briefing with it
  on recorded ranges. It is edited only to follow a rename, and removed with
  `internal/briefing/testdata/ranges` when 2.0 ships.

## Pull requests

- Keep the read-only, no-credential design: tools must never require secrets,
  and nothing from the user's environment may be sent upstream beyond project
  slugs (see the check_stack privacy contract in the README).
- If you change tool descriptions or add a tool, remember they are read by
  agent LLMs at runtime — descriptions are behavioral instructions, not just
  docs.
- Releases are automated: a version tag builds the multi-arch image, creates
  the GitHub Release, and publishes to the official MCP registry. Maintainers
  handle tagging; PRs should not bump versions.

## Conduct

Be kind. Assume good intent.
