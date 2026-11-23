package tools

// What check_stack and check_upgrade answer when detail is not "full" (1.3.0):
//
//	no detail       a route — for a range of more than one step, its steps
//	                ([upgradeSteps]): every security, breaking and deprecation
//	                change named by title, with the call that answers one step
//	                (next) and the call that reads the evidence (verify). A
//	                range of one step is answered as a briefing, opened by what
//	                it is (answer_kind "briefing", report_ready false,
//	                not_included).
//	detail:"brief"  the briefing of the whole range, as 1.2.0 answered it, with
//	                its top-level fields in a fixed order: hint and summary
//	                first.
//
// Why a route first (the owner, 2026-11-23): the briefing of a long range was
// too big to read whole (see [upgradeSteps]). Measured against the briefing
// with the same marks on the same four questions, the route made fewer first
// answers big enough for the client to save to a file (Claude Sonnet, 12
// runs: 1 against 6), and the reports carried about as many of the items they
// had to (Sonnet 44 of 48 against 47, GPT 45 of 48 against 44).
//
// Why a fixed order: encoded from a map, the fields came in name order, which
// put hint — how to read the answer — 70,221 characters into a kubernetes
// answer, after the change lists (1.2.0, 2026-11-23). The values are 1.2.0's;
// only their order moved.
//
// detail:"full" does not come here: it is /v2's own documents.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
)

// routeDescribe opens check_stack's and check_upgrade's descriptions: what a
// call without detail answers. The first %s says where the steps are, the
// second what a range of one step is answered as.
const routeDescribe = "Without detail, the first answer is a route (answer_kind \"route\"): %s one step per minor " +
	"version naming every security, breaking and deprecation change by title (must_read and lines), with counts and " +
	"advisories, no details. Get one step's changes in full with its next call; read the evidence of a change with " +
	"its verify call. %s detail:\"brief\" answers the whole range as one briefing, described below. "

var (
	upgradeRouteDescription = fmt.Sprintf(routeDescribe, "for the range,",
		"A range of one step is answered as a briefing (answer_kind \"briefing\").")
	stackRouteDescription = fmt.Sprintf(routeDescribe, "per component,",
		"A component of one step is answered as a briefing (answer_kind \"briefing\"); one that could not be checked as before.")
)

// notIncluded is what a briefing does not carry, said in the answer.
const notIncluded = "evidence quotes, action details and workarounds are not in this answer; " +
	"read them with the verify calls before you report a change"

// readFirst opens a route.
const readFirst = "This answer is a route: for each upgrade step it names every security, breaking and deprecation " +
	"change by title (must_read: the ones that ask something of the operator; lines: the rest), without their " +
	"details. Call a step's next to get its changes in full - who they affect and what to do - and read the evidence " +
	"of a change with verify (or get_release) before you report it. Do not judge a change from its title alone: a " +
	"change whose step you did not open is reported as \"not inspected\", never as no impact. If you fill a range " +
	"listed in not_read from another source, name that source."

// readFirstStack is added to a check_stack route.
const readFirstStack = " A component with answer_kind \"briefing\" has one step: all of its change lines are " +
	"here, read as hint says."

// The top-level fields of a briefing, in the order they are written; a field
// not named here comes after them, by name.
var (
	upgradeOrder = []string{"hint", "summary", "gaps", "note", "server_version", "project", "from", "to",
		"version_read_as", "privacy", "before_upgrade", "later_for_from", "later_in_range", "excluded_on_branch",
		"changes", "changes_omitted", "changes_more", "releases"}
	stackOrder = []string{"hint", "server_version", "privacy", "components", "could_not_check"}
)

// answerField is one field of an answer, encoded as json.Marshal encodes it.
func answerField(key string, v any) objField {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // the server's own values: a wiring bug
	}
	return objField{Key: key, Val: b}
}

// orderedObject writes m with the keys of order first, in that order, and any
// other key after them, sorted — after the fields of front.
func orderedObject(front []objField, m map[string]any, order []string) []byte {
	fields := slices.Clone(front)
	for _, k := range order {
		if v, ok := m[k]; ok {
			fields = append(fields, answerField(k, v))
		}
	}
	var rest []string
	for k := range m {
		if !slices.Contains(order, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	for _, k := range rest {
		fields = append(fields, answerField(k, m[k]))
	}
	return writeObject(fields)
}

// briefingFront opens a briefing answered in place of a route.
func briefingFront() []objField {
	return []objField{answerField("answer_kind", "briefing"), answerField("report_ready", false),
		answerField("not_included", notIncluded)}
}

func textResult(b []byte) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}

// shownHere is the number of changes a route names: every must_read change
// and every line (a before_upgrade item is not a change).
func shownHere(steps []upgradeStep) int {
	n := 0
	for _, s := range steps {
		n += len(s.Lines)
		for _, m := range s.MustRead {
			if m.kind != "" {
				n++
			}
		}
	}
	return n
}

// upgradeAnswer answers check_upgrade when detail is not "full". out is the
// briefing's fields as check_upgrade builds them; b and docs the briefing and
// the range it was built from.
func (h *handlers) upgradeAnswer(out map[string]any, args checkUpgradeArgs, b *briefing.Briefing, docs []*release.Doc) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Detail) != "" {
		return textResult(orderedObject(nil, out, upgradeOrder))
	}
	project, _ := out["project"].(string)
	kinds, _ := kindsArg(args.Kinds, true)
	steps := upgradeSteps(stepsIn{project: project, b: b, docs: docs, kinds: kinds, lang: args.Lang, from: args.From,
		next: func(from, to string) *toolCall {
			return &toolCall{Tool: "check_upgrade", Arguments: upgradeCallArgs{Project: project, From: from, To: to,
				Detail: "brief", Kinds: args.Kinds, Lang: args.Lang}}
		}})
	if len(steps) <= 1 {
		return textResult(orderedObject(briefingFront(), out, upgradeOrder))
	}
	gaps, _ := out["gaps"].([]releaseline.Gap)
	route := []objField{
		answerField("answer_kind", "route"), answerField("report_ready", false), answerField("read_first", readFirst),
		answerField("server_version", out["server_version"]), answerField("privacy", out["privacy"]),
		answerField("project", project), answerField("from", out["from"]), answerField("to", out["to"]),
	}
	for _, k := range []string{"version_read_as", "note"} {
		if v, ok := out[k]; ok {
			route = append(route, answerField(k, v))
		}
	}
	route = append(route,
		answerField("not_read", notReadGaps(gaps, docs)),
		answerField("later_for_from", out["later_for_from"]),
		answerField("later_in_range", out["later_in_range"]))
	if v, ok := out["excluded_on_branch"]; ok {
		route = append(route, answerField("excluded_on_branch", v))
	}
	route = append(route,
		answerField("totals", rangeTotals(b, kinds)), answerField("shown_here", shownHere(steps)),
		answerField("steps", steps))
	return textResult(writeObject(route))
}

// stackAnswer answers check_stack when detail is not "full". out is the
// answer's fields as check_stack builds them, run the call's components.
//
// Each component is answered by the same rule as check_upgrade's range. A
// step's next is check_stack, never check_upgrade: check_upgrade sends the
// versions upstream, which check_stack exists not to do.
func (h *handlers) stackAnswer(out map[string]any, run *stackRun, args checkStackArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Detail) != "" {
		return textResult(orderedObject(nil, out, stackOrder))
	}
	comps := make([]json.RawMessage, len(run.reports))
	routes, briefs := 0, 0
	for i := range run.reports {
		rep := &run.reports[i]
		raw, _ := json.Marshal(rep)
		if rep.Summary != nil {
			project := rep.Project
			steps := upgradeSteps(stepsIn{project: project, b: &rep.Briefing, docs: rep.rangeDocs, kinds: run.kinds,
				lang: args.Lang, from: rep.RunningVersion,
				next: func(from, to string) *toolCall {
					return &toolCall{Tool: "check_stack", Arguments: stackCallArgs{
						Components: []stackCallComponent{{Project: project, Version: from, TargetVersion: to}},
						Detail:     "brief", Kinds: args.Kinds, Lang: args.Lang}}
				}})
			if len(steps) > 1 {
				raw = routeComponent(rep, steps, run.kinds)
				routes++
			} else {
				fields, _ := readObject(raw)
				raw = writeObject(slices.Insert(fields, 1, answerField("answer_kind", "briefing")))
				briefs++
			}
		}
		comps[i] = raw
	}
	if routes == 0 {
		return textResult(orderedObject(briefingFront(), out, stackOrder))
	}
	route := []objField{answerField("answer_kind", "route"), answerField("report_ready", false),
		answerField("read_first", readFirst+readFirstStack)}
	if briefs > 0 {
		route = append(route, answerField("hint", out["hint"]))
	}
	route = append(route, answerField("server_version", out["server_version"]), answerField("privacy", out["privacy"]),
		answerField("components", comps))
	if v, ok := out["could_not_check"]; ok {
		route = append(route, answerField("could_not_check", v))
	}
	return textResult(writeObject(route))
}
