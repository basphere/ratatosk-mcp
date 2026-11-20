package main

// search's project filter and per-release matches (1.2.0).
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
	"strings"
	"sync"

	"github.com/basphere/ratatosk-mcp/internal/release"
)

// searchMatchReleases bounds the release documents one search call reads for
// matches (the spec, revised 2026-11-20): a document is up to 870 KB (cilium
// v1.20.0), the hosted endpoint shares one upstream allowance among its
// callers, and the first build read up to 40 documents a call (20 releases,
// in lang and in English). Each release is read once, in lang. A release past
// the bound, or one that could not be read, carries no matches and is counted
// in matches_not_computed. "BackendTLSPolicy" with project cilium matched 4
// releases on 2026-11-20; without project, 11. If /v2/search ever names every
// matching item itself, this reading goes.
const searchMatchReleases = 8

// maxMatches bounds matches per release; the rest are counted in
// matches_omitted.
const maxMatches = 20

// idShapedRe is a complete advisory id, matched whole: the website's search
// (apps/web/src/server/search.ts, ID_SHAPED) answers CVE-2026-2757 with that id
// only, never CVE-2026-27571. q arrives lower-cased.
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
// bake/search.go: title, body, who it applies to, what it requires, advisory
// ids, evidence quotes), lower-cased, plus the entries folded under a
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
// read in lang only (one document per release — searchMatchReleases): titles
// and bodies in that language, evidence quotes and advisory ids as published.
// The website's index holds every language, so a query in English text can
// match a release here on its English title alone; the server's own matched
// item is always among the matches, so such a release still lists that one.
func releaseMatches(ctx context.Context, project, ver, lang string, find func(string) bool, matched *searchMatch) ([]searchMatch, error) {
	read := func(l string) (matchDoc, error) {
		var d matchDoc
		b, err := api.releaseRaw(ctx, project, ver, l, true)
		if err != nil {
			return d, err
		}
		err = json.Unmarshal(b, &d)
		return d, err
	}
	doc, err := read(lang)
	if err != nil {
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
func searchAnswer(ctx context.Context, raw []byte, project, q, lang string) ([]byte, error) {
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
					m, err := releaseMatches(ctx, jb.h.Project, jb.h.Version, lang, find, jb.h.Matched)
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
