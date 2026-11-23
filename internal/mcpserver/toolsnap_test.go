package mcpserver

// What tools/list says is the latest release's plus what is declared below,
// and nothing taken away or renamed.
//
// Why: the rule for 1.x (the owner, 2026-11-03) is that a version number moves
// only when an argument or a field is removed or changes name or meaning;
// additions keep it. A client written against the latest release must find
// every argument it sends where it was, with the same type.
// testdata/release-snapshot/ holds tools/list as the latest release's tag
// answers it, in one file named for that release. Only that one is kept: the
// rule is additions only, so holding each release to the one before chains
// back to 1.0.0. How to dump the next one is in testdata/README.md at the
// repository root.
//
// What may differ from the snapshot: the arguments listed in addedSince, the
// arguments listed in retired, the description of a tool named in reworded,
// and the descriptions of arguments. The three lists are emptied when the
// snapshot is replaced.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/mcpserver/mcpservertest"
	"github.com/basphere/ratatosk-mcp/internal/tools"
)

// addedSince are the arguments added after the snapshot's release, by tool;
// "components" names the keys of one check_stack component.
var addedSince = map[string][]string{"check_stack": {"same_minor"}}

// retired are the arguments taken out of the listing after the snapshot's
// release, by tool: a call that sends one is still answered, as the same call
// without it (tools.RetiredArguments; TestRetiredArgumentsAreAcceptedAndIgnored).
// lang left every tool in 1.3.0 — the answers are English alone (the owner,
// 2026-11-23).
var retired = map[string][]string{
	"check_stack": {"lang"}, "check_upgrade": {"lang"}, "get_release": {"lang"}, "list_releases": {"lang"}, "search": {"lang"},
}

// reworded are the tools whose description changed after the snapshot's
// release. A description is what a model reads to choose a tool and its
// arguments — one phrase of check_stack's was read the wrong way by 7 of 8
// agent runs (2026-11-20) — so a change to one is declared here, not slipped
// in.
var reworded = map[string]bool{"check_stack": true, "check_upgrade": true}

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
	cs := mcpservertest.Connect(t, New(Options{}), &mcp.Implementation{Name: "toolsnap-test"})
	res, err := cs.ListTools(context.Background(), nil)
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

// latestSnapshot reads the one file in testdata/release-snapshot.
func latestSnapshot(t *testing.T) (string, []map[string]any) {
	t.Helper()
	files, _ := filepath.Glob("testdata/release-snapshot/tools-list-*.json")
	if len(files) != 1 {
		t.Fatalf("testdata/release-snapshot holds %d snapshots %v, want the latest release's alone", len(files), files)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var frozen []map[string]any
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	return filepath.Base(files[0]), frozen
}

func TestToolsListIsTheLatestReleasePlusWhatIsDeclared(t *testing.T) {
	snap, frozen := latestSnapshot(t)
	current := currentToolsList(t)
	if len(current) != len(frozen) {
		t.Fatalf("%d tools, %s lists %d", len(current), snap, len(frozen))
	}
	for i, tool := range current {
		old := frozen[i]
		name, _ := tool["name"].(string)
		if name != old["name"] {
			t.Errorf("tool %d is %v, %s lists %v", i, name, snap, old["name"])
			continue
		}
		for _, k := range []string{"annotations", "title", "outputSchema", "_meta"} {
			if !reflect.DeepEqual(tool[k], old[k]) {
				t.Errorf("%s %s differs from %s", name, k, snap)
			}
		}
		if !reworded[name] && tool["description"] != old["description"] {
			t.Errorf("%s's description differs from %s: name the tool in reworded", name, snap)
		}
		for k := range tool {
			if _, ok := old[k]; !ok {
				t.Errorf("%s has a field %s does not list: %s", name, snap, k)
			}
		}

		// The input schema with its descriptions set aside and the declared
		// arguments taken out must be the snapshot's, key for key.
		schema := stripDescriptions(tool["inputSchema"]).(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for _, a := range addedSince[name] {
			if _, ok := props[a]; !ok {
				t.Errorf("%s does not list the argument %s that addedSince names", name, a)
			}
			delete(props, a)
		}
		if name == "check_stack" {
			comp := props["components"].(map[string]any)["anyOf"].([]any)[0].(map[string]any)
			cprops := comp["items"].(map[string]any)["properties"].(map[string]any)
			for _, a := range addedSince["components"] {
				if _, ok := cprops[a]; !ok {
					t.Errorf("a check_stack component does not take the key %s that addedSince names", a)
				}
				delete(cprops, a)
			}
		}
		want := stripDescriptions(old["inputSchema"]).(map[string]any)
		oldProps, _ := want["properties"].(map[string]any)
		for _, a := range retired[name] {
			if _, ok := props[a]; ok {
				t.Errorf("%s still lists the argument %s that retired names", name, a)
			}
			if !slices.Contains(tools.RetiredArguments, a) {
				t.Errorf("%s: %s is not in tools.RetiredArguments, so a call that sends it is refused", name, a)
			}
			delete(oldProps, a)
		}
		if !reflect.DeepEqual(schema, want) {
			a, _ := json.Marshal(want)
			c, _ := json.Marshal(schema)
			t.Errorf("%s input schema is not %s's plus the declared arguments:\n  then %s\n  now  %s", name, snap, a, c)
		}
	}
}

// The list arguments take an array and, like components, the forms models
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
