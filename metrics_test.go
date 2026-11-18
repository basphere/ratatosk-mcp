package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func withMetrics(t *testing.T) *metrics {
	t.Helper()
	m := newMetrics("test-1.1")
	orig := mx
	mx = m
	t.Cleanup(func() { mx = orig })
	return m
}

// series parses an exposition into "name{labels}" → value.
func series(t *testing.T, m *metrics) map[string]float64 {
	t.Helper()
	var b bytes.Buffer
	if err := m.writeTo(&b); err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	sc := bufio.NewScanner(&b)
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
	swapAPI(t, httptest.NewServer(newFakeEtcd().handler(t)))
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
func assertClosedLabels(t *testing.T, m *metrics) {
	t.Helper()
	var b bytes.Buffer
	_ = m.writeTo(&b)
	text := b.String()
	for _, leak := range []string{"secret", "127.0.0.1", "no_such_tool", "v9.9.9", "Mcp-Session"} {
		if strings.Contains(text, leak) {
			t.Errorf("exposition carries caller input %q", leak)
		}
	}
	allowed := map[string][]string{
		"method":  methodLabels,
		"tool":    append(toolLabels(), ""),
		"outcome": append(slices.Clone(requestOutcomes), upstreamOutcomes...),
		"self":    selfLabels,
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
	// The slot holder's own handler answered 200 with no MCP server behind it:
	// it is nobody's to count here, and the HTTP layer must not count it.
	if got := sumPrefix(s, "ratatosk_mcp_requests_total{"); got != 2 {
		t.Errorf("%v requests counted, want the two refusals", got)
	}
}

// Stateful mode: requests after initialize run on the session's context, not
// the request's; each is still counted once, and the event stream (GET) is
// not a request.
func TestMetricsStatefulSessionCountsOnce(t *testing.T) {
	m := withMetrics(t)
	t.Setenv("MCP_SELF_CALLERS", "127.0.0.1")
	swapAPI(t, httptest.NewServer(newFakeEtcd().handler(t)))
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

// Every known combination is there at zero before the first request.
func TestMetricsStartAtZero(t *testing.T) {
	s := series(t, newMetrics("test-1.1"))
	var reqs, ups, buckets int
	for k, v := range s {
		switch {
		case strings.HasPrefix(k, "ratatosk_mcp_requests_total{"):
			reqs++
		case strings.HasPrefix(k, "ratatosk_mcp_upstream_requests_total{"):
			ups++
		case strings.Contains(k, "_bucket{"):
			buckets++
		}
		if v != 0 && !strings.HasPrefix(k, "ratatosk_mcp_build_info") {
			t.Errorf("%s = %v at start", k, v)
		}
	}
	// requests: tools/call 7 tools x 4 outcomes x 2 + three other methods x 4 x 2;
	// upstream: 7 tools x 5 outcomes; buckets: 7 tools x 12 for each histogram.
	if reqs != 7*4*2+3*4*2 || ups != 7*5 || buckets != 2*7*12 {
		t.Errorf("requests %d, upstream %d, buckets %d; want 80, 35, 168", reqs, ups, buckets)
	}
	if s[`ratatosk_mcp_build_info{version="test-1.1"}`] != 1 {
		t.Errorf("build_info missing: %v", s)
	}
	for _, k := range []string{req("tools/call", "check_stack", "busy", "true"), req("initialize", "", "rate_limited", "false"),
		`ratatosk_mcp_request_duration_seconds_count{tool="search"}`, `ratatosk_mcp_response_bytes_bucket{tool="unknown",le="+Inf"}`} {
		if _, ok := s[k]; !ok {
			t.Errorf("%s not laid down at zero", k)
		}
	}
}

func TestMetricsHistograms(t *testing.T) {
	m := newMetrics("test-1.1")
	m.observeTool("check_stack", 50*time.Millisecond, 1024)
	m.observeTool("check_stack", 300*time.Millisecond, 200<<10)
	m.observeTool("check_stack", 100*time.Second, 5<<20)
	m.observeTool("no_such_tool", time.Second, -1)
	s := series(t, m)
	d := func(le string) float64 {
		return s[`ratatosk_mcp_request_duration_seconds_bucket{tool="check_stack",le="`+le+`"}`]
	}
	if d("0.05") != 1 || d("0.25") != 1 || d("0.5") != 2 || d("90") != 2 || d("+Inf") != 3 {
		t.Errorf("duration buckets 0.05=%v 0.25=%v 0.5=%v 90=%v +Inf=%v; want 1 1 2 2 3 (cumulative, le inclusive)", d("0.05"), d("0.25"), d("0.5"), d("90"), d("+Inf"))
	}
	if got := s[`ratatosk_mcp_request_duration_seconds_sum{tool="check_stack"}`]; got < 100.34 || got > 100.36 {
		t.Errorf("duration sum %v", got)
	}
	by := func(le string) float64 {
		return s[`ratatosk_mcp_response_bytes_bucket{tool="check_stack",le="`+le+`"}`]
	}
	if by("1024") != 1 || by("131072") != 1 || by("262144") != 2 || by("4194304") != 2 || by("+Inf") != 3 {
		t.Errorf("bytes buckets 1024=%v 131072=%v 262144=%v 4194304=%v +Inf=%v", by("1024"), by("131072"), by("262144"), by("4194304"), by("+Inf"))
	}
	if s[`ratatosk_mcp_response_bytes_count{tool="check_stack"}`] != 3 || s[`ratatosk_mcp_response_bytes_sum{tool="check_stack"}`] != float64(1024+200<<10+5<<20) {
		t.Errorf("bytes count/sum %v %v", s[`ratatosk_mcp_response_bytes_count{tool="check_stack"}`], s[`ratatosk_mcp_response_bytes_sum{tool="check_stack"}`])
	}
	if s[`ratatosk_mcp_request_duration_seconds_count{tool="unknown"}`] != 1 || s[`ratatosk_mcp_response_bytes_count{tool="unknown"}`] != 0 {
		t.Error("a call with no tool result is timed under unknown and not sized")
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
		seen, tally = r.Body, r.Context().Value(tallyKey{})
		w.WriteHeader(http.StatusAccepted)
	})
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(notified))
	body := r.Body
	metricsHTTPMiddleware(inner).ServeHTTP(httptest.NewRecorder(), r)
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

func TestMetricsEndpoint(t *testing.T) {
	ts := httptest.NewServer(metricsHandler(newMetrics("test-1.1")))
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain; version=0.0.4") ||
		!strings.Contains(string(b), "# TYPE ratatosk_mcp_requests_total counter\n") ||
		!strings.Contains(string(b), "# TYPE ratatosk_mcp_response_bytes histogram\n") {
		t.Errorf("GET /metrics: %d %s\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	for _, c := range []struct{ method, path string }{{http.MethodPost, "/metrics"}, {http.MethodGet, "/"}, {http.MethodGet, "/mcp"}} {
		r, _ := http.NewRequest(c.method, ts.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Errorf("%s %s answered 200 on the metrics listener", c.method, c.path)
		}
	}
}

func TestMetricsUpstreamOutcomes(t *testing.T) {
	m := withMetrics(t)
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	ctx := context.WithValue(context.Background(), ctxKeyTool, "get_release")
	if _, err := api.releaseRaw(ctx, "etcd", "v3.6.8", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := api.releaseRaw(ctx, "etcd", "v0.0.1", "", false); err == nil {
		t.Fatal("a version not on record must be an error")
	}
	// A cache hit is not a request.
	if _, err := api.projectsRaw(context.WithValue(context.Background(), ctxKeyTool, "list_projects"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := api.projectsRaw(context.WithValue(context.Background(), ctxKeyTool, "list_projects"), true); err != nil {
		t.Fatal(err)
	}
	// Nothing listening: transport. A slow answer past the client timeout: timeout.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	api = newAPIClient(dead.URL)
	_, _ = api.searchRaw(context.WithValue(context.Background(), ctxKeyTool, "search"), "x", "")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	api = newAPIClient(slow.URL)
	api.http.Timeout = 50 * time.Millisecond
	_, _ = api.searchRaw(context.WithValue(context.Background(), ctxKeyTool, "search"), "x", "")
	cctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKeyTool, "check_stack"))
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

// A refusal written before the body was read (the SDK's Accept check), to a
// client that waits for 100 Continue before it sends the body: once a status
// is written net/http sends no 100 Continue, so the count must not wait for a
// body that will not come. (In the full chain the batch refusal reads the
// first byte before any status, which still triggers 100 Continue.)
func TestMetricsDoNotWaitForABodyBehindExpect(t *testing.T) {
	m := withMetrics(t)
	h := metricsHTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Accept must contain both 'application/json' and 'text/event-stream'", http.StatusBadRequest)
	}))
	pr, pw := io.Pipe() // a body that never arrives
	defer pw.Close()
	r := httptest.NewRequest(http.MethodPost, "/mcp", pr)
	r.Header.Set("Expect", "100-continue")
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), r)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the HTTP layer waited for a body the client holds back until 100 Continue")
	}
	if got := series(t, m)[req("other", "", "error", "false")]; got != 1 {
		t.Errorf("other/error = %v, want 1", got)
	}
}

func TestPeekLabels(t *testing.T) {
	big := `{"components":[` + strings.Repeat(`{"project":"x","version":"v1"},`, 3000) + `{}]}`
	cases := []struct{ body, method, tool string }{
		{toolCall(1, "check_stack", `{}`), "tools/call", "check_stack"},
		{`{"id":1,"jsonrpc":"2.0","method":"tools/call","params":{"arguments":{"q":"x"},"name":"search"}}`, "tools/call", "search"},
		{`{"params":{"name":"get_release","_meta":{"a":[1,{"b":2}]}},"method":"tools/call","id":1}`, "tools/call", "get_release"},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"arguments":` + big + `,"name":"check_stack"}}`, "tools/call", ""}, // name past the prefix
		{`  {"method":"initialize"`, "initialize", ""},                                                                               // cut short
		{`[{"method":"tools/call"}]`, "", ""},
		{`{"jsonrpc":"2.0","id":1,"result":{}}`, "", ""},
		{`not json`, "", ""},
		{`{"method":7,"params":{"name":{"x":1}}}`, "", ""},
		{`{"method":"tools/call","params":[1,2]}`, "tools/call", ""},
	}
	for _, c := range cases {
		b := []byte(c.body)
		if len(b) > metricsPeekBytes {
			b = b[:metricsPeekBytes]
		}
		m, tool := peekLabels(b)
		if m != c.method || tool != c.tool {
			t.Errorf("peekLabels(%.80s…) = %q, %q; want %q, %q", c.body, m, tool, c.method, c.tool)
		}
	}
}

// The closed tool list is the tool list a client receives.
func TestMetricsToolListMatchesServer(t *testing.T) {
	if got := keys(serverTools(t)); !slices.Equal(got, toolNames) {
		t.Errorf("toolNames %v, server registers %v", toolNames, got)
	}
}
