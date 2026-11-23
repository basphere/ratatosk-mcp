package tools

// check_stack: what leaves the process (the privacy property), the range and
// the briefing it answers, and its bounds. Most cases replay a measured
// failure; the anchor sits on the case.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// The whole point of check_stack over check_upgrade. Two installs on very
// different versions, one with a target, must produce exactly the same
// upstream requests — otherwise the server could read the running version
// off which releases were fetched. That holds for a version that is not read
// as one too: a version that could not be parsed skipped the release list and
// sent one request, one on a line with no releases skipped the documents and
// sent two, an ordinary one sent eight (independent review, 2026-11-21). The
// requests are compared as a multiset: documents are read several at a time.
func TestCheckStackRequestsDoNotDependOnVersions(t *testing.T) {
	var sets [][]string
	comps := []stackComponent{
		{Project: "etcd", Version: "v3.5.30"},
		{Project: "etcd", Version: "v3.6.8"},
		{Project: "etcd", Version: "3.5.31", TargetVersion: "v3.6.7"},
		{Project: "etcd", Version: "api/v3.6.7"},
		{Project: "etcd", Version: "v3.6.99"},                   // newer than every release on record
		{Project: "etcd", Version: "v3.4.0"},                    // older than every release on record
		{Project: "etcd", Version: "v3.5.30-1789133542-0f3e1a"}, // an image build suffix
		{Project: "etcd", Version: "knative-v3.5.30"},           // a line with no releases
		{Project: "etcd", Version: "garbage"},                   // not a version
		{Project: "etcd", Version: ""},
		{Project: "etcd", Version: "v3.5.30", SameMinor: true},
	}
	for _, comp := range comps {
		f := upstreamtest.NewEtcd()
		h := withAPI(t, httptest.NewServer(f.Handler(t)))
		runStack(t, h, checkStackArgs{Components: []stackComponent{comp}})
		reqs := slices.Sorted(slices.Values(f.Requests()))
		for _, u := range reqs {
			if strings.Contains(u, "from=") || strings.Contains(u, "/v2/upgrade") {
				t.Fatalf("check_stack called the server-side range: %s", u)
			}
		}
		sets = append(sets, reqs)
	}
	for i := 1; i < len(sets); i++ {
		if !slices.Equal(sets[0], sets[i]) {
			t.Errorf("requests depend on the running version: %q sent %d, %q sent %d:\n%v\nvs\n%v",
				comps[0].Version, len(sets[0]), comps[i].Version, len(sets[i]), sets[0], sets[i])
		}
	}
}

// A second call inside the cache window costs nothing upstream.
func TestCheckStackCachesPublicDocuments(t *testing.T) {
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	runStack(t, h, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}})
	n := len(f.Requests())
	runStack(t, h, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.6.7"}}})
	if m := len(f.Requests()); m != n {
		t.Fatalf("second call went upstream again: %d → %d requests", n, m)
	}
}

func TestCheckStackBriefing(t *testing.T) {
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	out := runStack(t, h, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}, Detail: "brief"})
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
		t.Errorf("security severity %s, want null (no grade found)", c.Changes[0].Severity)
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
	if want := []releaseline.Gap{{After: "v3.5.32", Before: "v3.6.7"}}; !slices.Equal(c.Gaps, want) {
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
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	out := runStack(t, h, checkStackArgs{Components: []stackComponent{
		{Project: "not-a-project", Version: "v1.0.0"},
		{Project: "etcd", Version: "flagd/v3.6.7"},
		{Project: "etcd", Version: "garbage"},
	}})
	if len(out.CouldNotCheck) != 3 {
		t.Fatalf("could_not_check %+v, want 3", out.CouldNotCheck)
	}
	if c := out.Components[0]; c.Tracked == nil || *c.Tracked || !strings.HasPrefix(c.Note, "unknown project name") || !strings.Contains(c.Note, "check the slug with list_projects") || !strings.Contains(c.Note, "not safety") {
		t.Errorf("untracked component %+v", c)
	}
	if !strings.Contains(out.CouldNotCheck[1].Reason, "release-line prefix was probably dropped") {
		t.Errorf("dropped-prefix reason %q", out.CouldNotCheck[1].Reason)
	}
	if !strings.Contains(out.CouldNotCheck[2].Reason, "could not be parsed") {
		t.Errorf("unparseable reason %q", out.CouldNotCheck[2].Reason)
	}

	// An image alias is checked as its project, and says so.
	g := upstreamtest.NewEtcd()
	g.Projects = `{"projects":[{"slug":"etcd","name":"etcd","image_aliases":["etcd-server"]}]}`
	h = withAPI(t, httptest.NewServer(g.Handler(t)))
	out = runStack(t, h, checkStackArgs{Components: []stackComponent{{Project: "etcd-server", Version: "v3.6.7"}}})
	if c := out.Components[0]; c.Project != "etcd" || !strings.Contains(c.Note, "image alias") || len(out.CouldNotCheck) != 0 {
		t.Errorf("alias not resolved: %+v", c)
	}
}

// Replay of the measured failure: running-as-target read as "no issues".
func TestCheckStackIgnoresSelfNullifyingTarget(t *testing.T) {
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	out := runStack(t, h, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.6.7", TargetVersion: "v3.6.7"}}})
	c := out.Components[0]
	if len(c.Releases) != 1 || c.Releases[0].Version != "v3.6.8" {
		t.Errorf("releases %+v, want the full path (v3.6.8) with the target ignored", c.Releases)
	}
	if !strings.Contains(c.Note, "empty by construction") {
		t.Errorf("note %q must explain the ignored target", c.Note)
	}
}

// Running v3.5.30 already carries GHSA-bbbb. A later patch on the same branch
// citing it again is a follow-up fix and must be listed; the same advisory on
// another branch is still a backport and stays excluded.
// (Independent review, 2026-11-11.)
func TestBriefingKeepsALaterFixOnTheRunningBranch(t *testing.T) {
	f := upstreamtest.NewEtcd()
	f.Docs["v3.5.32"] = upstreamtest.Doc("v3.5.32", "", "",
		`[{"id":"etcd@v3.5.32#b","kind":"security","title":"complete fix for GHSA-bbbb","body":null,"severity":"critical","advisories":["GHSA-bbbb"],`+
			`"affects":{"text":"gRPC listener","status":"stated"},"also_required":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}]`, "")
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	c := runStack(t, h, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}, Detail: "brief"}).Components[0]

	var ids []string
	for _, ch := range c.Changes {
		ids = append(ids, ch.ID)
	}
	joined := strings.Join(ids, " ")
	if !strings.Contains(joined, "etcd@v3.5.32#b") {
		t.Errorf("the follow-up fix on the running branch was dropped: %v", ids)
	}
	if strings.Contains(joined, "etcd@v3.6.8#b") {
		t.Errorf("the backport on another branch must stay excluded: %v", ids)
	}
	if !strings.Contains(c.Note, "1 security change(s) in range were excluded") {
		t.Errorf("note %q must count only the other branch's backport", c.Note)
	}
}

// (Independent review, 2026-11-11.)
func TestCheckStackChecksADuplicateComponentOnce(t *testing.T) {
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	same := stackComponent{Project: "etcd", Version: "v3.5.30"}
	out := runStack(t, h, checkStackArgs{Components: []stackComponent{same, {Project: " etcd ", Version: "v3.5.30"}, same, {Project: "etcd", Version: "v3.5.31"}}})
	if len(out.Components) != 2 {
		t.Fatalf("four components, two distinct: got %d reports", len(out.Components))
	}
	if !strings.Contains(out.Components[0].Note, "listed 3 times in this call; checked once") {
		t.Errorf("the kept report must say it stood for three: %q", out.Components[0].Note)
	}
	if strings.Contains(out.Components[1].Note, "listed") {
		t.Errorf("a component sent once carries no such note: %q", out.Components[1].Note)
	}
}

// detail:"full" is bounded per call, not only per component, and the cut is
// counted and explained.
// (Independent review, 2026-11-11.)
func TestFullDetailStopsAtTheByteBudget(t *testing.T) {
	f := upstreamtest.NewEtcd()
	pad := strings.Repeat("x", 900<<10)
	big := func(ver string) string {
		return upstreamtest.Doc(ver, "", "", `[{"id":"etcd@`+ver+`#e","kind":"enhancements","title":"t","body":"`+pad+`","affects":{"text":"","status":"not_stated"},"to_use":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}]`, "")
	}
	for _, v := range []string{"v3.5.31", "v3.5.32", "v3.6.7", "v3.6.8"} {
		f.Docs[v] = big(v)
	}
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	res, _, err := h.checkStackTool(context.Background(), nil, checkStackArgs{Detail: "full", Components: []stackComponent{
		{Project: "etcd", Version: "v3.5.30"}, {Project: "etcd", Version: "v3.5.31"},
	}})
	if err != nil || res.IsError {
		t.Fatalf("tool error: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var out struct {
		Components []struct {
			Note        string            `json:"note"`
			Full        []json.RawMessage `json:"releases_full"`
			FullOmitted int               `json:"releases_full_omitted"`
			Releases    []struct {
				Version string `json:"version"`
			} `json:"releases"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	first, second := out.Components[0], out.Components[1]
	// Four 900 KB documents in range; two fit under 2 MiB.
	if len(first.Full) != 2 || first.FullOmitted != 2 || len(first.Releases) != 4 {
		t.Errorf("first component: %d documents, %d omitted, %d releases listed; want 2, 2, 4", len(first.Full), first.FullOmitted, len(first.Releases))
	}
	if !strings.Contains(first.Note, "releases_full stops here") || !strings.Contains(first.Note, "get_release") {
		t.Errorf("the cut must be explained: %q", first.Note)
	}
	// The budget is the call's: the next component gets none, and says so.
	if len(second.Full) != 0 || second.FullOmitted != 3 || len(second.Releases) != 3 {
		t.Errorf("second component: %d documents, %d omitted, %d releases listed; want 0, 3, 3", len(second.Full), second.FullOmitted, len(second.Releases))
	}
	if len(text) > fullBytesBudget+(1<<20) {
		t.Errorf("answer is %d bytes, over the budget plus one document", len(text))
	}
}

// One failed document fails the component; the others must not be waited for.
// (Independent review, 2026-11-11.)
func TestFetchDocsStopsAfterTheFirstError(t *testing.T) {
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v3.5.31") {
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})))
	start := time.Now()
	_, err := h.fetchDocs(context.Background(), f.Rows, "")
	if err == nil {
		t.Fatal("a 500 on one document must fail the fetch")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("waited %s for documents that no longer mattered", took)
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("the error reported must be the upstream failure, not our own cancel: %v", err)
	}
}

// A deadline that has already passed must come back as an error. Without it
// the fetch returned an empty set and no error, and the component would have
// been answered as "nothing in range".
// (Independent review, 2026-11-11.)
func TestFetchDocsReportsTheCallersDeadline(t *testing.T) {
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	docs, err := h.fetchDocs(ctx, f.Rows, "")
	if err == nil {
		t.Fatalf("an expired deadline returned %d documents and no error", len(docs))
	}
	if r := fetchReason(ctx, err); !strings.Contains(r, "ran out of time") {
		t.Errorf("reason %q must say the call ran out of time", r)
	}
}

// A running version above every analyzed release on its line used to come back
// as an empty range with a target below it, which reads as "already up to
// date" (etcd v3.55.0 → releases [], target_version v3.7.3 on the preview API).
// It is listed in could_not_check, with no range at all.
// (Independent review before the 1.0 launch, 2026-11-15.)
func TestCheckStackRefusesAVersionNewerThanEveryAnalyzedRelease(t *testing.T) {
	h := withAPI(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)))
	out := runStack(t, h, checkStackArgs{Components: []stackComponent{
		{Project: "etcd", Version: "v3.6.99", TargetVersion: "v3.7.0"},
		{Project: "etcd", Version: "v3.6.9-rc.1"},
		{Project: "etcd", Version: "v3.6.8"},
		{Project: "etcd", Version: "v3.6.8-rc.1"},
	}})
	if len(out.CouldNotCheck) != 2 {
		t.Fatalf("could_not_check %+v, want the two versions above v3.6.8", out.CouldNotCheck)
	}
	for i, want := range []string{"v3.6.99", "v3.6.9-rc.1"} {
		cnc, c := out.CouldNotCheck[i], out.Components[i]
		if cnc.Version != want || !strings.Contains(cnc.Reason, "newer than every analyzed release on its line (newest analyzed: v3.6.8)") {
			t.Errorf("could_not_check[%d] = %+v", i, cnc)
		}
		if c.Releases != nil || c.TargetVersion != "" || !strings.Contains(c.Note, "not analyzed this version yet") {
			t.Errorf("%s must carry no range and no target below it: releases %v target %q note %q", want, c.Releases, c.TargetVersion, c.Note)
		}
	}
	if c := out.Components[2]; c.Releases == nil || len(c.Releases) != 0 || c.Note != "" {
		t.Errorf("the newest release on record is checked and already newest: %+v", c)
	}
	if c := out.Components[3]; len(c.Releases) != 1 || c.Releases[0].Version != "v3.6.8" {
		t.Errorf("a pre-release of the newest release is below it: %+v", c.Releases)
	}
}

// A target above every analyzed release used to come back as an empty (or
// short) range with no word: etcd v3.7.3 with target_version v3.7.4 read as
// "nothing between here and there". The component is still checked up to the
// newest analyzed release, and the note says the target itself was not.
// (Independent review before the 1.0 launch, 2026-11-15.)
func TestCheckStackNotesATargetNewerThanEveryAnalyzedRelease(t *testing.T) {
	h := withAPI(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)))
	out := runStack(t, h, checkStackArgs{Components: []stackComponent{
		{Project: "etcd", Version: "v3.6.8", TargetVersion: "v3.6.9"},
		{Project: "etcd", Version: "v3.6.8"},
	}})
	if len(out.CouldNotCheck) != 0 {
		t.Fatalf("could_not_check %+v, want none: the range up to the newest analyzed release is still checked", out.CouldNotCheck)
	}
	if c := out.Components[0]; !strings.Contains(c.Note, "target_version v3.6.9 is newer than every analyzed release on its line (newest analyzed: v3.6.8)") {
		t.Errorf("a target above the newest analyzed release must be named in the note: %q", c.Note)
	}
	if c := out.Components[1]; strings.Contains(c.Note, "target_version") {
		t.Errorf("no target, no target note: %q", c.Note)
	}
}

// later_in_range carries what was learned about the releases in range: the
// v1.20.3 BackendTLSPolicy regression fixed in v1.20.4 was in no briefing.
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.) A route carries it too, as no step counts
// it (1.3.0).
func TestLaterInRangeCarriesWhatWasLearnedAboutTheRange(t *testing.T) {
	f := upstreamtest.NewEtcd()
	ev := `{"seq":2,"at":"2026-08-02T00:00:00Z","type":"regression_fixed","project":"etcd","version":"v3.6.7","fixed_in":"v3.6.8","title":"lease regression"}`
	d := f.Docs["v3.6.7"]
	i := strings.LastIndex(d, `"later":[]}`) // the release's own later, after its changes'
	f.Docs["v3.6.7"] = d[:i] + `"later":[` + ev + `]}`
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	for _, detail := range []string{"", "brief"} {
		res, _, _ := h.checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}, {Project: "etcd", Version: "v3.6.8"}}, Detail: detail})
		var out struct {
			Components []struct {
				AnswerKind      string                  `json:"answer_kind"`
				LaterInRange    *[]json.RawMessage      `json:"later_in_range"`
				LaterForRunning []json.RawMessage       `json:"later_for_running"`
				BeforeUpgrade   *[]briefing.UpgradeItem `json:"before_upgrade"`
			} `json:"components"`
		}
		json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out)
		c := out.Components[0]
		if want := map[string]string{"": "route", "brief": ""}[detail]; c.AnswerKind != want {
			t.Errorf("detail %q: answer_kind %q, want %q", detail, c.AnswerKind, want)
		}
		if c.LaterInRange == nil || len(*c.LaterInRange) != 1 || !strings.Contains(string((*c.LaterInRange)[0]), "lease regression") {
			t.Errorf("detail %q: later_in_range %v", detail, c.LaterInRange)
		}
		if len(c.LaterForRunning) != 1 {
			t.Errorf("detail %q: later_for_running stays the running version's: %s", detail, c.LaterForRunning)
		}
		if e := out.Components[1]; e.LaterInRange == nil || len(*e.LaterInRange) != 0 || e.BeforeUpgrade == nil || len(*e.BeforeUpgrade) != 0 {
			t.Errorf("detail %q: an empty range answers later_in_range [] and before_upgrade [], not absent: %+v", detail, e)
		}
	}
}

// cilium-envoy publishes v1.37.6-1789133542-<commit>; read whole it named line
// "v1.37.6" and failed with "the release-line prefix was probably dropped".
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestImageBuildSuffixIsReadWithoutItAndSaid(t *testing.T) {
	for in, want := range map[string]string{
		"v1.37.6-1789133542-cbec91f666af0bf742da986d43832932dbb26b82": "v1.37.6",
		"v1.37.6-cbec91f":       "v1.37.6",
		"lts-4081.3.9-abc1234":  "lts-4081.3.9",
		"flagd/v0.16.1-9f8e7d6": "flagd/v0.16.1",
	} {
		if got, ok := releaseline.StripBuildTail(in); !ok || got != want {
			t.Errorf("releaseline.StripBuildTail(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"v1.24.3-rc1", "v1.2.3-beta.2", "v1.2.3-20240101", "v1.2.3-deadbee", "v1.2.3", "knative-v1.12.0", "lts-4081.3.9"} {
		if got, ok := releaseline.StripBuildTail(in); ok {
			t.Errorf("releaseline.StripBuildTail(%q) = %q: not a build suffix", in, got)
		}
	}

	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	out := runStack(t, h, checkStackArgs{Components: []stackComponent{
		{Project: "etcd", Version: "v3.6.7-1789133542-cbec91f666af0bf742da986d43832932dbb26b82"},
		{Project: "etcd", Version: "v3.6.7-20240101-xyz"},
	}})
	res, _, _ := h.checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.6.7-1789133542-cbec91f666af0bf742da986d43832932dbb26b82"}}})
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

// kinds narrows a briefing's lists, never its summary; detail full narrows each
// release document.
// (1.2.0: get_release's kinds and changes, kinds on the briefing tools.)
func TestKindsNarrowABriefing(t *testing.T) {
	h := withAPI(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)))
	c := runStack(t, h, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}, Kinds: stringList{"breaking"}, Detail: "brief"}).Components[0]
	if len(c.Changes) != 1 || c.Changes[0].Kind != "breaking" || c.Summary.Counts["security"] != 1 {
		t.Errorf("kinds breaking: %+v summary %+v", c.Changes, c.Summary)
	}
	res, _, _ := h.checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}, Detail: "full", Kinds: stringList{"enhancements"}})
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError || !strings.Contains(text, `"etcd@v3.6.7#e"`) || strings.Contains(text, `"etcd@v3.6.7#k"`) {
		t.Errorf("full with kinds enhancements: %.400s", text)
	}
	res, _, _ = h.checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}, Kinds: stringList{"enhancements"}})
	if !res.IsError {
		t.Error("a briefing asked for a counted kind must say where to read it")
	}
}

// check_stack reads a project's newest releases, whatever the branch, and
// cannot read more without telling the server the running version. When a
// release of the running branch at or below the running version is older
// than those, what it fixed is unknown here: check_upgrade left the change
// out and check_stack listed it without a word (independent review,
// 2026-11-21). It now says so in note; the change stays listed.
func TestCheckStackSaysWhenItsBranchReachesPastWhatWasRead(t *testing.T) {
	f := upstreamtest.NewEtcd()
	crit := func(ver string) string {
		return upstreamtest.Doc(ver, `{"security":1,"breaking":0,"deprecations":0,"important_fixes":0,"enhancements":0,"dependency_updates":0,"other":0}`, "",
			`[{"id":"etcd@`+ver+`#a","kind":"security","title":"fix","body":null,"severity":"critical","advisories":["GHSA-2222-3333-4444"],"affects":{"text":"","status":"not_stated"}}]`, "")
	}
	f.Rows = []release.Row{{Project: "etcd", Version: "v3.6.0"}}
	for i := 0; i < 58; i++ {
		f.Rows = append(f.Rows, release.Row{Project: "etcd", Version: fmt.Sprintf("api/v3.6.%d", 100-i)})
	}
	f.Rows = append(f.Rows, release.Row{Project: "etcd", Version: "v3.5.1"}, release.Row{Project: "etcd", Version: "v3.5.0"})
	f.Docs = map[string]string{"v3.6.0": crit("v3.6.0"), "v3.5.1": upstreamtest.Doc("v3.5.1", "", "", "", ""), "v3.5.0": crit("v3.5.0")}
	h := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/releases/etcd/api") {
			http.NotFound(w, r)
			return
		}
		f.Handler(t)(w, r)
	})))

	out := runStack(t, h, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.1"}}})
	c := out.Components[0]
	want := "1 release(s) at or below v3.5.1 on your branch were not read"
	if !strings.Contains(c.Note, want) || !strings.Contains(c.Note, "security changes your branch already fixed there are not left out") {
		t.Errorf("note %q, want it to say %q and what follows from it", c.Note, want)
	}
	if len(c.Changes) != 1 || c.Changes[0].Version != "v3.6.0" {
		t.Errorf("changes %+v: the change stays listed", c.Changes)
	}

	// The whole branch read: no such sentence.
	g := upstreamtest.NewEtcd()
	if n := runStack(t, withAPI(t, httptest.NewServer(g.Handler(t))), checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.31"}}}).Components[0].Note; strings.Contains(n, "on your branch were not read") {
		t.Errorf("note %q on a branch read whole", n)
	}
}
