#!/usr/bin/env bash
# Install rehearsal: walk the documented install paths against a real cluster and
# assert what a user would actually get.
#
# v0.4.0 shipped a chart pinned to the previous image and an agent allowlist
# missing the release's headline tool. Nothing failed — the build passed, helm
# rendered, the server ran — because no check ever installed the thing and looked.
# This is that check. It needs a throwaway cluster (kind) with kubectl and helm
# on PATH; CI runs it on every push and again after the release image is built.
#
# kagent 0.x and 1.x serve different APIs (kagent.dev/v1alpha2 and
# api.kagent.dev/v1alpha3; 1.x converts nothing), and the chart picks one from
# what the cluster serves. So the kagent paths run once per kagent line, each
# against its own pinned kagent-crds chart. Pinned on purpose: an unpinned
# `helm pull` takes the newest stable tag, which would have moved this test from
# the 0.x API to the 1.x one, unannounced, the day kagent 1.0 ships.
#
#   ./scripts/chart-smoke.sh [version]     # default: the chart's appVersion
#   KAGENT_CRDS_VERSIONS="0.10.4" ./scripts/chart-smoke.sh   # one kagent line only
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

CHART=charts/ratatosk-mcp
IMAGE_REPO=ghcr.io/basphere/ratatosk-mcp
VERSION="${1:-$(sed -n 's/^appVersion: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' $CHART/Chart.yaml)}"
NS=kagent
PORT=18099
# One kagent-crds chart per supported kagent line: 0.x, then 1.x. Drop the 0.x
# entry together with examples/kagent/0.x once kagent 1.x is GA.
KAGENT_CRDS_VERSIONS="${KAGENT_CRDS_VERSIONS:-0.10.4 1.0.0-rc1}"
# The Harness the 1.x Agent names. Nothing reconciles it here (no kagent
# controller), so it only has to be a valid name; the Agent object is what we test.
HARNESS=smoke

# main.go is the source of truth for what tools exist.
# The spacing after "Name:" is gofmt's business, not ours: it aligns struct
# fields, so a single-space pattern silently matched nothing the first time a
# field longer than "Name" joined the literal. With set -e that exits 1 before
# printing anything, which reads as an install failure rather than a broken
# regex (v0.7.1, 2026-08-10). Match any run of whitespace, and refuse to
# continue on an empty list instead of rehearsing against nothing.
TOOLS="$(grep -oE '^[[:space:]]*Name:[[:space:]]+"[a-z_]+"' main.go | grep -oE '"[a-z_]+"' | tr -d '"' | sort)"
[ -n "$TOOLS" ] || { echo "  ✗ no tool names found in main.go — the extraction pattern is stale, not the build" >&2; exit 1; }

pass() { echo "  ✓ $*"; }
fail() { echo "  ✗ $*" >&2; exit 1; }
step() { echo; echo "── $*"; }

WORK="$(mktemp -d)"
cleanup() { [ -n "${PF_PID:-}" ] && kill "$PF_PID" 2>/dev/null || true; rm -rf "$WORK"; }
trap cleanup EXIT

echo "rehearsing ratatosk-mcp $VERSION (tools: $(echo "$TOOLS" | tr '\n' ' ')) on kagent-crds $KAGENT_CRDS_VERSIONS"

# The release bump commit races the release workflow: this script starts the
# moment the bump lands on main, while the new appVersion image is still
# building — the rollout below then times out and reads as a build failure
# (observed v0.5.0 and v0.8.0). Wait for the manifest instead. Every non-release
# push targets an already-published tag and passes on the first probe.
step "image ${IMAGE_REPO}:${VERSION} on the registry"
for i in $(seq 1 32); do
  if docker manifest inspect "${IMAGE_REPO}:${VERSION}" >/dev/null 2>&1; then
    pass "manifest present (probe $i)"
    break
  fi
  [ "$i" -eq 32 ] && fail "image not on GHCR after 8m of waiting — the release build is missing or failed, not slow"
  sleep 15
done

# This script wipes namespace kagent and every kagent CRD between phases, which
# would destroy a real kagent install. Refuse anything that is not a throwaway
# kind cluster unless the caller says otherwise out loud.
CONTEXT="$(kubectl config current-context 2>/dev/null || echo none)"
case "$CONTEXT" in
  kind-*) ;;
  *) [ "${RATATOSK_SMOKE_ALLOW_ANY_CLUSTER:-}" = "1" ] ||
       fail "context '$CONTEXT' is not a kind cluster; this script deletes namespace $NS and the kagent CRDs. Set RATATOSK_SMOKE_ALLOW_ANY_CLUSTER=1 to override." ;;
esac

# Each phase installs the same names — via helm, then via kubectl — so it starts
# from an empty namespace and from no kagent CRDs at all. Without the first, a
# rerun fails on ownership metadata and that noise hides whatever the run was
# meant to catch. Without the second, CRDs left by the 1.x phase would make the
# chart render the 1.x shape in a later 0.x phase: the API the chart picks is
# decided by which kagent CRDs exist.
reset_kagent() {
  kubectl delete ns "$NS" --ignore-not-found --wait --timeout=180s >/dev/null
  local crds
  crds="$(kubectl get crd -o name | grep -E '\.kagent\.dev$' || true)"
  if [ -n "$crds" ]; then
    # shellcheck disable=SC2086
    kubectl delete $crds --wait --timeout=180s >/dev/null
  fi
}

# The Agent the install produced must bind every tool main.go registers.
assert_agent() {
  local how="$1" listed
  listed="$(kubectl -n $NS get "agents.$GROUP" ratatosk-agent -o jsonpath="$AGENT_TOOLS" | tr ' ' '\n' | sort)"
  [ "$listed" = "$TOOLS" ] || fail "$how agent binds [$(echo "$listed" | tr '\n' ' ')], expected [$(echo "$TOOLS" | tr '\n' ' ')]"
  pass "$how agent ($GROUP) binds every registered tool"
}

assert_deployed() {
  local how="$1" img url
  img="$(kubectl -n $NS get deploy ratatosk-mcp -o jsonpath='{.spec.template.spec.containers[0].image}')"
  [ "$img" = "$IMAGE_REPO:$VERSION" ] || fail "$how deployed $img, expected $IMAGE_REPO:$VERSION"
  pass "$how deploys $img"
  kubectl -n $NS rollout status deploy/ratatosk-mcp --timeout=180s >/dev/null || fail "$how rollout failed"
  pass "$how pod is running (PSS restricted namespace)"

  # kagent 1.x agents resolve only full Service names; 0.x gets the same URL.
  url="$(kubectl -n $NS get "remotemcpservers.$GROUP" ratatosk-mcp -o jsonpath='{.spec.url}')"
  [ "$url" = "http://ratatosk-mcp.$NS.svc.cluster.local:8080/mcp" ] || fail "$how registered $url, expected the full Service name"
  pass "$how registers $url"

  assert_agent "$how"
}

first=1
for KV in $KAGENT_CRDS_VERSIONS; do
  case "$KV" in
    0.*) LINE=0.x; GROUP=kagent.dev
         AGENT_TOOLS='{.spec.declarative.tools[0].mcpServer.toolNames[*]}'
         HELM_AGENT=() ;;
    *)   LINE=1.x; GROUP=api.kagent.dev
         AGENT_TOOLS='{.spec.template.inline.tools[0].mcp.tools[*]}'
         HELM_AGENT=(--set "kagent.harness=$HARNESS") ;;
  esac

  step "kagent $LINE: fresh namespace and kagent-crds $KV ($CONTEXT)"
  reset_kagent
  helm pull oci://ghcr.io/kagent-dev/kagent/helm/kagent-crds --version "$KV" --destination "$WORK" >/dev/null 2>&1 \
    || fail "could not pull kagent-crds $KV"
  helm upgrade --install kagent-crds "$WORK/kagent-crds-$KV.tgz" -n "$NS" --create-namespace >/dev/null
  # Established before helm runs: the chart reads the served APIs from discovery.
  kubectl wait --for condition=established --timeout=60s "crd/agents.$GROUP" "crd/remotemcpservers.$GROUP" >/dev/null
  pass "CRDs agents.$GROUP and remotemcpservers.$GROUP established"

  # The docs claim the chart runs clean under PSS restricted — enforce it so the
  # claim is tested, not trusted.
  kubectl label ns "$NS" pod-security.kubernetes.io/enforce=restricted --overwrite >/dev/null

  step "kagent $LINE path A — helm with kagent.enabled=true (docs: install.*.md)"
  helm upgrade --install ratatosk-mcp "$CHART" -n "$NS" --set kagent.enabled=true ${HELM_AGENT[@]+"${HELM_AGENT[@]}"} >/dev/null
  assert_deployed "helm"

  if [ "$LINE" = 1.x ]; then
    # kagent 1.x ships no Harness, so an install without kagent.harness is the
    # common first try: it must keep the server and registration, and only skip
    # the Agent.
    helm upgrade ratatosk-mcp "$CHART" -n "$NS" --set kagent.enabled=true >/dev/null
    kubectl -n $NS get "agents.$GROUP" ratatosk-agent >/dev/null 2>&1 && fail "kagent.harness empty, yet the Agent is still there"
    kubectl -n $NS get "remotemcpservers.$GROUP" ratatosk-mcp >/dev/null || fail "kagent.harness empty removed the RemoteMCPServer too"
    pass "helm without kagent.harness: Agent skipped, server and RemoteMCPServer kept"
  fi

  if [ "$first" = 1 ]; then
    first=0
    step "the served endpoint answers"
    kubectl -n $NS port-forward svc/ratatosk-mcp $PORT:8080 >/dev/null 2>&1 &
    PF_PID=$!
    for _ in $(seq 1 20); do curl -sf "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break; sleep 1; done
    curl -sf -o /dev/null "http://127.0.0.1:$PORT/healthz" || fail "/healthz did not answer"
    pass "/healthz 200"

    SESSION="$(curl -s -D- -o "$WORK/mcp-init.json" -X POST "http://127.0.0.1:$PORT/mcp" \
      -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
      -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}' \
      | grep -i '^mcp-session-id' | tr -d '\r' | cut -d' ' -f2)"
    [ -n "$SESSION" ] || fail "no MCP session id"

    curl -s -X POST "http://127.0.0.1:$PORT/mcp" -H 'Content-Type: application/json' \
      -H 'Accept: application/json, text/event-stream' -H "Mcp-Session-Id: $SESSION" \
      -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' > "$WORK/mcp-tools.json"

    python3 - "$VERSION" "$WORK" <<'PY'
import json, sys
want_version, work = sys.argv[1], sys.argv[2]
def payload(path):
    for line in open(path):
        line = line.strip()
        if line.startswith("data: "):
            line = line[6:]
        if line.startswith("{"):
            return json.loads(line)
    raise SystemExit(f"  ✗ no JSON-RPC payload in {path}")
served = payload(f"{work}/mcp-init.json")["result"]["serverInfo"]["version"]
if served != want_version:
    raise SystemExit(f"  ✗ served version {served}, expected {want_version}")
print(f"  ✓ serverInfo reports {served}")
names = sorted(t["name"] for t in payload(f"{work}/mcp-tools.json")["result"]["tools"])
open(f"{work}/mcp-served-tools", "w").write("\n".join(names))
print(f"  ✓ tools/list serves {len(names)}: {' '.join(names)}")
PY
    [ "$(cat "$WORK/mcp-served-tools")" = "$TOOLS" ] || fail "served tools differ from the ones registered in main.go"
    pass "served tools match main.go"

    kill "$PF_PID" 2>/dev/null || true; PF_PID=""
  fi

  step "kagent $LINE path B — plain manifests (docs: install.*.md, examples/kagent/$LINE)"
  helm uninstall ratatosk-mcp -n "$NS" >/dev/null
  kubectl wait --for=delete pod -l app.kubernetes.io/name=ratatosk-mcp -n "$NS" --timeout=120s >/dev/null 2>&1 || true
  kubectl apply -f examples/kagent/ratatosk-deploy.yaml >/dev/null
  for f in ratatosk-remote-mcpserver ratatosk-agent; do
    kubectl apply -f "examples/kagent/$LINE/$f.yaml" >/dev/null
  done
  assert_deployed "manifests"
done

step "path C — local stdio (docs: install.*.md)"
if command -v docker >/dev/null 2>&1; then
  { printf '%s\n' \
      '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}' \
      '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
      '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; sleep 5; } \
    | docker run -i --rm "$IMAGE_REPO:$VERSION" > "$WORK/stdio.out" 2>/dev/null
  python3 - "$VERSION" "$WORK" <<'PY'
import json, sys
want, work = sys.argv[1], sys.argv[2]
got_version = got_tools = None
for line in open(f"{work}/stdio.out"):
    try: msg = json.loads(line)
    except ValueError: continue
    if msg.get("id") == 1: got_version = msg["result"]["serverInfo"]["version"]
    if msg.get("id") == 2: got_tools = sorted(t["name"] for t in msg["result"]["tools"])
if got_version != want:
    raise SystemExit(f"  ✗ stdio served {got_version}, expected {want}")
open(f"{work}/stdio-tools", "w").write("\n".join(got_tools or []))
print(f"  ✓ stdio serves {got_version} with {len(got_tools or [])} tools")
PY
  [ "$(cat "$WORK/stdio-tools")" = "$TOOLS" ] || fail "stdio tools differ from main.go"
  pass "stdio tools match main.go"
else
  echo "  – docker unavailable, skipped"
fi

echo
echo "all documented install paths serve $VERSION with every registered tool, on kagent-crds $KAGENT_CRDS_VERSIONS"
