package docscheck

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
)

const fixtureDir = "testdata/pages"

// fixturePage reads one page of testdata/pages, judged at a path under docs/.
func fixturePage(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(os.DirFS(fixtureDir), name)
	if err != nil {
		t.Fatalf("reading %s/%s: %v", fixtureDir, name, err)
	}
	return string(data)
}

// withoutLastDiagramLine drops the last line of the page's one diagram, the
// line that introduces its sixteenth node.
func withoutLastDiagramLine(t *testing.T, text string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	end := slices.Index(lines, "```")
	if end < 0 || !strings.HasPrefix(lines[end-1], "    ") {
		t.Fatal("the fixture has no diagram line before its closing fence")
	}
	return strings.Join(slices.Delete(lines, end-1, end), "\n")
}

const goodDiagram = "\n```mermaid\nflowchart TD\n    A --> B\n```\n\nSources: `internal/thing/a.go`.\n"

func TestPageProblemsDiagrams(t *testing.T) {
	cfg := fixtureTree(t)
	cases := map[string]edited{
		"a diagram with its sources": {goodPagePath, goodPage + goodDiagram, ""},
		"sources wrapped over lines": {goodPagePath, goodPage +
			"\n```mermaid\nflowchart TD\n    A --> B\n```\n\nSources: `internal/thing/a.go`,\n`internal/thing/deep/b.go`.\n", ""},
		"a direction is optional": {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "flowchart TD", "flowchart"), ""},
		"an unknown kind":         {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "flowchart TD", "graph TD"), `"graph TD" is not flowchart, sequenceDiagram or stateDiagram-v2`},
		"an empty diagram":        {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "flowchart TD\n    A --> B\n", ""), "the diagram is empty"},
		"no closing fence":        {goodPagePath, goodPage + "\n```mermaid\nflowchart TD\n    A --> B\n", "has no closing fence"},
		"no sources":              {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "Sources: `internal/thing/a.go`.", "Some prose."), "is not a Sources: line"},
		"nothing after the fence": {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "\nSources: `internal/thing/a.go`.\n", ""), "is not a Sources: line"},
		"a source not backticked": {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "`internal/thing/a.go`", "internal/thing/a.go"), "is not one backticked path"},
		"a source that is no file": {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "internal/thing/a.go", "internal/thing/none.go"),
			"source internal/thing/none.go is no file of the walk"},
		"a source outside covers": {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "internal/thing/a.go", "internal/other/c.go"),
			"source internal/other/c.go is outside the page's covers"},
		"a flowchart line the counter cannot read": {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "A --> B", "A => B"), "cannot read"},
		"a flowchart line naming no node":          {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "A --> B", "(a) --> (b)"), `line "(a) --> (b)" names no node`},
		"a sequence line the counter cannot read": {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "flowchart TD\n    A --> B",
			"sequenceDiagram\n    A->>B: one\n    B: two"), "cannot read"},
		"a state line the counter cannot read": {goodPagePath, replaceOnce(t, goodPage+goodDiagram, "flowchart TD\n    A --> B",
			"stateDiagram-v2\n    A --> B\n    state \"only text\""), "cannot read"},
		"a second diagram is judged too": {goodPagePath, goodPage + goodDiagram + replaceOnce(t, goodDiagram, "flowchart TD", "graph TD"), `"graph TD" is not`},
	}
	for _, name := range []string{"flowchart-16.md", "sequence-16.md", "state-16.md"} {
		text := fixturePage(t, name)
		cases[name+" with sixteen nodes"] = edited{goodPagePath, text, "holds 16 nodes, the most is 15"}
		cases[name+" with fifteen nodes"] = edited{goodPagePath, withoutLastDiagramLine(t, text), ""}
	}
	judgeEdits(t, cfg, fixtureFiles, nil, nil, cases)
}

func TestCountNodes(t *testing.T) {
	for name, c := range map[string]struct {
		lines []string
		want  int
	}{
		"flowchart nodes only in edges":   {[]string{"flowchart LR", "A --> B", "B --> C"}, 3},
		"flowchart node used twice":       {[]string{"flowchart LR", "A[One] --> B", "A --> C"}, 3},
		"flowchart subgraph is a frame":   {[]string{"flowchart TB", "subgraph S[Group]", "A --> B", "end", "B --> C"}, 3},
		"flowchart quoted label":          {[]string{"flowchart LR", `A["a --> b"] --> B`}, 2},
		"flowchart link with text":        {[]string{"flowchart LR", "A -- yes --> B", "B -. no .-> C", "C == so ==> D"}, 4},
		"sequence declared and used":      {[]string{"sequenceDiagram", "participant A as First", "A->>B: hi", "B-->>A: hello"}, 2},
		"sequence note names no node":     {[]string{"sequenceDiagram", "A->>B: hi", "Note over A,B: text"}, 2},
		"state pseudo state is not one":   {[]string{"stateDiagram-v2", "[*] --> A", "A --> [*]"}, 1},
		"state declared and described":    {[]string{"stateDiagram-v2", `state "text" as A`, "B: text", "state C", "A --> B"}, 3},
		"comments and blanks are skipped": {[]string{"flowchart LR", "%% a comment", "", "A --> B"}, 2},
	} {
		got, err := countNodes(c.lines)
		if err != nil || got != c.want {
			t.Errorf("%s: countNodes = %d, %v; want %d", name, got, err, c.want)
		}
	}
}

const goodBlock = "\n<!-- generated: scripts/gen-thing.go -->\nrows\n<!-- /generated -->\n"

func TestPageProblemsGenerated(t *testing.T) {
	cfg := fixtureTree(t)
	claims := []blockClaim{{goodPagePath, "scripts/gen-thing.go"}}
	runs := []genRun{{"scripts/gen-thing.go", goodPagePath}}
	script := replaceOnce(t, goodPage, "---\n\n#", "generated: scripts/gen-thing.go\n---\n\n#")
	pin := replaceOnce(t, goodPage, "---\n\n#", "generated: go test ./cmd/tool -run TestPage\n---\n\n#")
	cases := map[string]edited{
		"a rendered page and its run":     {goodPagePath, script, ""},
		"a generator naming no file":      {goodPagePath, replaceOnce(t, script, "gen-thing.go", "gen-none.go"), "generated scripts/gen-none.go names no file"},
		"a generator docs-gen never runs": {"docs/guides/other.md", script, "the docs-gen recipe does not run scripts/gen-thing.go -o docs/guides/other.md"},
		"a test pin on a package":         {goodPagePath, pin, ""},
		"a test pin on no package":        {goodPagePath, replaceOnce(t, pin, "./cmd/tool", "./cmd/none"), "names package cmd/none, which holds no file"},
		"a test pin of another shape":     {goodPagePath, replaceOnce(t, pin, "-run TestPage", "-v"), "is not `go test <pkg> -run <Test>`"},
		"a claimed block":                 {goodPagePath, goodPage + goodBlock, ""},
		"an unclaimed block":              {goodPagePath, replaceOnce(t, goodPage+goodBlock, "gen-thing.go", "gen-other.go"), "generated block scripts/gen-other.go is claimed by nobody"},
		"a block on another page":         {"docs/guides/other.md", goodPage + goodBlock, "is claimed by nobody"},
		"a block never closed":            {goodPagePath, replaceOnce(t, goodPage+goodBlock, "<!-- /generated -->\n", ""), "a generated block is never closed"},
		"a close with no open":            {goodPagePath, replaceOnce(t, goodPage+goodBlock, "<!-- generated: scripts/gen-thing.go -->\n", ""), "closes and none is open"},
		"a nested block":                  {goodPagePath, replaceOnce(t, goodPage+goodBlock, "rows\n", "<!-- generated: scripts/gen-thing.go -->\nrows\n"), "opens inside another"},
		"a marker naming nothing":         {goodPagePath, replaceOnce(t, goodPage+goodBlock, "generated: scripts/gen-thing.go -->", "generated:  -->"), "does not name a generator"},
		"a marker inside a fence":         {goodPagePath, goodPage + "\n```\n<!-- generated: scripts/gen-other.go -->\n```\n", ""},
	}
	judgeEdits(t, cfg, fixtureFiles, runs, claims, cases)
}

// TestEveryClaimOpensItsBlock holds the registry to the pages: a claim on a
// page of the walk that opens no block of that generator is refused, so the
// registry cannot pre-approve a block nobody has drawn.
func TestEveryClaimOpensItsBlock(t *testing.T) {
	if len(generatedBlocks) == 0 {
		t.Fatal("the registry claims nothing, so nothing would be judged")
	}
	tree := loadTree(t)
	for _, problem := range claimProblems(generatedBlocks, parsedPages(t, tree.pages)) {
		t.Errorf("generatedBlocks: %s", problem)
	}
}

func TestClaimProblems(t *testing.T) {
	claims := []blockClaim{{"docs/b.md", "scripts/gen-b.go"}, {"docs/c.md", "go test ./cmd/tool -run TestPage"}}
	blocked := parsedPage{path: "docs/b.md", body: []byte("text\n<!-- generated: scripts/gen-b.go -->\nrows\n<!-- /generated -->\n")}
	pinned := parsedPage{path: "docs/c.md", body: []byte("text\n<!-- generated: go test ./cmd/tool -run TestPage -->\n```\nusage\n```\n<!-- /generated -->\n")}
	plain := parsedPage{path: "docs/d.md", body: []byte("text\n")}
	pages := []parsedPage{blocked, pinned, plain}
	if problems := claimProblems(claims, pages); len(problems) != 0 {
		t.Fatalf("a correct registry reported %q", problems)
	}
	fencedOnly := parsedPage{path: "docs/b.md", body: []byte("```\n<!-- generated: scripts/gen-b.go -->\n```\n")}
	for name, c := range map[string]struct {
		claim blockClaim
		pages []parsedPage
		want  string
	}{
		"a claim on no page of the walk":           {blockClaim{"docs/none.md", "scripts/gen-b.go"}, pages, "docs/none.md, which is no page of the walk"},
		"a claim on a page with no block":          {blockClaim{"docs/d.md", "go test ./cmd/tool -run TestPage"}, pages, "on docs/d.md and the page opens no such block"},
		"a claim naming another generator":         {blockClaim{"docs/b.md", "scripts/gen-c.go"}, pages, "a block of scripts/gen-c.go on docs/b.md and the page opens no such block"},
		"a claim whose marker sits inside a fence": {blockClaim{"docs/b.md", "scripts/gen-b.go"}, []parsedPage{fencedOnly}, "opens no such block"},
	} {
		if problems := claimProblems([]blockClaim{c.claim}, c.pages); !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
}

func TestDocsGenRuns(t *testing.T) {
	makefile := func(recipe ...string) []string {
		return append(append([]string{"other:", "\techo", "", "docs-gen:"}, recipe...), "", "next:", "\techo")
	}
	runs, problems := docsGenRuns(makefile("\t$(CD) $(GO) run scripts/gen-a.go -o docs/a.md", "\t$(GO) run scripts/gen-b.go \\", "\t\t-o docs/b.md"))
	if len(problems) != 0 || !slices.Equal(runs, []genRun{{"scripts/gen-a.go", "docs/a.md"}, {"scripts/gen-b.go", "docs/b.md"}}) {
		t.Errorf("docsGenRuns = %v, %q", runs, problems)
	}
	for name, c := range map[string]struct {
		lines []string
		want  string
	}{
		"no rule":               {[]string{"other:", "\techo"}, "found 0 rule(s) for docs-gen"},
		"two rules":             {append(makefile("\tgo run scripts/gen-a.go -o docs/a.md"), "docs-gen:"), "found 2 rule(s)"},
		"an empty recipe":       {makefile(), "docs-gen runs no generator"},
		"a run with no -o":      {makefile("\tgo run scripts/gen-a.go"), "runs no scripts/gen-<x>.go -o <page>"},
		"a line of other shape": {makefile("\tgo run scripts/gen-a.go -o docs/a.md", "\t@echo done"), "runs no scripts/gen-<x>.go -o <page>"},
	} {
		if _, problems := docsGenRuns(c.lines); !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
}

func TestRecipeProblems(t *testing.T) {
	claims := []blockClaim{{"docs/b.md", "scripts/gen-b.go"}, {"docs/c.md", "scripts/gen-b.go"}}
	rendered := parsedPage{path: "docs/a.md"}
	rendered.meta.Generated = "scripts/gen-a.go"
	blocked := parsedPage{path: "docs/b.md", body: []byte("text\n<!-- generated: scripts/gen-b.go -->\nrows\n<!-- /generated -->\n")}
	plain := parsedPage{path: "docs/c.md", body: []byte("text\n")}
	pages := []parsedPage{rendered, blocked, plain}
	if problems := recipeProblems([]genRun{{"scripts/gen-a.go", "docs/a.md"}, {"scripts/gen-b.go", "docs/b.md"}}, pages, claims); len(problems) != 0 {
		t.Fatalf("a correct recipe reported %q", problems)
	}
	for name, c := range map[string]struct {
		run  genRun
		want string
	}{
		"a run landing on no page":                {genRun{"scripts/gen-a.go", "docs/none.md"}, "which is no page of the walk"},
		"a run landing on a page naming another":  {genRun{"scripts/gen-b.go", "docs/a.md"}, "the page does not name it"},
		"a run landing on a plain page":           {genRun{"scripts/gen-a.go", "docs/c.md"}, "the page does not name it"},
		"a run claimed for a page without marker": {genRun{"scripts/gen-b.go", "docs/c.md"}, "the page does not name it"},
	} {
		if problems := recipeProblems([]genRun{c.run}, pages, claims); !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
}
