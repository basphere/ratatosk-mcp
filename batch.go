package main

import (
	"bufio"
	"io"
	"net/http"
)

// rejectBatchMiddleware turns away a POST whose body is a JSON-RPC batch (a
// JSON array of calls).
//
// Why it exists: the rate limiter and the in-flight cap both count HTTP
// requests, but the SDK runs every call in a batch, concurrently, whenever the
// Mcp-Protocol-Version header is absent or 2025-03-26. One request could carry
// any number of tool calls past both limits. Reproduced 2026-11-11: twenty
// list_projects calls in one POST all ran under a limit of 5 per minute, and
// eight full stack checks in one POST took the process past 1 GiB with the cap
// set to 1. Protocol revisions 2025-06-18 and later already refuse batches; this
// makes every revision behave the same, so one request is one call.
func rejectBatchMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.Body == nil {
			next.ServeHTTP(w, req)
			return
		}
		br := bufio.NewReader(req.Body)
		// Skip leading JSON whitespace to find the first real byte. Bounded, so a
		// body of nothing but spaces is handed on to the SDK's own size limit
		// rather than read here without end.
		for skipped := 0; skipped < 64<<10; skipped++ {
			b, err := br.Peek(1)
			if err != nil {
				break
			}
			if c := b[0]; c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				_, _ = br.Discard(1)
				continue
			}
			if b[0] == '[' {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"JSON-RPC batches are not accepted; send one request per POST"}}`))
				return
			}
			break
		}
		req.Body = struct {
			io.Reader
			io.Closer
		}{br, req.Body}
		next.ServeHTTP(w, req)
	})
}
