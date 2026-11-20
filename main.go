// ratatosk-mcp — read-only MCP server for the ratatosk agent API v2.
//
// Exposes what changed in CNCF project releases as MCP tools, in the same shape
// the website shows: before-upgrade checks plus changes in seven kinds, each
// with release-note evidence. check_stack is the privacy loop ("notify, don't
// tell"): the caller's running versions are compared INSIDE this process, and
// what goes upstream depends only on project slugs — see
// internal/tools/check_stack.go.
//
// This file reads the configuration, builds the server and serves it; the
// rest is in internal/ (CONTRIBUTING.md "Code layout"). Config: RATATOSK_URL
// (default https://ratatosk.io); the rest is listed in docs/install.en.md
// "Configuration reference" and read in internal/config.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/audit"
	"github.com/basphere/ratatosk-mcp/internal/config"
	"github.com/basphere/ratatosk-mcp/internal/httpfront"
	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/mcpserver"
	"github.com/basphere/ratatosk-mcp/internal/metrics"
	"github.com/basphere/ratatosk-mcp/internal/upstream"
)

// buildVersion is stamped by the release workflow via
// -ldflags "-X main.buildVersion=<tag>"; local builds report "dev".
// It stays in package main and is handed down from here: -X names a variable
// by its package path, and the linker ignores a path it does not find without
// an error, so a variable moved into another package would read "dev" in
// every release.
var buildVersion = "dev"

func main() {
	cfg := config.Read(os.Getenv)
	logging.Setup(cfg.LogLevel)
	aud := audit.New(cfg.AuditMode, os.Stderr)
	// Two transports, one binary: stdio for local agents (Claude Code, MCP
	// inspector), streamable HTTP for the in-cluster Service the Helm chart
	// deploys — agents in the cluster connect to http://<svc>:8080/mcp.
	if cfg.HTTPAddr != "" {
		serveHTTP(cfg, aud)
		return
	}
	serveStdio(cfg, aud)
}

// serveHTTP serves streamable HTTP at cfg.HTTPAddr, with /metrics on a
// listener of its own when asked for, until the server fails.
func serveHTTP(cfg config.Config, aud *audit.Auditor) {
	addr := cfg.HTTPAddr
	// Metrics, when asked for, listen on an address of their own: a proxy
	// that forwards this server's port (the hosted Caddy forwards /mcp)
	// cannot expose them, and the /mcp mux has no route to them.
	metricsField := "off"
	var mx *metrics.Metrics
	if cfg.MetricsAddr != "" {
		if cfg.MetricsAddr == addr {
			slog.Error("MCP_METRICS_ADDR must differ from MCP_HTTP_ADDR: metrics are served on a listener of their own")
			os.Exit(1)
		}
		mx = metrics.New(buildVersion)
		ln, err := net.Listen("tcp", cfg.MetricsAddr)
		if err != nil {
			slog.Error("metrics listener failed", "kind", logging.ErrKind(err))
			os.Exit(1)
		}
		go func() {
			if err := metrics.Server(mx).Serve(ln); err != nil {
				slog.Error("metrics server exited", "kind", logging.ErrKind(err))
				os.Exit(1)
			}
		}()
		metricsField = cfg.MetricsAddr + "/metrics"
	}
	// Built after the metrics, which it counts into.
	server := newServer(cfg, aud, mx)
	var rl *httpfront.RateLimiter
	if cfg.RateLimitPerMin > 0 {
		rl = httpfront.NewRateLimiter(cfg.RateLimitPerMin)
	}
	mux := httpfront.Handler(httpfront.MCPHandler(server, cfg.Stateless), httpfront.Options{
		RateLimiter: rl,
		MaxInflight: cfg.MaxInflight,
		SelfCallers: cfg.SelfCallers,
		Metrics:     mx,
	})
	mode := "stateful"
	if cfg.Stateless {
		mode = "stateless"
	}
	// The limit is a configured number, not a caller attribute — safe to log.
	rateField := "off"
	if rl != nil {
		rateField = strconv.Itoa(cfg.RateLimitPerMin) + "/min per caller"
	}
	inflightField := "off"
	if cfg.MaxInflight > 0 {
		inflightField = strconv.Itoa(cfg.MaxInflight)
	}
	slog.Info("listening", "transport", "http", "addr", addr+"/mcp", "mode", mode, "upstream", cfg.UpstreamURL, "version", buildVersion, "rate_limit", rateField, "max_inflight", inflightField, "metrics", metricsField)
	if err := httpfront.Server(addr, mux).ListenAndServe(); err != nil {
		slog.Error("http server exited", "kind", logging.ErrKind(err))
		os.Exit(1)
	}
}

// serveStdio serves one caller on stdin/stdout until it goes away.
func serveStdio(cfg config.Config, aud *audit.Auditor) {
	if cfg.MetricsAddr != "" {
		// One stdio process serves one caller and is started by that caller;
		// a port per process would collide as soon as a second client starts one.
		slog.Warn("MCP_METRICS_ADDR is ignored on stdio: metrics are served in HTTP mode only")
	}
	server := newServer(cfg, aud, nil)
	slog.Info("listening", "transport", "stdio", "upstream", cfg.UpstreamURL, "version", buildVersion)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		slog.Error("stdio session ended with error", "kind", logging.ErrKind(err))
		os.Exit(1)
	}
	slog.Info("stdio session ended")
}

// newServer is the MCP server both transports serve, reading /v2 at the
// configured address. docs_test.go lists the tools through it — exactly as a
// client sees them.
func newServer(cfg config.Config, aud *audit.Auditor, m *metrics.Metrics) *mcp.Server {
	return mcpserver.New(mcpserver.Options{
		Version: buildVersion,
		API:     upstream.New(cfg.UpstreamURL, buildVersion, cfg.CacheMaxBytes, m),
		Audit:   aud,
		Metrics: m,
	})
}
