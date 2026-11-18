package main

// How many different advisories a range fixes: summary.distinct_advisories
// (1.1.0).
//
// Why this exists (an outside agent's field report, 2026-11-18):
// summary.counts and summary.warnings add up every release in range, and a
// project ships one fix on several release lines, so Argo CD v2.10.7 → v3.6.1
// said 15 critical where five advisories are critical, and the agent passed 15
// on. This counts each advisory once. It changes nothing else in a briefing:
// which changes are listed, and how they are grouped into also_in, is the
// 1.0.0 rule (whole id sets, buildBriefing) — a draft that also grouped by
// shared ids answered less than 1.0.0 on live ranges and was taken out
// (2026-11-18).
//
// What links ids into one advisory: two ids cited together by a security
// change that names at most one id per family (one CVE, one GHSA, ...) — an
// advisory and its aliases. Links chain: a CVE linked to a GHSA on one release
// and that GHSA linked to a GO id on another make one advisory of all three.
// A change that cites two ids of one family names several advisories, and its
// ids are not linked: Argo CD v3.3.15 carries a card "Eight Argo CD security
// flaws fixed" citing eight GHSAs next to a card for each of them. Linked
// through it, the eight would have been counted as one advisory.
//
// An advisory never holds two ids of one family. When the links of a range
// would make such a group, the cards disagree about which ids belong together
// and nothing here can tell which card is wrong, so none of that group's links
// is used: each of its ids counts on its own. Envoy v1.39.0 has two cards
// citing [CVE-2026-48743, GHSA-p7c7-7c47-pwch], while six cards elsewhere pair
// that CVE with GHSA-8phg-2h2q-jgxf (headers-only Content-Length) and
// GHSA-p7c7 is the HTTP/3 QPACK fix; chained, the three ids were counted as
// one advisory (independent review, 2026-11-18). The rule decides per group,
// after every link of the range is known, so it does not depend on the order
// the releases are read in. Its cost is the other way round: an advisory whose
// ids sit in a disputed group is counted once per id (envoy: three where there
// are two). Overcounting is the side to err on — a count that merges two
// advisories hides a fix.
//
// Only ids in the advisory families the ratatosk worker recognises (its
// reader.advisoryPattern) take part. Anything else in advisories is not an
// advisory id and links nothing.

import (
	"regexp"
	"slices"
	"sort"
	"strings"
)

// advisoryIDRe is one whole advisory id: GHSA, CVE, GO, RUSTSEC or PYSEC,
// matched as the worker matches them (apps/worker/internal/reader/refs.go,
// advisoryPattern).
var advisoryIDRe = regexp.MustCompile(`(?i)^(?:GHSA(?:-[23456789cfghjmpqrvwx]{4}){3}|CVE-\d{4}-\d{4,7}|GO-\d{4}-\d{4,}|RUSTSEC-\d{4}-\d{4,}|PYSEC-\d{4}-\d+)$`)

// normAdvisoryID is the form ids are compared in: upper case, no surrounding
// space (advisoryKey has always compared this way).
func normAdvisoryID(id string) string {
	return strings.ToUpper(strings.TrimSpace(id))
}

// recognisedIDs returns the advisory ids among ids, normalised, sorted and
// without repeats.
func recognisedIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		if n := normAdvisoryID(id); advisoryIDRe.MatchString(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// idFamily is the family of a normalised advisory id: CVE, GHSA, GO, ...
func idFamily(id string) string {
	fam, _, _ := strings.Cut(id, "-")
	return fam
}

// namesOneAdvisory says a change's recognised ids are one advisory under its
// names: at most one id per family.
func namesOneAdvisory(ids []string) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		fam := idFamily(id)
		if seen[fam] {
			return false
		}
		seen[fam] = true
	}
	return true
}

// advisoryIndex maps every recognised id cited by the security changes of a
// range to the advisory it names (a union-find over the links above). The
// root of each advisory is its smallest id, so keys do not depend on the order
// the releases were read in.
type advisoryIndex struct {
	parent map[string]string
}

func newAdvisoryIndex(docs []*v2Release) *advisoryIndex {
	ix := &advisoryIndex{parent: map[string]string{}}
	for _, d := range docs {
		for _, c := range d.Changes {
			if c.Kind != "security" {
				continue
			}
			ids := recognisedIDs(c.Advisories)
			if len(ids) > 1 && namesOneAdvisory(ids) {
				for _, id := range ids[1:] {
					ix.union(ids[0], id)
				}
			}
		}
	}
	ix.dropDisputed()
	return ix
}

// dropDisputed unlinks every group that holds two ids of one family (see the
// file comment): all of its ids become advisories of their own. A group is
// judged whole, once every link is in, so the outcome is the same whatever
// order the links arrived in.
func (ix *advisoryIndex) dropDisputed() {
	type famOf struct{ root, fam string }
	first := map[famOf]string{}
	disputed := map[string]bool{}
	roots := make(map[string]string, len(ix.parent))
	for id := range ix.parent {
		r := ix.find(id)
		roots[id] = r
		k := famOf{r, idFamily(id)}
		if f, ok := first[k]; ok && f != id {
			disputed[r] = true
		} else {
			first[k] = id
		}
	}
	for id, r := range roots {
		if disputed[r] {
			ix.parent[id] = id
		}
	}
}

func (ix *advisoryIndex) find(id string) string {
	p, ok := ix.parent[id]
	if !ok || p == id {
		return id
	}
	r := ix.find(p)
	ix.parent[id] = r
	return r
}

func (ix *advisoryIndex) union(a, b string) {
	ra, rb := ix.find(a), ix.find(b)
	if ra == rb {
		return
	}
	if rb < ra {
		ra, rb = rb, ra
	}
	ix.parent[ra] = ra
	ix.parent[rb] = ra
}

// advisories names the advisories a change cites, one root id each, sorted.
// nil when it cites no recognised id.
func (ix *advisoryIndex) advisories(ids []string) []string {
	rec := recognisedIDs(ids)
	if len(rec) == 0 {
		return nil
	}
	out := make([]string, len(rec))
	for i, id := range rec {
		out[i] = ix.find(id)
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// distinctAdvisories counts the advisories a range's security changes cite,
// each once, by grade.
type distinctAdvisories struct {
	Total    int `json:"total"`
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	// Ungraded counts advisories no citing change carries a grade for — the
	// same null as severity: no grade was found in what was read.
	Ungraded int `json:"ungraded"`
}

// advisoryTally collects the security changes of a range for
// distinctAdvisories.
//
// An advisory's grade is the highest grade of the changes that cite it alone.
// A change citing several advisories carries the highest of their grades (the
// eight-advisory card is critical because four of the eight are), so it says
// nothing about each one; its grade is used only for an advisory no change
// cites alone, and then it is the highest grade of those several-advisory
// changes. Taken for every advisory it cites, Argo CD's range would count
// nine critical advisories where five are.
type advisoryTally struct {
	seen        map[string]bool
	aloneGrade  map[string]int
	citedAlone  map[string]bool
	sharedGrade map[string]int
}

func newAdvisoryTally() *advisoryTally {
	return &advisoryTally{seen: map[string]bool{}, aloneGrade: map[string]int{}, citedAlone: map[string]bool{}, sharedGrade: map[string]int{}}
}

// add records one security change. advs are its advisories (from
// advisoryIndex.advisories); with none recognised, each value it cites counts
// as an advisory of its own.
func (t *advisoryTally) add(advs, ids []string, severity *string) {
	if len(advs) == 0 {
		for _, id := range ids {
			if n := normAdvisoryID(id); n != "" {
				advs = append(advs, "raw:"+n)
			}
		}
		sort.Strings(advs)
		advs = slices.Compact(advs)
	}
	g := gradeRankOf(strOf(severity))
	for _, a := range advs {
		t.seen[a] = true
		if len(advs) == 1 {
			t.citedAlone[a] = true
			t.aloneGrade[a] = max(t.aloneGrade[a], g)
		} else {
			t.sharedGrade[a] = max(t.sharedGrade[a], g)
		}
	}
}

func (t *advisoryTally) counts() distinctAdvisories {
	var out distinctAdvisories
	for a := range t.seen {
		g := t.sharedGrade[a]
		if t.citedAlone[a] {
			g = t.aloneGrade[a]
		}
		out.Total++
		switch g {
		case 4:
			out.Critical++
		case 3:
			out.High++
		case 2:
			out.Medium++
		case 1:
			out.Low++
		default:
			out.Ungraded++
		}
	}
	return out
}

// gradeRankOf is gradeRank for a grade as text (the 1.0.0 comparison: case
// folded, nothing trimmed).
func gradeRankOf(g string) int {
	switch strings.ToLower(g) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium", "moderate":
		return 2
	case "low":
		return 1
	}
	return 0
}
