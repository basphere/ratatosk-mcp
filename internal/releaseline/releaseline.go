// Package releaseline settles which releases a version range holds: the
// release line a version is on (a tag prefix such as flagd/ or lts-, not a
// minor), the range (running, target] cut out of it, the newest release of a
// minor, the neighbouring versions whose numbers skip, and an image build
// suffix read off a tag.
//
// These are the rules /v2/upgrade applies on the server; check_stack applies
// them in this process to the releases it read by project slug alone, which is
// what keeps a running version from going upstream. So nothing here may touch
// the network: every function is a function of the rows it is given.
package releaseline

import (
	"sort"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

// DescCmp orders two tags of the SAME release line the way the website does
// (apps/web/src/lib/version.ts compareVersionsDesc): higher key first, and on an
// equal key the raw tag ascending, so a finished release ranks above its own
// pre-releases (1.24.3 above 1.24.3-rc1). Negative = a is higher.
func DescCmp(a, b string) int {
	if c := version.Compare(version.NormalizeVersion(a), version.NormalizeVersion(b)); c != 0 {
		return -c
	}
	ra, rb := strings.TrimSpace(a), strings.TrimSpace(b)
	switch {
	case ra == rb:
		return 0
	case ra < rb:
		return -1
	}
	return 1
}

// SameTag says two spellings name the same release: equal after the line
// prefix, ignoring a leading "v" and case ("1.30.2" and "v1.30.2").
func SameTag(a, b string) bool {
	if !version.SameLine(a, b) {
		return false
	}
	_, ra := version.SplitLine(a)
	_, rb := version.SplitLine(b)
	strip := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) >= 2 && (s[0] == 'v' || s[0] == 'V') && s[1] >= '0' && s[1] <= '9' {
			s = s[1:]
		}
		return strings.ToLower(s)
	}
	return strip(ra) == strip(rb)
}

// Gap is a place where neighbouring version numbers skip (see Gaps).
type Gap struct {
	After  string `json:"after"`
	Before string `json:"before"`
}

// Gaps names neighbouring versions whose numbers skip, so releases in
// between may exist that were never analyzed. Port of versionGaps in the web's
// lib/v2-shape.ts — the server's /v2/upgrade answers with the same rule.
// 2026-11-03: etcd v3.5.30 → latest went v3.5.32 then v3.6.7; v3.6.0–v3.6.6
// predate coverage, and without the gap an agent reads "nothing to do" across
// the minor jump that carries the breaking changes. Rule: the first differing
// component jumped by more than one, or by one with a later component above
// zero (3.5.32 → 3.6.7, 3.5.31 → 3.5.33). Versions that were never published
// match too — hence "may exist".
func Gaps(points []string) []Gap {
	var out []Gap
	for k := 1; k < len(points); k++ {
		a := version.NormalizeVersion(points[k-1])
		b := version.NormalizeVersion(points[k])
		if a == nil || b == nil {
			continue
		}
		n := max(len(a), len(b))
		at := func(v []int, i int) int {
			if i < len(v) {
				return v[i]
			}
			return 0
		}
		i := 0
		for i < n && at(a, i) == at(b, i) {
			i++
		}
		if i == n || at(b, i) < at(a, i) {
			continue
		}
		gap := at(b, i) > at(a, i)+1
		for j := i + 1; j < n && !gap; j++ {
			if at(b, j) > 0 {
				gap = true
			}
		}
		if gap {
			out = append(out, Gap{After: points[k-1], Before: points[k]})
		}
	}
	return out
}

// SameBranch reports whether two version keys sit on the same release branch,
// which for every project on record means sharing major and minor. A key too
// short to name a branch answers false — the conservative direction, since the
// only thing this decides is whether to hide something.
func SameBranch(a, b []int) bool {
	return len(a) >= 2 && len(b) >= 2 && a[0] == b[0] && a[1] == b[1]
}

// TagOf spells v the way the project's tags on that line do: the row that
// names the same release if there is one, otherwise v with its leading "v"
// added or dropped to match the line (a version not on record, such as a
// pre-release nobody analyzed). With no row to copy from, v comes back as is.
func TagOf(line []release.Row, v string) string {
	for _, r := range line {
		if r.Version == v {
			return v
		}
	}
	for _, r := range line {
		if SameTag(r.Version, v) {
			return r.Version
		}
	}
	if len(line) == 0 {
		return v
	}
	hasV := func(s string) bool {
		return len(s) >= 2 && (s[0] == 'v' || s[0] == 'V') && s[1] >= '0' && s[1] <= '9'
	}
	digit := func(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }
	t := strings.TrimSpace(v)
	_, rest := version.SplitLine(t)
	_, like := version.SplitLine(line[0].Version)
	prefix := t[:len(t)-len(rest)]
	switch {
	case hasV(like) && digit(rest):
		return prefix + like[:1] + rest
	case digit(like) && hasV(rest):
		return prefix + rest[1:]
	}
	return v
}

// UpgradeRange cuts (running, target] out of a project's rows on the running
// version's line, oldest first — the same selection /v2/upgrade makes. target
// "" means the highest version on that line.
func UpgradeRange(rows []release.Row, running, target string) (inRange []release.Row, resolvedTarget string) {
	var line []release.Row
	for _, r := range rows {
		if version.SameLine(r.Version, running) && version.NormalizeVersion(r.Version) != nil {
			line = append(line, r)
		}
	}
	// Compare in the project's own spelling. DescCmp breaks an equal numeric key
	// by the raw tag, so "v0.45.1" from the caller and the project's "0.45.1"
	// ranked as two versions: the running release came back as upgrade work, and
	// a target written with the other "v" style cut the range to nothing with no
	// note (falco, istio, keycloak, strimzi — reproduced 2026-11-11). /v2/upgrade
	// does the same mapping (tagOf).
	runningTag := TagOf(line, running)
	resolvedTarget = target
	targetTag := ""
	if target != "" {
		targetTag = TagOf(line, target)
	} else {
		for _, r := range line {
			if targetTag == "" || DescCmp(r.Version, targetTag) < 0 {
				targetTag = r.Version
			}
		}
		if targetTag == "" {
			targetTag = runningTag
		}
		resolvedTarget = targetTag
	}
	for _, r := range line {
		if SameTag(r.Version, runningTag) || DescCmp(r.Version, runningTag) >= 0 {
			continue
		}
		if SameTag(r.Version, targetTag) || DescCmp(r.Version, targetTag) >= 0 {
			inRange = append(inRange, r)
		}
	}
	sort.SliceStable(inRange, func(i, j int) bool { return DescCmp(inRange[i].Version, inRange[j].Version) > 0 })
	return inRange, resolvedTarget
}

// SameMinorTarget is same_minor's range end: the newest release on the running
// version's line and branch (major.minor) above it, as the project spells it.
// "" when no release above the running version is on record there.
func SameMinorTarget(rows []release.Row, running string) string {
	key := version.NormalizeVersion(running)
	var line []release.Row
	for _, r := range rows {
		if version.SameLine(r.Version, running) && version.NormalizeVersion(r.Version) != nil {
			line = append(line, r)
		}
	}
	runningTag := TagOf(line, running)
	best := ""
	for _, r := range line {
		if !SameBranch(version.NormalizeVersion(r.Version), key) || SameTag(r.Version, runningTag) || DescCmp(r.Version, runningTag) >= 0 {
			continue
		}
		if best == "" || DescCmp(r.Version, best) < 0 {
			best = r.Version
		}
	}
	return best
}

// NewestOnLine is the highest version on running's line, as spelled on record.
func NewestOnLine(rows []release.Row, running string) string {
	best := ""
	for _, r := range rows {
		if !version.SameLine(r.Version, running) || version.NormalizeVersion(r.Version) == nil {
			continue
		}
		if best == "" || DescCmp(r.Version, best) < 0 {
			best = r.Version
		}
	}
	return best
}

// LinesPresent lists the release lines a project publishes, in a stable order.
func LinesPresent(rows []release.Row) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		l := version.Line(r.Version)
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// RowsOnLine is the rows on v's release line that carry a version number.
func RowsOnLine(rows []release.Row, v string) []release.Row {
	var out []release.Row
	for _, r := range rows {
		if version.SameLine(r.Version, v) && version.NormalizeVersion(r.Version) != nil {
			out = append(out, r)
		}
	}
	return out
}
