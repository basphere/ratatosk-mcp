// Package mcpserver builds the MCP server both transports serve: the
// instructions every client receives on initialize, the six tools
// (internal/tools), and the receiving middleware around them — the audit
// stream, the per-call deadline and the request counters — in the order that
// decides what each one sees.
//
// It is assembly only: what a tool answers is decided in internal/tools, what
// an HTTP request meets before it gets here in internal/httpfront.
package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/audit"
	"github.com/basphere/ratatosk-mcp/internal/metrics"
	"github.com/basphere/ratatosk-mcp/internal/tools"
	"github.com/basphere/ratatosk-mcp/internal/upstream"
)

// toolTimeout bounds one tool call. The HTTP server cuts a response at 120 s
// (WriteTimeout), and before this bound a slow upstream ran a stack check into
// that cut: the caller got an empty reply instead of an answer saying what was
// not checked. 90 s leaves room to write the answer. Tests shorten it through
// Options.ToolTimeout.
const toolTimeout = 90 * time.Second

// instructions are what a client receives on initialize.
const instructions = "Data source: the public ratatosk.io agent API v2 — what changed in CNCF project releases, read by AI " +
	"from the release notes and the documents they link; verify critical decisions against the evidence quotes and " +
	"release_notes_url in get_release (terms: https://ratatosk.io/terms). A release has before_upgrade " +
	"(prerequisites, known_issues, upgrade_guide) and changes in seven kinds: security, breaking, deprecations, " +
	"important_fixes, enhancements, dependency_updates, other. affects says who a change touches and whether the " +
	"release notes stated it (status stated | inferred | not_stated | not_read). " + tools.DataNote + " " +
	"Rate limits: the upstream API allows 1200 requests/minute per IP, and the hosted endpoint additionally " +
	"allows each caller 60 MCP requests/minute (each tool call is one). One tool call can be many upstream " +
	"requests (check_stack reads every recent release of each component's project), so prefer one check_stack " +
	"for a whole stack over many per-project calls."

// Options are what the server is built from.
type Options struct {
	// Version is the server's version in serverInfo (main.buildVersion).
	Version string
	// API is the /v2 client the tools read through.
	API *upstream.Client
	// Audit writes the audit stream (MCP_AUDIT); nil is off.
	Audit *audit.Auditor
	// Metrics counts the requests the server runs; nil is off.
	Metrics *metrics.Metrics
	// ToolTimeout bounds one tool call; 0 is toolTimeout (90 s).
	ToolTimeout time.Duration
}

// New builds the server: the six tools and the receiving middleware. The
// server's tools are what a client lists, so docs_test.go (at the repository
// root) lists them through it — the docs are checked against this, not
// against a second hand-kept list.
func New(o Options) *mcp.Server {
	timeout := o.ToolTimeout
	if timeout == 0 {
		timeout = toolTimeout
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "ratatosk", Version: o.Version}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	tools.Register(server, o.API)

	// Audit stream (P3): observes tools/call when MCP_AUDIT is set; the
	// identity function otherwise. See internal/audit and docs/logging-design.md.
	server.AddReceivingMiddleware(o.Audit.Middleware)
	server.AddReceivingMiddleware(deadlineMiddleware(timeout))
	// Added last, so it is the outermost: a tool call is timed from the
	// moment it arrives to its answer. The identity while metrics are off.
	server.AddReceivingMiddleware(o.Metrics.Middleware)
	return server
}

// deadlineMiddleware puts timeout on every tool call.
func deadlineMiddleware(timeout time.Duration) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return next(ctx, method, req)
		}
	}
}
