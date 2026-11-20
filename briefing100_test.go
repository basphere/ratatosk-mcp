package main

// buildBriefing as released in 1.0.0 (ff60e46), kept as the reference the
// 1.1.0 regression harness (ranges_test.go) compares against.
//
// Why a frozen copy and not two binaries: the rule for 1.x is that a 1.0.0
// client never gets a poorer answer, and the harness has to see both answers
// change by change, which a copy in the same package can do with no build
// step, no process and no network. Only the four functions whose 1.0.0 body
// differs from today's are copied; the helpers they call (attrOf, briefText,
// branchOf, advisoryKey, carriesAStep, releaseline.DescCmp, kindRank, releaseline.SameBranch, strOf)
// are unchanged since 1.0.0 — if one of them changes, copy its 1.0.0 body
// here under a 100 name first, or the reference moves with the code under
// test. Do not "fix" anything in this file: it is a record, not a briefing.

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

func gradeRank100(raw json.RawMessage) int {
	var g string
	_ = json.Unmarshal(raw, &g)
	switch strings.ToLower(g) {
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

func carriesItsOwn100(c release.Change, kept briefChange) bool {
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

func raiseSeverity100(bc *briefChange, sev *string) {
	if sev == nil || *sev == "" {
		return
	}
	if raw, _ := json.Marshal(*sev); gradeRank100(raw) > gradeRank100(bc.Severity) {
		bc.Severity = raw
	}
}

func buildBriefing100(docs []*release.Doc, alreadyFixed map[string]bool, runningKey []int) (briefing, int) {
	b := briefing{
		Summary:  &stackSummary{Counts: map[string]int{}, Warnings: map[string]int{"critical": 0, "no_downgrade": 0}},
		Releases: &[]rangeRelease{},
	}
	settled := 0
	firstByAdv := map[string]int{}
	advOnBranch := map[string]bool{}
	for _, d := range docs {
		inRelease := map[string]int{}
		var touched []string
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
		rr := rangeRelease{Version: d.Version, ReleasedAt: d.ReleasedAt, URL: d.URL, Counts: d.Counts, Processing: d.Processing}
		if d.Warnings["critical"] > 0 || d.Warnings["no_downgrade"] > 0 {
			rr.Warnings = d.Warnings
		}
		*b.Releases = append(*b.Releases, rr)
		for _, sec := range []struct {
			name  string
			items []release.Item
		}{{"prerequisites", d.BeforeUpgrade.Prerequisites}, {"known_issues", d.BeforeUpgrade.KnownIssues}, {"upgrade_guide", d.BeforeUpgrade.UpgradeGuide}} {
			for _, it := range sec.items {
				b.BeforeUpgrade = append(b.BeforeUpgrade, briefItem{Version: d.Version, Section: sec.name, ID: it.ID, Title: it.Title, Body: briefText(strOf(it.Body))})
			}
		}
		for _, c := range d.Changes {
			if !slices.Contains(decisionKinds, c.Kind) {
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
						continue
					}
					branch := key + "|" + branchOf(d.Version)
					if i, ok := inRelease[key]; ok {
						if !carriesItsOwn100(c, b.Changes[i]) {
							raiseSeverity100(&b.Changes[i], c.Severity)
							continue
						}
					} else {
						touched = append(touched, branch)
						if i, ok := firstByAdv[key]; ok && !advOnBranch[branch] {
							b.Changes[i].AlsoIn = append(b.Changes[i].AlsoIn, d.Version)
							raiseSeverity100(&b.Changes[i], c.Severity)
							inRelease[key] = i
							continue
						}
						inRelease[key] = len(b.Changes)
					}
				}
			}
			bc := briefChange{
				Version: d.Version, ID: c.ID, Kind: c.Kind, Title: c.Title, Body: briefText(strOf(c.Body)),
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
			if _, ok := firstByAdv[key]; key != "" && !ok {
				firstByAdv[key] = len(b.Changes)
			}
			b.Changes = append(b.Changes, bc)
		}
		for _, t := range touched {
			advOnBranch[t] = true
		}
	}
	sort.SliceStable(b.Changes, func(i, j int) bool {
		ci, cj := b.Changes[i], b.Changes[j]
		if ki, kj := kindRank(ci.Kind), kindRank(cj.Kind); ki != kj {
			return ki < kj
		}
		if gi, gj := gradeRank100(ci.Severity), gradeRank100(cj.Severity); gi != gj {
			return gi > gj
		}
		if si, sj := carriesAStep(ci), carriesAStep(cj); si != sj {
			return si
		}
		return releaseline.DescCmp(ci.Version, cj.Version) > 0
	})
	kept := b.Changes[:0]
	shown := map[string]int{}
	for _, c := range b.Changes {
		if shown[c.Kind] >= kindCaps[c.Kind] {
			if b.ChangesOmitted == nil {
				b.ChangesOmitted = map[string]int{}
			}
			b.ChangesOmitted[c.Kind]++
			continue
		}
		shown[c.Kind]++
		kept = append(kept, c)
	}
	b.Changes = kept
	return b, settled
}
