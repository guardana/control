package rules

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestNoProductionFileImportsTheRegistry: nothing that decides reads a reason
// code's documented verdict (ADR-0012), so the registry is for tests here.
// The authorable reasons are literals, held to the registry by
// TestAuthorableReasonsAreRegisteredWithTheirEffect.
func TestNoProductionFileImportsTheRegistry(t *testing.T) {
	t.Parallel()
	imports := productionImports(t)
	sawCanon := false
	for file, paths := range imports {
		for _, path := range paths {
			if strings.Contains(path, "/internal/policy/reasons") {
				t.Errorf("%s imports %s", file, path)
			}
			sawCanon = sawCanon || strings.HasSuffix(path, "/internal/canon")
		}
	}
	// A scan that read no imports would pass anything; Parse calls canon, so a
	// scan that works sees it.
	if len(imports) == 0 || !sawCanon {
		t.Fatalf("read the imports of %d production files and saw canon imported: %v", len(imports), sawCanon)
	}
}

// productionImports lists the imports of each Go file in this directory that
// is not a test.
func productionImports(t *testing.T) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string][]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			out[name] = append(out[name], path)
		}
	}
	return out
}
