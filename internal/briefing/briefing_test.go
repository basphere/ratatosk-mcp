package briefing

// The briefing on hand-made ranges: what it lists, in which order, under which
// caps, and what it folds into an entry. Most cases replay a measured answer or
// a reviewed failure; the anchor sits on the case. The same rules are also run
// on recorded live ranges, against the frozen 1.0.0 code.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// Carried over from 0.x: the cut briefTextChars measures is visible and keeps a
// prefix.
func TestBriefTextBoundsTheTailVisibly(t *testing.T) {
	short := "cilium-agent now refuses an empty policy"
	if got := briefText(short); got != short {
		t.Fatalf("a typical text was altered: %q", got)
	}
	long := strings.Repeat("가나다", 200) // 600 runes, multi-byte on purpose
	got := briefText(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatal("truncation is invisible")
	}
	if r := []rune(got); len(r) != briefTextChars+1 {
		t.Fatalf("cut %d runes, want %d + ellipsis", len(r)-1, briefTextChars)
	}
	if !strings.HasPrefix(long, strings.TrimSuffix(got, "…")) {
		t.Fatal("the kept part is not a prefix of the original")
	}
}

// The envoy range on the preview API (2026-11-03) had 55 security changes ahead
// of every breaking one; a single cap would have hidden all of them. Caps are
// per kind, security keeps its most severe, and every cut is counted.
func TestBriefingCapsPerKind(t *testing.T) {
	d := &release.Doc{Version: "v1.38.0"}
	grades := []string{"low", "critical", "medium", "high"}
	for i := 0; i < 30; i++ {
		g := grades[i%4]
		d.Changes = append(d.Changes, release.Change{ID: fmt.Sprint("s", i), Kind: "security", Severity: &g, Advisories: []string{fmt.Sprint("CVE-2026-", 1000+i)}})
	}
	for i := 0; i < 3; i++ {
		d.Changes = append(d.Changes, release.Change{ID: fmt.Sprint("b", i), Kind: "breaking"})
	}
	b, _ := Build([]*release.Doc{d}, nil, nil)
	var sec, brk int
	for _, c := range b.Changes {
		switch c.Kind {
		case "security":
			sec++
			if gradeRank(c.Severity) == 1 {
				t.Errorf("a low-grade change was kept while others were cut: %s", c.ID)
			}
		case "breaking":
			brk++
		}
	}
	if sec != kindCaps["security"] || brk != 3 {
		t.Fatalf("kept security %d breaking %d, want %d and 3", sec, brk, kindCaps["security"])
	}
	if b.ChangesOmitted["security"] != 30-kindCaps["security"] || b.ChangesOmitted["breaking"] != 0 {
		t.Fatalf("omitted %v", b.ChangesOmitted)
	}
	if gradeRank(b.Changes[0].Severity) != 4 {
		t.Fatalf("security does not lead with critical: %s", b.Changes[0].Severity)
	}
}

// Replay of kubernetes v1.35.10 → v1.36.6 (independent review, 2026-11-15): 78
// breaking changes, the four with action_required sat late in screen order, and
// the cap of 20 kept one of them. Entries that say what to do lead their kind;
// inside each group the old order holds; the omitted count means what it meant.
func TestBriefingKeepsEntriesThatCarryAStepBeforeTheCap(t *testing.T) {
	d := &release.Doc{Version: "v1.36.0"}
	ar := "migrate the flex-volume mount before upgrading"
	stated := &release.Attr{Text: "1.37", Status: "stated"}
	notStated := &release.Attr{Text: "", Status: "not_stated"}
	for i := 0; i < 30; i++ {
		c := release.Change{ID: fmt.Sprint("b", i), Kind: "breaking", NoDowngrade: notStated}
		switch i {
		case 25, 27, 29:
			c.ActionRequired = &ar
		case 28:
			c.NoDowngrade = &release.Attr{Text: "", Status: "inferred"}
		}
		d.Changes = append(d.Changes, c)
	}
	for i := 0; i < 12; i++ {
		c := release.Change{ID: fmt.Sprint("d", i), Kind: "deprecations", RemovedIn: notStated}
		if i == 11 {
			c.RemovedIn = stated
		}
		d.Changes = append(d.Changes, c)
	}
	// Security: grade first — a low-grade change with a step beyond upgrading
	// does not jump a critical one — and a step leads only inside its grade.
	low, crit, high := "low", "critical", "high"
	also := "rotate the tokens"
	d.Changes = append(d.Changes,
		release.Change{ID: "s-crit", Kind: "security", Severity: &crit, Advisories: []string{"CVE-2026-1"}},
		release.Change{ID: "s-low-step", Kind: "security", Severity: &low, Advisories: []string{"CVE-2026-2"}, AlsoRequired: &also},
		release.Change{ID: "s-high", Kind: "security", Severity: &high, Advisories: []string{"CVE-2026-3"}},
		release.Change{ID: "s-high-step", Kind: "security", Severity: &high, Advisories: []string{"CVE-2026-4"}, AlsoRequired: &also},
	)
	b, _ := Build([]*release.Doc{d}, nil, nil)

	var brk, dep, sec []string
	for _, c := range b.Changes {
		switch c.Kind {
		case "breaking":
			brk = append(brk, c.ID)
		case "deprecations":
			dep = append(dep, c.ID)
		case "security":
			sec = append(sec, c.ID)
		}
	}
	if len(brk) != kindCaps["breaking"] {
		t.Fatalf("kept %d breaking, want the cap %d", len(brk), kindCaps["breaking"])
	}
	if want := []string{"b25", "b27", "b28", "b29", "b0", "b1"}; !slices.Equal(brk[:6], want) {
		t.Errorf("breaking order %v, want the four that carry a step first, in screen order, then the rest: %v…", brk, want)
	}
	if b.ChangesOmitted["breaking"] != 30-kindCaps["breaking"] {
		t.Errorf("omitted breaking %d, want %d", b.ChangesOmitted["breaking"], 30-kindCaps["breaking"])
	}
	if len(dep) != kindCaps["deprecations"] || dep[0] != "d11" || b.ChangesOmitted["deprecations"] != 2 {
		t.Errorf("deprecations %v omitted %d: the one with a stated removed_in must lead and stay", dep, b.ChangesOmitted["deprecations"])
	}
	if want := []string{"s-crit", "s-high-step", "s-high", "s-low-step"}; !slices.Equal(sec, want) {
		t.Errorf("security order %v, want %v", sec, want)
	}
}

// Inside the range: the first release of another branch carrying an advisory is
// the backport and folds into also_in; a later release of a branch that already
// carried it is a follow-up and is listed with its own severity.
// (Independent review, 2026-11-11.)
func TestBriefingListsAFollowUpFixInsideTheRange(t *testing.T) {
	sec := func(ver, grade string) string {
		g := "null"
		if grade != "" {
			g = `"` + grade + `"`
		}
		return upstreamtest.Doc(ver, "", "", `[{"id":"etcd@`+ver+`#a","kind":"security","title":"fix","body":null,"severity":`+g+`,"advisories":["GHSA-1111"],`+
			`"affects":{"text":"","status":"not_stated"},"also_required":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}]`, "")
	}
	var docs []*release.Doc
	for _, d := range []string{sec("v1.2.1", ""), sec("v1.2.2", "critical"), sec("v1.3.0", ""), sec("v1.3.1", "high")} {
		r, err := release.Parse([]byte(d))
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, r)
	}
	b, settled := Build(docs, nil, []int{1, 2, 0})
	got := map[string][]string{}
	for _, c := range b.Changes {
		got[c.ID] = c.AlsoIn
	}
	if settled != 0 || len(got) != 3 {
		t.Fatalf("want three entries (first fix, its follow-up, the other branch's follow-up), got %v settled %d", got, settled)
	}
	if also := got["etcd@v1.2.1#a"]; len(also) != 1 || also[0] != "v1.3.0" {
		t.Errorf("the other branch's first release folds into the first fix: also_in %v", also)
	}
	for _, id := range []string{"etcd@v1.2.2#a", "etcd@v1.3.1#a"} {
		if _, ok := got[id]; !ok {
			t.Errorf("follow-up %s must be listed on its own: %v", id, got)
		}
	}
	// The critical follow-up leads: security is ordered by grade.
	if b.Changes[0].ID != "etcd@v1.2.2#a" {
		t.Errorf("the critical follow-up must come first, got %s", b.Changes[0].ID)
	}
}

// Two changes of ONE release citing the same advisories are one entry, on any
// branch: they are not a fix and its follow-up. A backport's higher grade is
// carried onto the entry it folds into.
// (Independent review, 2026-11-11.)
func TestBriefingTreatsSiblingsInOneReleaseAsOne(t *testing.T) {
	ch := func(ver, id, grade string) string {
		g := "null"
		if grade != "" {
			g = `"` + grade + `"`
		}
		return `{"id":"etcd@` + ver + `#` + id + `","kind":"security","title":"fix","body":null,"severity":` + g + `,"advisories":["GHSA-2222"],` +
			`"affects":{"text":"","status":"not_stated"},"also_required":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}`
	}
	var docs []*release.Doc
	for _, d := range []string{
		upstreamtest.Doc("v1.2.1", "", "", `[`+ch("v1.2.1", "a", "")+`,`+ch("v1.2.1", "b", "")+`]`, ""),
		upstreamtest.Doc("v1.3.0", "", "", `[`+ch("v1.3.0", "a", "critical")+`,`+ch("v1.3.0", "b", "critical")+`]`, ""),
	} {
		r, err := release.Parse([]byte(d))
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, r)
	}
	b, _ := Build(docs, nil, []int{1, 2, 0})
	if len(b.Changes) != 1 {
		t.Fatalf("four changes, one advisory, two releases on two branches: want one entry, got %d", len(b.Changes))
	}
	c := b.Changes[0]
	if c.ID != "etcd@v1.2.1#a" || len(c.AlsoIn) != 1 || c.AlsoIn[0] != "v1.3.0" {
		t.Errorf("entry %s also_in %v, want the first change with the other branch named once", c.ID, c.AlsoIn)
	}
	if string(c.Severity) != `"critical"` {
		t.Errorf("the backport's grade must be carried onto the entry: %s", c.Severity)
	}
}

// A sibling that asks for something of its own is not folded away: it is the
// envoy v1.35.13 case, where the second change on one advisory carried the
// required step.
// (Independent review, 2026-11-11.)
func TestBriefingKeepsASiblingThatCarriesItsOwnStep(t *testing.T) {
	plain := `{"id":"etcd@v1.2.1#a","kind":"security","title":"padding oracle fixed","body":null,"severity":"high","advisories":["GHSA-3333"],` +
		`"affects":{"text":"","status":"not_stated"},"also_required":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}`
	withStep := `{"id":"etcd@v1.2.1#b","kind":"security","title":"opt-in GCM encryption","body":null,"severity":"high","advisories":["GHSA-3333"],` +
		`"affects":{"text":"","status":"not_stated"},"also_required":"set use_gcm_encryption to true","security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}`
	same := strings.Replace(plain, "#a", "#c", 1)
	r, err := release.Parse([]byte(upstreamtest.Doc("v1.2.1", "", "", `[`+plain+`,`+withStep+`,`+same+`]`, "")))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Build([]*release.Doc{r}, nil, []int{1, 2, 0})
	var ids []string
	for _, c := range b.Changes {
		ids = append(ids, c.ID)
	}
	// The one with its own step leads its kind (carriesAStep, 2026-11-15).
	if len(ids) != 2 || ids[0] != "etcd@v1.2.1#b" || ids[1] != "etcd@v1.2.1#a" {
		t.Fatalf("want the one with its own step and the first change, not the plain repeat: %v", ids)
	}
	if b.Changes[0].AlsoRequired != "set use_gcm_encryption to true" {
		t.Errorf("the step must reach the answer: %+v", b.Changes[0])
	}
}

func ptr(s string) *string { return &s }

// containerd v2.2.9 → v2.4.2 carried 17 before-upgrade items for 8 texts, and
// cilium v1.20.0's prerequisite had no body and its warning (Gateway API v1.6.1
// before upgrading, or TLSRoute objects are lost) only in folded.
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestBeforeUpgradeCarriesBodiesFoldedAndGroupsRecurringTexts(t *testing.T) {
	item := func(id, title string, body *string, folded string) release.Item {
		it := release.Item{ID: id, Title: title, Body: body}
		if folded != "" {
			it.Folded = []json.RawMessage{json.RawMessage(folded)}
		}
		return it
	}
	rel := func(v string, pre ...release.Item) *release.Doc {
		d := &release.Doc{Version: v}
		d.BeforeUpgrade.Prerequisites = pre
		return d
	}
	docs := []*release.Doc{
		rel("v2.3.1", item("c@v2.3.1#4f6a2055", "runc and CNI (2.3.1 wording)", ptr("install them separately"), "")),
		rel("v2.3.3", item("c@v2.3.3#4f6a2055", "runc and CNI (2.3.3 wording)", nil, ""), item("c@v2.3.3#6ddd209f", "glibc archive", nil, "")),
		rel("v2.4.0", item("c@v2.4.0#c62eff94", "legacy features", nil, `{"title":"Gateway API v1.6.1","body":"upgrade Gateway API to v1.6.1 before upgrading, or TLSRoute objects are lost"}`)),
		rel("v2.4.2", item("c@v2.4.2#4f6a2055", "runc and CNI (2.4.2 wording)", ptr("the newest wording"), "")),
	}
	got := beforeUpgradeOf(docs)
	var ids []string
	for _, it := range got {
		ids = append(ids, it.ID)
	}
	if want := []string{"c@v2.3.3#6ddd209f", "c@v2.4.0#c62eff94", "c@v2.4.2#4f6a2055"}; !slices.Equal(ids, want) {
		t.Fatalf("items %v, want %v (a recurring text once, at its newest release)", ids, want)
	}
	if g := got[2]; !slices.Equal(g.AlsoIn, []string{"v2.3.1", "v2.3.3"}) || g.Title != "runc and CNI (2.4.2 wording)" || *g.Body != "the newest wording" {
		t.Errorf("grouped item %+v", g)
	}
	if got[0].AlsoIn != nil {
		t.Errorf("a text on one release names no other: %v", got[0].AlsoIn)
	}
	b, _ := json.Marshal(got[1])
	for _, want := range []string{`"body":null`, `"folded":[{"title":"Gateway API v1.6.1","body":"upgrade Gateway API to v1.6.1 before upgrading, or TLSRoute objects are lost"}]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("no %s in %s", want, b)
		}
	}
	// A long body goes out whole: the instruction sits at its end.
	long := strings.Repeat("가", 300) + " — upgrade Gateway API first"
	if got := beforeUpgradeOf([]*release.Doc{rel("v1", item("x@v1#a", "t", &long, ""))}); *got[0].Body != long {
		t.Error("a before-upgrade body was cut")
	}
	if got := beforeUpgradeOf(nil); got == nil || len(got) != 0 {
		t.Errorf("no items is [], not null: %#v", got)
	}
}

// Every change the caps cut is listed in one line; a sibling folded into an
// entry of its own release is listed too (coredns v1.14.7 → v1.15.0: eight
// security changes, five listed, nothing omitted), and so is a backport's
// sibling.
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestBriefingListsWhatDidNotFitInOneLine(t *testing.T) {
	sec := func(id string, title string, advs ...string) release.Change {
		return release.Change{ID: id, Kind: "security", Title: title, Advisories: advs, Affects: &release.Attr{Text: "x", Status: "stated", Reason: "r"}}
	}
	go1273 := []string{"CVE-2026-111943", "CVE-2026-33818"}
	coredns := &release.Doc{Version: "v1.15.0", Changes: []release.Change{
		sec("c#asn1", "ASN.1 depth", append(slices.Clone(go1273), "GO-2026-5972")...),
		sec("c#tls", "TLS policies"),
		sec("c#h2", "HTTP/2 crash", go1273...),
		sec("c#trailer", "trailer memory", go1273...),
		sec("c#x509", "x509 intermediates", go1273...),
	}}
	ar := "migrate"
	for i := 0; i < 25; i++ {
		c := release.Change{ID: fmt.Sprint("c#b", i), Kind: "breaking", Title: "b"}
		if i == 24 {
			c.ActionRequired = &ar
		}
		coredns.Changes = append(coredns.Changes, c)
	}
	backport := &release.Doc{Version: "v1.15.1", Changes: []release.Change{sec("c#h2-bp", "HTTP/2 crash (1.15)", go1273...), sec("c#trailer-bp", "trailer (1.15)", go1273...)}}
	// v1.15.1 is the same branch as v1.15.0; make it another branch.
	backport.Version = "v1.16.0"

	b, settled := Build([]*release.Doc{coredns, backport}, nil, nil)
	if settled != 0 {
		t.Fatalf("settled %d", settled)
	}
	var entries, lines []string
	for _, c := range b.Changes {
		entries = append(entries, c.ID)
	}
	for _, l := range b.ChangesMore {
		lines = append(lines, l.ID)
	}
	for _, id := range []string{"c#trailer", "c#x509"} {
		if !slices.Contains(lines, id) {
			t.Errorf("the folded sibling %s is not in changes_more %v", id, lines)
		}
	}
	// The backport stands under also_in. Its sibling — another change of the
	// backport release citing the same advisories — is a change of its own and
	// has its line: left out, dapr v1.15.13 accounted for 128 of 129 security
	// changes (independent review, 2026-11-20).
	if slices.Contains(lines, "c#h2-bp") || slices.Contains(entries, "c#h2-bp") {
		t.Error("the backport stands under also_in, not in a list: c#h2-bp")
	}
	if !slices.Contains(lines, "c#trailer-bp") {
		t.Errorf("the backport's sibling c#trailer-bp is not in changes_more %v", lines)
	}
	// Every security change is accounted for: an entry, a line counted in
	// changes_omitted, or a version in an also_in.
	nSec, also := 0, 0
	for _, c := range b.Changes {
		if c.Kind == "security" {
			nSec++
			also += len(c.AlsoIn)
		}
	}
	for _, l := range b.ChangesMore {
		if l.Kind == "security" {
			also += len(l.AlsoIn)
		}
	}
	if got := nSec + b.ChangesOmitted["security"] + also; got != 7 {
		t.Errorf("security changes accounted for: %d entries + %d omitted + %d in also_in = %d, want all 7", nSec, b.ChangesOmitted["security"], also, got)
	}
	if b.ChangesOmitted["breaking"] != 5 {
		t.Fatalf("omitted %v", b.ChangesOmitted)
	}
	nb := 0
	for _, l := range b.ChangesMore {
		if l.Kind == "breaking" {
			nb++
			if l.Severity != nil || l.ActionRequired {
				t.Errorf("a breaking line without action_required: %+v", l)
			}
		}
		if l.Kind == "security" {
			if string(l.Severity) != "null" {
				t.Errorf("a security line says severity null when no grade was found: %s", l.Severity)
			}
			if l.Affects == nil || *l.Affects != (lineAttr{Text: "x", Status: "stated"}) {
				t.Errorf("line %s affects %+v", l.ID, l.Affects)
			}
		} else if l.Affects != nil {
			t.Errorf("a change without affects has none in its line: %+v", l)
		}
	}
	if nb != 5 {
		t.Errorf("%d breaking lines for 5 omitted", nb)
	}
	if !slices.Contains(entries, "c#b24") {
		t.Error("the breaking change with action_required must lead and stay in changes")
	}
	// A line marks what its change asks for, whatever the kind.
	req, nd := "re-register the cluster", &release.Attr{Text: "x", Status: "stated", Reason: "r"}
	marked := lineOf(Change{Kind: "security", AlsoRequired: req, NoDowngrade: nd, RemovedIn: &release.Attr{Text: "v1.21", Status: "inferred", Reason: "r"}})
	if !marked.AlsoRequired || !marked.NoDowngrade || marked.ActionRequired || marked.RemovedIn == nil || *marked.RemovedIn != (lineAttr{Text: "v1.21", Status: "inferred"}) {
		t.Errorf("line marks: %+v", marked)
	}
	if plain := lineOf(Change{Kind: "deprecations", NoDowngrade: &release.Attr{Status: "not_stated"}, RemovedIn: &release.Attr{Status: "not_stated"}}); plain.NoDowngrade || plain.RemovedIn != nil {
		t.Errorf("a not_stated no_downgrade or removed_in is no mark: %+v", plain)
	}
	j, _ := json.Marshal(b.ChangesMore[0])
	if strings.Contains(string(j), "reason") || strings.Contains(string(j), "body") {
		t.Errorf("a line is one line: %s", j)
	}
}

// later_in_range lists an event recorded twice on a release once.
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestLaterInRangeListsARepeatedEventOnce(t *testing.T) {
	ev := json.RawMessage(`{"seq":1,"type":"regression_fixed","version":"v1.38.0","fixed_in":"v1.38.7","title":"t"}`)
	other := json.RawMessage(`{"seq":2,"type":"regression_fixed","version":"v1.38.0","fixed_in":"v1.39.4","title":"t"}`)
	b, _ := Build([]*release.Doc{{Version: "v1.38.0", Later: []json.RawMessage{ev, ev, other}}}, nil, nil)
	if n := len(*b.LaterInRange); n != 2 {
		t.Errorf("later_in_range has %d events, want 2", n)
	}
}

// excluded_on_branch names every advisory left out, the most severe first;
// security counts every change left out. (A cap of 20 with a count of the
// rest stood here while answers were cut to a size; envoy v1.37.6 has 63.)
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestExcludedOnBranchNamesEveryAdvisory(t *testing.T) {
	high := "high"
	var past, cur []release.Change
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("CVE-2026-%d", 100+i)
		c := release.Change{ID: fmt.Sprint("s", i), Kind: "security", Advisories: []string{id}}
		if i >= 25 {
			c.Severity = &high
		}
		cur = append(cur, c)
		past = append(past, c)
	}
	fixed := map[string]bool{}
	for _, c := range past {
		fixed[c.Advisories[0]] = true
	}
	b, settled := Build([]*release.Doc{{Version: "v2.0.0", Changes: cur}}, fixed, []int{1, 0, 0})
	ex := b.ExcludedOnBranch
	if settled != 30 || ex == nil || ex.Security != 30 || len(ex.Advisories) != 30 {
		t.Fatalf("excluded %+v", ex)
	}
	if string(ex.Advisories[0].Severity) != `"high"` || string(ex.Advisories[4].Severity) != `"high"` || string(ex.Advisories[5].Severity) != "null" {
		t.Errorf("the most severe are kept: %+v", ex.Advisories[:6])
	}
}
