package mcpserver

// What tools/list says in 1.2.0 is 1.1.0's plus the new arguments, and
// nothing taken away or renamed.
//
// Why: the rule for 1.x (the owner, 2026-11-03) is that a version number moves
// only when an argument or a field is removed or changes name or meaning;
// additions keep it. A client written against 1.1.0 must find every argument
// it sends where it was, with the same type.
// testdata/release-snapshot/tools-list-1.1.0.json is tools/list as 2613481
// (the 1.1.0 tag) answers it, dumped through an in-memory client from that
// commit's newServer; 1.1.0 was held to 1.0.0's snapshot the same way. Only
// the latest release's snapshot is kept: the rule is additions only, so
// holding each release to the one before chains back to 1.0.0. How to dump the
// next one is in testdata/README.md at the repository root.
//
// What may differ: descriptions — 1.2.0 rewrites "the latest on the same
// release line" (read as "the same minor" by 7 of 8 agent runs, 2026-11-20)
// and the drill-down sentences — on the tools the release changes, and the
// arguments listed in added12. list_projects and list_releases stay word for
// word.

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// added12 are the arguments 1.2.0 adds, by tool; "components" names the keys
// of one check_stack component.
var added12 = map[string][]string{
	"check_stack":   {"kinds"},
	"components":    {"same_minor"},
	"check_upgrade": {"kinds", "same_minor"},
	"get_release":   {"changes", "kinds"},
	"search":        {"project"},
}

// stripDescriptions drops every "description" key, so two schemas compare by
// shape: names, types, required and the rest.
func stripDescriptions(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			if k != "description" {
				out[k] = stripDescriptions(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = stripDescriptions(val)
		}
		return out
	}
	return v
}

func currentToolsList(t *testing.T) []map[string]any {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := New(Options{}).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "tools110-test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Tools)
	var current []map[string]any
	if err := json.Unmarshal(b, &current); err != nil {
		t.Fatal(err)
	}
	return current
}

func TestToolsListIs110PlusTheNewArguments(t *testing.T) {
	raw, err := os.ReadFile("testdata/release-snapshot/tools-list-1.1.0.json")
	if err != nil {
		t.Fatal(err)
	}
	var frozen []map[string]any
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	current := currentToolsList(t)
	if len(current) != len(frozen) {
		t.Fatalf("%d tools, 1.1.0 listed %d", len(current), len(frozen))
	}
	for i, tool := range current {
		old := frozen[i]
		name, _ := tool["name"].(string)
		if name != old["name"] {
			t.Errorf("tool %d is %v, 1.1.0 listed %v", i, name, old["name"])
			continue
		}
		for _, k := range []string{"annotations", "title", "outputSchema", "_meta"} {
			if !reflect.DeepEqual(tool[k], old[k]) {
				t.Errorf("%s %s differs from 1.1.0", name, k)
			}
		}
		if (name == "list_projects" || name == "list_releases") && tool["description"] != old["description"] {
			t.Errorf("%s's description changed; 1.2.0 does not touch it", name)
		}
		for k := range tool {
			if _, ok := old[k]; !ok {
				t.Errorf("%s has a field 1.1.0 did not list: %s", name, k)
			}
		}

		// The input schema with its descriptions set aside and the 1.2.0
		// arguments taken out must be 1.1.0's, key for key.
		schema := stripDescriptions(tool["inputSchema"]).(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for _, a := range added12[name] {
			if _, ok := props[a]; !ok {
				t.Errorf("%s does not list the 1.2.0 argument %s", name, a)
			}
			delete(props, a)
		}
		if name == "check_stack" {
			comp := props["components"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
			cprops := comp["items"].(map[string]any)["properties"].(map[string]any)
			for _, a := range added12["components"] {
				if _, ok := cprops[a]; !ok {
					t.Errorf("a check_stack component does not take the 1.2.0 key %s", a)
				}
				delete(cprops, a)
			}
		}
		if want := stripDescriptions(old["inputSchema"]); !reflect.DeepEqual(schema, want) {
			a, _ := json.Marshal(want)
			c, _ := json.Marshal(schema)
			t.Errorf("%s input schema is not 1.1.0's plus the new arguments:\n  1.1.0 %s\n  now   %s", name, a, c)
		}
	}
}

// The new list arguments take an array and, like components, the forms models
// send in its place; the array form stays first, so it is what a model reads as
// the way to call.
func TestListArgumentsAcceptArrayAndString(t *testing.T) {
	for _, tool := range currentToolsList(t) {
		name := tool["name"].(string)
		props, _ := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		for _, a := range []string{"kinds", "changes"} {
			p, ok := props[a].(map[string]any)
			if !ok {
				continue
			}
			forms, _ := p["anyOf"].([]any)
			if len(forms) != 2 {
				t.Errorf("%s %s: %v, want an array form and a string form", name, a, p)
				continue
			}
			types, _ := forms[0].(map[string]any)["type"].([]any)
			if !slices.Contains(types, any("array")) || forms[1].(map[string]any)["type"] != "string" {
				t.Errorf("%s %s forms %v", name, a, forms)
			}
		}
	}
}
