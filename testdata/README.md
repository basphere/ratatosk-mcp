# Test data

Test data lives next to the tests that read it, in a `testdata` folder of
their package (Go's convention: the `go` command ignores those folders). This
table lists every bundle in the module: one bundle is one folder and one row.

`TestTestdataTableMatchesTheFiles` (`testdata_test.go` at the repository
root) fails when the table and the files disagree:

- a file under a `testdata` folder that no row covers,
- a row whose folder is missing or empty,
- a row that names no reading test, or a test that is not in that folder's
  package,
- a file in "Goes with" that does not exist.

To remove a bundle, delete its folder, the tests that read it, the files it
goes with and its row in one commit. To add one, add its row in the same
commit as the folder and the test that reads it.

| Bundle | What it is | Made | Read by | Goes with | Remove when |
|---|---|---|---|---|---|
| `internal/briefing/testdata/ranges/` | Seven live `/v2/upgrade` answers (argo, cilium, containerd, envoy, istio, kubernetes, kyverno), cut to the security changes the briefing folds, settles or links, with every model-written text replaced by a short token | 2026-11-18, from ratatosk.io ([Ranges](#ranges)) | `TestRangesAnswerAs100PlusDistinctAdvisories`, `TestRangesLeaveNoSecurityChangeUnaccounted` | `internal/briefing/frozen100_test.go`, `internal/briefing/ranges_test.go` | 2.0 ships: the 1.x promise that a 1.0.0 client never gets a poorer answer ends there. Drop the `assertSameAs100` calls in `internal/briefing/distinct_advisories_test.go` too; `TestRangesLeaveNoSecurityChangeUnaccounted` (a 1.2.0 property) needs ranges of its own then, cut the same way |
| `internal/mcpserver/testdata/release-snapshot/` | `tools/list` as the latest release answers it (1.1.0, commit 2613481): names, annotations, descriptions, input schemas | 2026-11-20, from the v1.1.0 tag ([Release snapshot](#release-snapshot)) | `TestToolsListIs110PlusTheNewArguments` | none | Replaced when a release is tagged, by that release's snapshot. One is kept: the rule is additions only, so each release held to the one before chains back to 1.0.0. The test's file name and its list of added arguments move with it |
| `internal/version/testdata/` | `version-golden.json`, a verbatim copy of the ratatosk worker's version answer key, which the worker, the web card sorter and this package are all tested against | 2026-08-13, copied from the ratatosk app repository (the JSON's `_copy` block says how) | `TestGoldenNormalize`, `TestGoldenSplitLine`, `TestGoldenCompare` | none | Not removed: refreshed verbatim whenever the worker's copy changes |

## Ranges

The ranges were whole answers first; the cut keeps what the briefing
compares and drops the worker's wording:

```bash
mkdir -p /tmp/ranges
curl -s 'https://ratatosk.io/v2/upgrade/envoy?from=v1.33.0' > /tmp/ranges/envoy.json   # one per project
CUT_RANGE_FIXTURES=/tmp/ranges go test -run TestCutRangeFixtures ./internal/briefing
```

The committed ones start from argo v3.1.12, cilium v1.16.18, containerd
v2.0.0, envoy v1.33.0, istio 1.25.0, kubernetes v1.32.11 and kyverno v1.15.2
(each file names its `from` and `to`). Whole answers can also be run without
cutting them, before a release:

```bash
RANGE_FIXTURES=/tmp/ranges go test -run TestRangesAnswerAs100 -v ./internal/briefing
```

## Release snapshot

`tools/list` of the release just tagged, read over stdio:

```bash
git worktree add /tmp/mcp-release vX.Y.Z
cd /tmp/mcp-release && go build -o ratatosk-mcp .
( printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"snapshot","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  sleep 2 ) \
  | ./ratatosk-mcp 2>/dev/null \
  | jq -S --indent 1 'select(.id == 2) | .result.tools' > tools-list-X.Y.Z.json
```

Run against v1.1.0 on 2026-11-21, this gives the committed
`tools-list-1.1.0.json` as JSON value for value; that file was written by
Go's encoder, which spells `<` as `<`, and the test compares values.
