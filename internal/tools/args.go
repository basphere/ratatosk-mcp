package tools

// The argument forms several tools share: a list that also arrives as a
// string, the schema that admits both, kinds and detail.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/release"
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
func kindsArg(list stringList, forBriefing bool) (map[string]bool, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := map[string]bool{}
	var counted []string
	for _, k := range list {
		k = strings.ToLower(strings.TrimSpace(k))
		if !containsStr(release.Kinds, k) {
			return nil, fmt.Errorf("kinds: unknown kind %q — kinds are %s", truncate(k, 40), strings.Join(release.Kinds, ", "))
		}
		if forBriefing && !containsStr(briefing.DecisionKinds, k) {
			counted = append(counted, k)
		}
		out[k] = true
	}
	if len(counted) > 0 {
		return nil, fmt.Errorf("kinds: a briefing lists %s and counts %s in summary.counts only — "+
			"for those, pass detail:\"full\" or read the release with get_release(kinds)",
			strings.Join(briefing.DecisionKinds, ", "), strings.Join(counted, ", "))
	}
	return out, nil
}

func detailArg(d string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(d)) {
	case "", "brief":
		return false, nil
	case "full":
		return true, nil
	}
	return false, fmt.Errorf(`detail must be "brief" or "full"`)
}
