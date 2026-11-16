package main

// Tests for the defects an independent review found on 2026-11-11. Each one
// replays the reported failure; several go through the SDK on purpose, because
// the SDK checks arguments against the schema and applies middleware before a
// handler runs, and handler-level tests could not see either.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// sdkSession connects an in-memory client to the real server, so a call passes
// schema validation and every middleware exactly as it does on the wire.
func sdkSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := newServer().Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "review-test", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func sdkText(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return err.Error(), true
	}
	if len(res.Content) == 0 {
		t.Fatal("empty tool result")
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

// The array form used to be refused by the schema before the handler could
// read "name" or name the keys it received.
func TestArrayFormReachesTheHandlerThroughTheSDK(t *testing.T) {
	swapAPI(t, httptest.NewServer(newFakeEtcd().handler(t)))
	cs := sdkSession(t)

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

// Running v3.5.30 already carries GHSA-bbbb. A later patch on the same branch
// citing it again is a follow-up fix and must be listed; the same advisory on
// another branch is still a backport and stays excluded.
func TestBriefingKeepsALaterFixOnTheRunningBranch(t *testing.T) {
	f := newFakeEtcd()
	f.docs["v3.5.32"] = doc("v3.5.32", "", "",
		`[{"id":"etcd@v3.5.32#b","kind":"security","title":"complete fix for GHSA-bbbb","body":null,"severity":"critical","advisories":["GHSA-bbbb"],`+
			`"affects":{"text":"gRPC listener","status":"stated"},"also_required":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}]`, "")
	swapAPI(t, httptest.NewServer(f.handler(t)))
	c := runStack(t, checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}}).Components[0]

	var ids []string
	for _, ch := range c.Changes {
		ids = append(ids, ch.ID)
	}
	joined := strings.Join(ids, " ")
	if !strings.Contains(joined, "etcd@v3.5.32#b") {
		t.Errorf("the follow-up fix on the running branch was dropped: %v", ids)
	}
	if strings.Contains(joined, "etcd@v3.6.8#b") {
		t.Errorf("the backport on another branch must stay excluded: %v", ids)
	}
	if !strings.Contains(c.Note, "1 security change(s) in range were excluded") {
		t.Errorf("note %q must count only the other branch's backport", c.Note)
	}
}

func TestCheckStackChecksADuplicateComponentOnce(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	same := stackComponent{Project: "etcd", Version: "v3.5.30"}
	out := runStack(t, checkStackArgs{Components: []stackComponent{same, {Project: " etcd ", Version: "v3.5.30"}, same, {Project: "etcd", Version: "v3.5.31"}}})
	if len(out.Components) != 2 {
		t.Fatalf("four components, two distinct: got %d reports", len(out.Components))
	}
	if !strings.Contains(out.Components[0].Note, "listed 3 times in this call; checked once") {
		t.Errorf("the kept report must say it stood for three: %q", out.Components[0].Note)
	}
	if strings.Contains(out.Components[1].Note, "listed") {
		t.Errorf("a component sent once carries no such note: %q", out.Components[1].Note)
	}
}

// detail:"full" is bounded per call, not only per component, and the cut is
// counted and explained.
func TestFullDetailStopsAtTheByteBudget(t *testing.T) {
	f := newFakeEtcd()
	pad := strings.Repeat("x", 900<<10)
	big := func(ver string) string {
		return doc(ver, "", "", `[{"id":"etcd@`+ver+`#e","kind":"enhancements","title":"t","body":"`+pad+`","affects":{"text":"","status":"not_stated"},"to_use":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}]`, "")
	}
	for _, v := range []string{"v3.5.31", "v3.5.32", "v3.6.7", "v3.6.8"} {
		f.docs[v] = big(v)
	}
	swapAPI(t, httptest.NewServer(f.handler(t)))
	res, _, err := checkStackTool(context.Background(), nil, checkStackArgs{Detail: "full", Components: []stackComponent{
		{Project: "etcd", Version: "v3.5.30"}, {Project: "etcd", Version: "v3.5.31"},
	}})
	if err != nil || res.IsError {
		t.Fatalf("tool error: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var out struct {
		Components []struct {
			Note        string            `json:"note"`
			Full        []json.RawMessage `json:"releases_full"`
			FullOmitted int               `json:"releases_full_omitted"`
			Releases    []struct {
				Version string `json:"version"`
			} `json:"releases"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	first, second := out.Components[0], out.Components[1]
	// Four 900 KB documents in range; two fit under 2 MiB.
	if len(first.Full) != 2 || first.FullOmitted != 2 || len(first.Releases) != 4 {
		t.Errorf("first component: %d documents, %d omitted, %d releases listed; want 2, 2, 4", len(first.Full), first.FullOmitted, len(first.Releases))
	}
	if !strings.Contains(first.Note, "releases_full stops here") || !strings.Contains(first.Note, "get_release") {
		t.Errorf("the cut must be explained: %q", first.Note)
	}
	// The budget is the call's: the next component gets none, and says so.
	if len(second.Full) != 0 || second.FullOmitted != 3 || len(second.Releases) != 3 {
		t.Errorf("second component: %d documents, %d omitted, %d releases listed; want 0, 3, 3", len(second.Full), second.FullOmitted, len(second.Releases))
	}
	if len(text) > fullBytesBudget+(1<<20) {
		t.Errorf("answer is %d bytes, over the budget plus one document", len(text))
	}
}

// A slow upstream used to run a call into the HTTP server's write timeout and
// the caller got an empty reply. Now the call ends on its own deadline with an
// answer that says what was not checked.
func TestToolCallEndsOnItsDeadlineWithAnAnswer(t *testing.T) {
	f := newFakeEtcd()
	inner := f.handler(t)
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/releases/etcd/") {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		inner(w, r)
	})))
	orig := toolTimeout
	toolTimeout = 200 * time.Millisecond
	t.Cleanup(func() { toolTimeout = orig })

	cs := sdkSession(t)
	start := time.Now()
	text, isErr := sdkText(t, cs, "check_stack", map[string]any{
		"components": []any{map[string]any{"project": "etcd", "version": "v3.5.30"}},
	})
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the call ran %s past a 200ms deadline", took)
	}
	if isErr || !strings.Contains(text, `"could_not_check"`) || !strings.Contains(text, "ran out of time") {
		t.Errorf("want an answer naming the component as not checked for lack of time, got error=%v %s", isErr, text)
	}
}

// One failed document fails the component; the others must not be waited for.
func TestFetchDocsStopsAfterTheFirstError(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v3.5.31") {
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})))
	start := time.Now()
	_, err := fetchDocs(context.Background(), f.rows, "")
	if err == nil {
		t.Fatal("a 500 on one document must fail the fetch")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("waited %s for documents that no longer mattered", took)
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("the error reported must be the upstream failure, not our own cancel: %v", err)
	}
}

// A deadline that has already passed must come back as an error. Without it
// the fetch returned an empty set and no error, and the component would have
// been answered as "nothing in range".
func TestFetchDocsReportsTheCallersDeadline(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	docs, err := fetchDocs(ctx, f.rows, "")
	if err == nil {
		t.Fatalf("an expired deadline returned %d documents and no error", len(docs))
	}
	if r := fetchReason(ctx, err); !strings.Contains(r, "ran out of time") {
		t.Errorf("reason %q must say the call ran out of time", r)
	}
}

// Inside the range: the first release of another branch carrying an advisory is
// the backport and folds into also_in; a later release of a branch that already
// carried it is a follow-up and is listed with its own severity.
func TestBriefingListsAFollowUpFixInsideTheRange(t *testing.T) {
	sec := func(ver, grade string) string {
		g := "null"
		if grade != "" {
			g = `"` + grade + `"`
		}
		return doc(ver, "", "", `[{"id":"etcd@`+ver+`#a","kind":"security","title":"fix","body":null,"severity":`+g+`,"advisories":["GHSA-1111"],`+
			`"affects":{"text":"","status":"not_stated"},"also_required":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}]`, "")
	}
	var docs []*v2Release
	for _, d := range []string{sec("v1.2.1", ""), sec("v1.2.2", "critical"), sec("v1.3.0", ""), sec("v1.3.1", "high")} {
		r, err := parseRelease([]byte(d))
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, r)
	}
	b, settled := buildBriefing(docs, nil, []int{1, 2, 0})
	got := map[string][]string{}
	for _, c := range b.Changes {
		got[c.ID] = c.AlsoIn
	}
	if settled != 0 || len(got) != 3 {
		t.Fatalf("want three entries (first fix, its follow-up, the other branch's follow-up), got %v settled %d", got, settled)
	}
	if also := got["etcd@v1.2.1#a"]; len(also) != 1 || also[0] != "v1.3.0" {
		t.Errorf("the other branch's first release folds into the first fix: also_in %v", also)
	}
	for _, id := range []string{"etcd@v1.2.2#a", "etcd@v1.3.1#a"} {
		if _, ok := got[id]; !ok {
			t.Errorf("follow-up %s must be listed on its own: %v", id, got)
		}
	}
	// The critical follow-up leads: security is ordered by grade.
	if b.Changes[0].ID != "etcd@v1.2.2#a" {
		t.Errorf("the critical follow-up must come first, got %s", b.Changes[0].ID)
	}
}

// A key the server does not read is tolerated, and said: "target" meant as
// target_version must not return the whole path as if the bound had applied.
// A component without a version is answered per component, not by the schema.
func TestUnreadKeysAndMissingVersionAreAnswered(t *testing.T) {
	swapAPI(t, httptest.NewServer(newFakeEtcd().handler(t)))
	cs := sdkSession(t)

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

// In stateful mode the client's event stream is a GET held open for the whole
// session. It does no work and must not take a slot from the session's calls.
func TestInflightCapCountsOnlyPosts(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	h := inflightMiddleware(1, 50*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			started <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}))
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp", nil))
		close(done)
	}()
	<-started
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}")))
	if rec.Code != http.StatusOK {
		t.Errorf("a POST while the stream is open: got %d, want 200", rec.Code)
	}
	close(release)
	<-done
}

// Two changes of ONE release citing the same advisories are one entry, on any
// branch: they are not a fix and its follow-up. A backport's higher grade is
// carried onto the entry it folds into.
func TestBriefingTreatsSiblingsInOneReleaseAsOne(t *testing.T) {
	ch := func(ver, id, grade string) string {
		g := "null"
		if grade != "" {
			g = `"` + grade + `"`
		}
		return `{"id":"etcd@` + ver + `#` + id + `","kind":"security","title":"fix","body":null,"severity":` + g + `,"advisories":["GHSA-2222"],` +
			`"affects":{"text":"","status":"not_stated"},"also_required":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}`
	}
	var docs []*v2Release
	for _, d := range []string{
		doc("v1.2.1", "", "", `[`+ch("v1.2.1", "a", "")+`,`+ch("v1.2.1", "b", "")+`]`, ""),
		doc("v1.3.0", "", "", `[`+ch("v1.3.0", "a", "critical")+`,`+ch("v1.3.0", "b", "critical")+`]`, ""),
	} {
		r, err := parseRelease([]byte(d))
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, r)
	}
	b, _ := buildBriefing(docs, nil, []int{1, 2, 0})
	if len(b.Changes) != 1 {
		t.Fatalf("four changes, one advisory, two releases on two branches: want one entry, got %d", len(b.Changes))
	}
	c := b.Changes[0]
	if c.ID != "etcd@v1.2.1#a" || len(c.AlsoIn) != 1 || c.AlsoIn[0] != "v1.3.0" {
		t.Errorf("entry %s also_in %v, want the first change with the other branch named once", c.ID, c.AlsoIn)
	}
	if string(c.Severity) != `"critical"` {
		t.Errorf("the backport's grade must be carried onto the entry: %s", c.Severity)
	}
}

// Keys are matched without regard to case, as the decode does, and what is
// echoed back is bounded.
func TestUnreadKeysMatchTheDecodeAndAreBounded(t *testing.T) {
	var c stackComponent
	if err := json.Unmarshal([]byte(`{"Project":"etcd","Version":"v3.5.30","Target_Version":"v3.5.31"}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.Project != "etcd" || c.TargetVersion != "v3.5.31" || c.unreadKeys != "" {
		t.Errorf("keys that were read must not be reported unread: %+v", c)
	}
	junk := `{"project":"etcd","version":"v1"`
	for i := 0; i < 30; i++ {
		junk += `,"k` + strings.Repeat("x", 100) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":1`
	}
	junk += `}`
	if err := json.Unmarshal([]byte(junk), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.unreadKeys) > 8*45+20 || !strings.Contains(c.unreadKeys, "(+22 more)") {
		t.Errorf("thirty long keys must come back as eight short ones and a count: %d chars %q", len(c.unreadKeys), c.unreadKeys)
	}
}

// A sibling that asks for something of its own is not folded away: it is the
// envoy v1.35.13 case, where the second change on one advisory carried the
// required step.
func TestBriefingKeepsASiblingThatCarriesItsOwnStep(t *testing.T) {
	plain := `{"id":"etcd@v1.2.1#a","kind":"security","title":"padding oracle fixed","body":null,"severity":"high","advisories":["GHSA-3333"],` +
		`"affects":{"text":"","status":"not_stated"},"also_required":null,"security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}`
	withStep := `{"id":"etcd@v1.2.1#b","kind":"security","title":"opt-in GCM encryption","body":null,"severity":"high","advisories":["GHSA-3333"],` +
		`"affects":{"text":"","status":"not_stated"},"also_required":"set use_gcm_encryption to true","security_unclear":false,"silent_failure":false,"workaround":null,"regression_since":null,"evidence":[],"later":[]}`
	same := strings.Replace(plain, "#a", "#c", 1)
	r, err := parseRelease([]byte(doc("v1.2.1", "", "", `[`+plain+`,`+withStep+`,`+same+`]`, "")))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := buildBriefing([]*v2Release{r}, nil, []int{1, 2, 0})
	var ids []string
	for _, c := range b.Changes {
		ids = append(ids, c.ID)
	}
	// The one with its own step leads its kind (carriesAStep, 2026-11-15).
	if len(ids) != 2 || ids[0] != "etcd@v1.2.1#b" || ids[1] != "etcd@v1.2.1#a" {
		t.Fatalf("want the one with its own step and the first change, not the plain repeat: %v", ids)
	}
	if b.Changes[0].AlsoRequired != "set use_gcm_encryption to true" {
		t.Errorf("the step must reach the answer: %+v", b.Changes[0])
	}
}

// The keys of a component that named no project are echoed in the refusal;
// that echo is bounded too.
func TestSentKeysAreBounded(t *testing.T) {
	var c stackComponent
	if err := json.Unmarshal([]byte(`{"`+strings.Repeat("k", 300000)+`":1}`), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.sentKeys) > 200 {
		t.Errorf("a 300,000-character key came back as %d characters", len(c.sentKeys))
	}
}
