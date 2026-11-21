package main

// The rules of the package layout that the compiler does not hold, held here
// so they need no prose to stay true. Imports are read from the sources with
// go/parser, so the test needs no go command and no network.

import (
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// importRules are imports a package must not reach, directly or through the
// packages it imports (non-test files only): the module's client, and the
// standard library's ways to the network (see network). What this holds is
// that such a package cannot fetch by itself. A client, or a function that
// fetches, handed to it as an argument fetches all the same; only the
// behaviour tests — what check_stack sends upstream — hold that.
var importRules = []struct{ from, to, why string }{
	{"internal/briefing", "internal/upstream", "a briefing is computed from the documents it is given: if it could fetch, " +
		"what check_stack sends upstream could depend on the running version, which is what check_stack exists to prevent"},
	{"internal/releaseline", "internal/upstream", "the range rules run on release rows read by project slug alone; " +
		"they must not choose what is fetched"},
	{"internal/advisory", "internal/upstream", "the advisory count is a function of the documents a briefing is given"},
	{"internal/release", "internal/upstream", "the release shapes are what the code that decides on releases may know " +
		"without knowing the client"},
}

// network says whether a standard package can reach the network: open a
// connection (net and the packages under it, crypto/tls) or start or call
// something that does (os/exec, syscall). net/url, net/netip and net/mail only
// parse values. A rule against internal/upstream alone let a package import
// net/http and fetch (independent review, 2026-11-21).
func network(p string) bool {
	switch p {
	case "net/url", "net/netip", "net/mail":
		return false
	case "net", "crypto/tls", "os/exec", "syscall":
		return true
	}
	return strings.HasPrefix(p, "net/")
}

// modulePackages reads every package of the module: its import path relative
// to the module, and the module packages and standard imports of its non-test
// files.
func modulePackages(t *testing.T) (module string, imports map[string][]string, docs map[string]bool) {
	t.Helper()
	gomod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindSubmatch(gomod)
	if m == nil {
		t.Fatal("go.mod names no module")
	}
	module = string(m[1])
	imports, docs = map[string][]string{}, map[string]bool{}
	fset := token.NewFileSet()
	err = filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly|parser.ParseComments)
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(filepath.Dir(p))
		if _, ok := imports[pkg]; !ok {
			imports[pkg] = nil
		}
		if f.Doc != nil && strings.TrimSpace(f.Doc.Text()) != "" {
			docs[pkg] = true
		}
		for _, s := range f.Imports {
			ip, _ := strconv.Unquote(s.Path.Value)
			if rel, ok := strings.CutPrefix(ip, module+"/"); ok {
				ip = rel
			}
			if !slices.Contains(imports[pkg], ip) {
				imports[pkg] = append(imports[pkg], ip)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return module, imports, docs
}

// reach lists every package from imports (module packages followed, standard
// ones listed but not followed).
func reach(imports map[string][]string, from string) map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(p string) {
		for _, ip := range imports[p] {
			if !seen[ip] {
				seen[ip] = true
				walk(ip)
			}
		}
	}
	walk(from)
	return seen
}

func TestImportRules(t *testing.T) {
	_, imports, _ := modulePackages(t)
	for _, r := range importRules {
		if _, ok := imports[r.from]; !ok {
			t.Errorf("rule for %s: no such package", r.from)
			continue
		}
		reached := reach(imports, r.from)
		if reached[r.to] {
			t.Errorf("%s reaches %s: %s", r.from, r.to, r.why)
		}
		for _, p := range slices.Sorted(maps.Keys(reached)) {
			if _, ours := imports[p]; !ours && network(p) {
				t.Errorf("%s reaches %s, a way to the network: %s", r.from, p, r.why)
			}
		}
	}

	// The binary is main and what it reaches. A test-only package (a "…test"
	// package beside the one it helps test) or the testing package in it would
	// ship test code in every release.
	inBinary := reach(imports, ".")
	for p := range inBinary {
		if _, ours := imports[p]; ours && strings.HasSuffix(path.Base(p), "test") {
			t.Errorf("the binary reaches the test-only package %s", p)
		}
		if p == "testing" || p == "net/http/httptest" {
			t.Errorf("the binary reaches %s", p)
		}
	}
}

// Each package says why it exists in its doc comment (go doc shows it); the
// layout is read there, not from a hand-kept list.
func TestEveryPackageHasADocComment(t *testing.T) {
	_, imports, docs := modulePackages(t)
	for p := range imports {
		if !docs[p] {
			t.Errorf("package %s has no doc comment on any of its package clauses", p)
		}
	}
}

// maxSourceLines is how long a source file may grow before it is split. A
// file past it is doing more than one job: stack.go reached 2,217 lines —
// argument shapes, range selection, the briefing and two tool handlers — and
// taking it apart cost a restructuring of its own (1.2.0, 2026-11-21). A
// feature that pushes a file past the line splits it in the same change
// (the owner, 2026-11-21). Test files are not held to it.
const maxSourceLines = 500

// longFiles are the source files allowed past maxSourceLines, each with why
// it is not split. An entry for a file that is back under the line fails, so
// the list cannot outlive its reasons.
var longFiles = map[string]string{}

func TestNoSourceFileOutgrowsItsJob(t *testing.T) {
	seen := map[string]bool{}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); p != "." && (strings.HasPrefix(name, ".") || name == "testdata" || name == "bin") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		p = filepath.ToSlash(p)
		lines := strings.Count(string(b), "\n")
		why, listed := longFiles[p]
		seen[p] = true
		switch {
		case lines > maxSourceLines && !listed:
			t.Errorf("%s is %d lines (limit %d): split it by what it does, or name it in longFiles with why it is one job", p, lines, maxSourceLines)
		case lines <= maxSourceLines && listed:
			t.Errorf("%s is %d lines, under the limit: take it out of longFiles (%q)", p, lines, why)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for p := range longFiles {
		if !seen[p] {
			t.Errorf("longFiles names %s, which is not a source file here", p)
		}
	}
}
