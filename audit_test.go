package main

// P3 "done means", as tests: default-off leaves zero audit bytes; metadata
// mode records names and never values (the marker probe extends here); full
// mode records values; the wire round-trip through a real client session
// carries clientInfo into the record.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/audit"
)

// startAuditedSession wires a real client/server pair over in-memory
// transports with the audit middleware installed, api pointed at a stub.
func startAuditedSession(t *testing.T, a *audit.Auditor) *mcp.ClientSession {
	t.Helper()
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"projects":[]}`))
	})))
	server := mcp.NewServer(&mcp.Implementation{Name: "ratatosk", Version: "test"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "list_projects", Description: "t"}, listProjectsTool)
	server.AddReceivingMiddleware(a.Middleware)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "audit-probe", Version: "7"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestAuditWireRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	cs := startAuditedSession(t, audit.New("metadata", &buf))
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_projects", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("no audit record emitted in metadata mode")
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("audit record is not one-line JSON: %q", line)
	}
	if rec["event"] != "audit" || rec["tool"] != "list_projects" || rec["outcome"] != "ok" {
		t.Errorf("record fields wrong: %v", rec)
	}
	if rec["client_name"] != "audit-probe" || rec["client_version"] != "7" {
		t.Errorf("caller identity missing: %v", rec)
	}
}

func TestAuditDefaultOff(t *testing.T) {
	var buf bytes.Buffer
	cs := startAuditedSession(t, audit.New("", &buf)) // "": the default
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_projects", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("audit bytes emitted with auditing off: %s", buf.String())
	}
}
