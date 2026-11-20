package main

// check_stack and check_upgrade: what an upgrade from the running version
// brings, as a briefing an agent can decide on.
//
// The privacy property check_stack exists for: what it sends upstream depends
// on project slugs only. For each component it asks for the project's release
// list and then for the project's newest releases (fetchSet) — the same
// requests whether the install runs the oldest version on record or the
// newest. Fetching only the releases above the running version would be
// cheaper, and it would tell the server the running version anyway: the first
// release fetched sits right above it. The range is then cut and compared in
// this process. check_upgrade is the other door and says so: it calls
// /v2/upgrade, which takes from/to as query parameters.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

// ---------------------------------------------------------------------------
// arguments
// ---------------------------------------------------------------------------

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
}

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
			case "project", "name", "version", "target_version", "version_source", "same_minor":
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
}

type checkUpgradeArgs struct {
	Project   string     `json:"project" jsonschema:"project slug, e.g. etcd"`
	From      string     `json:"from" jsonschema:"the version you run now (exclusive), as the project publishes it"`
	To        string     `json:"to,omitempty" jsonschema:"target version (inclusive); omit for the newest release on the same release line (a tag prefix such as flagd/ or lts-, not a minor: from v1.36.5 that reaches v1.37.x — use same_minor to stay on v1.36.x)"`
	Detail    string     `json:"detail,omitempty" jsonschema:"brief (default) or full (every release in range verbatim)"`
	Lang      string     `json:"lang,omitempty" jsonschema:"en (default), ko or ja"`
	Kinds     stringList `json:"kinds,omitempty" jsonschema:"only changes of these kinds: with brief any of security, breaking, deprecations, important_fixes; with full any of the seven kinds (folded lines are then left out). Omit for every kind"`
	SameMinor bool       `json:"same_minor,omitempty" jsonschema:"true: stop at the newest release of from's minor (v1.36.5 → the newest v1.36.x) instead of the newest on its release line; not together with to"`
}

// ---------------------------------------------------------------------------
// bounds
// ---------------------------------------------------------------------------

// maxComponents bounds one check_stack call. Real stacks are tens of items.
const maxComponents = 100

// fetchSetSize is how many of a project's newest releases check_stack reads,
// whatever the running version. Measured on 2026-11-03 over 77 projects: 977
// analyzed releases, about 13 a project; the busiest publish several a week.
// 60 covers months even for those. A running version older than the oldest
// release read is told so in note and its unread releases are named — never a
// silent partial answer.
const fetchSetSize = 60

// fetchBudget bounds the release documents one check_stack call may read
// upstream (cache hits are free). A self-hosted server shares its IP's 1200
// requests a minute; one call must not spend half of it. Components past the
// budget go to could_not_check with a reason, so the caller can split the call.
const fetchBudget = 600

// fetchParallel is how many release documents are in flight at once.
const fetchParallel = 6

// fullReleasesCap bounds detail:"full", where each release is a whole document.
const fullReleasesCap = 15

// fullBytesBudget bounds the release documents one detail:"full" call carries,
// summed over its components. fullReleasesCap alone bounded each component, not
// the call: forty components of fifteen documents made one 32 MB answer that
// held 382 MB while it was built (independent review, 2026-11-11). Measured the
// same day over the 70 newest releases: a document is 15 KB at the median,
// 120 KB at the 90th percentile, 302 KB at most, so 2 MiB is about nine
// components of ordinary releases. What does not fit is counted in
// releases_full_omitted and named in note, never dropped silently.
const fullBytesBudget = 2 << 20

// toolTimeout bounds one tool call. The HTTP server cuts a response at 120 s
// (WriteTimeout), and before this bound a slow upstream ran a stack check into
// that cut: the caller got an empty reply instead of an answer saying what was
// not checked. 90 s leaves room to write the answer. A var so tests can
// shorten it.
var toolTimeout = 90 * time.Second

// deadlineMiddleware puts toolTimeout on every tool call.
func deadlineMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		ctx, cancel := context.WithTimeout(ctx, toolTimeout)
		defer cancel()
		return next(ctx, method, req)
	}
}

// fetchReason words a failed fetch for could_not_check. Out of time is said as
// such, with what to do next, rather than as a transport error.
func fetchReason(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "not checked: this call ran out of time — call check_stack again with the remaining components"
	}
	return "fetch failed: " + err.Error()
}

// ---------------------------------------------------------------------------
// versions
// ---------------------------------------------------------------------------

// sameMinorNote says that same_minor found nothing to upgrade to.
func sameMinorNote(running, newest string) string {
	k := version.NormalizeVersion(running)
	minor := running
	if len(k) >= 2 {
		minor = fmt.Sprintf("%d.%d", k[0], k[1])
	}
	return fmt.Sprintf("same_minor: no release above %s on %s is on record, so the range is empty; the newest on its release line is %s", running, minor, newest)
}

// ---------------------------------------------------------------------------
// notes — the cross-checks this server can make on claims it cannot see
// ---------------------------------------------------------------------------

// coverageNote flags a running version that sits below every release on record
// on its line. It is the one cross-check this server can make on a claim about
// an environment it cannot see, and it catches two different things with the
// same sentence: a genuinely ancient install, where a briefing drawn from the
// covered window is not the whole upgrade path, and a version that was never
// read off a live resource (2026-07-28: an agent holding no version at all
// supplied cilium 1.16.0 against an actual 1.19.5 — every answer after that was
// confidently wrong and nothing downstream could tell).
func coverageNote(running string, runningKey, oldest []int, oldestTag string) string {
	if oldest == nil || version.Compare(runningKey, oldest) >= 0 {
		return ""
	}
	return fmt.Sprintf(
		"running version %s is older than every release on record (earliest on record: %s) — "+
			"this covers the analyzed window only, so treat it as partial, and re-check that the "+
			"running version was read off a live resource",
		running, oldestTag)
}

// newerNote flags a running version above every analyzed release on its line.
// The range (running, newest] is then empty, and an empty range with a target
// below the running version read as "already up to date" with no word said
// (etcd v3.55.0: releases [], target_version v3.7.3; independent review,
// 2026-11-15). Either ratatosk has not analyzed that version yet or the version
// was misread, and in both cases nothing was checked against it, so the
// component goes to could_not_check like a dropped line prefix does — /v2/upgrade
// answers the same case with a 404. A pre-release of a version not on record
// counts as that version (NormalizeVersion drops the suffix), so v3.6.9-rc.1
// above v3.6.8 is flagged and v3.6.8-rc.1 is not.
func newerNote(running string, runningKey, newest []int, newestTag string) string {
	if newest == nil || version.Compare(runningKey, newest) <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"running version %s is newer than every analyzed release on its line (newest analyzed: %s) — "+
			"ratatosk has not analyzed this version yet, or the version was misread. Nothing was checked against it, "+
			"so this is not \"already up to date\": re-check that the version was read off a live resource, and read "+
			"its release notes yourself",
		running, newestTag)
}

// confessionRe matches version_source values that describe an inference
// rather than a live read. Sixty controlled runs (2026-07-29 hub campaign)
// gave this a clean separation: all 8 versions whose source contained such
// vocabulary were hallucinations, all 197 citing a concrete read were correct,
// zero crossover. The model does not forge sources — it confesses. "stated"
// and "standard" stay OFF the list: a user-stated version is a legitimate
// source in the no-cluster-access deployment, and every observed confession
// is caught without them.
var confessionRe = regexp.MustCompile(`(?i)infer|assum|guess|estimat|likely|probab|typical|need verification|not verified|unverified`)

// confessionNote turns a self-confessed inferred source into a warning on the
// component's note — the same channel as the low-version warning.
func confessionNote(src string) string {
	if src == "" || !confessionRe.MatchString(src) {
		return ""
	}
	if len(src) > 80 {
		src = src[:80] + "…"
	}
	return fmt.Sprintf("version_source reads as an inference, not a live read (%q) — treat the "+
		"running version as unverified and re-read it off a live resource; in measurement, every "+
		"self-described inferred version was wrong", src)
}

// resolveTarget parses target_version against the running version. A target
// at or below running asks for the range (running, target], which is empty by
// construction — in measurement, five of twenty agent runs sent exactly
// running-as-target and read the guaranteed zero changes as "no issues". An
// empty-by-construction range is therefore refused loudly (field ignored,
// note says why and teaches the intended use) instead of honored silently.
func resolveTarget(currentKey []int, targetRaw string) ([]int, string) {
	if targetRaw == "" {
		return nil, ""
	}
	targetKey := version.NormalizeVersion(targetRaw)
	if targetKey == nil {
		return nil, "target_version could not be parsed; ignored"
	}
	if version.Compare(targetKey, currentKey) <= 0 {
		return nil, fmt.Sprintf("target_version (%s) is not above the running version, so the checked range "+
			"(running, target] is empty by construction — the field was ignored and the full upgrade path is shown. "+
			"To preview one upgrade hop, keep version = what is running now and set target_version to the destination.", targetRaw)
	}
	return targetKey, ""
}

// lineList renders the available lines for a human: "lines \"core\", \"flagd\"".
func lineList(lines []string) string {
	quoted := make([]string, len(lines))
	for i, l := range lines {
		quoted[i] = strconv.Quote(lineLabel(l))
	}
	if len(quoted) == 1 {
		return "only line " + quoted[0]
	}
	return "lines " + strings.Join(quoted, ", ")
}

// exampleTag returns a real tag from the given line, so the note shows the
// shape the caller should have sent rather than describing it.
func exampleTag(rows []release.Row, line string) string {
	for _, r := range rows {
		if version.Line(r.Version) == line {
			return r.Version
		}
	}
	return ""
}

func lineLabel(line string) string {
	if line == "" {
		return "main (no prefix)"
	}
	return line
}

// ---------------------------------------------------------------------------
// check_stack
// ---------------------------------------------------------------------------

type componentReport struct {
	Project        string `json:"project"`
	RunningVersion string `json:"running_version"`
	// VersionReadAs (1.2.0) is the version checked when it is not the one
	// passed: an image build suffix was dropped (releaseline.StripBuildTail).
	VersionReadAs string `json:"version_read_as,omitempty"`
	VersionSource string `json:"version_source,omitempty"`
	TargetVersion string `json:"target_version,omitempty"`
	Tracked       *bool  `json:"tracked,omitempty"`
	Note          string `json:"note,omitempty"`
	briefing.Briefing
	Gaps            []releaseline.Gap `json:"gaps,omitempty"`
	NotRead         []string          `json:"releases_not_read,omitempty"`
	LaterForRunning []json.RawMessage `json:"later_for_running,omitempty"`
	Full            []json.RawMessage `json:"releases_full,omitempty"` // detail:"full" only
	FullOmitted     int               `json:"releases_full_omitted,omitempty"`
}

// untrackedReason answers a component whose project is neither a slug nor an
// image alias. It leads with checking the slug, because a name in another form
// is the likelier cause; the warning that matters if it really is untracked
// stays: no coverage is not safety.
const untrackedReason = "unknown project name — not a slug ratatosk knows: check the slug with list_projects " +
	"(the project may be listed under another slug); if it is not listed, ratatosk does NOT track it, so no " +
	"changes here means no coverage, not safety"

type uncheckable struct {
	Project string `json:"project"`
	Version string `json:"version"`
	Reason  string `json:"reason"`
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

// fetchSet is the releases check_stack reads for a project: its newest
// fetchSetSize by release date, across every line. It is a function of the
// project alone — that is the privacy property, so nothing about the running
// version, its line or the target may enter here.
func fetchSet(rows []release.Row) []release.Row {
	if len(rows) > fetchSetSize {
		return rows[:fetchSetSize]
	}
	return rows
}

// fetchDocs reads release documents through the cache, a few at a time. A 404
// is "not on record" (the release list and the documents come from different
// moments) and is skipped; anything else is an error.
func fetchDocs(ctx context.Context, rows []release.Row, lang string) (map[string]*release.Doc, error) {
	out := make(map[string]*release.Doc, len(rows))
	var mu sync.Mutex
	var firstErr error
	// One failed document fails the component, so the rest are not worth
	// fetching: cancel them instead of reading up to 60 more.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, fetchParallel)
	var wg sync.WaitGroup
	for _, r := range rows {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(r release.Row) {
			defer wg.Done()
			defer func() { <-sem }()
			b, err := api.releaseRaw(ctx, r.Project, r.Version, lang, true)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if he, ok := err.(*httpError); ok && he.Status == 404 {
					return
				}
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			d, perr := release.Parse(b)
			if perr != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("/v2/releases/{project}/{version}: %w", perr)
					cancel()
				}
				return
			}
			out[r.Version] = d
		}(r)
	}
	wg.Wait()
	if firstErr == nil && ctx.Err() != nil {
		firstErr = ctx.Err() // the caller's deadline, not our own cancel
	}
	return out, firstErr
}

func checkStackTool(ctx context.Context, req *mcp.CallToolRequest, args checkStackArgs) (*mcp.CallToolResult, any, error) {
	ctx = logging.RequestContext(ctx, "check_stack", req)
	if len(args.Components) == 0 {
		return errResult(fmt.Errorf("components is required — %s", componentsShapeHint)), nil, nil
	}
	if len(args.Components) > maxComponents {
		return errResult(fmt.Errorf("too many components: %d (max %d)", len(args.Components), maxComponents)), nil, nil
	}
	// A component without a project is refused, never scanned. Upstream reads a
	// missing project filter as "every project", so one such component used to
	// page the whole corpus: a model that sent project_slug got 2,435 changes
	// back in a 676,771-character answer and timed out re-reading it (hub,
	// 2026-09-28).
	for i, comp := range args.Components {
		if strings.TrimSpace(comp.Project) != "" {
			continue
		}
		sent := comp.sentKeys
		if sent == "" {
			sent = "none"
		}
		return errResult(fmt.Errorf("component %d has no project (keys received: %s) — %s", i+1, sent, componentsShapeHint)), nil, nil
	}
	full, err := detailArg(args.Detail)
	if err != nil {
		return errResult(err), nil, nil
	}
	lang, err := langArg(args.Lang)
	if err != nil {
		return errResult(err), nil, nil
	}
	kindSet, err := kindsArg(args.Kinds, !full)
	if err != nil {
		return errResult(err), nil, nil
	}
	// Two range ends at once: whichever one won, the caller would read the
	// range as the other one's.
	for i, comp := range args.Components {
		if comp.SameMinor && strings.TrimSpace(comp.TargetVersion) != "" {
			return errResult(fmt.Errorf("component %d (%s) sets both same_minor and target_version — pass one: target_version for a given destination, same_minor for the newest patch of the running minor", i+1, truncate(comp.Project, 80))), nil, nil
		}
	}

	roster, err := api.projects(ctx)
	if err != nil {
		return errResult(fmt.Errorf("could not read the project list: %w", err)), nil, nil
	}
	slugs := map[string]bool{}
	aliasOf := map[string]string{}
	for _, p := range roster {
		slugs[p.Slug] = true
		for _, a := range p.ImageAliases {
			aliasOf[strings.ToLower(a)] = p.Slug
		}
	}

	// The same component listed twice gets one report. Each copy used to be
	// checked and answered again: a hundred copies of one project made one call
	// carry forty identical reports (independent review, 2026-11-11).
	times := map[stackComponent]int{}
	comps := make([]stackComponent, 0, len(args.Components))
	for _, comp := range args.Components {
		comp.Project = slugArg(comp.Project)
		comp.sentKeys = ""
		if times[comp]++; times[comp] == 1 {
			comps = append(comps, comp)
		}
	}

	reports := make([]componentReport, 0, len(comps))
	var cannot []uncheckable
	spent := 0
	fullBytes := 0
	for _, comp := range comps {
		rep := componentReport{Project: comp.Project, RunningVersion: comp.Version, VersionSource: comp.VersionSource}
		var notes []string
		if n := times[comp]; n > 1 {
			notes = append(notes, fmt.Sprintf("listed %d times in this call; checked once", n))
		}
		if comp.unreadKeys != "" {
			notes = append(notes, fmt.Sprintf("key(s) not read: %s — a component takes project, version, target_version, same_minor and version_source", comp.unreadKeys))
		}
		skip := func(reason string) {
			notes = append(notes, reason)
			rep.Note = strings.Join(notes, "; ")
			reports = append(reports, rep)
			cannot = append(cannot, uncheckable{Project: rep.Project, Version: comp.Version, Reason: reason})
		}
		slug := comp.Project
		if !slugs[slug] {
			if s, ok := aliasOf[strings.ToLower(slug)]; ok {
				notes = append(notes, fmt.Sprintf("%q is an image alias of project %q; checked as %q", slug, s, s))
				slug = s
				rep.Project = s
			} else {
				f := false
				rep.Tracked = &f
				skip(untrackedReason)
				continue
			}
		}
		if version.NormalizeVersion(comp.Version) == nil {
			skip("running version could not be parsed")
			continue
		}
		rows, err := api.projectReleases(ctx, slug)
		if err != nil {
			skip(fetchReason(ctx, err))
			continue
		}
		if len(rows) == 0 {
			t := true
			rep.Tracked = &t
			rep.Releases = &[]briefing.RangeRelease{}
			notes = append(notes, "tracked by ratatosk; no analyzed release on record yet")
			rep.Note = strings.Join(notes, "; ")
			reports = append(reports, rep)
			continue
		}
		// An image tag can carry a build suffix after the version it was built
		// from (releaseline.ReadBuildTail): dropped when the tag as passed is no release on
		// record and the version without it is, and the answer says what was
		// read.
		running := comp.Version
		if v, ok := releaseline.ReadBuildTail(rows, running); ok {
			running = v
			rep.VersionReadAs = v
		}
		onLine := releaseline.RowsOnLine(rows, running)
		// A version whose line does not exist here is almost always a dropped
		// prefix, not a real gap. Agents read versions off image tags, and an
		// image tag rarely carries the release-line prefix: knative publishes
		// "knative-v1.12.0" while its images say "v1.12.0" (hub question,
		// 2026-08-10). Silence would look like "nothing to do".
		if len(onLine) == 0 {
			lines := releaseline.LinesPresent(rows)
			line := version.Line(running)
			if version.NormalizeVersion(line) != nil {
				// What was read as the line is itself a version: something was
				// added after the version, not dropped before it.
				skip(fmt.Sprintf(
					"no releases on record for line %s — this project publishes %s. The version reads as %s followed by %q, which is not a release line here: pass the version as the project publishes it, without what the image tag adds after it (e.g. %s)",
					lineLabel(line), lineList(lines), line, truncate(strings.TrimPrefix(strings.TrimSpace(running), line), 60), exampleTag(rows, lines[0])))
				continue
			}
			skip(fmt.Sprintf(
				"no releases on record for line %s — this project publishes %s. If the version came from an image tag, the release-line prefix was probably dropped: pass the tag as the project publishes it (e.g. %s)",
				lineLabel(line), lineList(lines), exampleTag(rows, lines[0])))
			continue
		}
		currentKey := version.NormalizeVersion(running)
		targetKey, targetNote := resolveTarget(currentKey, comp.TargetVersion)
		if targetNote != "" {
			notes = append(notes, targetNote)
		}
		set := fetchSet(rows)
		if spent+len(set) > fetchBudget {
			skip("not checked: this call already read as many releases as one call may — call check_stack again with the remaining components")
			continue
		}
		spent += len(set)
		docs, err := fetchDocs(ctx, set, lang)
		if err != nil {
			skip(fetchReason(ctx, err))
			continue
		}

		target := ""
		if targetKey != nil {
			target = comp.TargetVersion
		}
		var inRange []release.Row
		var resolved string
		if comp.SameMinor {
			if t := releaseline.SameMinorTarget(rows, running); t != "" {
				target = t
				inRange, resolved = releaseline.UpgradeRange(rows, running, t)
			} else {
				resolved = releaseline.TagOf(onLine, running)
				notes = append(notes, sameMinorNote(running, releaseline.NewestOnLine(rows, running)))
			}
		} else {
			inRange, resolved = releaseline.UpgradeRange(rows, running, target)
		}
		rep.TargetVersion = resolved

		var oldest, newest []int
		oldestTag, newestTag := "", ""
		for _, r := range onLine {
			k := version.NormalizeVersion(r.Version)
			if oldest == nil || version.Compare(k, oldest) < 0 {
				oldest, oldestTag = k, r.Version
			}
			if newest == nil || version.Compare(k, newest) > 0 {
				newest, newestTag = k, r.Version
			}
		}
		if n := coverageNote(running, currentKey, oldest, oldestTag); n != "" {
			notes = append(notes, n)
		}
		if n := confessionNote(comp.VersionSource); n != "" {
			notes = append(notes, n)
		}
		// Decided here, after the documents were read, and not before the fetch
		// with the other refusals: skipping the fetch would make the upstream
		// requests depend on the running version, which is what check_stack
		// exists to prevent (see fetchSet).
		if n := newerNote(running, currentKey, newest, newestTag); n != "" {
			rep.TargetVersion = "" // the range end resolved above sits below the running version
			skip(n)
			continue
		}
		// A target above every analyzed release: the range stops at the newest
		// analyzed one and the target itself was never read. Without a word that
		// came back as releases [] and read as "nothing between here and there"
		// (etcd v3.7.3 with target_version v3.7.4; independent review, 2026-11-15)
		// — /v2/upgrade answers the same to= with a 404.
		if targetKey != nil && newest != nil && version.Compare(targetKey, newest) > 0 {
			notes = append(notes, fmt.Sprintf(
				"target_version %s is newer than every analyzed release on its line (newest analyzed: %s) — the checked range stops at %s; "+
					"%s itself and anything after %s was not checked, so read their release notes yourself",
				comp.TargetVersion, newestTag, newestTag, comp.TargetVersion, newestTag))
		}

		// What the running version's own branch already carries, at or below it.
		alreadyFixed := map[string]bool{}
		for _, d := range docs {
			k := version.NormalizeVersion(d.Version)
			if k == nil || !version.SameLine(d.Version, running) || !releaseline.SameBranch(k, currentKey) || version.Compare(k, currentKey) > 0 {
				continue
			}
			for _, c := range d.Changes {
				if c.Kind != "security" {
					continue
				}
				for _, id := range c.Advisories {
					alreadyFixed[strings.ToUpper(strings.TrimSpace(id))] = true
				}
			}
		}

		var rangeDocs []*release.Doc
		for _, r := range inRange {
			if d, ok := docs[r.Version]; ok {
				rangeDocs = append(rangeDocs, d)
			} else {
				rep.NotRead = append(rep.NotRead, r.Version)
			}
		}
		if len(rep.NotRead) > 0 {
			notes = append(notes, fmt.Sprintf("%d release(s) in range were not read (only the newest %d releases of a project are read per call, or the release was not available): see releases_not_read and check them with get_release", len(rep.NotRead), fetchSetSize))
		}

		points := []string{running}
		for _, r := range inRange {
			points = append(points, r.Version)
		}
		if target != "" && (len(inRange) == 0 || !releaseline.SameTag(inRange[len(inRange)-1].Version, target)) {
			points = append(points, target)
		}
		rep.Gaps = releaseline.Gaps(points)

		for _, d := range docs {
			if releaseline.SameTag(d.Version, running) {
				rep.LaterForRunning = d.Later
				break
			}
		}

		if full {
			rels := []briefing.RangeRelease{}
			for i, d := range rangeDocs {
				if i == fullReleasesCap {
					rep.FullOmitted = len(rangeDocs) - fullReleasesCap
					break
				}
				raw := json.RawMessage(d.Raw)
				if kindSet != nil {
					b, err := filterRelease(d.Raw, &docFilter{kinds: kindSet})
					if err != nil {
						return errResult(fmt.Errorf("/v2/releases/{project}/{version}: %w", err)), nil, nil
					}
					raw = b
				}
				// The first document of the call always goes out, whatever its size.
				if fullBytes > 0 && fullBytes+len(raw) > fullBytesBudget {
					rep.FullOmitted = len(rangeDocs) - i
					notes = append(notes, fmt.Sprintf("releases_full stops here: one call carries about %d MB of release documents — read the other %d with get_release, or call again with fewer components", fullBytesBudget>>20, rep.FullOmitted))
					break
				}
				fullBytes += len(raw)
				rep.Full = append(rep.Full, raw)
			}
			for _, d := range rangeDocs {
				rels = append(rels, briefing.RangeRelease{Version: d.Version, ReleasedAt: d.ReleasedAt, URL: d.URL, Counts: d.Counts})
			}
			rep.Releases = &rels
		} else {
			var settled int
			rep.Briefing, settled = briefing.Build(rangeDocs, alreadyFixed, currentKey)
			rep.Briefing.Narrow(kindSet)
			if settled > 0 {
				notes = append(notes, fmt.Sprintf(
					"%d security change(s) in range were excluded: their advisories are already fixed at or below %s on your branch",
					settled, running))
			}
		}
		rep.Note = strings.Join(notes, "; ")
		reports = append(reports, rep)
	}

	out := map[string]any{
		"components": reports,
		"privacy":    "versions were compared in this process; upstream requests named project slugs only (each project's recent releases, the same whatever version runs)",
	}
	if len(cannot) > 0 {
		out["could_not_check"] = cannot
	}
	if full {
		res, err := jsonResult(out)
		return res, nil, err
	}
	out["hint"] = briefing.Hint(kindSet)
	res, err := jsonResult(out)
	return res, nil, err
}

// ---------------------------------------------------------------------------
// check_upgrade
// ---------------------------------------------------------------------------

func checkUpgradeTool(ctx context.Context, req *mcp.CallToolRequest, args checkUpgradeArgs) (*mcp.CallToolResult, any, error) {
	ctx = logging.RequestContext(ctx, "check_upgrade", req)
	args.Project = slugArg(args.Project)
	if args.Project == "" || args.From == "" {
		return errResult(fmt.Errorf("project and from are required (from = the version you run now)")), nil, nil
	}
	full, err := detailArg(args.Detail)
	if err != nil {
		return errResult(err), nil, nil
	}
	lang, err := langArg(args.Lang)
	if err != nil {
		return errResult(err), nil, nil
	}
	kindSet, err := kindsArg(args.Kinds, !full)
	if err != nil {
		return errResult(err), nil, nil
	}
	// "." and ".." are refused before any request, as upgradeRaw refuses them
	// (pathSegment): the release list below would otherwise ask for them first.
	if _, err := pathSegment("project", args.Project); err != nil {
		return errResult(err), nil, nil
	}
	to := args.To
	if args.SameMinor && strings.TrimSpace(to) != "" {
		return errResult(fmt.Errorf("same_minor and to are both set — pass one: to for a given destination, same_minor for the newest patch of from's minor")), nil, nil
	}
	// The project's public release list, read through the cache: it decides
	// whether from carries an image build suffix, the range end of same_minor,
	// and which releases settle what from's branch already fixed. from is sent
	// to /v2/upgrade below in any case. detail:"full" settles nothing, so it
	// reads the list only for a build suffix or same_minor (1.1.0 made one
	// request; independent review, 2026-11-20). A list that cannot be read
	// fails the call only when same_minor needs it; otherwise the answer comes
	// as 1.1.0 gave it, and note says what could not be done.
	var rows []release.Row
	var listErr error
	_, tailLike := releaseline.StripBuildTail(args.From)
	if !full || args.SameMinor || tailLike {
		rows, listErr = api.projectReleases(ctx, args.Project)
	}
	if listErr != nil && args.SameMinor {
		return errResult(fmt.Errorf("could not read the release list to find the newest release of from's minor (same_minor): %w", listErr)), nil, nil
	}
	var notes []string
	// The build suffix of an image tag (releaseline.StripBuildTail), as check_stack reads
	// it: /v2/upgrade answered cilium-envoy's v1.37.6-1789133542-<commit> with
	// a 404 (2026-11-20).
	from, readAs := args.From, ""
	if v, ok := releaseline.ReadBuildTail(rows, from); ok {
		from, readAs = v, v
	} else if tailLike && listErr != nil {
		notes = append(notes, fmt.Sprintf("could not read the release list, so a build suffix on from was not read off: %v", listErr))
	}
	// When from's line has no release at all, to stays unset and /v2/upgrade
	// answers with its own error, which lists the tags the project publishes.
	if args.SameMinor && len(releaseline.RowsOnLine(rows, from)) > 0 {
		if t := releaseline.SameMinorTarget(rows, from); t != "" {
			to = t
		} else {
			to = from // (from, from] is empty: /v2/upgrade answers it with no releases
			notes = append(notes, sameMinorNote(from, releaseline.NewestOnLine(rows, from)))
		}
	}
	raw, err := api.upgradeRaw(ctx, args.Project, from, to, lang)
	if err != nil {
		return errResult(err), nil, nil
	}
	if full {
		if kindSet == nil && readAs == "" && len(notes) == 0 {
			return rawResult(raw), nil, nil
		}
		var f *docFilter
		if kindSet != nil {
			f = &docFilter{kinds: kindSet}
		}
		b, err := filterUpgradeReleases(raw, f, readAs, joinNotes(notes...))
		if err != nil {
			return errResult(fmt.Errorf("/v2/upgrade: %w", err)), nil, nil
		}
		return rawResult(b), nil, nil
	}
	var up struct {
		Project      string             `json:"project"`
		From         string             `json:"from"`
		To           string             `json:"to"`
		Releases     []json.RawMessage  `json:"releases"`
		Gaps         *[]releaseline.Gap `json:"gaps"`
		LaterForFrom []json.RawMessage  `json:"later_for_from"`
	}
	if err := json.Unmarshal(raw, &up); err != nil {
		return errResult(fmt.Errorf("/v2/upgrade: %w", err)), nil, nil
	}
	// A JSON object that is not an upgrade answer must not become an empty
	// briefing: /v2/upgrade/.. used to return the API catalog, and it was
	// answered with from "" and no releases — nothing to do (2026-11-15).
	if up.Project == "" || up.Releases == nil {
		return errResult(fmt.Errorf("/v2/upgrade answered without a project and a release range — not an upgrade answer; check the arguments")), nil, nil
	}
	var docs []*release.Doc
	for _, r := range up.Releases {
		d, err := release.Parse(r)
		if err != nil {
			return errResult(fmt.Errorf("/v2/upgrade: %w", err)), nil, nil
		}
		docs = append(docs, d)
	}
	// The server computes gaps; an older server did not send them, so the same
	// rule runs here rather than answering without them.
	gaps := []releaseline.Gap{}
	if up.Gaps != nil {
		gaps = *up.Gaps
	} else {
		points := []string{up.From}
		for _, d := range docs {
			points = append(points, d.Version)
		}
		if to != "" && (len(docs) == 0 || !releaseline.SameTag(docs[len(docs)-1].Version, to)) {
			points = append(points, to)
		}
		gaps = releaseline.Gaps(points)
	}
	// What from's own branch already fixed, as check_stack reads it. Until
	// 1.2.0 this path read only the range, settled nothing, and answered the
	// same upgrade differently from check_stack: containerd v2.2.9 → v2.4.2
	// omitted 42 security changes here and 30 there, with 13 excluded
	// (2026-11-20). The releases at or below from are public documents, read
	// through the same cache; from itself already went upstream. If they cannot
	// be read, nothing is settled, as in 1.1.0, and note says so.
	fromKey := version.NormalizeVersion(up.From)
	fixed, err := map[string]bool(nil), listErr
	if err == nil {
		fixed, err = fixedOnBranch(ctx, rows, up.From, lang)
	}
	if err != nil {
		fixed = nil
		notes = append(notes, fmt.Sprintf("could not read the releases at or below %s, so security changes your branch already fixed are not left out (as in 1.1.0): %v", truncate(up.From, 80), err))
	}
	b, _ := briefing.Build(docs, fixed, fromKey)
	b.Narrow(kindSet)
	if up.LaterForFrom == nil {
		up.LaterForFrom = []json.RawMessage{}
	}
	out := map[string]any{
		"project": up.Project,
		"from":    args.From, // as passed; version_read_as says what was checked
		"to":      up.To,
		"summary": b.Summary,
		"gaps":    gaps,
		"privacy": "from and to were sent to the server (/v2/upgrade); use check_stack to keep versions on your side",
		"hint":    briefing.Hint(kindSet),
	}
	if readAs != "" {
		out["version_read_as"] = readAs
	}
	if note := joinNotes(notes...); note != "" {
		out["note"] = note
	}
	// Arrays, never null, as the docs say: an empty range came back as
	// "changes": null and "before_upgrade": null (independent review,
	// 2026-11-15), which a client reading the documented shape may not expect.
	changes := b.Changes
	if changes == nil {
		changes = []briefing.Change{}
	}
	out["releases"] = b.Releases
	out["before_upgrade"] = b.Upgrade
	out["changes"] = changes
	out["later_for_from"] = up.LaterForFrom
	out["later_in_range"] = b.LaterInRange
	if len(b.ChangesOmitted) > 0 {
		out["changes_omitted"] = b.ChangesOmitted
	}
	if len(b.ChangesMore) > 0 {
		out["changes_more"] = b.ChangesMore
	}
	if b.ExcludedOnBranch != nil {
		out["excluded_on_branch"] = b.ExcludedOnBranch
	}
	res, err := jsonResult(out)
	return res, nil, err
}

// joinNotes joins the non-empty notes with "; ".
func joinNotes(parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), "; ")
}

// fixedOnBranch reads the advisory ids from's own branch carries at or below
// from — the alreadyFixed set check_stack builds from its fetch set. The rows
// come newest first, so the newest fetchSetSize releases of the branch are
// read, the same bound check_stack's fetch set puts on what it reads.
func fixedOnBranch(ctx context.Context, rows []release.Row, from, lang string) (map[string]bool, error) {
	fromKey := version.NormalizeVersion(from)
	if len(fromKey) < 2 {
		return nil, nil // no branch to read: nothing is settled, as before 1.2.0
	}
	var branch []release.Row
	for _, r := range rows {
		k := version.NormalizeVersion(r.Version)
		if k == nil || !version.SameLine(r.Version, from) || !releaseline.SameBranch(k, fromKey) || version.Compare(k, fromKey) > 0 {
			continue
		}
		branch = append(branch, r)
		if len(branch) == fetchSetSize {
			break
		}
	}
	docs, err := fetchDocs(ctx, branch, lang)
	if err != nil {
		return nil, err
	}
	fixed := map[string]bool{}
	for _, d := range docs {
		for _, c := range d.Changes {
			if c.Kind != "security" {
				continue
			}
			for _, id := range c.Advisories {
				fixed[strings.ToUpper(strings.TrimSpace(id))] = true
			}
		}
	}
	return fixed, nil
}

// filterUpgradeReleases applies f to every release of a /v2/upgrade answer and
// passes the rest of the answer on as received; readAs, when set, is added as
// version_read_as.
func filterUpgradeReleases(raw []byte, f *docFilter, readAs, note string) ([]byte, error) {
	fields, err := readObject(raw)
	if err != nil {
		return nil, err
	}
	if readAs != "" {
		v, _ := json.Marshal(readAs)
		fields = append(fields, objField{Key: "version_read_as", Val: v})
	}
	if note != "" {
		v, _ := json.Marshal(note)
		fields = append(fields, objField{Key: "note", Val: v})
	}
	for i, fl := range fields {
		if fl.Key != "releases" || f == nil {
			continue
		}
		var rels []json.RawMessage
		if err := json.Unmarshal(fl.Val, &rels); err != nil {
			return nil, fmt.Errorf("releases: %w", err)
		}
		for j, r := range rels {
			b, err := filterRelease(r, f)
			if err != nil {
				return nil, err
			}
			rels[j] = b
		}
		fields[i].Val = writeArray(rels)
	}
	return writeObject(fields), nil
}
