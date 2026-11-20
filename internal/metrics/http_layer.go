package metrics

// Counting at the HTTP layer: every POST the MCP server did not run, and the
// tally that settles which layer counts a POST.

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/logging"
)

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
	outcome string       // set by a refusal: rate_limited | busy
	claimed atomic.Int32 // who counts this POST: 0 nobody yet, claimedByMCP, claimedByHTTP
}

const (
	claimedByMCP  = 1
	claimedByHTTP = 2
)

// claim makes by the one layer that counts this POST; false when the other
// layer already is.
func (t *httpTally) claim(by int32) bool { return t.claimed.CompareAndSwap(0, by) }

// tallyHeader carries a POST's tally id from the HTTP layer to the MCP layer.
// The SDK hands every message of a POST that request's own header
// (RequestExtra.Header), in stateless and stateful mode alike, where the
// request's context does not reach: a stateful session runs its calls on the
// session's context. The HTTP layer sets it on every POST, over anything the
// client sent, and nothing else reads it.
const tallyHeader = "X-Ratatosk-Metrics-Post"

// mcpLayerCounts says whether Middleware is the layer that counts req:
// a request with no tally id did not come through the HTTP layer (stdio); one
// whose tally is gone or claimed belongs to a POST the HTTP layer counted
// already (it ended before this request was run).
func (m *Metrics) mcpLayerCounts(req mcp.Request) bool {
	extra := req.GetExtra()
	if extra == nil || extra.Header == nil {
		return true
	}
	id := extra.Header.Get(tallyHeader)
	if id == "" {
		return true
	}
	t, ok := m.tallies.Load(id)
	return ok && t.(*httpTally).claim(claimedByMCP)
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

// RefusedFor marks the request as refused before the server saw it, and
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
func RefusedFor(req *http.Request, outcome string) {
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

// HTTPMiddleware counts every POST the MCP server did not claim: refusals,
// requests the SDK rejected or never ran, and POSTs that carried no request.
// With metrics off (m nil) it does nothing at all — the body is not touched.
func (m *Metrics) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if m == nil || req.Method != http.MethodPost || req.Body == nil {
			next.ServeHTTP(w, req)
			return
		}
		t := &httpTally{body: &prefixReader{r: req.Body, max: metricsPeekBytes}}
		id := strconv.FormatUint(m.tallySeq.Add(1), 36)
		m.tallies.Store(id, t)
		defer m.tallies.Delete(id)
		req = req.WithContext(context.WithValue(req.Context(), tallyKey{}, t))
		if req.Header == nil {
			req.Header = http.Header{}
		}
		req.Header.Set(tallyHeader, id)
		req.Body = struct {
			io.Reader
			io.Closer
		}{t.body, req.Body}
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, req)
		if !t.claim(claimedByHTTP) {
			return // a request the server ran: Middleware counts it
		}
		outcome := t.outcome
		switch {
		case outcome != "":
		case sw.status == http.StatusAccepted:
			// Only notifications and responses: no request was in this POST,
			// whatever its body names. A body can name tools/call and still be a
			// notification to the SDK (a later "method" key wins), and counted as
			// a call it added one to a tool call that never ran (final review,
			// 2026-11-18).
			m.countRequest("other", "", "ok", logging.CallerClassFrom(req.Context()) == "self")
			return
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
		default:
			// Answered without running a request: params the SDK could not read
			// come back 200 with a JSON-RPC error, and the receiving middleware
			// never sees them (tools/call with "params":null or {"name":5}).
			outcome = "error"
		}
		method, tool := peekLabels(t.body.buf)
		m.countRequest(method, tool, outcome, logging.CallerClassFrom(req.Context()) == "self")
	})
}
