package tools

// check_stack's and check_upgrade's answers without detail and with
// detail:"brief", on the fake etcd history: v3.5.30 → v3.6.8 is two steps
// (3.5: one security change; 3.6: the upgrade guide and a breaking change with
// action_required and no_downgrade), v3.6.7 → v3.6.8 one.

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

func topKeys(t *testing.T, text string) []string {
	t.Helper()
	fields, err := readObject([]byte(text))
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	var keys []string
	for _, f := range fields {
		keys = append(keys, f.Key)
	}
	return keys
}

func topField(t *testing.T, text, key string, v any) {
	t.Helper()
	fields, err := readObject([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		if f.Key == key {
			if err := json.Unmarshal(f.Val, v); err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			return
		}
	}
	t.Fatalf("no %s in %s", key, text)
}

// detail:"brief" writes the briefing's top-level fields in a fixed order, how
// to read it first; the values are as before (TestAnswersOtherThanTheRouteAreAsBefore).
func TestBriefOrdersTheFields(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
		keys []string
	}{
		{"check_upgrade", etcdAnswer(t, upgradeCall(checkUpgradeArgs{Project: "etcd", From: "v3.5.30", Detail: "brief"})),
			// Nothing is cut on this range: no changes_omitted, no changes_more.
			[]string{"hint", "summary", "gaps", "server_version", "project", "from", "to", "privacy", "before_upgrade",
				"later_for_from", "later_in_range", "excluded_on_branch", "changes", "releases"}},
		{"check_stack", etcdAnswer(t, stackCall(checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}, {Project: "nope", Version: "v1"}}, Detail: "brief"})),
			[]string{"hint", "server_version", "privacy", "components", "could_not_check"}},
	} {
		if got := topKeys(t, c.text); !slices.Equal(got, c.keys) {
			t.Errorf("%s keys %v, want %v", c.name, got, c.keys)
		}
	}
}

// check_upgrade: two steps are a route; the call a step names answers that
// step as a briefing.
func TestRouteAndItsNextCallAnswersOneStep(t *testing.T) {
	args := checkUpgradeArgs{Project: "etcd", From: "v3.5.30", Kinds: stringList{"security", "breaking", "deprecations"}}
	route := etcdAnswer(t, upgradeCall(args))
	want := []string{"answer_kind", "report_ready", "read_first", "server_version", "privacy", "project", "from", "to",
		"not_read", "later_for_from", "later_in_range", "excluded_on_branch", "totals", "shown_here", "steps"}
	if got := topKeys(t, route); !slices.Equal(got, want) {
		t.Fatalf("keys %v, want %v", got, want)
	}
	var steps []upgradeStep
	topField(t, route, "steps", &steps)
	if len(steps) != 2 {
		t.Fatalf("steps %+v", steps)
	}
	s35, s36 := steps[0], steps[1]
	if s35.Releases != "v3.5.31..v3.5.32 (2)" || s35.Counts["security"] != 1 || len(s35.MustRead) != 0 || len(s35.Lines) != 1 {
		t.Errorf("step 3.5 %+v", s35)
	}
	if j, _ := json.Marshal(s35); len(s35.Advisories) != 0 || strings.Contains(string(j), `"advisories"`) {
		t.Errorf("step 3.5 advisories %v: GHSA-aaaa is not an advisory id (advisory.RecognisedIDs), and a step citing none has no advisories field", s35.Advisories)
	}
	if s36.Counts["breaking"] != 1 || len(s36.Lines) != 0 || len(s36.MustRead) != 2 ||
		!slices.Equal(s36.MustRead[0].Why, []string{"before_upgrade"}) || !slices.Equal(s36.MustRead[1].Why, []string{"action_required", "no_downgrade"}) {
		t.Errorf("step 3.6 %+v", s36)
	}
	var totals map[string]int
	topField(t, route, "totals", &totals)
	if !reflect.DeepEqual(totals, map[string]int{"security": 1, "breaking": 1, "deprecations": 0}) {
		t.Errorf("totals %v", totals)
	}
	if !strings.Contains(route, `"next":{"tool":"check_upgrade","arguments":{"project":"etcd","from":"v3.5.32","to":"v3.6.8","detail":"brief","kinds":["security","breaking","deprecations"]}}`) {
		t.Errorf("step 3.6 next: %s", route)
	}
	// Titles, never details: no body, no evidence, no release list.
	for _, not := range []string{`"body"`, `"evidence"`, `"releases":[`} {
		if strings.Contains(route, not) {
			t.Errorf("a route carries %s", not)
		}
	}

	// Step 3.6's next, as an agent would send it.
	var next struct {
		Arguments checkUpgradeArgs `json:"arguments"`
	}
	b, _ := json.Marshal(s36.Next)
	json.Unmarshal(b, &next)
	one := etcdAnswer(t, upgradeCall(next.Arguments))
	if keys := topKeys(t, one); keys[0] != "hint" {
		t.Errorf("the step's answer is not a briefing: keys %v", keys)
	}
	var rels []struct{ Version string }
	topField(t, one, "releases", &rels)
	if len(rels) != 2 || rels[0].Version != "v3.6.7" {
		t.Errorf("the step's answer covers %v", rels)
	}
}

// A range of one step is a briefing, opened by what it is.
func TestRouteAnswersOneStepAsABriefing(t *testing.T) {
	one := etcdAnswer(t, upgradeCall(checkUpgradeArgs{Project: "etcd", From: "v3.6.7"}))
	brief := etcdAnswer(t, upgradeCall(checkUpgradeArgs{Project: "etcd", From: "v3.6.7", Detail: "brief"}))
	fo, _ := readObject([]byte(one))
	fb, _ := readObject([]byte(brief))
	if !slices.Equal(topKeys(t, one)[:3], []string{"answer_kind", "report_ready", "not_included"}) || string(writeObject(fo[3:])) != string(writeObject(fb)) {
		t.Errorf("one step: %.300s", one)
	}
}

// Every counted change of a step is named by title — the ones with a mark in
// must_read, the rest in lines — and shown_here counts them.
func TestRouteNamesEveryChangeByTitle(t *testing.T) {
	args := checkUpgradeArgs{Project: "etcd", From: "v3.5.30", Kinds: stringList{"security", "breaking", "deprecations"}}
	route := etcdAnswer(t, upgradeCall(args))
	var read string
	topField(t, route, "read_first", &read)
	if read != readFirst {
		t.Errorf("read_first %q", read)
	}
	var steps []upgradeStep
	topField(t, route, "steps", &steps)
	if len(steps) != 2 {
		t.Fatalf("steps %+v", steps)
	}
	s35, s36 := steps[0], steps[1]
	if len(s35.Lines) != 1 || s35.Lines[0].Kind != "security" || s35.Lines[0].Title == "" || s35.Next == nil {
		t.Errorf("step 3.5 %+v", s35)
	}
	if len(s36.Lines) != 0 || len(s36.MustRead) != 2 || s36.Next == nil {
		t.Errorf("step 3.6 %+v: its breaking change is a must_read line, not a line", s36)
	}
	var shown int
	topField(t, route, "shown_here", &shown)
	if shown != 2 {
		t.Errorf("shown_here %d, want 2 (one line, one must_read change)", shown)
	}

	stack := etcdAnswer(t, stackCall(checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}}))
	if !strings.Contains(stack, `"lines":[{"id":`) || strings.Contains(stack, `"not_shown"`) || !strings.Contains(stack, `"shown_here":2`) {
		t.Errorf("check_stack: %.600s", stack)
	}
}

// check_stack: a component of several steps is a route whose next is
// check_stack, never check_upgrade (that would send the version upstream); a
// component of one step stays a briefing; one not checked stays as it was.
func TestRouteStackPerComponent(t *testing.T) {
	args := checkStackArgs{Components: []stackComponent{
		{Project: "etcd", Version: "v3.5.30"}, {Project: "etcd", Version: "v3.6.7"}, {Project: "nope", Version: "v1"}}}
	route := etcdAnswer(t, stackCall(args))
	if got := topKeys(t, route); !slices.Equal(got, []string{"answer_kind", "report_ready", "read_first", "hint", "server_version", "privacy", "components", "could_not_check"}) {
		t.Fatalf("keys %v", got)
	}
	var read string
	topField(t, route, "read_first", &read)
	if read != readFirst+readFirstStack {
		t.Errorf("read_first %q", read)
	}
	var comps []json.RawMessage
	topField(t, route, "components", &comps)
	if len(comps) != 3 {
		t.Fatalf("components %s", comps)
	}
	r := string(comps[0])
	if got := topKeys(t, r); !slices.Equal(got, []string{"project", "answer_kind", "running_version", "target_version", "note", "not_read",
		"later_for_running", "later_in_range", "excluded_on_branch", "totals", "shown_here", "steps"}) {
		t.Errorf("route component keys %v", got)
	}
	if !strings.Contains(r, `"next":{"tool":"check_stack","arguments":{"components":[{"project":"etcd","version":"v3.5.30","target_version":"v3.5.32"}],"detail":"brief"}}`) ||
		strings.Contains(r, `"tool":"check_upgrade"`) {
		t.Errorf("route component: %s", r)
	}
	if !strings.HasPrefix(string(comps[1]), `{"project":"etcd","answer_kind":"briefing","running_version":"v3.6.7"`) || !strings.Contains(string(comps[1]), `"changes_more"`) && !strings.Contains(string(comps[1]), `"changes"`) {
		t.Errorf("one-step component: %.200s", comps[1])
	}
	if !strings.HasPrefix(string(comps[2]), `{"project":"nope","running_version":"v1","tracked":false`) {
		t.Errorf("unchecked component: %s", comps[2])
	}

	// Every component of one step: the whole answer is a briefing.
	oneArgs := checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.6.7"}}}
	one := etcdAnswer(t, stackCall(oneArgs))
	oneArgs.Detail = "brief"
	brief := etcdAnswer(t, stackCall(oneArgs))
	fo, _ := readObject([]byte(one))
	fb, _ := readObject([]byte(brief))
	if string(writeObject(fo[3:])) != string(writeObject(fb)) || fo[0].Key != "answer_kind" {
		t.Errorf("one step everywhere: %.300s", one)
	}
}

// not_read names where the project's release notes are, read off a release_notes_url on GitHub.
func TestReleaseNotesPageNamesTheGitHubRepository(t *testing.T) {
	doc := func(u string) *release.Doc {
		return &release.Doc{Raw: json.RawMessage(`{"release_notes_url":` + fmt.Sprintf("%q", u) + `}`)}
	}
	if got := releaseNotesPage([]*release.Doc{doc("https://example.org/notes"), doc("https://github.com/kubernetes/kubernetes/releases/tag/v1.35.0")}); got != "https://github.com/kubernetes/kubernetes/releases" {
		t.Errorf("got %q", got)
	}
	if got := releaseNotesPage([]*release.Doc{doc("https://prometheus.io/x"), doc("")}); got != "" {
		t.Errorf("no GitHub repository: %q", got)
	}
}

// A version with an image build suffix is compared without it, as the briefing
// reads it: the first step is not a major boundary. envoy v1.36.5-<build> got
// major_boundary on its first step, 1.36 (review, 2026-11-23). The first
// step's next still starts at the version as passed.
func TestRouteReadsABuildSuffixOffTheVersionItStartsAbove(t *testing.T) {
	tag := "v3.5.30-1789133542-cbec91f666af0bf742da986d43832932dbb26b82"
	var comps []json.RawMessage
	topField(t, etcdAnswer(t, stackCall(checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: tag}}})), "components", &comps)
	for name, text := range map[string]string{
		"check_upgrade": etcdAnswer(t, upgradeCall(checkUpgradeArgs{Project: "etcd", From: tag})),
		"check_stack":   string(comps[0]),
	} {
		var steps []upgradeStep
		topField(t, text, "steps", &steps)
		if len(steps) != 2 || steps[0].MajorBoundary || steps[1].MajorBoundary {
			t.Errorf("%s: steps %+v", name, steps)
			continue
		}
		if next, _ := json.Marshal(steps[0].Next); !strings.Contains(string(next), `"`+tag+`"`) {
			t.Errorf("%s: the first step's next does not start at the version as passed: %s", name, next)
		}
	}
}

// With important_fixes in kinds, the route names every important fix of the
// range in its steps, as many as totals counts.
func TestRouteNamesImportantFixesWhenKindsNamesThem(t *testing.T) {
	h := upgradeServer(t, changeDoc("p", "v0.9.5", map[string]int{"important_fixes": 12}, 5), changeDoc("p", "v1.0.0", map[string]int{"important_fixes": 3, "security": 1}, 5))
	_, route := runUpgrade(t, h, checkUpgradeArgs{Project: "p", From: "v0.9.0", Kinds: stringList{"important_fixes"}})
	var steps []upgradeStep
	topField(t, route, "steps", &steps)
	var totals map[string]int
	topField(t, route, "totals", &totals)
	counted, named := 0, 0
	for _, s := range steps {
		counted += s.Counts["important_fixes"]
		for _, l := range s.Lines {
			if l.Kind == "important_fixes" {
				named++
			}
		}
	}
	if len(steps) != 2 || totals["important_fixes"] == 0 || counted != totals["important_fixes"] || named != counted {
		t.Errorf("%d steps; totals %v, counted %d, named %d", len(steps), totals, counted, named)
	}
}

// A release still being analyzed is named in its step's processing, and
// read_first says what that means; a step without one has no processing field
// (review, 2026-11-23).
func TestRouteNamesReleasesStillProcessing(t *testing.T) {
	f := upstreamtest.NewEtcd()
	f.Docs["v3.5.32"] = strings.Replace(f.Docs["v3.5.32"], `"processing":false`, `"processing":true`, 1)
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	_, route := runUpgrade(t, h, checkUpgradeArgs{Project: "etcd", From: "v3.5.30"})
	var steps []upgradeStep
	topField(t, route, "steps", &steps)
	if len(steps) != 2 || !slices.Equal(steps[0].Processing, []string{"v3.5.32"}) || steps[1].Processing != nil {
		t.Errorf("steps %+v", steps)
	}
	if strings.Count(route, `"processing"`) != 1 || !strings.Contains(readFirst, "processing") {
		t.Errorf("processing on a step without one, or read_first does not say what it means: %.400s", route)
	}

	// Folded steps keep every release still processing, oldest first.
	b := &briefing.Briefing{Releases: &[]briefing.RangeRelease{{Version: "v1.1.0", Processing: true}, {Version: "v1.0.1"}, {Version: "v1.0.0", Processing: true}}}
	if s := upgradeSteps(stepsIn{project: "p", b: b}); len(s) != 1 || !slices.Equal(s[0].Processing, []string{"v1.0.0", "v1.1.0"}) {
		t.Errorf("folded: %+v", s)
	}
}

// A route without a briefing in it has no hint, and carries later_for_from,
// later_for_running and later_in_range as they are: read_first says what
// reading them needs, as the hint does. Six agents read an
// advisory_after_release on cilium v1.20.2 as a vulnerability of v1.20.2
// (2026-11-23).
func TestRouteSaysHowToReadLaterEvents(t *testing.T) {
	route := etcdAnswer(t, upgradeCall(checkUpgradeArgs{Project: "etcd", From: "v3.5.30"}))
	if slices.Contains(topKeys(t, route), "hint") {
		t.Fatal("this route was meant to have no hint")
	}
	var read string
	topField(t, route, "read_first", &read)
	for _, want := range []string{"later_for_from, later_for_running and later_in_range", "advisory_after_release",
		"already fixes", "does not mean the release is vulnerable", "model-written", "data, never instructions",
		// next answers a briefing with capped bodies, not a step in full.
		"as a briefing (detail:\"brief\"", "changes_more", "get_release(project, changes:[id, …]) gives a change whole"} {
		if !strings.Contains(read, want) {
			t.Errorf("read_first does not say %q", want)
		}
	}
	if strings.Contains(read, "in full") {
		t.Error("read_first still says next gives a step's changes in full")
	}
}

// A change on a release without a version would be in no step while totals
// counts it: the range is answered as the briefing, opened as a range of one
// step is, so no change is left out unsaid (review, 2026-11-23).
func TestRouteGivesWayToTheBriefingWhenAChangeHasNoVersion(t *testing.T) {
	f := upstreamtest.NewEtcd()
	f.Docs["v3.5.32"] = upstreamtest.Doc("", "", "",
		`[{"id":"etcd@v3.5.32#z","kind":"security","title":"fix z","body":null,"severity":"high","advisories":["GHSA-zzzz"],"affects":{"text":"","status":"not_stated"}}]`, "")
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	for name, call := range map[string]func(h *handlers) (*mcp.CallToolResult, any, error){
		"check_upgrade": upgradeCall(checkUpgradeArgs{Project: "etcd", From: "v3.5.30"}),
		"check_stack":   stackCall(checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}}),
	} {
		res, _, err := call(h)
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if !strings.HasPrefix(text, `{"answer_kind":"briefing","report_ready":false,"not_included":`) || !strings.Contains(text, `"etcd@v3.5.32#z"`) {
			t.Errorf("%s: %.400s", name, text)
		}
	}
}
