// Package metrics is the request counters for the HTTP transport, served on a
// listener of their own (1.1.0). Off unless MCP_METRICS_ADDR is set: the same binary is what people
// self-host, and a self-hosted install that did not ask for a port gets none.
//
// Why they exist: the hosted endpoint's upstream (the web /v2 counter) sees
// tool calls only as the /v2 requests they turn into, and nothing at all of a
// call refused here — rate-limited, or turned away by the in-flight cap — or
// of an upstream request that never arrived (timeout, connection failure). The
// hub asked for calls by tool and outcome (2026-11-18); how long a tool call
// takes and how large its answer is had no measurement anywhere (an outside
// agent's 187 KB check_stack answer went over its own output limit the same
// day).
//
// The privacy line is the one the README draws: nothing a caller sends
// becomes a label. Every label value comes from a closed list — the six tool
// names and "unknown", four methods, four outcomes, and self as one bit, the
// same judgement X-Ratatosk-Caller carries. No address, user agent, session,
// argument or project name, so the set of series is fixed at startup and
// cannot grow with traffic.
//
// Where a request is counted: every POST to /mcp exactly once. A request the
// MCP server ran is counted by Middleware, which sees its result (an
// isError answer is an error). Every other POST is counted by
// HTTPMiddleware: refused by the rate limiter, the in-flight cap or the
// batch refusal, rejected by the SDK (a bad header, or params it could not
// read: answered 200 with a JSON-RPC error and never run), or carrying no
// request at all (a notification, answered 202). Which layer counts a POST is
// settled by a claim on its tally (httpTally.claim), so the two never count
// the same POST and neither misses one; status codes are not used to guess
// (final review, 2026-11-18: three malformed bodies answered 200 went
// uncounted that way).
//
// The two counting layers live here together because they share one tally
// per POST (http_layer.go). A *Metrics is built once and handed to both — the
// HTTP chain (internal/httpfront) and the MCP server (internal/mcpserver) —
// and to the upstream client, which counts what it sends. nil is off.
package metrics

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/basphere/ratatosk-mcp/internal/logging"
)

// toolNames is the closed list of tool labels. It must equal the tools the
// server registers (internal/tools; TestMetricsToolListMatchesServer);
// anything else a caller names is "unknown".
var toolNames = []string{"check_stack", "check_upgrade", "get_release", "list_projects", "list_releases", "search"}

var (
	methodLabels     = []string{"initialize", "other", "tools/call", "tools/list"}
	requestOutcomes  = []string{"busy", "error", "ok", "rate_limited"}
	upstreamOutcomes = []string{"canceled", "error", "ok", "timeout", "transport"}
	selfLabels       = []string{"false", "true"}
)

// Buckets. Duration: 182 normal calls on the hosted endpoint, 2026-11-11 to
// 11-18, took 1.49 s at most; a call ends at 90 s (toolTimeout). Bytes: agent
// clients cap a tool answer in the tens to low hundreds of KB, so the steps
// around 64–256 KiB are the ones that say how often an answer goes over.
var (
	durationBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 90}
	bytesBuckets    = []float64{1 << 10, 4 << 10, 16 << 10, 32 << 10, 64 << 10, 128 << 10, 256 << 10, 512 << 10, 1 << 20, 2 << 20, 4 << 20}
)

func toolLabel(name string) string {
	if slices.Contains(toolNames, name) {
		return name
	}
	return "unknown"
}

func methodLabel(m string) string {
	switch m {
	case "initialize", "tools/list", "tools/call":
		return m
	}
	return "other"
}

func toolLabels() []string {
	return append(slices.Clone(toolNames), "unknown")
}

type requestKey struct{ method, tool, outcome, self string }
type upstreamKey struct{ tool, outcome string }

type histogram struct {
	bounds []float64
	counts []uint64 // per bucket, not cumulative; one more for +Inf
	sum    float64
	n      uint64
}

func newHistogram(bounds []float64) *histogram {
	return &histogram{bounds: bounds, counts: make([]uint64, len(bounds)+1)}
}

func (h *histogram) observe(v float64) {
	i := sort.SearchFloat64s(h.bounds, v) // first bound >= v: le is inclusive
	h.counts[i]++
	h.sum += v
	h.n++
}

// Metrics is the process's metrics. A nil *Metrics is off (the default), and
// every method is a no-op on nil.
type Metrics struct {
	version   string
	mu        sync.Mutex
	requests  map[requestKey]uint64
	upstream  map[upstreamKey]uint64
	duration  map[string]*histogram
	respBytes map[string]*histogram

	tallies  sync.Map // tally id → *httpTally, while its POST is being served
	tallySeq atomic.Uint64
}

// New lays every known label combination down at zero. A series that
// first appears with the value 1 has no 0 before it, so increase() over a
// window that starts there misses the first call after every restart — the
// web /v2 counter lost them that way (2026-11-16).
func New(version string) *Metrics {
	m := &Metrics{
		version:   version,
		requests:  map[requestKey]uint64{},
		upstream:  map[upstreamKey]uint64{},
		duration:  map[string]*histogram{},
		respBytes: map[string]*histogram{},
	}
	for _, method := range methodLabels {
		tools := []string{""}
		if method == "tools/call" {
			tools = toolLabels()
		}
		for _, tool := range tools {
			for _, o := range requestOutcomes {
				for _, s := range selfLabels {
					m.requests[requestKey{method, tool, o, s}] = 0
				}
			}
		}
	}
	for _, tool := range toolLabels() {
		for _, o := range upstreamOutcomes {
			m.upstream[upstreamKey{tool, o}] = 0
		}
		m.duration[tool] = newHistogram(durationBuckets)
		m.respBytes[tool] = newHistogram(bytesBuckets)
	}
	return m
}

// countRequest counts one MCP request. Raw values go in; only closed-list
// labels come out, so no caller-chosen text can become a series.
func (m *Metrics) countRequest(method, tool, outcome string, self bool) {
	if m == nil {
		return
	}
	k := requestKey{method: methodLabel(method), outcome: outcome, self: strconv.FormatBool(self)}
	if k.method == "tools/call" {
		k.tool = toolLabel(tool)
	}
	if !slices.Contains(requestOutcomes, k.outcome) {
		k.outcome = "error"
	}
	m.mu.Lock()
	m.requests[k]++
	m.mu.Unlock()
}

// CountUpstream counts one request this process sent to /v2 (cache hits are
// not requests). outcome is ok, error (any answer but a 200) or, for a request
// that got no answer, UpstreamOutcome.
func (m *Metrics) CountUpstream(tool, outcome string) {
	if m == nil {
		return
	}
	k := upstreamKey{tool: toolLabel(tool), outcome: outcome}
	if !slices.Contains(upstreamOutcomes, k.outcome) {
		k.outcome = "transport"
	}
	m.mu.Lock()
	m.upstream[k]++
	m.mu.Unlock()
}

// observeTool records one tool call: how long it took, and how large its
// answer was when there is one (bytes < 0: no tool result, e.g. a protocol
// error for an unknown tool).
func (m *Metrics) observeTool(tool string, d time.Duration, bytes int) {
	if m == nil {
		return
	}
	t := toolLabel(tool)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.duration[t].observe(d.Seconds())
	if bytes >= 0 {
		m.respBytes[t].observe(float64(bytes))
	}
}

// UpstreamOutcome classifies a /v2 request that got no answer. canceled is
// the call going away (the caller left, or check_stack stopped reading after
// another document failed), not a failure of the upstream.
func UpstreamOutcome(err error) string {
	switch logging.ErrKind(err) {
	case "timeout":
		return "timeout"
	case "canceled":
		return "canceled"
	}
	return "transport"
}

// ---------------------------------------------------------------------------
// exposition (Prometheus text format 0.0.4)
// ---------------------------------------------------------------------------

func escapeLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

func formatBound(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// writeTo writes every series, sorted, so two scrapes of the same state are
// byte-identical. The exposition is formatted under the lock and written after
// it is released: written under it, a scraper that reads slowly held every
// counter of every request waiting on its network write (final review,
// 2026-11-18).
func (m *Metrics) writeTo(w io.Writer) error {
	_, err := w.Write(m.exposition())
	return err
}

// exposition formats every series under the lock.
func (m *Metrics) exposition() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b bytes.Buffer

	b.WriteString("# HELP ratatosk_mcp_build_info The version of this server; the value is always 1.\n")
	b.WriteString("# TYPE ratatosk_mcp_build_info gauge\n")
	fmt.Fprintf(&b, "ratatosk_mcp_build_info{version=\"%s\"} 1\n", escapeLabel(m.version))

	b.WriteString("# HELP ratatosk_mcp_requests_total MCP requests by method, tool, outcome and whether the caller is one of the operator's own addresses. No arguments, addresses or sessions.\n")
	b.WriteString("# TYPE ratatosk_mcp_requests_total counter\n")
	rk := make([]requestKey, 0, len(m.requests))
	for k := range m.requests {
		rk = append(rk, k)
	}
	sort.Slice(rk, func(i, j int) bool {
		a, c := rk[i], rk[j]
		if a.method != c.method {
			return a.method < c.method
		}
		if a.tool != c.tool {
			return a.tool < c.tool
		}
		if a.outcome != c.outcome {
			return a.outcome < c.outcome
		}
		return a.self < c.self
	})
	for _, k := range rk {
		fmt.Fprintf(&b, "ratatosk_mcp_requests_total{method=\"%s\",outcome=\"%s\",self=\"%s\",tool=\"%s\"} %d\n",
			escapeLabel(k.method), escapeLabel(k.outcome), escapeLabel(k.self), escapeLabel(k.tool), m.requests[k])
	}

	b.WriteString("# HELP ratatosk_mcp_upstream_requests_total Requests sent to the upstream /v2 API, by the tool that sent them and their outcome. Cache hits are not requests.\n")
	b.WriteString("# TYPE ratatosk_mcp_upstream_requests_total counter\n")
	uk := make([]upstreamKey, 0, len(m.upstream))
	for k := range m.upstream {
		uk = append(uk, k)
	}
	sort.Slice(uk, func(i, j int) bool {
		if uk[i].tool != uk[j].tool {
			return uk[i].tool < uk[j].tool
		}
		return uk[i].outcome < uk[j].outcome
	})
	for _, k := range uk {
		fmt.Fprintf(&b, "ratatosk_mcp_upstream_requests_total{outcome=\"%s\",tool=\"%s\"} %d\n", escapeLabel(k.outcome), escapeLabel(k.tool), m.upstream[k])
	}

	writeHistograms(&b, "ratatosk_mcp_request_duration_seconds", "Time a tool call took in this server, from the request to the answer.", m.duration)
	writeHistograms(&b, "ratatosk_mcp_response_bytes", "Size in bytes of a tool call's answer text, as the agent receives it.", m.respBytes)
	return b.Bytes()
}

func writeHistograms(b *bytes.Buffer, name, help string, hs map[string]*histogram) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	tools := make([]string, 0, len(hs))
	for t := range hs {
		tools = append(tools, t)
	}
	sort.Strings(tools)
	for _, t := range tools {
		h := hs[t]
		tl := escapeLabel(t)
		var cum uint64
		for i, bound := range h.bounds {
			cum += h.counts[i]
			fmt.Fprintf(b, "%s_bucket{tool=\"%s\",le=\"%s\"} %d\n", name, tl, formatBound(bound), cum)
		}
		cum += h.counts[len(h.bounds)]
		fmt.Fprintf(b, "%s_bucket{tool=\"%s\",le=\"+Inf\"} %d\n", name, tl, cum)
		fmt.Fprintf(b, "%s_sum{tool=\"%s\"} %s\n", name, tl, strconv.FormatFloat(h.sum, 'g', -1, 64))
		fmt.Fprintf(b, "%s_count{tool=\"%s\"} %d\n", name, tl, h.n)
	}
}

// Handler serves GET /metrics and nothing else. It is mounted on its own
// listener (Server), never on the /mcp mux, so a proxy that forwards /mcp
// cannot expose it.
func Handler(m *Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_ = m.writeTo(w)
	})
	return mux
}

// Server is the metrics listener's server, with its own timeouts.
func Server(m *Metrics) *http.Server {
	return &http.Server{
		Handler:           Handler(m),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}
