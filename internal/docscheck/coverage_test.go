package docscheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policy/reasons"
)

const (
	coveragePage    = "docs/reference/mcp-coverage.md"
	coverageHeading = "## Reason codes in the gateway's answers"
	// Fewer production files than this in a tree means the tree was not read,
	// not that it shrank.
	minPlaneFiles = 5
)

// The enforcement point: the pipeline and the one adapter. A reason code
// either tree spells is a code an agent can be answered with.
var planeTrees = []string{"internal/gateway", "adapters/mcp"}

// Code-shaped literals the plane spells that are not reason codes. Each has
// to be spelled somewhere, or the entry is stale.
var knownMarkers = []string{
	"DECIDED_PER_CALL", // the tools/list annotation for a tool decided per call
}

// Codes the plane is known to spell. The expected rows come from the source,
// so a new code needs a row on the page and no change here; this list only
// proves the source reader still finds what is there.
var knownPlaneCodes = []string{"LOCKDOWN", "EVIDENCE_UNAVAILABLE", "ACTION_UNCLASSIFIED", "APPROVAL_PENDING"}

var codeShaped = regexp.MustCompile(`^[A-Z][A-Z0-9_]{3,}$`)

// The coverage page's table is where an operator learns what a code in a
// gateway's answer means, and the plane's source is where the codes come
// from. Nothing else ties the two together: the kernel's codes reach the
// page through the registry, but a code the plane mints itself could be
// left off it, or a code the plane never spells kept on it, with every gate
// green.
func TestCoverageTableNamesWhatThePlaneSpells(t *testing.T) {
	fsys := repoFS(t)
	literals := planeLiterals(t, fsys)
	var spelled []string
	for _, literal := range literals {
		switch {
		case isRegistered(literal):
			spelled = append(spelled, literal)
		case !slices.Contains(knownMarkers, literal):
			t.Errorf("the plane spells %q, which is neither a registered reason code nor a known marker", literal)
		}
	}
	for _, marker := range knownMarkers {
		if !slices.Contains(literals, marker) {
			t.Errorf("knownMarkers names %q and nothing in the plane spells it", marker)
		}
	}
	for _, want := range knownPlaneCodes {
		if !slices.Contains(spelled, want) {
			t.Errorf("the source reader did not find %s", want)
		}
	}
	rows := tableRowsUnder(t, coveragePage, coverageHeading, readLines(t, fsys, coveragePage))
	for _, problem := range coverageProblems(rows, spelled, isRegistered) {
		t.Errorf("%s: %s", coveragePage, problem)
	}
}

func isRegistered(code string) bool {
	_, ok := reasons.Lookup(code)
	return ok
}

// coverageProblems checks the table's body rows, keyed by line index. Each
// row names one or more backticked, registered codes in its first cell,
// separated by a comma and a space; a code appears once in the table; and the
// table names exactly the codes the plane spells.
func coverageProblems(rows map[int]string, spelled []string, registered func(string) bool) []string {
	var problems []string
	named := make(map[string]int)
	lines := make([]int, 0, len(rows))
	for i := range rows {
		lines = append(lines, i)
	}
	slices.Sort(lines)
	for _, i := range lines {
		codes, rowProblems := rowCodes(rows[i], registered)
		for _, problem := range rowProblems {
			problems = append(problems, fmt.Sprintf("line %d: %s", i+1, problem))
		}
		for _, code := range codes {
			named[code]++
		}
	}
	codes := make([]string, 0, len(named))
	for code := range named {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	for _, code := range codes {
		if n := named[code]; n != 1 {
			problems = append(problems, fmt.Sprintf("%s is named %d times, want once", code, n))
		}
		if !slices.Contains(spelled, code) {
			problems = append(problems, fmt.Sprintf("the table names %s and nothing in %s spells it", code, strings.Join(planeTrees, " or ")))
		}
	}
	for _, code := range spelled {
		if named[code] == 0 {
			problems = append(problems, fmt.Sprintf("the plane spells %s and the table does not name it", code))
		}
	}
	return problems
}

// rowCodes reads the codes one row names, each backticked and registered,
// and the problems with the row.
func rowCodes(row string, registered func(string) bool) (codes, problems []string) {
	cells := tableCells(row)
	if len(cells) != 2 {
		return nil, []string{fmt.Sprintf("row has %d cells, want 2", len(cells))}
	}
	for _, cell := range strings.Split(cells[0], ", ") {
		code := backticked.FindStringSubmatch(cell)
		if code == nil || !codeShaped.MatchString(code[1]) {
			problems = append(problems, fmt.Sprintf("%q is not one backticked code", cell))
			continue
		}
		if !registered(code[1]) {
			problems = append(problems, fmt.Sprintf("%s is not in the registry", code[1]))
		}
		codes = append(codes, code[1])
	}
	return codes, problems
}

// planeLiterals returns, sorted and once each, every code-shaped string
// literal in the production source of the plane's trees.
func planeLiterals(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var found []string
	for _, tree := range planeTrees {
		files, err := fs.Glob(fsys, tree+"/*.go")
		if err != nil {
			t.Fatalf("listing %s: %v", tree, err)
		}
		read := 0
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := fs.ReadFile(fsys, name)
			if err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			literals, err := codeLiteralsIn(name, src)
			if err != nil {
				t.Fatalf("parsing %s: %v", name, err)
			}
			found = append(found, literals...)
			read++
		}
		if read < minPlaneFiles {
			t.Fatalf("%s: read %d production file(s), want at least %d; the tree was not examined", tree, read, minPlaneFiles)
		}
	}
	if len(found) == 0 {
		t.Fatal("no code-shaped literal found in the plane, so the table would be compared with nothing")
	}
	slices.Sort(found)
	return slices.Compact(found)
}

// codeLiteralsIn returns, sorted and once each, every string literal in one
// Go source file that is all capitals: the shape of a reason code.
func codeLiteralsIn(name string, src []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil && codeShaped.MatchString(s) {
				found = append(found, s)
			}
		}
		return true
	})
	slices.Sort(found)
	return slices.Compact(found), nil
}

func TestCodeLiteralsIn(t *testing.T) {
	src := "package p\n\nconst a = \"LOCKDOWN\"\n\nvar b = []string{\"lockdown\", \"Lockdown\", \"ABC\", \"A_B_C\", `RAW_CODE`, \"LOCKDOWN\"}\n\n" +
		"func f() string { return g(\"CODE_1\") + \"not a code\" }\n"
	got, err := codeLiteralsIn("p.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A_B_C", "CODE_1", "LOCKDOWN", "RAW_CODE"}; !slices.Equal(got, want) {
		t.Errorf("codeLiteralsIn = %q, want %q", got, want)
	}
	if _, err := codeLiteralsIn("p.go", []byte("not go source")); err == nil {
		t.Error("codeLiteralsIn accepted text that is not Go source")
	}
}

// Each case is one edit to a correct table, the first two being the page's
// own state before this check existed: a code the plane spells left off, and
// a code nothing in the plane spells kept on.
func TestCoverageProblems(t *testing.T) {
	spelled := []string{"CODE_A", "CODE_B", "CODE_C"}
	registry := map[string]bool{"CODE_A": true, "CODE_B": true, "CODE_C": true, "CODE_K": true}
	registered := func(code string) bool { return registry[code] }
	good := []string{
		"| `CODE_A` | when a |",
		"| `CODE_B`, `CODE_C` | when b or c |",
	}
	rows := func(lines ...string) map[int]string {
		m := make(map[int]string, len(lines))
		for i, line := range lines {
			m[i] = line
		}
		return m
	}
	if problems := coverageProblems(rows(good...), spelled, registered); len(problems) != 0 {
		t.Fatalf("a correct table reported %q", problems)
	}

	cases := map[string][]string{
		"a spelled code left off":         {good[0], "| `CODE_B` | when b |"},
		"a registered code nobody spells": {good[0], good[1], "| `CODE_K` | the kernel's |"},
		"an unregistered code":            {good[0], good[1], "| `CODE_Z` | never |"},
		"a code in two rows":              {good[0], good[1], "| `CODE_A` | again |"},
		"a code twice in one row":         {good[0], "| `CODE_B`, `CODE_C`, `CODE_B` | when b or c |"},
		"a code unmarked":                 {good[0], "| CODE_B, `CODE_C` | when b or c |"},
		"two codes split by a space":      {good[0], "| `CODE_B` `CODE_C` | when b or c |"},
		"a row with three cells":          {good[0], "| `CODE_B`, `CODE_C` | when | b or c |"},
		"a lowercase code":                {good[0], "| `code_b`, `CODE_C` | when b or c |"},
		"an empty table":                  {},
	}
	for name, lines := range cases {
		if problems := coverageProblems(rows(lines...), spelled, registered); len(problems) == 0 {
			t.Errorf("%s: no problem reported", name)
		}
	}
}
