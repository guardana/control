package docscheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policy/reasons"
)

// The first column of the refusal table holds one row per exported sentinel
// error of pkg/contract, and this row for a decode failure that carries none.
const decodeFailureRow = "a decode failure carrying no sentinel"

// The sentinels pkg/contract declares today. The expected rows come from the
// package's source, so a new sentinel needs a row on the page and no change
// here; this list only proves the source reader still finds what is there.
var knownSentinels = []string{
	"ErrUnsupportedSchema", "ErrUnknownField", "ErrInvalidEnum",
	"ErrMissingField", "ErrTooLarge", "ErrInvalidValue",
}

var (
	backticked     = regexp.MustCompile("^`([^`]+)`$")
	reasonCodeName = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)
)

// The refusal table on docs/contracts.md is where an integrator learns what a
// refusal means, and the registry is where the meaning lives. Nothing else ties
// the two together, so without this test a row could be deleted, a code left
// unmarked, or a verdict flipped to ALLOW with every gate still green.
func TestRefusalTableMatchesTheReasonRegistry(t *testing.T) {
	fsys := repoFS(t)
	sentinels := contractSentinels(t, fsys)
	for _, want := range knownSentinels {
		if !slices.Contains(sentinels, want) {
			t.Errorf("pkg/contract: the source reader did not find %s", want)
		}
	}
	rows := tableRowsUnder(t, "docs/contracts.md", "## Refusals", readLines(t, fsys, "docs/contracts.md"))
	for _, problem := range refusalProblems(rows, sentinels, registryVerdict) {
		t.Errorf("docs/contracts.md: %s", problem)
	}
}

func registryVerdict(code string) (string, bool) {
	c, ok := reasons.Lookup(code)
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(c.Verdict.String(), "VERDICT_"), true
}

// refusalProblems checks the table's body rows, keyed by line index. Each row
// names one refusal in its first cell, a backticked verdict in its second and
// exactly one backticked, registered reason code in its third, whose registry
// verdict is the documented one; and each refusal has exactly one row.
func refusalProblems(rows map[int]string, sentinels []string, verdictOf func(string) (string, bool)) []string {
	count := map[string]int{decodeFailureRow: 0}
	for _, sentinel := range sentinels {
		count["`"+sentinel+"`"] = 0
	}

	var problems []string
	lines := make([]int, 0, len(rows))
	for i := range rows {
		lines = append(lines, i)
	}
	slices.Sort(lines)
	for _, i := range lines {
		cells := tableCells(rows[i])
		if len(cells) != 3 {
			problems = append(problems, fmt.Sprintf("line %d: row has %d cells, want 3", i+1, len(cells)))
			continue
		}
		if _, known := count[cells[0]]; !known {
			problems = append(problems, fmt.Sprintf("line %d: %s is neither a pkg/contract sentinel nor %q", i+1, cells[0], decodeFailureRow))
		} else {
			count[cells[0]]++
		}
		if problem := rowCodeProblem(cells, verdictOf); problem != "" {
			problems = append(problems, fmt.Sprintf("line %d: %s", i+1, problem))
		}
	}

	refusals := make([]string, 0, len(count))
	for refusal := range count {
		refusals = append(refusals, refusal)
	}
	slices.Sort(refusals)
	for _, refusal := range refusals {
		if n := count[refusal]; n != 1 {
			problems = append(problems, fmt.Sprintf("%s has %d row(s), want 1", refusal, n))
		}
	}
	return problems
}

// rowCodeProblem checks the verdict and reason code cells of one row.
func rowCodeProblem(cells []string, verdictOf func(string) (string, bool)) string {
	code := backticked.FindStringSubmatch(cells[2])
	if code == nil || !reasonCodeName.MatchString(code[1]) {
		return fmt.Sprintf("reason code cell %q is not exactly one backticked code", cells[2])
	}
	verdict := backticked.FindStringSubmatch(cells[1])
	if verdict == nil {
		return fmt.Sprintf("verdict cell %q is not one backticked verdict", cells[1])
	}
	registered, ok := verdictOf(code[1])
	switch {
	case !ok:
		return fmt.Sprintf("reason code %s is not in the registry", code[1])
	case registered != verdict[1]:
		return fmt.Sprintf("%s is documented as %s, the registry declares %s", code[1], verdict[1], registered)
	}
	return ""
}

// tableRowsUnder returns the body rows of the pipe table under the heading,
// keyed by line index, skipping its header and separator. A section with no
// table is fatal, so no check over its rows can pass over nothing.
func tableRowsUnder(t *testing.T, page, heading string, lines []string) map[int]string {
	t.Helper()

	rows := make(map[int]string)
	in, seenSeparator := false, false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if in {
				break // the section ended at the next heading
			}
			in = trimmed == heading
			continue
		}
		switch {
		case !in || !strings.HasPrefix(trimmed, "|"):
		case tableSeparator.MatchString(line):
			seenSeparator = true
		case seenSeparator:
			rows[i] = line
		}
	}
	if !seenSeparator {
		t.Fatalf("%s: no table found under %q; the section moved or was deleted", page, heading)
	}
	return rows
}

// contractSentinels returns the exported package-level variables of
// pkg/contract whose names start with Err, read from its non-test source.
func contractSentinels(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	files, err := fs.Glob(fsys, "pkg/contract/*.go")
	if err != nil {
		t.Fatalf("listing pkg/contract: %v", err)
	}
	fset := token.NewFileSet()
	var found []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		found = append(found, sentinelsIn(file)...)
	}
	if len(found) == 0 {
		t.Fatal("pkg/contract: no exported Err variable found, so the refusal table would be compared with nothing")
	}
	slices.Sort(found)
	return found
}

func sentinelsIn(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, id := range value.Names {
				if id.IsExported() && strings.HasPrefix(id.Name, "Err") {
					names = append(names, id.Name)
				}
			}
		}
	}
	return names
}

func TestSentinelsIn(t *testing.T) {
	src := "package p\n\nimport \"errors\"\n\nvar ErrA = errors.New(\"a\")\n\nvar (\n\tErrB = errors.New(\"b\")\n\terrC = errors.New(\"c\")\n\tOther = 1\n)\n\nconst ErrD = \"d\"\n"
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if got := sentinelsIn(file); !slices.Equal(got, []string{"ErrA", "ErrB"}) {
		t.Errorf("sentinelsIn = %q, want [ErrA ErrB]", got)
	}
}

// Each case is one edit to a correct table, among them the two a reviewer made
// to the real page and saw pass: a verdict flipped to ALLOW with its code left
// unmarked, and two rows deleted.
func TestRefusalProblems(t *testing.T) {
	sentinels := []string{"ErrA", "ErrB"}
	registry := map[string]string{"CODE_A": "INDETERMINATE", "CODE_B": "INDETERMINATE", "CODE_C": "INDETERMINATE"}
	verdictOf := func(code string) (string, bool) {
		verdict, ok := registry[code]
		return verdict, ok
	}
	good := []string{
		"| `ErrA` | `INDETERMINATE` | `CODE_A` |",
		"| `ErrB` | `INDETERMINATE` | `CODE_B` |",
		"| " + decodeFailureRow + " | `INDETERMINATE` | `CODE_C` |",
	}
	rows := func(lines ...string) map[int]string {
		m := make(map[int]string, len(lines))
		for i, line := range lines {
			m[i] = line
		}
		return m
	}
	if problems := refusalProblems(rows(good...), sentinels, verdictOf); len(problems) != 0 {
		t.Fatalf("a correct table reported %q", problems)
	}

	cases := map[string][]string{
		"verdict flipped, code unmarked": {good[0], "| `ErrB` | `ALLOW` | CODE_B |", good[2]},
		"verdict flipped":                {good[0], "| `ErrB` | `ALLOW` | `CODE_B` |", good[2]},
		"a row deleted":                  {good[0], good[2]},
		"a row repeated":                 {good[0], good[1], good[1], good[2]},
		"two codes in one row":           {good[0], "| `ErrB` | `INDETERMINATE` | `CODE_B` `CODE_C` |", good[2]},
		"an unregistered code":           {good[0], "| `ErrB` | `INDETERMINATE` | `CODE_Z` |", good[2]},
		"a refusal nobody declares":      {good[0], good[1], good[2], "| `ErrZ` | `INDETERMINATE` | `CODE_A` |"},
		"a row with two cells":           {good[0], "| `ErrB` | `INDETERMINATE` |", good[2]},
		"no code at all":                 {good[0], "| `ErrB` | `INDETERMINATE` | none |", good[2]},
		"verdict unmarked":               {good[0], "| `ErrB` | INDETERMINATE | `CODE_B` |", good[2]},
		"the sentinel unmarked":          {good[0], "| ErrB | `INDETERMINATE` | `CODE_B` |", good[2]},
	}
	for name, lines := range cases {
		if problems := refusalProblems(rows(lines...), sentinels, verdictOf); len(problems) == 0 {
			t.Errorf("%s: no problem reported", name)
		}
	}
}
