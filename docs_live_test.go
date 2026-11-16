package main

// The measured responses in docs/tools.en.md and the README excerpt are
// replayed against a live API before a release: every key the docs show must
// still be in the answer to the same call. Values move with the data and are
// not compared. Off by default — CI has no business depending on ratatosk.io
// being up. Run before tagging:
//
//	DOCS_LIVE_URL=https://ratatosk.io go test -run TestDocsExamplesAgainstLive -v .
//
// Added 2026-11-04 for 1.0: the examples were measured on a /v2 preview on
// 11/03 and /v2 changed the same evening (security_advisory evidence source).

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	jsonBlockRE  = regexp.MustCompile("(?ms)^```json\n(.*?)^```")
	inlineArgsRE = regexp.MustCompile("(?m)^`(\\{.*\\})`:?\\s*$")
)

// keyPaths lists every key path in a JSON value; array elements collapse to [].
func keyPaths(v any, prefix string, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			p := prefix + "." + k
			out[p] = true
			keyPaths(val, p, out)
		}
	case []any:
		for _, val := range x {
			keyPaths(val, prefix+"[]", out)
		}
	}
}

func liveCall(t *testing.T, cs *mcp.ClientSession, tool, args string) any {
	t.Helper()
	var a map[string]any
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		t.Fatalf("%s example arguments do not parse: %v", tool, err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: a})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError || len(res.Content) == 0 {
		t.Fatalf("%s returned an error: %+v", tool, res.Content)
	}
	var v any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &v); err != nil {
		t.Fatalf("%s answer is not JSON: %v", tool, err)
	}
	return v
}

func TestDocsExamplesAgainstLive(t *testing.T) {
	base := os.Getenv("DOCS_LIVE_URL")
	if base == "" {
		t.Skip("set DOCS_LIVE_URL to replay the documented examples")
	}
	api = newAPIClient(base)
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := newServer().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "docs-live"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools := serverTools(t)
	for name, sec := range toolSections(t, "docs/tools.en.md", tools) {
		blocks := jsonBlockRE.FindAllStringSubmatch(sec, -1)
		args := "{}"
		var shown string
		switch {
		case len(blocks) >= 2: // "Example call" block, then the response
			args, shown = blocks[0][1], blocks[1][1]
		case len(blocks) == 1:
			shown = blocks[0][1]
			if m := inlineArgsRE.FindStringSubmatch(sec); m != nil {
				args = m[1]
			}
		default:
			t.Errorf("%s: no measured response in docs/tools.en.md", name)
			continue
		}
		var doc any
		if err := json.Unmarshal([]byte(shown), &doc); err != nil {
			t.Errorf("%s: documented response is not JSON: %v", name, err)
			continue
		}
		want, got := map[string]bool{}, map[string]bool{}
		keyPaths(doc, "", want)
		keyPaths(liveCall(t, cs, name, args), "", got)
		var missing []string
		for p := range want {
			if !got[p] {
				missing = append(missing, p)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s %s: documented keys the live answer lacks:\n  %s", name, args, strings.Join(missing, "\n  "))
		} else {
			t.Logf("%s %s: all %d documented keys present", name, args, len(want))
		}
	}

	// README: one check_stack entry, shown on its own. Its keys must be the keys
	// of a change entry in a live check_stack answer.
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	m := jsonBlockRE.FindStringSubmatch(string(readme))
	if m == nil {
		t.Fatal("README.md has no JSON excerpt")
	}
	var entry any
	if err := json.Unmarshal([]byte(m[1]), &entry); err != nil {
		t.Fatalf("README excerpt is not JSON: %v", err)
	}
	want, got := map[string]bool{}, map[string]bool{}
	keyPaths(entry, "", want)
	live := liveCall(t, cs, "check_stack", `{"components":[{"project":"etcd","version":"v3.5.30"},{"project":"cert-manager","version":"v1.19.2"}]}`)
	keyPaths(live, "", got)
	var missing []string
	for p := range want {
		found := false
		for g := range got {
			if strings.HasSuffix(g, "[]"+p) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("README check_stack excerpt shows keys no live change entry has:\n  %s", strings.Join(missing, "\n  "))
	}
}
