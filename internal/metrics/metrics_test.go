package metrics

// The counters and their exposition, the body scanner, and the HTTP layer on
// its own. Counting through the whole HTTP chain and the server — each POST
// once, every outcome — is in counting_test.go.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/basphere/ratatosk-mcp/internal/metrics/metricstest"
)

// Every known combination is there at zero before the first request.
func TestMetricsStartAtZero(t *testing.T) {
	s := metricstest.Series(t, Handler(New("test-1.1")))
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
	for _, k := range []string{metricstest.Requests("tools/call", "check_stack", "busy", "true"), metricstest.Requests("initialize", "", "rate_limited", "false"),
		`ratatosk_mcp_request_duration_seconds_count{tool="search"}`, `ratatosk_mcp_response_bytes_bucket{tool="unknown",le="+Inf"}`} {
		if _, ok := s[k]; !ok {
			t.Errorf("%s not laid down at zero", k)
		}
	}
}

func TestMetricsHistograms(t *testing.T) {
	m := New("test-1.1")
	m.observeTool("check_stack", 50*time.Millisecond, 1024)
	m.observeTool("check_stack", 300*time.Millisecond, 200<<10)
	m.observeTool("check_stack", 100*time.Second, 5<<20)
	m.observeTool("no_such_tool", time.Second, -1)
	s := metricstest.Series(t, Handler(m))
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

func TestMetricsEndpoint(t *testing.T) {
	ts := httptest.NewServer(Handler(New("test-1.1")))
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

// A refusal written before the body was read (the SDK's Accept check), to a
// client that waits for 100 Continue before it sends the body: once a status
// is written net/http sends no 100 Continue, so the count must not wait for a
// body that will not come. (In the full chain the batch refusal reads the
// first byte before any status, which still triggers 100 Continue.)
func TestMetricsDoNotWaitForABodyBehindExpect(t *testing.T) {
	m := New("test-1.1")
	h := m.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	if got := metricstest.Series(t, Handler(m))[metricstest.Requests("other", "", "error", "false")]; got != 1 {
		t.Errorf("other/error = %v, want 1", got)
	}
}

func TestPeekLabels(t *testing.T) {
	big := `{"components":[` + strings.Repeat(`{"project":"x","version":"v1"},`, 3000) + `{}]}`
	cases := []struct{ body, method, tool string }{
		{metricstest.ToolCall(1, "check_stack", `{}`), "tools/call", "check_stack"},
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

// The scanner reads keys as the SDK's decoder does (checked against go-sdk
// v1.7.0 on 2026-11-18): escapes decoded, case-sensitive, the last of a
// repeated key wins. A refused or rejected request is labelled from it.
func TestPeekLabelsReadKeysAsTheSDK(t *testing.T) {
	for _, c := range []struct{ body, method, tool string }{
		{`{"\u006dethod":"tools/list"}`, "tools/list", ""},
		{`{"method":"tools/call","\u0070arams":{"n\u0061me":"search"}}`, "tools/call", "search"},
		{`{"method":"tools/call","params":{"name":"check_stack"},"\u006dethod":"notifications/initialized"}`, "notifications/initialized", "check_stack"},
		{`{"METHOD":"tools/list"}`, "", ""},
		{`{"method":"tools/call","method":7}`, "", ""},
		{`{"method":"tools/call","params":{"name":"get_release"},"params":{"arguments":{}}}`, "tools/call", ""},
		{`{"method":"tools/call","params":{"name":"get_release"},"params":null}`, "tools/call", ""},
		{`{"method":"tools/call","params":{"name":"search","name":5}}`, "tools/call", ""},
		{`{"method":"tools/call","params":{"name":"search","\u006eame":"get_release"}}`, "tools/call", "get_release"},
		{`{"\u00e9method":"tools/list"}`, "", ""},
		{`{"\u006dethod`, "", ""},
	} {
		if m, tool := peekLabels([]byte(c.body)); m != c.method || tool != c.tool {
			t.Errorf("peekLabels(%s) = %q, %q; want %q, %q", c.body, m, tool, c.method, c.tool)
		}
	}
}

// blockingWriter holds every Write until release is closed.
type blockingWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	close(b.entered)
	<-b.release
	return len(p), nil
}

// A scrape that reads slowly does not hold the counters: writeTo wrote the
// exposition with the lock held, so every request waited on the scraper's
// network write (final review, 2026-11-18).
func TestMetricsSlowScrapeDoesNotStallCounting(t *testing.T) {
	m := New("test")
	w := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	scraped := make(chan struct{})
	go func() {
		_ = m.writeTo(w)
		close(scraped)
	}()
	<-w.entered
	counted := make(chan struct{})
	go func() {
		m.countRequest("tools/call", "search", "ok", false)
		m.CountUpstream("search", "ok")
		m.observeTool("search", time.Millisecond, 10)
		close(counted)
	}()
	select {
	case <-counted:
	case <-time.After(2 * time.Second):
		t.Error("counting waited for a scrape's network write")
	}
	close(w.release)
	<-scraped
	<-counted
}
