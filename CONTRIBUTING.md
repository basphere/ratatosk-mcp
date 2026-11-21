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

Each package's doc comment says why it exists and what it must not depend on:
`go doc ./internal/<name>`. The layout rules the compiler does not hold are
held by a test at the repository root (`go test .`), each rule with its
reason: which package must not reach which, that no test-only package is in
the binary, and that every package has a doc comment.

A test-only package is named `…test` and sits beside the package it helps
test. When it imports that package, the tests that use it are an external
test package (`package <name>_test`), and what they need of the internals is
exported to them in an `export_test.go`.

No package keeps state in package-level variables: what a package needs (the
client, the metrics, the audit stream, the limits) is passed in when it is
built. `main.buildVersion` is the exception: the release build sets it with
`-ldflags "-X main.buildVersion=<tag>"`, and it has to stay in `main` for
that. (`briefing`'s per-kind caps are a variable only so that the regression
harness can lift them in a test.)

Each source file does one job, and any non-test source file over 500 lines is
split by responsibility. The top-level test (`go test .`) enforces this limit
and fails if a source file exceeds 500 lines unless the file is listed in that
test with the reason it is not split. If adding a feature pushes a file past
500 lines, split the file in the same change; before 1.2.0, one file grew to
2,217 lines (covering argument types, range selection, briefing assembly, and
handlers for two tools) and required a separate cleanup pass to divide.

## Tests and test data

- A test sits beside the code it tests, in a `_test.go` file of the same
  folder, named for what it tests; the comment above a test names the review,
  measurement or report it replays.
- Test function names do not change: a restructuring is checked by comparing
  the list of test names before and after.
- A test goes with the package whose promise it checks: a handler's answer
  with the tools, what a client sees through the MCP SDK with the server, the
  docs against the tools at the repository root.
- A helper used by the tests of more than one package lives once, in the
  `…test` package beside the package it helps test, not copied into each.
  A helper used by one package's tests stays in that package's `_test.go`
  files.
- Test data lives in the `testdata` folder of the package whose tests read
  it. Each bundle is one folder and one row of
  [`testdata/README.md`](testdata/README.md): what it is, how and when it was
  made, which tests read it, what goes with it and when it is removed. A test
  at the repository root fails when the table and the files disagree. A
  bundle is removed with its tests, the files that go with it and its row, in
  one commit.
- The briefing package keeps the 1.0.0 briefing as code, frozen, and its
  tests compare the current briefing with it on recorded ranges. The frozen
  copy is edited only to follow a rename, and is removed with the recorded
  ranges when 2.0 ships; their row in `testdata/README.md` lists what goes
  with them.

## Release number increment rules

Release numbers follow `X.Y.Z`, and each position increments according to how
the change affects clients:

- **Major (`X`)**: removing an existing tool, parameter, or response field, or
  changing its name or meaning; changing a default value so the same call
  returns a response with a different meaning; rejecting input that was
  previously accepted.
- **Minor (`Y`)**: adding a tool, parameter, response field, or newly accepted
  input.
- **Patch (`Z`)**: fixing behavior without adding surface area, including
  values that differed from the documented meaning, server crashes, error
  messages, wording in tool descriptions and `hint`, and security updates to
  the server's own dependencies.
- **No release**: documentation changes and test changes do not require a
  release. Code cleanup does not require a release when it preserves existing
  behavior.

When a release combines multiple kinds of changes, increment only the highest
applicable position and reset lower positions to `0`. Shipping a change in
meaning as a lower increment causes clients written against an older version
to produce wrong answers without an error.

## Version numbers in the docs

- A parameter or response field added after 1.0.0 is marked in the tools
  reference with the release it arrived in, all three numbers:
  `Since 1.2.0.` (Korean `1.2.0부터.`, Japanese `1.2.0 以降。`). A minor
  release (the second number) is what adds tools, parameters and fields, so
  the third number of a `Since` mark is 0; a patch release fixes behaviour
  and adds no mark. The docs test that refuses an older release number in
  the docs lets these marks through and nothing else.
- Prose does not name the current release ("the tools 1.2 provides"): it is
  wrong at the next release. The exceptions are the install commands and
  examples that pin an image tag, which the release procedure bumps, and
  sentences about a past release (0.x read `/v1`; 1.0.0 reads `/v2`).

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
