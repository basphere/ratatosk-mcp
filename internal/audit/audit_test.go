package audit

// P3 "done means", the record itself: metadata mode records names and never
// values (the marker probe extends here); full mode records values. The wire
// round trip — a real client session carrying clientInfo into the record, and
// zero bytes with auditing off — goes through the server (internal/mcpserver).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAuditRecordModes(t *testing.T) {
	raw := json.RawMessage(`{"components":[{"project":"envoy","version":"9.9.9-MRKVER"}],"detail":"full"}`)
	client := &mcp.Implementation{Name: "probe", Version: "1"}

	meta := auditRecord("metadata", "check_stack", raw, "ok", client, "stdio", "")
	b, _ := json.Marshal(meta)
	if strings.Contains(string(b), "MRKVER") {
		t.Fatalf("metadata mode leaked an argument value: %s", b)
	}
	names, _ := meta["argument_names"].([]string)
	if len(names) != 2 || names[0] != "components" || names[1] != "detail" {
		t.Errorf("argument_names = %v, want sorted top-level names", meta["argument_names"])
	}
	if meta["event"] != "audit" || meta["tool"] != "check_stack" || meta["outcome"] != "ok" {
		t.Errorf("record fields wrong: %v", meta)
	}

	full := auditRecord("full", "check_stack", raw, "tool_error", client, "http", "4bf92f3577b34da6a3ce929d0e0e4736")
	fb, _ := json.Marshal(full)
	if !strings.Contains(string(fb), "MRKVER") {
		t.Errorf("full mode must carry argument values")
	}
	if full["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace_id missing from record: %v", full)
	}
}
