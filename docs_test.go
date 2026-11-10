package main

// The public docs are checked against the server and against each other.
// Every 0.x → 1.0 docs pass found the same three failures by reading: a tool
// or parameter the docs still named after the code dropped it, an image pin
// one release behind (install guides sat at 0.7.4 until 1.0 prep), and a
// translation that kept an old command after the English one changed
// (examples/kagent/README.ko.md, found 2026-11-04). Reading does not scale to
// three languages; these checks do. The server is the source of truth for
// tools and parameters — it is listed here exactly as a client sees it.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// docTrios are the documents published in English, Korean and Japanese. Their
// structure must match line for line in kind: headings, tables, code, links.
var docTrios = [][3]string{
	{"README.md", "README.ko.md", "README.ja.md"},
	{"docs/install.en.md", "docs/install.ko.md", "docs/install.ja.md"},
	{"docs/tools.en.md", "docs/tools.ko.md", "docs/tools.ja.md"},
	{"charts/ratatosk-mcp/README.md", "charts/ratatosk-mcp/README.ko.md", "charts/ratatosk-mcp/README.ja.md"},
	{"examples/kagent/README.md", "examples/kagent/README.ko.md", "examples/kagent/README.ja.md"},
}

type mdDoc struct {
	path     string
	headings []int      // heading levels in order, outside code
	tables   [][]string // each table's lines
	code     []string   // fenced block bodies
	links    []string   // link targets outside code
	prose    string     // text outside code fences
}

var (
	headingRE = regexp.MustCompile(`^(#{1,6}) `)
	linkRE    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
)

func readMD(t *testing.T, path string) mdDoc {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return parseMD(path, string(b))
}

func parseMD(path, text string) mdDoc {
	d := mdDoc{path: path}
	var prose, block []string
	var table []string
	inCode := false
	flush := func() {
		if len(table) > 0 {
			d.tables = append(d.tables, table)
			table = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			if inCode {
				d.code = append(d.code, strings.Join(block, "\n"))
				block = nil
			}
			inCode = !inCode
			flush()
			continue
		}
		if inCode {
			block = append(block, line)
			continue
		}
		prose = append(prose, line)
		if m := headingRE.FindStringSubmatch(line); m != nil {
			d.headings = append(d.headings, len(m[1]))
		}
		if strings.HasPrefix(line, "|") {
			table = append(table, line)
		} else {
			flush()
		}
		for _, m := range linkRE.FindAllStringSubmatch(line, -1) {
			d.links = append(d.links, m[1])
		}
	}
	flush()
	d.prose = strings.Join(prose, "\n")
	return d
}

// serverTools lists the tools through an in-memory client: names and input
// schemas as an agent receives them, not as the source spells them.
func serverTools(t *testing.T) map[string][]string {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := newServer().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "docs-test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string][]string{}
	for _, tool := range res.Tools {
		b, _ := json.Marshal(tool.InputSchema)
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(b, &s)
		params := []string{}
		for p := range s.Properties {
			params = append(params, p)
		}
		sort.Strings(params)
		tools[tool.Name] = params
	}
	return tools
}

func keys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var cellNameRE = regexp.MustCompile("^`([a-z_]+)`$")

// firstCells returns the backticked identifiers in a table's first column.
func firstCells(table []string) []string {
	var out []string
	for _, row := range table {
		cells := strings.Split(strings.Trim(row, "|"), "|")
		if m := cellNameRE.FindStringSubmatch(strings.TrimSpace(cells[0])); m != nil {
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// toolSections splits a tools reference into "## <tool>" sections.
func toolSections(t *testing.T, path string, tools map[string][]string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	parts := regexp.MustCompile(`(?m)^## `).Split(string(b), -1)
	for _, p := range parts[1:] {
		name := strings.Trim(strings.SplitN(p, "\n", 2)[0], " `")
		if _, ok := tools[name]; ok {
			out[name] = p
		}
	}
	return out
}

func TestDocsNameEveryToolTheServerHas(t *testing.T) {
	tools := serverTools(t)
	want := keys(tools)
	for _, trio := range docTrios[:1] { // the READMEs carry the tools table
		for _, path := range trio {
			d := readMD(t, path)
			var got []string
			for _, tb := range d.tables {
				if cells := firstCells(tb); len(cells) > 0 && slices.Contains(want, cells[0]) {
					got = cells
				}
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s tools table = %v, server has %v", path, got, want)
			}
		}
	}
	for _, path := range docTrios[2] {
		var got []string
		for name := range toolSections(t, path, tools) {
			got = append(got, name)
		}
		sort.Strings(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s has sections for %v, server has %v", path, got, want)
		}
	}
}

func TestDocsParametersMatchSchemas(t *testing.T) {
	tools := serverTools(t)
	for _, path := range docTrios[2] {
		for name, sec := range toolSections(t, path, tools) {
			got := []string{}
			for _, tb := range parseMD(path, sec).tables {
				if cells := firstCells(tb); len(cells) > 0 {
					got = cells
					break
				}
			}
			if !slices.Equal(got, tools[name]) {
				t.Errorf("%s %s parameters = %v, schema has %v", path, name, got, tools[name])
			}
		}
	}
}

var (
	commentRE     = regexp.MustCompile(`(?m)(^|\s)#\s.*$`)
	placeholderRE = regexp.MustCompile(`<[^<>\n]+>`)
)

// sameCode ignores what a translation is allowed to change inside code:
// shell comments and <placeholders>.
func sameCode(s string) string {
	s = commentRE.ReplaceAllString(s, "")
	s = placeholderRE.ReplaceAllString(s, "<>")
	return strings.Join(strings.Fields(s), " ")
}

func normLink(l string) string {
	l = strings.SplitN(l, "#", 2)[0]
	for _, suf := range []string{".en.md", ".ko.md", ".ja.md"} {
		if strings.HasSuffix(l, suf) {
			return strings.TrimSuffix(l, suf) + ".LANG.md"
		}
	}
	if strings.HasSuffix(l, "README.md") {
		return strings.TrimSuffix(l, "README.md") + "README.LANG.md"
	}
	return l
}

func TestDocsLanguagesMatch(t *testing.T) {
	for _, trio := range docTrios {
		en := readMD(t, trio[0])
		for _, path := range trio[1:] {
			tr := readMD(t, path)
			if !slices.Equal(en.headings, tr.headings) {
				t.Errorf("%s headings %v differ from %s %v", path, tr.headings, trio[0], en.headings)
			}
			if len(en.tables) != len(tr.tables) {
				t.Errorf("%s has %d tables, %s has %d", path, len(tr.tables), trio[0], len(en.tables))
			} else {
				for i := range en.tables {
					if len(en.tables[i]) != len(tr.tables[i]) {
						t.Errorf("%s table %d has %d rows, %s has %d", path, i+1, len(tr.tables[i]), trio[0], len(en.tables[i]))
					}
					if a, b := firstCells(en.tables[i]), firstCells(tr.tables[i]); !slices.Equal(a, b) {
						t.Errorf("%s table %d names %v, %s names %v", path, i+1, b, trio[0], a)
					}
				}
			}
			if len(en.code) != len(tr.code) {
				t.Errorf("%s has %d code blocks, %s has %d", path, len(tr.code), trio[0], len(en.code))
			} else {
				for i := range en.code {
					if sameCode(en.code[i]) != sameCode(tr.code[i]) {
						t.Errorf("%s code block %d differs from %s beyond comments and placeholders:\n%s\n---\n%s", path, i+1, trio[0], tr.code[i], en.code[i])
					}
				}
			}
			a, b := []string{}, []string{}
			for _, l := range en.links {
				a = append(a, normLink(l))
			}
			for _, l := range tr.links {
				b = append(b, normLink(l))
			}
			sort.Strings(a)
			sort.Strings(b)
			if !slices.Equal(a, b) {
				t.Errorf("%s links differ from %s:\n%v\n%v", path, trio[0], b, a)
			}
		}
	}
}

var imageRE = regexp.MustCompile(`ghcr\.io/([A-Za-z0-9_-]+)/ratatosk-mcp:([0-9A-Za-z.\-]+)`)

func TestDocsPinTheChartRelease(t *testing.T) {
	chart, err := os.ReadFile("charts/ratatosk-mcp/Chart.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^appVersion:\s*"?([^"\s]+)"?`).FindSubmatch(chart)
	if m == nil {
		t.Fatal("no appVersion in Chart.yaml")
	}
	app := string(m[1])
	for _, trio := range docTrios {
		for _, path := range trio {
			b, _ := os.ReadFile(path)
			for _, im := range imageRE.FindAllStringSubmatch(string(b), -1) {
				if im[1] != "basphere" {
					t.Errorf("%s pulls %s — images are published under basphere since 2026-10-17", path, im[0])
				}
				if im[2] != app && im[2] != "latest" {
					t.Errorf("%s pins %s, the chart ships %s", path, im[0], app)
				}
			}
			if strings.Contains(strings.ToLower(string(b)), "github.com/garlickim21/ratatosk-mcp") {
				t.Errorf("%s links the old repository path; it moved to github.com/basphere/ratatosk-mcp", path)
			}
		}
	}
}

// Words the 1.0 server no longer has. An agent that reads them in the docs
// calls a tool or passes an argument that is refused.
var goneWords = []string{"`list_changes`", "`changes_by_entity`", "`get_matter`", "`severity_min`", "`include_raw`", "`applies_if`", "`bucket`", "`family`"}

func TestDocsStyle(t *testing.T) {
	for _, trio := range docTrios {
		for _, path := range trio {
			d := readMD(t, path)
			for i, line := range strings.Split(d.prose, "\n") {
				// The em dash is the mark machine-written prose leaves most
				// often; the site's copy rules drop it (2026-11-01).
				if strings.Contains(line, "—") {
					t.Errorf("%s prose line %d has an em dash: %s", path, i+1, strings.TrimSpace(line))
				}
			}
			b, _ := os.ReadFile(path)
			for i, line := range strings.Split(string(b), "\n") {
				if strings.Contains(line, "0.x") {
					continue // a migration note may name what 0.x had
				}
				for _, w := range goneWords {
					if strings.Contains(line, w) {
						t.Errorf("%s line %d names %s, which 1.0 removed", path, i+1, w)
					}
				}
			}
			// The mascot was retired for the symbol on 2026-11-01.
			for _, a := range []string{"ratatosk-hero", "ratatosk-face"} {
				if strings.Contains(string(b), a) {
					t.Errorf("%s shows the retired mascot (%s)", path, a)
				}
			}
		}
	}
}

func TestDocsRelativeLinksResolve(t *testing.T) {
	for _, trio := range docTrios {
		for _, path := range trio {
			for _, l := range readMD(t, path).links {
				if strings.Contains(l, "://") || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "mailto:") {
					continue
				}
				target := filepath.Join(filepath.Dir(path), strings.SplitN(l, "#", 2)[0])
				if _, err := os.Stat(target); err != nil {
					t.Errorf("%s links %s, which does not exist", path, l)
				}
			}
		}
	}
}

// An older release of this server named in the docs is a pin or an example
// that was not moved: the install guides said 0.7.4 in prose, in a log line
// and in a serverInfo answer until 1.0 prep (2026-11-04). Released versions
// come from CHANGELOG headings, so the list grows by itself.
func TestDocsNameNoOlderRelease(t *testing.T) {
	cl, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	chart, _ := os.ReadFile("charts/ratatosk-mcp/Chart.yaml")
	app := string(regexp.MustCompile(`(?m)^appVersion:\s*"?([^"\s]+)"?`).FindSubmatch(chart)[1])
	var old []*regexp.Regexp
	var names []string
	for _, m := range regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`).FindAllStringSubmatch(string(cl), -1) {
		if m[1] == app {
			continue
		}
		names = append(names, m[1])
		old = append(old, regexp.MustCompile(`(^|[^0-9.])v?`+regexp.QuoteMeta(m[1])+`($|[^0-9])`))
	}
	if len(old) == 0 {
		t.Fatal("no released versions found in CHANGELOG.md")
	}
	for _, trio := range docTrios {
		for _, path := range trio {
			b, _ := os.ReadFile(path)
			for i, line := range strings.Split(string(b), "\n") {
				if strings.Contains(line, "0.x") {
					continue
				}
				for j, re := range old {
					if re.MatchString(line) {
						t.Errorf("%s line %d names release %s (the chart ships %s): %s", path, i+1, names[j], app, strings.TrimSpace(line))
					}
				}
			}
		}
	}
}

// slugify follows GitHub's heading anchors: lower case, drop everything but
// letters, digits, spaces, hyphens and underscores, spaces become hyphens.
func slugify(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(h)) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func anchors(t *testing.T, path string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	seen := map[string]int{}
	inCode := false
	b, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		if m := regexp.MustCompile(`^#{1,6} (.+)$`).FindStringSubmatch(line); m != nil {
			s := slugify(strings.NewReplacer("`", "", "*", "").Replace(m[1]))
			if n := seen[s]; n > 0 {
				out[s+"-"+strconv.Itoa(n)] = true
			} else {
				out[s] = true
			}
			seen[s]++
		}
	}
	return out
}

// A translated heading changes its anchor; a link that kept the English one
// lands at the top of the page without an error. Check every #fragment.
func TestDocsAnchorsResolve(t *testing.T) {
	for _, trio := range docTrios {
		for _, path := range trio {
			for _, l := range readMD(t, path).links {
				if strings.Contains(l, "://") || !strings.Contains(l, "#") {
					continue
				}
				file, frag, _ := strings.Cut(l, "#")
				target := path
				if file != "" {
					target = filepath.Join(filepath.Dir(path), file)
				}
				if !strings.HasSuffix(target, ".md") {
					continue
				}
				if !anchors(t, target)[frag] {
					t.Errorf("%s links %s, but %s has no heading with that anchor", path, l, target)
				}
			}
		}
	}
}
