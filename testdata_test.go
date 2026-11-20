package main

// The test data table (testdata/README.md) and the files under every testdata
// folder of the module must agree. Without this check test data only piles
// up: testdata/tools-list-1.0.0.json sat in the repository for a release with
// no test reading it (removed 2026-11-21). The table says what each bundle is,
// how it was made, which tests read it and when it goes; this holds the table
// to the files, so a bundle cannot be added, left behind or half removed
// without the table saying so.

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// testdataRow is one bundle of the table.
type testdataRow struct {
	folder   string   // repository-relative, with a trailing "/"
	readBy   []string // the tests that read it
	goesWith []string // repository-relative files removed with it
}

var (
	backtickedRE = regexp.MustCompile("`([^`]+)`")
	testNameRE   = regexp.MustCompile(`^Test[A-Za-z0-9_]+$`)
)

// readTestdataTable parses the bundle rows: the table lines whose first cell
// is a backticked folder.
func readTestdataTable(t *testing.T) []testdataRow {
	t.Helper()
	b, err := os.ReadFile("testdata/README.md")
	if err != nil {
		t.Fatal(err)
	}
	var rows []testdataRow
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "| "), " | ")
		if len(cells) != 6 {
			t.Fatalf("testdata/README.md: a bundle row has %d cells, want 6 (bundle, what, made, read by, goes with, remove when): %.120s", len(cells), line)
		}
		m := backtickedRE.FindStringSubmatch(cells[0])
		if m == nil || !strings.HasSuffix(m[1], "/") {
			t.Fatalf("testdata/README.md: a bundle row does not start with a folder: %.120s", line)
		}
		row := testdataRow{folder: m[1]}
		for _, n := range backtickedRE.FindAllStringSubmatch(cells[3], -1) {
			if testNameRE.MatchString(n[1]) {
				row.readBy = append(row.readBy, n[1])
			}
		}
		for _, n := range backtickedRE.FindAllStringSubmatch(cells[4], -1) {
			row.goesWith = append(row.goesWith, n[1])
		}
		rows = append(rows, row)
	}
	return rows
}

// testdataFiles lists every file under a folder named testdata, repository
// relative, the table itself aside.
func testdataFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		p = filepath.ToSlash(p)
		if d.IsDir() || p == "testdata/README.md" {
			return nil
		}
		if slashed := "/" + p; strings.Contains(slashed, "/testdata/") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTestdataTableMatchesTheFiles(t *testing.T) {
	rows := readTestdataTable(t)
	if len(rows) == 0 {
		t.Fatal("testdata/README.md lists no bundle")
	}
	files := testdataFiles(t)

	// Every file is in a bundle.
	for _, f := range files {
		covered := false
		for _, r := range rows {
			if strings.HasPrefix(f, r.folder) {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("%s is test data no row of testdata/README.md covers: add its bundle to the table, or remove it", f)
		}
	}

	for _, r := range rows {
		// Every bundle is a testdata folder with files in it.
		pkg, _, ok := strings.Cut("/"+r.folder, "/testdata/")
		if !ok {
			t.Errorf("testdata/README.md: %s is not under a testdata folder", r.folder)
			continue
		}
		pkg = strings.TrimPrefix(pkg, "/")
		n := 0
		for _, f := range files {
			if strings.HasPrefix(f, r.folder) {
				n++
			}
		}
		if n == 0 {
			t.Errorf("testdata/README.md lists %s, which holds no file: remove the row with the bundle", r.folder)
		}

		// Every bundle is read by tests of its package that exist.
		if len(r.readBy) == 0 {
			t.Errorf("testdata/README.md: %s names no test that reads it", r.folder)
		}
		var src strings.Builder
		tests, _ := filepath.Glob(filepath.Join(filepath.FromSlash(pkg), "*_test.go"))
		for _, f := range tests {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			src.Write(b)
		}
		for _, name := range r.readBy {
			if !regexp.MustCompile(`(?m)^func ` + name + `\(t \*testing\.T\) \{`).MatchString(src.String()) {
				t.Errorf("testdata/README.md: %s is read by %s, which is not a test in %s", r.folder, name, path.Join(pkg, "*_test.go"))
			}
		}

		// What goes with a bundle exists.
		for _, f := range r.goesWith {
			if _, err := os.Stat(filepath.FromSlash(f)); err != nil {
				t.Errorf("testdata/README.md: %s goes with %s, which does not exist", r.folder, f)
			}
		}
	}
}
