#!/usr/bin/env bash
# Renders the chart's kagent integration in both API shapes, for the static
# checks that read rendered manifests (check-tool-allowlists.sh,
# check-agent-prompt.sh, check-chart-kagent.sh). The chart picks kagent 0.x
# (kagent.dev/v1alpha2) or 1.x (api.kagent.dev/v1alpha3) from what the cluster
# serves; helm template has no cluster, so --api-versions stands in for a 1.x
# one. Checking the template source instead would pass a branch that never
# renders, which is how a prompt rule can vanish from one shape unnoticed.
#
#   ./scripts/render-kagent-shapes.sh OUTDIR
#   HELM="docker run --rm -v $PWD:/apps -w /apps alpine/helm:3" ./scripts/...
set -euo pipefail

cd "$(dirname "$0")/.."
OUT="${1:?usage: render-kagent-shapes.sh OUTDIR}"
HELM="${HELM:-helm}"
mkdir -p "$OUT"

# Defaults otherwise: k8sTools on, so every rule the prompt carries renders.
$HELM template ratatosk-mcp charts/ratatosk-mcp --namespace kagent \
  --set kagent.enabled=true > "$OUT/chart-0.x.yaml"
$HELM template ratatosk-mcp charts/ratatosk-mcp --namespace kagent \
  --set kagent.enabled=true --set kagent.harness=smoke \
  --api-versions api.kagent.dev/v1alpha3 > "$OUT/chart-1.x.yaml"
