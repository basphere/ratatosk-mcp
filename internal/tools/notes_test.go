package tools

// The notes check_stack writes about a version, carried over from 0.x — each
// one a measured failure.

import (
	"strings"
	"testing"

	"github.com/basphere/ratatosk-mcp/internal/version"
)

func TestCoverageNote(t *testing.T) {
	k := version.NormalizeVersion
	if n := coverageNote("v1.16.0", k("v1.16.0"), k("v1.18.0"), "v1.18.0"); !strings.Contains(n, "older than every release on record") {
		t.Errorf("ancient version not flagged: %q", n)
	}
	if n := coverageNote("v1.19.0", k("v1.19.0"), k("v1.18.0"), "v1.18.0"); n != "" {
		t.Errorf("covered version flagged: %q", n)
	}
	if n := coverageNote("v1.0.0", k("v1.0.0"), nil, ""); n != "" {
		t.Errorf("no history must not be flagged: %q", n)
	}
}

func TestConfessionNote(t *testing.T) {
	for _, src := range []string{"inferred from chart version", "assumed default", "likely v1.2", "not verified"} {
		if confessionNote(src) == "" {
			t.Errorf("%q not flagged", src)
		}
	}
	for _, src := range []string{"", "daemonset/cilium image tag", "user stated it", "standard install manifest"} {
		if n := confessionNote(src); n != "" {
			t.Errorf("%q flagged: %q", src, n)
		}
	}
}

func TestResolveTarget(t *testing.T) {
	cur := version.NormalizeVersion("v1.36.8")
	if k, note := resolveTarget(cur, "v1.37.0"); k == nil || note != "" {
		t.Errorf("valid target refused: %v %q", k, note)
	}
	if k, note := resolveTarget(cur, "v1.36.8"); k != nil || !strings.Contains(note, "empty by construction") {
		t.Errorf("self-nullifying target honored: %v %q", k, note)
	}
	if k, note := resolveTarget(cur, "nonsense"); k != nil || note == "" {
		t.Errorf("unparseable target honored: %v %q", k, note)
	}
}
