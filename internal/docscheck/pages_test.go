// The page checks. Every page under docs/ carries frontmatter the parser
// admits, is titled by its one heading, sits where its type says, covers code
// that exists, stays within its budget, draws diagrams the counter can read
// and names the generator that wrote it. docs.json's excluded selects which
// pages are judged, never which files a page may cover; the walk is fatal
// when it finds fewer pages than exist today.
package docscheck

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/docscheck/docsconfig"
	"github.com/guardana/control/internal/docscheck/frontmatter"
)

const (
	docsConfigPath = "docs/docs.json"
	docsPrefix     = "docs/"
	readmeName     = "README.md"
	docsGenTarget  = "docs-gen"
	// The pages and readmes that exist today. A walk that finds fewer
	// examined some other tree.
	minPagesToday   = 29
	minReadmesToday = 10
)

// blockClaim claims one generated block inside a hand-written page: the page
// and the generator its marker names. A marker no claim covers fails.
type blockClaim struct {
	page, generator string
}

// generatedBlocks is the registry of claimed blocks.
var generatedBlocks = []blockClaim{
	{"docs/concepts/enforcement-modes.md", "scripts/gen-diagrams.go"},
	{"docs/concepts/how-a-call-is-decided.md", "scripts/gen-diagrams.go"},
	{"docs/concepts/evidence-and-the-spool.md", "scripts/gen-diagrams.go"},
	{"docs/reference/cli.md", "go test ./cmd/" + brand.CLI + " -run TestCLIPage"},
	{"docs/reference/cli.md", "go test ./cmd/" + brand.Gateway + " -run TestCLIPage"},
}

// page is one Markdown file the walk found, by repository-relative path.
type page struct {
	path string
	data []byte
}

// parsedPage is a page whose frontmatter the parser admitted.
type parsedPage struct {
	path string
	meta frontmatter.Meta
	body []byte
}

// docsTree is what one walk found: every file a page may point at, and the
// pages under docs/ and the README and root files that are judged.
type docsTree struct {
	config  docsconfig.Config
	files   []string
	pages   []page
	readmes []page
}

func loadTree(t *testing.T) docsTree {
	t.Helper()
	fsys := repoFS(t)
	data, err := fs.ReadFile(fsys, docsConfigPath)
	if err != nil {
		t.Fatalf("reading %s: %v", docsConfigPath, err)
	}
	cfg, err := docsconfig.Parse(data)
	if err != nil {
		t.Fatalf("%s: %v", docsConfigPath, err)
	}
	tree, err := collectTree(fsys, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.pages) < minPagesToday {
		t.Fatalf("the walk found %d pages under docs/, want at least %d", len(tree.pages), minPagesToday)
	}
	if len(tree.readmes) < minReadmesToday {
		t.Fatalf("the walk found %d README and root files, want at least %d", len(tree.readmes), minReadmesToday)
	}
	return tree
}

// collectTree walks fsys once and sorts what it found: every file a covers
// glob or a Sources path may name, and the pages and readmes docs.json does
// not exclude, which are the ones judged.
func collectTree(fsys fs.FS, cfg docsconfig.Config) (docsTree, error) {
	files, err := walkFiles(fsys)
	if err != nil {
		return docsTree{}, err
	}
	tree := docsTree{config: cfg, files: files}
	for _, rel := range files {
		if excluded(cfg, rel) {
			continue
		}
		data, err := fs.ReadFile(fsys, rel)
		if err != nil {
			return docsTree{}, fmt.Errorf("reading %s: %w", rel, err)
		}
		switch {
		case isPage(rel):
			tree.pages = append(tree.pages, page{path: rel, data: data})
		case isReadme(rel):
			tree.readmes = append(tree.readmes, page{path: rel, data: data})
		}
	}
	return tree, nil
}

// walkFiles lists every file as a repository-relative slash path. It prunes
// hidden directories but .github, and node_modules; nothing docs.json
// excludes is pruned, so a page may cover a workflow or a record.
func walkFiles(fsys fs.FS) ([]string, error) {
	var found []string
	err := fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, walkErr error) error {
		switch {
		case walkErr != nil:
			return walkErr
		case d.IsDir():
			hidden := strings.HasPrefix(d.Name(), ".") && rel != ".github"
			if rel != "." && (hidden || d.Name() == "node_modules") {
				return fs.SkipDir
			}
		default:
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking the repository: %w", err)
	}
	return found, nil
}

// excluded reports whether docs.json puts rel outside the documentation
// system: an entry ending in a slash is a directory, any other an exact path.
func excluded(cfg docsconfig.Config, rel string) bool {
	for _, entry := range cfg.Excluded {
		if strings.HasSuffix(entry, "/") && strings.HasPrefix(rel, entry) || rel == entry {
			return true
		}
	}
	return false
}

func isPage(rel string) bool {
	return strings.HasPrefix(rel, docsPrefix) && strings.HasSuffix(rel, ".md") && path.Base(rel) != readmeName
}

func isReadme(rel string) bool {
	return path.Base(rel) == readmeName || !strings.Contains(rel, "/") && strings.HasSuffix(rel, ".md")
}

func parsePage(p page) (parsedPage, error) {
	meta, body, err := frontmatter.Parse(p.data)
	if err != nil {
		return parsedPage{}, err
	}
	return parsedPage{path: p.path, meta: meta, body: body}, nil
}

// parsedPages reports every page the parser refuses and returns the rest.
func parsedPages(t *testing.T, pages []page) []parsedPage {
	t.Helper()
	var parsed []parsedPage
	for _, p := range pages {
		pp, err := parsePage(p)
		if err != nil {
			t.Errorf("%s: %v", p.path, err)
			continue
		}
		parsed = append(parsed, pp)
	}
	return parsed
}

// pageProblems judges one page whole: its frontmatter, its heading, its
// place, its covers, its budget, its diagrams and its generated parts.
func pageProblems(cfg docsconfig.Config, files []string, runs []genRun, claims []blockClaim, p page) []string {
	pp, err := parsePage(p)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	problems = append(problems, describeProblems(cfg, files, pp)...)
	problems = append(problems, budgetProblems(cfg, pp)...)
	problems = append(problems, diagramProblems(files, pp)...)
	problems = append(problems, generatedProblems(files, runs, claims, pp)...)
	return problems
}

func TestPagesAreInOrder(t *testing.T) {
	tree := loadTree(t)
	runs, problems := docsGenRuns(readLines(t, repoFS(t), "Makefile"))
	for _, problem := range problems {
		t.Errorf("Makefile: %s", problem)
	}
	for _, p := range tree.pages {
		for _, problem := range pageProblems(tree.config, tree.files, runs, generatedBlocks, p) {
			t.Errorf("%s: %s", p.path, problem)
		}
	}
	for _, problem := range recipeProblems(runs, parsedPages(t, tree.pages), generatedBlocks) {
		t.Errorf("Makefile: %s", problem)
	}
	for _, p := range tree.readmes {
		for _, problem := range readmeProblems(tree.config, p) {
			t.Errorf("%s: %s", p.path, problem)
		}
	}
	paths := pagePaths(tree.pages)
	for _, problem := range directoryProblems(tree.config, paths) {
		t.Errorf("%s: %s", docsConfigPath, problem)
	}
	for _, problem := range pinProblems(tree.config, append(paths, pagePaths(tree.readmes)...)) {
		t.Errorf("%s: %s", docsConfigPath, problem)
	}
}

// The fixture tree the one-edit cases below are judged in. Paths spell no
// product name.
const fixtureConfig = `{
  "budgets": {"tutorial": 100, "how-to": 100, "explanation": 100, "reference": 100, "extending": 100},
  "exempt_types": ["spec", "project"],
  "readme": {"root": 20, "folder": 10},
  "ceilings": {"docs/reference/pinned.md": 150, "NOTES.md": 30},
  "directories": {
    "docs": {"type": "project"},
    "docs/guides": {"type": "how-to"},
    "docs/reference": {"type": "reference"},
    "docs/reference/deep": {"type": "reference"},
    "docs/spec": {"type": "spec", "may_be_empty": true},
    "docs/get-started": {"type": "tutorial"}
  },
  "page_types": {"docs/moved.md": "how-to"},
  "surfaces": ["internal/**"]
}`

var fixtureFiles = []string{
	"docs/guides/good.md", "docs/reference/pinned.md", "docs/moved.md", "docs/get-started/first.md",
	"internal/thing/a.go", "internal/thing/deep/b.go", "internal/other/c.go",
	"scripts/gen-thing.go", "cmd/tool/main.go", ".github/workflows/ci.yml",
}

const (
	goodPagePath = "docs/guides/good.md"
	goodHead     = "---\n" +
		"title: A good page\n" +
		"summary: One sentence.\n" +
		"type: how-to\n" +
		"covers: [internal/thing/**, cmd/tool/main.go]\n" +
		"---\n"
	goodPage = goodHead + "\n# A good page\n\nSome words here.\n"
)

func fixtureTree(t *testing.T) docsconfig.Config {
	t.Helper()
	cfg, err := docsconfig.Parse([]byte(fixtureConfig))
	if err != nil {
		t.Fatalf("the fixture configuration does not parse: %v", err)
	}
	return cfg
}

// edited is one edit to a correct page: the page text after the edit, and
// the reason the judge must give.
type edited struct {
	path, text string
	want       string
}

func judgeEdits(t *testing.T, cfg docsconfig.Config, files []string, runs []genRun, claims []blockClaim, cases map[string]edited) {
	t.Helper()
	for name, c := range cases {
		p := page{path: c.path, data: []byte(c.text)}
		problems := pageProblems(cfg, files, runs, claims, p)
		if c.want == "" {
			if len(problems) != 0 {
				t.Errorf("%s: a correct page reported %q", name, problems)
			}
			continue
		}
		if !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
}

// replaceOnce is the one edit each case makes; a fixture the edit does not
// touch would test the unedited page, so that is fatal.
func replaceOnce(t *testing.T, text, old, replacement string) string {
	t.Helper()
	if strings.Count(text, old) != 1 {
		t.Fatalf("the fixture holds %q %d times, want once", old, strings.Count(text, old))
	}
	return strings.Replace(text, old, replacement, 1)
}

func TestPageProblemsFrontmatter(t *testing.T) {
	cfg := fixtureTree(t)
	long := strings.Repeat("x", frontmatter.MaxSummary+1)
	cases := map[string]edited{
		"correct":              {goodPagePath, goodPage, ""},
		"no frontmatter":       {goodPagePath, "# A good page\n\nSome words.\n", `line 1 is not "---"`},
		"summary one too long": {goodPagePath, replaceOnce(t, goodPage, "One sentence.", long), fmt.Sprintf("summary is %d characters", frontmatter.MaxSummary+1)},
		"stability on a how-to": {goodPagePath, replaceOnce(t, goodPage, "---\n\n#",
			"stability: alpha\n---\n\n#"), "stability is declared by spec and extending pages"},
		"title is not the heading": {goodPagePath, replaceOnce(t, goodPage, "# A good page", "# Another heading"),
			`title "A good page" is not the heading "Another heading"`},
		"two headings": {goodPagePath, goodPage + "\n# Second\n", "2 top-level headings"},
		"no heading":   {goodPagePath, replaceOnce(t, goodPage, "# A good page\n", "## A good page\n"), "0 top-level headings"},
		"a heading inside a fence is not a heading": {goodPagePath, goodPage + "\n```sh\n# a comment\n```\n", ""},
		"type is not the directory's":               {goodPagePath, replaceOnce(t, goodPage, "type: how-to", "type: reference"), `type "reference" is not the "how-to" of docs/guides`},
		"page_types overrides the directory":        {"docs/moved.md", goodPage, ""},
		"page_types override not honoured":          {"docs/moved.md", replaceOnce(t, goodPage, "type: how-to", "type: project"), `type "project" is not the "how-to" page_types names`},
		"a listed own directory is accepted":        {"docs/reference/deep/x.md", replaceOnce(t, goodPage, "type: how-to", "type: reference"), ""},
		"an unlisted own directory under a listed parent is refused": {"docs/reference/deep/deeper/x.md", replaceOnce(t, goodPage, "type: how-to", "type: reference"),
			"no directory in docs.json holds the page: docs/reference/deep/deeper is not listed"},
		"a page in no directory":                    {"docs/nowhere/x.md", goodPage, "no directory in docs.json holds"},
		"a covers glob on a workflow":               {goodPagePath, replaceOnce(t, goodPage, "cmd/tool/main.go", ".github/**"), ""},
		"a covers glob matching nothing":            {goodPagePath, replaceOnce(t, goodPage, "cmd/tool/main.go", "cmd/none/**"), `covers "cmd/none/**" matches no file`},
		"a covers glob matching one file only deep": {goodPagePath, replaceOnce(t, goodPage, "cmd/tool/main.go", "internal/**/b.go"), ""},
		"a covers glob that is a bad pattern":       {goodPagePath, replaceOnce(t, goodPage, "cmd/tool/main.go", `cmd/tool/main.go\`), `covers "cmd/tool/main.go\\" matches no file`},
	}
	judgeEdits(t, cfg, fixtureFiles, nil, nil, cases)
}

// The walk answers two questions from two lists: excluded selects what is
// judged, and the file list a covers glob or a Sources path is matched
// against holds every file but hidden directories and node_modules.
func TestCollectTreeSeparatesJudgedFromCovered(t *testing.T) {
	cfg, err := docsconfig.Parse([]byte(replaceOnce(t, fixtureConfig, `"page_types"`,
		`"excluded": [".github/", "docs/adr/", "CHANGELOG.md", "docs/plans/"], "page_types"`)))
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	for _, rel := range []string{
		".github/workflows/ci.yml", ".github/README.md", ".hidden/x.md", "node_modules/m/index.js",
		"docs/adr/0001-record.md", "docs/plans/p.md", "CHANGELOG.md", "docs/index.md", "docs/guides/g.md",
		"README.md", "bench/README.md", "internal/thing/a.go",
	} {
		fsys[rel] = &fstest.MapFile{Data: []byte("# x\n")}
	}
	tree, err := collectTree(fsys, cfg)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{
		".github/README.md", ".github/workflows/ci.yml", "CHANGELOG.md", "README.md", "bench/README.md",
		"docs/adr/0001-record.md", "docs/guides/g.md", "docs/index.md", "docs/plans/p.md", "internal/thing/a.go",
	}
	if !slices.Equal(tree.files, wantFiles) {
		t.Errorf("files = %q, want %q", tree.files, wantFiles)
	}
	if want := []string{"docs/guides/g.md", "docs/index.md"}; !slices.Equal(pagePaths(tree.pages), want) {
		t.Errorf("pages = %q, want %q", pagePaths(tree.pages), want)
	}
	if want := []string{"README.md", "bench/README.md"}; !slices.Equal(pagePaths(tree.readmes), want) {
		t.Errorf("readmes = %q, want %q", pagePaths(tree.readmes), want)
	}
}

func pagePaths(pages []page) []string {
	paths := make([]string, 0, len(pages))
	for _, p := range pages {
		paths = append(paths, p.path)
	}
	return paths
}
