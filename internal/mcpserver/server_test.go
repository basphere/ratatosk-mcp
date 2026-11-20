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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/audit"
	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/mcpserver/mcpservertest"
	"github.com/basphere/ratatosk-mcp/internal/metrics"
	"github.com/basphere/ratatosk-mcp/internal/metrics/metricstest"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// sdkSession connects an in-memory client to the real server, so a call passes
// schema validation and every middleware exactly as it does on the wire.
func sdkSession(t *testing.T, o Options) *mcp.ClientSession {
	t.Helper()
	return mcpservertest.Connect(t, New(o), &mcp.Implementation{Name: "review-test", Version: "0"})
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
	api := upstreamtest.Client(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/releases/etcd/") {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		inner(w, r)
	})), nil)
	cs := sdkSession(t, Options{API: api, timeout: 200 * time.Millisecond})
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
// server is the whole server; the record it checks is the audit middleware's
// alone.
func startAuditedSession(t *testing.T, a *audit.Auditor) *mcp.ClientSession {
	t.Helper()
	api := upstreamtest.Client(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"projects":[]}`))
	})), nil)
	server := New(Options{Version: "test", API: api, Audit: a})
	return mcpservertest.Connect(t, server, &mcp.Implementation{Name: "audit-probe", Version: "7"})
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

// A tool call that panics is answered as a tool error, logged in one line
// without what the call carried, and counted outcome="error"; the server keeps
// serving. A release document without warnings made check_stack panic, and
// the panic took the whole server down (independent review, 2026-11-21).
func TestToolCallPanicIsAnsweredAsAToolError(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	logging.Setup(&buf, slog.LevelDebug)
	t.Cleanup(func() { slog.SetDefault(prev) })

	const marker = "v3.5.30-panic-marker"
	m := metrics.New("test")
	api := upstreamtest.Client(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)), m)
	server := New(Options{API: api, Metrics: m})
	type searchIn struct {
		Q string `json:"q"`
	}
	// Replaces search: a handler that panics with what the call carried.
	mcp.AddTool(server, &mcp.Tool{Name: "search"}, func(_ context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
		panic("cannot read " + in.Q)
	})
	cs := mcpservertest.Connect(t, server, &mcp.Implementation{Name: "panic-test", Version: "0"})

	text, isErr := sdkText(t, cs, "search", map[string]any{"q": marker})
	if !isErr || text != panicAnswer {
		t.Errorf("the panicked call: isError=%v %q, want the internal-error answer", isErr, text)
	}
	if text, isErr := sdkText(t, cs, "list_projects", nil); isErr || !strings.Contains(text, `"etcd"`) {
		t.Errorf("the next call: isError=%v %.200s", isErr, text)
	}

	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(l), &rec) == nil && rec["msg"] == "tool call panicked" {
			lines = append(lines, rec)
		}
	}
	if len(lines) != 1 || lines[0]["level"] != "ERROR" || lines[0]["tool"] != "search" || lines[0]["panic_type"] != "string" {
		t.Errorf("want one ERROR line with tool search and panic_type string, got %v", lines)
	}
	if strings.Contains(buf.String(), marker) || strings.Contains(buf.String(), "cannot read") {
		t.Errorf("the log carries what the call sent or the panic's message:\n%s", buf.String())
	}

	series := metricstest.Series(t, metrics.Server(m).Handler)
	if got := series[metricstest.Requests("tools/call", "search", "error", "false")]; got != 1 {
		t.Errorf(`tools/call search outcome="error" = %v, want 1`, got)
	}
	if got := series[metricstest.Requests("tools/call", "list_projects", "ok", "false")]; got != 1 {
		t.Errorf(`tools/call list_projects outcome="ok" = %v, want 1`, got)
	}
}
