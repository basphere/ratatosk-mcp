package main

// What tools/list says is 1.0.0's, word for word, but for one passage.
//
// Why: 1.1.0 is additive. A tool description is what an agent reads before it
// calls; a sentence left over from the widened same-fix grouping (taken out of
// 1.1.0 on 2026-11-18) still told agents that a CVE and its GHSA count as one
// fix, which the code no longer does (final review, same day). The only
// change the owner approved is the passage below, in check_stack's
// description. testdata/tools-list-1.0.0.json is tools/list as ff60e46
// answers it (dumped through an in-memory client from that commit's
// newServer), so a description or a schema that drifts fails here.

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// approvedCheckStackPassage is the one passage 1.1.0 adds to a tool
// description (check_stack), approved by the owner on 2026-11-18.
const approvedCheckStackPassage = "summary.counts and summary.warnings add up every release in range (one fix on several " +
	"release lines counts once per release); summary.distinct_advisories counts each advisory once. "

func TestToolsListIs100ApartFromTheApprovedPassage(t *testing.T) {
	raw, err := os.ReadFile("testdata/tools-list-1.0.0.json")
	if err != nil {
		t.Fatal(err)
	}
	var frozen []map[string]any
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := newServer().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "tools100-test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Tools)
	var current []map[string]any
	if err := json.Unmarshal(b, &current); err != nil {
		t.Fatal(err)
	}

	if len(current) != len(frozen) {
		t.Fatalf("%d tools, 1.0.0 listed %d", len(current), len(frozen))
	}
	for i, tool := range current {
		if tool["name"] == "check_stack" {
			d, _ := tool["description"].(string)
			if strings.Count(d, approvedCheckStackPassage) != 1 {
				t.Errorf("check_stack's description does not carry the approved passage once")
			}
			tool["description"] = strings.Replace(d, approvedCheckStackPassage, "", 1)
		}
		old := frozen[i]
		if tool["name"] != old["name"] {
			t.Errorf("tool %d is %v, 1.0.0 listed %v", i, tool["name"], old["name"])
			continue
		}
		for _, k := range []string{"description", "inputSchema", "annotations", "title", "outputSchema", "_meta"} {
			if !reflect.DeepEqual(tool[k], old[k]) {
				a, _ := json.Marshal(old[k])
				c, _ := json.Marshal(tool[k])
				n := 0
				for n < len(a) && n < len(c) && a[n] == c[n] {
					n++
				}
				t.Errorf("%v %s differs from 1.0.0 at byte %d:\n  1.0.0 …%.160s\n  now   …%.160s", tool["name"], k, n, a[max(0, n-40):], c[max(0, n-40):])
			}
		}
		for k := range tool {
			if _, ok := old[k]; !ok {
				t.Errorf("%v has a field 1.0.0 did not list: %s", tool["name"], k)
			}
		}
	}
}
