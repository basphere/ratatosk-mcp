package tools

// list_releases: recent releases as one-line rows, by release date.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/upstream"
)

// listReleasesDef is list_releases as a client lists it.
func listReleasesDef() *mcp.Tool {
	return &mcp.Tool{
		Name:        "list_releases",
		Annotations: &mcp.ToolAnnotations{Title: "Recent releases", ReadOnlyHint: true},
		Description: "Recent releases, newest first, as one-line summaries (project, version, released_at, counts " +
			"per kind, warnings). Use it for 'recent releases of X' or 'which releases this month had security " +
			"changes'. Filter by project, by kind (only releases with at least one change of that kind) and by since " +
			"(a date). Drill into a row with get_release(project, version). Rows are listed by release date, so what " +
			"was learned after a release (an advisory published later, a defect a later version fixed) does not show " +
			"here: check_stack's later_for_running, get_release's later and ratatosk.io's /v2/events carry it.",
	}
}

type listReleasesArgs struct {
	Project string `json:"project,omitempty" jsonschema:"project slug, e.g. istio; omit for every project"`
	Kind    string `json:"kind,omitempty" jsonschema:"only releases with at least one change of this kind: security|breaking|deprecations|important_fixes|enhancements|dependency_updates|other"`
	Since   string `json:"since,omitempty" jsonschema:"YYYY-MM-DD — released on or after this date (UTC)"`
	Before  string `json:"before,omitempty" jsonschema:"paging: pass next_before from the previous answer"`
	Limit   int    `json:"limit,omitempty" jsonschema:"how many releases, default 10, max 50"`
	Lang    string `json:"lang,omitempty" jsonschema:"en (default), ko or ja — the language of the website links in url"`
}

func (h *handlers) listReleasesTool(ctx context.Context, req *mcp.CallToolRequest, args listReleasesArgs) (*mcp.CallToolResult, any, error) {
	ctx = logging.RequestContext(ctx, "list_releases", req)
	lang, err := langArg(args.Lang)
	if err != nil {
		return errResult(err), nil, nil
	}
	if args.Kind != "" && !containsStr(release.Kinds, args.Kind) {
		return errResult(fmt.Errorf("kind must be one of %s", strings.Join(release.Kinds, "|"))), nil, nil
	}
	if args.Since != "" {
		if _, err := time.Parse("2006-01-02", args.Since); err != nil {
			return errResult(fmt.Errorf("since must be a date, YYYY-MM-DD")), nil, nil
		}
	}
	// An unknown slug is an error, not an empty list: "no releases" for a name
	// that was merely spelled differently reads as "nothing happened". The
	// roster is cached, so this costs one upstream request per five minutes.
	project := slugArg(args.Project)
	if project != "" {
		roster, err := h.api.Projects(ctx)
		if err != nil {
			return errResult(fmt.Errorf("could not read the project list: %w", err)), nil, nil
		}
		if !slices.ContainsFunc(roster, func(p upstream.Project) bool { return p.Slug == project }) {
			return errResult(unknownProject(project)), nil, nil
		}
	}
	// 10, not the upstream 50: a row is small, but this answer stays in the
	// agent's context for the rest of its session.
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	raw, err := h.api.ReleasesRaw(ctx, upstream.ReleasesQuery{Project: project, Kind: args.Kind, Since: args.Since, Before: args.Before, Limit: limit, Lang: lang}, false)
	if err != nil {
		return errResult(err), nil, nil
	}
	return rawResult(raw), nil, nil
}
