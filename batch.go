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
	const maxLeadingSpace = 64 << 10
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.Body == nil {
			next.ServeHTTP(w, req)
			return
		}
		br := bufio.NewReader(req.Body)
		refuse := func(msg string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"` + msg + `"}}`))
		}
		// Skip leading JSON whitespace to find the first real byte. The skip is
		// bounded, and running into the bound is a refusal, not a pass: handing
		// the body on at that point let a batch through behind 64 KiB of spaces
		// (independent re-review, 2026-11-11). No client pads a request that far.
		for skipped := 0; ; skipped++ {
			b, err := br.Peek(1)
			if err != nil {
				break // empty or unreadable: the SDK's to refuse
			}
			if c := b[0]; c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				if skipped >= maxLeadingSpace {
					refuse("request body starts with too much whitespace")
					return
				}
				_, _ = br.Discard(1)
				continue
			}
			if b[0] == '[' {
				refuse("JSON-RPC batches are not accepted; send one request per POST")
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
