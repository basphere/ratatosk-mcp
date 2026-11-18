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
// An advisory never holds two ids of one family. When the links of a range
// would make such a group, the cards disagree about which ids belong together
// and nothing here can tell which card is wrong, so none of that group's links
// is used: each of its ids stays an advisory of its own, as in 1.0.0. Envoy
// v1.39.0 has two cards citing [CVE-2026-48743, GHSA-p7c7-7c47-pwch], while
// six cards elsewhere pair that CVE with GHSA-8phg-2h2q-jgxf (headers-only
// Content-Length) and GHSA-p7c7 is the HTTP/3 QPACK fix. Chained, the three
// ids were one advisory: distinct_advisories counted one where there are two,
// and the QPACK fix lost the also_in it had in 1.0.0 (independent review,
// 2026-11-18). The rule decides per group, after every link of the range is
// known, so it does not depend on the order the releases are read in. Its
// cost is the other way round: an advisory whose ids sit in a disputed group
// is counted once per id (envoy: three where there are two). Overcounting is
// the side to err on — a count that merges two advisories hides a fix.
//
// Only ids in the advisory families the ratatosk worker recognises (its
// reader.advisoryPattern) take part. Anything else in advisories is not an
// advisory id, so it can never join two fixes; a change that cites nothing
// but such values keeps the 1.0.0 identity, its whole id set.
//
// What this index does NOT promise: a card that cites a wrong id and that no
// other card contradicts still links it. That is why grouping changes into one
// briefing entry (sameFix.group) asks more than this index does.

import (
	"cmp"
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

// fixKey names the advisories a security change cites, as one string: changes
// with the same fixKey cite the same advisories, so a change citing [GHSA-x]
// and one citing [CVE-y, GHSA-x] (y and x linked, undisputed) have one key,
// and the eight-advisory card has a key of its own. Same key is necessary for
// two briefing entries to be grouped, not sufficient (sameFix.group). A
// change citing only unrecognised values gets its whole id set as "raw:…";
// "" means no ids at all.
func (ix *advisoryIndex) fixKey(ids []string) string {
	if advs := ix.advisories(ids); len(advs) > 0 {
		return strings.Join(advs, ",")
	}
	if k := advisoryKey(ids); k != "" {
		return "raw:" + k
	}
	return ""
}

// sameFix is the second grouping step of buildBriefing: it joins entries of
// the first step (whole id sets, as 1.0.0 grouped) that are one fix cited
// under different ids. The case that asked for it, Argo CD's GHSA-3v3m (alone
// on v3.2.11, with CVE-2026-42880 on v3.3.9), stays two entries: v3.3.9 says
// more (regression since 3.3.0, another affects) — see group.
type sameFix struct {
	ix    *advisoryIndex
	at    map[string]int // version → its place in the range
	first map[string]int // fix key + "|" + branch → place of the first release on that branch citing it
}

func newSameFix(ix *advisoryIndex) *sameFix {
	return &sameFix{ix: ix, at: map[string]int{}, first: map[string]int{}}
}

// cited records a security change buildBriefing lists or folds (not one it
// settles), at place at of the range.
func (s *sameFix) cited(ids []string, ver string, at int) {
	s.at[ver] = at
	k := s.ix.fixKey(ids)
	if k == "" || strings.HasPrefix(k, "raw:") {
		return
	}
	if _, ok := s.first[k+"|"+branchOf(ver)]; !ok {
		s.first[k+"|"+branchOf(ver)] = at
	}
}

// group joins an entry into the earliest entry before it when all of these
// hold, and leaves it listed on its own otherwise:
//
//   - both cite the same advisories (one fixKey, recognised ids only);
//   - every change the two stand for shares an id with every other change
//     there: one id in common is the same fix, a chain through a third card
//     is not — a card citing a wrong id must not pull two fixes together;
//   - neither is a follow-up: each is on the first release of its branch that
//     cites the advisory, under any of its ids (a later release of a branch
//     that fixed it before fixed it again, and is listed — see advOnBranch in
//     buildBriefing);
//   - between them they name at most one release per branch;
//   - the joining entry carries nothing the other lacks or says differently
//     (addsToEntry).
//
// Why the last rule refuses rather than handing the joining entry's fields to
// the entry it joins: an entry has one affects, one workaround, one
// regression_since. Keeping only the surviving entry's value is what lost six
// workarounds, three regression_since values and two also_required steps on
// live ranges (independent review, 2026-11-18) — 1.0.0 had listed each of
// them. Two values on one entry would need a new field, and a 1.0.0 client
// reads the old one and gets the survivor's value for every release in
// also_in: a poorer answer, not an added one. So an entry that would lose
// something stays as 1.0.0 listed it. What that costs, measured on seven live
// ranges the same day (argo, envoy, containerd, istio, kyverno, cilium,
// kubernetes): 65 pairs of entries met every other rule here, all 65 differ in
// affects (36 in affects alone — the worker words it per release), so none
// joined. The step stays for the cards that do say the same; whether to keep
// it is a product call, not a correctness one.
func (s *sameFix) group(cs []briefChange) []briefChange {
	type joinable struct {
		fix    string
		idSets [][]string // the recognised id sets of the changes it stands for
	}
	info := make([]*joinable, len(cs))
	for i, c := range cs {
		if c.Kind != "security" {
			continue
		}
		fix := s.ix.fixKey(c.Advisories)
		if fix == "" || strings.HasPrefix(fix, "raw:") || s.first[fix+"|"+branchOf(c.Version)] != s.at[c.Version] {
			continue
		}
		info[i] = &joinable{fix: fix, idSets: [][]string{recognisedIDs(c.Advisories)}}
	}
	gone := make([]bool, len(cs))
	for j := range cs {
		if info[j] == nil {
			continue
		}
		for i := range j {
			if gone[i] || info[i] == nil || info[i].fix != info[j].fix || !everyPairShares(info[i].idSets, info[j].idSets) ||
				!oneReleasePerBranch(cs[i], cs[j]) || addsToEntry(cs[j], cs[i]) {
				continue
			}
			s.join(&cs[i], cs[j])
			info[i].idSets = append(info[i].idSets, info[j].idSets...)
			gone[j] = true
			break
		}
	}
	out := cs[:0]
	for i, c := range cs {
		if !gone[i] {
			out = append(out, c)
		}
	}
	return out
}

// join folds entry e into into: its release and also_in join into's also_in
// in range order, into takes the higher grade and every id e cites.
func (s *sameFix) join(into *briefChange, e briefChange) {
	vs := slices.Clone(into.AlsoIn)
	for _, v := range append([]string{e.Version}, e.AlsoIn...) {
		if v != into.Version && !slices.Contains(vs, v) {
			vs = append(vs, v)
		}
	}
	slices.SortStableFunc(vs, func(a, b string) int { return cmp.Compare(s.at[a], s.at[b]) })
	into.AlsoIn = vs
	if gradeRank(e.Severity) > gradeRank(into.Severity) {
		into.Severity = e.Severity
	}
	into.Advisories = mergeAdvisoryIDs(into.Advisories, e.Advisories)
	into.members = append(append(slices.Clip(into.members), e.ID), e.members...)
}

// everyPairShares says every id set in a has an id in common with every id
// set in b.
func everyPairShares(a, b [][]string) bool {
	for _, x := range a {
		for _, y := range b {
			if !slices.ContainsFunc(x, func(id string) bool { return slices.Contains(y, id) }) {
				return false
			}
		}
	}
	return true
}

// oneReleasePerBranch says entries a and b, taken together, name no two
// different releases of one branch.
func oneReleasePerBranch(a, b briefChange) bool {
	for _, va := range append([]string{a.Version}, a.AlsoIn...) {
		for _, vb := range append([]string{b.Version}, b.AlsoIn...) {
			if va != vb && branchOf(va) == branchOf(vb) {
				return false
			}
		}
	}
	return true
}

// mergeAdvisoryIDs returns into with the ids of more it lacks appended, as a
// new slice when anything was added. An entry that stands for one fix on
// several releases names every id those releases cite: joining a release that
// cites [CVE-y, GHSA-x] to an entry citing [GHSA-x] must not make the CVE
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
