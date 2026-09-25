package docscheck

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/docscheck/docsconfig"
	"github.com/guardana/control/internal/docscheck/frontmatter"
)

// describeProblems judges the frontmatter against the page and the tree:
// the title is the one top-level heading, the type is the one the page's
// directory or its page_types entry declares, and every covers glob matches
// a file of the walk.
func describeProblems(cfg docsconfig.Config, files []string, p parsedPage) []string {
	var problems []string
	headings := topHeadings(p.body)
	switch {
	case len(headings) != 1:
		problems = append(problems, fmt.Sprintf("holds %d top-level headings, want exactly one", len(headings)))
	case headings[0] != p.meta.Title:
		problems = append(problems, fmt.Sprintf("title %q is not the heading %q", p.meta.Title, headings[0]))
	}
	if problem := typeProblem(cfg, p.path, p.meta.Type); problem != "" {
		problems = append(problems, problem)
	}
	for _, glob := range p.meta.Covers {
		if !slices.ContainsFunc(files, func(f string) bool { return matchGlob(glob, f) }) {
			problems = append(problems, fmt.Sprintf("covers %q matches no file", glob))
		}
	}
	return problems
}

// topHeadings returns the text of every "# " line outside fenced code.
func topHeadings(body []byte) []string {
	var headings []string
	fenced := false
	for _, line := range strings.Split(string(body), "\n") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "```"):
			fenced = !fenced
		case !fenced && strings.HasPrefix(line, "# "):
			headings = append(headings, strings.TrimSpace(line[2:]))
		}
	}
	return headings
}

// typeProblem compares the declared type with the one docs.json assigns:
// page_types for the page itself, else the page's own directory, which must
// be listed.
func typeProblem(cfg docsconfig.Config, rel, typ string) string {
	if want, ok := cfg.PageTypes[rel]; ok {
		if typ != want {
			return fmt.Sprintf("type %q is not the %q page_types names", typ, want)
		}
		return ""
	}
	dir := path.Dir(rel)
	d, ok := cfg.Directories[dir]
	switch {
	case !ok:
		return fmt.Sprintf("no directory in docs.json holds the page: %s is not listed", dir)
	case typ != d.Type:
		return fmt.Sprintf("type %q is not the %q of %s", typ, d.Type, dir)
	}
	return ""
}

// matchGlob reports whether a covers glob matches a path: ** stands for any
// number of segments, and every other segment follows path.Match. A pattern
// path.Match cannot read matches nothing.
func matchGlob(glob, p string) bool {
	return matchSegments(strings.Split(glob, "/"), strings.Split(p, "/"))
}

func matchSegments(pattern, segments []string) bool {
	if len(pattern) == 0 {
		return len(segments) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(segments); i++ {
			if matchSegments(pattern[1:], segments[i:]) {
				return true
			}
		}
		return false
	}
	if len(segments) == 0 {
		return false
	}
	ok, err := path.Match(pattern[0], segments[0])
	return err == nil && ok && matchSegments(pattern[1:], segments[1:])
}

func TestMatchGlob(t *testing.T) {
	for _, c := range []struct {
		glob, p string
		want    bool
	}{
		{"a/**", "a/b/c.go", true},
		{"a/**", "a/c.go", true},
		{"a/**", "ab/c.go", false},
		{"a/**/z.go", "a/z.go", true},
		{"a/**/z.go", "a/b/c/z.go", true},
		{"a/*.go", "a/b/c.go", false},
		{"a/*.go", "a/c.go", true},
		{"a/c.go", "a/c.go", true},
		{"a/c.go", "a/c.gox", false},
		{"**", "any/thing", true},
	} {
		if got := matchGlob(c.glob, c.p); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.glob, c.p, got, c.want)
		}
	}
}

// budgetProblems holds a page to its type's budget, or to its ceiling when
// docs.json pins it. An exempt type and a generated page have no budget.
func budgetProblems(cfg docsconfig.Config, p parsedPage) []string {
	budget, budgeted := cfg.Budgets[p.meta.Type]
	if !budgeted || p.meta.Generated != "" {
		budget = 0
	}
	return wordProblems(cfg.Ceilings, p.path, frontmatter.Words(p.body), budget)
}

// readmeProblems holds a README.md or a root Markdown file, which carries
// no frontmatter, to the readme budget of its place.
func readmeProblems(cfg docsconfig.Config, p page) []string {
	budget := cfg.Readme.Folder
	if !strings.Contains(p.path, "/") {
		budget = cfg.Readme.Root
	}
	return wordProblems(cfg.Ceilings, p.path, frontmatter.Words(p.data), budget)
}

// wordProblems applies one budget, zero meaning none, and the ceiling if
// any: a pinned page holds exactly its ceiling, which must exceed the budget.
func wordProblems(ceilings map[string]int, rel string, words, budget int) []string {
	ceiling, pinned := ceilings[rel]
	switch {
	case pinned && budget == 0:
		return []string{fmt.Sprintf("a ceiling of %d on a page with no budget", ceiling)}
	case pinned && ceiling <= budget:
		return []string{fmt.Sprintf("ceiling %d is at or under the budget %d", ceiling, budget)}
	case pinned && words != ceiling:
		return []string{fmt.Sprintf("holds %d words, the ceiling pins %d", words, ceiling)}
	case !pinned && budget > 0 && words > budget:
		return []string{fmt.Sprintf("holds %d words, the budget is %d", words, budget)}
	}
	return nil
}

// directoryProblems names every directory of docs.json that must hold a
// page and directly holds none.
func directoryProblems(cfg docsconfig.Config, pages []string) []string {
	var problems []string
	dirs := make([]string, 0, len(cfg.Directories))
	for dir := range cfg.Directories {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		if cfg.Directories[dir].MayBeEmpty {
			continue
		}
		if !slices.ContainsFunc(pages, func(p string) bool { return path.Dir(p) == dir }) {
			problems = append(problems, fmt.Sprintf("directory %s holds no page and may not be empty", dir))
		}
	}
	return problems
}

// pinProblems names every ceiling and every page_types entry of docs.json
// that pins a page the walk did not judge: a pin on no page holds nothing.
func pinProblems(cfg docsconfig.Config, judged []string) []string {
	var problems []string
	for _, rel := range sortedKeys(cfg.Ceilings) {
		if !slices.Contains(judged, rel) {
			problems = append(problems, fmt.Sprintf("ceilings pins %s, which the walk did not judge", rel))
		}
	}
	for _, rel := range sortedKeys(cfg.PageTypes) {
		if !slices.Contains(judged, rel) {
			problems = append(problems, fmt.Sprintf("page_types names %s, which the walk did not judge", rel))
		}
	}
	return problems
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func TestPinProblems(t *testing.T) {
	judged := []string{"docs/reference/pinned.md", "NOTES.md", "docs/moved.md", "docs/guides/good.md"}
	if problems := pinProblems(fixtureTree(t), judged); len(problems) != 0 {
		t.Fatalf("the fixture's pins reported %q", problems)
	}
	for name, c := range map[string]struct{ old, repl, want string }{
		"a ceiling on a page the walk lacks":      {`"docs/reference/pinned.md": 150`, `"docs/reference/nothing.md": 150`, "ceilings pins docs/reference/nothing.md, which the walk did not judge"},
		"a ceiling on a root file the walk lacks": {`"NOTES.md": 30`, `"GONE.md": 30`, "ceilings pins GONE.md, which the walk did not judge"},
		"a page type on a page the walk lacks":    {`"docs/moved.md": "how-to"`, `"docs/nothing.md": "how-to"`, "page_types names docs/nothing.md, which the walk did not judge"},
	} {
		cfg, err := docsconfig.Parse([]byte(replaceOnce(t, fixtureConfig, c.old, c.repl)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		problems := pinProblems(cfg, judged)
		if !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
	if problems := pinProblems(fixtureTree(t), nil); len(problems) != 3 {
		t.Errorf("over no judged page, %d problems, want one per pin (3): %q", len(problems), problems)
	}
}

// wordsPage is a page whose body holds exactly n words.
func wordsPage(head string, n int) string {
	return head + "\n# Title\n\n" + strings.Repeat("word ", n-1) + "\n"
}

func TestPageProblemsBudget(t *testing.T) {
	cfg := fixtureTree(t)
	pinnedHead := replaceOnce(t, replaceOnce(t, goodHead, "type: how-to", "type: reference"), "A good page", "Title")
	head := replaceOnce(t, goodHead, "A good page", "Title")
	generatedHead := strings.TrimSuffix(head, "---\n") + "generated: scripts/gen-thing.go\n---\n"
	cases := map[string]edited{
		"at the budget":       {goodPagePath, wordsPage(head, 100), ""},
		"one over the budget": {goodPagePath, wordsPage(head, 101), "holds 101 words, the budget is 100"},
		"a fence is outside the budget": {goodPagePath, wordsPage(head, 100) +
			"\n```\n" + strings.Repeat("word ", 50) + "\n```\n", ""},
		"a generated block is outside the budget": {goodPagePath, wordsPage(head, 100) +
			"\n<!-- generated: scripts/gen-thing.go -->\n" + strings.Repeat("word ", 50) + "\n<!-- /generated -->\n", ""},
		"an exempt type over every budget": {"docs/exempt.md", wordsPage(replaceOnce(t, head, "type: how-to", "type: project"), 500), ""},
		"a generated page over its budget": {goodPagePath, wordsPage(generatedHead, 500), ""},
		"at the ceiling":                   {"docs/reference/pinned.md", wordsPage(pinnedHead, 150), ""},
		"one under the ceiling":            {"docs/reference/pinned.md", wordsPage(pinnedHead, 149), "holds 149 words, the ceiling pins 150"},
		"one over the ceiling":             {"docs/reference/pinned.md", wordsPage(pinnedHead, 151), "holds 151 words, the ceiling pins 150"},
	}
	claims := []blockClaim{{goodPagePath, "scripts/gen-thing.go"}}
	runs := []genRun{{"scripts/gen-thing.go", goodPagePath}}
	judgeEdits(t, cfg, fixtureFiles, runs, claims, cases)

	for name, c := range map[string]struct{ ceiling, want string }{
		"a ceiling at the budget":    {"100", "ceiling 100 is at or under the budget 100"},
		"a ceiling under the budget": {"99", "ceiling 99 is at or under the budget 100"},
		"a ceiling over the budget":  {"101", ""},
	} {
		altered, err := docsconfig.Parse([]byte(replaceOnce(t, fixtureConfig, `"docs/reference/pinned.md": 150`, `"docs/reference/pinned.md": `+c.ceiling)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		n := 101
		if c.want != "" {
			n = 100
		}
		judgeEdits(t, altered, fixtureFiles, nil, nil, map[string]edited{name: {"docs/reference/pinned.md", wordsPage(pinnedHead, n), c.want}})
	}
	exempt, err := docsconfig.Parse([]byte(replaceOnce(t, fixtureConfig, `"docs/reference/pinned.md": 150`, `"docs/exempt.md": 150`)))
	if err != nil {
		t.Fatal(err)
	}
	judgeEdits(t, exempt, fixtureFiles, nil, nil, map[string]edited{
		"a ceiling on an exempt page": {"docs/exempt.md", wordsPage(replaceOnce(t, head, "type: how-to", "type: project"), 150), "a ceiling of 150 on a page with no budget"},
	})
}

func TestReadmeProblems(t *testing.T) {
	cfg := fixtureTree(t)
	text := func(n int) []byte { return []byte("# Title\n\n" + strings.Repeat("word ", n-1) + "\n") }
	for name, c := range map[string]struct {
		path string
		n    int
		want string
	}{
		"root at the budget":               {"README.md", 20, ""},
		"root one over":                    {"README.md", 21, "holds 21 words, the budget is 20"},
		"a root file that is not a readme": {"SECURITY.md", 21, "holds 21 words, the budget is 20"},
		"folder at the budget":             {"bench/README.md", 10, ""},
		"folder one over":                  {"bench/README.md", 11, "holds 11 words, the budget is 10"},
		"a root file at its ceiling":       {"NOTES.md", 30, ""},
		"a root file one under":            {"NOTES.md", 29, "holds 29 words, the ceiling pins 30"},
		"a root file one over":             {"NOTES.md", 31, "holds 31 words, the ceiling pins 30"},
	} {
		problems := readmeProblems(cfg, page{path: c.path, data: text(c.n)})
		if c.want == "" && len(problems) != 0 {
			t.Errorf("%s: a correct file reported %q", name, problems)
		}
		if c.want != "" && !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
}

func TestDirectoryProblems(t *testing.T) {
	cfg := fixtureTree(t)
	full := []string{"docs/a.md", "docs/guides/b.md", "docs/reference/c.md", "docs/reference/deep/d.md", "docs/get-started/e.md"}
	if problems := directoryProblems(cfg, full); len(problems) != 0 {
		t.Fatalf("a full tree reported %q", problems)
	}
	for name, c := range map[string]struct {
		pages []string
		want  string
	}{
		"a directory that must hold a page holds none": {slices.Delete(slices.Clone(full), 4, 5), "directory docs/get-started holds no page"},
		"a nested page does not fill its parent":       {[]string{"docs/a.md", "docs/guides/b.md", "docs/reference/deep/d.md", "docs/get-started/e.md"}, "directory docs/reference holds no page"},
		"a directory that may be empty":                {full, ""},
		"no pages at all":                              {nil, "directory docs holds no page"},
	} {
		problems := directoryProblems(cfg, c.pages)
		if c.want == "" && len(problems) != 0 {
			t.Errorf("%s: reported %q", name, problems)
		}
		if c.want != "" && !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
}
