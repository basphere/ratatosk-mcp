// Package briefing compresses the releases of an upgrade range into what an
// agent decides on: the changes that ask for a decision (security, breaking,
// deprecations, important fixes), capped per kind with one line for each that
// did not fit, the before-upgrade items, the counts, what was learned later
// about the range, and the security changes the running branch already
// fixed. check_stack answers it per component and check_upgrade once.
//
// It is a pure computation over the release documents it is given
// (internal/release): no client, no network, no state kept between calls. It
// must not import internal/upstream — a briefing that could fetch would make
// what goes upstream depend on the running version, which is what check_stack
// exists to prevent. Being pure is also what lets the 1.x regression harness
// (ranges_test.go) run the frozen 1.0.0 code (frozen100_test.go) beside it on
// recorded ranges, change by change.
package briefing

import (
	"encoding/json"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/advisory"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

// DecisionKinds are listed change by change; the rest are counted. They are
// the kinds where an upgrade asks something of the operator: a fix they need,
// a behavior they must adapt to, a removal to plan for, a defect they keep if
// they stay. Never modified.
var DecisionKinds = []string{"security", "breaking", "deprecations", "important_fixes"}

// kindCaps bounds the changes listed per component, per kind; the cut is
// counted per kind in changes_omitted, never silent. Per kind, not one cap over
// a sorted list: measured on the preview API (2026-11-03), envoy v1.36.0 →
// v1.38.3 has 253 changes in range, 55 of the first 60 security — a single cap
// would have cut every breaking change and important fix without a trace in
// the list itself. Security is ordered by grade first, so what is cut is the
// least severe; inside a grade, and inside the kinds that have no grade,
// entries that say what to do come first (see carriesAStep), so what is cut is
// an entry that asks nothing of the operator.
var kindCaps = map[string]int{"security": 20, "breaking": 20, "deprecations": 10, "important_fixes": 10}

// Cap is how many changes of kind a briefing lists with their body; the rest
// are counted in changes_omitted. 0 for a kind a briefing only counts.
// Exported for check_upgrade's test, which holds a whole answer's entries to
// these caps: a test in another package cannot read kindCaps, and caps copied
// into it would stop checking the code once the caps changed.
func Cap(kind string) int { return kindCaps[kind] }

// briefTextChars bounds body, workaround and regression_since text in a
// briefing. Measured on the same envoy range: body was the largest single
// field (31% of the change list). The full text is one get_release away; the
// cut is visible.
const briefTextChars = 160

func briefText(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= briefTextChars {
		return string(r)
	}
	return strings.TrimRight(string(r[:briefTextChars]), " ") + "…"
}

func kindRank(k string) int {
	if i := slices.Index(release.Kinds, k); i >= 0 {
		return i
	}
	return len(release.Kinds)
}

// RangeRelease is one release of the range in a briefing's releases.
type RangeRelease struct {
	Version    string         `json:"version"`
	ReleasedAt string         `json:"released_at"`
	URL        string         `json:"url"`
	Counts     map[string]int `json:"counts"`
	Warnings   map[string]int `json:"warnings,omitempty"`
	Processing bool           `json:"processing,omitempty"`
}

// stackSummary: Counts and Warnings add up every release in range, so a fix
// shipped on several release lines is counted once per release (Argo CD
// v2.10.7 → v3.6.1: critical 15). DistinctAdvisories counts each advisory
// once (critical 5) — added in 1.1.0 beside them, which keep their values.
type stackSummary struct {
	Releases int `json:"releases"`
	// DistinctAdvisories goes out before Counts and Warnings (1.2.0): a reader
	// takes the first number it meets, and an agent told the operator "Argo CD:
	// 15 critical" off warnings.critical with distinct_advisories.critical 5
	// right after it (field report, 2026-11-20). The names and values are 1.1.0's.
	DistinctAdvisories advisory.Counts `json:"distinct_advisories"`
	Counts             map[string]int  `json:"counts"`
	Warnings           map[string]int  `json:"warnings"`
}

// Briefing is the answer for one range (see Build). check_stack embeds it in
// a component's report, so its fields are the component's fields.
type Briefing struct {
	Summary *stackSummary `json:"summary,omitempty"`
	// Releases is a pointer so "[]" (checked, already on the newest) and
	// absent (not checked at all — see could_not_check) stay distinguishable.
	Releases *[]RangeRelease `json:"releases,omitempty"`
	// BeforeUpgrade is the 1.0.0 list: only the frozen 1.0.0 briefing fills it
	// (frozen100_test.go), and it is never answered. 1.2.0 answers Upgrade.
	BeforeUpgrade []briefItem `json:"-"`
	// Upgrade is a pointer for the same reason as Releases: "[]" is checked
	// and empty, absent is not checked. 1.1.0 left an empty list out, and four
	// agent runs could not tell the two apart (2026-11-20).
	Upgrade        *[]UpgradeItem `json:"before_upgrade,omitempty"`
	Changes        []Change       `json:"changes,omitempty"`
	ChangesOmitted map[string]int `json:"changes_omitted,omitempty"`
	// The rest is 1.2.0. ChangesMore, LaterInRange: see Build.
	ChangesMore      []MoreLine         `json:"changes_more,omitempty"`
	LaterInRange     *[]json.RawMessage `json:"later_in_range,omitempty"`
	ExcludedOnBranch *ExcludedOnBranch  `json:"excluded_on_branch,omitempty"`
	// folded counts the security changes folded into an entry of their own
	// release (listed in changes_more, counted in changes_omitted since the
	// independent review of 2026-11-20). The 1.0.0 harness takes them off
	// changes_omitted to compare.
	folded int
}

// Build compresses the releases in range (oldest first) into what an
// agent decides on, and says how many security changes it settled (left out
// as already fixed on the running branch). alreadyFixed holds advisory ids the running version's own
// branch carries at or below it; a security change citing only those is
// dropped and counted — maintainers backport one fix onto every supported
// branch, and an operator on 2.2.5 who already has CVE-x from 2.2.4 must not be
// told to upgrade to 2.3.1 for it (observed on a live cluster, 2026-08-10,
// containerd CVE-2026-46680 and CVE-2026-47262).
//
// runningKey is the running version's key. A release on the running version's
// own branch is above it (the range is exclusive), so a security change there
// is new work even when it cites an advisory the running version already
// carries: that is a follow-up to an incomplete fix, and dropping it hid a
// critical change behind a count (independent review, 2026-11-11). Only other
// branches' backports are settled. nil settles nothing.
//
// What 1.2.0 adds leaves summary, releases, changes, changes_omitted and the
// settled count as 1.1.0 answered them (ranges_test.go holds it to that):
//   - changes_more: every change cut by the per-kind caps, one line each, in
//     the order of changes. In 1.1.0 they were a count only; on 62 ranges of
//     three releases behind (2026-11-19), 20 cut breaking changes and 12 cut
//     security changes that way — containerd's enable_cdi removal was one.
//   - also a security change of one release that cites the same advisories as
//     the entry it was folded into, and carries nothing of its own: it left no
//     trace at all. coredns v1.14.7 → v1.15.0 counted 8 security changes,
//     listed 5 and omitted none — three Go fixes (an HTTP/2 crash, a trailer
//     memory exhaustion, an x509 check) stood under one "Go 1.27.3" entry
//     because their cards cite the same seven CVEs (2026-11-20). The
//     same holds on a backport release: its first change stands under the
//     entry's also_in, the others each get their line.
//   - before_upgrade as UpgradeItem, later_in_range, excluded_on_branch.
func Build(docs []*release.Doc, alreadyFixed map[string]bool, runningKey []int) (Briefing, int) {
	b := Briefing{
		Summary:  &stackSummary{Counts: map[string]int{}, Warnings: map[string]int{"critical": 0, "no_downgrade": 0}},
		Releases: &[]RangeRelease{},
	}
	settled := 0
	// distinct_advisories (1.1.0) counts every security change of the range
	// that is not settled, before the per-kind caps cut the list; it reads the
	// whole range first, as a link between two ids can sit on a later release
	// than the changes it joins (internal/advisory). It changes nothing below.
	ix := advisory.NewIndex(docs)
	tally := advisory.NewTally()
	firstByAdv := map[string]int{} // advisory key → index in b.Changes
	// advisory key + branch → an EARLIER release of that branch carried it. The
	// first release of a branch that carries an advisory already listed is the
	// backport and folds into also_in. A later release of a branch that carried
	// it before is a follow-up to an incomplete fix and is listed on its own:
	// folded, a critical follow-up was reduced to a version string under an
	// ungraded first entry (re-review, 2026-11-11). Marked only after a release's
	// changes are all read, so two changes of ONE release citing the same
	// advisories are not taken for a fix and its follow-up — on envoy that
	// listed 47 extra entries and pushed five distinct advisories out of the
	// cap of 20 (final review, same day; 36 of 37 repeats sat inside one release).
	advOnBranch := map[string]bool{}
	var folded []Change                 // security changes folded into an entry (or a backport) of their own release: each gets its line (1.2.0)
	excluded := map[string]int{}        // advisory id as compared → the highest grade a left-out change gave it
	excludedName := map[string]string{} // advisory id as compared → as the change spelled it
	// settledCritical: how many of the left-out security changes are critical.
	// warnings.critical — the release's own figure, as the website shows it —
	// keeps counting them, so a range said "critical 1" for a change that was
	// in no list: containerd v2.2.9 → v2.4.2, where v2.3.4's CVE-2026-95837
	// was fixed on the 2.2 branch at v2.2.7. Claude runs reported that 1, Codex
	// runs dropped it (24 runs, 2026-11-20). The count goes out beside the
	// figure it explains, as warnings.critical_excluded_on_branch.
	settledCritical := 0
	for _, d := range docs {
		inRelease := map[string]int{} // advisory key → the entry this release's changes stand under
		var touched []string          // advisory key + branch pairs to mark once the release is read
		onRunningBranch := false
		if k := version.NormalizeVersion(d.Version); k != nil && runningKey != nil {
			onRunningBranch = releaseline.SameBranch(k, runningKey)
		}
		b.Summary.Releases++
		for k, n := range d.Counts {
			b.Summary.Counts[k] += n
		}
		for k, n := range d.Warnings {
			b.Summary.Warnings[k] += n
		}
		rr := RangeRelease{Version: d.Version, ReleasedAt: d.ReleasedAt, URL: d.URL, Counts: d.Counts, Processing: d.Processing}
		if d.Warnings["critical"] > 0 || d.Warnings["no_downgrade"] > 0 {
			rr.Warnings = d.Warnings
		}
		*b.Releases = append(*b.Releases, rr)
		relSettledCritical := 0
		for _, c := range d.Changes {
			if !slices.Contains(DecisionKinds, c.Kind) {
				continue
			}
			key := ""
			if c.Kind == "security" {
				key = advisoryKey(c.Advisories)
				if key != "" {
					covered := true
					for _, id := range strings.Split(key, ",") {
						if !alreadyFixed[id] {
							covered = false
							break
						}
					}
					if covered && !onRunningBranch {
						settled++
						g := 0
						if c.Severity != nil {
							g = advisory.GradeRank(*c.Severity)
						}
						if g == advisory.GradeRank("critical") {
							relSettledCritical++
						}
						for _, raw := range c.Advisories {
							id := strings.ToUpper(strings.TrimSpace(raw))
							if id == "" {
								continue
							}
							if prev, seen := excluded[id]; !seen {
								excluded[id], excludedName[id] = g, strings.TrimSpace(raw)
							} else if g > prev {
								excluded[id] = g
							}
						}
						continue
					}
				}
				// distinct_advisories counts what the upgrade still fixes: an id
				// the running branch already carries is left out even when the
				// change citing it stays listed, because it cites others too.
				// Until 1.2.0 it was counted, at that change's grade: envoy
				// v1.37.6 → v1.40.2 said "critical 9" for one critical advisory,
				// as v1.40.0's Brotli entry carries forward twelve ids and eight
				// of them — high and medium — were fixed at or below v1.37.6
				// (three agent runs called the count unusable, 2026-11-20). On
				// the running branch nothing is left out, as for the change.
				counted := c.Advisories
				if !onRunningBranch && len(alreadyFixed) > 0 {
					counted = nil
					for _, raw := range c.Advisories {
						if !alreadyFixed[strings.ToUpper(strings.TrimSpace(raw))] {
							counted = append(counted, raw)
						}
					}
				}
				tally.Add(ix.Advisories(counted), c.Severity)
				if key != "" {
					branch := key + "|" + branchOf(d.Version)
					if i, ok := inRelease[key]; ok {
						// Another change of this same release citing the same
						// advisories. One entry stands for them unless this one
						// asks for something the entry does not: measured over
						// five projects on 2026-11-11, 19 of 53 such changes
						// carried their own also_required or workaround (envoy
						// v1.35.13: "set oauth2_use_gcm_encryption to true"), and
						// folding them dropped that instruction without a trace.
						if !carriesItsOwn(c, b.Changes[i]) {
							raiseSeverity(&b.Changes[i], c.Severity)
							// A sibling under an entry of its own release, or
							// under a backport named in another entry's also_in:
							// either way it is a change of its own and gets its
							// line. The backport's siblings were left out until
							// the review of 2026-11-20 — dapr v1.15.13 counted
							// 129 security changes and accounted for 128: v1.19.1
							// "internal gRPC proxy forwarding to other apps
							// blocked" cites the same GHSA as the token-bypass
							// fix whose also_in names v1.19.1, and was nowhere
							// (istio 1.27.5 lost 2, etcd v3.4.40 3).
							folded = append(folded, newBriefChange(d.Version, c, key))
							continue
						}
					} else {
						touched = append(touched, branch)
						if i, ok := firstByAdv[key]; ok && !advOnBranch[branch] {
							// The same fix landed on another branch inside the range:
							// one entry at the earliest version, the rest named. The
							// entry takes the higher grade, so a critical backport is
							// not hidden under an ungraded first entry.
							b.Changes[i].AlsoIn = append(b.Changes[i].AlsoIn, d.Version)
							raiseSeverity(&b.Changes[i], c.Severity)
							inRelease[key] = i
							continue
						}
						inRelease[key] = len(b.Changes)
					}
				}
			}
			bc := newBriefChange(d.Version, c, key)
			if _, ok := firstByAdv[key]; key != "" && !ok {
				firstByAdv[key] = len(b.Changes)
			}
			b.Changes = append(b.Changes, bc)
		}
		for _, t := range touched {
			advOnBranch[t] = true
		}
		if relSettledCritical > 0 {
			// A copy: d.Warnings belongs to the cached release document.
			w := maps.Clone(d.Warnings)
			w[criticalExcludedKey] = relSettledCritical
			(*b.Releases)[len(*b.Releases)-1].Warnings = w
			settledCritical += relSettledCritical
		}
	}
	if settledCritical > 0 {
		b.Summary.Warnings[criticalExcludedKey] = settledCritical
	}
	// Kind first (security leads); security by grade, so a cap cuts the least
	// severe; then, inside a grade and inside the kinds that have no grade,
	// entries that carry a step before those that do not, so a cap never cuts
	// what the operator has to do while it keeps something that asks nothing;
	// then version. Within one release the server's order (the screen order)
	// is kept by the stable sort.
	sort.SliceStable(b.Changes, func(i, j int) bool { return briefLess(b.Changes[i], b.Changes[j]) })
	kept := b.Changes[:0]
	shown := map[string]int{}
	var cut []Change
	for _, c := range b.Changes {
		if shown[c.Kind] >= kindCaps[c.Kind] {
			if b.ChangesOmitted == nil {
				b.ChangesOmitted = map[string]int{}
			}
			b.ChangesOmitted[c.Kind]++
			cut = append(cut, c)
			continue
		}
		shown[c.Kind]++
		kept = append(kept, c)
	}
	b.Changes = kept
	b.Summary.DistinctAdvisories = tally.Counts()

	// 1.2.0 — nothing above reads what follows.
	if len(folded) > 0 {
		if b.ChangesOmitted == nil {
			b.ChangesOmitted = map[string]int{}
		}
		b.ChangesOmitted["security"] += len(folded)
		b.folded = len(folded)
	}
	more := append(cut, folded...)
	sort.SliceStable(more, func(i, j int) bool { return briefLess(more[i], more[j]) })
	for _, c := range more {
		b.ChangesMore = append(b.ChangesMore, lineOf(c))
	}
	up := beforeUpgradeOf(docs)
	b.Upgrade = &up
	// later_in_range carries only problems a release in range has
	// (regression_fixed, known_issue_fixed): what it is for is "do not stop at
	// that version". An advisory_after_release on a release in range is an
	// advisory for a vulnerability that release already fixes, and on cilium
	// v1.16.18's range it was 25 of the 34 events (spec "마", 2026-11-20).
	// An event the server records twice on one release is listed once: envoy
	// v1.38.0 carried the same regression_fixed event, byte for byte, two
	// times (2026-11-20).
	later := []json.RawMessage{}
	seenLater := map[string]bool{}
	for _, d := range docs {
		for _, ev := range d.Later {
			var e struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(ev, &e) == nil && (e.Type == "regression_fixed" || e.Type == "known_issue_fixed") && !seenLater[string(ev)] {
				seenLater[string(ev)] = true
				later = append(later, ev)
			}
		}
	}
	b.LaterInRange = &later
	if settled > 0 {
		ex := &ExcludedOnBranch{Security: settled, Advisories: []excludedAdvisory{}}
		for id, g := range excluded {
			sev := json.RawMessage("null")
			if g > 0 {
				sev, _ = json.Marshal(gradeName(g))
			}
			ex.Advisories = append(ex.Advisories, excludedAdvisory{ID: excludedName[id], Severity: sev})
		}
		sort.Slice(ex.Advisories, func(i, j int) bool {
			gi, gj := gradeRank(ex.Advisories[i].Severity), gradeRank(ex.Advisories[j].Severity)
			if gi != gj {
				return gi > gj
			}
			return ex.Advisories[i].ID < ex.Advisories[j].ID
		})
		b.ExcludedOnBranch = ex
	}
	return b, settled
}

// briefLess is the order of a briefing's changes (see Build).
func briefLess(ci, cj Change) bool {
	if ki, kj := kindRank(ci.Kind), kindRank(cj.Kind); ki != kj {
		return ki < kj
	}
	// Grade before step: only security carries a grade, and there the cap
	// must cut the least severe. With step first, envoy v1.36.0 → v1.40.2
	// kept critical 2 · high 2 · medium 8 · ungraded 8 and cut 17 high
	// advisories that had no extra step (measured 2026-11-15).
	if gi, gj := gradeRank(ci.Severity), gradeRank(cj.Severity); gi != gj {
		return gi > gj
	}
	if si, sj := carriesAStep(ci), carriesAStep(cj); si != sj {
		return si
	}
	return releaseline.DescCmp(ci.Version, cj.Version) > 0
}

// gradeRank orders a briefing's raw severity: critical first, a change whose
// grade was not found (null) and non-security changes (absent) last.
func gradeRank(raw json.RawMessage) int {
	var g string
	_ = json.Unmarshal(raw, &g)
	return advisory.GradeRank(g)
}

// gradeName is the grade gradeRankOf ranks as g.
func gradeName(g int) string {
	switch g {
	case 4:
		return "critical"
	case 3:
		return "high"
	case 2:
		return "medium"
	case 1:
		return "low"
	}
	return ""
}

// narrow settles which changes a briefing lists, for the kinds asked for
// (1.2.0 kinds; none asked = all four decision kinds).
//
// changes_more is whole for security, breaking and deprecations, always: those
// are what an upgrade decision turns on, and a change that is not listed is one
// the reader does not know to ask for (the misses measured on 2026-11-20 were
// all changes a briefing did not carry, in runs that never opened the release
// they sat on). Important fixes past their cap stay a count in changes_omitted
// unless kinds names important_fixes — then every one of them is listed too.
//
// Nothing here looks at the size of the answer. An earlier build of 1.2.0 cut
// answers to 40,000 characters so that one client would not save them to a
// file, and on a five-component stack years behind it answered no entry with a
// body and none of kubernetes' 33 security changes (2026-11-20). The server
// does not guess what a client can hold: it lists what there is, and the
// caller narrows the call — kinds, one component, target_version, same_minor.
//
// summary, releases, before_upgrade and the rest describe the range and stay
// whole; only the change lists and their counts are narrowed.
func (b *Briefing) Narrow(k map[string]bool) {
	var lines []MoreLine
	for _, l := range b.ChangesMore {
		if l.Kind == "important_fixes" && !k["important_fixes"] {
			continue
		}
		if len(k) == 0 || k[l.Kind] {
			lines = append(lines, l)
		}
	}
	b.ChangesMore = lines
	if len(k) == 0 {
		return
	}
	var ch []Change
	for _, c := range b.Changes {
		if k[c.Kind] {
			ch = append(ch, c)
		}
	}
	b.Changes = ch
	for kind := range b.ChangesOmitted {
		if !k[kind] {
			delete(b.ChangesOmitted, kind)
		}
	}
	if len(b.ChangesOmitted) == 0 {
		b.ChangesOmitted = nil
	}
}
