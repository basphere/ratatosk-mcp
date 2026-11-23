package tools

// search: the website's search, with a project filter and per-release
// matches (1.2.0).
//
// Why: /v2/search names one matched item per release (matched — the first in
// screen order) and takes no project. On 2026-11-20 agents searching
// "BackendTLSPolicy" for a cilium upgrade got one item for cilium v1.20.0
// where seven changes mention it, and "gateway" answered 104 releases of 19
// projects for a question about one. Both are done here: the project filter
// cuts the server's groups, and matches reads the matched releases and lists
// every item that matches q, the way the server matches it.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/upstream"
)

// searchDef is search as a client lists it.
func searchDef() *mcp.Tool {
	return &mcp.Tool{
		Name:        "search",
		Annotations: &mcp.ToolAnnotations{Title: "Search releases", ReadOnlyHint: true},
		Description: "Find releases by an advisory id (CVE-…, GHSA-…), a configuration flag, or text, as the " +
			"website's search does. q matches case-insensitively anywhere in the text, so a partial id finds the full " +
			"one. A complete advisory ID (CVE-2026-1234, GHSA-xxxx-xxxx-xxxx) matches only that ID. Three groups: in_analysis (a change or before-upgrade item matched, or the project name or version; " +
			"matched names its kind, release for the latter, and title), in_release_notes (the release notes matched; excerpt shows where) and " +
			"in_linked_documents (a document the notes link matched). in_analysis lists releases matched by project name " +
			"or version first, then releases matched through a change item, each set newest first; the other two groups " +
			"list releases newest first. The groups are capped at 200, 100 and 100 results, respectively. Truncation is not marked, so narrow q if a group reaches " +
			"its limit. project keeps one project's releases; the caps apply before it, so narrow q as well when a group is long. " +
			"An in_analysis release matched through an item also carries matches: every item of that release that " +
			"matches q (id, kind, title; up to 20, matches_omitted counts the rest), on the first 8 such releases; " +
			"matches_not_computed, at the top, counts the ones that carry none — narrow with project to reach them. " +
			"A release matched through a folded dependency line (matched kind dependency_updates, no title) has no item " +
			"to list and carries no matches. " +
			"q is 2–200 characters. Drill in with get_release(project, version, " +
			"changes:[id, …]) for the matched items' body and evidence. " + DataNote,
	}
}

type searchArgs struct {
	Q       string `json:"q" jsonschema:"advisory id (CVE-…, GHSA-…), configuration flag, project, version or text; 2–200 characters"`
	Project string `json:"project,omitempty" jsonschema:"project slug, e.g. cilium: only that project's releases; omit for every project"`
}

func (h *handlers) searchTool(ctx context.Context, req *mcp.CallToolRequest, args searchArgs) (*mcp.CallToolResult, any, error) {
	ctx = logging.RequestContext(ctx, "search", req)
	q := strings.TrimSpace(args.Q)
	if n := len([]rune(q)); n < 2 || n > 200 {
		return errResult(fmt.Errorf("q must be 2–200 characters")), nil, nil
	}
	// An unknown slug is an error, as in list_releases: no results for a name
	// spelled differently reads as "nothing matched".
	project := slugArg(args.Project)
	if project != "" {
		roster, err := h.api.Projects(ctx)
		if err != nil {
			return errResult(fmt.Errorf("could not read the project list: %w", err)), nil, nil
		}
		if !slices.ContainsFunc(roster, func(p upstream.Project) bool { return p.Slug == project }) {
			return errResult(unknownProject(project)), nil, nil
		}
	}
	raw, err := h.api.SearchRaw(ctx, q, answerLang)
	if err != nil {
		return errResult(err), nil, nil
	}
	if !isJSONObject(raw) {
		return rawResult(raw), nil, nil
	}
	b, err := h.searchAnswer(ctx, raw, project, q)
	if err != nil {
		return errResult(fmt.Errorf("/v2/search: %w", err)), nil, nil
	}
	return rawResult(b), nil, nil
}

// searchMatchReleases bounds the release documents one search call reads for
// matches (the spec, revised 2026-11-20): a document is up to 870 KB (cilium
// v1.20.0), the hosted endpoint shares one upstream allowance among its
// callers, and the first build read up to 40 documents a call (20 releases,
// in the asked language and in English). Each release is read once, in
// English. A release past the bound, or one that could not be read, carries
// no matches and is counted in matches_not_computed. "BackendTLSPolicy" with project cilium matched 4
// releases on 2026-11-20; without project, 11. If /v2/search ever names every
// matching item itself, this reading goes.
const searchMatchReleases = 8

// maxMatches bounds matches per release; the rest are counted in
// matches_omitted.
const maxMatches = 20

// idShapedRe is a complete advisory id, matched whole: the website's search
// (apps/web/src/server) answers CVE-2026-2757 with that id only, never
// CVE-2026-27571. q arrives lower-cased.
var idShapedRe = regexp.MustCompile(`^((cve|go)-\d{4}-\d{4,}|rustsec-\d{4}-\d{4}|ghsa(-[0-9a-z]{4}){3})$`)

// searchFinder says whether lowered text matches q as the website's finder
// does: anywhere for text, and for a complete advisory id only where it is
// not part of a longer id.
func searchFinder(q string) func(lowered string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if !idShapedRe.MatchString(q) {
		return func(t string) bool { return strings.Contains(t, q) }
	}
	alnum := func(c byte) bool { return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') }
	return func(t string) bool {
		for from := 0; ; {
			i := strings.Index(t[from:], q)
			if i < 0 {
				return false
			}
			i += from
			before := i > 0 && alnum(t[i-1])
			after := i+len(q) < len(t) && alnum(t[i+len(q)])
			if !before && !after {
				return true
			}
			from = i + 1
		}
	}
}

type searchMatch struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

// matchDoc is what matches reads of a release document.
type matchDoc struct {
	BeforeUpgrade struct {
		Prerequisites []matchItem `json:"prerequisites"`
		KnownIssues   []matchItem `json:"known_issues"`
		UpgradeGuide  []matchItem `json:"upgrade_guide"`
	} `json:"before_upgrade"`
	Changes []matchItem `json:"changes"`
}

type matchItem struct {
	ID             string        `json:"id"`
	Kind           string        `json:"kind"`
	Title          string        `json:"title"`
	Body           *string       `json:"body"`
	Affects        *release.Attr `json:"affects"`
	ActionRequired *string       `json:"action_required"`
	AlsoRequired   *string       `json:"also_required"`
	ToUse          *string       `json:"to_use"`
	Advisories     []string      `json:"advisories"`
	Evidence       []struct {
		Quote string `json:"quote"`
	} `json:"evidence"`
	Folded []struct {
		Title string  `json:"title"`
		Body  *string `json:"body"`
	} `json:"folded"`
}

// hay is the item's text as the website's index holds it (the worker's
// apps/worker/internal/bake), lower-cased, plus the entries folded under a
// before-upgrade item.
func (it matchItem) hay() string {
	parts := []string{it.Title, strOf(it.Body), strOf(it.ActionRequired), strOf(it.AlsoRequired), strOf(it.ToUse)}
	if it.Affects != nil {
		parts = append(parts, it.Affects.Text)
	}
	parts = append(parts, it.Advisories...)
	for _, e := range it.Evidence {
		parts = append(parts, e.Quote)
	}
	for _, f := range it.Folded {
		parts = append(parts, f.Title, strOf(f.Body))
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

// strOf is the text of an optional field, "" when it is absent.
func strOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// items lists a document's items in screen order: before-upgrade first, then
// the changes. A before-upgrade item's kind is "before_upgrade", as matched
// names it.
func (d matchDoc) items() []matchItem {
	var out []matchItem
	for _, sec := range [][]matchItem{d.BeforeUpgrade.Prerequisites, d.BeforeUpgrade.KnownIssues, d.BeforeUpgrade.UpgradeGuide} {
		for _, it := range sec {
			it.Kind = "before_upgrade"
			out = append(out, it)
		}
	}
	return append(out, d.Changes...)
}

// releaseMatches lists the items of one release that match q. The release is
// read once, in English (one document per release — searchMatchReleases):
// titles and bodies in English, evidence quotes and advisory ids as published.
// The website's index holds every language, so a query in Korean or Japanese
// text can match a release there on a title in that language alone; the
// server's own matched item is always among the matches, so such a release
// still lists that one.
func (h *handlers) releaseMatches(ctx context.Context, project, ver string, find func(string) bool, matched *searchMatch) ([]searchMatch, error) {
	var doc matchDoc
	b, err := h.api.ReleaseRaw(ctx, project, ver, answerLang, true)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	var out []searchMatch
	for _, it := range doc.items() {
		if find(it.hay()) {
			out = append(out, searchMatch{ID: it.ID, Kind: it.Kind, Title: it.Title})
		}
	}
	if matched != nil && matched.Title != "" {
		in := false
		for _, m := range out {
			if m.Kind == matched.Kind && m.Title == matched.Title {
				in = true
				break
			}
		}
		if !in {
			for _, it := range doc.items() {
				if it.Kind == matched.Kind && it.Title == matched.Title {
					out = append([]searchMatch{{ID: it.ID, Kind: it.Kind, Title: it.Title}}, out...)
					break
				}
			}
		}
	}
	if out == nil {
		out = []searchMatch{}
	}
	return out, nil
}

// searchAnswer cuts a /v2/search answer to one project (when project is set)
// and adds matches to the in_analysis releases matched through an item, and
// matches_not_computed, first, for those that carry none. Everything else is
// passed on as received.
func (h *handlers) searchAnswer(ctx context.Context, raw []byte, project, q string) ([]byte, error) {
	fields, err := readObject(raw)
	if err != nil {
		return nil, err
	}
	find := searchFinder(q)
	notComputed := 0
	for i, fl := range fields {
		switch fl.Key {
		case "in_analysis", "in_release_notes", "in_linked_documents":
		default:
			continue
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(fl.Val, &rows); err != nil {
			return nil, fmt.Errorf("%s: %w", fl.Key, err)
		}
		type rowHead struct {
			Project string       `json:"project"`
			Version string       `json:"version"`
			Matched *searchMatch `json:"matched"`
		}
		kept := []json.RawMessage{}
		var heads []rowHead
		for _, r := range rows {
			var h rowHead
			if err := json.Unmarshal(r, &h); err != nil {
				return nil, fmt.Errorf("%s: %w", fl.Key, err)
			}
			if project != "" && h.Project != project {
				continue
			}
			kept = append(kept, r)
			heads = append(heads, h)
		}
		if fl.Key == "in_analysis" {
			type job struct {
				i int
				h rowHead
			}
			var jobs []job
			for j, h := range heads {
				if h.Matched == nil || h.Matched.Kind == "release" {
					continue // matched by project name or version: no item to list
				}
				// Matched through a folded dependency line (the server names it
				// dependency_updates with no title): a line, not an item, so
				// reading the release lists nothing and spends one of the eight
				// reads (independent review, 2026-11-20).
				if h.Matched.Kind == "dependency_updates" && h.Matched.Title == "" {
					continue
				}
				if len(jobs) == searchMatchReleases {
					notComputed++
					continue
				}
				jobs = append(jobs, job{j, h})
			}
			found := make([][]searchMatch, len(kept))
			ok := make([]bool, len(kept))
			var wg sync.WaitGroup
			sem := make(chan struct{}, fetchParallel)
			for _, jb := range jobs {
				wg.Add(1)
				sem <- struct{}{}
				go func(jb job) {
					defer wg.Done()
					defer func() { <-sem }()
					m, err := h.releaseMatches(ctx, jb.h.Project, jb.h.Version, find, jb.h.Matched)
					if err != nil {
						return // that release carries no matches; the row itself is still answered
					}
					found[jb.i], ok[jb.i] = m, true
				}(jb)
			}
			wg.Wait()
			for _, jb := range jobs {
				if !ok[jb.i] {
					notComputed++
				}
			}
			for j := range kept {
				if !ok[j] {
					continue
				}
				rf, err := readObject(kept[j])
				if err != nil {
					return nil, err
				}
				m := found[j]
				omitted := 0
				if len(m) > maxMatches {
					omitted = len(m) - maxMatches
					m = m[:maxMatches]
				}
				mb, _ := json.Marshal(m)
				rf = append(rf, objField{Key: "matches", Val: mb})
				if omitted > 0 {
					rf = append(rf, objField{Key: "matches_omitted", Val: json.RawMessage(fmt.Sprint(omitted))})
				}
				kept[j] = writeObject(rf)
			}
		}
		fields[i].Val = writeArray(kept)
	}
	nc, _ := json.Marshal(notComputed)
	return writeObject(append([]objField{{Key: "matches_not_computed", Val: nc}}, fields...)), nil
}
