package upstream

// The client's guards and what it counts. Its answers are tested through the
// tools that read them (internal/tools).

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/metrics"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// The backstop where the fan-out happens: an empty slug must never page.
func TestProjectReleasesRefusesEmptyProject(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream was called (%s) with an empty project", r.URL.RequestURI())
	}))
	defer ts.Close()
	if _, err := New(ts.URL, "dev", 32<<20, nil).ProjectReleases(context.Background(), ""); err == nil {
		t.Fatal("an empty project paged every project")
	}
}

func TestMetricsUpstreamOutcomes(t *testing.T) {
	m := metrics.New("test-1.1")
	f := upstreamtest.NewEtcd()
	ts := httptest.NewServer(f.Handler(t))
	t.Cleanup(ts.Close)
	api := New(ts.URL, "dev", 32<<20, m)
	ctx := logging.RequestContext(context.Background(), "get_release", nil)
	if _, err := api.ReleaseRaw(ctx, "etcd", "v3.6.8", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ReleaseRaw(ctx, "etcd", "v0.0.1", "", false); err == nil {
		t.Fatal("a version not on record must be an error")
	}
	// A cache hit is not a request.
	if _, err := api.ProjectsRaw(logging.RequestContext(context.Background(), "list_projects", nil), true); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ProjectsRaw(logging.RequestContext(context.Background(), "list_projects", nil), true); err != nil {
		t.Fatal(err)
	}
	// Nothing listening: transport. A slow answer past the client timeout: timeout.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	api = New(dead.URL, "dev", 32<<20, m)
	_, _ = api.SearchRaw(logging.RequestContext(context.Background(), "search", nil), "x", "")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	api = New(slow.URL, "dev", 32<<20, m)
	api.http.Timeout = 50 * time.Millisecond
	_, _ = api.SearchRaw(logging.RequestContext(context.Background(), "search", nil), "x", "")
	cctx, cancel := context.WithCancel(logging.RequestContext(context.Background(), "check_stack", nil))
	cancel()
	_, _ = api.ProjectsRaw(cctx, false)

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

// series parses the metrics exposition into "name{labels}" → value.
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

func sumPrefix(s map[string]float64, prefix string) float64 {
	n := 0.0
	for k, v := range s {
		if strings.HasPrefix(k, prefix) {
			n += v
		}
	}
	return n
}
