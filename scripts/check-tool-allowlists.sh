#!/usr/bin/env bash
# kagent shows an agent only the tools its allowlist names, and that list lives
# in YAML nothing type-checks: the chart (rendered for kagent 0.x and 1.x) and
# the two example sets. v0.4.0 shipped list_releases invisible to both install
# paths for exactly that reason, and kagent 1.x compiles a misspelled name into
# a ready agent that simply never calls the tool. main.go is the source of
# truth; every allowlist must name exactly its tools.
set -euo pipefail

cd "$(dirname "$0")/.."

# gofmt aligns struct fields, so "Name:" can be followed by several spaces; a
# single-space pattern silently matched nothing after the 2026-08-10 rewrite.
TOOLS="$(grep -oE '^[[:space:]]*Name:[[:space:]]+"[a-z_]+"' main.go | grep -oE '"[a-z_]+"' | tr -d '"' | sort)"
[ -n "$TOOLS" ] || { echo "no tool names found in main.go: the extraction pattern is stale" >&2; exit 1; }
echo "registered: $(echo "$TOOLS" | tr '\n' ' ')"

RENDER="$(mktemp -d)"
trap 'rm -rf "$RENDER"' EXIT
./scripts/render-kagent-shapes.sh "$RENDER"

FILES=(
  examples/kagent/0.x/ratatosk-agent.yaml
  examples/kagent/1.x/ratatosk-agent.yaml
  "$RENDER/chart-0.x.yaml"
  "$RENDER/chart-1.x.yaml"
)

fail=0
for f in "${FILES[@]}"; do
  # Tool names are the only block-list items that are a bare identifier; the
  # k8s_ ones belong to kagent's own tool server.
  LISTED="$(grep -oE '^ *- [a-z0-9_]+$' "$f" | sed 's/^ *- //' | grep -v '^k8s_' | sort)"
  if [ "$LISTED" != "$TOOLS" ]; then
    echo "::error file=$f::tool allowlist disagrees with main.go"
    diff <(echo "$TOOLS") <(echo "$LISTED") || true
    fail=1
  fi
done
[ "$fail" -eq 0 ] || exit 1
echo "tool allowlists: ${#FILES[@]} manifests name exactly the registered tools"
