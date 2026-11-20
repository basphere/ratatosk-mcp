package httpfront

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInflightMiddlewareCapsConcurrentRequests(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	h := inflightMiddleware(1, 50*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	first := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
		first <- rec.Code
	}()
	<-started

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("second request while the only slot is held: got %d, want 503 with Retry-After", rec.Code)
	}

	close(release)
	if code := <-first; code != http.StatusOK {
		t.Fatalf("first request: got %d", code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("after the slot is free: got %d", rec.Code)
	}
}

// In stateful mode the client's event stream is a GET held open for the whole
// session. It does no work and must not take a slot from the session's calls.
// (Independent review, 2026-11-11.)
func TestInflightCapCountsOnlyPosts(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	h := inflightMiddleware(1, 50*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			started <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}))
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp", nil))
		close(done)
	}()
	<-started
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}")))
	if rec.Code != http.StatusOK {
		t.Errorf("a POST while the stream is open: got %d, want 200", rec.Code)
	}
	close(release)
	<-done
}
