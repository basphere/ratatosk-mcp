package httpfront_test

// A refusal with metrics off is the 1.0.0 refusal. How refusals are counted
// with metrics on is in internal/metrics (counting_test.go). An external test
// package, so it shares the refusal set-up of httpfronttest with those; the
// chain and the in-flight cap are exported to it by export_test.go.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/basphere/ratatosk-mcp/internal/httpfront"
	"github.com/basphere/ratatosk-mcp/internal/httpfront/httpfronttest"
)

// With metrics off a refusal is the 1.0.0 one: the refusing layer reads not a
// byte of the body, and answers the same status, headers and body. (The batch
// check, which sits before the in-flight cap since 1.0.0, peeks the first
// bytes of a POST; that is the in-flight refusal's 1.0.0 behaviour, so the cap
// is held to account on its own here.)
func TestRefusalWithMetricsOffReadsNothing(t *testing.T) {
	h := httpfront.Chain(httpfront.Options{RateLimiter: httpfronttest.RefusingLimiter()}, http.NotFoundHandler())
	body := &httpfronttest.CountingBody{R: strings.NewReader(httpfronttest.ZerosCall(64 << 10))}
	r := httptest.NewRequest(http.MethodPost, "/mcp", body)
	r.RemoteAddr = "192.0.2.1:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if body.N != 0 {
		t.Errorf("read %d bytes of the body with metrics off", body.N)
	}
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || rec.Header().Get("Content-Type") != "application/json" ||
		rec.Body.String() != `{"error":"rate limit exceeded (1 requests/minute per caller)"}` {
		t.Errorf("refusal %d %v %q differs from 1.0.0's", rec.Code, rec.Header(), rec.Body.String())
	}

	// The in-flight cap: the slot is held, the next POST waits and is refused.
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	h = httpfront.InflightMiddleware(1, 20*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}")))
	<-started
	body = &httpfronttest.CountingBody{R: strings.NewReader(httpfronttest.ZerosCall(1 << 10))}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", body))
	close(release)
	if rec.Code != http.StatusServiceUnavailable || body.N != 0 || rec.Header().Get("Retry-After") != "5" || rec.Body.String() != `{"error":"server busy, retry shortly"}` {
		t.Errorf("busy refusal with metrics off: %d, read %d bytes, %q", rec.Code, body.N, rec.Body.String())
	}
}
