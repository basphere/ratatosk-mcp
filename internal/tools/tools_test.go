package tools

// Handler-level tests of the tools, with the fake /v2 of
// internal/upstream/upstreamtest. Calls through the SDK — schema validation,
// descriptions as a client lists them, the server's middleware — are tested in
// internal/mcpserver. Here: the helpers the tool tests share, and what every
// tool does alike.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/briefing"
	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// withAPI points the tool handlers at ts, which is closed when the test ends.
func withAPI(t *testing.T, ts *httptest.Server) *handlers {
	t.Helper()
	return &handlers{api: upstreamtest.Client(t, ts, nil), version: "test"}
}

type stackOut struct {
	Components []struct {
		Project        string `json:"project"`
		RunningVersion string `json:"running_version"`
		TargetVersion  string `json:"target_version"`
		Tracked        *bool  `json:"tracked"`
		Note           string `json:"note"`
		Summary        struct {
			Releases int            `json:"releases"`
			Counts   map[string]int `json:"counts"`
		} `json:"summary"`
		Releases []struct {
			Version string `json:"version"`
		} `json:"releases"`
		BeforeUpgrade []briefing.UpgradeItem `json:"before_upgrade"`
		Changes       []struct {
			Version     string          `json:"version"`
			ID          string          `json:"id"`
			Kind        string          `json:"kind"`
			Severity    json.RawMessage `json:"severity"`
			AlsoIn      []string        `json:"also_in"`
			NoDowngrade *release.Attr   `json:"no_downgrade"`
		} `json:"changes"`
		Gaps            []releaseline.Gap `json:"gaps"`
		LaterForRunning []json.RawMessage `json:"later_for_running"`
	} `json:"components"`
	CouldNotCheck []uncheckable `json:"could_not_check"`
	Privacy       string        `json:"privacy"`
}

func runStack(t *testing.T, h *handlers, args checkStackArgs) stackOut {
	t.Helper()
	res, _, err := h.checkStackTool(context.Background(), nil, args)
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	var out stackOut
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, text)
	}
	return out
}

// changeDoc is a release document with n changes of each kind given, titles
// long enough to make the answer large.
func changeDoc(project, ver string, n map[string]int, titleLen int) string {
	var cs []string
	title := strings.Repeat("t", titleLen)
	for _, kind := range release.Kinds {
		for i := 0; i < n[kind]; i++ {
			c := fmt.Sprintf(`{"id":"%s@%s#%s%d","kind":%q,"title":"%s %s %d","body":"b","severity":null,"advisories":["CVE-2026-%d"],`+
				`"affects":{"text":"every install","status":"not_stated"},"evidence":[{"quote":"q","source":"release_notes","url":"u"}],"later":[]}`,
				project, ver, kind, i, kind, kind, title, i, 10000+i)
			if kind != "security" {
				c = strings.Replace(c, `"severity":null,"advisories":["CVE-2026-`+fmt.Sprint(10000+i)+`"],`, ``, 1)
			}
			cs = append(cs, c)
		}
	}
	return fmt.Sprintf(`{"project":%q,"version":%q,"released_at":"2026-07-01T00:00:00Z","url":"u","counts":{},"warnings":{"critical":0,"no_downgrade":0},`+
		`"before_upgrade":{"prerequisites":[],"known_issues":[],"upgrade_guide":[]},"changes":[%s],"later":[]}`, project, ver, strings.Join(cs, ","))
}

func upgradeServer(t *testing.T, docs ...string) *handlers {
	t.Helper()
	return withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/releases":
			w.Write([]byte(`{"releases":[],"next_before":null}`))
		case strings.HasPrefix(r.URL.Path, "/v2/upgrade/"):
			fmt.Fprintf(w, `{"project":"p","from":"v0.9.0","to":"v1.0.0","releases":[%s],"gaps":[],"later_for_from":[]}`, strings.Join(docs, ","))
		default:
			t.Errorf("unexpected upstream request %s", r.URL.RequestURI())
		}
	})))
}

type upgradeOut struct {
	Changes        []briefing.Change   `json:"changes"`
	ChangesOmitted map[string]int      `json:"changes_omitted"`
	ChangesMore    []briefing.MoreLine `json:"changes_more"`
	Hint           string              `json:"hint"`
}

func runUpgrade(t *testing.T, h *handlers, args checkUpgradeArgs) (upgradeOut, string) {
	t.Helper()
	res, _, err := h.checkUpgradeTool(context.Background(), nil, args)
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	var out upgradeOut
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out, text
}

// Every slug in /v2/projects is lower case. Matched as sent, "Kubernetes" was
// answered as untracked by check_stack and as an empty list by list_releases.
// (Independent review before the 1.0 launch, 2026-11-15.)
func TestSlugsAreMatchedWithoutCaseOrSurroundingSpace(t *testing.T) {
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))

	out := runStack(t, h, checkStackArgs{Components: []stackComponent{{Project: " Etcd ", Version: "v3.6.7"}, {Project: "ETCD", Version: "v3.6.7"}}})
	if len(out.CouldNotCheck) != 0 || len(out.Components) != 1 {
		t.Fatalf("Etcd and ETCD are etcd, listed twice: %+v", out)
	}
	if c := out.Components[0]; c.Project != "etcd" || len(c.Releases) != 1 || !strings.Contains(c.Note, "listed 2 times") {
		t.Errorf("component %+v", c)
	}

	if res, _, _ := h.getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "ETCD ", Version: "v3.6.7"}); res.IsError {
		t.Errorf("get_release ETCD: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	if res, _, _ := h.listReleasesTool(context.Background(), nil, listReleasesArgs{Project: " Etcd"}); res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, `"version":"v3.6.8"`) {
		t.Errorf("list_releases Etcd: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	reqs := strings.Join(f.Requests(), " ")
	for _, want := range []string{"/v2/releases/etcd/v3.6.7", "/v2/releases?limit=10&project=etcd"} {
		if !strings.Contains(reqs, want) {
			t.Errorf("no request %s in %s", want, reqs)
		}
	}

	// An unknown slug is an error before the release list is asked for.
	n := len(f.Requests())
	res, _, _ := h.listReleasesTool(context.Background(), nil, listReleasesArgs{Project: "argo-cd"})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.HasPrefix(text, "unknown project argo-cd") || !strings.Contains(text, "list_projects") {
		t.Errorf("list_releases argo-cd: isError=%v %s", res.IsError, text)
	}
	if got := f.Requests()[n:]; len(got) != 0 {
		t.Errorf("an unknown slug went upstream: %v", got)
	}

	var seen string
	h = withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/releases" { // the releases at or below from (1.2.0)
			w.Write([]byte(`{"releases":[],"next_before":null}`))
			return
		}
		seen = r.URL.Path
		w.Write([]byte(`{"project":"etcd","from":"v3.6.7","to":"v3.6.8","releases":[],"gaps":[],"later_for_from":[]}`))
	})))
	h.checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "Etcd ", From: "v3.6.7"})
	if seen != "/v2/upgrade/etcd" {
		t.Errorf("check_upgrade asked %s", seen)
	}
}

// "." and ".." are dot segments: the upstream resolved them before routing, so
// get_release(kubernetes, "..") returned the whole release list as one release
// and ("..", "..") the website's HTML. They are refused before any request.
// (Independent review before the 1.0 launch, 2026-11-15.)
func TestDotSegmentsAreRefusedBeforeAnyRequest(t *testing.T) {
	h := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/projects" {
			t.Errorf("upstream was asked %s", r.URL.RequestURI())
		}
		w.Write([]byte(`{"projects":[{"slug":"kubernetes","name":"Kubernetes"}]}`))
	})))
	for _, c := range []struct {
		tool string
		call func() *mcp.CallToolResult
		want string
	}{
		{"get_release version ..", func() *mcp.CallToolResult {
			r, _, _ := h.getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "kubernetes", Version: ".."})
			return r
		}, `version ".." is not a release version`},
		{"get_release version .", func() *mcp.CallToolResult {
			r, _, _ := h.getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "kubernetes", Version: "."})
			return r
		}, `version "." is not a release version`},
		{"get_release project ..", func() *mcp.CallToolResult {
			r, _, _ := h.getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "..", Version: ".."})
			return r
		}, `project ".." is not a project slug`},
		{"check_upgrade project ..", func() *mcp.CallToolResult {
			r, _, _ := h.checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "..", From: "v1"})
			return r
		}, `project ".." is not a project slug`},
	} {
		res := c.call()
		if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, c.want) {
			t.Errorf("%s: isError=%v %s", c.tool, res.IsError, text)
		}
	}
}

// What a tool passes through must be a JSON object; a web page answered with
// 200 is an error, not a result. check_upgrade does not turn a JSON object that
// is not an upgrade answer into an empty briefing either.
// (Independent review before the 1.0 launch, 2026-11-15.)
func TestAnswersThatAreNotAPIAnswersAreErrors(t *testing.T) {
	h := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/releases": // check_upgrade reads the release list first (1.2.0)
			w.Write([]byte(`{"releases":[],"next_before":null}`))
		case strings.HasPrefix(r.URL.Path, "/v2/upgrade/"):
			w.Write([]byte(`{"name":"Ratatosk agent API","version":"v2","endpoints":[]}`))
		default:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<!DOCTYPE html><html><body>home</body></html>"))
		}
	})))
	res, _, _ := h.getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Version: "v3.6.7"})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, "not a JSON object") || strings.Contains(text, "<html>") {
		t.Errorf("get_release on an HTML answer: isError=%v %s", res.IsError, text)
	}
	res, _, _ = h.searchTool(context.Background(), nil, searchArgs{Q: "CVE-2026-1"})
	if !res.IsError {
		t.Errorf("search on an HTML answer was a result")
	}
	res, _, _ = h.checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.6.7"})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, "not an upgrade answer") {
		t.Errorf("check_upgrade on a catalog answer: isError=%v %s", res.IsError, text)
	}
	if !isJSONObject([]byte(" \n{\"a\":1}")) || isJSONObject([]byte(`[1]`)) || isJSONObject([]byte(`{"a":`)) || isJSONObject(nil) {
		t.Error("isJSONObject")
	}
}

// The bounds on echoed text are byte counts, and a bound could fall inside a
// multi-byte character: the answer carried half a character, which a client
// received as U+FFFD (found 2026-11-21). The cut ends before that character;
// ASCII is cut where it always was.
func TestTruncateKeepsWholeCharacters(t *testing.T) {
	const n = 80
	for _, ch := range []string{"가", "あ", "😀"} {
		for k := 1; k < len(ch); k++ { // the bound falls k bytes into the character
			in := strings.Repeat("a", n-k) + strings.Repeat(ch, 10)
			got := truncate(in, n)
			if kept := strings.TrimSuffix(got, "…"); !utf8.ValidString(got) || len(kept) > n || kept != in[:n-k] || kept == got {
				t.Errorf("truncate(%d×a + %q…, %d) = %q, want the %d bytes before %q and …", n-k, ch, n, got, n-k, ch)
			}
		}
	}
	if got := truncate(strings.Repeat("a", 100), n); got != strings.Repeat("a", n)+"…" {
		t.Errorf("ASCII is cut at %d bytes as before: %q", n, got)
	}
	if got := truncate("가나다", n); got != "가나다" {
		t.Errorf("text within the bound is left alone: %q", got)
	}
	// Text that is not UTF-8 at all is out of scope; the cut still ends, in bounds.
	if got := truncate(strings.Repeat("\x80", 100), n); len(got) > n+len("…") {
		t.Errorf("not UTF-8: %d bytes", len(got))
	}

	// Through an answer: get_release echoes an unknown project in its refusal.
	h := withAPI(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)))
	res, _, _ := h.getReleaseTool(context.Background(), nil, getReleaseArgs{Project: strings.Repeat("a", 79) + "가나다"})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !utf8.ValidString(text) ||
		!strings.Contains(text, "unknown project "+strings.Repeat("a", 79)+"… — ") {
		t.Errorf("get_release on an unknown project cut inside 가: isError=%v %q", res.IsError, text)
	}
}

// Every tool answers the emptiest /v2 — an answer or a tool error, never a
// panic. A release document without warnings made check_stack and
// check_upgrade write into a nil map, and the panic took the whole server down
// (independent review, 2026-11-21).
func TestToolsAnswerTheSparsestUpstream(t *testing.T) {
	ctx := context.Background()
	stack := func(c stackComponent, detail string) checkStackArgs {
		return checkStackArgs{Components: []stackComponent{c}, Detail: detail}
	}
	calls := []struct {
		name string
		call func(h *handlers) (*mcp.CallToolResult, any, error)
	}{
		{"list_projects", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.listProjectsTool(ctx, nil, listProjectsArgs{})
		}},
		{"list_releases", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.listReleasesTool(ctx, nil, listReleasesArgs{})
		}},
		{"list_releases project", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.listReleasesTool(ctx, nil, listReleasesArgs{Project: "etcd"})
		}},
		{"get_release", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.getReleaseTool(ctx, nil, getReleaseArgs{Project: "etcd", Version: "v3.6.8"})
		}},
		{"get_release latest", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.getReleaseTool(ctx, nil, getReleaseArgs{Project: "etcd"})
		}},
		{"get_release kinds", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.getReleaseTool(ctx, nil, getReleaseArgs{Project: "etcd", Version: "v3.6.8", Kinds: stringList{"security"}})
		}},
		{"get_release changes", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.getReleaseTool(ctx, nil, getReleaseArgs{Project: "etcd", Version: "v3.6.8", Changes: stringList{"etcd@v3.6.8#a"}})
		}},
		{"get_release changes without version", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.getReleaseTool(ctx, nil, getReleaseArgs{Project: "etcd", Changes: stringList{"etcd@v3.6.8#a"}})
		}},
		{"search", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.searchTool(ctx, nil, searchArgs{Q: "etcd"})
		}},
		{"search project", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.searchTool(ctx, nil, searchArgs{Q: "CVE-2026-0001", Project: "etcd"})
		}},
		{"check_upgrade", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.checkUpgradeTool(ctx, nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30"})
		}},
		{"check_upgrade brief", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.checkUpgradeTool(ctx, nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30", Detail: "brief"})
		}},
		{"check_upgrade same_minor", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.checkUpgradeTool(ctx, nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30", SameMinor: true})
		}},
		{"check_upgrade full", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.checkUpgradeTool(ctx, nil, checkUpgradeArgs{Project: "etcd", From: "v3.5.30", Detail: "full", Kinds: stringList{"security"}})
		}},
		{"check_stack", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.checkStackTool(ctx, nil, stack(stackComponent{Project: "etcd", Version: "v3.5.30"}, ""))
		}},
		{"check_stack brief", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.checkStackTool(ctx, nil, stack(stackComponent{Project: "etcd", Version: "v3.5.30"}, "brief"))
		}},
		{"check_stack target", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.checkStackTool(ctx, nil, stack(stackComponent{Project: "etcd", Version: "v3.5.30", TargetVersion: "v3.6.8"}, ""))
		}},
		{"check_stack same_minor", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.checkStackTool(ctx, nil, stack(stackComponent{Project: "etcd", Version: "v3.5.30", SameMinor: true}, ""))
		}},
		{"check_stack full", func(h *handlers) (*mcp.CallToolResult, any, error) {
			return h.checkStackTool(ctx, nil, stack(stackComponent{Project: "etcd", Version: "v3.5.30"}, "full"))
		}},
	}
	for _, s := range upstreamtest.SparseShapes() {
		for _, c := range calls {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s on %s: panic: %v", c.name, s.Name, r)
					}
				}()
				h := withAPI(t, httptest.NewServer(s.Handler()))
				res, _, err := c.call(h)
				if err != nil || res == nil || len(res.Content) == 0 {
					t.Errorf("%s on %s: no answer (err %v)", c.name, s.Name, err)
				}
			}()
		}
	}
}
