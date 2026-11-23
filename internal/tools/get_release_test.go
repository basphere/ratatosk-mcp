package tools

// get_release: the latest release, and part of a release (1.2.0 kinds and
// changes).

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// "Latest" is what /v2/projects says it is (the highest version), not the first
// row of the release list (the most recently published): here the list leads
// with v3.6.8 and the project's latest is v3.6.7.
func TestGetReleaseWithoutVersionReadsTheLatest(t *testing.T) {
	f := upstreamtest.NewEtcd()
	f.Projects = `{"projects":[{"slug":"etcd","name":"etcd","latest":{"version":"v3.6.7","released_on":"2026-07-01"}},{"slug":"envoy","name":"Envoy","latest":null}]}`
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	res, _, err := h.getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd"})
	if err != nil || res.IsError {
		t.Fatalf("get_release: %v %+v", err, res)
	}
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, `"version":"v3.6.7"`) {
		t.Fatalf("not the latest: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	// Read in English, which sends no lang (1.3.0: the answers are English alone).
	if reqs := f.Requests(); !slices.Contains(reqs, "/v2/releases/etcd/v3.6.7") {
		t.Fatalf("not read in English: %v", reqs)
	}
	// A tracked project with nothing analyzed is told so; an unknown slug is told
	// to check the slug. Both used to get "check the slug" (spiffe, 2026-11-15).
	for slug, want := range map[string]string{"envoy": "project envoy is tracked by ratatosk but has no analyzed release yet", "nope": "unknown project nope — not a slug ratatosk knows; check the slug with list_projects"} {
		res, _, _ := h.getReleaseTool(context.Background(), nil, getReleaseArgs{Project: slug})
		if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, want) {
			t.Fatalf("%s: got %q, want %q", slug, text, want)
		}
	}
}

// get_release with kinds keeps those kinds; with changes, the named changes and
// before-upgrade items and the ids it does not have. Folded lines go either
// way; the rest of the document comes as it was, in its order.
// (1.2.0: get_release's kinds and changes, kinds on the briefing tools.)
func TestGetReleaseReadsPartOfARelease(t *testing.T) {
	f := upstreamtest.NewEtcd()
	f.Projects = `{"projects":[{"slug":"etcd","name":"etcd","latest":{"version":"v3.6.8"}}]}`
	full := f.Docs["v3.6.7"]
	f.Docs["v3.6.7"] = full[:len(full)-1] + `,"folded":{"dependency_updates_without_security":["- bump x"],"already_in_earlier_release":[]}}`
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	get := func(a getReleaseArgs) (map[string]json.RawMessage, string) {
		t.Helper()
		res, _, _ := h.getReleaseTool(context.Background(), nil, a)
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("get_release %+v: %s", a, text)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			t.Fatal(err)
		}
		return m, text
	}
	ids := func(raw json.RawMessage) []string {
		var cs []struct {
			ID string `json:"id"`
		}
		json.Unmarshal(raw, &cs)
		var out []string
		for _, c := range cs {
			out = append(out, c.ID)
		}
		return out
	}

	whole, text := get(getReleaseArgs{Project: "etcd", Version: "v3.6.7"})
	if _, ok := whole["folded"]; !ok || text != f.Docs["v3.6.7"] {
		t.Fatal("without kinds or changes the release passes through as it came")
	}

	m, text := get(getReleaseArgs{Project: "etcd", Version: "v3.6.7", Kinds: stringList{"breaking"}})
	if got := ids(m["changes"]); !slices.Equal(got, []string{"etcd@v3.6.7#k"}) {
		t.Errorf("kinds breaking: %v", got)
	}
	if _, ok := m["folded"]; ok {
		t.Error("folded lines are left out when kinds is given")
	}
	if !strings.Contains(text, `"upgrade_guide":[{"id":"etcd@v3.6.7#g"`) || !strings.HasPrefix(text, `{"project":"etcd","version":"v3.6.7"`) {
		t.Errorf("the rest of the document is passed on, in its order: %.200s", text)
	}

	// With version: the named changes and before-upgrade items of that release.
	m, _ = get(getReleaseArgs{Project: "etcd", Version: "v3.6.7", Changes: stringList{"etcd@v3.6.7#a", "etcd@v3.6.7#g", "etcd@v3.6.7#zzz", "etcd@v3.6.7#a"}})
	if string(m["version"]) != `"v3.6.7"` {
		t.Errorf("version %s", m["version"])
	}
	if got := ids(m["changes"]); !slices.Equal(got, []string{"etcd@v3.6.7#a"}) {
		t.Errorf("changes %v", got)
	}
	var bu map[string]json.RawMessage
	json.Unmarshal(m["before_upgrade"], &bu)
	if got := ids(bu["upgrade_guide"]); !slices.Equal(got, []string{"etcd@v3.6.7#g"}) {
		t.Errorf("a before_upgrade id is answered with its item: %v", got)
	}
	if string(m["not_found"]) != `["etcd@v3.6.7#zzz"]` {
		t.Errorf("not_found %s", m["not_found"])
	}
	// Both at once is refused (TestGetReleaseReadsTheReleasesTheIDsName);
	// without version, the releases the ids name are read (same test).

	res, _, _ := h.getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Version: "v3.6.7", Kinds: stringList{"nope"}})
	if !res.IsError {
		t.Error("an unknown kind was answered")
	}
	many := make(stringList, maxPickedChanges+1)
	for i := range many {
		many[i] = "etcd@v3.6.7#" + strings.Repeat("x", i+1)
	}
	if res, _, _ := h.getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Version: "v3.6.7", Changes: many}); !res.IsError {
		t.Error("more ids than the bound were answered")
	}
}
