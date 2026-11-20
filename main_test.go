package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

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
		BeforeUpgrade []briefing.UpgradeItem `json:"before_upgrade"`
		Changes       []struct {
			Version     string          `json:"version"`
			ID          string          `json:"id"`
			Kind        string          `json:"kind"`
			Severity    json.RawMessage `json:"severity"`
			AlsoIn      []string        `json:"also_in"`
			NoDowngrade *release.Attr   `json:"no_downgrade"`
		} `json:"changes"`
		Gaps            []releaseline.Gap `json:"gaps"`
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
		{Project: "etcd", Version: "v3.6.99"}, // newer than every release on record: refused after the same fetch
	} {
		f := upstreamtest.NewEtcd()
		swapAPI(t, httptest.NewServer(f.Handler(t)))
		runStack(t, checkStackArgs{Components: []stackComponent{comp}})
		reqs := f.Requests()
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
	f := upstreamtest.NewEtcd()
	swapAPI(t, httptest.NewServer(f.Handler(t)))
	runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}})
	n := len(f.Requests())
	runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.6.7"}}})
	if m := len(f.Requests()); m != n {
		t.Fatalf("second call went upstream again: %d → %d requests", n, m)
	}
}

// ---------------------------------------------------------------------------
// the briefing
// ---------------------------------------------------------------------------

func TestCheckStackBriefing(t *testing.T) {
	f := upstreamtest.NewEtcd()
	swapAPI(t, httptest.NewServer(f.Handler(t)))
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
	swapAPI(t, httptest.NewServer(f.Handler(t)))
	out := runStack(t, checkStackArgs{Components: []stackComponent{
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
	swapAPI(t, httptest.NewServer(g.Handler(t)))
	out = runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd-server", Version: "v3.6.7"}}})
	if c := out.Components[0]; c.Project != "etcd" || !strings.Contains(c.Note, "image alias") || len(out.CouldNotCheck) != 0 {
		t.Errorf("alias not resolved: %+v", c)
	}
}

// Replay of the measured failure: running-as-target read as "no issues".
func TestCheckStackIgnoresSelfNullifyingTarget(t *testing.T) {
	f := upstreamtest.NewEtcd()
	swapAPI(t, httptest.NewServer(f.Handler(t)))
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

// ---------------------------------------------------------------------------
// the other tools
// ---------------------------------------------------------------------------

// "Latest" is what /v2/projects says it is (the highest version), not the first
// row of the release list (the most recently published): here the list leads
// with v3.6.8 and the project's latest is v3.6.7.
func TestGetReleaseWithoutVersionReadsTheLatest(t *testing.T) {
	f := upstreamtest.NewEtcd()
	f.Projects = `{"projects":[{"slug":"etcd","name":"etcd","latest":{"version":"v3.6.7","released_on":"2026-07-01"}},{"slug":"envoy","name":"Envoy","latest":null}]}`
	swapAPI(t, httptest.NewServer(f.Handler(t)))
	res, _, err := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Lang: "ko"})
	if err != nil || res.IsError {
		t.Fatalf("get_release: %v %+v", err, res)
	}
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, `"version":"v3.6.7"`) {
		t.Fatalf("not the latest: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	if reqs := f.Requests(); !slices.Contains(reqs, "/v2/releases/etcd/v3.6.7?lang=ko") {
		t.Fatalf("lang not passed: %v", reqs)
	}
	// A tracked project with nothing analyzed is told so; an unknown slug is told
	// to check the slug. Both used to get "check the slug" (spiffe, 2026-11-15).
	for slug, want := range map[string]string{"envoy": "project envoy is tracked by ratatosk but has no analyzed release yet", "nope": "unknown project nope — not a slug ratatosk knows; check the slug with list_projects"} {
		res, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: slug})
		if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, want) {
			t.Fatalf("%s: got %q, want %q", slug, text, want)
		}
	}
	if res, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Lang: "de"}); !res.IsError {
		t.Fatal("an unknown lang was read as English")
	}
}

func TestCheckUpgradeComputesGapsAnOlderServerOmits(t *testing.T) {
	var seen string
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/releases" { // the releases at or below from (1.2.0: what from's branch already fixed)
			w.Write([]byte(`{"releases":[],"next_before":null}`))
			return
		}
		seen = r.URL.RequestURI()
		fmt.Fprintf(w, `{"project":"etcd","from":"v3.5.30","to":"v3.6.7","releases":[%s,%s],"later_for_from":[]}`,
			upstreamtest.Doc("v3.5.31", "", "", "", ""), upstreamtest.Doc("v3.6.7", "", "", "", ""))
	})))
	res, _, err := checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30"})
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
