package main

import (
	"net/http"
	"net/http/httptest"
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

func TestInflightOffByDefault(t *testing.T) {
	for _, raw := range []string{"", "0", "-3", "x"} {
		if n := inflightFromEnv(raw); n != 0 {
			t.Errorf("inflightFromEnv(%q) = %d, want 0 (off)", raw, n)
		}
	}
	if n := inflightFromEnv("16"); n != 16 {
		t.Errorf("inflightFromEnv(16) = %d", n)
	}
}

func TestRespCacheByteBudget(t *testing.T) {
	c := newRespCache(time.Minute, 100, 1000)
	c.put("a", make([]byte, 100))
	c.put("b", make([]byte, 100))
	if _, ok := c.get("a"); !ok {
		t.Fatal("a should be cached")
	}
	c.put("big", make([]byte, 200)) // over maxBytes/8 — not cached
	if _, ok := c.get("big"); ok {
		t.Fatal("a body over an eighth of the budget must not be cached")
	}
	for i := 0; i < 20; i++ {
		c.put(string(rune('c'+i)), make([]byte, 100))
		if c.size > c.maxBytes {
			t.Fatalf("size %d over budget %d", c.size, c.maxBytes)
		}
	}
	c.put("b", make([]byte, 50)) // replacing a key must not double-count
	total := 0
	for _, e := range c.data {
		total += len(e.body)
	}
	if total != c.size {
		t.Fatalf("tracked size %d, actual %d", c.size, total)
	}
}
