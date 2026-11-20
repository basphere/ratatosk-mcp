package upstream

import "time"

// What the external tests in package upstream_test (client_test.go) read and
// set of this package's internals. Compiled into tests only.

// SetHTTPTimeout replaces c's HTTP client timeout (20 s), to make a slow
// answer time out within a test.
func SetHTTPTimeout(c *Client, d time.Duration) { c.http.Timeout = d }

// ErrorBody is the upstream answer an HTTPError carries, as bounded.
func ErrorBody(e *HTTPError) string { return e.body }
