package mcpserver

// lang left every tool in 1.3.0; a client written against 1.2.0 still sends
// it. Its call is answered as the same call without lang, never refused.

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/mcpserver/mcpservertest"
	"github.com/basphere/ratatosk-mcp/internal/tools"
	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// retiredCalls are a call to every tool that took lang before 1.3.0.
var retiredCalls = map[string]map[string]any{
	"check_stack":   {"components": []any{map[string]any{"project": "etcd", "version": "v3.5.30"}}},
	"check_upgrade": {"project": "etcd", "from": "v3.5.30"},
	"get_release":   {"project": "etcd", "version": "v3.6.7"},
	"list_releases": {"project": "etcd"},
	"search":        {"q": "v2store"},
}

// etcdWithSearch is the fake etcd history with a /v2/search answer.
func etcdWithSearch(t *testing.T, f *upstreamtest.Fake) http.HandlerFunc {
	inner := f.Handler(t)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/search" {
			inner(w, r)
			return
		}
		f.Record(r.URL.RequestURI())
		w.Write([]byte(`{"q":"v2store","in_analysis":[{"project":"etcd","version":"v3.6.7","matched":{"id":"etcd@v3.6.7#k","kind":"breaking","title":"v2store removed"}}],"in_release_notes":[],"in_linked_documents":[]}`))
	}
}

// The schema lists no lang and refuses a key it does not list, so the call
// reaches the tool only because IgnoreRetiredArguments takes lang off; any
// value a 1.2.0 client could send, or a wrong one, is answered as the call
// without it, and upstream is asked in English. Another key the schema does
// not list is still refused.
func TestRetiredArgumentsAreAcceptedAndIgnored(t *testing.T) {
	f := upstreamtest.NewEtcd()
	cs := sdkSession(t, Options{API: upstreamtest.Client(t, httptest.NewServer(etcdWithSearch(t, f)), nil)})
	for tool, args := range retiredCalls {
		want, isErr := sdkText(t, cs, tool, args)
		if isErr {
			t.Fatalf("%s without lang: %s", tool, want)
		}
		for _, lang := range []any{"ko", "ja", "en", "de", 1} {
			with := maps.Clone(args)
			with["lang"] = lang
			if got, isErr := sdkText(t, cs, tool, with); isErr || got != want {
				t.Errorf("%s with lang %v: error=%v, the same answer as without lang: %v\n%.300s", tool, lang, isErr, got == want, got)
			}
		}
		with := maps.Clone(args)
		with["langs"] = "ko"
		if text, isErr := sdkText(t, cs, tool, with); !isErr || !strings.Contains(text, "langs") {
			t.Errorf("%s with a key no tool reads: error=%v %.300s", tool, isErr, text)
		}
	}
	for _, u := range f.Requests() {
		if strings.Contains(u, "lang=") {
			t.Errorf("upstream was asked in another language: %s", u)
		}
	}
}

// Why the middleware exists: the SDK checks a call against the schema before
// the tool runs and refuses lang there. If this stops failing, the SDK reads
// unknown keys differently, and what the middleware holds needs a new look.
func TestTheSchemaAloneRefusesARetiredArgument(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "bare"}, nil)
	tools.Register(s, upstreamtest.Client(t, httptest.NewServer(upstreamtest.NewEtcd().Handler(t)), nil), "test")
	cs := mcpservertest.Connect(t, s, &mcp.Implementation{Name: "bare-test"})
	args := maps.Clone(retiredCalls["get_release"])
	args["lang"] = "ko"
	if text, isErr := sdkText(t, cs, "get_release", args); !isErr || !strings.Contains(text, "lang") {
		t.Errorf("the SDK alone answered a call with lang: error=%v %.300s", isErr, text)
	}
}
