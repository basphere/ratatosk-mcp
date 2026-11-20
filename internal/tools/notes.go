package tools

// The notes check_stack and check_upgrade write about a version — the
// cross-checks this server can make on claims it cannot see.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

// coverageNote flags a running version that sits below every release on record
// on its line. It is the one cross-check this server can make on a claim about
// an environment it cannot see, and it catches two different things with the
// same sentence: a genuinely ancient install, where a briefing drawn from the
// covered window is not the whole upgrade path, and a version that was never
// read off a live resource (2026-07-28: an agent holding no version at all
// supplied cilium 1.16.0 against an actual 1.19.5 — every answer after that was
// confidently wrong and nothing downstream could tell).
func coverageNote(running string, runningKey, oldest []int, oldestTag string) string {
	if oldest == nil || version.Compare(runningKey, oldest) >= 0 {
		return ""
	}
	return fmt.Sprintf(
		"running version %s is older than every release on record (earliest on record: %s) — "+
			"this covers the analyzed window only, so treat it as partial, and re-check that the "+
			"running version was read off a live resource",
		running, oldestTag)
}

// newerNote flags a running version above every analyzed release on its line.
// The range (running, newest] is then empty, and an empty range with a target
// below the running version read as "already up to date" with no word said
// (etcd v3.55.0: releases [], target_version v3.7.3; independent review,
// 2026-11-15). Either ratatosk has not analyzed that version yet or the version
// was misread, and in both cases nothing was checked against it, so the
// component goes to could_not_check like a dropped line prefix does — /v2/upgrade
// answers the same case with a 404. A pre-release of a version not on record
// counts as that version (NormalizeVersion drops the suffix), so v3.6.9-rc.1
// above v3.6.8 is flagged and v3.6.8-rc.1 is not.
func newerNote(running string, runningKey, newest []int, newestTag string) string {
	if newest == nil || version.Compare(runningKey, newest) <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"running version %s is newer than every analyzed release on its line (newest analyzed: %s) — "+
			"ratatosk has not analyzed this version yet, or the version was misread. Nothing was checked against it, "+
			"so this is not \"already up to date\": re-check that the version was read off a live resource, and read "+
			"its release notes yourself",
		running, newestTag)
}

// confessionRe matches version_source values that describe an inference
// rather than a live read. Sixty controlled runs (2026-07-29 hub campaign)
// gave this a clean separation: all 8 versions whose source contained such
// vocabulary were hallucinations, all 197 citing a concrete read were correct,
// zero crossover. The model does not forge sources — it confesses. "stated"
// and "standard" stay OFF the list: a user-stated version is a legitimate
// source in the no-cluster-access deployment, and every observed confession
// is caught without them.
var confessionRe = regexp.MustCompile(`(?i)infer|assum|guess|estimat|likely|probab|typical|need verification|not verified|unverified`)

// confessionNote turns a self-confessed inferred source into a warning on the
// component's note — the same channel as the low-version warning.
func confessionNote(src string) string {
	if src == "" || !confessionRe.MatchString(src) {
		return ""
	}
	if len(src) > 80 {
		src = src[:80] + "…"
	}
	return fmt.Sprintf("version_source reads as an inference, not a live read (%q) — treat the "+
		"running version as unverified and re-read it off a live resource; in measurement, every "+
		"self-described inferred version was wrong", src)
}

// resolveTarget parses target_version against the running version. A target
// at or below running asks for the range (running, target], which is empty by
// construction — in measurement, five of twenty agent runs sent exactly
// running-as-target and read the guaranteed zero changes as "no issues". An
// empty-by-construction range is therefore refused loudly (field ignored,
// note says why and teaches the intended use) instead of honored silently.
func resolveTarget(currentKey []int, targetRaw string) ([]int, string) {
	if targetRaw == "" {
		return nil, ""
	}
	targetKey := version.NormalizeVersion(targetRaw)
	if targetKey == nil {
		return nil, "target_version could not be parsed; ignored"
	}
	if version.Compare(targetKey, currentKey) <= 0 {
		return nil, fmt.Sprintf("target_version (%s) is not above the running version, so the checked range "+
			"(running, target] is empty by construction — the field was ignored and the full upgrade path is shown. "+
			"To preview one upgrade hop, keep version = what is running now and set target_version to the destination.", targetRaw)
	}
	return targetKey, ""
}

// sameMinorNote says that same_minor found nothing to upgrade to.
func sameMinorNote(running, newest string) string {
	k := version.NormalizeVersion(running)
	minor := running
	if len(k) >= 2 {
		minor = fmt.Sprintf("%d.%d", k[0], k[1])
	}
	return fmt.Sprintf("same_minor: no release above %s on %s is on record, so the range is empty; the newest on its release line is %s", running, minor, newest)
}

// lineList renders the available lines for a human: "lines \"core\", \"flagd\"".
func lineList(lines []string) string {
	quoted := make([]string, len(lines))
	for i, l := range lines {
		quoted[i] = strconv.Quote(lineLabel(l))
	}
	if len(quoted) == 1 {
		return "only line " + quoted[0]
	}
	return "lines " + strings.Join(quoted, ", ")
}

// exampleTag returns a real tag from the given line, so the note shows the
// shape the caller should have sent rather than describing it.
func exampleTag(rows []release.Row, line string) string {
	for _, r := range rows {
		if version.Line(r.Version) == line {
			return r.Version
		}
	}
	return ""
}

func lineLabel(line string) string {
	if line == "" {
		return "main (no prefix)"
	}
	return line
}
