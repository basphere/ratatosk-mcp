package httpfront

// A refusal with metrics off is the 1.0.0 refusal. How refusals are counted
// with metrics on is in internal/metrics (counting_test.go).

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// With metrics off a refusal is the 1.0.0 one: the refusing layer reads not a
// byte of the body, and answers the same status, headers and body. (The batch
// check, which sits before the in-flight cap since 1.0.0, peeks the first
// bytes of a POST; that is the in-flight refusal's 1.0.0 behaviour, so the cap
// is held to account on its own here.)
func TestRefusalWithMetricsOffReadsNothing(t *testing.T) {
	h := chain(Options{RateLimiter: refusingLimiter()}, http.NotFoundHandler())
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
func refusingLimiter() *RateLimiter {
	rl := NewRateLimiter(1)
	rl.Allow("192.0.2.1", time.Now())
	return rl
}

// zerosCall is a tools/call whose arguments are n bytes of "0," tokens before
// the tool name: the cheapest body to send and, before 1.1.0's bounds, the
// dearest to label (independent review, 2026-11-18: 25.7 ms and 3.4 MB of
// allocations per refusal at 64 KiB).
func zerosCall(n int) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"arguments":[` + strings.Repeat("0,", n/2) + `0],"name":"check_stack"}}`
}
