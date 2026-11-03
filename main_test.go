package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/version"
)

// ---------------------------------------------------------------------------
// fake /v2
// ---------------------------------------------------------------------------

// fakeV2 serves a small etcd history in the /v2 shape and records every
// request URL, so tests can assert on what left this process.
type fakeV2 struct {
	mu       sync.Mutex
	seen     []string
	rows     []v2Row
	docs     map[string]string // version → release document
	projects string
}

func (f *fakeV2) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.URL.RequestURI())
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/v2/projects":
			w.Write([]byte(f.projects))
		case r.URL.Path == "/v2/releases":
			var rows []v2Row
			for _, row := range f.rows {
				if p := r.URL.Query().Get("project"); p == "" || p == row.Project {
					rows = append(rows, row)
				}
			}
			if l := r.URL.Query().Get("limit"); l == "1" && len(rows) > 1 {
				rows = rows[:1]
			}
			json.NewEncoder(w).Encode(map[string]any{"releases": rows, "next_before": nil})
		case strings.HasPrefix(r.URL.Path, "/v2/releases/etcd/"):
			v := strings.TrimPrefix(r.URL.Path, "/v2/releases/etcd/")
			d, ok := f.docs[v]
			if !ok {
				http.Error(w, `{"error":"no analyzed release etcd `+v+`"}`, http.StatusNotFound)
				return
			}
			w.Write([]byte(d))
		default:
			t.Errorf("unexpected upstream request %s", r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}
}

func (f *fakeV2) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.seen)
	sort.Strings(out)
	return out
}

func doc(ver string, counts string, before string, changes string, later string) string {
	if counts == "" {
		counts = `{"security":0,"breaking":0,"deprecations":0,"important_fixes":0,"enhancements":0,"dependency_updates":0,"other":0}`
	}
	if before == "" {
		before = `{"prerequisites":[],"known_issues":[],"upgrade_guide":[]}`
	}
	if changes == "" {
		changes = `[]`
	}
	if later == "" {
		later = `[]`
	}
	return fmt.Sprintf(`{"project":"etcd","version":%q,"released_at":"2026-07-01T00:00:00Z","url":"https://x/en/releases/etcd/%s",`+
		`"counts":%s,"warnings":{"critical":0,"no_downgrade":0},"processing":false,"before_upgrade":%s,"changes":%s,"later":%s}`,
		ver, ver, counts, before, changes, later)
}

func newFakeEtcd() *fakeV2 {
	sec := func(id, adv string) string {
		return `{"id":"` + id + `","kind":"security","title":"fix ` + adv + `","body":null,"severity":null,"advisories":["` + adv + `"],` +
			`"affects":{"text":"gRPC listener","status":"stated"},"also_required":null,"impact_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}`
	}
	f := &fakeV2{
		projects: `{"projects":[{"slug":"etcd","name":"etcd"},{"slug":"envoy","name":"Envoy","image_aliases":["cilium-envoy"]}]}`,
		rows: []v2Row{
			{Project: "etcd", Version: "v3.6.8"}, {Project: "etcd", Version: "v3.6.7"}, {Project: "etcd", Version: "api/v3.6.7"},
			{Project: "etcd", Version: "v3.5.32"}, {Project: "etcd", Version: "v3.5.31"}, {Project: "etcd", Version: "v3.5.30"},
		},
		docs: map[string]string{
			"v3.5.30": doc("v3.5.30", "", "", `[`+sec("etcd@v3.5.30#b", "GHSA-bbbb")+`]`,
				`[{"seq":1,"at":"2026-08-01T00:00:00Z","type":"known_issue_fixed","project":"etcd","version":"v3.5.30","fixed_in":"v3.5.31","title":"leak"}]`),
			"v3.5.31": doc("v3.5.31", "", "", `[`+sec("etcd@v3.5.31#a", "GHSA-aaaa")+`]`, ""),
			"v3.5.32": doc("v3.5.32", "", "", "", ""),
			"v3.6.7": doc("v3.6.7", `{"security":1,"breaking":1,"deprecations":0,"important_fixes":0,"enhancements":2,"dependency_updates":0,"other":0}`,
				`{"prerequisites":[],"known_issues":[],"upgrade_guide":[{"id":"etcd@v3.6.7#g","title":"read the 3.6 upgrade guide","body":null,"evidence":[],"folded":[]}]}`,
				`[`+sec("etcd@v3.6.7#a", "GHSA-aaaa")+`,`+
					`{"id":"etcd@v3.6.7#k","kind":"breaking","title":"v2store removed","body":"long","affects":{"text":"","status":"not_stated"},"action_required":"migrate","no_downgrade":{"text":"","status":"stated"},"impact_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"also_touches":[],"evidence":[],"later":[]},`+
					`{"id":"etcd@v3.6.7#e","kind":"enhancements","title":"new flag","body":null,"affects":{"text":"","status":"not_stated"},"to_use":null,"impact_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}]`, ""),
			"v3.6.8":     doc("v3.6.8", "", "", `[`+sec("etcd@v3.6.8#b", "GHSA-bbbb")+`]`, ""),
			"api/v3.6.7": doc("api/v3.6.7", "", "", "", ""),
		},
	}
	return f
}

type stackOut struct {
	Components []struct {
		Project        string `json:"project"`
		RunningVersion string `json:"running_version"`
		TargetVersion  string `json:"target_version"`
		Tracked        *bool  `json:"tracked"`
		Note           string `json:"note"`
		Summary        struct {
			Releases int            `json:"releases"`
			Counts   map[string]int `json:"counts"`
		} `json:"summary"`
		Releases []struct {
			Version string `json:"version"`
		} `json:"releases"`
		BeforeUpgrade []briefItem `json:"before_upgrade"`
		Changes       []struct {
			Version     string          `json:"version"`
			ID          string          `json:"id"`
			Kind        string          `json:"kind"`
			Severity    json.RawMessage `json:"severity"`
			AlsoIn      []string        `json:"also_in"`
			NoDowngrade *v2Attr         `json:"no_downgrade"`
		} `json:"changes"`
		Gaps            []versionGap      `json:"gaps"`
		LaterForRunning []json.RawMessage `json:"later_for_running"`
	} `json:"components"`
	CouldNotCheck []uncheckable `json:"could_not_check"`
	Privacy       string        `json:"privacy"`
}

func runStack(t *testing.T, args checkStackArgs) stackOut {
	t.Helper()
	res, _, err := checkStackTool(context.Background(), nil, args)
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	var out stackOut
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, text)
	}
	return out
}

// ---------------------------------------------------------------------------
// privacy: what leaves this process depends on the project only
// ---------------------------------------------------------------------------

// The whole point of check_stack over check_upgrade. Two installs on very
// different versions, one with a target, must produce exactly the same
// upstream requests — otherwise the server could read the running version
// off which releases were fetched.
func TestCheckStackRequestsDoNotDependOnVersions(t *testing.T) {
	var sets [][]string
	for _, comp := range []stackComponent{
		{Project: "etcd", Version: "v3.5.30"},
		{Project: "etcd", Version: "v3.6.8"},
		{Project: "etcd", Version: "3.5.31", TargetVersion: "v3.6.7"},
		{Project: "etcd", Version: "api/v3.6.7"},
	} {
		f := newFakeEtcd()
		swapAPI(t, httptest.NewServer(f.handler(t)))
		runStack(t, checkStackArgs{Components: []stackComponent{comp}})
		reqs := f.requests()
		for _, u := range reqs {
			if strings.Contains(u, "from=") || strings.Contains(u, "/v2/upgrade") {
				t.Fatalf("check_stack called the server-side range: %s", u)
			}
		}
		sets = append(sets, reqs)
	}
	for i := 1; i < len(sets); i++ {
		if !slices.Equal(sets[0], sets[i]) {
			t.Fatalf("requests depend on the running version:\n%v\nvs\n%v", sets[0], sets[i])
		}
	}
}

// A second call inside the cache window costs nothing upstream.
func TestCheckStackCachesPublicDocuments(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}})
	n := len(f.requests())
	runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.6.7"}}})
	if m := len(f.requests()); m != n {
		t.Fatalf("second call went upstream again: %d → %d requests", n, m)
	}
}

// ---------------------------------------------------------------------------
// the briefing
// ---------------------------------------------------------------------------

func TestCheckStackBriefing(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	out := runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}})
	c := out.Components[0]

	var vers []string
	for _, r := range c.Releases {
		vers = append(vers, r.Version)
	}
	if want := []string{"v3.5.31", "v3.5.32", "v3.6.7", "v3.6.8"}; !slices.Equal(vers, want) {
		t.Fatalf("range %v, want %v (running excluded, other line excluded, oldest first)", vers, want)
	}
	if c.TargetVersion != "v3.6.8" {
		t.Errorf("target %q, want the highest on the line", c.TargetVersion)
	}
	// GHSA-aaaa landed on 3.5.31 and 3.6.7: one entry at the earliest.
	// GHSA-bbbb is already in the running 3.5.30: excluded and counted.
	var ids []string
	for _, ch := range c.Changes {
		ids = append(ids, ch.ID)
	}
	if want := []string{"etcd@v3.5.31#a", "etcd@v3.6.7#k"}; !slices.Equal(ids, want) {
		t.Fatalf("changes %v, want %v (security first; enhancements counted only)", ids, want)
	}
	if got := c.Changes[0].AlsoIn; !slices.Equal(got, []string{"v3.6.7"}) {
		t.Errorf("also_in %v, want [v3.6.7]", got)
	}
	if string(c.Changes[0].Severity) != "null" {
		t.Errorf("security severity %s, want null (the advisory states no grade)", c.Changes[0].Severity)
	}
	if c.Changes[1].Severity != nil {
		t.Errorf("a breaking change carries severity %s", c.Changes[1].Severity)
	}
	if c.Changes[1].NoDowngrade == nil {
		t.Error("no_downgrade dropped")
	}
	if !strings.Contains(c.Note, "1 security change(s) in range were excluded") {
		t.Errorf("note %q must count the change already fixed on the running branch", c.Note)
	}
	if c.Summary.Releases != 4 || c.Summary.Counts["enhancements"] != 2 {
		t.Errorf("summary %+v", c.Summary)
	}
	if len(c.BeforeUpgrade) != 1 || c.BeforeUpgrade[0].Version != "v3.6.7" || c.BeforeUpgrade[0].Section != "upgrade_guide" {
		t.Errorf("before_upgrade %+v", c.BeforeUpgrade)
	}
	if want := []versionGap{{After: "v3.5.32", Before: "v3.6.7"}}; !slices.Equal(c.Gaps, want) {
		t.Errorf("gaps %v, want %v", c.Gaps, want)
	}
	if len(c.LaterForRunning) != 1 || !strings.Contains(string(c.LaterForRunning[0]), "known_issue_fixed") {
		t.Errorf("later_for_running %s", c.LaterForRunning)
	}
	if len(out.CouldNotCheck) != 0 {
		t.Errorf("could_not_check %+v", out.CouldNotCheck)
	}
}

func TestCheckStackUntrackedAliasAndDroppedPrefix(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	out := runStack(t, checkStackArgs{Components: []stackComponent{
		{Project: "not-a-project", Version: "v1.0.0"},
		{Project: "etcd", Version: "flagd/v3.6.7"},
		{Project: "etcd", Version: "garbage"},
	}})
	if len(out.CouldNotCheck) != 3 {
		t.Fatalf("could_not_check %+v, want 3", out.CouldNotCheck)
	}
	if c := out.Components[0]; c.Tracked == nil || *c.Tracked || !strings.Contains(c.Note, "NOT tracked") {
		t.Errorf("untracked component %+v", c)
	}
	if !strings.Contains(out.CouldNotCheck[1].Reason, "release-line prefix was probably dropped") {
		t.Errorf("dropped-prefix reason %q", out.CouldNotCheck[1].Reason)
	}
	if !strings.Contains(out.CouldNotCheck[2].Reason, "could not be parsed") {
		t.Errorf("unparseable reason %q", out.CouldNotCheck[2].Reason)
	}

	// An image alias is checked as its project, and says so.
	g := newFakeEtcd()
	g.projects = `{"projects":[{"slug":"etcd","name":"etcd","image_aliases":["etcd-server"]}]}`
	swapAPI(t, httptest.NewServer(g.handler(t)))
	out = runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd-server", Version: "v3.6.7"}}})
	if c := out.Components[0]; c.Project != "etcd" || !strings.Contains(c.Note, "image alias") || len(out.CouldNotCheck) != 0 {
		t.Errorf("alias not resolved: %+v", c)
	}
}

// Replay of the measured failure: running-as-target read as "no issues".
func TestCheckStackIgnoresSelfNullifyingTarget(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	out := runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.6.7", TargetVersion: "v3.6.7"}}})
	c := out.Components[0]
	if len(c.Releases) != 1 || c.Releases[0].Version != "v3.6.8" {
		t.Errorf("releases %+v, want the full path (v3.6.8) with the target ignored", c.Releases)
	}
	if !strings.Contains(c.Note, "empty by construction") {
		t.Errorf("note %q must explain the ignored target", c.Note)
	}
}

// ---------------------------------------------------------------------------
// version helpers
// ---------------------------------------------------------------------------

// Same cases as the web's versionGaps test (apps/web/src/lib/v2-shape.test.ts):
// the server's /v2/upgrade and check_stack must flag the same places.
func TestVersionGaps(t *testing.T) {
	cases := []struct {
		points []string
		want   []versionGap
	}{
		{[]string{"v3.5.30", "v3.5.31", "v3.5.32", "v3.6.7", "v3.6.8"}, []versionGap{{"v3.5.32", "v3.6.7"}}},
		{[]string{"1.9.4", "2.0.0", "2.0.2"}, []versionGap{{"2.0.0", "2.0.2"}}},
		{[]string{"v1.4.0", "v1.6.0"}, []versionGap{{"v1.4.0", "v1.6.0"}}},
		{[]string{"0.9.52.0", "0.9.53.0", "v1.2.3-rc.1", "v1.2.3"}, []versionGap{{"0.9.53.0", "v1.2.3-rc.1"}}},
		{[]string{"v2.1.0", "v2.1.1"}, nil},
	}
	for _, c := range cases {
		if got := versionGaps(c.points); !slices.Equal(got, c.want) {
			t.Errorf("versionGaps(%v) = %v, want %v", c.points, got, c.want)
		}
	}
}

func TestUpgradeRange(t *testing.T) {
	rows := []v2Row{{Version: "v1.24.4"}, {Version: "v1.24.3"}, {Version: "v1.24.3-rc1"}, {Version: "v1.24.2"}, {Version: "api/v1.30.0"}, {Version: "redirect"}}
	got := func(running, target string) ([]string, string) {
		r, tgt := upgradeRange(rows, running, target)
		var v []string
		for _, x := range r {
			v = append(v, x.Version)
		}
		return v, tgt
	}
	if v, tgt := got("v1.24.2", ""); !slices.Equal(v, []string{"v1.24.3-rc1", "v1.24.3", "v1.24.4"}) || tgt != "v1.24.4" {
		t.Errorf("full path %v → %s", v, tgt)
	}
	if v, _ := got("v1.24.3-rc1", "v1.24.3"); !slices.Equal(v, []string{"v1.24.3"}) {
		t.Errorf("the finished release ranks above its own pre-release: %v", v)
	}
	if v, _ := got("1.24.2", "v1.24.3"); !slices.Equal(v, []string{"v1.24.3-rc1", "v1.24.3"}) {
		t.Errorf("to is inclusive, a missing v is fine: %v", v)
	}
	if v, _ := got("api/v1.29.0", ""); !slices.Equal(v, []string{"api/v1.30.0"}) {
		t.Errorf("only the running line: %v", v)
	}
}

func TestSameTag(t *testing.T) {
	if !sameTag("1.30.2", "v1.30.2") || sameTag("v1.30.2", "api/v1.30.2") || sameTag("v1.24.3", "v1.24.3-rc1") {
		t.Error("sameTag")
	}
}

func TestSameBranch(t *testing.T) {
	k := version.NormalizeVersion
	if !sameBranch(k("v2.2.4"), k("v2.2.5")) || sameBranch(k("v2.2.4"), k("v2.3.1")) || sameBranch(k("v2"), k("v2")) {
		t.Error("sameBranch")
	}
}

// ---------------------------------------------------------------------------
// notes carried over from 0.x — each one a measured failure
// ---------------------------------------------------------------------------

func TestCoverageNote(t *testing.T) {
	k := version.NormalizeVersion
	if n := coverageNote("v1.16.0", k("v1.16.0"), k("v1.18.0"), "v1.18.0"); !strings.Contains(n, "older than every release on record") {
		t.Errorf("ancient version not flagged: %q", n)
	}
	if n := coverageNote("v1.19.0", k("v1.19.0"), k("v1.18.0"), "v1.18.0"); n != "" {
		t.Errorf("covered version flagged: %q", n)
	}
	if n := coverageNote("v1.0.0", k("v1.0.0"), nil, ""); n != "" {
		t.Errorf("no history must not be flagged: %q", n)
	}
}

func TestConfessionNote(t *testing.T) {
	for _, src := range []string{"inferred from chart version", "assumed default", "likely v1.2", "not verified"} {
		if confessionNote(src) == "" {
			t.Errorf("%q not flagged", src)
		}
	}
	for _, src := range []string{"", "daemonset/cilium image tag", "user stated it", "standard install manifest"} {
		if n := confessionNote(src); n != "" {
			t.Errorf("%q flagged: %q", src, n)
		}
	}
}

func TestResolveTarget(t *testing.T) {
	cur := version.NormalizeVersion("v1.36.8")
	if k, note := resolveTarget(cur, "v1.37.0"); k == nil || note != "" {
		t.Errorf("valid target refused: %v %q", k, note)
	}
	if k, note := resolveTarget(cur, "v1.36.8"); k != nil || !strings.Contains(note, "empty by construction") {
		t.Errorf("self-nullifying target honored: %v %q", k, note)
	}
	if k, note := resolveTarget(cur, "nonsense"); k != nil || note == "" {
		t.Errorf("unparseable target honored: %v %q", k, note)
	}
}

func TestBriefTextBoundsTheTailVisibly(t *testing.T) {
	short := "cilium-agent now refuses an empty policy"
	if got := briefText(short); got != short {
		t.Fatalf("a typical text was altered: %q", got)
	}
	long := strings.Repeat("가나다", 200) // 600 runes, multi-byte on purpose
	got := briefText(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatal("truncation is invisible")
	}
	if r := []rune(got); len(r) != briefTextChars+1 {
		t.Fatalf("cut %d runes, want %d + ellipsis", len(r)-1, briefTextChars)
	}
	if !strings.HasPrefix(long, strings.TrimSuffix(got, "…")) {
		t.Fatal("the kept part is not a prefix of the original")
	}
}

// ---------------------------------------------------------------------------
// argument tolerance (0.8–0.9.1)
// ---------------------------------------------------------------------------

func TestCheckStackAcceptsTheShapesModelsSend(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"documented array", `{"components":[{"project":"cilium","version":"v1.19.5"}]}`},
		{"array JSON-encoded as a string", `{"components":"[{\"project\": \"cilium\", \"version\": \"v1.19.5\"}]"}`},
		{"name instead of project", `{"components":[{"name":"cilium","version":"v1.19.5"}]}`},
		{"string form with name key", `{"components":"[{\"name\": \"cilium\", \"version\": \"v1.19.5\"}]"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got checkStackArgs
			if err := json.Unmarshal([]byte(tc.args), &got); err != nil {
				t.Fatalf("rejected a shape we promised to accept: %v", err)
			}
			if len(got.Components) != 1 || got.Components[0].Project != "cilium" || got.Components[0].Version != "v1.19.5" {
				t.Fatalf("landed wrong: %+v", got.Components)
			}
		})
	}
}

// The schema is what stops the SDK refusing the string form before the handler
// runs. If the widening is lost, the tolerance above becomes unreachable.
func TestCheckStackSchemaAcceptsBothComponentForms(t *testing.T) {
	s := checkStackSchema()
	comp := s.Properties["components"]
	if len(comp.AnyOf) != 2 {
		t.Fatalf("components schema is not widened: %+v", comp)
	}
	array := comp.AnyOf[0]
	if array.Type != "array" && !slices.Contains(array.Types, "array") {
		t.Fatalf("first branch is not the documented array form: %+v", array)
	}
	if comp.AnyOf[1].Type != "string" {
		t.Fatalf("second branch should accept the JSON-encoded string, got %q", comp.AnyOf[1].Type)
	}
}

func TestComponentsRejectionShowsTheShape(t *testing.T) {
	var got checkStackArgs
	err := json.Unmarshal([]byte(`{"components":"not json at all"}`), &got)
	if err == nil {
		t.Fatal("garbage was accepted")
	}
	if !strings.Contains(err.Error(), `[{"project":"cilium","version":"v1.19.5"}]`) {
		t.Fatalf("rejection does not show the correct shape: %v", err)
	}
}

// Replay of the hub's 2026-09-28 call: project_slug instead of project. It must
// be refused before any upstream request, naming the key that went unread.
func TestCheckStackRefusesComponentWithoutProject(t *testing.T) {
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream was called (%s) for a component with no project", r.URL.RequestURI())
	})))
	cases := []struct {
		name, args, want string
	}{
		{"hub replay, string form", `{"components":"[{\"version\": \"v1.35.0\", \"project_slug\": \"kubernetes\"}, {\"version\": \"2.2.0\", \"project_slug\": \"containerd\"}]","detail":"brief"}`,
			"component 1 has no project (keys received: project_slug, version)"},
		{"empty project value", `{"components":[{"project":"","version":"v1.19.5"}]}`,
			"component 1 has no project (keys received: project, version)"},
		{"only the second is missing", `{"components":[{"project":"cilium","version":"v1.19.5"},{"slug":"envoy","version":"v1.36.8"}]}`,
			"component 2 has no project (keys received: slug, version)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var args checkStackArgs
			if err := json.Unmarshal([]byte(tc.args), &args); err != nil {
				t.Fatalf("decode should stay tolerant; the refusal belongs to the handler: %v", err)
			}
			res, _, err := checkStackTool(context.Background(), nil, args)
			if err != nil {
				t.Fatal(err)
			}
			text := res.Content[0].(*mcp.TextContent).Text
			if !res.IsError || !strings.Contains(text, tc.want) || !strings.Contains(text, `[{"project":"cilium","version":"v1.19.5"}]`) {
				t.Fatalf("refusal = %q, want it to contain %q and the shape", text, tc.want)
			}
		})
	}
}

// The backstop where the fan-out happens: an empty slug must never page.
func TestProjectReleasesRefusesEmptyProject(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream was called (%s) with an empty project", r.URL.RequestURI())
	}))
	defer ts.Close()
	if _, err := newAPIClient(ts.URL).projectReleases(context.Background(), ""); err == nil {
		t.Fatal("an empty project paged every project")
	}
}

// ---------------------------------------------------------------------------
// the other tools
// ---------------------------------------------------------------------------

func TestGetReleaseWithoutVersionReadsTheLatest(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	res, _, err := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Lang: "ko"})
	if err != nil || res.IsError {
		t.Fatalf("get_release: %v %+v", err, res)
	}
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, `"version":"v3.6.8"`) {
		t.Fatalf("not the latest: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	if reqs := f.requests(); !slices.Contains(reqs, "/v2/releases/etcd/v3.6.8?lang=ko") {
		t.Fatalf("lang not passed: %v", reqs)
	}
	if res, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Lang: "de"}); !res.IsError {
		t.Fatal("an unknown lang was read as English")
	}
}

func TestCheckUpgradeComputesGapsAnOlderServerOmits(t *testing.T) {
	var seen string
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.RequestURI()
		fmt.Fprintf(w, `{"project":"etcd","from":"v3.5.30","to":"v3.6.7","releases":[%s,%s],"later_for_from":[]}`,
			doc("v3.5.31", "", "", "", ""), doc("v3.6.7", "", "", "", ""))
	})))
	res, _, err := checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30"})
	if err != nil || res.IsError {
		t.Fatalf("check_upgrade: %v %+v", err, res)
	}
	if seen != "/v2/upgrade/etcd?from=v3.5.30" {
		t.Errorf("request %s", seen)
	}
	var out struct {
		Gaps    []versionGap `json:"gaps"`
		Privacy string       `json:"privacy"`
	}
	json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out)
	if !slices.Equal(out.Gaps, []versionGap{{"v3.5.31", "v3.6.7"}}) {
		t.Errorf("gaps %v", out.Gaps)
	}
	if !strings.Contains(out.Privacy, "sent to the server") {
		t.Errorf("check_upgrade must say the versions went upstream: %q", out.Privacy)
	}
}

func TestListReleasesValidates(t *testing.T) {
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Get("limit") != "10" || q.Get("kind") != "security" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"releases":[],"next_before":null}`))
	})))
	if res, _, _ := listReleasesTool(context.Background(), nil, listReleasesArgs{Kind: "security"}); res.IsError {
		t.Fatalf("valid call refused: %+v", res)
	}
	if res, _, _ := listReleasesTool(context.Background(), nil, listReleasesArgs{Kind: "action"}); !res.IsError {
		t.Fatal("a 0.x bucket name was accepted as a kind")
	}
	if res, _, _ := listReleasesTool(context.Background(), nil, listReleasesArgs{Since: "last week"}); !res.IsError {
		t.Fatal("a non-date since was accepted")
	}
}

// The envoy range on the preview API (2026-11-03) had 55 security changes ahead
// of every breaking one; a single cap would have hidden all of them. Caps are
// per kind, security keeps its most severe, and every cut is counted.
func TestBriefingCapsPerKind(t *testing.T) {
	d := &v2Release{Version: "v1.38.0"}
	grades := []string{"low", "critical", "medium", "high"}
	for i := 0; i < 30; i++ {
		g := grades[i%4]
		d.Changes = append(d.Changes, v2Change{ID: fmt.Sprint("s", i), Kind: "security", Severity: &g, Advisories: []string{fmt.Sprint("CVE-2026-", 1000+i)}})
	}
	for i := 0; i < 3; i++ {
		d.Changes = append(d.Changes, v2Change{ID: fmt.Sprint("b", i), Kind: "breaking"})
	}
	b, _ := buildBriefing([]*v2Release{d}, nil)
	var sec, brk int
	for _, c := range b.Changes {
		switch c.Kind {
		case "security":
			sec++
			if gradeRank(c.Severity) == 1 {
				t.Errorf("a low-grade change was kept while others were cut: %s", c.ID)
			}
		case "breaking":
			brk++
		}
	}
	if sec != kindCaps["security"] || brk != 3 {
		t.Fatalf("kept security %d breaking %d, want %d and 3", sec, brk, kindCaps["security"])
	}
	if b.ChangesOmitted["security"] != 30-kindCaps["security"] || b.ChangesOmitted["breaking"] != 0 {
		t.Fatalf("omitted %v", b.ChangesOmitted)
	}
	if gradeRank(b.Changes[0].Severity) != 4 {
		t.Fatalf("security does not lead with critical: %s", b.Changes[0].Severity)
	}
}
