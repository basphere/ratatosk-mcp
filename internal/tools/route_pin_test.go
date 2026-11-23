package tools

// What did not change with the route (1.3.0): detail:"full" answers byte for
// byte as before it, and detail:"brief" — and a range of one step without
// detail, once its opening fields are set aside — gives the same fields and
// values, only in a fixed order. The hashes are of the answers the code
// before the route gave (branch v1.3 at e8dc1dd, taken 2026-11-23); a change
// that leaks into these answers changes them.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basphere/ratatosk-mcp/internal/upstream/upstreamtest"
)

// beforeRoute are calls whose answers are pinned. front is the number of
// opening fields the answer has now and did not have then; sorted says the
// answer's top-level fields are compared in name order, as they were written
// before the route.
var beforeRoute = []struct {
	name   string
	call   func(h *handlers) (*mcp.CallToolResult, any, error)
	front  int
	sorted bool
	want   string // sha256 of the answer text before the route
}{
	{"check_upgrade brief", upgradeCall(checkUpgradeArgs{Project: "etcd", From: "v3.5.30", Detail: "brief"}), 0, true,
		"a1b34b8759f2a26956c531278ae4fba6509076aad0307c208c4175684c273f71"},
	// Taken with lang "en", which sent no lang upstream: lang left in 1.3.0.
	{"check_upgrade brief kinds", upgradeCall(checkUpgradeArgs{Project: "etcd", From: "v3.5.30", Detail: "brief",
		Kinds: stringList{"security"}}), 0, true,
		"2cf7cb434f053b099aaf53696258d1f0a05438dbe8c4ef6d6acd1fd25e5954b3"},
	{"check_upgrade one step", upgradeCall(checkUpgradeArgs{Project: "etcd", From: "v3.6.7"}), 3, true,
		"3f30b879d29c54a20818cec56d57c24e53f8661c1777f28472d6e6c42ef39730"},
	{"check_upgrade full", upgradeCall(checkUpgradeArgs{Project: "etcd", From: "v3.5.30", Detail: "full"}), 0, false,
		"73c3a1bc5cea330eeadbf27d4d6becf974c14e4ff7fed6a8bae2b843518e42e3"},
	{"check_stack brief", stackCall(checkStackArgs{Components: []stackComponent{
		{Project: "etcd", Version: "v3.5.30", VersionSource: "user"}, {Project: "nope", Version: "v1.0.0"}}, Detail: "brief"}), 0, true,
		"f1ef44f2e17acfaab13f91fb12b426baec270e6cc8696c8c478789864ea9fdd8"},
	{"check_stack brief kinds", stackCall(checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.6.7"}},
		Detail: "brief", Kinds: stringList{"security"}}), 0, true, // taken with lang "en", as above
		"fb946adc760dc28d9dfee405a6bff2752aa471e66327232d59b323a00d7d1769"},
	{"check_stack full", stackCall(checkStackArgs{Components: []stackComponent{{Project: "etcd", Version: "v3.5.30"}}, Detail: "full"}), 0, false,
		"4f36cbaab22c366f532da223619424a4574a0382c9f8a029ac7521123aab61f5"},
}

func upgradeCall(args checkUpgradeArgs) func(h *handlers) (*mcp.CallToolResult, any, error) {
	return func(h *handlers) (*mcp.CallToolResult, any, error) {
		return h.checkUpgradeTool(context.Background(), nil, args)
	}
}

func stackCall(args checkStackArgs) func(h *handlers) (*mcp.CallToolResult, any, error) {
	return func(h *handlers) (*mcp.CallToolResult, any, error) {
		return h.checkStackTool(context.Background(), nil, args)
	}
}

// etcdAnswer is the text of call on the fake etcd history.
func etcdAnswer(t *testing.T, call func(h *handlers) (*mcp.CallToolResult, any, error)) string {
	t.Helper()
	f := upstreamtest.NewEtcd()
	h := withAPI(t, httptest.NewServer(f.Handler(t)))
	res, _, err := call(h)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	return res.Content[0].(*mcp.TextContent).Text
}

func TestAnswersOtherThanTheRouteAreAsBefore(t *testing.T) {
	for _, c := range beforeRoute {
		text := etcdAnswer(t, c.call)
		if c.front > 0 || c.sorted {
			fields, err := readObject([]byte(text))
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			opening := fields[:c.front]
			for i, k := range []string{"answer_kind", "report_ready", "not_included"}[:c.front] {
				if opening[i].Key != k {
					t.Errorf("%s: opening field %d is %s, want %s", c.name, i, opening[i].Key, k)
				}
			}
			if c.front > 0 && string(opening[0].Val) != `"briefing"` {
				t.Errorf("%s: answer_kind %s", c.name, opening[0].Val)
			}
			fields = fields[c.front:]
			if c.sorted {
				if fields[0].Key != "hint" {
					t.Errorf("%s: the briefing opens with %s, not hint", c.name, fields[0].Key)
				}
				fields = slices.Clone(fields)
				slices.SortFunc(fields, func(a, b objField) int { return strings.Compare(a.Key, b.Key) })
			}
			text = string(writeObject(fields))
		}
		sum := sha256.Sum256([]byte(text))
		if got := hex.EncodeToString(sum[:]); got != c.want {
			t.Errorf("%s: sha256 %s, before the route %s (%d bytes now)", c.name, got, c.want, len(text))
		}
	}
}
