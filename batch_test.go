package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectBatchMiddleware(t *testing.T) {
	var seen string
	h := rejectBatchMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		seen = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	post := func(body string) int {
		seen = ""
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
		return rec.Code
	}

	single := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	if code := post(single); code != http.StatusOK || seen != single {
		t.Errorf("a single request must pass through whole: %d %q", code, seen)
	}
	if code := post(" \r\n\t" + single); code != http.StatusOK || seen != single {
		t.Errorf("leading whitespace: %d %q", code, seen)
	}
	for _, batch := range []string{`[` + single + `,` + single + `]`, "\n  [" + single + "]", `[]`} {
		if code := post(batch); code != http.StatusBadRequest || seen != "" {
			t.Errorf("batch %q must be refused before the handler: %d, handler saw %q", batch, code, seen)
		}
	}
	if code := post(""); code != http.StatusOK {
		t.Errorf("an empty body is the SDK's to refuse: %d", code)
	}

	// GET (the stateful SSE stream) has no body to look at.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET must pass: %d", rec.Code)
	}
}

// One POST is one call for the limiter: a batch never reaches the handler, so
// it cannot run calls the limiter did not count.
func TestBatchDoesNotSlipPastTheRateLimit(t *testing.T) {
	calls := 0
	h := rateLimitMiddleware(newRateLimiter(1), rejectBatchMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})))
	single := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("["+single+","+single+","+single+"]")))
	if rec.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("batch: got %d with %d handler calls, want 400 and 0", rec.Code, calls)
	}
}
