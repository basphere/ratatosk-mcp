package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// maxComponents bounds one check_stack call. Real stacks are tens of items.
const maxComponents = 100

type stackComponent struct {
	Project       string `json:"project" jsonschema:"project slug, e.g. envoy"`
	Version       string `json:"version" jsonschema:"the version currently running, exactly as the project publishes it — keep any release-line prefix (flagd/v0.16.1, lts-4081.3.9, api/v1.11.1), since only that line is compared. e.g. v1.36.8"`
	TargetVersion string `json:"target_version,omitempty" jsonschema:"optional upgrade destination, strictly above the running version: only releases with running < version <= target are returned; a target at or below running would make that range empty and is ignored with a note"`
	// VersionSource is an audit trail, never a check: this server cannot see the
	// caller's environment and therefore cannot tell a real citation from an
	// invented one. What it buys is a machine-readable claim to compare against
	// later, and a slot the caller has to fill by actually looking something up.
	VersionSource string `json:"version_source,omitempty" jsonschema:"where the running version was read, e.g. daemonset/cilium image tag or a user-provided value; echoed back so the claim can be audited"`
	// SameMinor (1.2.0): the default target is the newest release on the
	// running version's release line, and a line is a tag prefix, not a minor —
	// kubernetes v1.36.5 went to v1.37.2. 7 of 8 agent runs read "the latest on
	// the same release line" as "the same minor" (2026-11-20), and operators
	// usually look at the newest patch of their minor first.
	SameMinor bool `json:"same_minor,omitempty" jsonschema:"true: stop at the newest release of the running version's minor (v1.36.5 → the newest v1.36.x) instead of the newest on its release line; not together with target_version"`
	// sentKeys lists the keys a component arrived with, recorded only when no
	// project could be read from them. Unknown keys are otherwise dropped, so
	// without this a refusal could only echo an empty value — the model needs
	// to see that its project_slug went unread. A string, not a slice, so the
	// struct stays comparable.
	sentKeys string
	// unreadKeys lists the keys that are none of the above. They are tolerated
	// (see checkStackSchema) but never silently: a "target" meant as
	// target_version would otherwise return the whole upgrade path with nothing
	// to say the bound was ignored. Named in the component's note.
	unreadKeys string
	// sameMinorSaid: the component carried a same_minor key, true or false.
	// Only an explicit false is told apart by it, when the call sets
	// same_minor for every component.
	sameMinorSaid bool
	// callSameMinor says how the call's same_minor meets what this component
	// says for itself; "" when the call set none or the component just takes it.
	callSameMinor string
}

const (
	// The component set same_minor:false: it keeps its own range.
	callSameMinorOff = "off"
	// The component named a target_version: used if it can be, and the
	// call's same_minor if it cannot.
	callSameMinorOrTarget = "or-target"
)

// checkStackSchema is the inferred schema with one widening: components also
// accepts the JSON-encoded-string form. Without this the SDK refuses that shape
// before the handler ever runs, and the refusal is the expensive part
// (see stackComponents). The array form stays first and keeps its full
// description, so it remains what a model reads as "the way to call this".
func checkStackSchema() *jsonschema.Schema {
	s, err := jsonschema.For[checkStackArgs](nil)
	if err != nil {
		panic(fmt.Errorf("check_stack schema: %w", err)) // wiring bug, not runtime input
	}
	arrayForm := s.Properties["components"]
	// The handler reads "name" as an alias for project and names the keys it
	// received when no project could be read (see stackComponent.UnmarshalJSON).
	// The inferred item schema forbade unknown keys and required project, so in
	// the array form the SDK refused both before the handler ran and the caller
	// got a bare schema error instead of the shape (reproduced 2026-11-11). The
	// handler still refuses a component without a project.
	// version is not required either: the handler answers a missing one per
	// component ("running version could not be parsed") and checks the rest.
	if items := arrayForm.Items; items != nil {
		items.AdditionalProperties = nil
		items.Required = nil
	}
	s.Properties["components"] = &jsonschema.Schema{
		Description: arrayForm.Description,
		AnyOf: []*jsonschema.Schema{
			arrayForm,
			{Type: "string", Description: "the same array JSON-encoded as a string; accepted, but send the array"},
		},
	}
	// kinds takes the string forms too (stringList, toolSchema).
	kindsForm := s.Properties["kinds"]
	s.Properties["kinds"] = &jsonschema.Schema{
		Description: kindsForm.Description,
		AnyOf: []*jsonschema.Schema{
			kindsForm,
			{Type: "string", Description: "the same list as a string (JSON array or comma-separated); accepted, but send the array"},
		},
	}
	return s
}

// stackComponents accepts what models actually send, not only what the schema
// asks for. A self-hosted model was measured sending the whole array as a JSON
// *string* (`"[{\"name\": …}]"`) and was refused 8 times out of 8 — and a schema
// refusal is not free on that side: the model burns a whole turn regenerating
// arguments, about two minutes each, so eight microsecond-cheap refusals cost
// that session 16 productive minutes (hub measurement, 2026-09-04). The wire
// form is unambiguous either way, so we parse it and get on with the work.
type stackComponents []stackComponent

func (s *stackComponents) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if strings.HasPrefix(trimmed, `"`) {
		// The array arrived JSON-encoded inside a string. Unwrap once, then
		// read it as the array it was meant to be.
		var inner string
		if err := json.Unmarshal(b, &inner); err != nil {
			return fmt.Errorf("components: %w — %s", err, componentsShapeHint)
		}
		var out []stackComponent
		if err := json.Unmarshal([]byte(inner), &out); err != nil {
			return fmt.Errorf("components arrived as a string but its contents are not a JSON array: %w — %s", err, componentsShapeHint)
		}
		*s = out
		return nil
	}
	var out []stackComponent
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("components: %w — %s", err, componentsShapeHint)
	}
	*s = out
	return nil
}

// boundedKeys joins key names for an answer. Caller-chosen text goes back to
// the caller, so it is bounded: a model that passes a whole manifest object as
// a component must not get it echoed (one 1 MiB key name made a 1 MB answer,
// and a 300,000-character key a 300 KB refusal; reviews of 2026-11-11).
func boundedKeys(keys []string) string {
	const maxKeys, maxKeyChars = 8, 40
	more := 0
	if len(keys) > maxKeys {
		more, keys = len(keys)-maxKeys, keys[:maxKeys]
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		if r := []rune(k); len(r) > maxKeyChars {
			k = string(r[:maxKeyChars]) + "…"
		}
		out[i] = k
	}
	s := strings.Join(out, ", ")
	if more > 0 {
		s += fmt.Sprintf(" (+%d more)", more)
	}
	return s
}

// componentsShapeHint is carried in every rejection this type can produce.
// An error that only echoes what was received leaves the model guessing: the
// measured run varied the same mistake eight times. Showing the shape once is
// what turns a refusal into one corrected retry.
const componentsShapeHint = `expected components like [{"project":"cilium","version":"v1.19.5"}] ` +
	`(project = the slug from list_projects; version = exactly as the project publishes it)`

// UnmarshalJSON accepts "name" as an alias for "project". Models reach for
// "name" (measured), and refusing that costs a turn to learn nothing: the
// value they put there is the slug we would have asked for. The alias is
// deliberately absent from the schema and the docs — list_projects remains the
// way to resolve a slug, and advertising a second spelling would blur that.
func (c *stackComponent) UnmarshalJSON(b []byte) error {
	type raw stackComponent // no methods: plain decode
	var v struct {
		raw
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("component: %w — %s", err, componentsShapeHint)
	}
	*c = stackComponent(v.raw)
	if c.Project == "" {
		c.Project = v.Name
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(b, &keys) == nil {
		names := make([]string, 0, len(keys))
		var unread []string
		for k := range keys {
			names = append(names, k)
			// Lower-cased: the decode above matches field names without regard
			// to case, so "Project" was read and must not be reported unread.
			switch strings.ToLower(k) {
			case "same_minor":
				c.sameMinorSaid = true
			case "project", "name", "version", "target_version", "version_source":
			default:
				unread = append(unread, k)
			}
		}
		sort.Strings(names)
		sort.Strings(unread)
		if c.Project == "" {
			c.sentKeys = boundedKeys(names)
		}
		c.unreadKeys = boundedKeys(unread)
	}
	return nil
}

type checkStackArgs struct {
	Components stackComponents `json:"components" jsonschema:"the running stack to check"`
	Detail     string          `json:"detail,omitempty" jsonschema:"brief (default): summary, before_upgrade and the changes that need a decision; full: every release in range verbatim"`
	Lang       string          `json:"lang,omitempty" jsonschema:"en (default), ko or ja — language of title, body and attribute text"`
	Kinds      stringList      `json:"kinds,omitempty" jsonschema:"only changes of these kinds, for every component: with brief any of security, breaking, deprecations, important_fixes; with full any of the seven kinds (folded lines are then left out). Omit for every kind"`
	// SameMinor (1.3.0): checking a whole cluster for patches is one call
	// with same_minor on every component, and the description listed
	// same_minor beside kinds, which is set here. An agent set it here and was
	// refused by the schema before the handler ran (2026-11-22).
	SameMinor bool `json:"same_minor,omitempty" jsonschema:"true: same_minor for every component — each stops at the newest release of its running minor (v1.36.5 → the newest v1.36.x); what a component says for itself wins, and its note says so: same_minor:false keeps its default range, and a target_version above the running version is used"`
}
