package tools

// check_upgrade: one upgrade, computed by the server — from and to are sent
// upstream, and the tool says so. check_stack is the door that keeps running
// versions on the caller's side.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/upstream"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

// checkUpgradeDef is check_upgrade as a client lists it.
func checkUpgradeDef() *mcp.Tool {
	return &mcp.Tool{
		Name:        "check_upgrade",
		Annotations: &mcp.ToolAnnotations{Title: "What one upgrade brings", ReadOnlyHint: true},
		Description: "Everything between the version you run (from, exclusive) and a target (to, inclusive; default " +
			"the newest release on from's release line — a release line is a tag prefix such as flagd/ or lts-, not a " +
			"minor version, so from v1.36.5 the default reaches v1.37.x; same_minor:true stops at the newest patch of " +
			"from's minor), computed BY THE SERVER: from and to are sent upstream. To keep " +
			"running versions on your side, use check_stack instead — it compares locally and sends only project " +
			"slugs. Returns a briefing (detail:\"brief\", default) in the same form as one check_stack component (the " +
			"fields are described there), or " +
			"every release in range verbatim (detail:\"full\"); kinds keeps the changes of those kinds. Read before_upgrade of every release in range — " +
			"prerequisites and upgrade guides usually sit on the release that starts a new minor. Every security, breaking " +
			"and deprecation change that changes_omitted counts has its line in changes_more; the briefing carries no evidence quotes " +
			"(only a before_upgrade item without a body has its own), so read the body " +
			"and evidence of the entries you report with get_release(project, version, changes:[id, …]). A security " +
			"change whose advisories from's own branch already fixed at or below from is left out and counted in " +
			"excluded_on_branch, as check_stack does; if the releases that tell cannot be read, nothing is left out " +
			"and note says so. An image build suffix on from is read off as in check_stack (version_read_as). gaps lists " +
			"neighbouring versions whose numbers skip (e.g. v3.5.32 then v3.6.7): releases in between may exist that " +
			"were not analyzed, so check their release notes yourself. later_for_from is " + laterNote("the from version") +
			" later_in_range lists the same for every release in range. A briefing names the server that built it in " +
			"server_version. " + DataNote,
		InputSchema: toolSchema[checkUpgradeArgs]("kinds"),
	}
}

type checkUpgradeArgs struct {
	Project   string     `json:"project" jsonschema:"project slug, e.g. etcd"`
	From      string     `json:"from" jsonschema:"the version you run now (exclusive), as the project publishes it"`
	To        string     `json:"to,omitempty" jsonschema:"target version (inclusive); omit for the newest release on the same release line (a tag prefix such as flagd/ or lts-, not a minor: from v1.36.5 that reaches v1.37.x — use same_minor to stay on v1.36.x)"`
	Detail    string     `json:"detail,omitempty" jsonschema:"brief (default) or full (every release in range verbatim)"`
	Lang      string     `json:"lang,omitempty" jsonschema:"en (default), ko or ja"`
	Kinds     stringList `json:"kinds,omitempty" jsonschema:"only changes of these kinds: with brief any of security, breaking, deprecations, important_fixes; with full any of the seven kinds (folded lines are then left out). Omit for every kind"`
	SameMinor bool       `json:"same_minor,omitempty" jsonschema:"true: stop at the newest release of from's minor (v1.36.5 → the newest v1.36.x) instead of the newest on its release line; not together with to"`
}

func (h *handlers) checkUpgradeTool(ctx context.Context, req *mcp.CallToolRequest, args checkUpgradeArgs) (*mcp.CallToolResult, any, error) {
	ctx = logging.RequestContext(ctx, "check_upgrade", req)
	args.Project = slugArg(args.Project)
	if args.Project == "" || args.From == "" {
		return errResult(fmt.Errorf("project and from are required (from = the version you run now)")), nil, nil
	}
	full, err := detailArg(args.Detail)
	if err != nil {
		return errResult(err), nil, nil
	}
	lang, err := langArg(args.Lang)
	if err != nil {
		return errResult(err), nil, nil
	}
	kindSet, err := kindsArg(args.Kinds, !full)
	if err != nil {
		return errResult(err), nil, nil
	}
	// "." and ".." are refused before any request, as the client refuses them
	// in a path: the release list below would otherwise ask for them first.
	if _, err := upstream.PathSegment("project", args.Project); err != nil {
		return errResult(err), nil, nil
	}
	to := args.To
	if args.SameMinor && strings.TrimSpace(to) != "" {
		return errResult(fmt.Errorf("same_minor and to are both set — pass one: to for a given destination, same_minor for the newest patch of from's minor")), nil, nil
	}
	// The project's public release list, read through the cache: it decides
	// whether from carries an image build suffix, the range end of same_minor,
	// and which releases settle what from's branch already fixed. from is sent
	// to /v2/upgrade below in any case. detail:"full" settles nothing, so it
	// reads the list only for a build suffix or same_minor (1.1.0 made one
	// request; independent review, 2026-11-20). A list that cannot be read
	// fails the call only when same_minor needs it; otherwise the answer comes
	// as 1.1.0 gave it, and note says what could not be done.
	var rows []release.Row
	var listErr error
	_, tailLike := releaseline.StripBuildTail(args.From)
	if !full || args.SameMinor || tailLike {
		rows, listErr = h.api.ProjectReleases(ctx, args.Project)
	}
	if listErr != nil && args.SameMinor {
		return errResult(fmt.Errorf("could not read the release list to find the newest release of from's minor (same_minor): %w", listErr)), nil, nil
	}
	var notes []string
	// The build suffix of an image tag, read off as check_stack reads it:
	// /v2/upgrade answered cilium-envoy's v1.37.6-1789133542-<commit> with
	// a 404 (2026-11-20).
	from, readAs := args.From, ""
	if v, ok := releaseline.ReadBuildTail(rows, from); ok {
		from, readAs = v, v
	} else if tailLike && listErr != nil {
		notes = append(notes, fmt.Sprintf("could not read the release list, so a build suffix on from was not read off: %v", listErr))
	}
	// When from's line has no release at all, to stays unset and /v2/upgrade
	// answers with its own error, which lists the tags the project publishes.
	if args.SameMinor && len(releaseline.RowsOnLine(rows, from)) > 0 {
		if t := releaseline.SameMinorTarget(rows, from); t != "" {
			to = t
		} else {
			to = from // (from, from] is empty: /v2/upgrade answers it with no releases
			notes = append(notes, sameMinorNote(from, releaseline.NewestOnLine(rows, from)))
		}
	}
	raw, err := h.api.UpgradeRaw(ctx, args.Project, from, to, lang)
	if err != nil {
		return errResult(err), nil, nil
	}
	if full {
		if kindSet == nil && readAs == "" && len(notes) == 0 {
			return rawResult(raw), nil, nil
		}
		var f *docFilter
		if kindSet != nil {
			f = &docFilter{kinds: kindSet}
		}
		b, err := filterUpgradeReleases(raw, f, readAs, joinNotes(notes...))
		if err != nil {
			return errResult(fmt.Errorf("/v2/upgrade: %w", err)), nil, nil
		}
		return rawResult(b), nil, nil
	}
	var up struct {
		Project      string             `json:"project"`
		From         string             `json:"from"`
		To           string             `json:"to"`
		Releases     []json.RawMessage  `json:"releases"`
		Gaps         *[]releaseline.Gap `json:"gaps"`
		LaterForFrom []json.RawMessage  `json:"later_for_from"`
	}
	if err := json.Unmarshal(raw, &up); err != nil {
		return errResult(fmt.Errorf("/v2/upgrade: %w", err)), nil, nil
	}
	// A JSON object that is not an upgrade answer must not become an empty
	// briefing: /v2/upgrade/.. used to return the API catalog, and it was
	// answered with from "" and no releases — nothing to do (2026-11-15).
	if up.Project == "" || up.Releases == nil {
		return errResult(fmt.Errorf("/v2/upgrade answered without a project and a release range — not an upgrade answer; check the arguments")), nil, nil
	}
	var docs []*release.Doc
	for _, r := range up.Releases {
		d, err := release.Parse(r)
		if err != nil {
			return errResult(fmt.Errorf("/v2/upgrade: %w", err)), nil, nil
		}
		docs = append(docs, d)
	}
	// The server computes gaps; an older server did not send them, so the same
	// rule runs here rather than answering without them.
	gaps := []releaseline.Gap{}
	if up.Gaps != nil {
		gaps = *up.Gaps
	} else {
		points := []string{up.From}
		for _, d := range docs {
			points = append(points, d.Version)
		}
		if to != "" && (len(docs) == 0 || !releaseline.SameTag(docs[len(docs)-1].Version, to)) {
			points = append(points, to)
		}
		gaps = releaseline.Gaps(points)
	}
	// What from's own branch already fixed, as check_stack reads it. Until
	// 1.2.0 this path read only the range, settled nothing, and answered the
	// same upgrade differently from check_stack: containerd v2.2.9 → v2.4.2
	// omitted 42 security changes here and 30 there, with 13 excluded
	// (2026-11-20). The releases at or below from are public documents, read
	// through the same cache; from itself already went upstream. If they cannot
	// be read, nothing is settled, as in 1.1.0, and note says so.
	fromKey := version.NormalizeVersion(up.From)
	fixed, err := map[string]bool(nil), listErr
	if err == nil {
		fixed, err = h.fixedOnBranch(ctx, rows, up.From, lang)
	}
	if err != nil {
		fixed = nil
		notes = append(notes, fmt.Sprintf("could not read the releases at or below %s, so security changes your branch already fixed are not left out (as in 1.1.0): %v", truncate(up.From, 80), err))
	}
	b, _ := briefing.Build(docs, fixed, fromKey)
	b.Narrow(kindSet)
	if up.LaterForFrom == nil {
		up.LaterForFrom = []json.RawMessage{}
	}
	out := map[string]any{
		"project": up.Project,
		"from":    args.From, // as passed; version_read_as says what was checked
		"to":      up.To,
		"summary": b.Summary,
		"gaps":    gaps,
		"privacy": "from and to were sent to the server (/v2/upgrade); use check_stack to keep versions on your side",
		"hint":    briefing.Hint(kindSet),
		// Not on detail:"full": that answer is /v2's own document, and this
		// server adds to it only what the call made necessary.
		"server_version": h.version,
	}
	if readAs != "" {
		out["version_read_as"] = readAs
	}
	if note := joinNotes(notes...); note != "" {
		out["note"] = note
	}
	// Arrays, never null, as the docs say: an empty range came back as
	// "changes": null and "before_upgrade": null (independent review,
	// 2026-11-15), which a client reading the documented shape may not expect.
	changes := b.Changes
	if changes == nil {
		changes = []briefing.Change{}
	}
	out["releases"] = b.Releases
	out["before_upgrade"] = b.Upgrade
	out["changes"] = changes
	out["later_for_from"] = up.LaterForFrom
	out["later_in_range"] = b.LaterInRange
	if len(b.ChangesOmitted) > 0 {
		out["changes_omitted"] = b.ChangesOmitted
	}
	if len(b.ChangesMore) > 0 {
		out["changes_more"] = b.ChangesMore
	}
	if b.ExcludedOnBranch != nil {
		out["excluded_on_branch"] = b.ExcludedOnBranch
	}
	return h.upgradeAnswer(out, args, &b, docs)
}

// joinNotes joins the non-empty notes with "; ".
func joinNotes(parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), "; ")
}

// filterUpgradeReleases applies f to every release of a /v2/upgrade answer and
// passes the rest of the answer on as received; readAs, when set, is added as
// version_read_as.
func filterUpgradeReleases(raw []byte, f *docFilter, readAs, note string) ([]byte, error) {
	fields, err := readObject(raw)
	if err != nil {
		return nil, err
	}
	if readAs != "" {
		v, _ := json.Marshal(readAs)
		fields = append(fields, objField{Key: "version_read_as", Val: v})
	}
	if note != "" {
		v, _ := json.Marshal(note)
		fields = append(fields, objField{Key: "note", Val: v})
	}
	for i, fl := range fields {
		if fl.Key != "releases" || f == nil {
			continue
		}
		var rels []json.RawMessage
		if err := json.Unmarshal(fl.Val, &rels); err != nil {
			return nil, fmt.Errorf("releases: %w", err)
		}
		for j, r := range rels {
			b, err := filterRelease(r, f)
			if err != nil {
				return nil, err
			}
			rels[j] = b
		}
		fields[i].Val = writeArray(rels)
	}
	return writeObject(fields), nil
}
