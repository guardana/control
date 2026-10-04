package gateway_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNothingThatDecidesReadsTheTraceFields: the envelope's trace_id and
// span_id are a client's claim that correlates a call with a trace, so no
// code that decides, pauses, holds or resumes a call may read them. A source
// walk over the non-test files of those trees refuses a selector naming them
// and a string literal spelling their field names. A reflective read by
// number or by a computed name, and a whole-message comparison, it cannot
// see; the resume test across trace contexts holds the comparison.
func TestNothingThatDecidesReadsTheTraceFields(t *testing.T) {
	for _, tree := range []string{"../core", "../policy", "../pause", "../approvals", "../holdjournal", "../runs", "."} {
		files := sourceFiles(t, tree)
		if len(files) == 0 {
			t.Fatalf("%s holds no Go file, so the walk examined nothing", tree)
		}
		for _, path := range files {
			for _, read := range traceReads(t, path) {
				t.Errorf("%s %s", path, read)
			}
		}
	}
}

// TestTheTraceWalkFindsEveryRead: the walk finds each selector and string
// literal of a trace field in a fixture that holds all of them, and none of
// the names that only resemble one.
func TestTheTraceWalkFindsEveryRead(t *testing.T) {
	want := []string{
		"reads TraceId", "reads SpanId", "reads GetTraceId", "reads GetSpanId",
		`spells "trace_id"`, `spells "span_id"`, `spells "traceId"`, `spells "spanId"`, "spells `trace_id`",
	}
	got := traceReads(t, filepath.Join("testdata", "tracereads", "reads.go"))
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the walk found\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// sourceFiles lists the non-test Go files under tree, testdata aside.
func sourceFiles(t *testing.T, tree string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && d.Name() == "testdata":
			return filepath.SkipDir
		case !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", tree, err)
	}
	return files
}

// traceReads names each selector of a trace field, and each string literal
// spelling one's name, in the file at path.
func traceReads(t *testing.T, path string) []string {
	t.Helper()
	forbidden := map[string]bool{"TraceId": true, "SpanId": true, "GetTraceId": true, "GetSpanId": true}
	names := map[string]bool{"trace_id": true, "span_id": true, "traceId": true, "spanId": true}
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var reads []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if forbidden[n.Sel.Name] {
				reads = append(reads, "reads "+n.Sel.Name)
			}
		case *ast.BasicLit:
			if v, err := strconv.Unquote(n.Value); n.Kind == token.STRING && err == nil && names[v] {
				reads = append(reads, "spells "+n.Value)
			}
		}
		return true
	})
	return reads
}
