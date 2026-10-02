package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// TestThePlaneNeverExplainsADecision: explaining a decision is the author's,
// at the command line. No package this binary links names a method Explain,
// called or as a value, but at the two places the one evaluation Decide and
// Explain share hands the trace down. It is sought in the syntax rather than
// among the symbols because the compiler inlines Kernel.Explain into its
// caller.
func TestThePlaneNeverExplainsADecision(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{join .GoFiles \" \"}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	shared := map[string]int{"/internal/core/decide.go": 0, "/internal/policy/policy.go": 0}
	examined := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || !strings.HasPrefix(fields[0], brand.ModulePath) {
			continue
		}
		pkg := strings.TrimPrefix(fields[0], brand.ModulePath)
		for _, name := range strings.Fields(fields[2]) {
			examined++
			n := explainSelectors(t, filepath.Join(fields[1], name))
			if _, known := shared[pkg+"/"+name]; known {
				shared[pkg+"/"+name] += n
			} else if n > 0 {
				t.Errorf("%s reaches Explain", filepath.Join(fields[1], name))
			}
		}
	}
	// A listing that reached none of the plane's own files examined nothing.
	if examined < 50 {
		t.Fatalf("examined %d file(s) of this module's packages; the plane holds more", examined)
	}
	for file, n := range shared {
		if n != 1 {
			t.Errorf("%s names Explain %d time(s), want once", file, n)
		}
	}
}

// explainSelectors counts the selectors named Explain in one Go file.
func explainSelectors(t *testing.T, path string) int {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	n := 0
	ast.Inspect(file, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == "Explain" {
			n++
		}
		return true
	})
	return n
}
