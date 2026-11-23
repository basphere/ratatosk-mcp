package mcpserver

// The tool descriptions and the briefing hint, as a client receives them: what
// they must say, each because an agent read the answer wrong without it.

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
)

// "What happened to the running version after it shipped" was read as "what is
// wrong with it": an advisory_after_release is an advisory published later for
// a vulnerability that version already fixes. And list_releases, which lists by
// release date, read as the place to ask what is new about a version.
// (Independent review before the 1.0 launch, 2026-11-15.)
func TestDescriptionsSayWhatLaterEventsMean(t *testing.T) {
	d := toolDescriptions(t)
	for _, c := range []struct{ where, text string }{
		{"check_stack", d["check_stack"]},
		{"check_upgrade", d["check_upgrade"]},
		{"hint", briefing.Hint(nil)},
	} {
		for _, want := range []string{"already fixes", "does not mean the release is vulnerable", "recorded on that version, not here"} {
			// hint keeps the first two; where a defect is recorded is in the
			// descriptions (hint bounded at 2,000 characters, 2026-11-20).
			if c.where == "hint" && want == "recorded on that version, not here" {
				continue
			}
			if !strings.Contains(c.text, want) {
				t.Errorf("%s does not say %q", c.where, want)
			}
		}
		if strings.Contains(c.text, "what happened to the") {
			t.Errorf("%s still says the later events are what happened to the version", c.where)
		}
	}
	if !strings.Contains(d["get_release"], "already fixes, which does not make it vulnerable") {
		t.Errorf("get_release description: %s", d["get_release"])
	}
	lr := d["list_releases"]
	if strings.Contains(lr, "THE tool for") || !strings.Contains(lr, "/v2/events") || !strings.Contains(lr, "later_for_running") {
		t.Errorf("list_releases must say it lists by release date and where later events are: %s", lr)
	}
}

// severity:null was described as "the advisory states none". It means no grade
// was found in what was read, an advisory not yet published included: the docs
// example measured on 2026-11-03 showed GHSA-3wh4-j44w-pg92 on etcd v3.5.32 as
// null, and the same change reads medium on 2026-11-15.
// (Independent review before the 1.0 launch, 2026-11-15.)
func TestDescriptionsSayWhatSeverityNullMeans(t *testing.T) {
	d := toolDescriptions(t)
	for _, c := range []struct{ where, text string }{{"get_release", d["get_release"]}, {"hint", briefing.Hint(nil)}} {
		if strings.Contains(c.text, "states none") || !strings.Contains(c.text, "no grade was found in what was read") {
			t.Errorf("%s must say null is a grade not found: %s", c.where, c.text)
		}
	}
}

// The text an agent reads says what a release line is, and where to read
// evidence, which a briefing does not carry.
// (1.2.0; measured answers of 2026-11-19 and 11-20 — docs/design/mcp-answer-shape.md
// in the ratatosk app repository.)
func TestDescriptionsSayWhatALineIsAndWhereEvidenceIs(t *testing.T) {
	d := toolDescriptions(t)
	for _, name := range []string{"check_stack", "check_upgrade"} {
		for _, want := range []string{"not a minor version", "same_minor", "changes_more", "get_release(project, version, changes:[id, …])"} {
			if !strings.Contains(d[name], want) {
				t.Errorf("%s description does not say %q", name, want)
			}
		}
		if strings.Contains(d[name], "the latest on the same release line") {
			t.Errorf("%s still says \"the latest on the same release line\" without saying what a line is", name)
		}
	}
	// hint keeps what reading the answer needs, within 2,000 characters; the
	// meanings it dropped are in the description (4,429 characters rode on
	// every briefing, 2026-11-20).
	if n := utf8.RuneCountInString(briefing.Hint(nil)); n > 2000 {
		t.Errorf("hint is %d characters, at most 2,000", n)
	}
	for _, want := range []string{"ONLY IF", "What is not listed is not absent", "changes_more", `kinds:["important_fixes"]`,
		"get_release(project, version, changes:[id, …])", "no evidence", "does not mean the release is vulnerable", "later_in_range"} {
		if !strings.Contains(briefing.Hint(nil), want) {
			t.Errorf("hint does not say %q", want)
		}
	}
	for _, want := range []string{"excluded_on_branch", "version_read_as", "also_in naming the others", "security_unclear", "no_downgrade means",
		"removed_in is when", "also_required (security) are what the notes say to do", "none of those three kinds is left out", "The answer is not cut to a size",
		"Security changes citing no advisory id are not in distinct_advisories", "recorded on that version, not here", "null when no grade was found"} {
		if !strings.Contains(d["check_stack"], want) {
			t.Errorf("check_stack description does not say %q", want)
		}
	}
	if strings.Contains(briefing.Hint(nil), "never read an omitted count as nothing to do") {
		t.Error("the hint still sends the agent to read whole releases for what changes_more lists")
	}
	for _, want := range []string{"kinds", "changes:[id, …]", "not_found"} {
		if !strings.Contains(d["get_release"], want) {
			t.Errorf("get_release description does not say %q", want)
		}
	}
	for _, want := range []string{"project keeps one project's releases", "matches"} {
		if !strings.Contains(d["search"], want) {
			t.Errorf("search description does not say %q", want)
		}
	}
}

// check_stack's and check_upgrade's first answer without detail is a route
// (1.3.0): the descriptions and the detail argument say so first, and no
// sentence still calls detail:"brief" the default.
func TestDescriptionsSayTheFirstAnswerIsARoute(t *testing.T) {
	d := toolDescriptions(t)
	for _, tool := range currentToolsList(t) {
		name := tool["name"].(string)
		if name != "check_stack" && name != "check_upgrade" {
			continue
		}
		if !strings.HasPrefix(d[name], `Without detail, the first answer is a route (answer_kind "route")`) {
			t.Errorf("%s description: %.200s", name, d[name])
		}
		if strings.Contains(d[name], `"brief\", default`) || strings.Contains(d[name], `brief", default`) {
			t.Errorf("%s still says brief is the default", name)
		}
		props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		detail, _ := props["detail"].(map[string]any)["description"].(string)
		if !strings.HasPrefix(detail, "omit for a route") || strings.Contains(detail, "(default)") {
			t.Errorf("%s detail: %q", name, detail)
		}
	}
}
