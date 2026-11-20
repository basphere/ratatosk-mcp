package releaseline

// The range rules on hand-made rows; check_stack's tests (internal/tools) run
// them on whole answers.

import (
	"slices"
	"strings"
	"testing"

	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

// Same cases as the web's version-gap test (apps/web/src/server/agent):
// the server's /v2/upgrade and check_stack must flag the same places.
func TestVersionGaps(t *testing.T) {
	cases := []struct {
		points []string
		want   []Gap
	}{
		{[]string{"v3.5.30", "v3.5.31", "v3.5.32", "v3.6.7", "v3.6.8"}, []Gap{{"v3.5.32", "v3.6.7"}}},
		{[]string{"1.9.4", "2.0.0", "2.0.2"}, []Gap{{"2.0.0", "2.0.2"}}},
		{[]string{"v1.4.0", "v1.6.0"}, []Gap{{"v1.4.0", "v1.6.0"}}},
		{[]string{"0.9.52.0", "0.9.53.0", "v1.2.3-rc.1", "v1.2.3"}, []Gap{{"0.9.53.0", "v1.2.3-rc.1"}}},
		{[]string{"v2.1.0", "v2.1.1"}, nil},
	}
	for _, c := range cases {
		if got := Gaps(c.points); !slices.Equal(got, c.want) {
			t.Errorf("Gaps(%v) = %v, want %v", c.points, got, c.want)
		}
	}
}

func TestUpgradeRange(t *testing.T) {
	rows := []release.Row{{Version: "v1.24.4"}, {Version: "v1.24.3"}, {Version: "v1.24.3-rc1"}, {Version: "v1.24.2"}, {Version: "api/v1.30.0"}, {Version: "redirect"}}
	got := func(running, target string) ([]string, string) {
		r, tgt := UpgradeRange(rows, running, target)
		var v []string
		for _, x := range r {
			v = append(v, x.Version)
		}
		return v, tgt
	}
	if v, tgt := got("v1.24.2", ""); !slices.Equal(v, []string{"v1.24.3-rc1", "v1.24.3", "v1.24.4"}) || tgt != "v1.24.4" {
		t.Errorf("full path %v → %s", v, tgt)
	}
	if v, _ := got("v1.24.3-rc1", "v1.24.3"); !slices.Equal(v, []string{"v1.24.3"}) {
		t.Errorf("the finished release ranks above its own pre-release: %v", v)
	}
	if v, _ := got("1.24.2", "v1.24.3"); !slices.Equal(v, []string{"v1.24.3-rc1", "v1.24.3"}) {
		t.Errorf("to is inclusive, a missing v is fine: %v", v)
	}
	if v, _ := got("api/v1.29.0", ""); !slices.Equal(v, []string{"api/v1.30.0"}) {
		t.Errorf("only the running line: %v", v)
	}
}

// Projects that tag without a "v" (falco, istio, keycloak, strimzi): a caller
// who writes the "v" must get the same range as one who does not. Before
// 2026-11-11 the running release itself came back as upgrade work, and a target
// spelled with "v" returned no releases and no note.
func TestUpgradeRangeIgnoresTheCallersVStyle(t *testing.T) {
	bare := []release.Row{{Version: "0.45.2"}, {Version: "0.45.1"}, {Version: "0.45.1-rc1"}, {Version: "0.45.0"}}
	withV := []release.Row{{Version: "v0.45.2"}, {Version: "v0.45.1"}, {Version: "v0.45.1-rc1"}, {Version: "v0.45.0"}}
	names := func(rows []release.Row, running, target string) []string {
		r, _ := UpgradeRange(rows, running, target)
		var v []string
		for _, x := range r {
			v = append(v, strings.TrimPrefix(x.Version, "v"))
		}
		return v
	}
	for _, rows := range [][]release.Row{bare, withV} {
		for _, c := range []struct {
			running, target string
			want            []string
		}{
			{"0.45.1", "", []string{"0.45.2"}},
			{"v0.45.1", "", []string{"0.45.2"}},
			{"0.45.0", "0.45.1", []string{"0.45.1-rc1", "0.45.1"}},
			{"0.45.0", "v0.45.1", []string{"0.45.1-rc1", "0.45.1"}},
			{"v0.45.0", "0.45.1", []string{"0.45.1-rc1", "0.45.1"}},
			{"v0.45.1-rc1", "", []string{"0.45.1", "0.45.2"}},
			{"0.45.1-rc1", "v0.45.1", []string{"0.45.1"}},
			{"v0.45.2", "", nil},
			// Not on record: a pre-release nobody analyzed still sorts below its release.
			{"v0.45.2-rc9", "", []string{"0.45.2"}},
			{"0.45.2-rc9", "", []string{"0.45.2"}},
		} {
			if got := names(rows, c.running, c.target); !slices.Equal(got, c.want) {
				t.Errorf("tags like %q, running %q target %q: got %v, want %v", rows[0].Version, c.running, c.target, got, c.want)
			}
		}
	}
}

func TestSameTag(t *testing.T) {
	if !SameTag("1.30.2", "v1.30.2") || SameTag("v1.30.2", "api/v1.30.2") || SameTag("v1.24.3", "v1.24.3-rc1") {
		t.Error("SameTag")
	}
}

func TestSameBranch(t *testing.T) {
	k := version.NormalizeVersion
	if !SameBranch(k("v2.2.4"), k("v2.2.5")) || SameBranch(k("v2.2.4"), k("v2.3.1")) || SameBranch(k("v2"), k("v2")) {
		t.Error("SameBranch")
	}
}
