// Package metricstest reads the /metrics exposition and builds the request
// bodies the counting tests send. It is imported by tests only: the counter
// tests of internal/metrics itself, its end-to-end tests, and the client's
// tests in internal/upstream.
//
// It imports nothing from this module, so the metrics package's own tests can
// use it: it reads the exposition through the http.Handler that serves it
// rather than from a *metrics.Metrics.
package metricstest

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// Series reads GET /metrics from h and parses the exposition into
// "name{labels}" → value. A series written twice fails t.
func Series(t testing.TB, h http.Handler) map[string]float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
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

// Requests is the name of one ratatosk_mcp_requests_total series.
func Requests(method, tool, outcome, self string) string {
	return `ratatosk_mcp_requests_total{method="` + method + `",outcome="` + outcome + `",self="` + self + `",tool="` + tool + `"}`
}

// Sum adds up every series whose name starts with prefix.
func Sum(s map[string]float64, prefix string) float64 {
	n := 0.0
	for k, v := range s {
		if strings.HasPrefix(k, prefix) {
			n += v
		}
	}
	return n
}

// ToolCall is a JSON-RPC tools/call request body.
func ToolCall(id int, name, args string) string {
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}
