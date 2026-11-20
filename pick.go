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
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

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

// releaseOfID reads the release an id names: "cilium@v1.20.0#dd9d9e39" names
// cilium v1.20.0.
func releaseOfID(id string) (project, version string, ok bool) {
	head, _, found := strings.Cut(id, "#")
	if !found {
		return "", "", false
	}
	p, v, found := strings.Cut(head, "@")
	if !found || p == "" || v == "" {
		return "", "", false
	}
	return p, v, true
}

// maxPickedReleases bounds the releases one get_release call reads for the
// ids it is given without a version (spec "마"): a release document is up to
// 870 KB (cilium v1.20.0), and ten is more than a briefing's lines name.
const maxPickedReleases = 10

// pickedRelease is one release of get_release's answer to changes without a
// version: the named changes and, when an id names one, before-upgrade items.
type pickedRelease struct {
	Version         string          `json:"version"`
	URL             json.RawMessage `json:"url,omitempty"`
	ReleaseNotesURL json.RawMessage `json:"release_notes_url,omitempty"`
	Changes         json.RawMessage `json:"changes"`
	BeforeUpgrade   json.RawMessage `json:"before_upgrade,omitempty"`
}

// pickedReleases answers get_release(changes) without a version: every
// release the ids name, in the order the ids first name it, each cut to the
// named entries. Ids of another project, ids that are not ids, and ids a
// release does not have (or a release that is not on record) are listed in
// not_found.
func pickedReleases(ctx context.Context, project string, ids []string, lang string) (*mcp.CallToolResult, any, error) {
	var order []string
	byRelease := map[string][]string{}
	notFound := []string{}
	for _, id := range ids {
		p, v, ok := releaseOfID(id)
		if !ok || slugArg(p) != project {
			notFound = append(notFound, truncate(id, 120))
			continue
		}
		if _, seen := byRelease[v]; !seen {
			order = append(order, v)
		}
		byRelease[v] = append(byRelease[v], id)
	}
	if len(order) > maxPickedReleases {
		return errResult(fmt.Errorf("changes name %d releases; at most %d per call without version — split the ids", len(order), maxPickedReleases)), nil, nil
	}
	releases := []pickedRelease{}
	for _, v := range order {
		raw, err := api.releaseRaw(ctx, project, v, lang, false)
		if he, ok := err.(*httpError); ok && he.Status == 404 {
			for _, id := range byRelease[v] {
				notFound = append(notFound, truncate(id, 120))
			}
			continue
		}
		if err != nil {
			return errResult(err), nil, nil
		}
		if !isJSONObject(raw) {
			return rawResult(raw), nil, nil // the same refusal as one release
		}
		f := &docFilter{ids: map[string]bool{}, found: map[string]bool{}, foundUpgrade: map[string]bool{}}
		for _, id := range byRelease[v] {
			f.ids[id] = true
		}
		b, err := filterRelease(raw, f)
		if err != nil {
			return errResult(fmt.Errorf("/v2/releases/{project}/{version}: %w", err)), nil, nil
		}
		fields, err := readObject(b)
		if err != nil {
			return errResult(err), nil, nil
		}
		pr := pickedRelease{Version: v, Changes: json.RawMessage("[]")}
		var bu json.RawMessage
		for _, fl := range fields {
			switch fl.Key {
			case "version":
				_ = json.Unmarshal(fl.Val, &pr.Version)
			case "url":
				pr.URL = fl.Val
			case "release_notes_url":
				pr.ReleaseNotesURL = fl.Val
			case "changes":
				pr.Changes = fl.Val
			case "before_upgrade":
				bu = fl.Val
			}
		}
		for _, id := range byRelease[v] {
			if !f.found[id] {
				notFound = append(notFound, truncate(id, 120))
			}
		}
		if len(f.foundUpgrade) > 0 {
			pr.BeforeUpgrade = bu
		}
		releases = append(releases, pr)
	}
	out := struct {
		Project  string          `json:"project"`
		Releases []pickedRelease `json:"releases"`
		NotFound []string        `json:"not_found"`
	}{project, releases, notFound}
	res, err := jsonResult(out)
	return res, nil, err
}
