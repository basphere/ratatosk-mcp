package mcpserver

// What the input schemas let through to a handler: the forms models send are
// accepted, and a refusal comes from the handler, with the shape, not from the
// schema.

import (
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
