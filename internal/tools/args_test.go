package tools

// The list arguments' accepted forms and kinds (1.2.0). That the schema lets
// both forms through to a handler is tested through the SDK
// (internal/mcpserver).

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
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
