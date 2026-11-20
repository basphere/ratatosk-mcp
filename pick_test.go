package main

// get_release's kinds and changes, kinds on the briefing tools, and the list
// arguments' accepted forms (1.2.0). The SDK checks arguments against the
// schema before a handler runs, so the forms are tested through it too: a
// handler-level test cannot see a schema that refuses an array.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStringListTakesTheFormsModelsSend(t *testing.T) {
	for in, want := range map[string][]string{
		`["security","breaking"]`:       {"security", "breaking"},
		`"[\"security\",\"breaking\"]"`: {"security", "breaking"},
		`"security, breaking"`:          {"security", "breaking"},
		`"security"`:                    {"security"},
		`null`:                          nil,
		`[]`:                            {},
	} {
		var l stringList
		if err := json.Unmarshal([]byte(in), &l); err != nil || !slices.Equal(l, want) {
			t.Errorf("%s → %v %v, want %v", in, l, err, want)
		}
	}
	var l stringList
	if err := json.Unmarshal([]byte(`"[not json"`), &l); err == nil || !strings.Contains(err.Error(), "send an array") {
		t.Errorf("a broken array in a string: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"a":1}`), &l); err == nil {
		t.Error("an object was read as a list")
	}
}

func TestKindsArg(t *testing.T) {
	if k, err := kindsArg(stringList{" Security ", "breaking"}, true); err != nil || !k["security"] || !k["breaking"] || len(k) != 2 {
		t.Errorf("%v %v", k, err)
	}
	if k, err := kindsArg(nil, true); k != nil || err != nil {
		t.Errorf("no kinds is no filter: %v %v", k, err)
	}
	if _, err := kindsArg(stringList{"action"}, false); err == nil || !strings.Contains(err.Error(), "kinds are security, breaking") {
		t.Errorf("an unknown kind must list the kinds: %v", err)
	}
	// A briefing lists the decision kinds only; asked for another, an empty list
	// would read as "none in range".
	if _, err := kindsArg(stringList{"security", "enhancements"}, true); err == nil || !strings.Contains(err.Error(), "detail:\"full\"") {
		t.Errorf("a counted kind in a briefing: %v", err)
	}
	if _, err := kindsArg(stringList{"enhancements"}, false); err != nil {
		t.Errorf("every kind is readable whole: %v", err)
	}
}

// The schema takes an array and a string for every list argument; a call that
// passes either reaches the handler.
func TestListArgumentsPassTheSchema(t *testing.T) {
	swapAPI(t, httptest.NewServer(newFakeEtcd().handler(t)))
	cs := sdkSession(t)
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
		text, isErr = sdkText(t, cs, "get_release", map[string]any{"project": "etcd", "version": "v3.6.7", "kinds": kinds, "changes": kinds})
		if isErr || !strings.Contains(text, `"not_found"`) {
			t.Errorf("get_release kinds %v: error=%v %.300s", kinds, isErr, text)
		}
	}
}

// get_release with kinds keeps those kinds; with changes, the named changes and
// before-upgrade items and the ids it does not have. Folded lines go either
// way; the rest of the document comes as it was, in its order.
func TestGetReleaseReadsPartOfARelease(t *testing.T) {
	f := newFakeEtcd()
	f.projects = `{"projects":[{"slug":"etcd","name":"etcd","latest":{"version":"v3.6.8"}}]}`
	full := f.docs["v3.6.7"]
	f.docs["v3.6.7"] = full[:len(full)-1] + `,"folded":{"dependency_updates_without_security":["- bump x"],"already_in_earlier_release":[]}}`
	swapAPI(t, httptest.NewServer(f.handler(t)))
	get := func(a getReleaseArgs) (map[string]json.RawMessage, string) {
		t.Helper()
		res, _, _ := getReleaseTool(context.Background(), nil, a)
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("get_release %+v: %s", a, text)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			t.Fatal(err)
		}
		return m, text
	}
	ids := func(raw json.RawMessage) []string {
		var cs []struct {
			ID string `json:"id"`
		}
		json.Unmarshal(raw, &cs)
		var out []string
		for _, c := range cs {
			out = append(out, c.ID)
		}
		return out
	}

	whole, text := get(getReleaseArgs{Project: "etcd", Version: "v3.6.7"})
	if _, ok := whole["folded"]; !ok || text != f.docs["v3.6.7"] {
		t.Fatal("without kinds or changes the release passes through as it came")
	}

	m, text := get(getReleaseArgs{Project: "etcd", Version: "v3.6.7", Kinds: stringList{"breaking"}})
	if got := ids(m["changes"]); !slices.Equal(got, []string{"etcd@v3.6.7#k"}) {
		t.Errorf("kinds breaking: %v", got)
	}
	if _, ok := m["folded"]; ok {
		t.Error("folded lines are left out when kinds is given")
	}
	if !strings.Contains(text, `"upgrade_guide":[{"id":"etcd@v3.6.7#g"`) || !strings.HasPrefix(text, `{"project":"etcd","version":"v3.6.7"`) {
		t.Errorf("the rest of the document is passed on, in its order: %.200s", text)
	}

	// No version: the ids name the release.
	m, _ = get(getReleaseArgs{Project: "etcd", Changes: stringList{"etcd@v3.6.7#a", "etcd@v3.6.7#g", "etcd@v3.6.7#zzz", "etcd@v3.6.7#a"}})
	if string(m["version"]) != `"v3.6.7"` {
		t.Errorf("version %s, want the one the ids name", m["version"])
	}
	if got := ids(m["changes"]); !slices.Equal(got, []string{"etcd@v3.6.7#a"}) {
		t.Errorf("changes %v", got)
	}
	var bu map[string]json.RawMessage
	json.Unmarshal(m["before_upgrade"], &bu)
	if got := ids(bu["upgrade_guide"]); !slices.Equal(got, []string{"etcd@v3.6.7#g"}) {
		t.Errorf("a before_upgrade id is answered with its item: %v", got)
	}
	if string(m["not_found"]) != `["etcd@v3.6.7#zzz"]` {
		t.Errorf("not_found %s", m["not_found"])
	}
	// Both: the named changes of those kinds.
	m, _ = get(getReleaseArgs{Project: "etcd", Version: "v3.6.7", Kinds: stringList{"breaking"}, Changes: stringList{"etcd@v3.6.7#a", "etcd@v3.6.7#k"}})
	if got := ids(m["changes"]); !slices.Equal(got, []string{"etcd@v3.6.7#k"}) || string(m["not_found"]) != `[]` {
		t.Errorf("kinds and changes: %v, not_found %s", got, m["not_found"])
	}

	res, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Version: "v3.6.7", Kinds: stringList{"nope"}})
	if !res.IsError {
		t.Error("an unknown kind was answered")
	}
	many := make(stringList, maxPickedChanges+1)
	for i := range many {
		many[i] = "etcd@v3.6.7#" + strings.Repeat("x", i+1)
	}
	if res, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Version: "v3.6.7", Changes: many}); !res.IsError {
		t.Error("more ids than the bound were answered")
	}
}

// kinds narrows a briefing's lists, never its summary; detail full narrows each
// release document.
func TestKindsNarrowABriefing(t *testing.T) {
	swapAPI(t, httptest.NewServer(newFakeEtcd().handler(t)))
	c := runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}, Kinds: stringList{"breaking"}}).Components[0]
	if len(c.Changes) != 1 || c.Changes[0].Kind != "breaking" || c.Summary.Counts["security"] != 1 {
		t.Errorf("kinds breaking: %+v summary %+v", c.Changes, c.Summary)
	}
	res, _, _ := checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}, Detail: "full", Kinds: stringList{"enhancements"}})
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError || !strings.Contains(text, `"etcd@v3.6.7#e"`) || strings.Contains(text, `"etcd@v3.6.7#k"`) {
		t.Errorf("full with kinds enhancements: %.400s", text)
	}
	res, _, _ = checkStackTool(context.Background(), nil, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}, Kinds: stringList{"enhancements"}})
	if !res.IsError {
		t.Error("a briefing asked for a counted kind must say where to read it")
	}
}
