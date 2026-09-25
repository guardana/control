package delegation_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core/delegation"
	"github.com/guardana/control/internal/policy/reasons"
)

// codeShape is the shape of a reason code identifier.
var codeShape = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)

// TestEveryCodeCheckEmitsIsARegisteredDeny reads the package's own source,
// because that is where the codes live: nothing on the decision path imports
// the registry (ADR-0012), so only this test holds the codes to it. Step 3 of
// the kernel's order makes each one a built-in DENY taken alone, and that is
// the verdict the registry has to document for it.
func TestEveryCodeCheckEmitsIsARegisteredDeny(t *testing.T) {
	codes, _ := productionSource(t)
	want := []string{"DELEGATION_CYCLE", "DELEGATION_EXCEEDS_PARENT", "DELEGATION_EXPIRED"}
	if !slices.Equal(codes, want) {
		t.Fatalf("the code literals in the package are %q, want %q", codes, want)
	}
	for _, id := range want {
		code, ok := reasons.Lookup(id)
		if !ok {
			t.Errorf("%s is not in the registry", id)
			continue
		}
		if code.Verdict != controlv1.Verdict_VERDICT_DENY {
			t.Errorf("the registry documents %s with %s, want %s", id, code.Verdict, controlv1.Verdict_VERDICT_DENY)
		}
	}
}

// TestProductionImportsOnlyTheContract: this package needs nothing of this
// module beyond the generated contract. The registry least of all, since a
// code taken from it is a documented verdict one Lookup away (ADR-0012).
func TestProductionImportsOnlyTheContract(t *testing.T) {
	_, imports := productionSource(t)
	self := reflect.TypeFor[delegation.Effective]().PkgPath()
	module := strings.TrimSuffix(self, "/internal/core/delegation")
	if module == self {
		t.Fatalf("cannot find the module path in %q", self)
	}
	allowed := []string{reflect.TypeFor[controlv1.Delegation]().PkgPath()}
	for _, imp := range imports {
		if (imp == module || strings.HasPrefix(imp, module+"/")) && !slices.Contains(allowed, imp) {
			t.Errorf("the package imports %s; only %q are allowed from this module", imp, allowed)
		}
	}
}

// productionSource returns the reason-code literals and the imports of the
// package's non-test files, each sorted and without repeats.
func productionSource(t *testing.T) (codes, imports []string) {
	t.Helper()
	files := productionFiles(t)
	for _, f := range files {
		imports = append(imports, importsOf(t, f)...)
		codes = append(codes, codeLiteralsOf(f)...)
	}
	// A directory with no production file would pass both tests above while
	// examining nothing.
	if len(files) == 0 || len(imports) == 0 {
		t.Fatalf("read %d production file(s) and %d import(s); nothing was examined", len(files), len(imports))
	}
	slices.Sort(codes)
	slices.Sort(imports)
	return slices.Compact(codes), slices.Compact(imports)
}

func productionFiles(t *testing.T) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	return files
}

func importsOf(t *testing.T, f *ast.File) []string {
	t.Helper()
	var imports []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("import %s: %v", imp.Path.Value, err)
		}
		imports = append(imports, path)
	}
	return imports
}

// codeLiteralsOf returns every string literal in f shaped like a reason code.
func codeLiteralsOf(f *ast.File) []string {
	var codes []string
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil && codeShape.MatchString(s) {
				codes = append(codes, s)
			}
		}
		return true
	})
	return codes
}
