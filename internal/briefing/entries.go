package briefing

import (
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

type Change struct {
	Version string `json:"version"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	// Severity is present on security changes only, and null there when no
	// grade was found in what was read — absent and null mean different things.
	Severity        json.RawMessage   `json:"severity,omitempty"`
	Advisories      []string          `json:"advisories,omitempty"`
	Affects         *release.Attr     `json:"affects,omitempty"`
	ActionRequired  string            `json:"action_required,omitempty"`
	AlsoRequired    string            `json:"also_required,omitempty"`
	NoDowngrade     *release.Attr     `json:"no_downgrade,omitempty"`
	RemovedIn       *release.Attr     `json:"removed_in,omitempty"`
	Workaround      *release.Attr     `json:"workaround,omitempty"`
	SecurityUnclear bool              `json:"security_unclear,omitempty"`
	SilentFailure   bool              `json:"silent_failure,omitempty"`
	RegressionSince *release.Attr     `json:"regression_since,omitempty"`
	AlsoIn          []string          `json:"also_in,omitempty"`
	Later           []json.RawMessage `json:"later,omitempty"`
	advKey          string
}

// newBriefChange is one change as a briefing entry: texts cut to
// briefTextChars, severity present on security only (null when no grade).
func newBriefChange(ver string, c release.Change, key string) Change {
	bc := Change{
		Version: ver, ID: c.ID, Kind: c.Kind, Title: c.Title, Body: briefText(strOf(c.Body)),
		Advisories: c.Advisories, Affects: attrOf(c.Affects),
		ActionRequired: strOf(c.ActionRequired), AlsoRequired: strOf(c.AlsoRequired),
		NoDowngrade: attrOf(c.NoDowngrade), RemovedIn: attrOf(c.RemovedIn),
		SecurityUnclear: c.SecurityUnclear, SilentFailure: c.SilentFailure,
		Later: c.Later, advKey: key,
	}
	if rs := attrOf(c.RegressionSince); rs != nil {
		rr := *rs
		rr.Text = briefText(rr.Text)
		bc.RegressionSince = &rr
	}
	if w := attrOf(c.Workaround); w != nil {
		ww := *w
		ww.Text = briefText(ww.Text)
		bc.Workaround = &ww
	}
	if c.Kind == "security" {
		bc.Severity = json.RawMessage("null")
		if c.Severity != nil && *c.Severity != "" {
			bc.Severity, _ = json.Marshal(*c.Severity)
		}
	}
	return bc
}

// MoreLine is one entry of changes_more (1.2.0): a change that did not fit in
// changes, in one line. Severity is present on security lines only, null there
// when no grade was found, as in changes. ActionRequired, AlsoRequired and
// NoDowngrade say the change carries that step (carriesAStep's reading: a
// no_downgrade stated or inferred); the text is one get_release(changes) away.
// RemovedIn is when a deprecation becomes a removal, stated or inferred. A
// line marked only action_required left the reader unable to tell which
// security and deprecation lines ask for something: istio 1.27.5 had five
// security lines with also_required, cilium v1.16.18 three deprecation lines
// with removed_in (independent review, 2026-11-20).
type MoreLine struct {
	Version        string          `json:"version"`
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Title          string          `json:"title"`
	Severity       json.RawMessage `json:"severity,omitempty"`
	Affects        *lineAttr       `json:"affects,omitempty"`
	ActionRequired bool            `json:"action_required,omitempty"`
	AlsoRequired   bool            `json:"also_required,omitempty"`
	NoDowngrade    bool            `json:"no_downgrade,omitempty"`
	RemovedIn      *lineAttr       `json:"removed_in,omitempty"`
	// AlsoIn is the entry's also_in, carried onto its line: without it, an
	// entry moved to a line took with it every backport it stood for —
	// kubernetes v1.32.11 to its newest lost 10 of 43 security changes
	// without a trace (independent review, 2026-11-20).
	AlsoIn []string `json:"also_in,omitempty"`
}

// lineAttr is an attribute in a one-line entry: its text and status, without
// the reason a full entry carries.
type lineAttr struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

func lineOf(c Change) MoreLine {
	said := func(a *release.Attr) bool { return a != nil && (a.Status == "stated" || a.Status == "inferred") }
	l := MoreLine{Version: c.Version, ID: c.ID, Kind: c.Kind, Title: c.Title, Severity: c.Severity, AlsoIn: c.AlsoIn,
		ActionRequired: c.ActionRequired != "", AlsoRequired: c.AlsoRequired != "", NoDowngrade: said(c.NoDowngrade)}
	if c.Affects != nil {
		l.Affects = &lineAttr{Text: c.Affects.Text, Status: c.Affects.Status}
	}
	if said(c.RemovedIn) {
		l.RemovedIn = &lineAttr{Text: c.RemovedIn.Text, Status: c.RemovedIn.Status}
	}
	return l
}

// ExcludedOnBranch (1.2.0) names the security changes a briefing left out
// because the running version's own branch already fixed their advisories:
// summary.warnings adds up every release in range with them, so containerd
// said warnings.critical 1 while distinct_advisories.critical was 0, and
// agents spent two calls finding out why (2026-11-20). Advisories lists each
// advisory id they cite once, with the highest grade a left-out change gave it.
type ExcludedOnBranch struct {
	Security   int                `json:"security"`
	Advisories []excludedAdvisory `json:"advisories"`
}

// criticalExcludedKey sits in summary.warnings and in a release's warnings,
// right after "critical" (map keys go out sorted): how many of the critical
// security changes counted there the briefing left out because the running
// branch already fixed them. Present only when there is one.
const criticalExcludedKey = "critical_excluded_on_branch"

type excludedAdvisory struct {
	ID       string          `json:"id"`
	Severity json.RawMessage `json:"severity"`
}

func strOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func attrOf(a *release.Attr) *release.Attr {
	if a == nil || (a.Text == "" && a.Status == "") {
		return nil
	}
	return a
}

// carriesItsOwn says a change asks the operator for something the entry it
// would fold into does not: its own required step, workaround, no-downgrade
// warning or regression note.
func carriesItsOwn(c release.Change, kept Change) bool {
	if s := strOf(c.AlsoRequired); s != "" && s != kept.AlsoRequired {
		return true
	}
	if w := attrOf(c.Workaround); w != nil && (kept.Workaround == nil || briefText(w.Text) != kept.Workaround.Text) {
		return true
	}
	if attrOf(c.NoDowngrade) != nil && kept.NoDowngrade == nil {
		return true
	}
	return attrOf(c.RegressionSince) != nil && kept.RegressionSince == nil
}

// carriesAStep says a briefing entry tells the operator what to do or plan:
// a required action, a step beyond upgrading, a stated or inferred
// no-downgrade, or a removal date. Such entries lead their kind so the per-kind
// cap cannot cut them. Before this, the cap ran over the screen order alone:
// kubernetes v1.35.10 → v1.36.6 has 78 breaking changes, and 3 of the 4 that
// carry action_required were cut — among them kubeadm's "ACTION REQUIRED …
// before upgrading to v1.36" — while 19 that ask nothing stayed (independent
// review, 2026-11-15). An attribute with status not_stated says nothing.
func carriesAStep(c Change) bool {
	if c.ActionRequired != "" || c.AlsoRequired != "" {
		return true
	}
	for _, a := range []*release.Attr{c.NoDowngrade, c.RemovedIn} {
		if a != nil && (a.Status == "stated" || a.Status == "inferred") {
			return true
		}
	}
	return false
}

// raiseSeverity lifts an entry's grade to sev when sev is the higher one.
func raiseSeverity(bc *Change, sev *string) {
	if sev == nil || *sev == "" {
		return
	}
	if raw, _ := json.Marshal(*sev); gradeRank(raw) > gradeRank(bc.Severity) {
		bc.Severity = raw
	}
}

// branchOf names a version's branch for grouping: its line and major.minor.
// A version with fewer than two numbers is its own branch.
func branchOf(v string) string {
	k := version.NormalizeVersion(v)
	if len(k) < 2 {
		return strings.TrimSpace(v)
	}
	return version.Line(v) + "/" + strconv.Itoa(k[0]) + "." + strconv.Itoa(k[1])
}

// advisoryKey is the identity of a security fix across branches: the sorted,
// upper-cased advisory ids. A change citing none has no identity to compare.
func advisoryKey(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	u := make([]string, len(ids))
	for i, id := range ids {
		u[i] = strings.ToUpper(strings.TrimSpace(id))
	}
	sort.Strings(u)
	return strings.Join(slices.Compact(u), ",")
}
