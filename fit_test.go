package main

// fitBriefings (fit.go) against the inputs the independent review of
// 2026-11-20 broke it with: ranges whose fixed parts are large (dozens of
// releases, long before-upgrade bodies, many later events), twenty
// components, English and kinds. Every answer must be within
// briefCharBudget with hint within maxHint, and every decision change must be
// accounted for: listed, folded under an also_in, left out on the branch, or
// counted in changes_unlisted.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// relSpec says what a made-up release holds.
type relSpec struct {
	sec, secHigh, brk, brkStep, dep, ifx int
	title                                int // title length
	buItems, buBody                      int // before-upgrade items and their body length
	later                                int // later events: half advisory_after_release, half regression_fixed
	advBase                              int // advisory ids are CVE-2026-<advBase+i>: equal bases make backports
	siblings                             int // security changes repeating the first one's advisories
}

func genDoc(project, ver string, sp relSpec) string {
	var cs []string
	counts := map[string]int{}
	title := strings.Repeat("t", sp.title)
	add := func(kind, extra string, i int) {
		counts[kind]++
		cs = append(cs, fmt.Sprintf(`{"id":"%s@%s#%s%d","kind":%q,"title":"%s %s %d","body":"%s","affects":{"text":"setups with the feature on","status":"stated"}%s,"evidence":[{"quote":"q"}],"later":[]}`,
			project, ver, kind, i, kind, kind, title, i, strings.Repeat("b", 120), extra))
	}
	for i := 0; i < sp.sec; i++ {
		sev := "null"
		if i < sp.secHigh {
			sev = `"high"`
		}
		add("security", fmt.Sprintf(`,"severity":%s,"advisories":["CVE-2026-%d"]`, sev, sp.advBase+i), i)
	}
	for i := 0; i < sp.siblings; i++ {
		add("security", fmt.Sprintf(`,"severity":null,"advisories":["CVE-2026-%d"]`, sp.advBase), 1000+i)
	}
	for i := 0; i < sp.brk; i++ {
		extra := ""
		if i < sp.brkStep {
			extra = `,"action_required":"migrate first"`
		}
		add("breaking", extra, i)
	}
	for i := 0; i < sp.dep; i++ {
		add("deprecations", "", i)
	}
	for i := 0; i < sp.ifx; i++ {
		add("important_fixes", "", i)
	}
	var bu []string
	for i := 0; i < sp.buItems; i++ {
		bu = append(bu, fmt.Sprintf(`{"id":"%s@%s#u%d","title":"read first %d","body":"%s","evidence":[],"folded":[]}`, project, ver, i, i, strings.Repeat("u", sp.buBody)))
	}
	var later []string
	for i := 0; i < sp.later; i++ {
		if i%2 == 0 {
			later = append(later, fmt.Sprintf(`{"type":"advisory_after_release","advisory":"GHSA-%04d","at":"2026-08-01T00:00:00Z"}`, i))
		} else {
			later = append(later, fmt.Sprintf(`{"type":"regression_fixed","version":%q,"fixed_in":"later","title":"regression %d"}`, ver, i))
		}
	}
	cj, _ := json.Marshal(counts)
	return fmt.Sprintf(`{"project":%q,"version":%q,"released_at":"2026-07-01T00:00:00Z","url":"https://x/%s/%s","counts":%s,"warnings":{"critical":0,"no_downgrade":0},`+
		`"before_upgrade":{"prerequisites":[%s],"known_issues":[],"upgrade_guide":[]},"changes":[%s],"later":[%s]}`,
		project, ver, project, ver, cj, strings.Join(bu, ","), strings.Join(cs, ","), strings.Join(later, ","))
}

// bigAPI serves made-up projects in the /v2 shape.
type bigAPI struct {
	mu   sync.Mutex
	seen []string
	rows map[string][]string // project → versions, newest first
	docs map[string]string   // project@version → document
	fail func(path string) int
}

func newBigAPI() *bigAPI {
	return &bigAPI{rows: map[string][]string{}, docs: map[string]string{}}
}

// add puts releases v<major>.<minor>.<patch> for patch 0..n-1 of each minor.
func (a *bigAPI) add(project string, minors []string, n int, sp func(ver string) relSpec) {
	for _, m := range minors {
		for p := 0; p < n; p++ {
			v := fmt.Sprintf("v%s.%d", m, p)
			a.rows[project] = append([]string{v}, a.rows[project]...)
			a.docs[project+"@"+v] = genDoc(project, v, sp(v))
		}
	}
}

func (a *bigAPI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.seen = append(a.seen, r.URL.RequestURI())
		a.mu.Unlock()
		if a.fail != nil {
			if code := a.fail(r.URL.Path); code != 0 {
				http.Error(w, `{"error":"made to fail"}`, code)
				return
			}
		}
		rowsOf := func(p string) []v2Row {
			var out []v2Row
			for _, v := range a.rows[p] {
				out = append(out, v2Row{Project: p, Version: v})
			}
			return out
		}
		switch {
		case r.URL.Path == "/v2/projects":
			var ps []string
			for p := range a.rows {
				ps = append(ps, fmt.Sprintf(`{"slug":%q,"name":%q}`, p, p))
			}
			fmt.Fprintf(w, `{"projects":[%s]}`, strings.Join(ps, ","))
		case r.URL.Path == "/v2/releases":
			json.NewEncoder(w).Encode(map[string]any{"releases": rowsOf(r.URL.Query().Get("project")), "next_before": nil})
		case strings.HasPrefix(r.URL.Path, "/v2/releases/"):
			parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/v2/releases/"), "/", 2)
			d, ok := a.docs[parts[0]+"@"+parts[1]]
			if !ok {
				http.Error(w, `{"error":"no analyzed release"}`, http.StatusNotFound)
				return
			}
			w.Write([]byte(d))
		case strings.HasPrefix(r.URL.Path, "/v2/upgrade/"):
			p := strings.TrimPrefix(r.URL.Path, "/v2/upgrade/")
			from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
			inRange, target := upgradeRange(rowsOf(p), from, to)
			var rels []string
			for _, x := range inRange {
				rels = append(rels, a.docs[p+"@"+x.Version])
			}
			fmt.Fprintf(w, `{"project":%q,"from":%q,"to":%q,"releases":[%s],"gaps":[],"later_for_from":[]}`, p, from, target, strings.Join(rels, ","))
		default:
			t.Errorf("unexpected upstream request %s", r.URL.RequestURI())
		}
	}
}

func (a *bigAPI) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.seen)
}

// callText takes a tool's three results and returns its text, failing the
// test on an error.
func callText(t *testing.T) func(*mcp.CallToolResult, any, error) string {
	return func(res *mcp.CallToolResult, _ any, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("tool error: %.300s", text)
		}
		return text
	}
}

// briefOut is one fitted briefing as an agent reads it.
type briefOut struct {
	Project          string                           `json:"project"`
	Note             string                           `json:"note"`
	Summary          *struct{ Counts map[string]int } `json:"summary"`
	Changes          []briefChange                    `json:"changes"`
	ChangesOmitted   map[string]int                   `json:"changes_omitted"`
	ChangesMore      []moreLine                       `json:"changes_more"`
	ChangesUnlisted  map[string]int                   `json:"changes_unlisted"`
	ExcludedOnBranch *excludedOnBranch                `json:"excluded_on_branch"`
	BeforeUpgrade    *[]upgradeItem                   `json:"before_upgrade"`
	Releases         []map[string]any                 `json:"releases"`
}

// assertAccounted checks the counts of one fitted briefing: per decision
// kind, summary.counts = changes + changes_omitted + excluded_on_branch +
// backports folded into an also_in (alsoIn, what the range was built with);
// and changes_unlisted never exceeds what changes_omitted counts.
func assertAccounted(t *testing.T, b briefOut, alsoIn map[string]int, asked ...string) {
	t.Helper()
	if b.Summary == nil {
		return
	}
	entries := map[string]int{}
	for _, c := range b.Changes {
		entries[c.Kind]++
	}
	lines := map[string]int{}
	for _, l := range b.ChangesMore {
		lines[l.Kind]++
	}
	for _, k := range decisionKinds {
		if len(asked) > 0 && !slices.Contains(asked, k) {
			continue
		}
		ex := 0
		if k == "security" && b.ExcludedOnBranch != nil {
			ex = b.ExcludedOnBranch.Security
		}
		if got := entries[k] + b.ChangesOmitted[k] + ex + alsoIn[k]; got != b.Summary.Counts[k] {
			t.Errorf("%s %s: changes %d + omitted %d + excluded %d + also_in %d = %d, summary.counts %d",
				b.Project, k, entries[k], b.ChangesOmitted[k], ex, alsoIn[k], got, b.Summary.Counts[k])
		}
		if lines[k]+b.ChangesUnlisted[k] != b.ChangesOmitted[k] {
			t.Errorf("%s %s: %d lines + %d unlisted, %d omitted", b.Project, k, lines[k], b.ChangesUnlisted[k], b.ChangesOmitted[k])
		}
	}
}

func assertWithin(t *testing.T, text string) {
	t.Helper()
	if n := utf8.RuneCountInString(text); n > briefCharBudget {
		t.Errorf("answer %d characters, budget %d", n, briefCharBudget)
	}
	var top struct {
		Hint string `json:"hint"`
	}
	json.Unmarshal([]byte(text), &top)
	if n := utf8.RuneCountInString(top.Hint); n > maxHint {
		t.Errorf("hint %d characters, at most %d", n, maxHint)
	}
}

func heavy(ver string) relSpec {
	return relSpec{sec: 12, secHigh: 3, brk: 15, brkStep: 3, dep: 6, ifx: 30, title: 80, buItems: 3, buBody: 1500, later: 4, advBase: 10000 + 100*len(ver)}
}

// No briefing goes over the budget, whatever holds the room: a range of 60
// releases with long before-upgrade bodies and many later events, twenty
// components, kinds.
func TestEveryBriefingStaysWithinTheBudget(t *testing.T) {
	a := newBigAPI()
	uniq := 0
	spec := func(ver string) relSpec {
		uniq++
		sp := heavy(ver)
		sp.advBase = 20000 + 100*uniq
		return sp
	}
	a.add("big", []string{"1.1", "1.2", "1.3", "1.4", "1.5", "1.6"}, 10, spec)
	var comps []stackComponent
	for i := 0; i < 20; i++ {
		p := fmt.Sprintf("p%02d", i)
		a.add(p, []string{"2.0", "2.1", "2.2"}, 6, spec)
		comps = append(comps, stackComponent{Project: p, Version: "v2.0.0"})
	}
	swapAPI(t, httptest.NewServer(a.handler(t)))

	for _, c := range []struct {
		name  string
		asked []string
		call  func() (*mcp.CallToolResult, any, error)
	}{
		{"check_upgrade, 59 releases", nil, func() (*mcp.CallToolResult, any, error) {
			return checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "big", From: "v1.1.0"})
		}},
		{"check_upgrade, kinds breaking", []string{"breaking"}, func() (*mcp.CallToolResult, any, error) {
			return checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "big", From: "v1.1.0", Kinds: stringList{"breaking"}})
		}},
		{"check_stack, one heavy component", nil, func() (*mcp.CallToolResult, any, error) {
			return checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "big", Version: "v1.1.0"}}})
		}},
		{"check_stack, twenty components", nil, func() (*mcp.CallToolResult, any, error) {
			return checkStackTool(context.Background(), nil, checkStackArgs{Components: comps})
		}},
		{"check_stack, twenty components, kinds security", []string{"security"}, func() (*mcp.CallToolResult, any, error) {
			return checkStackTool(context.Background(), nil, checkStackArgs{Components: comps, Kinds: stringList{"security"}})
		}},
		{"check_stack, twenty components, ko", nil, func() (*mcp.CallToolResult, any, error) {
			return checkStackTool(context.Background(), nil, checkStackArgs{Components: comps, Lang: "ko"})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			text := callText(t)(c.call())
			assertWithin(t, text)
			var out struct {
				Components []briefOut `json:"components"`
				Hint       string     `json:"hint"`
				briefOut
			}
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatal(err)
			}
			bs := out.Components
			if bs == nil {
				bs = []briefOut{out.briefOut}
			}
			stubbed := 0
			for _, b := range bs {
				if !strings.Contains(b.Note, "summary only") {
					assertAccounted(t, b, nil, c.asked...)
				}
				if strings.Contains(b.Note, stubNote) {
					stubbed++
					if !strings.Contains(out.Hint, b.Project) && !strings.Contains(out.Hint, "more)") {
						t.Errorf("hint does not name the component cut to a summary, %s: %s", b.Project, out.Hint)
					}
				} else if len(b.ChangesUnlisted) > 0 && !strings.Contains(out.Hint, b.Project) && !strings.Contains(out.Hint, "more)") && len(bs) > 1 {
					t.Errorf("hint does not name %s, which has changes not listed: %s", b.Project, out.Hint)
				}
			}
			if len(bs) > 1 && stubbed == len(bs) {
				t.Errorf("every component was cut to a summary")
			}
			t.Logf("%d characters, %d of %d components summary only", utf8.RuneCountInString(text), stubbed, len(bs))
		})
	}
}

// The counts add up after fitting, also_in included: entries moved to a line
// keep the backports they stand for (kubernetes v1.32.11 to its newest lost
// 10 of 43 security changes without a trace — independent review,
// 2026-11-20), and a sibling folded in its own release is counted in
// changes_omitted (coredns v1.14.7 → v1.15.0: 8 counted, 5 listed, 0 omitted).
func TestCountsAddUpAfterFittingWithBackportsAndSiblings(t *testing.T) {
	a := newBigAPI()
	// Branches 1.33 to 1.36 carry the same 25 advisories on their first patch,
	// the oldest with a sibling of the first; heavy enough to be fitted.
	a.add("k", []string{"1.32", "1.33", "1.34", "1.35", "1.36"}, 4, func(ver string) relSpec {
		sp := relSpec{ifx: 14, brk: 6, title: 150, buItems: 1, buBody: 300}
		if strings.HasSuffix(ver, ".0") && !strings.HasPrefix(ver, "v1.32") {
			sp.sec, sp.advBase = 25, 50000
			if ver == "v1.33.0" {
				sp.siblings = 2
			}
		}
		return sp
	})
	swapAPI(t, httptest.NewServer(a.handler(t)))
	text := callText(t)(checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "k", From: "v1.32.3"}))
	assertWithin(t, text)
	var b briefOut
	json.Unmarshal([]byte(text), &b)
	// 25 advisories on four branches: one entry each, three backports each.
	assertAccounted(t, b, map[string]int{"security": 75})
	trail := 0
	for _, c := range b.Changes {
		if c.Kind == "security" {
			trail += 1 + len(c.AlsoIn)
		}
	}
	for _, l := range b.ChangesMore {
		if l.Kind == "security" {
			trail += 1 + len(l.AlsoIn)
		}
	}
	if trail+b.ChangesUnlisted["security"] != b.Summary.Counts["security"] {
		t.Errorf("security changes traceable in the answer: %d listed or in also_in + %d unlisted, %d in range", trail, b.ChangesUnlisted["security"], b.Summary.Counts["security"])
	}
	sec := 0
	for _, c := range b.Changes {
		if c.Kind == "security" {
			sec++
		}
	}
	if sec == kindCaps["security"] || b.ChangesUnlisted["important_fixes"] == 0 {
		t.Fatalf("the case must move security entries to lines and drop important fixes: %d security entries, unlisted %v", sec, b.ChangesUnlisted)
	}
}

// Layers are taken across components: one component's low layer goes before
// another's severe advisories, and each component keeps five lines of its
// highest layer.
func TestLaddersCrossComponentsByLayer(t *testing.T) {
	a := newBigAPI()
	base := 60000
	a.add("severe", []string{"1.0", "1.1"}, 2, func(ver string) relSpec {
		if ver == "v1.0.0" {
			return relSpec{}
		}
		base += 100
		return relSpec{sec: 40, secHigh: 40, title: 150, advBase: base}
	})
	a.add("fixes", []string{"2.0", "2.1"}, 2, func(ver string) relSpec {
		if ver == "v2.0.0" {
			return relSpec{}
		}
		return relSpec{ifx: 400, title: 150}
	})
	swapAPI(t, httptest.NewServer(a.handler(t)))
	text := callText(t)(checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "fixes", Version: "v2.0.0"}, {Project: "severe", Version: "v1.0.0"}}}))
	assertWithin(t, text)
	var out struct {
		Components []briefOut `json:"components"`
	}
	json.Unmarshal([]byte(text), &out)
	fixes, severe := out.Components[0], out.Components[1]
	if severe.ChangesUnlisted != nil {
		t.Errorf("severe advisories were dropped while another component's important fixes were listed: %v", severe.ChangesUnlisted)
	}
	if fixes.ChangesUnlisted["important_fixes"] == 0 {
		t.Fatal("the case must drop important fixes")
	}
	if n := len(fixes.Changes) + len(fixes.ChangesMore); n < protectedLines {
		t.Errorf("a component keeps five lines of its highest layer: %d", n)
	}
	assertAccounted(t, fixes, nil)
	assertAccounted(t, severe, nil)
}

// What fitting adds to hint is one sentence of names and one of how, within
// maxHint however many components there are.
func TestHintStaysWithinItsBoundWithManyNames(t *testing.T) {
	var units []*fitUnit
	for i := 0; i < 100; i++ {
		b := &briefing{ChangesUnlisted: map[string]int{"breaking": 1}}
		u := &fitUnit{name: fmt.Sprintf("a-rather-long-project-name-%03d", i), comp: &fitComp{b: b}}
		if i%3 == 0 {
			u.stubLevel = 1
		}
		units = append(units, u)
	}
	h := boundHint(stackHint, stackExtra(units))
	if n := utf8.RuneCountInString(h); n > maxHint {
		t.Errorf("hint %d characters", n)
	}
	if !strings.Contains(h, "more)") || !strings.Contains(h, "To list them") {
		t.Errorf("hint %s", h[len(stackHint):])
	}
}

// later_in_range carries the problems of the releases in range only — an
// advisory_after_release there is for a vulnerability that release already
// fixes (cilium v1.16.18's range: 25 of 34 events).
func TestLaterInRangeCarriesOnlyProblems(t *testing.T) {
	a := newBigAPI()
	a.add("p", []string{"1.0"}, 3, func(ver string) relSpec { return relSpec{later: 4} })
	swapAPI(t, httptest.NewServer(a.handler(t)))
	text := callText(t)(checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "p", From: "v1.0.0"}))
	var out struct {
		LaterInRange []map[string]any `json:"later_in_range"`
	}
	json.Unmarshal([]byte(text), &out)
	if len(out.LaterInRange) != 4 {
		t.Errorf("later_in_range %d events, want the 4 regressions of two releases", len(out.LaterInRange))
	}
	for _, e := range out.LaterInRange {
		if e["type"] != "regression_fixed" {
			t.Errorf("later_in_range carries %v", e["type"])
		}
	}
}

// check_upgrade answers when the reads that settle what from's branch fixed
// fail — as 1.1.0 did, with nothing settled and a note — and detail "full"
// does not read the list unless a build suffix or same_minor needs it.
func TestCheckUpgradeAnswersWhenTheExtraReadsFail(t *testing.T) {
	for _, failing := range []string{"/v2/releases", "/v2/releases/etcd/v3.5.30"} {
		f := newFakeEtcd()
		h := f.handler(t)
		swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == failing {
				http.Error(w, "busy", http.StatusServiceUnavailable)
				return
			}
			h(w, r)
		})))
		text := callText(t)(checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30"}))
		var out struct {
			Note     string            `json:"note"`
			Excluded *excludedOnBranch `json:"excluded_on_branch"`
			Changes  []briefChange     `json:"changes"`
		}
		json.Unmarshal([]byte(text), &out)
		if !strings.Contains(out.Note, "are not left out (as in 1.1.0)") || out.Excluded != nil {
			t.Errorf("%s failing: note %q, excluded %+v", failing, out.Note, out.Excluded)
		}
		if res, _, _ := checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30", SameMinor: true}); failing == "/v2/releases" && !res.IsError {
			t.Error("same_minor cannot be answered without the list")
		}
	}
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	callText(t)(checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30", Detail: "full"}))
	for _, u := range f.requests() {
		if strings.HasPrefix(u, "/v2/releases") {
			t.Errorf("detail full read %s", u)
		}
	}
	text := callText(t)(checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.32", Detail: "full", SameMinor: true}))
	if !strings.Contains(text, `"note":"same_minor: no release above v3.5.32`) {
		t.Errorf("full with same_minor and nothing newer: %.300s", text)
	}
}

// A build suffix is read off when the version as passed is no release on
// record and the version without it is: v3.6.7-cbec91f, whose line is the
// main one, was read as a pre-release of v3.6.7 and listed v3.6.7 itself.
// A pre-release stays as passed.
func TestBuildSuffixIsReadOffOnlyWhenTheVersionIsNotOnRecord(t *testing.T) {
	f := newFakeEtcd()
	f.rows = append([]v2Row{{Project: "etcd", Version: "v3.7.0"}, {Project: "etcd", Version: "v3.7.0-rc.1"}}, f.rows...)
	f.docs["v3.7.0"] = doc("v3.7.0", "", "", "", "")
	f.docs["v3.7.0-rc.1"] = doc("v3.7.0-rc.1", "", "", "", "")
	swapAPI(t, httptest.NewServer(f.handler(t)))
	text := callText(t)(checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{
		{Project: "etcd", Version: "v3.6.7-cbec91f"},
		{Project: "etcd", Version: "v3.7.0-rc.1"},
		{Project: "etcd", Version: "v2.0.0-beta.1"},
	}}))
	var out struct {
		Components []struct {
			RunningVersion string `json:"running_version"`
			VersionReadAs  string `json:"version_read_as"`
			Releases       []struct {
				Version string `json:"version"`
			} `json:"releases"`
		} `json:"components"`
	}
	json.Unmarshal([]byte(text), &out)
	c := out.Components[0]
	var vers []string
	for _, r := range c.Releases {
		vers = append(vers, r.Version)
	}
	if c.VersionReadAs != "v3.6.7" || slices.Contains(vers, "v3.6.7") || !slices.Contains(vers, "v3.6.8") {
		t.Errorf("v3.6.7-cbec91f: read as %q, range %v", c.VersionReadAs, vers)
	}
	for _, c := range out.Components[1:] {
		if c.VersionReadAs != "" {
			t.Errorf("a pre-release was read as %s", c.VersionReadAs)
		}
	}
	text = callText(t)(checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.6.7-cbec91f"}))
	if !strings.Contains(text, `"version_read_as":"v3.6.7"`) || !slices.Contains(f.requests(), "/v2/upgrade/etcd?from=v3.6.7") {
		t.Errorf("check_upgrade: %.200s", text)
	}
}

// get_release without version reads every release the ids name, and changes
// with kinds is refused.
func TestGetReleaseReadsTheReleasesTheIDsName(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	text := callText(t)(getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Changes: stringList{
		"etcd@v3.6.7#a", "etcd@v3.6.8#b", "etcd@v3.6.7#g", "etcd@v3.6.7#zzz", "etcd@v9.9.9#x", "envoy@v1.0.0#x", "not-an-id",
	}}))
	var out struct {
		Project  string `json:"project"`
		Releases []struct {
			Version       string            `json:"version"`
			URL           string            `json:"url"`
			Changes       []json.RawMessage `json:"changes"`
			BeforeUpgrade json.RawMessage   `json:"before_upgrade"`
		} `json:"releases"`
		NotFound []string `json:"not_found"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Project != "etcd" || len(out.Releases) != 2 || out.Releases[0].Version != "v3.6.7" || out.Releases[1].Version != "v3.6.8" {
		t.Fatalf("%s", text)
	}
	if r := out.Releases[0]; len(r.Changes) != 1 || r.BeforeUpgrade == nil || r.URL == "" {
		t.Errorf("v3.6.7: %+v", r)
	}
	if r := out.Releases[1]; len(r.Changes) != 1 || r.BeforeUpgrade != nil {
		t.Errorf("v3.6.8 names no before-upgrade item: %+v", r)
	}
	if want := []string{"envoy@v1.0.0#x", "not-an-id", "etcd@v3.6.7#zzz", "etcd@v9.9.9#x"}; !slices.Equal(slices.Sorted(slices.Values(out.NotFound)), slices.Sorted(slices.Values(want))) {
		t.Errorf("not_found %v", out.NotFound)
	}
	res, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Version: "v3.6.7", Kinds: stringList{"security"}, Changes: stringList{"etcd@v3.6.7#k"}})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, "not both") {
		t.Errorf("changes with kinds: %s", text)
	}
	var many stringList
	for i := 0; i <= maxPickedReleases; i++ {
		many = append(many, fmt.Sprintf("etcd@v3.6.%d#a", 20+i))
	}
	if res, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Changes: many}); !res.IsError {
		t.Error("ids of more releases than the bound were read")
	}
}

// A release the server matched through a folded dependency line is not read.
func TestSearchDoesNotReadAReleaseMatchedByAFoldedLine(t *testing.T) {
	f := newFakeEtcd()
	answer := `{"query":"golang.org/x/net","in_analysis":[{"project":"etcd","version":"v3.6.7","matched":{"kind":"dependency_updates","title":null}}],"in_release_notes":[],"in_linked_documents":[]}`
	h := f.handler(t)
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/search" {
			w.Write([]byte(answer))
			return
		}
		h(w, r)
	})))
	text := callText(t)(searchTool(context.Background(), nil, searchArgs{Q: "golang.org/x/net"}))
	if strings.Contains(text, `"matches"`) || !strings.Contains(text, `"matches_not_computed":0`) {
		t.Errorf("%s", text)
	}
	for _, u := range f.requests() {
		if strings.HasPrefix(u, "/v2/releases/") {
			t.Errorf("read %s", u)
		}
	}
}

// Inside a layer each component gives up the same share of its items at a
// time: halfway through a layer, a component with 4 items has lost 2 and one
// with 40 has lost 20.
func TestLadderTakesTheSameShareFromEachComponent(t *testing.T) {
	mk := func(n int) *fitComp {
		d := &v2Release{Version: "v1.0.0"}
		for i := 0; i < n; i++ {
			d.Changes = append(d.Changes, v2Change{ID: fmt.Sprint("x", n, "#", i), Kind: "important_fixes"})
		}
		b, _ := buildBriefing([]*v2Release{d}, nil, nil)
		return newFitComp(&b)
	}
	small, large := mk(4), mk(40)
	var bare []fitStep
	for _, st := range ladder([]*fitComp{small, large}) {
		if st.do == stepBare {
			bare = append(bare, st)
		}
	}
	lost := map[*fitComp]int{}
	for _, st := range bare[:22] {
		for _, f := range []*fitComp{small, large} {
			if slices.Contains(f.shown, st.it) {
				lost[f]++
			}
		}
	}
	if lost[small] != 2 || lost[large] != 20 {
		t.Errorf("halfway: small lost %d of 4, large %d of 40", lost[small], lost[large])
	}
}
