// Package httpfronttest sets up the refusals of the HTTP front for tests: a
// rate limiter that refuses, the request body that is cheapest to send and
// was dearest to label, and a body that counts what is read of it — one
// set-up, so a refusal is held to the same bounds with metrics off and on. It
// is imported by tests only.
//
// It imports internal/httpfront for the limiter, so a test of that package
// that uses it is an external test package (package httpfront_test).
package httpfronttest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/httpfront"
)

// RefusedCaller is the address RefusingLimiter has spent its budget for.
const RefusedCaller = "192.0.2.1"

// RefusingLimiter refuses every request after its first: a limit of 1 a
// minute, already spent for RefusedCaller — by one request through the front,
// the way a caller spends it.
func RefusingLimiter() *httpfront.RateLimiter {
	rl := httpfront.NewRateLimiter(1)
	r := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	r.RemoteAddr = RefusedCaller + ":1"
	httpfront.Handler(http.NotFoundHandler(), httpfront.Options{RateLimiter: rl}).ServeHTTP(httptest.NewRecorder(), r)
	return rl
}

// CountingBody counts in N what is read of a request body R.
type CountingBody struct {
	R io.Reader
	N int
}

func (c *CountingBody) Read(b []byte) (int, error) {
	n, err := c.R.Read(b)
	c.N += n
	return n, err
}

// ZerosCall is a tools/call whose arguments are n bytes of "0," tokens before
// the tool name: the cheapest body to send and, before 1.1.0's bounds, the
// dearest to label (independent review, 2026-11-18: 25.7 ms and 3.4 MB of
// allocations per refusal at 64 KiB).
func ZerosCall(n int) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"arguments":[` + strings.Repeat("0,", n/2) + `0],"name":"check_stack"}}`
}
