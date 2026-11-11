package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	// The whitespace skip is bounded, and the bound refuses: a batch behind
	// 64 KiB of spaces used to be handed on and run.
	pad := strings.Repeat(" ", 64<<10)
	if code := post(pad + `[` + single + `]`); code != http.StatusBadRequest || seen != "" {
		t.Errorf("a batch behind 64 KiB of whitespace must be refused: %d, handler saw %d bytes", code, len(seen))
	}
	if code := post(pad + "\n\n" + single); code != http.StatusBadRequest || seen != "" {
		t.Errorf("more whitespace than the bound is refused whatever follows: %d", code)
	}
	if code := post(pad[:1000] + single); code != http.StatusOK || seen != single {
		t.Errorf("ordinary padding passes: %d", code)
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

// The chain main() serves, held to account for its order: a batch never
// reaches the handler, and the attempt itself is counted by the limiter. With
// the refusal taken out of the chain this fails.
func TestHTTPChainRefusesBatchesBehindTheLimiter(t *testing.T) {
	calls := 0
	h := mcpHTTPChain(newRateLimiter(2), 4, time.Second, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	single := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}`
	post := func(body string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
		return rec.Code
	}
	if code := post("[" + single + "," + single + "," + single + "]"); code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("batch: got %d with %d handler calls, want 400 and 0", code, calls)
	}
	if code := post(single); code != http.StatusOK || calls != 1 {
		t.Fatalf("a single call after it: got %d with %d handler calls", code, calls)
	}
	// Limit 2: the refused batch was the first request, the single call the second.
	if code := post(single); code != http.StatusTooManyRequests || calls != 1 {
		t.Fatalf("the refused batch must have counted against the limit: got %d with %d handler calls", code, calls)
	}
}

// A batch is refused before it can wait for, or take, an in-flight slot: with
// the only slot held it gets 400 at once, not a wait and a 503.
func TestHTTPChainRefusesBatchesBeforeTheInflightCap(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	h := mcpHTTPChain(nil, 1, 2*time.Second, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	single := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(single)))
		close(done)
	}()
	<-started
	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("["+single+"]")))
	if rec.Code != http.StatusBadRequest || time.Since(start) > time.Second {
		t.Errorf("batch while the slot is held: got %d after %s, want 400 at once", rec.Code, time.Since(start))
	}
	close(release)
	<-done
}
