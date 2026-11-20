package upstream_test

// The client's guards and what it counts. Its answers are tested through the
// tools that read them (internal/tools). An external test package: the fake
// /v2 it reads (upstreamtest) imports this package; the one internal it sets
// (the HTTP timeout) is exported to it by export_test.go.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/metrics"
	"github.com/basphere/ratatosk-mcp/internal/metrics/metricstest"
	"github.com/basphere/ratatosk-mcp/internal/upstream"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// The backstop where the fan-out happens: an empty slug must never page.
func TestProjectReleasesRefusesEmptyProject(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream was called (%s) with an empty project", r.URL.RequestURI())
	}))
	if _, err := upstreamtest.Client(t, ts, nil).ProjectReleases(context.Background(), ""); err == nil {
		t.Fatal("an empty project paged every project")
	}
}

func TestMetricsUpstreamOutcomes(t *testing.T) {
	m := metrics.New("test-1.1")
	f := upstreamtest.NewEtcd()
	api := upstreamtest.Client(t, httptest.NewServer(f.Handler(t)), m)
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
	api = upstreamtest.Client(t, dead, m)
	_, _ = api.SearchRaw(logging.RequestContext(context.Background(), "search", nil), "x", "")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	api = upstreamtest.Client(t, slow, m)
	upstream.SetHTTPTimeout(api, 50*time.Millisecond)
	_, _ = api.SearchRaw(logging.RequestContext(context.Background(), "search", nil), "x", "")
	cctx, cancel := context.WithCancel(logging.RequestContext(context.Background(), "check_stack", nil))
	cancel()
	_, _ = api.ProjectsRaw(cctx, false)

	s := metricstest.Series(t, metrics.Handler(m))
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
	if got := metricstest.Sum(s, "ratatosk_mcp_upstream_requests_total{"); got != 6 {
		t.Errorf("%v upstream requests counted, want 6", got)
	}
}
