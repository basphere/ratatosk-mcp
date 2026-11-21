package mcpserver

// What the input schemas let through to a handler: the forms models send are
// accepted, and a refusal comes from the handler, with the shape, not from the
// schema.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// The array form used to be refused by the schema before the handler could
// read "name" or name the keys it received.
// (Independent review, 2026-11-11.)
func TestArrayFormReachesTheHandlerThroughTheSDK(t *testing.T) {
	cs := sdkSession(t, Options{API: upstreamtest.Client(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)), nil)})

	text, isErr := sdkText(t, cs, "check_stack", map[string]any{
		"components": []any{map[string]any{"name": "etcd", "version": "v3.5.31"}},
	})
	if isErr || !strings.Contains(text, `"project":"etcd"`) {
		t.Errorf(`"name" in the array form must be read as the project: error=%v %s`, isErr, text)
	}

	text, isErr = sdkText(t, cs, "check_stack", map[string]any{
		"components": []any{map[string]any{"project_slug": "etcd", "version": "v3.5.31"}},
	})
	if !isErr || !strings.Contains(text, "component 1 has no project (keys received: project_slug, version)") ||
		!strings.Contains(text, `[{"project":"cilium","version":"v1.19.5"}]`) {
		t.Errorf("a wrong key in the array form must get the handler's refusal with the shape, got: %s", text)
	}
}

// A key the server does not read is tolerated, and said: "target" meant as
// target_version must not return the whole path as if the bound had applied.
// A component without a version is answered per component, not by the schema.
// (Independent review, 2026-11-11.)
func TestUnreadKeysAndMissingVersionAreAnswered(t *testing.T) {
	cs := sdkSession(t, Options{API: upstreamtest.Client(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)), nil)})

	text, isErr := sdkText(t, cs, "check_stack", map[string]any{
		"components": []any{map[string]any{"project": "etcd", "version": "v3.5.30", "target": "v3.5.31", "extra": 1}},
	})
	if isErr || !strings.Contains(text, "key(s) not read: extra, target") {
		t.Errorf("unread keys must be named in note: error=%v %s", isErr, text)
	}

	text, isErr = sdkText(t, cs, "check_stack", map[string]any{
		"components": []any{map[string]any{"project": "etcd"}, map[string]any{"project": "etcd", "version": "v3.5.31"}},
	})
	if isErr || !strings.Contains(text, "running version could not be parsed") || !strings.Contains(text, `"running_version":"v3.5.31"`) {
		t.Errorf("a component without a version goes to could_not_check and the rest is checked: error=%v %s", isErr, text)
	}
}

// The schema takes an array and a string for every list argument; a call that
// passes either reaches the handler.
// (1.2.0: get_release's kinds and changes, kinds on the briefing tools.)
func TestListArgumentsPassTheSchema(t *testing.T) {
	cs := sdkSession(t, Options{API: upstreamtest.Client(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)), nil)})
	for _, kinds := range []any{[]any{"security", "breaking"}, "security,breaking", `["security"]`} {
		text, isErr := sdkText(t, cs, "check_stack", map[string]any{
			"components": []any{map[string]any{"project": "etcd", "version": "v3.5.30", "same_minor": true}},
			"kinds":      kinds,
		})
		if isErr || !strings.Contains(text, `"target_version":"v3.5.32"`) {
			t.Errorf("check_stack kinds %v: error=%v %.300s", kinds, isErr, text)
		}
		text, isErr = sdkText(t, cs, "check_upgrade", map[string]any{"project": "etcd", "from": "v3.5.30", "kinds": kinds, "same_minor": true})
		if isErr || !strings.Contains(text, `"to":"v3.5.32"`) {
			t.Errorf("check_upgrade kinds %v: error=%v %.300s", kinds, isErr, text)
		}
		text, isErr = sdkText(t, cs, "get_release", map[string]any{"project": "etcd", "version": "v3.6.7", "kinds": kinds})
		if isErr || !strings.Contains(text, `"etcd@v3.6.7#a"`) {
			t.Errorf("get_release kinds %v: error=%v %.300s", kinds, isErr, text)
		}
		text, isErr = sdkText(t, cs, "get_release", map[string]any{"project": "etcd", "version": "v3.6.7", "changes": kinds})
		if isErr || !strings.Contains(text, `"not_found"`) {
			t.Errorf("get_release changes %v: error=%v %.300s", kinds, isErr, text)
		}
	}
}

// same_minor at the top of check_stack is same_minor on every component: the
// schema refused it there before the handler ran, and an agent checking a
// cluster for patches had set it beside kinds (2026-11-22). A component that
// names target_version keeps it and says so. The answers name the server.
// (1.3.0)
func TestCheckStackTakesSameMinorForTheWholeCall(t *testing.T) {
	cs := sdkSession(t, Options{Version: "9.9.9", API: upstreamtest.Client(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)), nil)})
	text, isErr := sdkText(t, cs, "check_stack", map[string]any{
		"same_minor": true,
		"components": []any{
			map[string]any{"project": "etcd", "version": "v3.5.30"},
			map[string]any{"project": "etcd", "version": "v3.5.31", "target_version": "v3.6.7"},
		},
	})
	if isErr {
		t.Fatalf("same_minor at the top was refused: %s", text)
	}
	var out struct {
		Components []struct {
			RunningVersion string `json:"running_version"`
			TargetVersion  string `json:"target_version"`
			Note           string `json:"note"`
		} `json:"components"`
		ServerVersion string `json:"server_version"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil || len(out.Components) != 2 {
		t.Fatalf("answer: %v %.300s", err, text)
	}
	if c := out.Components[0]; c.TargetVersion != "v3.5.32" || strings.Contains(c.Note, "same_minor") {
		t.Errorf("a component without target_version stops at its minor: %+v", c)
	}
	if c := out.Components[1]; c.TargetVersion != "v3.6.7" || !strings.Contains(c.Note, "the call's same_minor was not applied here") {
		t.Errorf("a component with target_version keeps it and says so: %+v", c)
	}
	if out.ServerVersion != "9.9.9" {
		t.Errorf("check_stack server_version %q, want the server's", out.ServerVersion)
	}
	text, isErr = sdkText(t, cs, "check_upgrade", map[string]any{"project": "etcd", "from": "v3.5.30", "same_minor": true})
	if isErr || !strings.Contains(text, `"server_version":"9.9.9"`) {
		t.Errorf("check_upgrade briefing names no server: error=%v %.300s", isErr, text)
	}
	// Without it at the top a component decides for itself, as in 1.2.0.
	text, _ = sdkText(t, cs, "check_stack", map[string]any{
		"components": []any{map[string]any{"project": "etcd", "version": "v3.5.30"}},
	})
	if !strings.Contains(text, `"target_version":"v3.6.8"`) {
		t.Errorf("the default still reaches the newest on the line: %.300s", text)
	}
}
