package main

// Which security changes in a range are the same fix, and how many different
// advisories a range fixes.
//
// Why this exists (1.1.0, from an outside agent's field report, 2026-11-18):
// a project ships one fix on several release lines, and the advisory ids a
// change cites are not always the same set on each. Argo CD's GHSA-3v3m was
// cited as [GHSA-3v3m] on v3.2.11 and as [CVE-2026-42880, GHSA-3v3m] on v3.3.9;
// 1.0.0 matched whole id sets, so the two came back as two fixes. Over the
// whole board, 73 GHSAs in 22 projects carry a different id set on different
// releases of one project. And summary.counts adds up every release, so
// Argo CD v2.10.7 → v3.6.1 said 15 critical where five advisories are critical.
//
// What links ids into one advisory: two ids cited together by a security
// change that names at most one id per family (one CVE, one GHSA, ...) — an
// advisory and its aliases. Links chain: a CVE linked to a GHSA on one release
// and that GHSA linked to a GO id on another make one advisory of all three.
// A change that cites two ids of one family names several advisories, and its
// ids are not linked: Argo CD v3.3.15 carries a card "Eight Argo CD security
// flaws fixed" citing eight GHSAs next to a card for each of them. Linked
// through it, the eight would have become one fix — one entry in place of
// eight, and one advisory counted where there are eight.
//
// Only ids in the advisory families the ratatosk worker recognises (its
// reader.advisoryPattern) take part. Anything else in advisories is not an
// advisory id, so it can never join two fixes; a change that cites nothing
// but such values keeps the 1.0.0 identity, its whole id set.

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

// namesOneAdvisory says a change's recognised ids are one advisory under its
// names: at most one id per family.
func namesOneAdvisory(ids []string) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		fam, _, _ := strings.Cut(id, "-")
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
	return ix
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

// fixKey is the identity of a security fix inside a range: the advisories it
// names. Two changes are the same fix when they name the same advisories, so a
// change citing [GHSA-x] and one citing [CVE-y, GHSA-x] are one fix, and the
// eight-advisory card is not the same fix as any of its eight. A change citing
// only unrecognised values keeps its whole id set as its identity; "" means no
// identity (no ids at all).
func (ix *advisoryIndex) fixKey(ids []string) string {
	if advs := ix.advisories(ids); len(advs) > 0 {
		return strings.Join(advs, ",")
	}
	if k := advisoryKey(ids); k != "" {
		return "raw:" + k
	}
	return ""
}

// mergeAdvisoryIDs returns into with the ids of more it lacks appended, as a
// new slice when anything was added. An entry that stands for one fix on
// several releases names every id those releases cite: folding v3.3.9's
// [CVE-2026-42880, GHSA-3v3m] into v3.2.11's [GHSA-3v3m] must not make the CVE
// disappear from the answer.
func mergeAdvisoryIDs(into, more []string) []string {
	have := map[string]bool{}
	for _, id := range into {
		have[normAdvisoryID(id)] = true
	}
	var out []string
	for _, id := range more {
		n := normAdvisoryID(id)
		if n == "" || have[n] {
			continue
		}
		have[n] = true
		if out == nil {
			out = slices.Clone(into)
		}
		out = append(out, id)
	}
	if out == nil {
		return into
	}
	return out
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
// cites alone. Taken for every advisory it cites, Argo CD's range would count
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

// gradeRankOf is gradeRank for a grade as text.
func gradeRankOf(g string) int {
	switch strings.ToLower(strings.TrimSpace(g)) {
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
