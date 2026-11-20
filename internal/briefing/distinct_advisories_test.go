package briefing

// summary.distinct_advisories (1.1.0). The Argo CD fixture is the 2026-11-18
// field report's range cut down to its security changes: the ids and grades
// are the live ones of v3.2.11, v3.3.9, v3.3.15, v3.4.10, v3.5.4 and v3.6.0
// (read from /v2 the same day), including the v3.3.15 card that cites eight
// GHSAs next to a card for each of them. Every case also checks that the rest
// of the briefing is the 1.0.0 one (assertSameAs100, ranges_test.go): the
// count is the only thing 1.1.0 adds to a briefing.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/basphere/ratatosk-mcp/internal/advisory"
	"github.com/basphere/ratatosk-mcp/internal/release"
)

type secSpec struct {
	id    string
	grade string // "" = null
	advs  []string
}

// relDoc builds a release whose counts and warnings are what the website
// would show for these security changes: security = how many, critical = how
// many critical.
func relDoc(t *testing.T, ver string, changes ...secSpec) *release.Doc {
	t.Helper()
	var cs []map[string]any
	critical := 0
	for _, s := range changes {
		var sev any
		if s.grade != "" {
			sev = s.grade
		}
		if s.grade == "critical" {
			critical++
		}
		advs := s.advs
		if advs == nil {
			advs = []string{}
		}
		cs = append(cs, map[string]any{
			"id": "argo@" + ver + "#" + s.id, "kind": "security", "title": "fix " + s.id, "body": nil,
			"severity": sev, "advisories": advs, "affects": map[string]any{"text": "", "status": "not_stated"},
			"also_required": nil, "security_unclear": false, "silent_failure": false, "workaround": nil,
			"regression_since": nil, "evidence": []any{}, "later": []any{},
		})
	}
	b, err := json.Marshal(map[string]any{
		"project": "argo", "version": ver, "released_at": "2026-10-06T00:00:00Z", "url": "https://x/" + ver,
		"counts":         map[string]int{"security": len(changes), "breaking": 0, "deprecations": 0, "important_fixes": 0, "enhancements": 0, "dependency_updates": 0, "other": 0},
		"warnings":       map[string]int{"critical": critical, "no_downgrade": 0},
		"before_upgrade": map[string]any{"prerequisites": []any{}, "known_issues": []any{}, "upgrade_guide": []any{}},
		"changes":        cs, "later": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := release.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const (
	g3v3m = "GHSA-3v3m-wc6v-x4x3"
	g9v9p = "GHSA-9v9p-x54c-58gc"
	gfw5c = "GHSA-fw5c-w8rc-j7fx"
	gm3vr = "GHSA-m3vr-7329-44ww"
	gfmxq = "GHSA-fmxq-cgp8-87wp"
	g4439 = "GHSA-4439-h7jw-5cjj"
	gj6cw = "GHSA-j6cw-g6p4-7hch"
	gg3ff = "GHSA-g3ff-q88g-chrj"
	gw996 = "GHSA-w996-f2wq-x9c6"
	g52cp = "GHSA-52cp-r559-cp3m"
)

// argoRange is the range in version order, as check_stack and /v2/upgrade
// hand it to Build.
func argoRange(t *testing.T) []*release.Doc {
	coordinated := func(fw5cCVE ...string) []secSpec {
		return []secSpec{
			{"9v9p", "critical", []string{g9v9p}},
			{"m3vr", "critical", []string{gm3vr}},
			{"fmxq", "critical", []string{"CVE-2026-77459", gfmxq}},
			{"fw5c", "critical", append(fw5cCVE, gfw5c)},
			{"4439", "high", []string{"CVE-2025-61560", g4439}},
			{"j6cw", "high", []string{"CVE-2026-55797", gj6cw}},
			{"g3ff", "medium", []string{gg3ff}},
			{"w996", "medium", []string{gw996}},
			{"logs", "", nil},
		}
	}
	v3315 := []secSpec{
		{"9v9p", "critical", []string{g9v9p}},
		{"fw5c", "critical", []string{gfw5c}},
		{"m3vr", "critical", []string{"CVE-2026-113672", gm3vr}},
		{"fmxq", "critical", []string{"CVE-2026-77459", gfmxq}},
		{"eight", "critical", []string{g4439, g9v9p, gfmxq, gfw5c, gg3ff, gj6cw, gm3vr, gw996}},
		{"4439", "high", []string{"CVE-2025-61560", g4439}},
		{"j6cw", "high", []string{"CVE-2026-55797", gj6cw}},
		{"g3ff", "medium", []string{gg3ff}},
		{"w996", "medium", []string{gw996}},
		{"52cp", "", []string{"CVE-2026-59869", g52cp}},
	}
	return []*release.Doc{
		relDoc(t, "v3.2.11", secSpec{"3v3m", "critical", []string{g3v3m}}),
		relDoc(t, "v3.3.9", secSpec{"3v3m", "critical", []string{"CVE-2026-42880", g3v3m}}, secSpec{"go", "", nil}),
		relDoc(t, "v3.3.15", v3315...),
		relDoc(t, "v3.4.10", coordinated()...),
		relDoc(t, "v3.5.4", coordinated("CVE-2026-113709")...),
		relDoc(t, "v3.6.0",
			secSpec{"fmxq", "", []string{gfmxq}}, secSpec{"g3ff", "", []string{gg3ff}}, secSpec{"9v9p", "", []string{g9v9p}},
			secSpec{"fw5c", "", []string{gfw5c}}, secSpec{"m3vr", "", []string{gm3vr}}, secSpec{"4439", "", []string{g4439}},
			secSpec{"j6cw", "", []string{gj6cw}}, secSpec{"w996", "", []string{gw996}}),
	}
}

// The field report's case: one set of fixes shipped on four release lines.
// The per-release sums keep their 1.0.0 values; the new count says five
// critical advisories where the sums say 15.
func TestDistinctAdvisoriesCountEachAdvisoryOnce(t *testing.T) {
	docs := argoRange(t)
	b, settled := Build(docs, nil, nil)
	if settled != 0 {
		t.Fatalf("settled %d with no running branch", settled)
	}
	if b.Summary.Warnings["critical"] != 15 || b.Summary.Counts["security"] != 39 || b.Summary.Releases != 6 {
		t.Errorf("per-release sums changed: releases %d, security %d, critical %d (want 6, 39, 15)",
			b.Summary.Releases, b.Summary.Counts["security"], b.Summary.Warnings["critical"])
	}
	want := advisory.Counts{Total: 10, Critical: 5, High: 2, Medium: 2, Low: 0, Ungraded: 1}
	if got := b.Summary.DistinctAdvisories; got != want {
		t.Errorf("distinct advisories %+v, want %+v (critical: 3v3m 9v9p fw5c m3vr fmxq; the eight-advisory card's grade must not reach 4439 j6cw g3ff w996; 52cp has no grade)", got, want)
	}
	raw, _ := json.Marshal(b.Summary)
	// Every grade present; before counts and warnings since 1.2.0 (see
	// stackSummary: the first number met is the one quoted).
	const shape = `{"releases":6,"distinct_advisories":{"total":10,"critical":5,"high":2,"medium":2,"low":0,"ungraded":1},"counts":`
	if !strings.HasPrefix(string(raw), shape) || !strings.HasSuffix(string(raw), `"warnings":{"critical":15,"no_downgrade":0}}`) {
		t.Errorf("summary JSON %s, want it to start %s and end with warnings (every grade present, before counts and warnings)", raw, shape)
	}
	assertSameAs100(t, docs, nil, nil)
}

// Links chain: the ids of one advisory, linked on different releases, count
// once, whichever release carries the link — the whole range is read before
// any change is counted.
func TestLinkedIDsCountOnce(t *testing.T) {
	const cve, ghsa, gov = "CVE-2026-1001", "GHSA-2222-3333-4444", "GO-2026-0001"
	for name, docs := range map[string][]*release.Doc{
		"links first": {
			relDoc(t, "v1.1.0", secSpec{"a", "", []string{cve, ghsa}}),
			relDoc(t, "v1.2.0", secSpec{"a", "high", []string{ghsa, gov}}),
			relDoc(t, "v1.3.0", secSpec{"a", "", []string{gov}}),
			relDoc(t, "v1.4.0", secSpec{"a", "", []string{cve}}),
		},
		"link last": {
			relDoc(t, "v1.1.0", secSpec{"a", "", []string{cve}}),
			relDoc(t, "v1.2.0", secSpec{"a", "high", []string{gov}}),
			relDoc(t, "v1.3.0", secSpec{"a", "", []string{ghsa}}),
			relDoc(t, "v1.4.0", secSpec{"a", "", []string{"cve-2026-1001 ", gov, ghsa}}),
		},
	} {
		b, _ := Build(docs, nil, nil)
		if got := b.Summary.DistinctAdvisories; got != (advisory.Counts{Total: 1, High: 1}) {
			t.Errorf("%s: distinct %+v, want one high advisory", name, got)
		}
		assertSameAs100(t, docs, nil, nil)
	}
}

// Envoy v1.39.0 has two cards citing [CVE-2026-48743, GHSA-p7c7-7c47-pwch];
// six cards elsewhere pair that CVE with GHSA-8phg-2h2q-jgxf (headers-only
// Content-Length), and GHSA-p7c7 is the HTTP/3 QPACK fix. Chained, the three
// ids were counted as one advisory (independent review, 2026-11-18). The
// cards disagree, so none of those links is used, whatever order they come
// in: each id counts on its own, three where there are two, never one.
func TestDisputedIDsLinkNothing(t *testing.T) {
	const cve, g8phg, gp7c7 = "CVE-2026-48743", "GHSA-8phg-2h2q-jgxf", "GHSA-p7c7-7c47-pwch"
	qp, cl := secSpec{"qpack", "high", []string{gp7c7}}, secSpec{"cl", "medium", []string{cve, g8phg}}
	docs := []*release.Doc{
		relDoc(t, "v1.36.9", qp, cl),
		relDoc(t, "v1.37.5", qp, cl),
		relDoc(t, "v1.38.3", qp, cl),
		relDoc(t, "v1.39.0", qp, secSpec{"wrong1", "medium", []string{cve, gp7c7}}, secSpec{"wrong2", "medium", []string{cve, gp7c7}}, cl),
	}
	b, _ := Build(docs, nil, nil)
	if got := b.Summary.DistinctAdvisories; got != (advisory.Counts{Total: 3, High: 1, Medium: 2}) {
		t.Errorf("distinct %+v, want total 3: the QPACK fix high on its own, the CVE and GHSA-8phg medium apart", got)
	}
	assertSameAs100(t, docs, nil, nil)
	rev := slices.Clone(docs)
	slices.Reverse(rev)
	a, z := advisory.NewIndex(docs), advisory.NewIndex(rev)
	for _, id := range []string{cve, g8phg, gp7c7} {
		n := advisory.NormID(id)
		if root(a, n) != n || root(z, n) != n {
			t.Errorf("%s is linked (%s, reversed %s); a disputed group links nothing", n, root(a, n), root(z, n))
		}
	}
	// An undisputed group elsewhere in the same range still links.
	docs = append(docs, relDoc(t, "v1.40.0", secSpec{"x", "low", []string{"CVE-2026-9999", "GHSA-2222-3333-4444"}}),
		relDoc(t, "v1.40.1", secSpec{"y", "low", []string{"GHSA-2222-3333-4444"}}))
	if got := root(advisory.NewIndex(docs), "GHSA-2222-3333-4444"); got != "CVE-2026-9999" {
		t.Errorf("an undisputed CVE ~ GHSA pair is not linked: root %s", got)
	}
}

// root is the advisory a recognised id names: the root of its group.
func root(ix *advisory.Index, id string) string { return ix.Advisories([]string{id})[0] }

// What must count apart.
func TestUnlinkedIDsCountApart(t *testing.T) {
	cases := []struct {
		name string
		docs []*release.Doc
		want advisory.Counts
	}{
		// A value that is not an advisory id links nothing, even next to real ids.
		{"N/A", []*release.Doc{
			relDoc(t, "v1.2.0", secSpec{"a", "high", []string{"CVE-2026-1111", "N/A"}}),
			relDoc(t, "v1.3.0", secSpec{"b", "high", []string{"CVE-2026-2222", "N/A"}}),
		}, advisory.Counts{Total: 2, High: 2}},
		// Two CVEs cited together are two advisories, and neither links the other.
		{"two of a family", []*release.Doc{
			relDoc(t, "v1.2.0", secSpec{"both", "", []string{"CVE-2026-3333", "CVE-2026-4444"}}),
			relDoc(t, "v1.3.0", secSpec{"x", "high", []string{"CVE-2026-3333"}}),
			relDoc(t, "v1.4.0", secSpec{"y", "", []string{"CVE-2026-4444"}}),
		}, advisory.Counts{Total: 2, High: 1, Ungraded: 1}},
		// A change citing only values that are not advisory ids cites no
		// advisory id: not counted, as such a value next to a CVE never was
		// (final review, 2026-11-18: it was counted when it stood alone).
		{"unrecognised values only", []*release.Doc{
			relDoc(t, "v1.2.0", secSpec{"a", "", []string{"VENDOR-1", "VENDOR-2"}}),
			relDoc(t, "v1.3.0", secSpec{"a", "", []string{"vendor-2", "vendor-1"}}),
			relDoc(t, "v1.4.0", secSpec{"a", "low", []string{"OSV-2026-1"}}),
			relDoc(t, "v1.5.0", secSpec{"b", "high", []string{"CVE-2026-7777", "OSV-2026-1"}}),
		}, advisory.Counts{Total: 1, High: 1}},
		// A pairing no other card contradicts is taken at its word: one card
		// citing two different fixes' ids as one advisory counts them as one.
		{"an uncontradicted pairing", []*release.Doc{
			relDoc(t, "v1.2.0", secSpec{"a", "high", []string{"CVE-2026-1001", "GHSA-2222-3333-4444"}}),
		}, advisory.Counts{Total: 1, High: 1}},
		// Cited alone without a grade, and graded on a several-advisory card:
		// the card's grade, not ungraded (final review, 2026-11-18).
		{"ungraded alone, graded with others", []*release.Doc{
			relDoc(t, "v1.2.0", secSpec{"x", "", []string{"CVE-2026-1111"}}),
			relDoc(t, "v1.3.0", secSpec{"xy", "critical", []string{"CVE-2026-1111", "CVE-2026-2222"}}),
			relDoc(t, "v1.4.0", secSpec{"y", "low", []string{"CVE-2026-2222"}}),
		}, advisory.Counts{Total: 2, Critical: 1, Low: 1}},
		// One release, one advisory under two names: counted once.
		{"siblings", []*release.Doc{
			relDoc(t, "v1.22.0", secSpec{"a", "high", []string{"GHSA-2222-3333-4444"}}, secSpec{"b", "", []string{"CVE-2026-5555", "GHSA-2222-3333-4444"}}),
		}, advisory.Counts{Total: 1, High: 1}},
	}
	for _, c := range cases {
		b, _ := Build(c.docs, nil, nil)
		if got := b.Summary.DistinctAdvisories; got != c.want {
			t.Errorf("%s: distinct %+v, want %+v", c.name, got, c.want)
		}
		assertSameAs100(t, c.docs, nil, nil)
	}
}

// What check_stack excludes as already fixed on the running branch is not
// counted among the advisories the upgrade brings.
func TestDistinctAdvisoriesLeaveOutWhatIsAlreadyFixed(t *testing.T) {
	docs := []*release.Doc{
		relDoc(t, "v1.3.0", secSpec{"a", "critical", []string{"CVE-2026-1111"}}, secSpec{"b", "high", []string{"CVE-2026-2222"}}),
	}
	fixed, running := map[string]bool{"CVE-2026-1111": true}, []int{1, 2, 5}
	b, settled := Build(docs, fixed, running)
	if settled != 1 {
		t.Fatalf("settled %d, want 1", settled)
	}
	if got := b.Summary.DistinctAdvisories; got != (advisory.Counts{Total: 1, High: 1}) {
		t.Errorf("distinct %+v, want only the high one", got)
	}
	if b.Summary.Warnings["critical"] != 1 {
		t.Errorf("warnings.critical %d: the per-release sum keeps counting the release as published", b.Summary.Warnings["critical"])
	}
	// The left-out critical change is said beside the figure that still counts
	// it, in the summary and on its release; the release document is not touched.
	if got := b.Summary.Warnings[criticalExcludedKey]; got != 1 {
		t.Errorf("summary.warnings.%s %d, want 1", criticalExcludedKey, got)
	}
	if got := (*b.Releases)[0].Warnings[criticalExcludedKey]; got != 1 {
		t.Errorf("release warnings.%s %d, want 1", criticalExcludedKey, got)
	}
	if _, ok := docs[0].Warnings[criticalExcludedKey]; ok {
		t.Error("the cached release document got the key")
	}
	raw, _ := json.Marshal(b.Summary.Warnings)
	if string(raw) != `{"critical":1,"critical_excluded_on_branch":1,"no_downgrade":0}` {
		t.Errorf("warnings as answered: %s", raw)
	}
	assertSameAs100(t, docs, fixed, running)

	// Nothing critical left out: the key is absent, in the summary and on the row.
	fixed = map[string]bool{"CVE-2026-2222": true}
	b, settled = Build(docs, fixed, running)
	if _, ok := b.Summary.Warnings[criticalExcludedKey]; ok || settled != 1 {
		t.Errorf("a high change left out: settled %d, warnings %v", settled, b.Summary.Warnings)
	}
	if _, ok := (*b.Releases)[0].Warnings[criticalExcludedKey]; ok {
		t.Error("the release row carries the key with no critical change left out")
	}
}

// The docs and the hint say what distinct_advisories leaves out (security
// changes citing no advisory id) and how it grades an advisory that only
// several-advisory cards cite (the highest of their grades). Both were
// missing (independent review, 2026-11-18): an agent quoting the count as
// "every security fix" over-promised.
func TestDistinctAdvisoriesAreDescribedAsComputed(t *testing.T) {
	t.Chdir(filepath.Join("..", "..")) // the docs are read from the repository root
	b, _ := Build([]*release.Doc{relDoc(t, "v1.2.0",
		secSpec{"xy", "critical", []string{"CVE-2026-1111", "CVE-2026-2222"}},
		secSpec{"xz", "high", []string{"CVE-2026-1111", "CVE-2026-3333"}},
		secSpec{"z", "low", []string{"CVE-2026-3333"}},
		secSpec{"none", "critical", nil},
	)}, nil, nil)
	// 1111 and 2222: only several-advisory cards cite them, the highest is
	// critical. 3333: cited alone as low. The id-less change: not counted.
	if got, want := b.Summary.DistinctAdvisories, (advisory.Counts{Total: 3, Critical: 2, Low: 1}); got != want {
		t.Errorf("distinct %+v, want %+v", got, want)
	}
	if !strings.Contains(stackHint, "citing no advisory id are not in it") {
		t.Error("the hint does not say security changes citing no advisory id are left out of distinct_advisories")
	}
	for _, p := range []string{"docs/tools.en.md", "docs/tools.ko.md", "docs/tools.ja.md"} {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "| `distinct_advisories` |") {
				continue
			}
			if !strings.Contains(line, "`counts.security`") {
				t.Errorf("%s: the distinct_advisories row does not point to counts.security for what it leaves out", p)
			}
			// The ids that count, named in every language (the scanner's
			// families, internal/advisory).
			for _, fam := range []string{"CVE", "GHSA", "GO", "RUSTSEC", "PYSEC"} {
				if !strings.Contains(line, fam) {
					t.Errorf("%s: the distinct_advisories row does not name %s among the ids that count", p, fam)
				}
			}
		}
	}
	// What the English row promises (final review, 2026-11-18): a pairing no
	// other card contradicts is believed, and the grade falls back when the
	// changes citing an advisory alone carry none.
	en, _ := os.ReadFile("docs/tools.en.md")
	for _, want := range []string{"no other change contradicts is taken at its word", "when none of those carries a grade"} {
		if !strings.Contains(string(en), want) {
			t.Errorf("docs/tools.en.md: the distinct_advisories row does not say %q", want)
		}
	}
}

// distinct_advisories leaves out an id the running branch already fixed, even
// when a change that stays listed cites it beside others: envoy v1.37.6 →
// v1.40.2 counted nine critical advisories for one (2026-11-20).
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestDistinctAdvisoriesLeaveOutWhatTheBranchFixed(t *testing.T) {
	crit := "critical"
	bundle := release.Change{ID: "e#bundle", Kind: "security", Severity: &crit,
		Advisories: []string{"CVE-2026-111497", "GHSA-3wvj-j6qm-62mr", "CVE-2026-73512", "CVE-2026-73513"}}
	own := release.Change{ID: "e#brotli", Kind: "security", Severity: &crit, Advisories: []string{"CVE-2026-111497", "GHSA-3wvj-j6qm-62mr"}}
	docs := []*release.Doc{
		{Version: "v1.37.8", Changes: []release.Change{own}},
		{Version: "v1.40.0", Changes: []release.Change{bundle}},
	}
	fixed := map[string]bool{"CVE-2026-73512": true, "CVE-2026-73513": true}
	b, settled := Build(docs, fixed, []int{1, 37, 6})
	if settled != 0 {
		t.Fatalf("settled %d: the bundle cites ids the branch has not fixed and stays listed", settled)
	}
	if got, want := b.Summary.DistinctAdvisories, (advisory.Counts{Total: 1, Critical: 1}); got != want {
		t.Errorf("distinct advisories %+v, want %+v", got, want)
	}
	// With nothing known about the branch, every id counts, as in 1.1.0.
	b, _ = Build(docs, nil, []int{1, 37, 6})
	if b.Summary.DistinctAdvisories.Total != 3 {
		t.Errorf("without a fixed set, total %d, want 3", b.Summary.DistinctAdvisories.Total)
	}
}
