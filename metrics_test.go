package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/metrics"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

func withMetrics(t *testing.T) *metrics.Metrics {
	t.Helper()
	m := metrics.New("test-1.1")
	orig := mx
	mx = m
	t.Cleanup(func() { mx = orig })
	return m
}

// series parses an exposition into "name{labels}" → value.
func series(t *testing.T, m *metrics.Metrics) map[string]float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler(m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	out := map[string]float64{}
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		v, err := strconv.ParseFloat(line[i+1:], 64)
		if err != nil {
			t.Fatalf("bad sample %q", line)
		}
		if _, dup := out[line[:i]]; dup {
			t.Fatalf("series written twice: %s", line[:i])
		}
		out[line[:i]] = v
	}
	return out
}

func req(method, tool, outcome, self string) string {
	return `ratatosk_mcp_requests_total{method="` + method + `",outcome="` + outcome + `",self="` + self + `",tool="` + tool + `"}`
}

func sumPrefix(s map[string]float64, prefix string) float64 {
	n := 0.0
	for k, v := range s {
		if strings.HasPrefix(k, prefix) {
			n += v
		}
	}
	return n
}

// mcpPost sends one JSON-RPC body the way an MCP HTTP client does.
func mcpPost(t *testing.T, url, body string, hdr map[string]string) (int, string) {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Mcp-Session-Id") + "|" + string(b)
}

func mcpServer(t *testing.T, rl *rateLimiter, stateless bool) *httptest.Server {
	t.Helper()
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return newServer() }, &mcp.StreamableHTTPOptions{Stateless: stateless})
	ts := httptest.NewServer(mcpMux(mcpHTTPChain(rl, 4, time.Second, h)))
	t.Cleanup(ts.Close)
	return ts
}

const (
	initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"secret-client","version":"0"}}}`
	notified = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
)

func toolCall(id int, name, args string) string {
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

// Every outcome, each POST counted once, labels from closed lists only.
func TestMetricsCountEveryOutcomeOnce(t *testing.T) {
	m := withMetrics(t)
	swapAPI(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)))
	ts := mcpServer(t, newRateLimiter(9), true)
	u := ts.URL + "/mcp"

	posts := 0
	send := func(body string, hdr map[string]string, want int) {
		t.Helper()
		posts++
		if code, resp := mcpPost(t, u, body, hdr); code != want {
			t.Fatalf("POST %s: %d, want %d: %s", body, code, want, resp)
		}
	}
	send(initBody, nil, 200)
	send(notified, nil, 202)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, nil, 200)
	send(toolCall(3, "list_projects", `{}`), nil, 200)
	send(toolCall(4, "get_release", `{"project":"secret-project-xyz"}`), nil, 200) // isError: unknown slug
	send(toolCall(5, "no_such_tool", `{"q":"secret"}`), nil, 200)                  // protocol error
	send(`[`+toolCall(6, "search", `{}`)+`]`, nil, 400)                            // batch refused
	send(toolCall(7, "list_releases", `{}`), map[string]string{"Accept": ""}, 400) // the SDK refuses before reading
	send(`{"jsonrpc":"2.0","id":8,"method":"ping"}`, nil, 200)
	// The limit of 9 is spent; the 10th is refused before it is read.
	send(toolCall(9, "check_stack", `{"components":[{"project":"secret-project-xyz","version":"v9.9.9-secret"}]}`), nil, 429)

	s := series(t, m)
	for k, want := range map[string]float64{
		req("initialize", "", "ok", "false"):                      1,
		req("other", "", "ok", "false"):                           2, // notifications/initialized (202) and ping
		req("tools/list", "", "ok", "false"):                      1,
		req("tools/call", "list_projects", "ok", "false"):         1,
		req("tools/call", "get_release", "error", "false"):        1,
		req("tools/call", "unknown", "error", "false"):            1,
		req("other", "", "error", "false"):                        1, // the batch
		req("tools/call", "list_releases", "error", "false"):      1,
		req("tools/call", "check_stack", "rate_limited", "false"): 1,
	} {
		if s[k] != want {
			t.Errorf("%s = %v, want %v", k, s[k], want)
		}
	}
	if got := sumPrefix(s, "ratatosk_mcp_requests_total{"); got != float64(posts) {
		t.Errorf("%v requests counted for %d POSTs: each POST is counted exactly once", got, posts)
	}
	// Two upstream requests, both /v2/projects: list_projects reads it fresh,
	// get_release through the cache, which list_projects does not fill.
	if s[`ratatosk_mcp_upstream_requests_total{outcome="ok",tool="list_projects"}`] != 1 ||
		s[`ratatosk_mcp_upstream_requests_total{outcome="ok",tool="get_release"}`] != 1 ||
		sumPrefix(s, "ratatosk_mcp_upstream_requests_total{") != 2 {
		t.Errorf("upstream: want one ok each for list_projects and get_release, got %v", s)
	}
	if s[`ratatosk_mcp_request_duration_seconds_count{tool="list_projects"}`] != 1 ||
		s[`ratatosk_mcp_response_bytes_count{tool="list_projects"}`] != 1 ||
		s[`ratatosk_mcp_request_duration_seconds_count{tool="unknown"}`] != 1 {
		t.Errorf("tool calls must be timed and sized: %v", s)
	}
	if s[`ratatosk_mcp_response_bytes_sum{tool="list_projects"}`] < 50 {
		t.Errorf("list_projects answer size %v", s[`ratatosk_mcp_response_bytes_sum{tool="list_projects"}`])
	}
	assertClosedLabels(t, m)
}

var labelRE = regexp.MustCompile(`([a-z_]+)="((?:[^"\\]|\\.)*)"`)

// assertClosedLabels: every label value is from a closed list, and nothing a
// caller sent appears anywhere in the exposition.
func assertClosedLabels(t *testing.T, m *metrics.Metrics) {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler(m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	text := rec.Body.String()
	for _, leak := range []string{"secret", "127.0.0.1", "no_such_tool", "v9.9.9", "Mcp-Session"} {
		if strings.Contains(text, leak) {
			t.Errorf("exposition carries caller input %q", leak)
		}
	}
	allowed := map[string][]string{
		"method":  metrics.MethodLabels,
		"tool":    append(metrics.ToolLabels(), ""),
		"outcome": append(slices.Clone(metrics.RequestOutcomes), metrics.UpstreamOutcomes...),
		"self":    metrics.SelfLabels,
		"version": {"test-1.1"},
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		for _, l := range labelRE.FindAllStringSubmatch(line, -1) {
			if l[1] == "le" {
				if _, err := strconv.ParseFloat(strings.TrimPrefix(l[2], "+"), 64); err != nil {
					t.Errorf("le %q is not a number", l[2])
				}
				continue
			}
			vals, ok := allowed[l[1]]
			if !ok || !slices.Contains(vals, l[2]) {
				t.Errorf("label %s=%q is outside its closed list: %s", l[1], l[2], line)
			}
		}
	}
}

// busy: the in-flight cap waited and gave up — counted under the tool the
// refused call named, read from the body the server never got to.
func TestMetricsCountBusy(t *testing.T) {
	m := withMetrics(t)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	h := mcpHTTPChain(nil, 1, 50*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(toolCall(1, "check_stack", `{}`))))
		close(done)
	}()
	<-started
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(toolCall(2, "search", `{"q":"CVE-2026-1"}`))))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	// A caller that stops waiting is turned away too.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(toolCall(3, "search", `{}`))).WithContext(ctx))
	close(release)
	<-done
	s := series(t, m)
	if got := s[req("tools/call", "search", "busy", "false")]; got != 2 {
		t.Errorf("busy for search = %v, want 2", got)
	}
	// The slot holder's own handler answered 200 with no MCP server behind it,
	// so nothing claimed that POST: it is counted once too, as an error.
	if got := sumPrefix(s, "ratatosk_mcp_requests_total{"); got != 3 {
		t.Errorf("%v requests counted, want the two refusals and the slot holder", got)
	}
	if got := s[req("tools/call", "check_stack", "error", "false")]; got != 1 {
		t.Errorf("the slot holder's POST: tools/call check_stack error = %v, want 1", got)
	}
}

// Stateful mode: requests after initialize run on the session's context, not
// the request's; each is still counted once, and the event stream (GET) is
// not a request.
func TestMetricsStatefulSessionCountsOnce(t *testing.T) {
	m := withMetrics(t)
	t.Setenv("MCP_SELF_CALLERS", "127.0.0.1")
	swapAPI(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)))
	ts := mcpServer(t, nil, false)
	u := ts.URL + "/mcp"
	code, resp := mcpPost(t, u, initBody, nil)
	sid, _, _ := strings.Cut(resp, "|")
	if code != 200 || sid == "" {
		t.Fatalf("initialize: %d %q", code, resp)
	}
	hdr := map[string]string{"Mcp-Session-Id": sid, "Mcp-Protocol-Version": "2025-06-18"}
	for _, b := range []string{notified, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, toolCall(3, "list_projects", `{}`)} {
		if code, resp := mcpPost(t, u, b, hdr); code >= 300 {
			t.Fatalf("%s: %d %s", b, code, resp)
		}
	}
	s := series(t, m)
	for _, k := range []string{
		req("initialize", "", "ok", "true"), req("other", "", "ok", "true"),
		req("tools/list", "", "ok", "true"), req("tools/call", "list_projects", "ok", "true"),
	} {
		if s[k] != 1 {
			t.Errorf("%s = %v, want 1", k, s[k])
		}
	}
	if got := sumPrefix(s, "ratatosk_mcp_requests_total{"); got != 4 {
		t.Errorf("%v counted for 4 POSTs", got)
	}
}

// Off by default: nothing is counted, the body is the caller's, and there is
// no route to metrics on the MCP listener — with metrics on or off.
func TestMetricsOffByDefaultAndNotOnTheMCPListener(t *testing.T) {
	if mx != nil {
		t.Fatal("metrics are on before anything turned them on")
	}
	var seen io.ReadCloser
	var tally any
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, tally = r.Body, r.Context().Value(metrics.TallyKey{})
		w.WriteHeader(http.StatusAccepted)
	})
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(notified))
	body := r.Body
	mx.HTTPMiddleware(inner).ServeHTTP(httptest.NewRecorder(), r)
	if seen != body || tally != nil {
		t.Error("with metrics off the HTTP layer must hand the request on untouched")
	}

	for _, on := range []bool{false, true} {
		if on {
			withMetrics(t)
		}
		ts := httptest.NewServer(mcpMux(mcpHTTPChain(nil, 0, time.Second, inner)))
		for _, p := range []string{"/metrics", "/mcp/metrics", "/healthz/metrics"} {
			resp, err := http.Get(ts.URL + p)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound || strings.Contains(string(b), "ratatosk_mcp_") {
				t.Errorf("metrics on=%v: GET %s on the MCP listener answered %d %q, want 404", on, p, resp.StatusCode, b)
			}
		}
		ts.Close()
	}
}

func TestMetricsUpstreamOutcomes(t *testing.T) {
	m := withMetrics(t)
	f := upstreamtest.NewEtcd()
	swapAPI(t, httptest.NewServer(f.Handler(t)))
	ctx := logging.RequestContext(context.Background(), "get_release", nil)
	if _, err := api.releaseRaw(ctx, "etcd", "v3.6.8", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := api.releaseRaw(ctx, "etcd", "v0.0.1", "", false); err == nil {
		t.Fatal("a version not on record must be an error")
	}
	// A cache hit is not a request.
	if _, err := api.projectsRaw(logging.RequestContext(context.Background(), "list_projects", nil), true); err != nil {
		t.Fatal(err)
	}
	if _, err := api.projectsRaw(logging.RequestContext(context.Background(), "list_projects", nil), true); err != nil {
		t.Fatal(err)
	}
	// Nothing listening: transport. A slow answer past the client timeout: timeout.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	api = newAPIClient(dead.URL)
	_, _ = api.searchRaw(logging.RequestContext(context.Background(), "search", nil), "x", "")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	api = newAPIClient(slow.URL)
	api.http.Timeout = 50 * time.Millisecond
	_, _ = api.searchRaw(logging.RequestContext(context.Background(), "search", nil), "x", "")
	cctx, cancel := context.WithCancel(logging.RequestContext(context.Background(), "check_stack", nil))
	cancel()
	_, _ = api.projectsRaw(cctx, false)

	s := series(t, m)
	for k, want := range map[string]float64{
		`ratatosk_mcp_upstream_requests_total{outcome="ok",tool="get_release"}`:       1,
		`ratatosk_mcp_upstream_requests_total{outcome="error",tool="get_release"}`:    1,
		`ratatosk_mcp_upstream_requests_total{outcome="ok",tool="list_projects"}`:     1,
		`ratatosk_mcp_upstream_requests_total{outcome="transport",tool="search"}`:     1,
		`ratatosk_mcp_upstream_requests_total{outcome="timeout",tool="search"}`:       1,
		`ratatosk_mcp_upstream_requests_total{outcome="canceled",tool="check_stack"}`: 1,
	} {
		if s[k] != want {
			t.Errorf("%s = %v, want %v", k, s[k], want)
		}
	}
	if got := sumPrefix(s, "ratatosk_mcp_upstream_requests_total{"); got != 6 {
		t.Errorf("%v upstream requests counted, want 6", got)
	}
}

// The closed tool list is the tool list a client receives.
func TestMetricsToolListMatchesServer(t *testing.T) {
	if got := keys(serverTools(t)); !slices.Equal(got, metrics.ToolNames) {
		t.Errorf("toolNames %v, server registers %v", metrics.ToolNames, got)
	}
}

// countingBody counts what is read of a request body.
type countingBody struct {
	r io.Reader
	n int
}

func (c *countingBody) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += n
	return n, err
}

// refusingLimiter refuses every request after its first.
func refusingLimiter() *rateLimiter {
	rl := newRateLimiter(1)
	rl.allow("192.0.2.1", time.Now())
	return rl
}

// zerosCall is a tools/call whose arguments are n bytes of "0," tokens before
// the tool name: the cheapest body to send and, before 1.1.0's bounds, the
// dearest to label (independent review, 2026-11-18: 25.7 ms and 3.4 MB of
// allocations per refusal at 64 KiB).
func zerosCall(n int) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"arguments":[` + strings.Repeat("0,", n/2) + `0],"name":"check_stack"}}`
}

// Labelling a refusal reads at most metricsPeekBytes of the body, in one pass
// with no allocation but the labels; a tool name past the bound is "unknown".
func TestRefusalLabellingIsBounded(t *testing.T) {
	m := withMetrics(t)
	h := mcpHTTPChain(refusingLimiter(), 0, 0, http.NotFoundHandler())
	body := &countingBody{r: strings.NewReader(zerosCall(64 << 10))}
	r := httptest.NewRequest(http.MethodPost, "/mcp", body)
	r.RemoteAddr = "192.0.2.1:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429", rec.Code)
	}
	if body.n > 4<<10+512 {
		t.Errorf("read %d bytes of a refused request's body; the bound is about 4 KiB", body.n)
	}
	if got := series(t, m)[req("tools/call", "unknown", "rate_limited", "false")]; got != 1 {
		t.Errorf("tools/call unknown rate_limited = %v, want 1", got)
	}
	if meth, tool := metrics.PeekLabels([]byte(zerosCall(3 << 10))); meth != "tools/call" || tool != "check_stack" {
		t.Errorf("peekLabels inside the bound = %q, %q; want tools/call, check_stack", meth, tool)
	}
	for name, prefix := range map[string]string{
		"0, tokens": zerosCall(64 << 10),
		"nesting":   `{"jsonrpc":"2.0","method":"tools/call","params":{"arguments":` + strings.Repeat(`[{"a":`, 2000),
		"strings":   `{"method":"tools/call","params":{"arguments":[` + strings.Repeat(`"\"\\",`, 2000),
	} {
		b := []byte(prefix)[:metrics.PeekBytes]
		if a := testing.AllocsPerRun(20, func() { metrics.PeekLabels(b) }); a > 2 {
			t.Errorf("%s: peekLabels made %.0f allocations on a %d-byte prefix; want the labels only", name, a, len(b))
		}
	}
}

// With metrics off a refusal is the 1.0.0 one: the refusing layer reads not a
// byte of the body, and answers the same status, headers and body. (The batch
// check, which sits before the in-flight cap since 1.0.0, peeks the first
// bytes of a POST; that is the in-flight refusal's 1.0.0 behaviour, so the cap
// is held to account on its own here.)
func TestRefusalWithMetricsOffReadsNothing(t *testing.T) {
	if mx != nil {
		t.Fatal("metrics are on at the start of a test")
	}
	h := mcpHTTPChain(refusingLimiter(), 0, 0, http.NotFoundHandler())
	body := &countingBody{r: strings.NewReader(zerosCall(64 << 10))}
	r := httptest.NewRequest(http.MethodPost, "/mcp", body)
	r.RemoteAddr = "192.0.2.1:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if body.n != 0 {
		t.Errorf("read %d bytes of the body with metrics off", body.n)
	}
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || rec.Header().Get("Content-Type") != "application/json" ||
		rec.Body.String() != `{"error":"rate limit exceeded (1 requests/minute per caller)"}` {
		t.Errorf("refusal %d %v %q differs from 1.0.0's", rec.Code, rec.Header(), rec.Body.String())
	}

	// The in-flight cap: the slot is held, the next POST waits and is refused.
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	h = inflightMiddleware(1, 20*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}")))
	<-started
	body = &countingBody{r: strings.NewReader(zerosCall(1 << 10))}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", body))
	close(release)
	if rec.Code != http.StatusServiceUnavailable || body.n != 0 || rec.Header().Get("Retry-After") != "5" || rec.Body.String() != `{"error":"server busy, retry shortly"}` {
		t.Errorf("busy refusal with metrics off: %d, read %d bytes, %q", rec.Code, body.n, rec.Body.String())
	}
}

// A refused request sent with Expect: 100-continue: metrics on or off, the
// client sends what 1.0.0 had it send. Reading the body to label a refusal
// made net/http send 100 Continue, so the client sent a body the server then
// refused, where 1.0.0 answered the rate limit at once.
func TestRefusalBehindExpectSendsNoMoreThan100(t *testing.T) {
	send := func(t *testing.T, outcome string) (status, sent int) {
		h := mcpHTTPChain(refusingLimiter(), 0, 0, http.NotFoundHandler())
		if outcome == "busy" {
			release, started, held := make(chan struct{}), make(chan struct{}), make(chan struct{})
			h = mcpHTTPChain(nil, 1, 20*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				close(started)
				<-release
			}))
			go func() {
				h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}")))
				close(held)
			}()
			<-started
			defer func() { close(release); <-held }()
		}
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.RemoteAddr = "192.0.2.1:1"
			h.ServeHTTP(w, r)
		}))
		defer ts.Close()
		call := toolCall(1, "search", `{}`)
		body := &countingBody{r: strings.NewReader(call)}
		r, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", body)
		r.ContentLength = int64(len(call))
		r.Header.Set("Expect", "100-continue")
		client := &http.Client{Transport: &http.Transport{ExpectContinueTimeout: 5 * time.Second}}
		start := time.Now()
		resp, err := client.Do(r)
		if err != nil {
			t.Fatalf("%s: %v", outcome, err)
		}
		resp.Body.Close()
		if time.Since(start) > 2*time.Second {
			t.Errorf("%s: answered after %v", outcome, time.Since(start))
		}
		return resp.StatusCode, body.n
	}
	for _, outcome := range []string{"rate_limited", "busy"} {
		offStatus, offSent := send(t, outcome)
		m := withMetrics(t)
		onStatus, onSent := send(t, outcome)
		if onStatus != offStatus || onSent != offSent {
			t.Errorf("%s: metrics on %d with %d body bytes sent, off %d with %d", outcome, onStatus, onSent, offStatus, offSent)
		}
		if outcome == "rate_limited" && (onStatus != http.StatusTooManyRequests || onSent != 0) {
			t.Errorf("rate limit behind Expect: %d, %d body bytes sent; want 429 before any body", onStatus, onSent)
		}
		if outcome == "rate_limited" && series(t, m)[req("other", "", "rate_limited", "false")] != 1 {
			t.Errorf("rate limit behind Expect: want it counted once as other (nothing read)")
		}
		refusals := 0.0
		for k, v := range series(t, m) {
			if strings.HasPrefix(k, "ratatosk_mcp_requests_total{") && strings.Contains(k, `outcome="`+outcome+`"`) {
				refusals += v
			}
		}
		if refusals != 1 {
			t.Errorf("%s: %v refusals counted, want 1", outcome, refusals)
		}
		mx = nil
	}
}

// What one rate-limited POST costs, metrics off (the 1.0.0 refusal) and on.
// The body is 64 KiB of "0," tokens before the tool name; before the 1.1.0
// bounds, labelling it cost 25.7 ms and 3.4 MB per refusal on the review
// machine (2026-11-18).
func BenchmarkRefusedRequestLabel(b *testing.B) {
	body := []byte(zerosCall(64 << 10))
	for _, on := range []bool{false, true} {
		b.Run(map[bool]string{false: "metrics_off", true: "metrics_on"}[on], func(b *testing.B) {
			orig := mx
			mx = nil
			if on {
				mx = metrics.New("bench")
			}
			b.Cleanup(func() { mx = orig })
			h := mcpHTTPChain(refusingLimiter(), 0, 0, http.NotFoundHandler())
			b.ReportAllocs()
			for b.Loop() {
				r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
				r.RemoteAddr = "192.0.2.1:1"
				h.ServeHTTP(httptest.NewRecorder(), r)
			}
		})
	}
}

// Every POST is counted once, in stateless and stateful mode: also the ones
// the SDK answers 200 with a JSON-RPC error without running them (params it
// could not read), which went uncounted while the HTTP layer took a 200 for a
// request the MCP layer had counted (final review, 2026-11-18).
func TestMetricsCountPostsTheSDKNeverRan(t *testing.T) {
	bodies := []struct{ body, key string }{
		{toolCall(2, "list_projects", `{}`), req("tools/call", "list_projects", "ok", "false")},
		{`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":5}}`, req("tools/call", "unknown", "error", "false")},
		{`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":null}`, req("tools/call", "unknown", "error", "false")},
		{`{"jsonrpc":"2.0","id":5,"method":"tools/list","params":"x"}`, req("tools/list", "", "error", "false")},
	}
	// One metrics for both modes, read as deltas: the SDK handles a
	// notification after it answers 202, so swapping the global between modes
	// would race with it.
	m := withMetrics(t)
	swapAPI(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)))
	for _, stateless := range []bool{true, false} {
		u := mcpServer(t, nil, stateless).URL + "/mcp"
		var hdr map[string]string
		if !stateless {
			code, resp := mcpPost(t, u, initBody, nil)
			sid, _, _ := strings.Cut(resp, "|")
			if code != 200 || sid == "" {
				t.Fatalf("initialize: %d %q", code, resp)
			}
			hdr = map[string]string{"Mcp-Session-Id": sid, "Mcp-Protocol-Version": "2025-06-18"}
			mcpPost(t, u, notified, hdr)
		}
		before := sumPrefix(series(t, m), "ratatosk_mcp_requests_total{")
		for _, b := range bodies {
			was := series(t, m)
			code, resp := mcpPost(t, u, b.body, hdr)
			now := series(t, m)
			if got := sumPrefix(now, "ratatosk_mcp_requests_total{") - sumPrefix(was, "ratatosk_mcp_requests_total{"); got != 1 {
				t.Errorf("stateless=%v %s (answered %d %.120s): counted %v times, want once", stateless, b.body, code, resp, got)
			}
			if now[b.key]-was[b.key] != 1 {
				t.Errorf("stateless=%v %s: %s did not move", stateless, b.body, b.key)
			}
		}
		if got := sumPrefix(series(t, m), "ratatosk_mcp_requests_total{") - before; got != float64(len(bodies)) {
			t.Errorf("stateless=%v: %v counted for %d POSTs", stateless, got, len(bodies))
		}
	}
}

// A POST the SDK answers 202 carried no request: whatever its body names, it
// is counted as method other. A later "method" key wins in the SDK, so this
// body is a notification there; labelled from the scanner it added one to a
// tools/call that never ran (final review, 2026-11-18).
func TestMetricsCount202AsNoCall(t *testing.T) {
	m := withMetrics(t) // one for both modes, read as deltas (see above)
	for _, stateless := range []bool{true, false} {
		u := mcpServer(t, nil, stateless).URL + "/mcp"
		var hdr map[string]string
		if !stateless {
			code, resp := mcpPost(t, u, initBody, nil)
			sid, _, _ := strings.Cut(resp, "|")
			if code != 200 || sid == "" {
				t.Fatalf("initialize: %d %q", code, resp)
			}
			hdr = map[string]string{"Mcp-Session-Id": sid, "Mcp-Protocol-Version": "2025-06-18"}
		}
		for _, body := range []string{
			`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"check_stack"},"method":"notifications/initialized"}`,
			`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"check_stack"},"\u006dethod":"notifications/initialized"}`,
		} {
			was := series(t, m)
			if code, resp := mcpPost(t, u, body, hdr); code != http.StatusAccepted {
				t.Fatalf("stateless=%v %s: %d %s, want 202", stateless, body, code, resp)
			}
			now := series(t, m)
			if d := now[req("other", "", "ok", "false")] - was[req("other", "", "ok", "false")]; d != 1 {
				t.Errorf("stateless=%v %s: other/ok moved by %v, want 1", stateless, body, d)
			}
			if d := sumPrefix(now, `ratatosk_mcp_requests_total{method="tools/call"`) - sumPrefix(was, `ratatosk_mcp_requests_total{method="tools/call"`); d != 0 {
				t.Errorf("stateless=%v %s: a notification moved tools/call by %v", stateless, body, d)
			}
		}
	}
}

// The privacy bullets say every kind of number the metrics keep: per-tool
// durations, answer sizes and upstream requests too, not only request counts
// (final review, 2026-11-18). English is pinned here; the Korean and Japanese
// bullets say the same.
func TestPrivacyBulletsNameEveryMetric(t *testing.T) {
	for _, p := range []string{"README.md", "docs/install.en.md"} {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(raw)), " ")
		if !strings.Contains(text, "how long calls took, how large the answers were and how many upstream requests they sent") {
			t.Errorf("%s: the privacy bullet does not name durations, answer sizes and upstream requests", p)
		}
	}
}
