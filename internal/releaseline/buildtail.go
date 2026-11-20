package releaseline

// Image build suffixes (1.2.0). Agents read running versions off image tags,
// and an image tag can carry a build suffix after the version it was built
// from; read whole, it names a release line the project does not publish.

import (
	"regexp"
	"slices"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/release"
)

// buildTailRe matches the build suffix an image tag carries after the version
// it was built from: "-<unix time>-<commit>" (cilium-envoy:
// v1.37.6-1789133542-cbec91f666af0bf742da986d43832932dbb26b82) or "-<commit>".
// Read whole, such a tag splits as release line "v1.37.6" and failed with
// "no releases on record for line v1.37.6 … the release-line prefix was
// probably dropped" — the wrong advice, since a suffix was added (outside
// agent report, reproduced 2026-11-19). The commit must hold a letter and a
// digit, so a pre-release ("-rc1", "-beta.2") or a word never matches.
var buildTailRe = regexp.MustCompile(`^(.*?[0-9])-(?:[0-9]{9,}-)?([0-9a-f]{7,40})$`)

// StripBuildTail returns v without an image build suffix, and whether there
// was one. A release-line prefix (flagd/, lts-) is never touched: only the end
// of the tag is read. The caller uses it only when the tag as given names a
// line the project does not publish and the stripped one does.
func StripBuildTail(v string) (string, bool) {
	m := buildTailRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return v, false
	}
	hash := m[2]
	if !strings.ContainsAny(hash, "abcdef") || !strings.ContainsAny(hash, "0123456789") {
		return v, false
	}
	return m[1], true
}

// ReadBuildTail is the version an image build suffix stands on: v without
// the suffix (StripBuildTail), when v as passed is not a release on record
// and the version without the suffix is (spec "마", 2026-11-20). The first
// rule asked only whether v's release line existed, so v1.37.6-cbec91f —
// whose line is the main one — was read as a pre-release of v1.37.6 and
// v1.37.6 itself was listed as upgrade work. A pre-release (v1.21.0-rc.1)
// never matches StripBuildTail.
func ReadBuildTail(rows []release.Row, v string) (string, bool) {
	if onRecord(rows, v) {
		return "", false
	}
	if s, ok := StripBuildTail(v); ok && onRecord(rows, s) {
		return s, true
	}
	return "", false
}

// onRecord says v names a release in rows (SameTag: the leading "v" and case
// aside).
func onRecord(rows []release.Row, v string) bool {
	return slices.ContainsFunc(rows, func(r release.Row) bool { return SameTag(r.Version, v) })
}
