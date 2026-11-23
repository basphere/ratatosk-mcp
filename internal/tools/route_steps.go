package tools

// The upgrade steps of a briefed range: what check_stack's and check_upgrade's
// first answer is made of when detail is not given. One step per minor
// version, each naming every security, breaking and deprecation change it
// holds by title, with the call that answers that step whole (next) and the
// call that reads the evidence of its must-read lines (verify).
//
// Why steps (1.3.0): a briefing of a long range was too big to read. Asked
// about kubernetes v1.28.6 → v1.37.2, an agent got 82,757 characters from
// 1.2.0, which its client saved to a file, and it skipped the reading
// instructions that came 70,221 characters in (2026-11-23). The steps of a
// range are a fraction of its briefing: containerd v2.2.9 → the newest,
// 18,525 characters against 56,075 for the briefing with the same marks;
// Argo CD, CoreDNS and Prometheus in one check_stack, 41,705 against 141,196
// (measured 2026-11-23).
//
// Why every change is named by title, not counted: a step that gave a count
// for the changes without a mark was read as nothing to do by an agent that
// did not open it — containerd 2.4.0's enable_cdi removal was left out of 3
// reports of 3 (2026-11-23). Named by title, it was in 3 of 3.
//
// The steps are computed from the briefing already built for the range, so a
// step counts what the briefing lists: a change the briefing folded into an
// earlier entry (the same advisories on another branch) counts at that entry.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/advisory"
	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

// stepKinds are the kinds a step counts and names; important_fixes too when
// the call's kinds names it (upgradeSteps).
var stepKinds = []string{"security", "breaking", "deprecations"}

// upgradeStep is one step of a route.
type upgradeStep struct {
	Line     string `json:"line"`
	Releases string `json:"releases"`
	// Processing names the step's releases still being analyzed, oldest
	// first: their changes may not all be named yet. The briefing marks each
	// such release; a route had dropped the mark (review, 2026-11-23).
	Processing    []string       `json:"processing,omitempty"`
	MajorBoundary bool           `json:"major_boundary,omitempty"` // a major other than the step before (or the version it starts above)
	Counts        map[string]int `json:"counts"`
	// Advisories is left out when the step's changes cite none: an empty
	// object beside nine security changes read as "no security" (review,
	// 2026-11-23).
	Advisories map[string]string `json:"advisories,omitempty"`
	MustRead   []mustReadLine    `json:"must_read"`
	// Lines name every other change of the kinds the step counts by title
	// (see the file comment for why).
	Lines  []stepLine `json:"lines,omitempty"`
	Next   *toolCall  `json:"next,omitempty"`
	Verify any        `json:"verify,omitempty"`

	major    int
	minors   []string // the minors folded in, ascending
	versions []string // the releases, ascending
	grades   map[string]*advisoryGrade
}

// mustReadLine is a line a step carries in must_read: a before_upgrade item,
// or a change that asks something of the operator — the four marks 1.2.0
// gives a line for what a change asks (action_required, also_required,
// no_downgrade, removed_in).
type mustReadLine struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Why   []string `json:"why"`
	kind  string
}

// stepLine is a change a step names by title, with no details.
type stepLine struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Severity string `json:"severity,omitempty"`
}

// advisoryGrade is what the step's changes say of one advisory's grade, as
// advisory.Tally reads it: a change citing it alone first.
type advisoryGrade struct {
	spelled       string
	alone, shared int
}

// stepsIn is what the steps of one range are computed from.
type stepsIn struct {
	project string
	b       *briefing.Briefing
	docs    []*release.Doc  // the range's documents: the advisories a line cites
	kinds   map[string]bool // the call's kinds; nil is every kind
	// next is the call that answers the releases above from up to to; nil
	// gives the steps no next.
	next func(from, to string) *toolCall
	// from is the version the first step starts above, as the call passed it:
	// the first step's next starts there. fromRead is the same version as the
	// briefing read it, an image build suffix dropped (version_read_as); ""
	// when nothing was dropped.
	from, fromRead string
}

// stepEntry is one change of the briefing, as the steps read it.
type stepEntry struct {
	version, id, kind, title string
	severity                 json.RawMessage
	why                      []string
}

func saidAttr(a *release.Attr) bool {
	return a != nil && (a.Status == "stated" || a.Status == "inferred")
}

func stepEntries(b *briefing.Briefing) []stepEntry {
	var out []stepEntry
	for _, c := range b.Changes {
		e := stepEntry{version: c.Version, id: c.ID, kind: c.Kind, title: c.Title, severity: c.Severity}
		if c.ActionRequired != "" {
			e.why = append(e.why, "action_required")
		}
		if c.AlsoRequired != "" {
			e.why = append(e.why, "also_required")
		}
		if saidAttr(c.NoDowngrade) {
			e.why = append(e.why, "no_downgrade")
		}
		if saidAttr(c.RemovedIn) {
			e.why = append(e.why, "removed_in")
		}
		out = append(out, e)
	}
	for _, l := range b.ChangesMore {
		e := stepEntry{version: l.Version, id: l.ID, kind: l.Kind, title: l.Title, severity: l.Severity}
		if l.ActionRequired {
			e.why = append(e.why, "action_required")
		}
		if l.AlsoRequired {
			e.why = append(e.why, "also_required")
		}
		if l.NoDowngrade {
			e.why = append(e.why, "no_downgrade")
		}
		if l.RemovedIn != nil {
			e.why = append(e.why, "removed_in")
		}
		out = append(out, e)
	}
	// Kind first, as a briefing orders them; inside a kind, changes then
	// changes_more, each in the briefing's order.
	slices.SortStableFunc(out, func(a, b stepEntry) int {
		return slices.Index(release.Kinds, a.kind) - slices.Index(release.Kinds, b.kind)
	})
	return out
}

// minorOf is the minor a version is read in: "1.35", or "3" for a version
// with one number.
func minorOf(v string) (minor string, major int, key []int) {
	k := version.NormalizeVersion(v)
	switch len(k) {
	case 0:
		return strings.TrimSpace(v), -1, nil
	case 1:
		return fmt.Sprint(k[0]), k[0], k
	}
	return fmt.Sprintf("%d.%d", k[0], k[1]), k[0], k[:2]
}

// upgradeSteps computes the steps of a briefed range: one per minor, oldest
// first, neighbouring steps with nothing to read folded into one. It is nil
// when a change or a before_upgrade item of the briefing has no version: that
// one could be in no step, and the range is answered as its briefing instead.
func upgradeSteps(in stepsIn) []upgradeStep {
	covered := map[string]bool{}
	for _, k := range stepKinds {
		if in.kinds == nil || in.kinds[k] {
			covered[k] = true
		}
	}
	// Important fixes only when kinds names them: then the briefing lists every
	// one, and a route that asked for them answered none (review, 2026-11-23).
	// Without kinds a step holds the three kinds that were measured.
	if in.kinds["important_fixes"] {
		covered["important_fixes"] = true
	}
	byMinor := map[string]*upgradeStep{}
	keys := map[string][]int{}
	var order []string
	// A release without a version is in no step: it made a step named ""
	// (review, 2026-11-23). What was read from it is in no step either, and
	// a route would leave it out unsaid while totals counts it, so a change or
	// a before_upgrade item without a version gives no steps at all (review,
	// 2026-11-23). /v2 sends a version on every release; this is a server that
	// does not.
	noVersion := func(v string) bool { return strings.TrimSpace(v) == "" }
	stepOf := func(v string) *upgradeStep {
		m, major, key := minorOf(v)
		s, ok := byMinor[m]
		if !ok {
			s = &upgradeStep{Counts: map[string]int{}, Advisories: map[string]string{}, MustRead: []mustReadLine{},
				major: major, minors: []string{m}, grades: map[string]*advisoryGrade{}}
			for k := range covered {
				s.Counts[k] = 0
			}
			byMinor[m], keys[m] = s, key
			order = append(order, m)
		}
		return s
	}
	if in.b.Releases != nil {
		for _, r := range *in.b.Releases {
			if noVersion(r.Version) {
				continue
			}
			s := stepOf(r.Version)
			s.versions = append(s.versions, r.Version)
			if r.Processing {
				s.Processing = append(s.Processing, r.Version)
			}
		}
	}
	cited := map[string][]string{}
	for _, d := range in.docs {
		for _, c := range d.Changes {
			cited[c.ID] = advisory.RecognisedIDs(c.Advisories)
		}
	}
	if in.b.Upgrade != nil {
		for _, it := range *in.b.Upgrade {
			// An item that recurs is listed once, at the newest release, with
			// also_in naming the others; it is read in the step where the range
			// first meets it. At the newest it sat in a later step than the
			// first upgrade that asks for it (review, 2026-11-23). Its id is the
			// one the step's next answers with (itemIDInStep).
			v := earliestOf(it.Version, it.AlsoIn)
			if noVersion(v) {
				return nil
			}
			s := stepOf(v)
			s.MustRead = append(s.MustRead, mustReadLine{ID: itemIDInStep(it, in.docs, v), Title: it.Title, Why: []string{"before_upgrade"}})
		}
	}
	for _, e := range stepEntries(in.b) {
		if noVersion(e.version) {
			return nil
		}
		s := stepOf(e.version)
		if covered[e.kind] {
			s.Counts[e.kind]++
		}
		ids := cited[e.id]
		g := 0
		var sev string
		if json.Unmarshal(e.severity, &sev) == nil {
			g = advisory.GradeRank(sev)
		}
		for _, id := range ids {
			key := strings.ToUpper(id)
			lg := s.grades[key]
			if lg == nil {
				lg = &advisoryGrade{spelled: id}
				s.grades[key] = lg
			}
			if len(ids) == 1 {
				lg.alone = max(lg.alone, g)
			} else {
				lg.shared = max(lg.shared, g)
			}
		}
		if len(e.why) > 0 {
			s.MustRead = append(s.MustRead, mustReadLine{ID: e.id, Title: e.title, Why: e.why, kind: e.kind})
		} else if covered[e.kind] {
			l := stepLine{ID: e.id, Kind: e.kind, Title: e.title}
			json.Unmarshal(e.severity, &l.Severity)
			s.Lines = append(s.Lines, l)
		}
	}
	slices.SortStableFunc(order, func(a, b string) int { return version.Compare(keys[a], keys[b]) })

	var steps []upgradeStep
	for _, m := range order {
		s := byMinor[m]
		slices.SortFunc(s.versions, byVersion)
		slices.SortFunc(s.Processing, byVersion)
		steps = append(steps, *s)
	}
	steps = foldSteps(steps)

	prev := in.from
	// The first step is compared with the version it starts above: Prometheus
	// v2.51.2 → v3.16.0 crosses its major there, and so does OPA v0.34.2 →
	// v1.22.4 (2026-11-23). It is compared as read: with its build suffix,
	// envoy v1.36.5-<build> made a boundary of its first step, 1.36 (review,
	// 2026-11-23).
	read := in.fromRead
	if read == "" {
		read = in.from
	}
	_, prevMajor, _ := minorOf(read)
	for i := range steps {
		s := &steps[i]
		if prevMajor >= 0 && s.major >= 0 && s.major != prevMajor {
			s.MajorBoundary = true
		}
		prevMajor = s.major
		s.Line = s.minors[0]
		if len(s.minors) > 1 {
			s.Line += "–" + s.minors[len(s.minors)-1]
		}
		switch n := len(s.versions); n {
		case 0:
			s.Releases = "(0)"
		case 1:
			s.Releases = fmt.Sprintf("%s (1)", s.versions[0])
		default:
			s.Releases = fmt.Sprintf("%s..%s (%d)", s.versions[0], s.versions[n-1], n)
		}
		for _, g := range s.grades {
			grade := g.alone
			if grade == 0 {
				grade = g.shared
			}
			s.Advisories[g.spelled] = stepGradeName(grade)
		}
		last := lastVersion(s)
		if last != "" && in.next != nil {
			s.Next = in.next(prev, last)
		}
		if last != "" {
			prev = last
		}
		s.Verify = verifyCalls(in.project, s.MustRead)
	}
	return steps
}

// earliestOf is the oldest of v and others; a version that does not read as
// one is passed over.
func earliestOf(v string, others []string) string {
	best, key := v, version.NormalizeVersion(v)
	for _, o := range others {
		if k := version.NormalizeVersion(o); len(k) > 0 && (len(key) == 0 || version.Compare(k, key) < 0) {
			best, key = o, k
		}
	}
	return best
}

// itemIDInStep is the id a before_upgrade item has in the step of release v:
// on the last of docs, in their order, that is of v's minor and has the item
// — the same section and the same fingerprint after "#", as the briefing
// groups the items that recur. The step's next briefs those releases, and a
// briefing lists a recurring item at the last one, so the two ids are the
// same. It is read from a document, never composed; when none has the item the
// briefing's id stays.
func itemIDInStep(it briefing.UpgradeItem, docs []*release.Doc, v string) string {
	_, fp, ok := strings.Cut(it.ID, "#")
	if !ok || fp == "" {
		return it.ID
	}
	minor, _, _ := minorOf(v)
	id := it.ID
	for _, d := range docs {
		if m, _, _ := minorOf(d.Version); m != minor {
			continue
		}
		var items []release.Item
		switch it.Section {
		case "prerequisites":
			items = d.BeforeUpgrade.Prerequisites
		case "known_issues":
			items = d.BeforeUpgrade.KnownIssues
		case "upgrade_guide":
			items = d.BeforeUpgrade.UpgradeGuide
		}
		for _, x := range items {
			if _, f, ok := strings.Cut(x.ID, "#"); ok && f == fp {
				id = x.ID
			}
		}
	}
	return id
}

func byVersion(a, b string) int {
	return version.Compare(version.NormalizeVersion(a), version.NormalizeVersion(b))
}

func lastVersion(s *upgradeStep) string {
	if len(s.versions) == 0 {
		return ""
	}
	return s.versions[len(s.versions)-1]
}

func stepGradeName(g int) string {
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
	return "ungraded"
}

// foldSteps folds neighbouring steps that carry no must_read line into one. A
// step on another major is not folded into the one before it: the boundary
// stays a step of its own.
func foldSteps(steps []upgradeStep) []upgradeStep {
	var out []upgradeStep
	for _, s := range steps {
		if n := len(out); n > 0 {
			last := &out[n-1]
			if len(last.MustRead) == 0 && len(s.MustRead) == 0 && last.major == s.major {
				last.minors = append(last.minors, s.minors...)
				last.versions = append(last.versions, s.versions...)
				last.Processing = append(last.Processing, s.Processing...)
				last.Lines = append(last.Lines, s.Lines...)
				for k, v := range s.Counts {
					last.Counts[k] += v
				}
				for k, g := range s.grades {
					if lg, ok := last.grades[k]; ok {
						lg.alone, lg.shared = max(lg.alone, g.alone), max(lg.shared, g.shared)
					} else {
						last.grades[k] = g
					}
				}
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

// rangeTotals counts the range's distinct changes per kind the call covers:
// the entries in changes and the ones changes_omitted counts — for security,
// breaking and deprecations those are the lines of changes_more.
func rangeTotals(b *briefing.Briefing, kinds map[string]bool) map[string]int {
	out := map[string]int{}
	for _, k := range briefing.DecisionKinds {
		if kinds == nil || kinds[k] {
			out[k] = b.ChangesOmitted[k]
		}
	}
	for _, c := range b.Changes {
		if _, ok := out[c.Kind]; ok {
			out[c.Kind]++
		}
	}
	return out
}
