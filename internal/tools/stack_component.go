package tools

// One component of a check_stack call: resolve the project, read its
// releases, cut the range in this process and build the report.
//
// What goes upstream for a component is decided from its project alone,
// before anything reads the running version (see the package comment of
// check_stack and fetch): the order of the steps below is that property.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

// stackRun is one check_stack call while its components are checked: what the
// call asked for, what the roster says, and what the components share — the
// reports so far, and the two budgets of a call (releases read, bytes of
// release documents in a full answer).
type stackRun struct {
	h       *handlers
	slugs   map[string]bool
	aliasOf map[string]string
	// times: how often each component was listed in the call.
	times map[stackComponent]int
	full  bool
	lang  string
	kinds map[string]bool

	reports   []componentReport
	cannot    []uncheckable
	spent     int
	fullBytes int
}

// check adds comp's report to the run. An error fails the whole call; a
// component that cannot be checked is a report with a note and an entry in
// cannot, not an error.
func (r *stackRun) check(ctx context.Context, comp stackComponent) error {
	rep := componentReport{Project: comp.Project, RunningVersion: comp.Version, VersionSource: comp.VersionSource}
	var notes []string
	if n := r.times[comp]; n > 1 {
		notes = append(notes, fmt.Sprintf("listed %d times in this call; checked once", n))
	}
	if comp.unreadKeys != "" {
		notes = append(notes, fmt.Sprintf("key(s) not read: %s — a component takes project, version, target_version, same_minor and version_source", comp.unreadKeys))
	}
	skip := func(reason string) {
		notes = append(notes, reason)
		rep.Note = strings.Join(notes, "; ")
		r.reports = append(r.reports, rep)
		r.cannot = append(r.cannot, uncheckable{Project: rep.Project, Version: comp.Version, Reason: reason})
	}
	slug := comp.Project
	if !r.slugs[slug] {
		if s, ok := r.aliasOf[strings.ToLower(slug)]; ok {
			notes = append(notes, fmt.Sprintf("%q is an image alias of project %q; checked as %q", slug, s, s))
			slug = s
			rep.Project = s
		} else {
			f := false
			rep.Tracked = &f
			skip(untrackedReason)
			return nil
		}
	}
	// What goes upstream is decided here, from the project alone: its
	// release list and, within the call's budget, its newest releases.
	// Every check below that reads the running version comes after, so a
	// version that cannot be parsed, sits on a line with no releases or
	// lies above every release on record sends the same requests as any
	// other. Refusing the first two before the reads let the number of
	// requests tell the server which case it was (1, 2 or 8 on the test
	// history; independent review, 2026-11-21).
	rows, listErr := r.h.api.ProjectReleases(ctx, slug)
	var docs map[string]*release.Doc
	var docsErr error
	overBudget := false
	if listErr == nil && len(rows) > 0 {
		set := fetchSet(rows)
		if r.spent+len(set) > fetchBudget {
			overBudget = true
		} else {
			r.spent += len(set)
			docs, docsErr = r.h.fetchDocs(ctx, set, r.lang)
		}
	}
	if version.NormalizeVersion(comp.Version) == nil {
		skip("running version could not be parsed")
		return nil
	}
	if listErr != nil {
		skip(fetchReason(ctx, listErr))
		return nil
	}
	if len(rows) == 0 {
		t := true
		rep.Tracked = &t
		rep.Releases = &[]briefing.RangeRelease{}
		notes = append(notes, "tracked by ratatosk; no analyzed release on record yet")
		rep.Note = strings.Join(notes, "; ")
		r.reports = append(r.reports, rep)
		return nil
	}
	// An image tag can carry a build suffix after the version it was built
	// from: dropped when the tag as passed is no release on record and the
	// version without it is, and the answer says what was read.
	running := comp.Version
	if v, ok := releaseline.ReadBuildTail(rows, running); ok {
		running = v
		rep.VersionReadAs = v
	}
	onLine := releaseline.RowsOnLine(rows, running)
	// A version whose line does not exist here is almost always a dropped
	// prefix, not a real gap. Agents read versions off image tags, and an
	// image tag rarely carries the release-line prefix: knative publishes
	// "knative-v1.12.0" while its images say "v1.12.0" (hub question,
	// 2026-08-10). Silence would look like "nothing to do".
	if len(onLine) == 0 {
		lines := releaseline.LinesPresent(rows)
		line := version.Line(running)
		if version.NormalizeVersion(line) != nil {
			// What was read as the line is itself a version: something was
			// added after the version, not dropped before it.
			skip(fmt.Sprintf(
				"no releases on record for line %s — this project publishes %s. The version reads as %s followed by %q, which is not a release line here: pass the version as the project publishes it, without what the image tag adds after it (e.g. %s)",
				lineLabel(line), lineList(lines), line, truncate(strings.TrimPrefix(strings.TrimSpace(running), line), 60), exampleTag(rows, lines[0])))
			return nil
		}
		skip(fmt.Sprintf(
			"no releases on record for line %s — this project publishes %s. If the version came from an image tag, the release-line prefix was probably dropped: pass the tag as the project publishes it (e.g. %s)",
			lineLabel(line), lineList(lines), exampleTag(rows, lines[0])))
		return nil
	}
	currentKey := version.NormalizeVersion(running)
	// The call's same_minor against what the component says for itself. A
	// target_version that cannot be used falls back to the call's same_minor,
	// not to the whole release line: decided on the key alone, a target at or
	// below the running version answered the whole line under a note that the
	// target "was used" (independent review, 2026-11-22).
	sameMinor := comp.SameMinor
	targetKey, targetNote := resolveTarget(currentKey, comp.TargetVersion, comp.callSameMinor == callSameMinorOrTarget)
	if targetNote != "" {
		notes = append(notes, targetNote)
	}
	switch comp.callSameMinor {
	case callSameMinorOff:
		notes = append(notes, "the call's same_minor was not applied here: this component sets same_minor:false")
	case callSameMinorOrTarget:
		if targetKey != nil {
			notes = append(notes, "the call's same_minor was not applied here: this component names target_version, which was used")
		} else {
			sameMinor = true
		}
	}
	if overBudget {
		skip("not checked: this call already read as many releases as one call may — call check_stack again with the remaining components")
		return nil
	}
	if docsErr != nil {
		skip(fetchReason(ctx, docsErr))
		return nil
	}

	target := ""
	if targetKey != nil {
		target = comp.TargetVersion
	}
	var inRange []release.Row
	var resolved string
	if sameMinor {
		if t := releaseline.SameMinorTarget(rows, running); t != "" {
			target = t
			inRange, resolved = releaseline.UpgradeRange(rows, running, t)
		} else {
			resolved = releaseline.TagOf(onLine, running)
			notes = append(notes, sameMinorNote(running, releaseline.NewestOnLine(rows, running)))
		}
	} else {
		inRange, resolved = releaseline.UpgradeRange(rows, running, target)
	}
	rep.TargetVersion = resolved

	var oldest, newest []int
	oldestTag, newestTag := "", ""
	for _, r := range onLine {
		k := version.NormalizeVersion(r.Version)
		if oldest == nil || version.Compare(k, oldest) < 0 {
			oldest, oldestTag = k, r.Version
		}
		if newest == nil || version.Compare(k, newest) > 0 {
			newest, newestTag = k, r.Version
		}
	}
	if n := coverageNote(running, currentKey, oldest, oldestTag); n != "" {
		notes = append(notes, n)
	}
	if n := confessionNote(comp.VersionSource); n != "" {
		notes = append(notes, n)
	}
	if n := newerNote(running, currentKey, newest, newestTag); n != "" {
		rep.TargetVersion = "" // the range end resolved above sits below the running version
		skip(n)
		return nil
	}
	// A target above every analyzed release: the range stops at the newest
	// analyzed one and the target itself was never read. Without a word that
	// came back as releases [] and read as "nothing between here and there"
	// (etcd v3.7.3 with target_version v3.7.4; independent review, 2026-11-15)
	// — /v2/upgrade answers the same to= with a 404.
	if targetKey != nil && newest != nil && version.Compare(targetKey, newest) > 0 {
		notes = append(notes, fmt.Sprintf(
			"target_version %s is newer than every analyzed release on its line (newest analyzed: %s) — the checked range stops at %s; "+
				"%s itself and anything after %s was not checked, so read their release notes yourself",
			comp.TargetVersion, newestTag, newestTag, comp.TargetVersion, newestTag))
	}

	// What the running version's own branch already carries, at or below it.
	alreadyFixed := map[string]bool{}
	for _, d := range docs {
		k := version.NormalizeVersion(d.Version)
		if k == nil || !version.SameLine(d.Version, running) || !releaseline.SameBranch(k, currentKey) || version.Compare(k, currentKey) > 0 {
			continue
		}
		for _, c := range d.Changes {
			if c.Kind != "security" {
				continue
			}
			for _, id := range c.Advisories {
				alreadyFixed[strings.ToUpper(strings.TrimSpace(id))] = true
			}
		}
	}

	var rangeDocs []*release.Doc
	for _, r := range inRange {
		if d, ok := docs[r.Version]; ok {
			rangeDocs = append(rangeDocs, d)
		} else {
			rep.NotRead = append(rep.NotRead, r.Version)
		}
	}
	if len(rep.NotRead) > 0 {
		notes = append(notes, fmt.Sprintf("%d release(s) in range were not read (only the newest %d releases of a project are read per call, or the release was not available): see releases_not_read and check them with get_release", len(rep.NotRead), fetchSetSize))
	}
	// What the running branch fixed is read from the releases read above,
	// and reading further down the branch would tell the server the running
	// version. A release of the branch at or below it that was not read may
	// have fixed what a change in range cites: that change stays listed,
	// where check_upgrade, which reads from's branch itself, leaves it out —
	// and the answer says so (independent review, 2026-11-21).
	if !r.full {
		unread := 0
		for _, r := range onLine {
			k := version.NormalizeVersion(r.Version)
			if _, read := docs[r.Version]; !read && releaseline.SameBranch(k, currentKey) && version.Compare(k, currentKey) <= 0 {
				unread++
			}
		}
		if unread > 0 {
			notes = append(notes, fmt.Sprintf("%d release(s) at or below %s on your branch were not read (only the newest %d releases of a project are read per call, or the release was not available), so security changes your branch already fixed there are not left out", unread, running, fetchSetSize))
		}
	}

	points := []string{running}
	for _, r := range inRange {
		points = append(points, r.Version)
	}
	if target != "" && (len(inRange) == 0 || !releaseline.SameTag(inRange[len(inRange)-1].Version, target)) {
		points = append(points, target)
	}
	rep.Gaps = releaseline.Gaps(points)

	for _, d := range docs {
		if releaseline.SameTag(d.Version, running) {
			rep.LaterForRunning = d.Later
			break
		}
	}

	if r.full {
		rels := []briefing.RangeRelease{}
		for i, d := range rangeDocs {
			if i == fullReleasesCap {
				rep.FullOmitted = len(rangeDocs) - fullReleasesCap
				break
			}
			raw := json.RawMessage(d.Raw)
			if r.kinds != nil {
				b, err := filterRelease(d.Raw, &docFilter{kinds: r.kinds})
				if err != nil {
					return fmt.Errorf("/v2/releases/{project}/{version}: %w", err)
				}
				raw = b
			}
			// The first document of the call always goes out, whatever its size.
			if r.fullBytes > 0 && r.fullBytes+len(raw) > fullBytesBudget {
				rep.FullOmitted = len(rangeDocs) - i
				notes = append(notes, fmt.Sprintf("releases_full stops here: one call carries about %d MB of release documents — read the other %d with get_release, or call again with fewer components", fullBytesBudget>>20, rep.FullOmitted))
				break
			}
			r.fullBytes += len(raw)
			rep.Full = append(rep.Full, raw)
		}
		for _, d := range rangeDocs {
			rels = append(rels, briefing.RangeRelease{Version: d.Version, ReleasedAt: d.ReleasedAt, URL: d.URL, Counts: d.Counts})
		}
		rep.Releases = &rels
	} else {
		var settled int
		rep.Briefing, settled = briefing.Build(rangeDocs, alreadyFixed, currentKey)
		rep.Briefing.Narrow(r.kinds)
		if settled > 0 {
			notes = append(notes, fmt.Sprintf(
				"%d security change(s) in range were excluded: their advisories are already fixed at or below %s on your branch",
				settled, running))
		}
	}
	rep.Note = strings.Join(notes, "; ")
	r.reports = append(r.reports, rep)
	return nil
}
