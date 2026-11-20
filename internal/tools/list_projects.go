package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/logging"
)

// listProjectsDef is list_projects as a client lists it.
func listProjectsDef() *mcp.Tool {
	return &mcp.Tool{
		Name:        "list_projects",
		Annotations: &mcp.ToolAnnotations{Title: "List tracked projects", ReadOnlyHint: true},
		Description: "Every project ratatosk tracks: slug (the canonical id all other tools take), name, " +
			"tier (graduated|incubating), category, latest release, analyzed_releases; image_aliases where a project runs " +
			"under other names in clusters (an image or workload matching an alias belongs to that project " +
			"at the version its tag says), and cluster_core:true on the cluster substrate (control plane, " +
			"datastore, DNS, runtime, CNI/dataplane) — every cluster_core project present in a cluster " +
			"belongs in its check_stack call. Some cluster_core entries carry a visibility hint (how the " +
			"component is observed and where it can legitimately be unreadable — e.g. etcd may live outside " +
			"the k8s API): an unreadable one is reported as unchecked, never guessed. Small response, no arguments — " +
			"call this FIRST when you are unsure of a slug instead of guessing (a wrong slug shows up " +
			"as tracked:false in check_stack).",
	}
}

type listProjectsArgs struct{}

func (h *handlers) listProjectsTool(ctx context.Context, req *mcp.CallToolRequest, args listProjectsArgs) (*mcp.CallToolResult, any, error) {
	ctx = logging.RequestContext(ctx, "list_projects", req)
	raw, err := h.api.ProjectsRaw(ctx, false)
	if err != nil {
		return errResult(err), nil, nil
	}
	return rawResult(raw), nil, nil
}
