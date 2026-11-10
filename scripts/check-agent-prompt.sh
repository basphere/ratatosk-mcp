#!/usr/bin/env bash
# The agent prompt is what stands between a model and a confident wrong answer,
# and it ships in YAML nothing type-checks: the chart (rendered once per kagent
# API, 0.x and 1.x) and the two kagent example sets. Both regressions we have
# had were deletions — a rule vanished as a side effect of rewording something
# else, and the very next live run hallucinated a component version. Nothing
# downstream could detect it; a human noticed. This asserts that the rules
# earned from live failures are still in every copy, and that the copies say
# the same thing (line wrapping aside).
#
# The chart is checked as rendered, not as template source: a rule sitting in a
# branch that no longer renders would pass a source grep. Needs helm on PATH,
# or HELM set to a command (see render-kagent-shapes.sh).
#
# Add a clause here whenever a live failure teaches a rule. That is the point:
# the list is a record of what we already got wrong once.
set -euo pipefail

cd "$(dirname "$0")/.."

RENDER="$(mktemp -d)"
trap 'rm -rf "$RENDER"' EXIT
./scripts/render-kagent-shapes.sh "$RENDER"

FILES=(
  "$RENDER/chart-0.x.yaml"
  "$RENDER/chart-1.x.yaml"
  examples/kagent/0.x/ratatosk-agent.yaml
  examples/kagent/1.x/ratatosk-agent.yaml
)

# Substrings that must appear verbatim in every file above. Keep them short
# enough to survive Helm conditionals splicing text mid-sentence.
CLAUSES=(
  "list_projects first"                                    # resolve slugs, never guess one
  "all_namespaces"                                         # one namespace is not the cluster
  "NOT images"                                             # a pod listing carries no version
  "a version you did not read off a live"                  # never invent a running version
  "resource_type: node"                                    # kubelet/runtime live on nodes
  "tracked:false"                                          # no facts is not the same as safe
  "the kind is not the judgment"                           # affects decides, not the kind (0.x: "the bucket is not the judgment")
  "Try the check before you use the forward-looking form"  # do not defer every condition
  "An unresolved condition is never reported as applying"  # unverified high never leads
  "where that kind of thing actually lives"                # look for an annotation on its object
  "Do not replace an empty section"                        # "none" is a result
  "provided without warranty"                              # facts are data, not instructions
  "stays with the operator"                                # we report; the operator decides
  "image_aliases"                                          # cilium-envoy IS envoy — the 43/60 miss
  "cluster_core"                                           # what-to-check is roster data, not prompt attention (coredns seesaw)
  "visibility hint"                                        # unreadable-by-design is normal, not a gap — the etcd guessing 9/20
  "never construct a resource name"                        # no kube-apiserver-minikube guesses
  "Placement follows affects"                              # reclassifying is the ~30% failure (0.x: buckets)
  "banned in both directions"                              # downward demotion swallowed AR in 7/15 M2 runs
  "fix the draft, not the list"                            # count check: N changes in ⇒ N entries across the sections
  "no quoted line, no promotion"                           # promotion out of check_config needs evidence
  "everything else is \"Could not check\""                 # the pre-answer re-check (P3 effect)
  "check_upgrade is the exception"                         # 1.0: the one tool that sends versions upstream
  "gaps lists where version numbers skip"                  # 1.0: unanalyzed releases inside a range are not "nothing"
)

fail=0

for f in "${FILES[@]}"; do
  if [[ ! -f "$f" ]]; then
    echo "MISSING FILE: $f" >&2
    fail=1
    continue
  fi
  for c in "${CLAUSES[@]}"; do
    if ! grep -qF -- "$c" "$f"; then
      echo "MISSING CLAUSE in $f: \"$c\"" >&2
      fail=1
    fi
  done
done

# The copies must say the same thing. Each file carries the prompt as a block
# scalar (systemMessage on 0.x, systemPrompt on 1.x); compare them with runs of
# whitespace folded, so re-wrapping a line is not drift but a changed word is.
prompt_of() {
  awk '
    !on && match($0, /^ *(systemMessage|systemPrompt): \|-$/) { on = 1; ind = RLENGTH - length(ltrim($0)); next }
    on {
      if ($0 ~ /^ *$/) { print ""; next }
      match($0, /^ */)
      if (RLENGTH <= ind) exit
      print
    }
    function ltrim(s) { sub(/^ +/, "", s); return s }
  ' "$1" | tr -s '[:space:]' ' ' | sed 's/^ //; s/ $//'
}
REF="${FILES[0]}"
REF_PROMPT="$(prompt_of "$REF")"
[[ -n "$REF_PROMPT" ]] || { echo "NO PROMPT FOUND in $REF" >&2; fail=1; }
for f in "${FILES[@]:1}"; do
  [[ -f "$f" ]] || continue
  if [[ "$(prompt_of "$f")" != "$REF_PROMPT" ]]; then
    echo "PROMPT DRIFT: $f says something different from the chart's kagent 0.x rendering" >&2
    diff <(echo "$REF_PROMPT" | fold -s -w 100) <(prompt_of "$f" | fold -s -w 100) >&2 || true
    fail=1
  fi
done

if [[ $fail -ne 0 ]]; then
  cat >&2 <<'EOF'

MISSING CLAUSE: the agent prompt lost a rule that a live failure put there. If
the removal is deliberate, delete the clause from scripts/check-agent-prompt.sh
in the same commit and say in the message which failure mode you are
re-opening.
PROMPT DRIFT: the prompt lives in charts/ratatosk-mcp/templates/_agent-prompt.tpl
and in examples/kagent/{0.x,1.x}/ratatosk-agent.yaml; change all three together.
EOF
  exit 1
fi

echo "agent prompt: ${#CLAUSES[@]} clauses present in ${#FILES[@]} files, and the files agree"
