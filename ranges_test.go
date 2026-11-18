package main

// The 1.1.0 regression harness: on real /v2/upgrade ranges, every answer
// 1.1.0 gives must hold everything the 1.0.0 answer held.
//
// Why it exists: 1.1.0 groups more security changes into one entry (same fix
// under different ids), and the rule for 1.x is additive only — a 1.0.0
// client must never get a silently wrong or poorer answer. The first cut of
// that grouping passed every hand-written test and still, on live ranges,
// dropped six workarounds, three regression_since values and two
// also_required steps, lost also_in relations 1.0.0 had, and merged two
// different advisories through one card's wrong id (independent review,
// 2026-11-18). Hand-written cases test what the author thought of; this runs
// both versions over what the worker actually wrote.
//
// What it asserts, per range, with the per-kind caps lifted (a cap cuts a
// different tail when the entry count changes, which is not a loss):
//
//	(i)   summary.counts, summary.warnings, releases, before_upgrade and the
//	      settled count are identical;
//	(ii)  every non-empty workaround, regression_since, also_required,
//	      action_required, no_downgrade, removed_in, affects and later item,
//	      every advisory id, the grade and the security_unclear and
//	      silent_failure flags of a 1.0.0 entry are on the 1.1.0 entry that
//	      stands for it;
//	(iii) every (entry, also_in version) relation of 1.0.0 is in 1.1.0;
//	(iv)  no 1.1.0 entry stands for two changes whose recognised id sets are
//	      disjoint (unless their whole id sets are equal, the 1.0.0 identity).
//
// Where the ranges come from: testdata/ranges/*.json are /v2/upgrade answers
// cut down to the security changes whose ids another change in the range also
// cites under a different id set — the only ones the two versions can group
// differently — with every release kept (TestCutRangeFixtures writes them),
// committed so the check runs on every go test. RANGE_FIXTURES=/dir adds every
// *.json in that directory — whole /v2/upgrade answers saved as they came —
// for a full run before a release:
//
//	curl -s 'https://ratatosk.io/v2/upgrade/envoy?from=v1.33.0' > /tmp/ranges/envoy.json
//	RANGE_FIXTURES=/tmp/ranges go test -run TestRangesKeepEverything100Answered -v .
//
// The 1.0.0 side is buildBriefing100 (briefing100_test.go), a frozen copy.

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/basphere/ratatosk-mcp/internal/version"
)

type upgradeAnswer struct {
	Project  string            `json:"project"`
	From     string            `json:"from"`
	To       string            `json:"to"`
	Releases []json.RawMessage `json:"releases"`
}

type namedRange struct {
	name string
	docs []*v2Release
}

func loadRanges(t *testing.T) []namedRange {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join("testdata", "ranges", "*.json"))
	if len(paths) == 0 {
		t.Fatal("no committed range fixtures under testdata/ranges")
	}
	if dir := os.Getenv("RANGE_FIXTURES"); dir != "" {
		more, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		if len(more) == 0 {
			t.Fatalf("RANGE_FIXTURES=%s holds no *.json", dir)
		}
		paths = append(paths, more...)
	}
	var out []namedRange
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var up upgradeAnswer
		if err := json.Unmarshal(raw, &up); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if up.Project == "" || len(up.Releases) == 0 {
			t.Fatalf("%s is not a /v2/upgrade answer with releases", p)
		}
		var docs []*v2Release
		for _, r := range up.Releases {
			d, err := parseRelease(r)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			docs = append(docs, d)
		}
		out = append(out, namedRange{name: fmt.Sprintf("%s %s→%s (%s)", up.Project, up.From, up.To, filepath.Base(p)), docs: docs})
	}
	return out
}

// liftCaps sets every per-kind cap out of reach for the test.
func liftCaps(t *testing.T) {
	saved := maps.Clone(kindCaps)
	for k := range kindCaps {
		kindCaps[k] = 1 << 30
	}
	t.Cleanup(func() { kindCaps = saved })
}

// briefingDiff is what 1.1.0 lost against 1.0.0 on one range, plus how much it
// grouped. Every slice must be empty.
type briefingDiff struct {
	entries100, entries110 int
	grouped                int // 1.0.0 entries that stand inside another 1.1.0 entry
	split                  int // 1.1.0 entries that 1.0.0 folded away
	summary                []string
	missing                []string
	lost                   map[string][]string // field → "entry: value"
	lostAlsoIn             []string
	disjoint               []string
}

func (d briefingDiff) losses() int {
	n := len(d.summary) + len(d.missing) + len(d.lostAlsoIn) + len(d.disjoint)
	for _, v := range d.lost {
		n += len(v)
	}
	return n
}

func (d briefingDiff) lostTexts() int {
	n := 0
	for f, v := range d.lost {
		if f != "advisories" && f != "severity" {
			n += len(v)
		}
	}
	return n
}

func attrText(a *v2Attr) string {
	if a == nil {
		return ""
	}
	return a.Text
}

func compareWith100(docs []*v2Release, fixed map[string]bool, running []int) briefingDiff {
	old, oldSettled := buildBriefing100(docs, fixed, running)
	cur, curSettled := buildBriefing(docs, fixed, running)
	d := briefingDiff{entries100: len(old.Changes), entries110: len(cur.Changes), lost: map[string][]string{}}

	// (i)
	if oldSettled != curSettled {
		d.summary = append(d.summary, fmt.Sprintf("settled %d → %d", oldSettled, curSettled))
	}
	if old.Summary.Releases != cur.Summary.Releases || !maps.Equal(old.Summary.Counts, cur.Summary.Counts) || !maps.Equal(old.Summary.Warnings, cur.Summary.Warnings) {
		d.summary = append(d.summary, fmt.Sprintf("summary %+v → %+v", *old.Summary, *cur.Summary))
	}
	if !reflect.DeepEqual(old.Releases, cur.Releases) || !reflect.DeepEqual(old.BeforeUpgrade, cur.BeforeUpgrade) {
		d.summary = append(d.summary, "releases or before_upgrade differ")
	}
	if len(old.ChangesOmitted)+len(cur.ChangesOmitted) > 0 {
		d.summary = append(d.summary, "a cap cut changes: lift the caps before comparing")
	}

	home := map[string]int{}
	for i, c := range cur.Changes {
		home[c.ID] = i
		for _, m := range c.members {
			home[m] = i
		}
	}
	oldIDs := map[string]bool{}
	for _, e := range old.Changes {
		oldIDs[e.ID] = true
	}
	for _, c := range cur.Changes {
		if !oldIDs[c.ID] {
			d.split++
		}
	}

	// (ii) and (iii)
	for _, e := range old.Changes {
		i, ok := home[e.ID]
		if !ok {
			d.missing = append(d.missing, e.ID)
			continue
		}
		h := cur.Changes[i]
		if h.ID != e.ID {
			d.grouped++
		}
		lose := func(field, want, got string) {
			if want != "" && want != got {
				d.lost[field] = append(d.lost[field], fmt.Sprintf("%s: %q (1.1.0 entry %s has %q)", e.ID, want, h.ID, got))
			}
		}
		lose("workaround", attrText(e.Workaround), attrText(h.Workaround))
		lose("regression_since", attrText(e.RegressionSince), attrText(h.RegressionSince))
		lose("no_downgrade", attrText(e.NoDowngrade), attrText(h.NoDowngrade))
		lose("removed_in", attrText(e.RemovedIn), attrText(h.RemovedIn))
		lose("affects", attrText(e.Affects), attrText(h.Affects))
		lose("also_required", e.AlsoRequired, h.AlsoRequired)
		lose("action_required", e.ActionRequired, h.ActionRequired)
		for _, id := range e.Advisories {
			if !slices.ContainsFunc(h.Advisories, func(x string) bool { return normAdvisoryID(x) == normAdvisoryID(id) }) {
				d.lost["advisories"] = append(d.lost["advisories"], e.ID+": "+id)
			}
		}
		for _, l := range e.Later {
			if !slices.ContainsFunc(h.Later, func(x json.RawMessage) bool { return string(x) == string(l) }) {
				d.lost["later"] = append(d.lost["later"], e.ID+": "+string(l))
			}
		}
		if gradeRank100(h.Severity) < gradeRank100(e.Severity) {
			d.lost["severity"] = append(d.lost["severity"], fmt.Sprintf("%s: %s → %s", e.ID, e.Severity, h.Severity))
		}
		if e.SecurityUnclear && !h.SecurityUnclear {
			d.lost["security_unclear"] = append(d.lost["security_unclear"], e.ID)
		}
		if e.SilentFailure && !h.SilentFailure {
			d.lost["silent_failure"] = append(d.lost["silent_failure"], e.ID)
		}
		for _, v := range e.AlsoIn {
			if v != h.Version && !slices.Contains(h.AlsoIn, v) {
				d.lostAlsoIn = append(d.lostAlsoIn, fmt.Sprintf("%s also_in %s (1.1.0 entry %s also_in %v)", e.ID, v, h.ID, h.AlsoIn))
			}
		}
	}

	// (iv)
	rec, whole := map[string][]string{}, map[string]string{}
	for _, doc := range docs {
		for _, c := range doc.Changes {
			rec[c.ID], whole[c.ID] = recognisedIDs(c.Advisories), advisoryKey(c.Advisories)
		}
	}
	for _, h := range cur.Changes {
		ms := append([]string{h.ID}, h.members...)
		for a := range ms {
			for b := a + 1; b < len(ms); b++ {
				x, y := ms[a], ms[b]
				if whole[x] == whole[y] {
					continue
				}
				if !slices.ContainsFunc(rec[x], func(id string) bool { return slices.Contains(rec[y], id) }) {
					d.disjoint = append(d.disjoint, fmt.Sprintf("entry %s holds %s %v and %s %v", h.ID, x, rec[x], y, rec[y]))
				}
			}
		}
	}
	return d
}

// rangeModes are the two ways a range reaches buildBriefing: check_upgrade
// (nothing settled) and check_stack with the first release of the range as
// the running version (its advisories settle backports on other branches).
func rangeModes(docs []*v2Release) []struct {
	name    string
	docs    []*v2Release
	fixed   map[string]bool
	running []int
} {
	fixed := map[string]bool{}
	for _, c := range docs[0].Changes {
		if c.Kind == "security" {
			for _, id := range c.Advisories {
				fixed[normAdvisoryID(id)] = true
			}
		}
	}
	return []struct {
		name    string
		docs    []*v2Release
		fixed   map[string]bool
		running []int
	}{
		{"check_upgrade", docs, nil, nil},
		{"check_stack from " + docs[0].Version, docs[1:], fixed, version.NormalizeVersion(docs[0].Version)},
	}
}

func TestRangesKeepEverything100Answered(t *testing.T) {
	liftCaps(t)
	for _, r := range loadRanges(t) {
		for _, m := range rangeModes(r.docs) {
			d := compareWith100(m.docs, m.fixed, m.running)
			byField := map[string]int{}
			for f, v := range d.lost {
				byField[f] = len(v)
			}
			t.Logf("%s, %s: entries %d → %d (grouped %d, split %d); lost texts %d, lost ids/grades %d, lost also_in %d, disjoint merges %d, summary diffs %d; lost by field %v",
				r.name, m.name, d.entries100, d.entries110, d.grouped, d.split, d.lostTexts(), len(d.lost["advisories"])+len(d.lost["severity"]),
				len(d.lostAlsoIn), len(d.disjoint), len(d.summary), byField)
			if d.losses() == 0 {
				continue
			}
			var b strings.Builder
			show := func(what string, xs []string) {
				for i, x := range xs {
					if i == 5 {
						fmt.Fprintf(&b, "\n  %s: … %d more", what, len(xs)-5)
						break
					}
					fmt.Fprintf(&b, "\n  %s: %s", what, x)
				}
			}
			show("summary", d.summary)
			show("missing entry", d.missing)
			for _, f := range slices.Sorted(maps.Keys(d.lost)) {
				show("lost "+f, d.lost[f])
			}
			show("lost also_in", d.lostAlsoIn)
			show("disjoint merge", d.disjoint)
			t.Errorf("%s, %s: 1.1.0 lost what 1.0.0 answered:%s", r.name, m.name, b.String())
		}
	}
}

// TestCutRangeFixtures rewrites testdata/ranges from whole /v2/upgrade answers:
//
//	CUT_RANGE_FIXTURES=/tmp/ranges go test -run TestCutRangeFixtures .
//
// It keeps every release (counts and warnings are what (i) compares) and, of
// the changes, the security changes that share an id with another security
// change citing a different id set, plus every change citing one of those
// same sets — what either version could group differently. Bodies, evidence
// and before_upgrade are dropped; the fields a briefing entry carries stay as
// the worker wrote them.
func TestCutRangeFixtures(t *testing.T) {
	dir := os.Getenv("CUT_RANGE_FIXTURES")
	if dir == "" {
		t.Skip("set CUT_RANGE_FIXTURES=/dir of whole /v2/upgrade answers to rewrite testdata/ranges")
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var up struct {
			Project  string           `json:"project"`
			From     string           `json:"from"`
			To       string           `json:"to"`
			Releases []map[string]any `json:"releases"`
		}
		if err := json.Unmarshal(raw, &up); err != nil || up.Project == "" {
			t.Fatalf("%s: not a /v2/upgrade answer (%v)", p, err)
		}
		sets := map[string]map[string]bool{} // recognised id → the whole id sets citing it
		ids := func(c map[string]any) []string {
			var out []string
			for _, v := range c["advisories"].([]any) {
				out = append(out, v.(string))
			}
			return out
		}
		for _, r := range up.Releases {
			for _, c := range r["changes"].([]any) {
				ch := c.(map[string]any)
				if ch["kind"] != "security" {
					continue
				}
				for _, id := range recognisedIDs(ids(ch)) {
					if sets[id] == nil {
						sets[id] = map[string]bool{}
					}
					sets[id][advisoryKey(ids(ch))] = true
				}
			}
		}
		keep := map[string]bool{} // whole id sets kept
		for _, s := range sets {
			if len(s) > 1 {
				for k := range s {
					keep[k] = true
				}
			}
		}
		var b strings.Builder
		head, _ := json.Marshal(map[string]string{"project": up.Project, "from": up.From, "to": up.To})
		b.WriteString(strings.TrimSuffix(string(head), "}") + `,"releases":[`)
		kept := 0
		for i, r := range up.Releases {
			var cs []map[string]any
			for _, c := range r["changes"].([]any) {
				ch := c.(map[string]any)
				if ch["kind"] != "security" || !keep[advisoryKey(ids(ch))] {
					continue
				}
				out := map[string]any{}
				for _, f := range []string{"id", "kind", "title", "severity", "advisories", "affects", "action_required", "also_required",
					"no_downgrade", "removed_in", "workaround", "regression_since", "security_unclear", "silent_failure", "later"} {
					if v, ok := ch[f]; ok {
						out[f] = v
					}
				}
				cs = append(cs, out)
			}
			kept += len(cs)
			rel := map[string]any{"changes": cs, "later": []any{},
				"before_upgrade": map[string]any{"prerequisites": []any{}, "known_issues": []any{}, "upgrade_guide": []any{}}}
			if cs == nil {
				rel["changes"] = []any{}
			}
			for _, f := range []string{"project", "version", "released_at", "url", "counts", "warnings", "processing"} {
				if v, ok := r[f]; ok {
					rel[f] = v
				}
			}
			line, _ := json.Marshal(rel)
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n")
			b.Write(line)
		}
		b.WriteString("\n]}\n")
		out := filepath.Join("testdata", "ranges", up.Project+".json")
		if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d releases, %d security changes kept → %s (%d bytes)", up.Project, len(up.Releases), kept, out, b.Len())
	}
}
