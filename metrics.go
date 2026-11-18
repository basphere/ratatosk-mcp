package main

// Request counters for the HTTP transport, served on a listener of their own
// (1.1.0). Off unless MCP_METRICS_ADDR is set: the same binary is what people
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
// Where a request is counted: a call the MCP server ran is counted by
// metricsMiddleware, which sees its result (an isError answer is an error). A
// POST the server never ran — refused by the rate limiter, the in-flight cap,
// the batch refusal or the SDK's own checks — or one that carried no call (a
// notification, answered 202) is counted by metricsHTTPMiddleware. The two
// never count the same POST: a call the server ran is answered 200, and the
// HTTP layer counts only 202, an error status, or a refusal it was told of.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mx is the process's metrics; nil = off (the default), and every method is a
// no-op on nil.
var mx *metrics

// toolNames is the closed list of tool labels. It must equal the tools
// newServer registers (TestMetricsToolListMatchesServer); anything else a
// caller names is "unknown".
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

type metrics struct {
	version   string
	mu        sync.Mutex
	requests  map[requestKey]uint64
	upstream  map[upstreamKey]uint64
	duration  map[string]*histogram
	respBytes map[string]*histogram
}

// newMetrics lays every known label combination down at zero. A series that
// first appears with the value 1 has no 0 before it, so increase() over a
// window that starts there misses the first call after every restart — the
// web /v2 counter lost them that way (2026-11-16).
func newMetrics(version string) *metrics {
	m := &metrics{
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
func (m *metrics) countRequest(method, tool, outcome string, self bool) {
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

// countUpstream counts one request this process sent to /v2 (cache hits are
// not requests).
func (m *metrics) countUpstream(tool, outcome string) {
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
func (m *metrics) observeTool(tool string, d time.Duration, bytes int) {
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

// upstreamOutcome classifies a /v2 request that got no answer. canceled is
// the call going away (the caller left, or check_stack stopped reading after
// another document failed), not a failure of the upstream.
func upstreamOutcome(err error) string {
	switch errKind(err) {
	case "timeout":
		return "timeout"
	case "canceled":
		return "canceled"
	}
	return "transport"
}

func toolFrom(ctx context.Context) string {
	t, _ := ctx.Value(ctxKeyTool).(string)
	return t
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
// byte-identical.
func (m *metrics) writeTo(w io.Writer) error {
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

	_, err := w.Write(b.Bytes())
	return err
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

// metricsHandler serves GET /metrics and nothing else. It is mounted on its
// own listener (metricsServer), never on the /mcp mux, so a proxy that
// forwards /mcp cannot expose it.
func metricsHandler(m *metrics) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_ = m.writeTo(w)
	})
	return mux
}

func metricsServer(m *metrics) *http.Server {
	return &http.Server{
		Handler:           metricsHandler(m),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}

// ---------------------------------------------------------------------------
// counting at the MCP layer
// ---------------------------------------------------------------------------

// metricsMiddleware counts every request the MCP server runs, with the
// outcome its result shows, and times and sizes tool calls. Notifications are
// left to the HTTP layer, which answers them 202 whether or not they are run
// before the request ends. self is read from the context exactly as the
// upstream call reads it for X-Ratatosk-Caller.
func metricsMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		m := mx
		if m == nil || strings.HasPrefix(method, "notifications/") {
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
		m.countRequest(method, tool, outcome, callerClassFrom(ctx) == "self")
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

// ---------------------------------------------------------------------------
// counting at the HTTP layer
// ---------------------------------------------------------------------------

// metricsPeekBytes bounds what is read of a request body to label it.
// Labelling a request the server turns away must cost about what turning it
// away costs: with 64 KiB read and a json.Decoder walking every token, a body
// of "0," tokens took 25.7 ms and 3.4 MB of allocations per refused request
// (independent review, 2026-11-18), and refusals are not bounded by the rate
// limiter — they are what it produces — while the hosted container has one
// CPU. method sits in the first bytes of every request seen, params.name close
// behind it; a label past the bound is "other" (method) or "unknown" (tool).
// peekLabels reads the prefix in one pass, byte by byte, without recursion,
// so its work is bounded by these 4 KiB whatever their nesting.
const metricsPeekBytes = 4 << 10

type tallyKey struct{}

// httpTally is one POST as the HTTP layer sees it.
type httpTally struct {
	body    *prefixReader
	outcome string // set by a refusal: rate_limited | busy
}

// prefixReader keeps the first max bytes read through it, whoever reads.
type prefixReader struct {
	r   io.Reader
	buf []byte
	max int
}

func (p *prefixReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if room := p.max - len(p.buf); room > 0 && n > 0 {
		p.buf = append(p.buf, b[:min(n, room)]...)
	}
	return n, err
}

// fill reads on until the prefix is full or the body ends — for a request
// answered before anything read its body.
func (p *prefixReader) fill() {
	if room := p.max - len(p.buf); room > 0 {
		_, _ = io.CopyN(io.Discard, p, int64(room))
	}
}

// refusedFor marks the request as refused before the server saw it, and
// reads the beginning of its body so the refusal is counted under the method
// and tool it was for. The rate limiter and the in-flight cap call it before
// they answer: a handler reads a body before it writes the response. With
// metrics off it reads nothing — the refusal is the 1.0.0 one.
//
// Behind Expect: 100-continue it reads nothing either. The client sends the
// body only when the server asks for it, and net/http asks on the first read:
// reading here sent 100 Continue for a body about to be refused, then waited
// for it, where 1.0.0 answered at once (independent review, 2026-11-18). Such
// a refusal is counted from what was read, nothing: method "other".
func refusedFor(req *http.Request, outcome string) {
	t, ok := req.Context().Value(tallyKey{}).(*httpTally)
	if !ok {
		return
	}
	t.outcome = outcome
	if req.Header.Get("Expect") == "" {
		t.body.fill()
	}
}

// statusWriter remembers the status of the response. It passes Flush through
// (the SDK streams answers as server-sent events) and unwraps for
// http.ResponseController.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 && code >= 200 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// metricsHTTPMiddleware counts the POSTs the MCP server does not: refusals,
// requests the SDK rejects before running them, and requests with no call.
// With metrics off it does nothing at all — the body is not touched.
func metricsHTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		m := mx
		if m == nil || req.Method != http.MethodPost || req.Body == nil {
			next.ServeHTTP(w, req)
			return
		}
		t := &httpTally{body: &prefixReader{r: req.Body, max: metricsPeekBytes}}
		req = req.WithContext(context.WithValue(req.Context(), tallyKey{}, t))
		req.Body = struct {
			io.Reader
			io.Closer
		}{t.body, req.Body}
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, req)
		outcome := t.outcome
		switch {
		case outcome != "":
		case sw.status >= 400:
			outcome = "error"
			// The SDK can refuse before it reads the body (a missing Accept
			// header). Reading it now is safe only without Expect: once a status
			// is written, net/http no longer sends 100 Continue, and a client
			// waiting for it would hold this read until its own timeout. Such a
			// request is labelled from what was read: method "other".
			if req.Header.Get("Expect") == "" {
				t.body.fill()
			}
		case sw.status == http.StatusAccepted:
			outcome = "ok"
		default:
			return // a call the server ran: metricsMiddleware counted it
		}
		method, tool := peekLabels(t.body.buf)
		m.countRequest(method, tool, outcome, callerClassFrom(req.Context()) == "self")
	})
}

// peekLabels reads the JSON-RPC method and, for tools/call, params.name from
// the beginning of a request body. The prefix may end mid-value; whatever was
// read before that counts. Anything not a single JSON object (a batch, a
// response, garbage) has no method. One pass over b, no recursion, and no
// allocation but the two strings it returns, decoded once at the end — it is
// not a validator: the SDK judges the body, this only names what it was for,
// and each name ends up as a closed-list label anyway.
func peekLabels(b []byte) (method, tool string) {
	var m, n []byte // the last method and params.name strings seen, quotes included
	sc := jsonScan{b: b}
	defer func() { method, tool = unquote(m), unquote(n) }()
	if !sc.next('{') {
		return
	}
	for {
		key, ok := sc.str()
		if !ok || !sc.next(':') {
			return
		}
		switch {
		case string(key) == `"method"` && sc.peek() == '"':
			if m, ok = sc.str(); !ok {
				m = nil
				return
			}
		case string(key) == `"params"` && sc.peek() == '{':
			sc.i++
			if sc.peek() != '}' {
				for {
					pk, ok := sc.str()
					if !ok || !sc.next(':') {
						return
					}
					if string(pk) == `"name"` && sc.peek() == '"' {
						if n, ok = sc.str(); !ok {
							n = nil
							return
						}
					} else if !sc.skipValue() {
						return
					}
					if !sc.next(',') {
						break
					}
				}
			}
			if !sc.next('}') {
				return
			}
		default:
			if !sc.skipValue() {
				return
			}
		}
		if !sc.next(',') {
			return
		}
	}
}

// unquote decodes one JSON string, quotes included; "" for nil or invalid.
func unquote(q []byte) string {
	if len(q) < 2 {
		return ""
	}
	if !bytes.ContainsRune(q, '\\') {
		return string(q[1 : len(q)-1])
	}
	var v string
	if json.Unmarshal(q, &v) != nil {
		return ""
	}
	return v
}

// jsonScan walks a JSON prefix one byte at a time; i only moves forward.
type jsonScan struct {
	b []byte
	i int
}

// peek skips white space and returns the next byte, 0 at the end.
func (s *jsonScan) peek() byte {
	for s.i < len(s.b) {
		switch c := s.b[s.i]; c {
		case ' ', '\t', '\n', '\r':
			s.i++
		default:
			return c
		}
	}
	return 0
}

// next consumes c if it comes next.
func (s *jsonScan) next(c byte) bool {
	if s.peek() != c {
		return false
	}
	s.i++
	return true
}

// str reads a string and returns it as written, quotes included; false when
// none comes next or it is cut short.
func (s *jsonScan) str() ([]byte, bool) {
	if !s.next('"') {
		return nil, false
	}
	start := s.i - 1
	for ; s.i < len(s.b); s.i++ {
		switch s.b[s.i] {
		case '\\':
			s.i++
		case '"':
			s.i++
			return s.b[start:s.i], true
		}
	}
	return nil, false
}

// skipValue reads past one value of any kind; false when the input ends
// first. Objects and arrays are skipped by counting brackets outside strings.
func (s *jsonScan) skipValue() bool {
	switch s.peek() {
	case 0:
		return false
	case '"':
		_, ok := s.str()
		return ok
	case '{', '[':
		for depth := 0; s.i < len(s.b); {
			switch s.b[s.i] {
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					s.i++
					return true
				}
			case '"':
				if _, ok := s.str(); !ok {
					return false
				}
				continue
			}
			s.i++
		}
		return false
	}
	for ; s.i < len(s.b); s.i++ { // a number, true, false or null
		switch s.b[s.i] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			return true
		}
	}
	return false
}
