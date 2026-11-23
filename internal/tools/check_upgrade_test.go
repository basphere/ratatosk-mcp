package tools

// check_upgrade: the server's range, briefed as check_stack briefs it.

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

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

func TestCheckUpgradeComputesGapsAnOlderServerOmits(t *testing.T) {
	var seen string
	h := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/releases" { // the releases at or below from (1.2.0: what from's branch already fixed)
			w.Write([]byte(`{"releases":[],"next_before":null}`))
			return
		}
		seen = r.URL.RequestURI()
		fmt.Fprintf(w, `{"project":"etcd","from":"v3.5.30","to":"v3.6.7","releases":[%s,%s],"later_for_from":[]}`,
			upstreamtest.Doc("v3.5.31", "", "", "", ""), upstreamtest.Doc("v3.6.7", "", "", "", ""))
	})))
	res, _, err := h.checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30"})
	if err != nil || res.IsError {
		t.Fatalf("check_upgrade: %v %+v", err, res)
	}
	if seen != "/v2/upgrade/etcd?from=v3.5.30" {
		t.Errorf("request %s", seen)
	}
	var out struct {
		Gaps    []releaseline.Gap `json:"gaps"`
		Privacy string            `json:"privacy"`
	}
	json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out)
	if !slices.Equal(out.Gaps, []releaseline.Gap{{After: "v3.5.31", Before: "v3.6.7"}}) {
		t.Errorf("gaps %v", out.Gaps)
	}
	if !strings.Contains(out.Privacy, "sent to the server") {
		t.Errorf("check_upgrade must say the versions went upstream: %q", out.Privacy)
	}
}

// However long the range, every security, breaking and deprecation change has
// its entry or its line: the answer is not cut to a size. An earlier build of
// 1.2.0 kept answers within 40,000 characters and, on a five-component stack
// years behind, answered none of kubernetes' 33 security changes (2026-11-20).
// Important fixes past their cap stay a count unless kinds asks for them.
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestBriefingLeavesOutNoDecisionChangeHoweverLong(t *testing.T) {
	n := map[string]int{"security": 150, "breaking": 300, "deprecations": 40, "important_fixes": 200}
	h := upgradeServer(t, changeDoc("p", "v1.0.0", n, 120))
	out, text := runUpgrade(t, h, checkUpgradeArgs{Project: "p", From: "v0.9.0"})
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
		if entries[kind] != briefing.Cap(kind) || entries[kind]+lines[kind] != n[kind] {
			t.Errorf("%s: %d entries + %d lines, want all %d (entries %d)", kind, entries[kind], lines[kind], n[kind], briefing.Cap(kind))
		}
		if out.ChangesOmitted[kind] != lines[kind] {
			t.Errorf("%s: changes_omitted %d, lines %d", kind, out.ChangesOmitted[kind], lines[kind])
		}
	}
	if entries["important_fixes"] != 10 || lines["important_fixes"] != 0 || out.ChangesOmitted["important_fixes"] != 190 {
		t.Errorf("important fixes: %d entries, %d lines, omitted %d — want 10, 0, 190", entries["important_fixes"], lines["important_fixes"], out.ChangesOmitted["important_fixes"])
	}

	h = upgradeServer(t, changeDoc("p", "v1.0.0", n, 120))
	out, _ = runUpgrade(t, h, checkUpgradeArgs{Project: "p", From: "v0.9.0", Kinds: stringList{"important_fixes"}})
	entries, lines = count(out)
	if entries["important_fixes"] != 10 || lines["important_fixes"] != 190 || len(out.Changes)+len(out.ChangesMore) != 200 {
		t.Errorf("kinds important_fixes: %d entries, %d lines of %d in all — want 10 and 190, nothing else", entries["important_fixes"], lines["important_fixes"], len(out.Changes)+len(out.ChangesMore))
	}
}

// hint does not send a caller that asked for important fixes to ask again.
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestHintKnowsWhenImportantFixesAreListed(t *testing.T) {
	n := map[string]int{"important_fixes": 30}
	h := upgradeServer(t, changeDoc("p", "v1.0.0", n, 10))
	out, _ := runUpgrade(t, h, checkUpgradeArgs{Project: "p", From: "v0.9.0"})
	if !strings.Contains(out.Hint, `call again with kinds:["important_fixes"]`) {
		t.Errorf("default hint does not say how to list important fixes: %s", out.Hint)
	}
	h = upgradeServer(t, changeDoc("p", "v1.0.0", n, 10))
	out, _ = runUpgrade(t, h, checkUpgradeArgs{Project: "p", From: "v0.9.0", Kinds: stringList{"important_fixes"}})
	if strings.Contains(out.Hint, "call again") || !strings.Contains(out.Hint, "nothing of that kind is left out") {
		t.Errorf("hint with kinds important_fixes: %s", out.Hint)
	}
	if n := utf8.RuneCountInString(briefing.Hint(map[string]bool{"important_fixes": true})); n > 2000 {
		t.Errorf("hint is %d characters, at most 2,000", n)
	}
}

// distinct_advisories is read before counts and warnings: an agent quoted
// warnings.critical 15 for five advisories (field report, 2026-11-20).
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestSummaryLeadsWithDistinctAdvisories(t *testing.T) {
	h := upgradeServer(t, changeDoc("p", "v1.0.0", map[string]int{"security": 2}, 10))
	_, text := runUpgrade(t, h, checkUpgradeArgs{Project: "p", From: "v0.9.0"})
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

// check_upgrade reads an image build suffix off from as check_stack does;
// /v2/upgrade answered such a from with a 404.
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestCheckUpgradeReadsABuildSuffixOffFrom(t *testing.T) {
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	tag := "v3.5.30-1789133542-cbec91f666af0bf742da986d43832932dbb26b82"
	res, _, _ := h.checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: tag})
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError || !strings.Contains(text, `"version_read_as":"v3.5.30"`) || !strings.Contains(text, `"from":"`+tag+`"`) {
		t.Errorf("isError=%v %.300s", res.IsError, text)
	}
	if reqs := strings.Join(f.Requests(), " "); !strings.Contains(reqs, "/v2/upgrade/etcd?from=v3.5.30 ") && !strings.HasSuffix(reqs, "/v2/upgrade/etcd?from=v3.5.30") {
		t.Errorf("from sent upstream: %s", reqs)
	}
	res, _, _ = h.checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: tag, Detail: "full"})
	if text := res.Content[0].(*mcp.TextContent).Text; res.IsError || !strings.Contains(text, `"version_read_as":"v3.5.30"`) {
		t.Errorf("full: %.300s", text)
	}
}

// check_upgrade settles what from's branch already fixed, as check_stack does:
// containerd v2.2.9 → v2.4.2 omitted 42 security changes there and 30 here,
// with 13 excluded (2026-11-20). The same range must give the same lists.
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestCheckUpgradeSettlesWhatFromsBranchFixedLikeCheckStack(t *testing.T) {
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	stack := runStack(t, h, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}, Detail: "brief"}).Components[0]
	res, _, _ := h.checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30", Detail: "brief"})
	text := res.Content[0].(*mcp.TextContent).Text
	var up struct {
		Changes          []briefing.Change          `json:"changes"`
		ExcludedOnBranch *briefing.ExcludedOnBranch `json:"excluded_on_branch"`
		LaterInRange     []json.RawMessage          `json:"later_in_range"`
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
	if len(a) == 0 || !slices.Equal(a, b) {
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
	g := upstreamtest.NewEtcd()
	h = withAPI(t, httptest.NewServer(g.Handler(t)))
	h.checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30"})
	var docs []string
	for _, u := range g.Requests() {
		if strings.HasPrefix(u, "/v2/releases/") {
			docs = append(docs, u)
		}
	}
	if !slices.Equal(docs, []string{"/v2/releases/etcd/v3.5.30"}) {
		t.Errorf("documents read %v, want from's own branch at or below it", docs)
	}
}

// same_minor stops at the newest patch of the running minor; check_upgrade
// sends that as to.
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestSameMinorStopsAtTheRunningMinor(t *testing.T) {
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	out := runStack(t, h, checkStackArgs{Components: []stackComponent{
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
	res, _, _ := h.checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30", SameMinor: true, TargetVersion: "v3.6.7"}}})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, "sets both same_minor and target_version") {
		t.Errorf("both ends at once: %s", text)
	}

	runUpgradeEtcd := func(args checkUpgradeArgs) string {
		res, _, _ := h.checkUpgradeTool(context.Background(), nil, args)
		return res.Content[0].(*mcp.TextContent).Text
	}
	runUpgradeEtcd(checkUpgradeArgs{Project: "etcd", From: "v3.5.30", SameMinor: true})
	if reqs := strings.Join(f.Requests(), " "); !strings.Contains(reqs, "/v2/upgrade/etcd?from=v3.5.30&to=v3.5.32") {
		t.Errorf("check_upgrade same_minor asked %s", reqs)
	}
	if text := runUpgradeEtcd(checkUpgradeArgs{Project: "etcd", From: "v3.5.32", SameMinor: true}); !strings.Contains(text, `"releases":[]`) || !strings.Contains(text, "same_minor: no release above v3.5.32") {
		t.Errorf("nothing newer on the minor: %.400s", text)
	}
}

// /v2/upgrade answers the same case with a 404 (ratatosk.io, 2026-11-15);
// check_upgrade must pass that error on, not an empty range.
// (Independent review before the 1.0 launch, 2026-11-15.)
func TestCheckUpgradePassesTheNewerThanEveryReleaseError(t *testing.T) {
	h := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"v3.55.0 is newer than every analyzed release of etcd in its line — latest analyzed: v3.7.3"}`, http.StatusNotFound)
	})))
	res, _, err := h.checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.55.0"})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !res.IsError || !strings.Contains(text, "HTTP 404") || !strings.Contains(text, "newer than every analyzed release") || !strings.Contains(text, "v3.7.3") {
		t.Errorf("isError=%v %s", res.IsError, text)
	}
}

// An empty range answered "changes": null and "before_upgrade": null; the docs
// say array. Same for later_for_from when the server leaves it out.
// (Independent review before the 1.0 launch, 2026-11-15.)
func TestCheckUpgradeAnswersArraysForAnEmptyRange(t *testing.T) {
	h := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"project":"etcd","from":"v3.7.3","to":"v3.7.3","releases":[],"gaps":[]}`))
	})))
	res, _, err := h.checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.7.3"})
	if err != nil || res.IsError {
		t.Fatalf("check_upgrade: %v %+v", err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{`"changes":[]`, `"before_upgrade":[]`, `"later_for_from":[]`, `"releases":[]`} {
		if !strings.Contains(text, want) {
			t.Errorf("no %s in %s", want, text)
		}
	}
	if strings.Contains(text, ":null") {
		t.Errorf("a null in the answer: %s", text)
	}
}
