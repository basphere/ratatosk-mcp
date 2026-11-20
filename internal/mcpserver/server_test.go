package mcpserver

// The server as a client sees it, over in-memory transports: what it adds
// around the tools — the per-call deadline and the audit stream (P3 "done
// means": the wire round trip carries clientInfo into the record; with
// auditing off, zero audit bytes). Calls go through the SDK on purpose: the
// SDK checks arguments against the schema and applies middleware before a
// handler runs, and handler-level tests (internal/tools) cannot see either.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/audit"
	"github.com/basphere/ratatosk-mcp/internal/upstream"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// withAPI is a client of ts, which is closed when the test ends.
func withAPI(t *testing.T, ts *httptest.Server) *upstream.Client {
	t.Helper()
	t.Cleanup(ts.Close)
	return upstream.New(ts.URL, "dev", 32<<20, nil) // 32 MiB: the default cache budget
}

// sdkSession connects an in-memory client to the real server, so a call passes
// schema validation and every middleware exactly as it does on the wire.
func sdkSession(t *testing.T, o Options) *mcp.ClientSession {
	t.Helper()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := New(o).Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "review-test", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func sdkText(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return err.Error(), true
	}
	if len(res.Content) == 0 {
		t.Fatal("empty tool result")
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

// toolDescriptions lists the descriptions exactly as a client receives them.
func toolDescriptions(t *testing.T) map[string]string {
	t.Helper()
	res, err := sdkSession(t, Options{}).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tool := range res.Tools {
		out[tool.Name] = tool.Description
	}
	return out
}

// A slow upstream used to run a call into the HTTP server's write timeout and
// the caller got an empty reply. Now the call ends on its own deadline with an
// answer that says what was not checked.
// (Independent review, 2026-11-11.)
func TestToolCallEndsOnItsDeadlineWithAnAnswer(t *testing.T) {
	f := upstreamtest.NewEtcd()
	inner := f.Handler(t)
	api := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/releases/etcd/") {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		inner(w, r)
	})))
	cs := sdkSession(t, Options{API: api, ToolTimeout: 200 * time.Millisecond})
	start := time.Now()
	text, isErr := sdkText(t, cs, "check_stack", map[string]any{
		"components": []any{map[string]any{"project": "etcd", "version": "v3.5.30"}},
	})
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the call ran %s past a 200ms deadline", took)
	}
	if isErr || !strings.Contains(text, `"could_not_check"`) || !strings.Contains(text, "ran out of time") {
		t.Errorf("want an answer naming the component as not checked for lack of time, got error=%v %s", isErr, text)
	}
}

// startAuditedSession wires a real client/server pair over in-memory
// transports with the audit middleware installed, api pointed at a stub. The
// server is the whole server (until 2026-11-21 a bare one with list_projects
// alone); the record is the audit middleware's either way.
func startAuditedSession(t *testing.T, a *audit.Auditor) *mcp.ClientSession {
	t.Helper()
	api := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"projects":[]}`))
	})))
	server := New(Options{Version: "test", API: api, Audit: a})
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
