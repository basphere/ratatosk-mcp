package main

// Same-fix grouping and distinct advisory counts (1.1.0). The Argo CD fixture
// is the 2026-11-18 field report's range cut down to its security changes: the
// ids and grades are the live ones of v3.2.11, v3.3.9, v3.3.15, v3.4.10,
// v3.5.4 and v3.6.0 (read from /v2 the same day), including the v3.3.15 card
// that cites eight GHSAs next to a card for each of them.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

type secSpec struct {
	id    string
	grade string // "" = null
	advs  []string
}

// relDoc builds a release whose counts and warnings are what the website
// would show for these security changes: security = how many, critical = how
// many critical.
func relDoc(t *testing.T, ver string, changes ...secSpec) *v2Release {
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
	r, err := parseRelease(b)
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
// hand it to buildBriefing.
func argoRange(t *testing.T) []*v2Release {
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
	return []*v2Release{
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

func byID(b briefing) map[string]briefChange {
	out := map[string]briefChange{}
	for _, c := range b.Changes {
		out[c.ID] = c
	}
	return out
}

// The field report's case: one set of fixes shipped on four release lines.
// The per-release sums keep their 1.0.0 values; the new count says five
// critical advisories where the sums say 15.
func TestDistinctAdvisoriesCountEachAdvisoryOnce(t *testing.T) {
	b, settled := buildBriefing(argoRange(t), nil, nil)
	if settled != 0 {
		t.Fatalf("settled %d with no running branch", settled)
	}
	if b.Summary.Warnings["critical"] != 15 || b.Summary.Counts["security"] != 39 || b.Summary.Releases != 6 {
		t.Errorf("per-release sums changed: releases %d, security %d, critical %d (want 6, 39, 15)",
			b.Summary.Releases, b.Summary.Counts["security"], b.Summary.Warnings["critical"])
	}
	want := distinctAdvisories{Total: 10, Critical: 5, High: 2, Medium: 2, Low: 0, Ungraded: 1}
	if got := b.Summary.DistinctAdvisories; got != want {
		t.Errorf("distinct advisories %+v, want %+v (critical: 3v3m 9v9p fw5c m3vr fmxq; the eight-advisory card's grade must not reach 4439 j6cw g3ff w996; 52cp has no grade)", got, want)
	}
	raw, _ := json.Marshal(b.Summary)
	const shape = `"warnings":{"critical":15,"no_downgrade":0},"distinct_advisories":{"total":10,"critical":5,"high":2,"medium":2,"low":0,"ungraded":1}}`
	if !strings.HasSuffix(string(raw), shape) {
		t.Errorf("summary JSON %s, want it to end %s (every grade present, after warnings)", raw, shape)
	}
}

// GHSA-3v3m: [GHSA-3v3m] on v3.2.11 and [CVE-2026-42880, GHSA-3v3m] on v3.3.9
// came back as two fixes in 1.0.0. One shared id is the same fix now, and the
// entry keeps the CVE only v3.3.9 cites.
func TestSameFixWhenOneAdvisoryIDIsShared(t *testing.T) {
	b, _ := buildBriefing(argoRange(t), nil, nil)
	got := byID(b)
	e, ok := got["argo@v3.2.11#3v3m"]
	if !ok {
		t.Fatalf("no entry at v3.2.11 for GHSA-3v3m: %v", keysOf(got))
	}
	if !slices.Equal(e.AlsoIn, []string{"v3.3.9"}) {
		t.Errorf("GHSA-3v3m also_in %v, want [v3.3.9]", e.AlsoIn)
	}
	if !slices.Equal(e.Advisories, []string{g3v3m, "CVE-2026-42880"}) {
		t.Errorf("GHSA-3v3m advisories %v, want the GHSA and the CVE only v3.3.9 cites", e.Advisories)
	}
	if _, ok := got["argo@v3.3.9#3v3m"]; ok {
		t.Error("v3.3.9's GHSA-3v3m is listed on its own; it is the same fix on another branch")
	}
	// m3vr carries a CVE on v3.3.15 only: one entry, the other three lines named.
	if m := got["argo@v3.3.15#m3vr"]; !slices.Equal(m.AlsoIn, []string{"v3.4.10", "v3.5.4", "v3.6.0"}) {
		t.Errorf("GHSA-m3vr also_in %v, want [v3.4.10 v3.5.4 v3.6.0]", m.AlsoIn)
	}
	for _, id := range []string{"argo@v3.4.10#m3vr", "argo@v3.5.4#fw5c", "argo@v3.5.4#m3vr"} {
		if _, ok := got[id]; ok {
			t.Errorf("%s listed on its own: its advisory is already an entry", id)
		}
	}
}

// The eight-advisory card shares an id with each of eight cards in its own
// release. Joined through it, the eight fixes would be one: it stays an entry
// of its own, and so does each of the eight.
func TestACardCitingSeveralAdvisoriesJoinsNothing(t *testing.T) {
	b, _ := buildBriefing(argoRange(t), nil, nil)
	got := byID(b)
	for _, id := range []string{"9v9p", "fw5c", "m3vr", "fmxq", "eight", "4439", "j6cw", "g3ff", "w996", "52cp"} {
		if _, ok := got["argo@v3.3.15#"+id]; !ok {
			t.Errorf("v3.3.15 %s is not an entry: %v", id, keysOf(got))
		}
	}
	if e := got["argo@v3.3.15#eight"]; len(e.AlsoIn) != 0 || len(e.Advisories) != 8 {
		t.Errorf("the eight-advisory card: also_in %v, %d advisories; want none and eight", e.AlsoIn, len(e.Advisories))
	}
	// 3v3m 1 + the ten v3.3.15 cards + the two id-less changes (v3.3.9, and one
	// each on v3.4.10 and v3.5.4).
	security := 0
	for _, c := range b.Changes {
		if c.Kind == "security" {
			security++
		}
	}
	if security+b.ChangesOmitted["security"] != 14 {
		t.Errorf("%d security entries (+%d omitted), want 14", security, b.ChangesOmitted["security"])
	}
}

// A link between ids chains (CVE ~ GHSA on one release, GHSA ~ GO on another),
// and it holds whichever release carries the link — the whole range is read
// before any change is keyed.
func TestSameFixChainsThroughLinkedIDs(t *testing.T) {
	const cve, ghsa, gov = "CVE-2026-1001", "GHSA-2222-3333-4444", "GO-2026-0001"
	cases := map[string][]*v2Release{
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
	}
	for name, docs := range cases {
		b, _ := buildBriefing(docs, nil, nil)
		if len(b.Changes) != 1 {
			t.Errorf("%s: %d entries, want one fix", name, len(b.Changes))
			continue
		}
		e := b.Changes[0]
		if e.ID != "argo@v1.1.0#a" || !slices.Equal(e.AlsoIn, []string{"v1.2.0", "v1.3.0", "v1.4.0"}) {
			t.Errorf("%s: entry %s also_in %v", name, e.ID, e.AlsoIn)
		}
		if string(e.Severity) != `"high"` {
			t.Errorf("%s: severity %s, want the backport's high", name, e.Severity)
		}
		ids := map[string]bool{}
		for _, id := range e.Advisories {
			ids[normAdvisoryID(id)] = true
		}
		if len(ids) != 3 || !ids[cve] || !ids[ghsa] || !ids[gov] {
			t.Errorf("%s: advisories %v, want the three ids once each", name, e.Advisories)
		}
		if got := b.Summary.DistinctAdvisories; got.Total != 1 || got.High != 1 {
			t.Errorf("%s: distinct %+v, want one high advisory", name, got)
		}
	}
}

// Negative cases: what must stay apart.
func TestSameFixNeedsAnAdvisoryInCommon(t *testing.T) {
	// A value that is not an advisory id never joins two fixes, even when the
	// changes also cite real ids.
	b, _ := buildBriefing([]*v2Release{
		relDoc(t, "v1.2.0", secSpec{"a", "high", []string{"CVE-2026-1111", "N/A"}}),
		relDoc(t, "v1.3.0", secSpec{"b", "high", []string{"CVE-2026-2222", "N/A"}}),
	}, nil, nil)
	if len(b.Changes) != 2 || len(b.Changes[0].AlsoIn)+len(b.Changes[1].AlsoIn) != 0 {
		t.Errorf("two CVEs sharing only \"N/A\" were joined: %+v", b.Changes)
	}
	if got := b.Summary.DistinctAdvisories; got.Total != 2 {
		t.Errorf("distinct %+v, want two (N/A is not an advisory)", got)
	}

	// Two CVEs cited together are two advisories: neither links the other, so
	// a later release citing one of them is not the fix for the other.
	b, _ = buildBriefing([]*v2Release{
		relDoc(t, "v1.2.0", secSpec{"both", "", []string{"CVE-2026-3333", "CVE-2026-4444"}}),
		relDoc(t, "v1.3.0", secSpec{"x", "", []string{"CVE-2026-3333"}}),
		relDoc(t, "v1.4.0", secSpec{"y", "", []string{"CVE-2026-4444"}}),
	}, nil, nil)
	got := byID(b)
	if len(got) != 3 {
		t.Fatalf("want three entries (the pair, then each CVE on its own branch), got %v", keysOf(got))
	}
	if x := got["argo@v1.3.0#x"]; len(x.AlsoIn) != 0 {
		t.Errorf("CVE-2026-4444's fix folded into CVE-2026-3333's: also_in %v", x.AlsoIn)
	}

	// A change citing only values that are not advisory ids keeps the 1.0.0
	// identity: the same whole set folds, a different one does not.
	b, _ = buildBriefing([]*v2Release{
		relDoc(t, "v1.2.0", secSpec{"a", "", []string{"VENDOR-1", "VENDOR-2"}}),
		relDoc(t, "v1.3.0", secSpec{"a", "", []string{"vendor-2", "vendor-1"}}),
		relDoc(t, "v1.4.0", secSpec{"a", "", []string{"VENDOR-1"}}),
	}, nil, nil)
	if len(b.Changes) != 2 || !slices.Equal(b.Changes[0].AlsoIn, []string{"v1.3.0"}) {
		t.Errorf("unrecognised ids: want [VENDOR-1 VENDOR-2] folded once and [VENDOR-1] apart, got %+v", b.Changes)
	}
}

// Two changes of one release naming one advisory under different ids are one
// entry (the same rule as identical ids), with the ids of both.
func TestSiblingsNamingOneAdvisoryAreOneEntry(t *testing.T) {
	b, _ := buildBriefing([]*v2Release{
		relDoc(t, "v1.22.0", secSpec{"a", "high", []string{"GHSA-2222-3333-4444"}}, secSpec{"b", "", []string{"CVE-2026-5555", "GHSA-2222-3333-4444"}}),
	}, nil, nil)
	if len(b.Changes) != 1 || !slices.Equal(b.Changes[0].Advisories, []string{"GHSA-2222-3333-4444", "CVE-2026-5555"}) {
		t.Errorf("want one entry citing both ids, got %+v", b.Changes)
	}
}

// What check_stack excludes as already fixed on the running branch is not
// counted among the advisories the upgrade brings.
func TestDistinctAdvisoriesLeaveOutWhatIsAlreadyFixed(t *testing.T) {
	b, settled := buildBriefing([]*v2Release{
		relDoc(t, "v1.3.0", secSpec{"a", "critical", []string{"CVE-2026-1111"}}, secSpec{"b", "high", []string{"CVE-2026-2222"}}),
	}, map[string]bool{"CVE-2026-1111": true}, []int{1, 2, 5})
	if settled != 1 {
		t.Fatalf("settled %d, want 1", settled)
	}
	if got := b.Summary.DistinctAdvisories; got != (distinctAdvisories{Total: 1, High: 1}) {
		t.Errorf("distinct %+v, want only the high one", got)
	}
	if b.Summary.Warnings["critical"] != 1 {
		t.Errorf("warnings.critical %d: the per-release sum keeps counting the release as published", b.Summary.Warnings["critical"])
	}
}

func TestRecognisedAdvisoryIDs(t *testing.T) {
	got := recognisedIDs([]string{" ghsa-3v3m-wc6v-x4x3", "CVE-2026-42880", "GO-2026-1234", "RUSTSEC-2026-0001", "PYSEC-2026-7",
		"GHSA-aaaa-bbbb-cccc", "CVE-26-1", "N/A", "", "#30040", "CVE-2026-42880"})
	want := []string{"CVE-2026-42880", "GHSA-3V3M-WC6V-X4X3", "GO-2026-1234", "PYSEC-2026-7", "RUSTSEC-2026-0001"}
	if !slices.Equal(got, want) {
		t.Errorf("recognised %v, want %v", got, want)
	}
	if namesOneAdvisory([]string{"CVE-2026-1", "GHSA-2222-3333-4444"}) != true || namesOneAdvisory([]string{"CVE-2026-1111", "CVE-2026-2222"}) {
		t.Error("one id per family names one advisory; two of a family name two")
	}
}

func keysOf(m map[string]briefChange) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
