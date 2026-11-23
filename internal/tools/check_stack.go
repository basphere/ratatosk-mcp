package tools

// check_stack and check_upgrade: what an upgrade from the running version
// brings, as a briefing an agent can decide on.
//
// The privacy property check_stack exists for: what it sends upstream depends
// on project slugs only. For each component it asks for the project's release
// list and then for the project's newest releases (fetchSet) — the same
// requests whether the install runs the oldest version on record or the
// newest. Fetching only the releases above the running version would be
// cheaper, and it would tell the server the running version anyway: the first
// release fetched sits right above it. The range is then cut and compared in
// this process. check_upgrade is the other door and says so: it calls
// /v2/upgrade, which takes from/to as query parameters.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
)

// checkStackDef is check_stack as a client lists it.
func checkStackDef() *mcp.Tool {
	return &mcp.Tool{
		Name:        "check_stack",
		Annotations: &mcp.ToolAnnotations{Title: "Check a running stack", ReadOnlyHint: true},
		Description: stackRouteDescription + "Check the user's running component versions. Versions are compared INSIDE THIS SERVER " +
			"PROCESS: for each component it reads the project's recent releases by slug — the same requests whatever " +
			"version you run — so running versions are never sent upstream. Run the server yourself and they never " +
			"leave your infrastructure; on the hosted endpoint they transit server memory only and are not logged. " +
			"Returns, per component, the releases NEWER than the running version up to target_version (default the " +
			"newest release on the running version's release line — a release line is a tag prefix such as flagd/ or " +
			"lts-, not a minor version, so v1.36.5 is checked up to v1.37.x; same_minor:true — on a component, or once " +
			"at the top of the call for every component that does not set same_minor:false or a target_version " +
			"above its running version — stops at the newest patch of the running minor), oldest first: before_upgrade items of every release in range (each with its " +
			"body and folded entries; an item whose text recurs on several releases is listed once, at the newest, " +
			"with also_in naming the others), and the " +
			"changes that need a decision — security, breaking, deprecations, important_fixes — with affects, " +
			"required actions, no_downgrade and removed_in; enhancements, dependency_updates and other are counted " +
			"in summary only. action_required (breaking) and also_required (security) are what the notes say to do; " +
			"no_downgrade means rolling back after the upgrade is not possible; removed_in is when a deprecation " +
			"becomes a removal; security_unclear means two separate checks disagreed on whether the change is a " +
			"security fix and the sources do not settle it (it stays in the kind first chosen); severity is the grade " +
			"the project's own advisory states, null when no grade was found in what was read. changes holds up to " +
			"20 security (most severe first), 20 breaking, 10 deprecations and 10 important_fixes with their body " +
			"(inside a kind, the ones that say what to do first); changes_omitted counts per kind what is not in " +
			"changes (left out on your branch, in excluded_on_branch, and backports folded into an entry's also_in " +
			"aside). changes_more has one line — version, id, kind, title, severity, affects, also_in, and what it asks " +
			"for as marks (action_required, also_required, no_downgrade: true; removed_in) — for every " +
			"security, breaking and deprecation change changes_omitted counts: none of those three kinds is left " +
			"out, however long the range. Important fixes past the 10 in changes are only counted, unless kinds " +
			"names important_fixes: then each has its line too. The answer is not cut to a size; a long range or " +
			"many components make a long answer, and the way to a shorter one is a narrower call — kinds or same_minor " +
			"for the whole call, fewer components, or target_version on a component. " +
			"later_in_range carries only problems (regression_fixed, known_issue_fixed). The briefing carries no " +
			"evidence quotes, except on a before_upgrade item that has no body (evidence: the release-note lines it " +
			"was read from): read the body and evidence of the entries you report with get_release(project, version, " +
			"changes:[id, …]). " +
			"kinds keeps the changes of those kinds. " +
			"summary.counts and summary.warnings add up every release in range (one fix on several " +
			"release lines counts once per release); summary.distinct_advisories counts each advisory once. " +
			"Security changes citing no advisory id are not in distinct_advisories (summary.counts.security has them), " +
			"and summary.counts and summary.warnings include the changes excluded_on_branch counts (it names each " +
			"advisory once, the most severe first); warnings.critical_excluded_on_branch, in summary and on a release, " +
			"says how many of warnings.critical are among them, so critical minus that is what the upgrade newly fixes. " +
			"affects decides who a change touches: status stated or inferred means it applies ONLY " +
			"IF that holds in this setup — check before you report it; not_stated means the release notes name no " +
			"narrower scope. gaps lists where version numbers skip (releases in between may be unanalyzed); " +
			"later_for_running is " + laterNote("the running version") + " later_in_range lists the same for every " +
			"release in range. A security change whose " +
			"advisories the running version's own branch already fixed at or below it is excluded and counted in " +
			"note and excluded_on_branch; the same advisories fixed on several branches inside the range collapse into the earliest, with " +
			"also_in naming the rest. A later release of a branch that cites them again is a follow-up fix and is " +
			"listed on its own. Line-aware: a repository can publish separate products or channels " +
			"(containerd api/, Flatcar lts vs stable, openfeature flagd vs core) with NO version order between " +
			"lines, so only the running version's line is compared — pass the tag as published, prefix included " +
			"(\"flagd/v0.16.1\", \"lts-4081.3.9\"); an image build suffix after the version (v1.37.6-1789133542-<commit>) " +
			"is read without it, and version_read_as says so. Pass version_source per component (where you read the version, " +
			"e.g. a daemonset image tag, or that the user stated it): it is echoed back as an audit trail. This " +
			"server cannot see your environment, so it cannot verify a version; a running version older than every " +
			"release on record is flagged in note. Components it could not check (unknown project, unparseable " +
			"version, no releases on that line, a running version newer than every analyzed release on its line — not " +
			"analyzed yet, or misread) are listed in could_not_check — tracked:false means ratatosk knows no " +
			"project by that slug: check it with list_projects (case and surrounding spaces are ignored); a project not " +
			"listed there is NOT covered by ratatosk, so the absence of changes is no coverage, not safety. detail:\"full\" returns " +
			"every release in range verbatim instead. server_version is the version of the server that built the " +
			"answer. " + DataNote,
		InputSchema: checkStackSchema(),
	}
}

type componentReport struct {
	Project        string `json:"project"`
	RunningVersion string `json:"running_version"`
	// VersionReadAs (1.2.0) is the version checked when it is not the one
	// passed: an image build suffix was dropped.
	VersionReadAs string `json:"version_read_as,omitempty"`
	VersionSource string `json:"version_source,omitempty"`
	TargetVersion string `json:"target_version,omitempty"`
	Tracked       *bool  `json:"tracked,omitempty"`
	Note          string `json:"note,omitempty"`
	briefing.Briefing
	Gaps            []releaseline.Gap `json:"gaps,omitempty"`
	NotRead         []string          `json:"releases_not_read,omitempty"`
	LaterForRunning []json.RawMessage `json:"later_for_running,omitempty"`
	Full            []json.RawMessage `json:"releases_full,omitempty"` // detail:"full" only
	FullOmitted     int               `json:"releases_full_omitted,omitempty"`
	// The documents the briefing was built from, and every one the call read
	// for the component: a route reads its steps' advisories and the
	// project's release notes page from them (stackAnswer).
	rangeDocs []*release.Doc
	allDocs   map[string]*release.Doc
}

// untrackedReason answers a component whose project is neither a slug nor an
// image alias. It leads with checking the slug, because a name in another form
// is the likelier cause; the warning that matters if it really is untracked
// stays: no coverage is not safety.
const untrackedReason = "unknown project name — not a slug ratatosk knows: check the slug with list_projects " +
	"(the project may be listed under another slug); if it is not listed, ratatosk does NOT track it, so no " +
	"changes here means no coverage, not safety"

type uncheckable struct {
	Project string `json:"project"`
	Version string `json:"version"`
	Reason  string `json:"reason"`
}

func (h *handlers) checkStackTool(ctx context.Context, req *mcp.CallToolRequest, args checkStackArgs) (*mcp.CallToolResult, any, error) {
	ctx = logging.RequestContext(ctx, "check_stack", req)
	if len(args.Components) == 0 {
		return errResult(fmt.Errorf("components is required — %s", componentsShapeHint)), nil, nil
	}
	if len(args.Components) > maxComponents {
		return errResult(fmt.Errorf("too many components: %d (max %d)", len(args.Components), maxComponents)), nil, nil
	}
	// A component without a project is refused, never scanned. Upstream reads a
	// missing project filter as "every project", so one such component used to
	// page the whole corpus: a model that sent project_slug got 2,435 changes
	// back in a 676,771-character answer and timed out re-reading it (hub,
	// 2026-09-28).
	for i, comp := range args.Components {
		if strings.TrimSpace(comp.Project) != "" {
			continue
		}
		sent := comp.sentKeys
		if sent == "" {
			sent = "none"
		}
		return errResult(fmt.Errorf("component %d has no project (keys received: %s) — %s", i+1, sent, componentsShapeHint)), nil, nil
	}
	full, err := detailArg(args.Detail)
	if err != nil {
		return errResult(err), nil, nil
	}
	kindSet, err := kindsArg(args.Kinds, !full)
	if err != nil {
		return errResult(err), nil, nil
	}
	// Two range ends at once: whichever one won, the caller would read the
	// range as the other one's.
	for i, comp := range args.Components {
		if comp.SameMinor && strings.TrimSpace(comp.TargetVersion) != "" {
			return errResult(fmt.Errorf("component %d (%s) sets both same_minor and target_version — pass one: target_version for a given destination, same_minor for the newest patch of the running minor", i+1, truncate(comp.Project, 80))), nil, nil
		}
	}
	// same_minor at the top of the call (1.3.0) is the default of every
	// component. What a component says for itself wins — same_minor:false, or
	// a target_version — and its note says so; whether a target_version can be
	// used is known only once the component's releases are read, so that case
	// is settled there (stackRun.check).
	if args.SameMinor {
		for i := range args.Components {
			switch comp := &args.Components[i]; {
			case comp.sameMinorSaid && !comp.SameMinor:
				comp.callSameMinor = callSameMinorOff
			case strings.TrimSpace(comp.TargetVersion) != "":
				comp.callSameMinor = callSameMinorOrTarget
			default:
				comp.SameMinor = true
			}
		}
	}

	roster, err := h.api.Projects(ctx)
	if err != nil {
		return errResult(fmt.Errorf("could not read the project list: %w", err)), nil, nil
	}
	slugs := map[string]bool{}
	aliasOf := map[string]string{}
	for _, p := range roster {
		slugs[p.Slug] = true
		for _, a := range p.ImageAliases {
			aliasOf[strings.ToLower(a)] = p.Slug
		}
	}

	// The same component listed twice gets one report. Each copy used to be
	// checked and answered again: a hundred copies of one project made one call
	// carry forty identical reports (independent review, 2026-11-11).
	times := map[stackComponent]int{}
	comps := make([]stackComponent, 0, len(args.Components))
	for _, comp := range args.Components {
		comp.Project = slugArg(comp.Project)
		comp.sentKeys = ""
		comp.sameMinorSaid = false // read above; "same_minor": false and no same_minor are one component
		if times[comp]++; times[comp] == 1 {
			comps = append(comps, comp)
		}
	}

	run := &stackRun{h: h, slugs: slugs, aliasOf: aliasOf, times: times, full: full, lang: answerLang, kinds: kindSet,
		reports: make([]componentReport, 0, len(comps))}
	for _, comp := range comps {
		if err := run.check(ctx, comp); err != nil {
			return errResult(err), nil, nil
		}
	}

	out := map[string]any{
		"components":     run.reports,
		"privacy":        "versions were compared in this process; upstream requests named project slugs only (each project's recent releases, the same whatever version runs)",
		"server_version": h.version,
	}
	if len(run.cannot) > 0 {
		out["could_not_check"] = run.cannot
	}
	if full {
		res, err := jsonResult(out)
		return res, nil, err
	}
	out["hint"] = briefing.Hint(kindSet)
	return h.stackAnswer(out, run, args)
}
