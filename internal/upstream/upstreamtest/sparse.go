package upstreamtest

import (
	"fmt"
	"net/http"
	"path"
	"strings"
)

// Sparse is a /v2 that answers with as little as it could and still be read as
// JSON: the same shape at every endpoint, from {} to items whose every field is
// null. /v2 sends every field today; a server that changes its shape, or a
// RATATOSK_URL that points somewhere else, may not. A release document without
// warnings took the whole server down (independent review, 2026-11-21): one
// call's panic reaches no recover in the SDK.
type Sparse struct {
	Name                                string
	Projects, Releases, Upgrade, Search string
	// Doc is the release document served for version.
	Doc func(version string) string
}

// SparseShapes are the shapes a Sparse takes. The project is etcd, its
// releases v3.5.30 and v3.6.8 where a shape names any. The last shape's
// documents carry a critical security change citing CVE-2026-0001 on every
// release and no warnings: from v3.5.30 the change on v3.6.8 is one the
// running branch already fixed, which is the path that failed.
func SparseShapes() []Sparse {
	nullDoc := `{"project":null,"version":null,"released_at":null,"url":null,"counts":null,"warnings":null,"processing":null,` +
		`"before_upgrade":null,"changes":null,"later":null}`
	nullItems := func(v string) string {
		return fmt.Sprintf(`{"version":%q,"warnings":null,"before_upgrade":{"prerequisites":[{"id":null,"title":null,"body":null,"folded":[null]},null],`+
			`"known_issues":null,"upgrade_guide":[]},"changes":[{"id":null,"kind":"security","title":null,"body":null,"severity":"critical",`+
			`"advisories":["CVE-2026-0001"],"affects":null,"action_required":null,"also_required":null,"no_downgrade":null,`+
			`"removed_in":null,"security_unclear":null,"workaround":null,"regression_since":null,"later":[null]},`+
			`{"id":null,"kind":"security","severity":null,"advisories":[null]},`+
			`{"id":null,"kind":"breaking","affects":{"text":null,"status":null},"no_downgrade":{"text":null,"status":null},`+
			`"removed_in":{"text":null,"status":"stated"}},null],"later":[null]}`, v)
	}
	return []Sparse{
		{Name: "null bodies", Projects: `null`, Releases: `null`, Upgrade: `null`, Search: `null`,
			Doc: func(string) string { return `null` }},
		{Name: "empty objects", Projects: `{}`, Releases: `{}`, Upgrade: `{}`, Search: `{}`,
			Doc: func(string) string { return `{}` }},
		{Name: "every field null", Projects: `{"projects":null}`, Releases: `{"releases":null,"next_before":null}`,
			Upgrade: `{"project":null,"from":null,"to":null,"releases":null,"gaps":null,"later_for_from":null}`,
			Search:  `{"q":null,"in_analysis":null,"in_release_notes":null,"in_linked_documents":null}`,
			Doc:     func(string) string { return nullDoc }},
		{Name: "empty lists", Projects: `{"projects":[]}`, Releases: `{"releases":[],"next_before":null}`,
			Upgrade: `{"project":"etcd","from":"v3.5.30","to":"v3.6.8","releases":[],"gaps":[],"later_for_from":[]}`,
			Search:  `{"q":"etcd","in_analysis":[],"in_release_notes":[],"in_linked_documents":[]}`,
			Doc: func(v string) string {
				return fmt.Sprintf(`{"project":"etcd","version":%q,"counts":{},"warnings":{},`+
					`"before_upgrade":{"prerequisites":[],"known_issues":[],"upgrade_guide":[]},"changes":[],"later":[]}`, v)
			}},
		{Name: "identity only", Projects: `{"projects":[{"slug":"etcd"}]}`,
			Releases: `{"releases":[{"project":"etcd","version":"v3.6.8"},{"project":"etcd","version":"v3.5.30"}]}`,
			Upgrade:  `{"project":"etcd","releases":[{"version":"v3.6.8"}]}`,
			Search:   `{"in_analysis":[{"project":"etcd","version":"v3.6.8"}],"in_release_notes":[{"project":"etcd","version":"v3.6.8"}]}`,
			Doc:      func(v string) string { return fmt.Sprintf(`{"version":%q}`, v) }},
		{Name: "null items", Projects: `{"projects":[{"slug":"etcd","name":null,"image_aliases":null,"latest":null},null]}`,
			Releases: `{"releases":[{"project":"etcd","version":"v3.6.8","released_at":null,"url":null,"counts":null,"warnings":null},` +
				`{"project":"etcd","version":"v3.5.30","released_at":null,"url":null,"counts":null,"warnings":null},null],"next_before":null}`,
			Upgrade: `{"project":"etcd","from":"v3.5.30","to":null,"releases":[` + nullItems("v3.6.8") + `,null],"gaps":null,"later_for_from":[null]}`,
			Search: `{"in_analysis":[{"project":"etcd","version":"v3.6.8","matched":{"id":null,"kind":null,"title":null}},null],` +
				`"in_release_notes":[null],"in_linked_documents":null}`,
			Doc: nullItems},
	}
}

// Handler serves s at every path a tool reads.
func (s Sparse) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch p := r.URL.Path; {
		case p == "/v2/projects":
			w.Write([]byte(s.Projects))
		case p == "/v2/releases":
			w.Write([]byte(s.Releases))
		case strings.HasPrefix(p, "/v2/releases/"):
			w.Write([]byte(s.Doc(path.Base(p))))
		case strings.HasPrefix(p, "/v2/upgrade/"):
			w.Write([]byte(s.Upgrade))
		case p == "/v2/search":
			w.Write([]byte(s.Search))
		default:
			http.NotFound(w, r)
		}
	}
}
