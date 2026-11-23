package tools

// get_release: one release, whole or in part. Reading only part of a
// release (1.2.0): kinds and changes.
//
// Why: one release is too large to read whole. cilium v1.20.0 is 758,000
// characters (1,094 changes, 676 of them "other"), and 32 agent runs on
// 2026-11-20 all had at least one get_release answer dropped to a file by
// their client (84 answers) — Claude Code hands a tool answer over 50,000
// characters to the model as a file, not as text. The briefing, in turn,
// carries no evidence for a change. kinds keeps the kinds that ask for a decision ("between
// the briefing and the whole release"), and changes fetches the few entries a
// briefing named, with body and evidence. Both are arguments on the tools that
// already exist (owner's decision, 2026-11-20 — no new tool).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/upstream"
)

// getReleaseDef is get_release as a client lists it.
func getReleaseDef() *mcp.Tool {
	return &mcp.Tool{
		Name:        "get_release",
		Annotations: &mcp.ToolAnnotations{Title: "One release in full", ReadOnlyHint: true},
		Description: "One release in full, the same shape the website shows: counts per kind, warnings (critical " +
			"security, no_downgrade), before_upgrade (prerequisites, known_issues, upgrade_guide), changes in screen " +
			"order, folded lines, and later events (an advisory published after the release for a vulnerability it " +
			"already fixes, which does not make it vulnerable; a problem it has, fixed in a newer version). Every " +
			"change carries evidence: the release-note lines it came from, verbatim, " +
			"with their URL — cite those when you report a change. severity is the grade the project's own advisory " +
			"states; null means no grade was found in what was read (an advisory not yet published included), and the " +
			"same advisory can carry a grade on another release. Omit version for the latest release of the project: its " +
			"highest version, which is not always the most recently published one (a patch on an older branch can " +
			"ship later). " +
			"version is accepted with or without the leading 'v'; a wrong one returns an error listing the project's " +
			"recent versions — retry with one of those. A whole release can run to hundreds of thousands of characters " +
			"(cilium v1.20.0: 758,000), so read part of it: kinds keeps the changes of those kinds (security, breaking, " +
			"deprecations, important_fixes is what asks for a decision); changes:[id, …] returns only the changes and " +
			"before_upgrade items named — the ids a briefing's changes, changes_more and before_upgrade carry — with body " +
			"and evidence, and lists the ids this release does not have in not_found. Without version, changes reads the " +
			"releases the ids name (up to 10) and answers {project, releases:[{version, url, release_notes_url, changes, " +
			"before_upgrade when an id names one}], not_found}. Both leave the folded lines out; pass one of them, not both. " + DataNote,
		InputSchema: toolSchema[getReleaseArgs]("kinds", "changes"),
	}
}

type getReleaseArgs struct {
	Project string     `json:"project" jsonschema:"project slug, e.g. envoy"`
	Version string     `json:"version,omitempty" jsonschema:"release version as published, e.g. v1.38.3; omit for the latest release (or, with changes, for the releases the ids name)"`
	Kinds   stringList `json:"kinds,omitempty" jsonschema:"only changes of these kinds (any of security, breaking, deprecations, important_fixes, enhancements, dependency_updates, other); folded lines are then left out. [security, breaking, deprecations, important_fixes] reads what asks for a decision. Omit for the whole release"`
	Changes stringList `json:"changes,omitempty" jsonschema:"ids of changes or before_upgrade items (from a briefing's changes, changes_more or before_upgrade), up to 50: only those, with body and evidence; ids not found are listed in not_found. Without version, the releases the ids name are read (up to 10). Not together with kinds"`
}

func (h *handlers) getReleaseTool(ctx context.Context, req *mcp.CallToolRequest, args getReleaseArgs) (*mcp.CallToolResult, any, error) {
	ctx = logging.RequestContext(ctx, "get_release", req)
	project := slugArg(args.Project)
	if project == "" {
		return errResult(fmt.Errorf("project is required")), nil, nil
	}
	kindSet, err := kindsArg(args.Kinds, false)
	if err != nil {
		return errResult(err), nil, nil
	}
	ids, err := changesArg(args.Changes)
	if err != nil {
		return errResult(err), nil, nil
	}
	// Both at once used to answer an id of another kind nowhere — neither in
	// changes nor in not_found (independent review, 2026-11-20).
	if kindSet != nil && ids != nil {
		return errResult(fmt.Errorf("pass changes or kinds, not both: changes picks entries by id whatever their kind, kinds picks a whole kind")), nil, nil
	}
	ver := args.Version
	// Ids name their release (project@version#…). Without a version, the
	// releases they name are read — the latest alone answered every id of
	// another release as not found (independent review, 2026-11-20).
	if ver == "" && len(ids) > 0 {
		return h.pickedReleases(ctx, project, ids)
	}
	if ver == "" {
		// "Latest" is decided in one place, the API: /v2/projects carries each
		// project's latest release, which is its highest version. This used to
		// take the first row of the release list (the most recently published),
		// so a patch on an older branch that shipped later was answered as the
		// latest: kubernetes v1.36.6 while v1.37.2 was on record, 12 of 78
		// projects on 2026-11-11.
		roster, err := h.api.Projects(ctx)
		if err != nil {
			return errResult(fmt.Errorf("could not read the project list: %w", err)), nil, nil
		}
		known := false
		for _, p := range roster {
			if p.Slug == project {
				known = true
				if p.Latest != nil {
					ver = p.Latest.Version
				}
			}
		}
		if !known {
			return errResult(unknownProject(project)), nil, nil
		}
		// A tracked project with nothing analyzed yet (spiffe on 2026-11-15) used
		// to be told to check its slug, though the slug was right.
		if ver == "" {
			return errResult(fmt.Errorf("project %s is tracked by ratatosk but has no analyzed release yet — there is no release to show", project)), nil, nil
		}
	}
	raw, err := h.api.ReleaseRaw(ctx, project, ver, answerLang, false)
	if err != nil {
		return errResult(err), nil, nil
	}
	if kindSet == nil && ids == nil {
		return rawResult(raw), nil, nil
	}
	if !isJSONObject(raw) {
		return rawResult(raw), nil, nil // the same refusal as an unfiltered answer
	}
	f := &docFilter{kinds: kindSet}
	if ids != nil {
		f.ids, f.found = map[string]bool{}, map[string]bool{}
		for _, id := range ids {
			f.ids[id] = true
		}
	}
	b, err := filterRelease(raw, f)
	if err != nil {
		return errResult(fmt.Errorf("/v2/releases/{project}/{version}: %w", err)), nil, nil
	}
	if ids != nil {
		missing := []string{}
		for _, id := range ids {
			if !f.found[id] {
				missing = append(missing, truncate(id, 120))
			}
		}
		fields, err := readObject(b)
		if err != nil {
			return errResult(err), nil, nil
		}
		nf, _ := json.Marshal(missing)
		b = writeObject(append(fields, objField{Key: "not_found", Val: nf}))
	}
	return rawResult(b), nil, nil
}

// maxPickedChanges bounds get_release's changes argument. A briefing's
// changes_more line names one change, and fifty of them are already more than a
// decision needs from one release; past that, kinds is the way to read a kind.
const maxPickedChanges = 50

// changesArg reads get_release's changes argument: trimmed, empty entries
// dropped, repeats kept once.
func changesArg(list stringList) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, id := range list {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > maxPickedChanges {
		return nil, fmt.Errorf("changes: %d ids, at most %d per call — split them, or read a whole kind with kinds", len(out), maxPickedChanges)
	}
	return out, nil
}

// releaseOfID reads the release an id names: "cilium@v1.20.0#dd9d9e39" names
// cilium v1.20.0.
func releaseOfID(id string) (project, version string, ok bool) {
	head, _, found := strings.Cut(id, "#")
	if !found {
		return "", "", false
	}
	p, v, found := strings.Cut(head, "@")
	if !found || p == "" || v == "" {
		return "", "", false
	}
	return p, v, true
}

// maxPickedReleases bounds the releases one get_release call reads for the
// ids it is given without a version (spec "마"): a release document is up to
// 870 KB (cilium v1.20.0), and ten is more than a briefing's lines name.
const maxPickedReleases = 10

// pickedRelease is one release of get_release's answer to changes without a
// version: the named changes and, when an id names one, before-upgrade items.
type pickedRelease struct {
	Version         string          `json:"version"`
	URL             json.RawMessage `json:"url,omitempty"`
	ReleaseNotesURL json.RawMessage `json:"release_notes_url,omitempty"`
	Changes         json.RawMessage `json:"changes"`
	BeforeUpgrade   json.RawMessage `json:"before_upgrade,omitempty"`
}

// pickedReleases answers get_release(changes) without a version: every
// release the ids name, in the order the ids first name it, each cut to the
// named entries. Ids of another project, ids that are not ids, and ids a
// release does not have (or a release that is not on record) are listed in
// not_found.
func (h *handlers) pickedReleases(ctx context.Context, project string, ids []string) (*mcp.CallToolResult, any, error) {
	var order []string
	byRelease := map[string][]string{}
	notFound := []string{}
	for _, id := range ids {
		p, v, ok := releaseOfID(id)
		if !ok || slugArg(p) != project {
			notFound = append(notFound, truncate(id, 120))
			continue
		}
		if _, seen := byRelease[v]; !seen {
			order = append(order, v)
		}
		byRelease[v] = append(byRelease[v], id)
	}
	if len(order) > maxPickedReleases {
		return errResult(fmt.Errorf("changes name %d releases; at most %d per call without version — split the ids", len(order), maxPickedReleases)), nil, nil
	}
	releases := []pickedRelease{}
	for _, v := range order {
		raw, err := h.api.ReleaseRaw(ctx, project, v, answerLang, false)
		if he, ok := err.(*upstream.HTTPError); ok && he.Status == 404 {
			for _, id := range byRelease[v] {
				notFound = append(notFound, truncate(id, 120))
			}
			continue
		}
		if err != nil {
			return errResult(err), nil, nil
		}
		if !isJSONObject(raw) {
			return rawResult(raw), nil, nil // the same refusal as one release
		}
		f := &docFilter{ids: map[string]bool{}, found: map[string]bool{}, foundUpgrade: map[string]bool{}}
		for _, id := range byRelease[v] {
			f.ids[id] = true
		}
		b, err := filterRelease(raw, f)
		if err != nil {
			return errResult(fmt.Errorf("/v2/releases/{project}/{version}: %w", err)), nil, nil
		}
		fields, err := readObject(b)
		if err != nil {
			return errResult(err), nil, nil
		}
		pr := pickedRelease{Version: v, Changes: json.RawMessage("[]")}
		var bu json.RawMessage
		for _, fl := range fields {
			switch fl.Key {
			case "version":
				_ = json.Unmarshal(fl.Val, &pr.Version)
			case "url":
				pr.URL = fl.Val
			case "release_notes_url":
				pr.ReleaseNotesURL = fl.Val
			case "changes":
				pr.Changes = fl.Val
			case "before_upgrade":
				bu = fl.Val
			}
		}
		for _, id := range byRelease[v] {
			if !f.found[id] {
				notFound = append(notFound, truncate(id, 120))
			}
		}
		if len(f.foundUpgrade) > 0 {
			pr.BeforeUpgrade = bu
		}
		releases = append(releases, pr)
	}
	out := struct {
		Project  string          `json:"project"`
		Releases []pickedRelease `json:"releases"`
		NotFound []string        `json:"not_found"`
	}{project, releases, notFound}
	res, err := jsonResult(out)
	return res, nil, err
}
