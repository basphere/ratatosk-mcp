package main

// Fitting a briefing answer (check_stack, check_upgrade with detail "brief")
// to briefCharBudget, hint included.
//
// Why: a client hands a tool answer over 50,000 characters to the model as a
// file, and the briefing exists to be read. The first 1.2.0 build shrank only
// the change lists and left everything else as it was: cilium v1.16.18 and
// dapr v1.15.13 answered in 59,638 characters with no change listed at all,
// twenty components in 262,198 (independent review, 2026-11-20). The spec's
// section "마" (app repo docs/design/mcp-answer-shape.md) is what this does:
//
//  1. The parts that are not change lists shrink first, and only when the
//     answer is over: before-upgrade bodies are cut to 400 characters
//     (body_truncated), then each release in releases keeps its version and
//     date. later_in_range carries only problems (buildBriefing), always.
//  2. Then the change lists, layer by layer across every component
//     (giveWayTier): the lowest layer of every component goes down to ids and
//     titles — entries become lines, then lines lose affects — before the
//     next layer anywhere gives up anything; only then are lines dropped,
//     lowest layer first, and counted in changes_unlisted. Each component keeps
//     the first five lines of its highest layer whatever happens.
//  3. Still over (many components): from the last component back, a component
//     keeps only project, running_version, target_version, summary and a note
//     saying to ask for it alone; past that, not even the summary.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// fitItem is one decision change of a briefing while the answer is fitted.
type fitItem struct {
	c         briefChange
	wasEntry  bool // in changes as built
	demoted   bool // moved from changes to changes_more
	bare      bool // a line without affects
	dropped   bool // in neither list: counted in changes_unlisted
	protected bool // one of the five lines of the component's highest layer: never dropped
	// sizes of the change as an entry of changes, as a line, as a line
	// without affects, comma included
	sEntry, sLine, sBare int
}

func (it *fitItem) size() int {
	switch {
	case it.dropped:
		return 0
	case it.wasEntry && !it.demoted:
		return it.sEntry
	case it.bare:
		return it.sBare
	}
	return it.sLine
}

// giveWayTier is the layer a change keeps its place in when an answer is over
// the budget: lower gives way first. Kept to the end (the main session's
// decision, 2026-11-20, after the first fitting answered envoy's 103 security
// changes, critical advisories among them, with a count while 15 breaking lines
// stayed):
//
//	5  security critical or high
//	4  breaking that says what to do (action_required, or a stated or inferred no_downgrade)
//	3  other security (medium, low, no grade found)
//	2  other breaking
//	1  deprecations
//	0  important fixes
func giveWayTier(c briefChange) int {
	switch c.Kind {
	case "important_fixes":
		return 0
	case "deprecations":
		return 1
	case "breaking":
		if c.ActionRequired != "" || (c.NoDowngrade != nil && (c.NoDowngrade.Status == "stated" || c.NoDowngrade.Status == "inferred")) {
			return 4
		}
		return 2
	case "security":
		if gradeRank(c.Severity) >= 3 {
			return 5
		}
		return 3
	}
	return 0
}

// protectedLines is how many lines of its highest layer a component keeps
// whatever the budget (spec "마": a component's severest entries must not
// give way to another component's lower layers).
const protectedLines = 5

// fitComp is one briefing's change lists while the answer is fitted.
type fitComp struct {
	b       *briefing
	omitted map[string]int // changes_omitted as built
	shown   []*fitItem     // briefing order
	layers  [6][]*fitItem  // per layer, the end of the briefing's order first
}

func runesOf(v any) int {
	b, _ := json.Marshal(v)
	return utf8.RuneCount(b)
}

func newFitComp(b *briefing) *fitComp {
	f := &fitComp{b: b, omitted: maps.Clone(b.ChangesOmitted)}
	add := func(c briefChange, entry bool) {
		it := &fitItem{c: c, wasEntry: entry}
		if entry {
			it.sEntry = runesOf(c) + 1
		}
		l := lineOf(c)
		it.sLine = runesOf(l) + 1
		l.Affects = nil
		it.sBare = runesOf(l) + 1
		f.shown = append(f.shown, it)
	}
	for _, c := range b.Changes {
		add(c, true)
	}
	for _, c := range b.more {
		add(c, false)
	}
	sort.SliceStable(f.shown, func(i, j int) bool { return briefLess(f.shown[i].c, f.shown[j].c) })
	top, kept := -1, 0
	for _, it := range f.shown {
		top = max(top, giveWayTier(it.c))
	}
	for _, it := range f.shown {
		if giveWayTier(it.c) == top && kept < protectedLines {
			it.protected = true
			kept++
		}
	}
	for i := len(f.shown) - 1; i >= 0; i-- {
		it := f.shown[i]
		t := giveWayTier(it.c)
		f.layers[t] = append(f.layers[t], it)
	}
	return f
}

func (f *fitComp) size() int {
	n := 0
	for _, it := range f.shown {
		n += it.size()
	}
	return n
}

func (f *fitComp) reset() {
	for _, it := range f.shown {
		it.demoted, it.bare, it.dropped = false, false, false
	}
}

// write puts the items' current form into the briefing.
func (f *fitComp) write() {
	var changes []briefChange
	var lines []moreLine
	omitted := maps.Clone(f.omitted)
	unlisted := map[string]int{}
	for _, it := range f.shown {
		if it.demoted {
			if omitted == nil {
				omitted = map[string]int{}
			}
			omitted[it.c.Kind]++
		}
		switch {
		case it.wasEntry && !it.demoted:
			changes = append(changes, it.c)
		case it.dropped:
			unlisted[it.c.Kind]++
		default:
			l := lineOf(it.c)
			if it.bare {
				l.Affects = nil
			}
			lines = append(lines, l)
		}
	}
	if len(unlisted) == 0 {
		unlisted = nil
	}
	f.b.Changes, f.b.ChangesMore, f.b.ChangesOmitted, f.b.ChangesUnlisted = changes, lines, omitted, unlisted
}

// clear empties the briefing's change lists, to measure the rest of the answer.
func (f *fitComp) clear() {
	f.b.Changes, f.b.ChangesMore, f.b.ChangesOmitted, f.b.ChangesUnlisted = nil, nil, nil, nil
}

// fitStep is one step of the ladder.
type fitStep struct {
	it *fitItem
	do int // stepDemote, stepBare or stepDrop
}

const (
	stepDemote = iota // an entry of changes becomes a line of changes_more
	stepBare          // a line loses affects
	stepDrop          // a line goes, counted in changes_unlisted
)

func (st fitStep) apply() {
	switch st.do {
	case stepDemote:
		st.it.demoted = true
	case stepBare:
		st.it.bare = true
	case stepDrop:
		st.it.dropped = true
	}
}

// delta is what the step changes in the lists' length.
func (st fitStep) delta() int {
	switch st.do {
	case stepDemote:
		return st.it.sLine - st.it.sEntry
	case stepBare:
		return st.it.sBare - st.it.sLine
	}
	return -st.it.sBare
}

// ladder is the steps of every component together: per layer from the lowest,
// every component's entries become lines, then the lines lose affects; then,
// per layer from the lowest, lines are dropped (never a protected one). Inside
// a layer each component gives up the same share of its own items at a time,
// so a component with few of them is not emptied while one with many keeps
// most: taking turns item by item, coredns lost all 8 security lines of its
// layer while envoy kept 77 of 91 (six components, 2026-11-20).
func ladder(comps []*fitComp) []fitStep {
	var steps []fitStep
	turns := func(pick func(*fitComp) []*fitItem, do int) {
		type at struct {
			it    *fitItem
			share float64 // how far into its component's list this step is
			comp  int
		}
		var all []at
		for c, f := range comps {
			l := pick(f)
			for j, it := range l {
				all = append(all, at{it, float64(j+1) / float64(len(l)), c})
			}
		}
		sort.SliceStable(all, func(a, b int) bool {
			if all[a].share != all[b].share {
				return all[a].share < all[b].share
			}
			return all[a].comp < all[b].comp
		})
		for _, x := range all {
			steps = append(steps, fitStep{x.it, do})
		}
	}
	for t := 0; t < 6; t++ {
		turns(func(f *fitComp) []*fitItem {
			var out []*fitItem
			for _, it := range f.layers[t] {
				if it.wasEntry {
					out = append(out, it)
				}
			}
			return out
		}, stepDemote)
		turns(func(f *fitComp) []*fitItem { return f.layers[t] }, stepBare)
	}
	for t := 0; t < 6; t++ {
		turns(func(f *fitComp) []*fitItem {
			var out []*fitItem
			for _, it := range f.layers[t] {
				if !it.protected {
					out = append(out, it)
				}
			}
			return out
		}, stepDrop)
	}
	return steps
}

// fitUnit is one briefed component of an answer.
type fitUnit struct {
	name string
	comp *fitComp
	// fixed parts: cut the before-upgrade bodies, slim the releases, and
	// stub the component (1: summary only, 2: not even the summary).
	cutBodies    func()
	slimReleases func()
	stub         func(level int)
	stubLevel    int
}

func unitOf(name string, b *briefing, stub func(level int)) *fitUnit {
	return &fitUnit{
		name:         name,
		comp:         newFitComp(b),
		cutBodies:    func() { cutUpgradeBodies(b) },
		slimReleases: func() { slimReleases(b) },
		stub:         stub,
	}
}

// upgradeBodyChars is where a before-upgrade body is cut when the answer is
// over the budget (spec "마"); the whole body is one get_release(changes) away.
const upgradeBodyChars = 400

func cutUpgradeBodies(b *briefing) {
	if b.Upgrade == nil {
		return
	}
	for i := range *b.Upgrade {
		it := &(*b.Upgrade)[i]
		if it.Body == nil {
			continue
		}
		r := []rune(*it.Body)
		if len(r) <= upgradeBodyChars {
			continue
		}
		s := strings.TrimRight(string(r[:upgradeBodyChars]), " ") + "…"
		it.Body, it.BodyTruncated = &s, true
	}
}

func slimReleases(b *briefing) {
	if b.Releases == nil {
		return
	}
	for i := range *b.Releases {
		(*b.Releases)[i].slim = true
	}
}

// fitSlack covers what the list sizes leave out: the keys of the four lists
// and the counts in changes_omitted and changes_unlisted.
const fitSlack = 160

// maxHint bounds hint with what fitting adds to it (spec "마": hint was
// 2,837 characters on six components with a sentence for each).
const maxHint = 2000

// fitBriefings fits the answer render() makes (see the file comment). units
// are the briefed components in answer order; extra says what hint adds for
// them (the components with changes not listed, the ones cut to a summary).
func fitBriefings(units []*fitUnit, hint string, setHint func(string), render func() ([]byte, error), extra func([]*fitUnit) string) ([]byte, error) {
	setHint(hint)
	text, err := render()
	if err != nil || utf8.RuneCount(text) <= briefCharBudget {
		return text, err
	}
	for _, fixed := range []func(*fitUnit){func(u *fitUnit) { u.cutBodies() }, func(u *fitUnit) { u.slimReleases() }} {
		for _, u := range units {
			fixed(u)
		}
		if text, err = render(); err != nil || utf8.RuneCount(text) <= briefCharBudget {
			return text, err
		}
	}
	// The stub moves, from the last component back: summary only, then not
	// even that.
	var stubs []*fitUnit
	for level := 1; level <= 2; level++ {
		for i := len(units) - 1; i >= 0; i-- {
			stubs = append(stubs, units[i])
		}
	}
	for n := 0; ; n++ {
		if text, err = fitLists(units, hint, setHint, render, extra); err != nil || utf8.RuneCount(text) <= briefCharBudget || n == len(stubs) {
			return text, err
		}
		u := stubs[n]
		u.stubLevel++
		u.stub(u.stubLevel)
	}
}

// fitLists takes as few ladder steps as the budget needs over the components
// that are not stubbed, and sets hint for the outcome.
func fitLists(units []*fitUnit, hint string, setHint func(string), render func() ([]byte, error), extra func([]*fitUnit) string) ([]byte, error) {
	var comps []*fitComp
	for _, u := range units {
		u.comp.reset()
		if u.stubLevel == 0 {
			comps = append(comps, u.comp)
		} else {
			u.comp.clear()
		}
	}
	steps := ladder(comps)
	full := 0
	for _, f := range comps {
		full += f.size()
	}
	k := 0
	add := ""
	for round := 0; round < 3; round++ {
		setHint(boundHint(hint, add))
		for _, f := range comps {
			f.clear()
		}
		base, err := render()
		if err != nil {
			return nil, err
		}
		est := utf8.RuneCount(base) + full + fitSlack*len(comps)
		for _, f := range comps {
			f.reset()
		}
		k = 0
		for k < len(steps) && est > briefCharBudget {
			est += steps[k].delta()
			steps[k].apply()
			k++
		}
		for _, f := range comps {
			f.write()
		}
		next := extra(units)
		if next == add {
			break
		}
		add = next
	}
	for {
		setHint(boundHint(hint, add))
		text, err := render()
		if err != nil || utf8.RuneCount(text) <= briefCharBudget || k == len(steps) {
			return text, err
		}
		// The estimate fell short: one more step, and measure again.
		steps[k].apply()
		k++
		for _, f := range comps {
			f.write()
		}
		add = extra(units)
	}
}

// boundHint joins hint and what fitting adds, cut so the whole stays within
// maxHint.
func boundHint(hint, add string) string {
	s := hint + add
	if r := []rune(s); len(r) > maxHint {
		s = strings.TrimRight(string(r[:maxHint-1]), " ") + "…"
	}
	return s
}

// namesWithin joins component names so the sentence they go into stays within
// room characters: past it, the rest is counted ("+3 more").
func namesWithin(names []string, room int) string {
	s := strings.Join(names, ", ")
	for n := len(names) - 1; n > 0 && utf8.RuneCountInString(s) > room; n-- {
		s = strings.Join(names[:n], ", ") + fmt.Sprintf(" (+%d more)", len(names)-n)
	}
	return s
}

// stackExtra is what check_stack's hint says after fitting: one sentence
// naming the components with changes not listed, one naming those cut to a
// summary, one on how to list them. The counts are in each component's
// changes_unlisted; a sentence per component made hint 2,837 characters.
func stackExtra(units []*fitUnit) string {
	var unlisted, stubbed []string
	for _, u := range units {
		switch {
		case u.stubLevel > 0:
			stubbed = append(stubbed, u.name)
		case u.comp.b.ChangesUnlisted != nil:
			unlisted = append(unlisted, u.name)
		}
	}
	if len(unlisted) == 0 && len(stubbed) == 0 {
		return ""
	}
	room := maxHint - utf8.RuneCountInString(stackHint) - 330
	var s string
	if len(unlisted) > 0 {
		s += " Components with changes not listed here (changes_unlisted counts them): " + namesWithin(unlisted, room/2) + "."
	}
	if len(stubbed) > 0 {
		s += " Components given as a summary only, at the size limit: " + namesWithin(stubbed, room/2) + "."
	}
	return s + " To list them, call check_stack with one component alone and kinds naming one kind per call; " +
		"if one kind is still too many, narrow its range with target_version or same_minor."
}

// upgradeExtra is the same for check_upgrade.
func upgradeExtra(units []*fitUnit) string {
	u := units[0]
	switch {
	case u.stubLevel > 0:
		return " The change lists were left out at the size limit (summary only): narrow the range with to or " +
			"same_minor, or read the releases with get_release(kinds or changes)."
	case u.comp.b.ChangesUnlisted != nil:
		return " Changes not listed here are counted in changes_unlisted. To list them, call check_upgrade again with " +
			"kinds naming one kind per call; if one kind is still too many, narrow the range with to or same_minor."
	}
	return ""
}

// stubNote is the note of a component cut to a summary.
const stubNote = "summary only: this answer is at its size limit, so the lists of this component were left out — call check_stack with it alone"

// joinNotes joins the non-empty notes with "; ".
func joinNotes(parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), "; ")
}
