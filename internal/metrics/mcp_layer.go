package metrics

// Counting at the MCP layer: the requests the MCP server runs.

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/logging"
)

// Middleware counts every request the MCP server runs, with the outcome its
// result shows, and times and sizes tool calls. It claims the POST before it
// runs the request (mcpLayerCounts), so the HTTP layer leaves it alone.
// Notifications are left to the HTTP layer, which answers them 202 whether or
// not they are run before the request ends. self is read from the context
// exactly as the upstream call reads it for X-Ratatosk-Caller. With m nil it
// counts nothing and only passes the request on.
func (m *Metrics) Middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		// A notification is passed on before anything else is read: the SDK
		// runs one after it has answered 202, unordered with anything else.
		// (While the metrics were a package-level variable, reading it there
		// was a read nothing synchronised with; since 2026-11-21 m is fixed
		// when the server is built.)
		if strings.HasPrefix(method, "notifications/") {
			return next(ctx, method, req)
		}
		if m == nil || !m.mcpLayerCounts(req) {
			return next(ctx, method, req)
		}
		start := time.Now()
		res, err := next(ctx, method, req)
		outcome := "ok"
		r, isTool := res.(*mcp.CallToolResult)
		if err != nil || (isTool && r != nil && r.IsError) {
			outcome = "error"
		}
		tool := ""
		if method == "tools/call" {
			if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && p != nil {
				tool = p.Name
			}
		}
		m.countRequest(method, tool, outcome, logging.CallerClassFrom(ctx) == "self")
		if method == "tools/call" {
			size := -1
			if isTool && r != nil {
				size = 0
				for _, c := range r.Content {
					if tc, ok := c.(*mcp.TextContent); ok {
						size += len(tc.Text)
					}
				}
			}
			m.observeTool(tool, time.Since(start), size)
		}
		return res, err
	}
}
