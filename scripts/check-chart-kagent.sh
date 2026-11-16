#!/usr/bin/env bash
# The chart renders its kagent integration in two APIs and picks one from the
# cluster: kagent 1.x (api.kagent.dev/v1alpha3) when the cluster serves it,
# kagent 0.x (kagent.dev/v1alpha2) otherwise. 1.x does not read 0.x objects
# and 0.x does not know the 1.x group, so a branch that renders the wrong shape
# installs nothing that works, and says nothing. chart-smoke.sh proves the
# shapes against real CRDs on a kind cluster; this pins the branching itself
# with helm template, cheaply, on every push:
#
#   - each API gets its own apiVersion, field names and tool-binding shape;
#   - kagent 1.x with no kagent.harness skips the Agent but still registers
#     the server (1.x ships no default Harness, so this is the default case);
#   - the RemoteMCPServer URL is the full Service name (1.x agents cannot
#     resolve a short one), built from the release namespace and clusterDomain;
#   - the kagent tool server gets a namespace only when it lives elsewhere.
#
# Needs helm on PATH, or HELM set to a command (see render-kagent-shapes.sh).
set -euo pipefail

cd "$(dirname "$0")/.."
HELM="${HELM:-helm}"
V1=(--api-versions api.kagent.dev/v1alpha3)

fail=0
# expect DESCRIPTION TEXT REGEX...: every regex matches a line of TEXT.
# expect_not DESCRIPTION TEXT REGEX: no line of TEXT matches.
expect() {
  local desc="$1" text="$2"; shift 2
  local re
  for re in "$@"; do
    grep -qE -- "$re" <<<"$text" || { echo "  FAIL $desc (no line matches: $re)" >&2; fail=1; return 0; }
  done
  echo "  ok  $desc"
}
expect_not() {
  if grep -qE -- "$3" <<<"$2"; then
    echo "  FAIL $1 (found: $(grep -E -- "$3" <<<"$2" | head -1))" >&2; fail=1
  else
    echo "  ok  $1"
  fi
}

render() { $HELM template rel charts/ratatosk-mcp --namespace team-a --set kagent.enabled=true "$@"; }
# The single document of the given kind from a rendered stream.
# Reads to the end (no early exit) so helm never sees a closed pipe under pipefail.
doc() { awk -v k="kind: $1" 'BEGIN{RS="\n---\n"} !done && index($0, "\n" k "\n") {print; done=1}'; }

echo "kagent 0.x (no api.kagent.dev/v1alpha3 on the cluster)"
out="$(render)"
agent="$(doc Agent <<<"$out")"; rmcps="$(doc RemoteMCPServer <<<"$out")"
expect "Agent kagent.dev/v1alpha2" "$agent" '^apiVersion: kagent\.dev/v1alpha2$'
expect "declarative shape (systemMessage, mcpServer.toolNames, type)" "$agent" \
  '^    systemMessage: \|-$' '^        toolNames:$' '^      type: McpServer$'
expect_not "no harnessRef" "$agent" 'harnessRef'
expect "RemoteMCPServer kagent.dev/v1alpha2" "$rmcps" '^apiVersion: kagent\.dev/v1alpha2$'
expect "url is the full Service name" "$rmcps" '^  url: "http://rel-ratatosk-mcp\.team-a\.svc\.cluster\.local:8080/mcp"$'

echo "kagent 1.x, kagent.harness set"
out="$(render "${V1[@]}" --set kagent.harness=platform)"
agent="$(doc Agent <<<"$out")"; rmcps="$(doc RemoteMCPServer <<<"$out")"
expect "Agent api.kagent.dev/v1alpha3" "$agent" '^apiVersion: api\.kagent\.dev/v1alpha3$'
expect "harnessRef.name from kagent.harness" "$agent" '^  harnessRef:$' '^    name: platform$'
expect "template.inline shape (systemPrompt, modelConfig.name)" "$agent" \
  '^      systemPrompt: \|-$' '^      modelConfig:$' '^        name: default-model-config$'
expect "tools[].mcp.server bindings" "$agent" '^      - mcp:$' '^            kind: RemoteMCPServer$'
# 1.x rejects apiGroup at admission ("apiGroup must be omitted") and has none of the rest.
expect_not "no 0.x fields (apiGroup, toolNames, declarative, type, stream)" "$agent" \
  'apiGroup:|toolNames:|declarative:|type: (McpServer|Declarative)|stream:'
expect "RemoteMCPServer api.kagent.dev/v1alpha3" "$rmcps" '^apiVersion: api\.kagent\.dev/v1alpha3$'
expect "RemoteMCPServer description (required on 1.x)" "$rmcps" '^  description: '

echo "kagent 1.x, kagent.harness empty (the default)"
out="$(render "${V1[@]}")"
expect_not "Agent skipped" "$out" '^kind: Agent$'
expect "server and RemoteMCPServer still install" "$out" '^kind: RemoteMCPServer$' '^kind: Deployment$'

echo "kagent tool server namespace, clusterDomain, k8sTools"
for api in 0.x 1.x; do
  args=()
  if [ "$api" = 1.x ]; then args=("${V1[@]}" --set kagent.harness=platform); fi
  agent="$(render "${args[@]}" | doc Agent)"
  expect_not "$api: same namespace, no namespace field" "$agent" '^ +namespace:'
  agent="$(render "${args[@]}" --set kagent.agent.toolServer.namespace=team-a | doc Agent)"
  expect_not "$api: release namespace given, still omitted" "$agent" '^ +namespace:'
  agent="$(render "${args[@]}" --set kagent.agent.toolServer.namespace=kagent | doc Agent)"
  expect "$api: tool server in kagent's namespace is referenced there" "$agent" '^ +namespace: kagent$'
  agent="$(render "${args[@]}" --set kagent.agent.k8sTools=false | doc Agent)"
  expect_not "$api: k8sTools=false drops the tool server binding" "$agent" 'kagent-tool-server|k8s_get'
  rmcps="$(render "${args[@]}" --set clusterDomain=corp.example | doc RemoteMCPServer)"
  expect "$api: clusterDomain reaches the url" "$rmcps" 'svc\.corp\.example:8080/mcp"$'
done

echo "kagent.enabled=false"
out="$($HELM template rel charts/ratatosk-mcp --namespace team-a "${V1[@]}" --set kagent.harness=platform)"
expect_not "no kagent objects" "$out" 'kagent\.dev'

[ "$fail" -eq 0 ] || { echo "chart kagent shapes: FAILED" >&2; exit 1; }
echo "chart kagent shapes: both APIs render as intended"
