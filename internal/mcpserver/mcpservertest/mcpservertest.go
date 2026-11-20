// Package mcpservertest connects a client to an MCP server in memory, so a
// test lists and calls tools exactly as a client on the wire does: through
// schema validation and every middleware. It is imported by tests only — the
// server's own tests, the counting tests of internal/metrics and the docs
// tests at the repository root.
//
// It takes the server built, rather than building it, and imports nothing
// from this module, so the server package's own tests can use it.
package mcpservertest

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Connect connects client to server over in-memory transports. The session
// is closed when the test ends.
func Connect(t testing.TB, server *mcp.Server, client *mcp.Implementation) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(client, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}
