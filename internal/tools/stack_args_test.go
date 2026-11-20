package tools

// check_stack's components: argument tolerance (0.8–0.9.1) — the shapes
// models send are read, a component without a project is refused with the
// shape, and what is echoed back is bounded.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCheckStackAcceptsTheShapesModelsSend(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"documented array", `{"components":[{"project":"cilium","version":"v1.19.5"}]}`},
		{"array JSON-encoded as a string", `{"components":"[{\"project\": \"cilium\", \"version\": \"v1.19.5\"}]"}`},
		{"name instead of project", `{"components":[{"name":"cilium","version":"v1.19.5"}]}`},
		{"string form with name key", `{"components":"[{\"name\": \"cilium\", \"version\": \"v1.19.5\"}]"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got checkStackArgs
			if err := json.Unmarshal([]byte(tc.args), &got); err != nil {
				t.Fatalf("rejected a shape we promised to accept: %v", err)
			}
			if len(got.Components) != 1 || got.Components[0].Project != "cilium" || got.Components[0].Version != "v1.19.5" {
				t.Fatalf("landed wrong: %+v", got.Components)
			}
		})
	}
}

// The schema is what stops the SDK refusing the string form before the handler
// runs. If the widening is lost, the tolerance above becomes unreachable.
func TestCheckStackSchemaAcceptsBothComponentForms(t *testing.T) {
	s := checkStackSchema()
	comp := s.Properties["components"]
	if len(comp.AnyOf) != 2 {
		t.Fatalf("components schema is not widened: %+v", comp)
	}
	array := comp.AnyOf[0]
	if array.Type != "array" && !slices.Contains(array.Types, "array") {
		t.Fatalf("first branch is not the documented array form: %+v", array)
	}
	if comp.AnyOf[1].Type != "string" {
		t.Fatalf("second branch should accept the JSON-encoded string, got %q", comp.AnyOf[1].Type)
	}
}

func TestComponentsRejectionShowsTheShape(t *testing.T) {
	var got checkStackArgs
	err := json.Unmarshal([]byte(`{"components":"not json at all"}`), &got)
	if err == nil {
		t.Fatal("garbage was accepted")
	}
	if !strings.Contains(err.Error(), `[{"project":"cilium","version":"v1.19.5"}]`) {
		t.Fatalf("rejection does not show the correct shape: %v", err)
	}
}

// Replay of the hub's 2026-09-28 call: project_slug instead of project. It must
// be refused before any upstream request, naming the key that went unread.
func TestCheckStackRefusesComponentWithoutProject(t *testing.T) {
	h := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream was called (%s) for a component with no project", r.URL.RequestURI())
	})))
	cases := []struct {
		name, args, want string
	}{
		{"hub replay, string form", `{"components":"[{\"version\": \"v1.35.0\", \"project_slug\": \"kubernetes\"}, {\"version\": \"2.2.0\", \"project_slug\": \"containerd\"}]","detail":"brief"}`,
			"component 1 has no project (keys received: project_slug, version)"},
		{"empty project value", `{"components":[{"project":"","version":"v1.19.5"}]}`,
			"component 1 has no project (keys received: project, version)"},
		{"only the second is missing", `{"components":[{"project":"cilium","version":"v1.19.5"},{"slug":"envoy","version":"v1.36.8"}]}`,
			"component 2 has no project (keys received: slug, version)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var args checkStackArgs
			if err := json.Unmarshal([]byte(tc.args), &args); err != nil {
				t.Fatalf("decode should stay tolerant; the refusal belongs to the handler: %v", err)
			}
			res, _, err := h.checkStackTool(context.Background(), nil, args)
			if err != nil {
				t.Fatal(err)
			}
			text := res.Content[0].(*mcp.TextContent).Text
			if !res.IsError || !strings.Contains(text, tc.want) || !strings.Contains(text, `[{"project":"cilium","version":"v1.19.5"}]`) {
				t.Fatalf("refusal = %q, want it to contain %q and the shape", text, tc.want)
			}
		})
	}
}

// Keys are matched without regard to case, as the decode does, and what is
// echoed back is bounded.
// (Independent review, 2026-11-11.)
func TestUnreadKeysMatchTheDecodeAndAreBounded(t *testing.T) {
	var c stackComponent
	if err := json.Unmarshal([]byte(`{"Project":"etcd","Version":"v3.5.30","Target_Version":"v3.5.31"}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.Project != "etcd" || c.TargetVersion != "v3.5.31" || c.unreadKeys != "" {
		t.Errorf("keys that were read must not be reported unread: %+v", c)
	}
	junk := `{"project":"etcd","version":"v1"`
	for i := 0; i < 30; i++ {
		junk += `,"k` + strings.Repeat("x", 100) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":1`
	}
	junk += `}`
	if err := json.Unmarshal([]byte(junk), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.unreadKeys) > 8*45+20 || !strings.Contains(c.unreadKeys, "(+22 more)") {
		t.Errorf("thirty long keys must come back as eight short ones and a count: %d chars %q", len(c.unreadKeys), c.unreadKeys)
	}
}

// The keys of a component that named no project are echoed in the refusal;
// that echo is bounded too.
// (Independent review, 2026-11-11.)
func TestSentKeysAreBounded(t *testing.T) {
	var c stackComponent
	if err := json.Unmarshal([]byte(`{"`+strings.Repeat("k", 300000)+`":1}`), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.sentKeys) > 200 {
		t.Errorf("a 300,000-character key came back as %d characters", len(c.sentKeys))
	}
}
