package main

import (
	"net/http"
	"strconv"
	"time"
)

// inflightMiddleware caps how many requests the HTTP transport works on at once.
//
// Why it exists: per-caller rate limiting bounds each caller, not the crowd.
// Many callers each within their own limit can still arrive together, and a
// stack-wide check holds tens of MiB while it runs (measured 2026-11-05, ten
// components: 29 MiB brief, 52 MiB detail=full). Without a cap the hosted
// container was killed at two full checks in flight — and a kill drops every
// other caller's answer with it. Requests over the cap wait briefly for a slot,
// then get 503 with Retry-After, which a client can retry.
//
// MCP_MAX_INFLIGHT: absent, zero or unparseable means off — the default, and
// what a single-tenant self-hosted server wants.
func inflightMiddleware(n int, wait time.Duration, next http.Handler) http.Handler {
	if n <= 0 {
		return next
	}
	slots := make(chan struct{}, n)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Only a POST does work. In stateful mode a client keeps a GET open as
		// its event stream for the whole session; counted, that stream held a
		// slot and, with a cap of 1, the session's own next call got 503
		// (reproduced 2026-11-11).
		if req.Method != http.MethodPost {
			next.ServeHTTP(w, req)
			return
		}
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case slots <- struct{}{}:
		case <-t.C:
			refusedFor(req, "busy") // before the answer: it may read the body (metrics.go)
			w.Header().Set("Retry-After", "5")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"server busy, retry shortly"}`))
			return
		case <-req.Context().Done():
			// The caller stopped waiting for a slot: still a request the cap
			// turned away, and counted as such.
			refusedFor(req, "busy")
			return
		}
		defer func() { <-slots }()
		next.ServeHTTP(w, req)
	})
}

func inflightFromEnv(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
