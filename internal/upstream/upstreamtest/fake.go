// Package upstreamtest is a fake ratatosk /v2 API for tests: a small etcd
// history served in the /v2 shape, recording every request URL so a test can
// assert on what left this process, and a client of it.
//
// It is a package of its own because tests in several packages run on the same
// history and the same client. It is imported by tests only. It imports
// internal/upstream for the client, so a test of that package that uses it is
// an external test package (package upstream_test).
package upstreamtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/basphere/ratatosk-mcp/internal/metrics"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/upstream"
)

// Fake serves Projects at /v2/projects, Rows at /v2/releases, Docs (version →
// release document) at /v2/releases/etcd/{version} and a range cut from them
// at /v2/upgrade/etcd. A test may change them before it serves.
type Fake struct {
	mu       sync.Mutex
	seen     []string
	Rows     []release.Row
	Docs     map[string]string // version → release document
	Projects string
}

// Handler answers as described on Fake; any other path fails t.
func (f *Fake) Handler(t testing.TB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.Record(r.URL.RequestURI())
		switch {
		case r.URL.Path == "/v2/projects":
			w.Write([]byte(f.Projects))
		case r.URL.Path == "/v2/releases":
			var rows []release.Row
			for _, row := range f.Rows {
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
			d, ok := f.Docs[v]
			if !ok {
				http.Error(w, `{"error":"no analyzed release etcd `+v+`"}`, http.StatusNotFound)
				return
			}
			w.Write([]byte(d))
		case r.URL.Path == "/v2/upgrade/etcd":
			// The server's range, cut the way check_stack cuts it (1.2.0 tests).
			from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
			inRange, target := releaseline.UpgradeRange(f.Rows, from, to)
			var rels []string
			for _, x := range inRange {
				rels = append(rels, f.Docs[x.Version])
			}
			fmt.Fprintf(w, `{"project":"etcd","from":%q,"to":%q,"releases":[%s],"gaps":[],"later_for_from":[]}`, from, target, strings.Join(rels, ","))
		default:
			t.Errorf("unexpected upstream request %s", r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}
}

// Record adds a request URL to what Requests returns — for a test that
// answers some paths itself and hands the rest to Handler.
func (f *Fake) Record(uri string) {
	f.mu.Lock()
	f.seen = append(f.seen, uri)
	f.mu.Unlock()
}

// Requests lists every request URL seen so far, sorted.
func (f *Fake) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.seen)
	sort.Strings(out)
	return out
}

// Doc is a release document of etcd in the /v2 shape; an empty argument
// takes the default (zero counts, empty before_upgrade, changes and later).
func Doc(ver string, counts string, before string, changes string, later string) string {
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

// NewEtcd is the etcd history most tests run on; its Rows and Docs are the
// record of what it holds.
func NewEtcd() *Fake {
	sec := func(id, adv string) string {
		return `{"id":"` + id + `","kind":"security","title":"fix ` + adv + `","body":null,"severity":null,"advisories":["` + adv + `"],` +
			`"affects":{"text":"gRPC listener","status":"stated"},"also_required":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}`
	}
	f := &Fake{
		Projects: `{"projects":[{"slug":"etcd","name":"etcd"},{"slug":"envoy","name":"Envoy","image_aliases":["cilium-envoy"]}]}`,
		Rows: []release.Row{
			{Project: "etcd", Version: "v3.6.8"}, {Project: "etcd", Version: "v3.6.7"}, {Project: "etcd", Version: "api/v3.6.7"},
			{Project: "etcd", Version: "v3.5.32"}, {Project: "etcd", Version: "v3.5.31"}, {Project: "etcd", Version: "v3.5.30"},
		},
		Docs: map[string]string{
			"v3.5.30": Doc("v3.5.30", "", "", `[`+sec("etcd@v3.5.30#b", "GHSA-bbbb")+`]`,
				`[{"seq":1,"at":"2026-08-01T00:00:00Z","type":"known_issue_fixed","project":"etcd","version":"v3.5.30","fixed_in":"v3.5.31","title":"leak"}]`),
			"v3.5.31": Doc("v3.5.31", "", "", `[`+sec("etcd@v3.5.31#a", "GHSA-aaaa")+`]`, ""),
			"v3.5.32": Doc("v3.5.32", "", "", "", ""),
			"v3.6.7": Doc("v3.6.7", `{"security":1,"breaking":1,"deprecations":0,"important_fixes":0,"enhancements":2,"dependency_updates":0,"other":0}`,
				`{"prerequisites":[],"known_issues":[],"upgrade_guide":[{"id":"etcd@v3.6.7#g","title":"read the 3.6 upgrade guide","body":null,"evidence":[{"quote":"Before upgrading to 3.6, read the upgrade guide.","source":"release_notes","url":"https://example.test/etcd/v3.6.7"}],"folded":[]}]}`,
				`[`+sec("etcd@v3.6.7#a", "GHSA-aaaa")+`,`+
					`{"id":"etcd@v3.6.7#k","kind":"breaking","title":"v2store removed","body":"long","affects":{"text":"","status":"not_stated"},"action_required":"migrate","no_downgrade":{"text":"","status":"stated"},"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"also_touches":[],"evidence":[],"later":[]},`+
					`{"id":"etcd@v3.6.7#e","kind":"enhancements","title":"new flag","body":null,"affects":{"text":"","status":"not_stated"},"to_use":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}]`, ""),
			"v3.6.8":     Doc("v3.6.8", "", "", `[`+sec("etcd@v3.6.8#b", "GHSA-bbbb")+`]`, ""),
			"api/v3.6.7": Doc("api/v3.6.7", "", "", "", ""),
		},
	}
	return f
}

// Client is a client of ts built as the server builds one, counting into m
// (nil: metrics off); ts is closed when the test ends.
func Client(t testing.TB, ts *httptest.Server, m *metrics.Metrics) *upstream.Client {
	t.Helper()
	t.Cleanup(ts.Close)
	return upstream.New(ts.URL, "dev", 32<<20, m)
}
