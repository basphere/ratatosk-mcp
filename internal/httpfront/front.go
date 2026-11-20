// Package httpfront is everything an HTTP request meets before the MCP
// server: the caller class, the per-caller rate limit, the batch refusal, the
// in-flight cap, the mux that serves /mcp and /healthz, and the http.Server
// timeouts. Off-by-default limits are what a self-hosted, single-tenant
// install wants; the hosted endpoint turns them on (internal/config).
//
// The privacy line it holds: the caller's address is read here — as a rate
// limit key and to tell the operator's own calls apart — and goes no further.
// It is never logged, never forwarded upstream; only the one-bit caller class
// travels on (internal/logging).
package httpfront

import (
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/metrics"
)

// inflightWait is how long a request over the in-flight cap waits for a slot
// before it is answered 503 (docs/install.en.md: "wait up to 10 s").
const inflightWait = 10 * time.Second

// Options are the limits and counters of the chain. The zero value is the
// self-hosted default: every limit off, metrics off.
type Options struct {
	// RateLimiter limits each caller (MCP_RATE_LIMIT_PER_MIN); nil is off.
	RateLimiter *RateLimiter
	// MaxInflight caps the requests worked on at once (MCP_MAX_INFLIGHT); 0
	// is off.
	MaxInflight int
	// InflightWait is how long an over-cap request waits; 0 is 10 s. The
	// server leaves it 0. It is exported for the counting tests in
	// internal/metrics, which need an in-flight refusal within a test's
	// time (one asserts an answer within 2 s).
	InflightWait time.Duration
	// SelfCallers are the operator's own addresses (MCP_SELF_CALLERS).
	SelfCallers []string
	// Metrics counts what the chain refuses; nil is off.
	Metrics *metrics.Metrics
}

// MCPHandler serves server over streamable HTTP.
//
// MCP_HTTP_STATELESS=1 serves every POST on a temporary session: no
// Mcp-Session-Id round-trip, GET/DELETE answer 405 — and, per go-sdk v1.7.0,
// it is the only HTTP mode that speaks the 2026-07-28 revision (stdio speaks
// it regardless). Default stays stateful so existing in-cluster clients see no
// behavior change; hosted endpoints and horizontally-scaled installs are what
// the flag is for.
func MCPHandler(server *mcp.Server, stateless bool) http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			Stateless: stateless,
			// Stateful mode keeps a session per client, and the SDK never
			// closes idle ones when this is zero — an install that reconnects
			// often grows memory until it dies, with no attacker involved.
			// Stateless mode (what the 2026-07-28 revision needs) allocates
			// nothing, so this only guards the backward-compatibility path.
			// Expiry is the protocol's own mechanism: a client returning with
			// a closed session id re-initializes.
			SessionTimeout: 30 * time.Minute,
		})
}

// Handler is everything the MCP listener serves: /mcp through the chain to
// mcpHandler, and /healthz.
//
// Per-caller limiting sits in front of the MCP handler, not upstream: this
// process is the one that can tell hosted callers apart, and the upstream API
// deliberately cannot. The in-flight cap sits behind the rate limiter: an
// over-limit caller is turned away without taking a slot. A batch is refused
// behind the limiter (the attempt counts as a request) and before it can take
// a slot.
func Handler(mcpHandler http.Handler, o Options) http.Handler {
	if o.InflightWait == 0 {
		o.InflightWait = inflightWait
	}
	return mux(chain(o, mcpHandler))
}

// chain is everything an HTTP request passes before the MCP handler:
// caller class, the per-caller rate limit, the batch refusal, the in-flight
// cap. A function of its own so a test can hold the order to account — the
// batch refusal is only worth something between the limiter and the handler.
//
// The metrics layer sits right inside the caller class (it labels self from
// it) and outside every refusal, so it sees each one; it is the identity while
// metrics are off.
func chain(o Options, handler http.Handler) http.Handler {
	return callerClassMiddleware(o.SelfCallers, o.Metrics.HTTPMiddleware(rateLimitMiddleware(o.RateLimiter, rejectBatchMiddleware(inflightMiddleware(o.MaxInflight, o.InflightWait, handler)))))
}

// mux is everything the MCP listener serves: /mcp and /healthz. Metrics are
// not here — they have a listener of their own.
func mux(mcpHandler http.Handler) *http.ServeMux {
	m := http.NewServeMux()
	m.Handle("/mcp", mcpHandler)
	m.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return m
}

// Server is the MCP listener's server.
//
// Explicit timeouts: this is an unauthenticated public endpoint, and the
// zero-value http.Server leaves every timeout unset (unbounded), so a client
// that dribbles a request header one byte at a time holds a goroutine/FD
// forever (slowloris) — and does it below the rate limiter, which only counts
// requests that finish their headers. WriteTimeout is generous because a large
// check_stack legitimately streams a while.
func Server(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}
