package tools

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/release"
)

func briefedRange(versions ...string) *[]briefing.RangeRelease {
	var out []briefing.RangeRelease
	for _, v := range versions {
		out = append(out, briefing.RangeRelease{Version: v})
	}
	return &out
}

// Neighbouring steps with nothing to read fold into one, but not across a
// major version; the step after a major boundary says so.
func TestStepsFoldQuietNeighboursWithinAMajor(t *testing.T) {
	b := &briefing.Briefing{
		Releases: briefedRange("v1.7.0", "v1.7.1", "v1.8.0", "v1.9.0", "v1.10.0", "v2.0.0", "v2.1.0", "v2.2.0"),
		Changes: []briefing.Change{
			{Version: "v1.9.0", ID: "p@v1.9.0#k", Kind: "breaking", Title: "k", ActionRequired: "migrate"},
			{Version: "v1.8.0", ID: "p@v1.8.0#x", Kind: "breaking", Title: "x"},
		},
		ChangesMore: []briefing.MoreLine{{Version: "v2.1.0", ID: "p@v2.1.0#d", Kind: "deprecations", Title: "d"}},
		Upgrade:     &[]briefing.UpgradeItem{},
	}
	steps := upgradeSteps(stepsIn{project: "p", b: b, from: "v1.6.4",
		next: func(from, to string) *toolCall { return &toolCall{Tool: "t", Arguments: []string{from, to}} }})
	var got []string
	for _, s := range steps {
		got = append(got, fmt.Sprintf("%s %s %v %v", s.Line, s.Releases, s.MajorBoundary, s.Next.Arguments))
	}
	want := []string{
		"1.7–1.8 v1.7.0..v1.8.0 (3) false [v1.6.4 v1.8.0]",
		"1.9 v1.9.0 (1) false [v1.8.0 v1.9.0]",
		"1.10 v1.10.0 (1) false [v1.9.0 v1.10.0]",
		"2.0–2.2 v2.0.0..v2.2.0 (3) true [v1.10.0 v2.2.0]",
	}
	if !slices.Equal(got, want) {
		t.Errorf("steps\n%q\nwant\n%q", got, want)
	}
	if steps[0].Counts["breaking"] != 1 || len(steps[0].Lines) != 1 || steps[0].Lines[0].ID != "p@v1.8.0#x" || steps[3].Counts["deprecations"] != 1 {
		t.Errorf("counts and lines %+v %+v", steps[0], steps[3])
	}
	if len(steps[1].Lines) != 0 || steps[1].Verify == nil || steps[0].Verify != nil {
		t.Errorf("step 1.9 %+v", steps[1])
	}
	// From another major, the first step is a boundary too.
	steps = upgradeSteps(stepsIn{project: "p", b: b, from: "v0.34.2"})
	if !steps[0].MajorBoundary || steps[1].MajorBoundary {
		t.Errorf("from v0.34.2: %+v", steps[:2])
	}
}

// An advisory takes the grade of a change citing it alone first; no grade is
// "ungraded", not "low".
func TestStepsGradeAdvisories(t *testing.T) {
	sev := func(s string) json.RawMessage {
		if s == "" {
			return json.RawMessage("null")
		}
		b, _ := json.Marshal(s)
		return b
	}
	const one, two, three = "CVE-2026-11111", "GHSA-2222-3333-4444", "CVE-2026-33333"
	b := &briefing.Briefing{
		Releases: briefedRange("v1.0.0", "v1.0.1"),
		Changes: []briefing.Change{
			{Version: "v1.0.0", ID: "p@v1.0.0#a", Kind: "security", Severity: sev("critical")},
			{Version: "v1.0.1", ID: "p@v1.0.1#b", Kind: "security", Severity: sev("medium")},
		},
		ChangesMore: []briefing.MoreLine{{Version: "v1.0.1", ID: "p@v1.0.1#c", Kind: "security", Severity: sev("")}},
	}
	docs := []*release.Doc{
		{Changes: []release.Change{{ID: "p@v1.0.0#a", Advisories: []string{one, two}}}},
		{Changes: []release.Change{{ID: "p@v1.0.1#b", Advisories: []string{one}}, {ID: "p@v1.0.1#c", Advisories: []string{three, "N/A"}}}},
	}
	steps := upgradeSteps(stepsIn{project: "p", b: b, docs: docs})
	want := map[string]string{one: "medium", two: "critical", three: "ungraded"}
	if len(steps) != 1 || !stringMapsEqual(steps[0].Advisories, want) {
		t.Errorf("advisories %+v, want %v", steps, want)
	}
}

func stringMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// verify is one call while get_release takes the ids in one, and several when
// they pass its 50 ids or 10 releases.
func TestVerifySplitsWhatOneCallCannotTake(t *testing.T) {
	lines := func(n, releases int) []mustReadLine {
		var out []mustReadLine
		for i := range n {
			out = append(out, mustReadLine{ID: fmt.Sprintf("p@v1.0.%d#%d", i%releases, i)})
		}
		return out
	}
	if _, ok := verifyCalls("p", lines(50, 10), "").(*toolCall); !ok {
		t.Error("50 ids in 10 releases: one call")
	}
	for _, c := range []struct{ n, releases, calls int }{{51, 1, 2}, {11, 11, 2}, {120, 3, 3}} {
		calls, ok := verifyCalls("p", lines(c.n, c.releases), "en").([]*toolCall)
		if !ok || len(calls) != c.calls {
			t.Errorf("%d ids in %d releases: %d calls, want %d", c.n, c.releases, len(calls), c.calls)
		}
	}
	if verifyCalls("p", nil, "") != nil {
		t.Error("no line: no verify")
	}
}

// totals count every distinct change of a kind the call covers: important
// fixes past their cap are in changes_omitted only.
func TestRangeTotalsCountWhatTheBriefingCounts(t *testing.T) {
	b := &briefing.Briefing{
		Changes:        []briefing.Change{{Kind: "security"}, {Kind: "important_fixes"}},
		ChangesOmitted: map[string]int{"security": 2, "important_fixes": 30},
	}
	got := rangeTotals(b, nil)
	if got["security"] != 3 || got["important_fixes"] != 31 || got["breaking"] != 0 || len(got) != 4 {
		t.Errorf("totals %v", got)
	}
	if got := rangeTotals(b, map[string]bool{"security": true}); len(got) != 1 || got["security"] != 3 {
		t.Errorf("kinds security: %v", got)
	}
}

// A must_read line carries every mark 1.2.0 gives a line for what a change
// asks: also_required on a security change too. A change without a mark is a
// line.
func TestMustReadTakesTheFourMarks(t *testing.T) {
	b := &briefing.Briefing{
		Releases: briefedRange("v1.0.0"),
		Changes: []briefing.Change{
			{Version: "v1.0.0", ID: "p@v1.0.0#s", Kind: "security", AlsoRequired: "rotate the token"},
			{Version: "v1.0.0", ID: "p@v1.0.0#d", Kind: "deprecations", RemovedIn: &release.Attr{Text: "v2", Status: "stated"}},
		},
		ChangesMore: []briefing.MoreLine{
			{Version: "v1.0.0", ID: "p@v1.0.0#m", Kind: "security", AlsoRequired: true, NoDowngrade: true},
			{Version: "v1.0.0", ID: "p@v1.0.0#n", Kind: "breaking"},
		},
	}
	steps := upgradeSteps(stepsIn{project: "p", b: b})
	var got []string
	for _, m := range steps[0].MustRead {
		got = append(got, fmt.Sprintf("%s %v", m.ID, m.Why))
	}
	want := []string{"p@v1.0.0#s [also_required]", "p@v1.0.0#m [also_required no_downgrade]", "p@v1.0.0#d [removed_in]"}
	if !slices.Equal(got, want) {
		t.Errorf("must_read %q, want %q", got, want)
	}
	if len(steps[0].Lines) != 1 || steps[0].Lines[0].ID != "p@v1.0.0#n" || steps[0].Lines[0].Kind != "breaking" {
		t.Errorf("lines %+v, want the breaking change without a mark", steps[0].Lines)
	}
	if b, _ := json.Marshal(steps[0]); strings.Contains(string(b), `"advisories"`) {
		t.Errorf("no advisory cited, yet %s", b)
	}
}
