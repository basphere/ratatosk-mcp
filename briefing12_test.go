package main

// What 1.2.0 adds to a briefing: changes_more under the
// answer's size budget, before_upgrade with bodies and grouped texts,
// later_in_range, excluded_on_branch, version_read_as and same_minor, and
// check_upgrade settling what from's branch already fixed. Each case replays
// a measured answer (docs/design/mcp-answer-shape.md in the ratatosk app
// repository, 2026-11-19 and 11-20).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ptr(s string) *string { return &s }

// containerd v2.2.9 → v2.4.2 carried 17 before-upgrade items for 8 texts, and
// cilium v1.20.0's prerequisite had no body and its warning (Gateway API v1.6.1
// before upgrading, or TLSRoute objects are lost) only in folded.
func TestBeforeUpgradeCarriesBodiesFoldedAndGroupsRecurringTexts(t *testing.T) {
	item := func(id, title string, body *string, folded string) v2Item {
		it := v2Item{ID: id, Title: title, Body: body}
		if folded != "" {
			it.Folded = []json.RawMessage{json.RawMessage(folded)}
		}
		return it
	}
	rel := func(v string, pre ...v2Item) *v2Release {
		d := &v2Release{Version: v}
		d.BeforeUpgrade.Prerequisites = pre
		return d
	}
	docs := []*v2Release{
		rel("v2.3.1", item("c@v2.3.1#4f6a2055", "runc and CNI (2.3.1 wording)", ptr("install them separately"), "")),
		rel("v2.3.3", item("c@v2.3.3#4f6a2055", "runc and CNI (2.3.3 wording)", nil, ""), item("c@v2.3.3#6ddd209f", "glibc archive", nil, "")),
		rel("v2.4.0", item("c@v2.4.0#c62eff94", "legacy features", nil, `{"title":"Gateway API v1.6.1","body":"upgrade Gateway API to v1.6.1 before upgrading, or TLSRoute objects are lost"}`)),
		rel("v2.4.2", item("c@v2.4.2#4f6a2055", "runc and CNI (2.4.2 wording)", ptr("the newest wording"), "")),
	}
	got := beforeUpgradeOf(docs)
	var ids []string
	for _, it := range got {
		ids = append(ids, it.ID)
	}
	if want := []string{"c@v2.3.3#6ddd209f", "c@v2.4.0#c62eff94", "c@v2.4.2#4f6a2055"}; !slices.Equal(ids, want) {
		t.Fatalf("items %v, want %v (a recurring text once, at its newest release)", ids, want)
	}
	if g := got[2]; !slices.Equal(g.AlsoIn, []string{"v2.3.1", "v2.3.3"}) || g.Title != "runc and CNI (2.4.2 wording)" || *g.Body != "the newest wording" {
		t.Errorf("grouped item %+v", g)
	}
	if got[0].AlsoIn != nil {
		t.Errorf("a text on one release names no other: %v", got[0].AlsoIn)
	}
	b, _ := json.Marshal(got[1])
	for _, want := range []string{`"body":null`, `"folded":[{"title":"Gateway API v1.6.1","body":"upgrade Gateway API to v1.6.1 before upgrading, or TLSRoute objects are lost"}]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("no %s in %s", want, b)
		}
	}
	// A long body goes out whole: the instruction sits at its end.
	long := strings.Repeat("가", 300) + " — upgrade Gateway API first"
	if got := beforeUpgradeOf([]*v2Release{rel("v1", item("x@v1#a", "t", &long, ""))}); *got[0].Body != long {
		t.Error("a before-upgrade body was cut")
	}
	if got := beforeUpgradeOf(nil); got == nil || len(got) != 0 {
		t.Errorf("no items is [], not null: %#v", got)
	}
}

// Every change the caps cut is listed in one line; a sibling folded into an
// entry of its own release is listed too (coredns v1.14.7 → v1.15.0: eight
// security changes, five listed, nothing omitted), a backport's sibling stands
// under also_in with the backport.
func TestBriefingListsWhatDidNotFitInOneLine(t *testing.T) {
	sec := func(id string, title string, advs ...string) v2Change {
		return v2Change{ID: id, Kind: "security", Title: title, Advisories: advs, Affects: &v2Attr{Text: "x", Status: "stated", Reason: "r"}}
	}
	go1273 := []string{"CVE-2026-111943", "CVE-2026-33818"}
	coredns := &v2Release{Version: "v1.15.0", Changes: []v2Change{
		sec("c#asn1", "ASN.1 depth", append(slices.Clone(go1273), "GO-2026-5972")...),
		sec("c#tls", "TLS policies"),
		sec("c#h2", "HTTP/2 crash", go1273...),
		sec("c#trailer", "trailer memory", go1273...),
		sec("c#x509", "x509 intermediates", go1273...),
	}}
	ar := "migrate"
	for i := 0; i < 25; i++ {
		c := v2Change{ID: fmt.Sprint("c#b", i), Kind: "breaking", Title: "b"}
		if i == 24 {
			c.ActionRequired = &ar
		}
		coredns.Changes = append(coredns.Changes, c)
	}
	backport := &v2Release{Version: "v1.15.1", Changes: []v2Change{sec("c#h2-bp", "HTTP/2 crash (1.15)", go1273...), sec("c#trailer-bp", "trailer (1.15)", go1273...)}}
	// v1.15.1 is the same branch as v1.15.0; make it another branch.
	backport.Version = "v1.16.0"

	b, settled := buildBriefing([]*v2Release{coredns, backport}, nil, nil)
	if settled != 0 {
		t.Fatalf("settled %d", settled)
	}
	var entries, lines []string
	for _, c := range b.Changes {
		entries = append(entries, c.ID)
	}
	for _, l := range b.ChangesMore {
		lines = append(lines, l.ID)
	}
	for _, id := range []string{"c#trailer", "c#x509"} {
		if !slices.Contains(lines, id) {
			t.Errorf("the folded sibling %s is not in changes_more %v", id, lines)
		}
	}
	for _, id := range []string{"c#h2-bp", "c#trailer-bp"} {
		if slices.Contains(lines, id) || slices.Contains(entries, id) {
			t.Errorf("the backport and its sibling stand under also_in, not in a list: %s", id)
		}
	}
	if b.ChangesOmitted["breaking"] != 5 {
		t.Fatalf("omitted %v", b.ChangesOmitted)
	}
	nb := 0
	for _, l := range b.ChangesMore {
		if l.Kind == "breaking" {
			nb++
			if l.Severity != nil || l.ActionRequired {
				t.Errorf("a breaking line without action_required: %+v", l)
			}
		}
		if l.Kind == "security" {
			if string(l.Severity) != "null" {
				t.Errorf("a security line says severity null when no grade was found: %s", l.Severity)
			}
			if l.Affects == nil || *l.Affects != (lineAttr{Text: "x", Status: "stated"}) {
				t.Errorf("line %s affects %+v", l.ID, l.Affects)
			}
		} else if l.Affects != nil {
			t.Errorf("a change without affects has none in its line: %+v", l)
		}
	}
	if nb != 5 {
		t.Errorf("%d breaking lines for 5 omitted", nb)
	}
	if !slices.Contains(entries, "c#b24") {
		t.Error("the breaking change with action_required must lead and stay in changes")
	}
	j, _ := json.Marshal(b.ChangesMore[0])
	if strings.Contains(string(j), "reason") || strings.Contains(string(j), "body") {
		t.Errorf("a line is one line: %s", j)
	}
}

// changeDoc is a release document with n changes of each kind given, titles
// long enough to make the answer large.
func changeDoc(project, ver string, n map[string]int, titleLen int) string {
	var cs []string
	title := strings.Repeat("t", titleLen)
	for _, kind := range kinds {
		for i := 0; i < n[kind]; i++ {
			c := fmt.Sprintf(`{"id":"%s@%s#%s%d","kind":%q,"title":"%s %s %d","body":"b","severity":null,"advisories":["CVE-2026-%d"],`+
				`"affects":{"text":"every install","status":"not_stated"},"evidence":[{"quote":"q","source":"release_notes","url":"u"}],"later":[]}`,
				project, ver, kind, i, kind, kind, title, i, 10000+i)
			if kind != "security" {
				c = strings.Replace(c, `"severity":null,"advisories":["CVE-2026-`+fmt.Sprint(10000+i)+`"],`, ``, 1)
			}
			cs = append(cs, c)
		}
	}
	return fmt.Sprintf(`{"project":%q,"version":%q,"released_at":"2026-07-01T00:00:00Z","url":"u","counts":{},"warnings":{"critical":0,"no_downgrade":0},`+
		`"before_upgrade":{"prerequisites":[],"known_issues":[],"upgrade_guide":[]},"changes":[%s],"later":[]}`, project, ver, strings.Join(cs, ","))
}

func upgradeServer(t *testing.T, docs ...string) {
	t.Helper()
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/releases":
			w.Write([]byte(`{"releases":[],"next_before":null}`))
		case strings.HasPrefix(r.URL.Path, "/v2/upgrade/"):
			fmt.Fprintf(w, `{"project":"p","from":"v0.9.0","to":"v1.0.0","releases":[%s],"gaps":[],"later_for_from":[]}`, strings.Join(docs, ","))
		default:
			t.Errorf("unexpected upstream request %s", r.URL.RequestURI())
		}
	})))
}

type upgradeOut struct {
	Changes        []briefChange  `json:"changes"`
	ChangesOmitted map[string]int `json:"changes_omitted"`
	ChangesMore    []moreLine     `json:"changes_more"`
	Hint           string         `json:"hint"`
}

func runUpgrade(t *testing.T, args checkUpgradeArgs) (upgradeOut, string) {
	t.Helper()
	res, _, err := checkUpgradeTool(context.Background(), nil, args)
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	var out upgradeOut
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out, text
}

// However long the range, every security, breaking and deprecation change has
// its entry or its line: the answer is not cut to a size. An earlier build of
// 1.2.0 kept answers within 40,000 characters and, on a five-component stack
// years behind, answered none of kubernetes' 33 security changes (2026-11-20).
// Important fixes past their cap stay a count unless kinds asks for them.
func TestBriefingLeavesOutNoDecisionChangeHoweverLong(t *testing.T) {
	n := map[string]int{"security": 150, "breaking": 300, "deprecations": 40, "important_fixes": 200}
	upgradeServer(t, changeDoc("p", "v1.0.0", n, 120))
	out, text := runUpgrade(t, checkUpgradeArgs{Project: "p", From: "v0.9.0"})
	if utf8.RuneCountInString(text) < 60000 {
		t.Fatalf("the fixture is meant to be a long answer, got %d characters", utf8.RuneCountInString(text))
	}
	count := func(o upgradeOut) (entries, lines map[string]int) {
		entries, lines = map[string]int{}, map[string]int{}
		for _, c := range o.Changes {
			entries[c.Kind]++
		}
		for _, l := range o.ChangesMore {
			lines[l.Kind]++
		}
		return
	}
	entries, lines := count(out)
	for _, kind := range []string{"security", "breaking", "deprecations"} {
		if entries[kind] != kindCaps[kind] || entries[kind]+lines[kind] != n[kind] {
			t.Errorf("%s: %d entries + %d lines, want all %d (entries %d)", kind, entries[kind], lines[kind], n[kind], kindCaps[kind])
		}
		if out.ChangesOmitted[kind] != lines[kind] {
			t.Errorf("%s: changes_omitted %d, lines %d", kind, out.ChangesOmitted[kind], lines[kind])
		}
	}
	if entries["important_fixes"] != 10 || lines["important_fixes"] != 0 || out.ChangesOmitted["important_fixes"] != 190 {
		t.Errorf("important fixes: %d entries, %d lines, omitted %d — want 10, 0, 190", entries["important_fixes"], lines["important_fixes"], out.ChangesOmitted["important_fixes"])
	}

	upgradeServer(t, changeDoc("p", "v1.0.0", n, 120))
	out, _ = runUpgrade(t, checkUpgradeArgs{Project: "p", From: "v0.9.0", Kinds: stringList{"important_fixes"}})
	entries, lines = count(out)
	if entries["important_fixes"] != 10 || lines["important_fixes"] != 190 || len(out.Changes)+len(out.ChangesMore) != 200 {
		t.Errorf("kinds important_fixes: %d entries, %d lines of %d in all — want 10 and 190, nothing else", entries["important_fixes"], lines["important_fixes"], len(out.Changes)+len(out.ChangesMore))
	}
}

// distinct_advisories is read before counts and warnings: an agent quoted
// warnings.critical 15 for five advisories (field report, 2026-11-20).
func TestSummaryLeadsWithDistinctAdvisories(t *testing.T) {
	upgradeServer(t, changeDoc("p", "v1.0.0", map[string]int{"security": 2}, 10))
	_, text := runUpgrade(t, checkUpgradeArgs{Project: "p", From: "v0.9.0"})
	at := strings.Index(text, `"summary":{`)
	if at < 0 {
		t.Fatalf("no summary in %s", text)
	}
	sum := text[at:]
	d, c, w := strings.Index(sum, `"distinct_advisories"`), strings.Index(sum, `"counts"`), strings.Index(sum, `"warnings"`)
	if d < 0 || c < 0 || w < 0 || d > c || d > w {
		t.Errorf("summary order: distinct_advisories at %d, counts at %d, warnings at %d", d, c, w)
	}
}

// excluded_on_branch names at most 20 advisories, the most severe; security
// still counts every change left out.
func TestExcludedOnBranchNamesTwentyAdvisories(t *testing.T) {
	high := "high"
	var past, cur []v2Change
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("CVE-2026-%d", 100+i)
		c := v2Change{ID: fmt.Sprint("s", i), Kind: "security", Advisories: []string{id}}
		if i >= 25 {
			c.Severity = &high
		}
		cur = append(cur, c)
		past = append(past, c)
	}
	fixed := map[string]bool{}
	for _, c := range past {
		fixed[c.Advisories[0]] = true
	}
	b, settled := buildBriefing([]*v2Release{{Version: "v2.0.0", Changes: cur}}, fixed, []int{1, 0, 0})
	ex := b.ExcludedOnBranch
	if settled != 30 || ex == nil || ex.Security != 30 || len(ex.Advisories) != maxExcludedAdvisories || ex.AdvisoriesOmitted != 10 {
		t.Fatalf("excluded %+v", ex)
	}
	if string(ex.Advisories[0].Severity) != `"high"` || string(ex.Advisories[4].Severity) != `"high"` || string(ex.Advisories[5].Severity) != "null" {
		t.Errorf("the most severe are kept: %+v", ex.Advisories[:6])
	}
}

// check_upgrade reads an image build suffix off from as check_stack does;
// /v2/upgrade answered such a from with a 404.
func TestCheckUpgradeReadsABuildSuffixOffFrom(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	tag := "v3.5.30-1789133542-cbec91f666af0bf742da986d43832932dbb26b82"
	res, _, _ := checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: tag})
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError || !strings.Contains(text, `"version_read_as":"v3.5.30"`) || !strings.Contains(text, `"from":"`+tag+`"`) {
		t.Errorf("isError=%v %.300s", res.IsError, text)
	}
	if reqs := strings.Join(f.requests(), " "); !strings.Contains(reqs, "/v2/upgrade/etcd?from=v3.5.30 ") && !strings.HasSuffix(reqs, "/v2/upgrade/etcd?from=v3.5.30") {
		t.Errorf("from sent upstream: %s", reqs)
	}
	res, _, _ = checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: tag, Detail: "full"})
	if text := res.Content[0].(*mcp.TextContent).Text; res.IsError || !strings.Contains(text, `"version_read_as":"v3.5.30"`) {
		t.Errorf("full: %.300s", text)
	}
}

// check_upgrade settles what from's branch already fixed, as check_stack does:
// containerd v2.2.9 → v2.4.2 omitted 42 security changes there and 30 here,
// with 13 excluded (2026-11-20). The same range now gives the same lists.
func TestCheckUpgradeSettlesWhatFromsBranchFixedLikeCheckStack(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	stack := runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}}).Components[0]
	res, _, _ := checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30"})
	text := res.Content[0].(*mcp.TextContent).Text
	var up struct {
		Changes          []briefChange     `json:"changes"`
		ExcludedOnBranch *excludedOnBranch `json:"excluded_on_branch"`
		LaterInRange     []json.RawMessage `json:"later_in_range"`
	}
	if err := json.Unmarshal([]byte(text), &up); err != nil || res.IsError {
		t.Fatalf("%v %s", err, text)
	}
	var a, b []string
	for _, c := range stack.Changes {
		a = append(a, c.ID)
	}
	for _, c := range up.Changes {
		b = append(b, c.ID)
	}
	if !slices.Equal(a, b) {
		t.Errorf("check_upgrade %v, check_stack %v", b, a)
	}
	ex := up.ExcludedOnBranch
	if ex == nil || ex.Security != 1 || len(ex.Advisories) != 1 || ex.Advisories[0].ID != "GHSA-bbbb" || string(ex.Advisories[0].Severity) != "null" {
		t.Errorf("excluded_on_branch %+v", ex)
	}
	if up.LaterInRange == nil {
		t.Error("later_in_range is [] when nothing is recorded, never absent")
	}
	// Only the releases at or below from on its branch are read for it.
	g := newFakeEtcd()
	swapAPI(t, httptest.NewServer(g.handler(t)))
	checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30"})
	var docs []string
	for _, u := range g.requests() {
		if strings.HasPrefix(u, "/v2/releases/") {
			docs = append(docs, u)
		}
	}
	if !slices.Equal(docs, []string{"/v2/releases/etcd/v3.5.30"}) {
		t.Errorf("documents read %v, want from's own branch at or below it", docs)
	}
}

// later_in_range carries what was learned about the releases in range: the
// v1.20.3 BackendTLSPolicy regression fixed in v1.20.4 was in no briefing.
func TestLaterInRangeCarriesWhatWasLearnedAboutTheRange(t *testing.T) {
	f := newFakeEtcd()
	ev := `{"seq":2,"at":"2026-08-02T00:00:00Z","type":"regression_fixed","project":"etcd","version":"v3.6.7","fixed_in":"v3.6.8","title":"lease regression"}`
	d := f.docs["v3.6.7"]
	i := strings.LastIndex(d, `"later":[]}`) // the release's own later, after its changes'
	f.docs["v3.6.7"] = d[:i] + `"later":[` + ev + `]}`
	swapAPI(t, httptest.NewServer(f.handler(t)))
	res, _, _ := checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}, {Project: "etcd", Version: "v3.6.8"}}})
	var out struct {
		Components []struct {
			LaterInRange    *[]json.RawMessage `json:"later_in_range"`
			LaterForRunning []json.RawMessage  `json:"later_for_running"`
			BeforeUpgrade   *[]upgradeItem     `json:"before_upgrade"`
		} `json:"components"`
	}
	json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out)
	c := out.Components[0]
	if c.LaterInRange == nil || len(*c.LaterInRange) != 1 || !strings.Contains(string((*c.LaterInRange)[0]), "lease regression") {
		t.Errorf("later_in_range %v", c.LaterInRange)
	}
	if len(c.LaterForRunning) != 1 {
		t.Errorf("later_for_running stays the running version's: %s", c.LaterForRunning)
	}
	if e := out.Components[1]; e.LaterInRange == nil || len(*e.LaterInRange) != 0 || e.BeforeUpgrade == nil || len(*e.BeforeUpgrade) != 0 {
		t.Errorf("an empty range answers later_in_range [] and before_upgrade [], not absent: %+v", e)
	}
}

// cilium-envoy publishes v1.37.6-1789133542-<commit>; read whole it named line
// "v1.37.6" and failed with "the release-line prefix was probably dropped".
func TestImageBuildSuffixIsReadWithoutItAndSaid(t *testing.T) {
	for in, want := range map[string]string{
		"v1.37.6-1789133542-cbec91f666af0bf742da986d43832932dbb26b82": "v1.37.6",
		"v1.37.6-cbec91f":       "v1.37.6",
		"lts-4081.3.9-abc1234":  "lts-4081.3.9",
		"flagd/v0.16.1-9f8e7d6": "flagd/v0.16.1",
	} {
		if got, ok := stripBuildTail(in); !ok || got != want {
			t.Errorf("stripBuildTail(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"v1.24.3-rc1", "v1.2.3-beta.2", "v1.2.3-20240101", "v1.2.3-deadbee", "v1.2.3", "knative-v1.12.0", "lts-4081.3.9"} {
		if got, ok := stripBuildTail(in); ok {
			t.Errorf("stripBuildTail(%q) = %q: not a build suffix", in, got)
		}
	}

	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	out := runStack(t, checkStackArgs{Components: []stackComponent{
		{Project: "etcd", Version: "v3.6.7-1789133542-cbec91f666af0bf742da986d43832932dbb26b82"},
		{Project: "etcd", Version: "v3.6.7-20240101-xyz"},
	}})
	res, _, _ := checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.6.7-1789133542-cbec91f666af0bf742da986d43832932dbb26b82"}}})
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, `"version_read_as":"v3.6.7"`) {
		t.Errorf("version_read_as missing: %.300s", text)
	}
	if c := out.Components[0]; len(c.Releases) != 1 || c.Releases[0].Version != "v3.6.8" || c.RunningVersion != "v3.6.7-1789133542-cbec91f666af0bf742da986d43832932dbb26b82" {
		t.Errorf("read as v3.6.7: %+v", c)
	}
	if len(out.CouldNotCheck) != 1 || !strings.Contains(out.CouldNotCheck[0].Reason, `reads as v3.6.7 followed by "-20240101-xyz"`) ||
		strings.Contains(out.CouldNotCheck[0].Reason, "prefix was probably dropped") {
		t.Errorf("a suffix that cannot be read off is named as such: %+v", out.CouldNotCheck)
	}
}

// same_minor stops at the newest patch of the running minor; check_upgrade
// sends that as to.
func TestSameMinorStopsAtTheRunningMinor(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	out := runStack(t, checkStackArgs{Components: []stackComponent{
		{Project: "etcd", Version: "v3.5.30", SameMinor: true},
		{Project: "etcd", Version: "v3.5.32", SameMinor: true},
	}})
	var vers []string
	for _, r := range out.Components[0].Releases {
		vers = append(vers, r.Version)
	}
	if !slices.Equal(vers, []string{"v3.5.31", "v3.5.32"}) || out.Components[0].TargetVersion != "v3.5.32" {
		t.Errorf("same_minor from v3.5.30: %v → %s", vers, out.Components[0].TargetVersion)
	}
	if c := out.Components[1]; len(c.Releases) != 0 || !strings.Contains(c.Note, "same_minor: no release above v3.5.32 on 3.5") || !strings.Contains(c.Note, "v3.6.8") {
		t.Errorf("nothing newer on the minor: %+v", c)
	}
	res, _, _ := checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30", SameMinor: true, TargetVersion: "v3.6.7"}}})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, "sets both same_minor and target_version") {
		t.Errorf("both ends at once: %s", text)
	}

	runUpgradeEtcd := func(args checkUpgradeArgs) string {
		res, _, _ := checkUpgradeTool(context.Background(), nil, args)
		return res.Content[0].(*mcp.TextContent).Text
	}
	runUpgradeEtcd(checkUpgradeArgs{Project: "etcd", From: "v3.5.30", SameMinor: true})
	if reqs := strings.Join(f.requests(), " "); !strings.Contains(reqs, "/v2/upgrade/etcd?from=v3.5.30&to=v3.5.32") {
		t.Errorf("check_upgrade same_minor asked %s", reqs)
	}
	if text := runUpgradeEtcd(checkUpgradeArgs{Project: "etcd", From: "v3.5.32", SameMinor: true}); !strings.Contains(text, `"releases":[]`) || !strings.Contains(text, "same_minor: no release above v3.5.32") {
		t.Errorf("nothing newer on the minor: %.400s", text)
	}
}

// The text an agent reads says what a release line is, and where to read
// evidence now that a briefing has none.
func TestDescriptionsSayWhatALineIsAndWhereEvidenceIs(t *testing.T) {
	d := toolDescriptions(t)
	for _, name := range []string{"check_stack", "check_upgrade"} {
		for _, want := range []string{"not a minor version", "same_minor", "changes_more", "get_release(project, version, changes:[id, …])"} {
			if !strings.Contains(d[name], want) {
				t.Errorf("%s description does not say %q", name, want)
			}
		}
		if strings.Contains(d[name], "the latest on the same release line") {
			t.Errorf("%s still says \"the latest on the same release line\" without saying what a line is", name)
		}
	}
	// hint keeps what reading the answer needs, within 2,000 characters; the
	// meanings it dropped are in the description (4,429 characters rode on
	// every briefing, 2026-11-20).
	if n := utf8.RuneCountInString(stackHint); n > 2000 {
		t.Errorf("hint is %d characters, at most 2,000", n)
	}
	for _, want := range []string{"ONLY IF", "What is not listed is not absent", "changes_more", `kinds:["important_fixes"]`,
		"get_release(project, version, changes:[id, …])", "no evidence", "does not mean the release is vulnerable", "later_in_range"} {
		if !strings.Contains(stackHint, want) {
			t.Errorf("hint does not say %q", want)
		}
	}
	for _, want := range []string{"excluded_on_branch", "version_read_as", "also_in naming the others", "security_unclear", "no_downgrade means",
		"removed_in is when", "also_required (security) are what the notes say to do", "none of those three kinds is left out", "The answer is not cut to a size",
		"Security changes citing no advisory id are not in distinct_advisories", "recorded on that version, not here", "null when no grade was found"} {
		if !strings.Contains(d["check_stack"], want) {
			t.Errorf("check_stack description does not say %q", want)
		}
	}
	if strings.Contains(stackHint, "never read an omitted count as nothing to do") {
		t.Error("the hint still sends the agent to read whole releases for what changes_more lists")
	}
	for _, want := range []string{"kinds", "changes:[id, …]", "not_found"} {
		if !strings.Contains(d["get_release"], want) {
			t.Errorf("get_release description does not say %q", want)
		}
	}
	for _, want := range []string{"project keeps one project's releases", "matches"} {
		if !strings.Contains(d["search"], want) {
			t.Errorf("search description does not say %q", want)
		}
	}
}
