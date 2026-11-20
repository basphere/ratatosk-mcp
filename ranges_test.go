package main

// The 1.x regression harness: on real /v2/upgrade ranges, the briefing is the
// 1.0.0 briefing plus summary.distinct_advisories (1.1.0) and what 1.2.0 adds
// beside the 1.0.0 fields (changes_more, changes_unlisted, later_in_range,
// excluded_on_branch, and before_upgrade in its 1.2.0 form), and nothing else.
//
// Why it exists: the rule for 1.x is additive only — a 1.0.0 client must
// never get a silently wrong or poorer answer. A 1.1.0 draft that also grouped
// security changes by shared advisory ids passed its hand-written tests and,
// on seven live ranges, dropped 74 field texts and 5 also_in relations that
// 1.0.0 returned (independent review, 2026-11-18); the owner took that
// grouping out of 1.1.0 the same day. Hand-written cases test what the author
// thought of; this runs both versions over what the worker actually wrote.
//
// The assertion: for every range, in check_upgrade and check_stack modes,
// with the production caps and with the caps lifted, buildBriefing's result
// with summary.distinct_advisories zeroed is reflect.DeepEqual to
// buildBriefing100's (briefing100_test.go, the 1.0.0 code frozen), and the
// settled counts are equal: same entries in the same order, same also_in,
// advisories, changes_omitted, counts, warnings and releases.
//
// Where the ranges come from: testdata/ranges/*.json are live /v2/upgrade
// answers cut down by TestCutRangeFixtures — the security changes whose ids
// another security change in the range also cites, every release with its
// counts and warnings, and every model-written text replaced by a short token
// (equal texts get equal tokens, so every comparison the code makes between
// two texts comes out as it did on the live data). RANGE_FIXTURES=/dir adds
// every *.json in that directory, whole answers saved as they came, for a full
// run before a release:
//
//	curl -s 'https://ratatosk.io/v2/upgrade/envoy?from=v1.33.0' > /tmp/ranges/envoy.json
//	RANGE_FIXTURES=/tmp/ranges go test -run TestRangesAnswerAs100 -v .

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
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

// withCaps sets every per-kind cap to n (0: the production caps) until the
// returned function is called.
func withCaps(n int) (restore func()) {
	saved := maps.Clone(kindCaps)
	if n > 0 {
		for k := range kindCaps {
			kindCaps[k] = n
		}
	}
	return func() { kindCaps = saved }
}

// diffFrom100 compares buildBriefing with the frozen 1.0.0 one on one input
// and says where they differ, "" when they do not (distinct_advisories and the
// 1.2.0 fields aside). before_upgrade is set aside on both sides: 1.2.0
// answers it in a new form (upgradeItem), and the fixtures carry none.
func diffFrom100(docs []*v2Release, fixed map[string]bool, running []int) string {
	old, oldSettled := buildBriefing100(docs, fixed, running)
	cur, curSettled := buildBriefing(docs, fixed, running)
	sum := *cur.Summary
	sum.DistinctAdvisories = distinctAdvisories{}
	cur.Summary = &sum
	old.BeforeUpgrade = nil
	// changes_omitted counts the security changes folded into an entry of
	// their own release since the independent review of 2026-11-20 (1.0.0 left
	// them without a trace); taken off here, the rest must be 1.0.0's.
	if cur.folded > 0 {
		cur.ChangesOmitted = maps.Clone(cur.ChangesOmitted)
		if cur.ChangesOmitted["security"] -= cur.folded; cur.ChangesOmitted["security"] == 0 {
			delete(cur.ChangesOmitted, "security")
		}
		if len(cur.ChangesOmitted) == 0 {
			cur.ChangesOmitted = nil
		}
	}
	cur.Upgrade, cur.ChangesMore, cur.ChangesUnlisted, cur.LaterInRange, cur.ExcludedOnBranch, cur.more, cur.folded = nil, nil, nil, nil, nil, nil, 0
	if oldSettled != curSettled {
		return fmt.Sprintf("settled %d, 1.0.0 %d", curSettled, oldSettled)
	}
	if reflect.DeepEqual(old, cur) {
		return ""
	}
	if len(old.Changes) != len(cur.Changes) {
		return fmt.Sprintf("%d entries, 1.0.0 %d", len(cur.Changes), len(old.Changes))
	}
	for i := range old.Changes {
		if !reflect.DeepEqual(old.Changes[i], cur.Changes[i]) {
			a, _ := json.Marshal(old.Changes[i])
			b, _ := json.Marshal(cur.Changes[i])
			return fmt.Sprintf("entry %d:\n  1.0.0 %.400s\n  now   %.400s", i, a, b)
		}
	}
	a, _ := json.Marshal(old)
	b, _ := json.Marshal(cur)
	return fmt.Sprintf("briefing:\n  1.0.0 %.600s\n  now   %.600s", a, b)
}

// assertSameAs100 fails the test unless buildBriefing answers docs as 1.0.0
// did, distinct_advisories aside, with the production caps and with none.
func assertSameAs100(t *testing.T, docs []*v2Release, fixed map[string]bool, running []int) {
	t.Helper()
	for _, caps := range []int{0, 1 << 30} {
		restore := withCaps(caps)
		d := diffFrom100(docs, fixed, running)
		restore()
		if d != "" {
			t.Errorf("caps %d: the briefing differs from 1.0.0's: %s", caps, d)
		}
	}
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

func TestRangesAnswerAs100PlusDistinctAdvisories(t *testing.T) {
	for _, r := range loadRanges(t) {
		for _, m := range rangeModes(r.docs) {
			b, settled := buildBriefing(m.docs, m.fixed, m.running)
			alsoIn := 0
			for _, c := range b.Changes {
				alsoIn += len(c.AlsoIn)
			}
			t.Logf("%s, %s: %d entries, %d also_in, %d settled, omitted %v, distinct_advisories %+v",
				r.name, m.name, len(b.Changes), alsoIn, settled, b.ChangesOmitted, b.Summary.DistinctAdvisories)
			t.Run(r.name+" "+m.name, func(t *testing.T) { assertSameAs100(t, m.docs, m.fixed, m.running) })
		}
	}
}

// TestCutRangeFixtures rewrites testdata/ranges from whole /v2/upgrade answers:
//
//	CUT_RANGE_FIXTURES=/tmp/ranges go test -run TestCutRangeFixtures .
//
// It keeps every release with its version, counts and warnings, and of the
// changes the security changes whose whole id set another security change in
// the range also cites, or that share an id with one citing a different set:
// the only ones the briefing folds, settles or links. Titles, bodies,
// evidence, before_upgrade, later, the release date and URL, and zero counts
// are dropped. Every other text (affects, workaround, regression_since,
// no_downgrade, removed_in, also_required, action_required) becomes a short
// token, the same token for the same text in a range (a workaround or
// regression note as cut to 160 characters, which is how the code compares
// them), so presence and every equality the code tests survive while the
// worker's wording stays out of the repository.
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
		ids := func(c map[string]any) []string {
			var out []string
			for _, v := range c["advisories"].([]any) {
				out = append(out, v.(string))
			}
			return out
		}
		sets := map[string]map[string]bool{} // recognised id → the whole id sets citing it
		whole := map[string]int{}            // whole id set → security changes citing it
		for _, r := range up.Releases {
			for _, c := range r["changes"].([]any) {
				ch := c.(map[string]any)
				if ch["kind"] != "security" {
					continue
				}
				k := advisoryKey(ids(ch))
				whole[k]++
				for _, id := range recognisedIDs(ids(ch)) {
					if sets[id] == nil {
						sets[id] = map[string]bool{}
					}
					sets[id][k] = true
				}
			}
		}
		keep := func(ch map[string]any) bool {
			k := advisoryKey(ids(ch))
			if k == "" {
				return false
			}
			if whole[k] > 1 {
				return true
			}
			for _, id := range recognisedIDs(ids(ch)) {
				if len(sets[id]) > 1 {
					return true
				}
			}
			return false
		}
		tokens := map[string]string{}
		token := func(s string) string {
			if s == "" {
				return ""
			}
			if _, ok := tokens[s]; !ok {
				tokens[s] = fmt.Sprintf("t%d", len(tokens)+1)
			}
			return tokens[s]
		}
		var b strings.Builder
		head, _ := json.Marshal(map[string]string{"project": up.Project, "from": up.From, "to": up.To})
		b.WriteString(strings.TrimSuffix(string(head), "}") + `,"releases":[`)
		kept := 0
		for i, r := range up.Releases {
			cs := []map[string]any{}
			for _, c := range r["changes"].([]any) {
				ch := c.(map[string]any)
				if ch["kind"] != "security" || !keep(ch) {
					continue
				}
				out := map[string]any{"id": ch["id"], "kind": ch["kind"], "advisories": ch["advisories"]}
				for _, f := range []string{"severity", "security_unclear", "silent_failure"} {
					if v, ok := ch[f]; ok && v != nil && v != false {
						out[f] = v
					}
				}
				for _, f := range []string{"also_required", "action_required"} {
					if s, ok := ch[f].(string); ok && s != "" {
						out[f] = token(s)
					}
				}
				for _, f := range []string{"affects", "workaround", "regression_since", "no_downgrade", "removed_in"} {
					a, ok := ch[f].(map[string]any)
					if !ok {
						continue
					}
					text, _ := a["text"].(string)
					attr := map[string]any{"text": token(briefText(text)), "status": a["status"]}
					if text != "" && f != "workaround" && f != "regression_since" {
						attr["text"] = token(text) // compared whole, not cut to 160
					}
					out[f] = attr
				}
				cs = append(cs, out)
			}
			kept += len(cs)
			rel := map[string]any{"version": r["version"], "changes": cs}
			if r["processing"] == true {
				rel["processing"] = true
			}
			for _, f := range []string{"counts", "warnings"} { // a missing count sums as a 0
				m := map[string]any{}
				for k, v := range r[f].(map[string]any) {
					if v != float64(0) {
						m[k] = v
					}
				}
				rel[f] = m
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

// TestRangesLeaveNoSecurityChangeUnaccounted (1.2.0): on the live ranges,
// every security change is an entry, a line of changes_more, a release named in
// an entry's also_in, or settled — never gone without a trace, as three of
// coredns v1.14.7 → v1.15.0's eight were (2026-11-20). And the caps move
// entries to changes_more without losing one: listed with the production
// caps or with none, the same ids come back, and each kind has one line per
// omitted change (security: plus the folded siblings).
func TestRangesLeaveNoSecurityChangeUnaccounted(t *testing.T) {
	for _, r := range loadRanges(t) {
		for _, m := range rangeModes(r.docs) {
			t.Run(r.name+" "+m.name, func(t *testing.T) {
				restore := withCaps(1 << 30)
				all, settled := buildBriefing(m.docs, m.fixed, m.running)
				restore()
				capped, _ := buildBriefing(m.docs, m.fixed, m.running)

				listed := map[string]bool{}
				for _, c := range all.Changes {
					listed[c.ID] = true
				}
				folded := map[string]int{}
				for _, l := range all.ChangesMore {
					listed[l.ID] = true
					folded[l.Kind]++
				}
				seenSettled := 0
				for _, d := range m.docs {
					onRunning := false
					if k := version.NormalizeVersion(d.Version); k != nil && m.running != nil {
						onRunning = sameBranch(k, m.running)
					}
					for _, c := range d.Changes {
						if c.Kind != "security" || listed[c.ID] {
							continue
						}
						key := advisoryKey(c.Advisories)
						covered := key != ""
						for _, id := range strings.Split(key, ",") {
							covered = covered && m.fixed[id]
						}
						if covered && !onRunning {
							seenSettled++
							continue
						}
						backport := false
						for _, e := range all.Changes {
							if e.advKey == key && slices.Contains(e.AlsoIn, d.Version) {
								backport = true
								break
							}
						}
						if !backport {
							t.Errorf("%s %s is in no list, no also_in and not settled", d.Version, c.ID)
						}
					}
				}
				if seenSettled != settled {
					t.Errorf("settled %d, counted %d", settled, seenSettled)
				}

				ids := func(b briefing) []string {
					var out []string
					for _, c := range b.Changes {
						out = append(out, c.ID)
					}
					for _, l := range b.ChangesMore {
						out = append(out, l.ID)
					}
					sort.Strings(out)
					return out
				}
				if a, b := ids(all), ids(capped); !slices.Equal(a, b) {
					t.Errorf("the caps lost or added ids: %d without caps, %d with", len(a), len(b))
				}
				lines := map[string]int{}
				for _, l := range capped.ChangesMore {
					lines[l.Kind]++
				}
				for _, k := range decisionKinds {
					if lines[k] != capped.ChangesOmitted[k] {
						t.Errorf("%s: %d lines for %d omitted (%d folded)", k, lines[k], capped.ChangesOmitted[k], folded[k])
					}
				}
			})
		}
	}
}
