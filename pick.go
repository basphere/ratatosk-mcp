package main

// Reading only part of a release (1.2.0): kinds and changes.
//
// Why: one release is too large to read whole. cilium v1.20.0 is 758,000
// characters (1,094 changes, 676 of them "other"), and 32 agent runs on
// 2026-11-20 all had at least one get_release answer dropped to a file by
// their client (84 answers) — Claude Code hands a tool answer over 50,000
// characters to the model as a file, not as text. The briefing, in turn,
// carries no evidence. kinds keeps the kinds that ask for a decision ("between
// the briefing and the whole release"), and changes fetches the few entries a
// briefing named, with body and evidence. Both are arguments on the tools that
// already exist (owner's decision, 2026-11-20 — no new tool).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// stringList is an array argument that also takes the forms models send in
// its place: the array JSON-encoded inside a string, or a comma-separated
// string. check_stack's components were refused 8 times out of 8 when a model
// sent the array as a string, and each refusal cost that model a turn
// (stackComponents); a list of kind names or ids is no different. The schema
// says array (listArgSchema), so the array stays the documented form.
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	t := bytes.TrimSpace(b)
	if bytes.Equal(t, []byte("null")) {
		*l = nil
		return nil
	}
	if len(t) > 0 && t[0] == '"' {
		var s string
		if err := json.Unmarshal(t, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "[") {
			var out []string
			if err := json.Unmarshal([]byte(s), &out); err != nil {
				return fmt.Errorf("a string holding %s is not a JSON array of strings — send an array, e.g. [\"security\",\"breaking\"]", truncate(s, 80))
			}
			*l = out
			return nil
		}
		var out []string
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		*l = out
		return nil
	}
	var out []string
	if err := json.Unmarshal(t, &out); err != nil {
		return fmt.Errorf("send an array of strings, e.g. [\"security\",\"breaking\"]: %w", err)
	}
	*l = out
	return nil
}

// toolSchema is the schema the SDK would infer for T, with every list argument
// named in lists widened to also take a string (see stringList). Without the
// widening the SDK refuses a string before the handler runs, and the refusal is
// the expensive part.
func toolSchema[T any](lists ...string) *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Errorf("schema: %w", err)) // wiring bug, not runtime input
	}
	for _, name := range lists {
		arr := s.Properties[name]
		if arr == nil {
			panic(fmt.Errorf("schema: no property %s", name))
		}
		s.Properties[name] = &jsonschema.Schema{
			Description: arr.Description,
			AnyOf: []*jsonschema.Schema{
				arr,
				{Type: "string", Description: "the same list as a string (JSON array or comma-separated); accepted, but send the array"},
			},
		}
	}
	return s
}

// kindsArg reads the kinds argument. An unknown name is refused with the names
// that exist: read as "no filter" it would answer every kind, and read as
// "nothing" it would answer none — both look like an answer. A briefing lists
// only the kinds that ask for a decision, so in a briefing the other three are
// refused too, with where they can be read: accepted, they would come back as
// an empty list, which reads as "none in range".
func kindsArg(list stringList, briefing bool) (map[string]bool, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := map[string]bool{}
	var counted []string
	for _, k := range list {
		k = strings.ToLower(strings.TrimSpace(k))
		if !containsStr(kinds, k) {
			return nil, fmt.Errorf("kinds: unknown kind %q — kinds are %s", truncate(k, 40), strings.Join(kinds, ", "))
		}
		if briefing && !containsStr(decisionKinds, k) {
			counted = append(counted, k)
		}
		out[k] = true
	}
	if len(counted) > 0 {
		return nil, fmt.Errorf("kinds: a briefing lists %s and counts %s in summary.counts only — "+
			"for those, pass detail:\"full\" or read the release with get_release(kinds)",
			strings.Join(decisionKinds, ", "), strings.Join(counted, ", "))
	}
	return out, nil
}

// maxPickedChanges bounds get_release's changes argument. A briefing's
// changes_more line names one change, and fifty of them are already more than a
// decision needs from one release; past that, kinds is the way to read a kind.
const maxPickedChanges = 50

// changesArg reads get_release's changes argument: trimmed, empty entries
// dropped, repeats kept once.
func changesArg(list stringList) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, id := range list {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > maxPickedChanges {
		return nil, fmt.Errorf("changes: %d ids, at most %d per call — split them, or read a whole kind with kinds", len(out), maxPickedChanges)
	}
	return out, nil
}

// releaseOfIDs reads the release an id names: "cilium@v1.20.0#dd9d9e39" names
// cilium v1.20.0. ok is false unless every id names the same release.
func releaseOfIDs(ids []string) (project, version string, ok bool) {
	for i, id := range ids {
		head, _, found := strings.Cut(id, "#")
		if !found {
			return "", "", false
		}
		p, v, found := strings.Cut(head, "@")
		if !found || p == "" || v == "" {
			return "", "", false
		}
		if i > 0 && (p != project || v != version) {
			return "", "", false
		}
		project, version = p, v
	}
	return project, version, len(ids) > 0
}
