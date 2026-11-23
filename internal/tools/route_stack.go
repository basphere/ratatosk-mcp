package tools

// One check_stack component as a route, and what a route says of the ranges
// it could not read: where their release notes are.
//
// A route carries later_in_range and excluded_on_branch as the briefing has
// them: a problem a release in range has, and the security changes left out as
// already fixed on the running branch, are not counted in any step (review,
// 2026-11-23).

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

// notReadGap is a gap of the range with where the project's release notes
// are, so what was not read can be read at the source: an agent filled the
// kubernetes 1.29–1.31 gap from memory (2026-11-23).
type notReadGap struct {
	After        string `json:"after"`
	Before       string `json:"before"`
	ReleaseNotes string `json:"release_notes,omitempty"`
}

// notReadGaps is the gaps, each with the project's release notes page when
// one of docs names its GitHub repository.
func notReadGaps(gaps []releaseline.Gap, docs []*release.Doc) []notReadGap {
	notes := releaseNotesPage(docs)
	out := []notReadGap{}
	for _, g := range gaps {
		out = append(out, notReadGap{After: g.After, Before: g.Before, ReleaseNotes: notes})
	}
	return out
}

// releaseNotesPage is https://github.com/<owner>/<repo>/releases for the first
// document whose release_notes_url is on GitHub; "" when none is.
func releaseNotesPage(docs []*release.Doc) string {
	for _, d := range docs {
		var x struct {
			URL string `json:"release_notes_url"`
		}
		if d == nil || json.Unmarshal(d.Raw, &x) != nil || x.URL == "" {
			continue
		}
		u, err := url.Parse(x.URL)
		if err != nil || !strings.EqualFold(u.Host, "github.com") {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		return "https://github.com/" + parts[0] + "/" + parts[1] + "/releases"
	}
	return ""
}

// routeComponent is one check_stack component as a route.
func routeComponent(rep *componentReport, steps []upgradeStep, kinds map[string]bool) json.RawMessage {
	// The range's documents first, then the others the call read, in version
	// order: any of them names the repository.
	docs := slices.Clone(rep.rangeDocs)
	var rest []*release.Doc
	for _, d := range rep.allDocs {
		if !slices.Contains(rep.rangeDocs, d) {
			rest = append(rest, d)
		}
	}
	slices.SortFunc(rest, func(a, b *release.Doc) int {
		return version.Compare(version.NormalizeVersion(a.Version), version.NormalizeVersion(b.Version))
	})
	later := rep.LaterForRunning
	if later == nil {
		later = []json.RawMessage{}
	}
	inRange := rep.LaterInRange
	if inRange == nil {
		inRange = &[]json.RawMessage{}
	}
	out := struct {
		Project          string                     `json:"project"`
		AnswerKind       string                     `json:"answer_kind"`
		RunningVersion   string                     `json:"running_version"`
		VersionReadAs    string                     `json:"version_read_as,omitempty"`
		VersionSource    string                     `json:"version_source,omitempty"`
		TargetVersion    string                     `json:"target_version"`
		Note             string                     `json:"note,omitempty"`
		NotRead          []notReadGap               `json:"not_read"`
		ReleasesNotRead  []string                   `json:"releases_not_read,omitempty"`
		LaterForRunning  []json.RawMessage          `json:"later_for_running"`
		LaterInRange     *[]json.RawMessage         `json:"later_in_range"`
		ExcludedOnBranch *briefing.ExcludedOnBranch `json:"excluded_on_branch,omitempty"`
		Totals           map[string]int             `json:"totals"`
		ShownHere        int                        `json:"shown_here"`
		Steps            []upgradeStep              `json:"steps"`
	}{
		Project: rep.Project, AnswerKind: "route", RunningVersion: rep.RunningVersion, VersionReadAs: rep.VersionReadAs,
		VersionSource: rep.VersionSource, TargetVersion: rep.TargetVersion, Note: rep.Note,
		NotRead: notReadGaps(rep.Gaps, append(docs, rest...)), ReleasesNotRead: rep.NotRead,
		LaterForRunning: later, LaterInRange: inRange, ExcludedOnBranch: rep.ExcludedOnBranch,
		Totals: rangeTotals(&rep.Briefing, kinds), ShownHere: shownHere(steps), Steps: steps,
	}
	b, err := json.Marshal(out)
	if err != nil {
		panic(err) // the server's own values: a wiring bug
	}
	return b
}
