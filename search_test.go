package main

// search's project filter and matches (1.2.0): agents searching
// "BackendTLSPolicy" for a cilium upgrade got one matched item for cilium
// v1.20.0 where seven changes mention it (2026-11-20).

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
)

func TestSearchFinderMatchesWholeAdvisoryIDs(t *testing.T) {
	f := searchFinder("CVE-2026-2757")
	if f("fixes cve-2026-27571") || !f("fixes cve-2026-2757.") || !f("cve-2026-2757") {
		t.Error("a complete advisory id matches only that id")
	}
	if g := searchFinder("GHSA-3wh4"); !g("ghsa-3wh4-j44w-pg92") {
		t.Error("a partial id is text and matches anywhere")
	}
	if h := searchFinder("BackendTLSPolicy"); !h("the backendtlspolicy crd") {
		t.Error("text matches case-insensitively")
	}
}

func searchServer(t *testing.T, matches int) *fakeV2 {
	t.Helper()
	row := func(project, ver, kind, title string) string {
		m := fmt.Sprintf(`{"kind":%q,"title":%q}`, kind, title)
		if kind == "release" {
			m = `{"kind":"release","title":null}`
		}
		return fmt.Sprintf(`{"project":%q,"name":"x","version":%q,"counts":{},"warnings":{},"matched":%s}`, project, ver, m)
	}
	var changes []string
	for i := 0; i < matches; i++ {
		changes = append(changes, fmt.Sprintf(`{"id":"etcd@v3.6.7#m%d","kind":"breaking","title":"BackendTLSPolicy change %d","body":null,"evidence":[],"later":[]}`, i, i))
	}
	changes = append(changes,
		`{"id":"etcd@v3.6.7#q","kind":"important_fixes","title":"quoted only","body":null,"evidence":[{"quote":"see backendTLSPolicy docs"}],"later":[]}`,
		`{"id":"etcd@v3.6.7#n","kind":"other","title":"unrelated","body":null,"evidence":[],"later":[]}`)
	f := newFakeEtcd()
	f.docs["v3.6.7"] = doc("v3.6.7", "", `{"prerequisites":[{"id":"etcd@v3.6.7#p","title":"read first","body":null,"evidence":[],"folded":[{"title":"BackendTLSPolicy CRD","body":"install it"}]}],"known_issues":[],"upgrade_guide":[]}`,
		"["+strings.Join(changes, ",")+"]", "")
	search := `{"query":"BackendTLSPolicy","in_analysis":[` +
		row("etcd", "v3.6.7", "before_upgrade", "read first") + "," +
		row("envoy", "v1.38.0", "security", "x") + "," +
		row("etcd", "v3.6.8", "release", "") +
		`],"in_release_notes":[{"project":"envoy","version":"v1.38.0","excerpt":"…"},{"project":"etcd","version":"v3.6.8","excerpt":"…"}],"in_linked_documents":[],"extra":{"kept":true}}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/search":
			f.mu.Lock()
			f.seen = append(f.seen, r.URL.RequestURI())
			f.mu.Unlock()
			w.Write([]byte(search))
			return
		case strings.HasPrefix(r.URL.Path, "/v2/releases/envoy/"):
			// Not readable: that release is answered without matches.
			http.Error(w, `{"error":"no analyzed release"}`, http.StatusNotFound)
			return
		}
		f.handler(t)(w, r)
	}))
	swapAPI(t, ts)
	return f
}

func TestSearchFiltersByProjectAndListsEveryMatch(t *testing.T) {
	f := searchServer(t, 3)
	res, _, _ := searchTool(context.Background(), nil, searchArgs{Q: "BackendTLSPolicy", Project: " Etcd "})
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		t.Fatal(text)
	}
	var out struct {
		InAnalysis []struct {
			Project        string        `json:"project"`
			Version        string        `json:"version"`
			Matches        []searchMatch `json:"matches"`
			MatchesOmitted int           `json:"matches_omitted"`
		} `json:"in_analysis"`
		InReleaseNotes []struct {
			Project string `json:"project"`
		} `json:"in_release_notes"`
		Extra map[string]bool `json:"extra"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.InAnalysis) != 2 || len(out.InReleaseNotes) != 1 || out.InReleaseNotes[0].Project != "etcd" {
		t.Fatalf("project etcd kept %+v / %+v", out.InAnalysis, out.InReleaseNotes)
	}
	if !out.Extra["kept"] || !strings.HasPrefix(text, `{"matches_not_computed":0,"query":"BackendTLSPolicy","in_analysis":[{"project":"etcd"`) {
		t.Errorf("the rest of the answer is passed on, in its order: %.120s", text)
	}
	var got []string
	for _, m := range out.InAnalysis[0].Matches {
		got = append(got, m.ID+" "+m.Kind)
	}
	want := []string{"etcd@v3.6.7#p before_upgrade", "etcd@v3.6.7#m0 breaking", "etcd@v3.6.7#m1 breaking", "etcd@v3.6.7#m2 breaking", "etcd@v3.6.7#q important_fixes"}
	if !slices.Equal(got, want) {
		t.Errorf("matches %v, want %v (folded text and quotes match; screen order)", got, want)
	}
	if out.InAnalysis[1].Matches != nil {
		t.Errorf("a release matched by its name has no item to list: %+v", out.InAnalysis[1])
	}
	if reqs := strings.Join(f.requests(), " "); strings.Contains(reqs, "envoy") || strings.Contains(reqs, "/v2/releases/etcd/v3.6.8") {
		t.Errorf("read releases the answer does not carry matches for: %s", reqs)
	}

	res, _, _ = searchTool(context.Background(), nil, searchArgs{Q: "BackendTLSPolicy", Project: "argo-cd"})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, "unknown project argo-cd") {
		t.Errorf("an unknown project: %s", text)
	}
}

func TestSearchCountsMatchesPastTheBound(t *testing.T) {
	searchServer(t, maxMatches+5)
	res, _, _ := searchTool(context.Background(), nil, searchArgs{Q: "BackendTLSPolicy"})
	var out struct {
		NotComputed int `json:"matches_not_computed"`
		InAnalysis  []struct {
			Matches        []searchMatch `json:"matches"`
			MatchesOmitted int           `json:"matches_omitted"`
		} `json:"in_analysis"`
	}
	json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out)
	if out.NotComputed != 1 {
		t.Errorf("the release that could not be read is counted: matches_not_computed %d", out.NotComputed)
	}
	// 25 changes, the folded prerequisite and the quoted fix match: 27.
	if r := out.InAnalysis[0]; len(r.Matches) != maxMatches || r.MatchesOmitted != 27-maxMatches {
		t.Errorf("%d matches, %d omitted", len(r.Matches), r.MatchesOmitted)
	}
	if r := out.InAnalysis[1]; r.Matches != nil {
		t.Errorf("a release that could not be read carries no matches: %+v", r)
	}
}

// matches reads at most searchMatchReleases documents a call; the releases it
// did not read are counted at the top of the answer.
func TestSearchReadsAtMostEightReleases(t *testing.T) {
	f := newFakeEtcd()
	var rows []string
	for i := 0; i < searchMatchReleases+3; i++ {
		v := fmt.Sprintf("v9.0.%d", i)
		f.docs[v] = doc(v, "", "", `[{"id":"etcd@`+v+`#m","kind":"breaking","title":"BackendTLSPolicy","body":null,"evidence":[],"later":[]}]`, "")
		rows = append(rows, fmt.Sprintf(`{"project":"etcd","version":%q,"matched":{"kind":"breaking","title":"BackendTLSPolicy"}}`, v))
	}
	answer := `{"query":"BackendTLSPolicy","in_analysis":[` + strings.Join(rows, ",") + `],"in_release_notes":[],"in_linked_documents":[]}`
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/search" {
			w.Write([]byte(answer))
			return
		}
		f.handler(t)(w, r)
	})))
	res, _, _ := searchTool(context.Background(), nil, searchArgs{Q: "BackendTLSPolicy"})
	var out struct {
		NotComputed int `json:"matches_not_computed"`
		InAnalysis  []struct {
			Matches []searchMatch `json:"matches"`
		} `json:"in_analysis"`
	}
	json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out)
	with := 0
	for _, r := range out.InAnalysis {
		if r.Matches != nil {
			with++
		}
	}
	if with != searchMatchReleases || out.NotComputed != 3 {
		t.Errorf("%d releases with matches, matches_not_computed %d", with, out.NotComputed)
	}
	docs := 0
	for _, u := range f.requests() {
		if strings.HasPrefix(u, "/v2/releases/") {
			docs++
		}
	}
	if docs != searchMatchReleases {
		t.Errorf("%d release documents read, at most %d", docs, searchMatchReleases)
	}
}
