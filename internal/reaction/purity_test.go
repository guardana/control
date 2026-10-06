package reaction_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/runs"
)

// impurePackages are refused whole: each is there to read or write files,
// start or signal processes, reach the network or draw randomness.
var impurePackages = []string{
	"os", "os/exec", "os/signal", "os/user", "io/ioutil", "path/filepath", "syscall", "plugin", "unsafe", "runtime",
	"net", "crypto/rand", "math/rand", "math/rand/v2",
	brand.ModulePath + "/internal/files",
}

// impurePrefixes refuses every package under each.
var impurePrefixes = []string{"net/", "golang.org/x/sys/"}

// impureNames are the functions of an allowed package that read the clock,
// the zone database or standard input, or draw randomness.
var impureNames = map[string][]string{
	"time": {
		"Now", "Since", "Until", "After", "AfterFunc", "Tick", "NewTicker", "NewTimer", "Sleep",
		"LoadLocation", "Local",
	},
	"context":        {"WithDeadline", "WithTimeout", "WithDeadlineCause", "WithTimeoutCause"},
	"fmt":            {"Scan", "Scanf", "Scanln"},
	"crypto/ed25519": {"GenerateKey"},
}

// impureMethods read the local zone, whatever value they are called on.
var impureMethods = []string{"Local", "Zone", "ZoneBounds", "IsDST"}

func impurePackage(path string) bool {
	return slices.Contains(impurePackages, path) ||
		slices.ContainsFunc(impurePrefixes, func(p string) bool { return strings.HasPrefix(path, p) })
}

// impureReads returns a message for each import or name of src this package
// may not use.
func impureReads(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	var problems []string
	report := func(pos token.Pos, what string) {
		problems = append(problems, fmt.Sprintf("%s:%d: %s", name, fset.Position(pos).Line, what))
	}
	imported := impureImports(t, name, file, report)
	ast.Inspect(file, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok {
			if what := impureSelector(imported, sel); what != "" {
				report(sel.Sel.Pos(), what)
			}
		}
		return true
	})
	return problems
}

// impureImports reports each refused import of file, and maps the name each
// other import is bound to onto its path.
func impureImports(t *testing.T, name string, file *ast.File, report func(token.Pos, string)) map[string]string {
	t.Helper()
	imported := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("%s: import %s: %v", name, spec.Path.Value, err)
		}
		local := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			local = spec.Name.Name
		}
		_, named := impureNames[path]
		switch {
		case impurePackage(path):
			report(spec.Pos(), "imports "+path)
		case local == "." && named:
			report(spec.Pos(), "imports "+path+" with a dot")
		case local != "_" && local != ".":
			imported[local] = path
		}
	}
	return imported
}

// impureSelector says what refused read sel names, or "". A selector on an
// imported package is judged by that package's names alone.
func impureSelector(imported map[string]string, sel *ast.SelectorExpr) string {
	if ident, isIdent := sel.X.(*ast.Ident); isIdent {
		if path, isImport := imported[ident.Name]; isImport {
			if slices.Contains(impureNames[path], sel.Sel.Name) {
				return "names " + path + "." + sel.Sel.Name
			}
			return ""
		}
	}
	if slices.Contains(impureMethods, sel.Sel.Name) {
		return "names the method " + sel.Sel.Name
	}
	return ""
}

// TestPackageReadsNoClockFileProcessOrRandomness holds every non-test Go file
// of this package, whatever its build constraints, to the names above.
func TestPackageReadsNoClockFileProcessOrRandomness(t *testing.T) {
	pkg := os.DirFS(".")
	names, err := fs.Glob(pkg, "*.go")
	if err != nil {
		t.Fatal(err)
	}
	var read []string
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := fs.ReadFile(pkg, name)
		if err != nil {
			t.Fatal(err)
		}
		read = append(read, name)
		for _, problem := range impureReads(t, name, src) {
			t.Error(problem)
		}
	}
	// A glob that missed the package's own files examined nothing.
	for _, want := range []string{"doc.go", "route.go", "envelope.go"} {
		if !slices.Contains(read, want) {
			t.Fatalf("read %v, which lacks %s", read, want)
		}
	}
}

// Each case is a way to write a refused read, or a near miss that is not one.
func TestImpureReads(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      []string
	}{
		{"clock", `import "time"; var _ = time.Now()`, []string{"names time.Now"}},
		{"clock as a value", `import clock "time"; var f = clock.Since`, []string{"names time.Since"}},
		{"zone", `import "time"; func f(t time.Time) { _ = t.Local() }`, []string{"names the method Local"}},
		{"deadline", `import "context"; var _, _ = context.WithTimeout(nil, 0)`, []string{"names context.WithTimeout"}},
		{"input", `import "fmt"; var _, _ = fmt.Scanln()`, []string{"names fmt.Scanln"}},
		{"key generation", `import "crypto/ed25519"; var _, _, _ = ed25519.GenerateKey(nil)`, []string{"names crypto/ed25519.GenerateKey"}},
		{"file", `import "os"; var _, _ = os.ReadFile("x")`, []string{"imports os"}},
		{"process", `import "os/exec"; var _ = exec.Command("x")`, []string{"imports os/exec"}},
		{"randomness", `import "crypto/rand"; var _ = rand.Reader`, []string{"imports crypto/rand"}},
		{"renamed randomness", `import r "math/rand/v2"; var _ = r.Int()`, []string{"imports math/rand/v2"}},
		{"network", `import "net/http"; var _ = http.Get`, []string{"imports net/http"}},
		{"the module's file helpers", `import _ "` + brand.ModulePath + `/internal/files"`, []string{"imports " + brand.ModulePath + "/internal/files"}},
		{"dot import of the clock", `import . "time"; var _ = Now()`, []string{"imports time with a dot"}},

		{"a time from a number", `import "time"; var _ = time.Unix(0, 0)`, nil},
		{"a duration", `import "time"; var _ = time.Minute`, nil},
		{"a clock of another package", `import clock "example.invalid/m/clock"; var _ = clock.Now()`, nil},
		{"a name in a string", `var _ = "time.Now"`, nil},
	} {
		problems := impureReads(t, "x.go", []byte("package x; "+c.src))
		if len(problems) != len(c.want) {
			t.Errorf("%s: impureReads = %q, want %q", c.name, problems, c.want)
			continue
		}
		for i, want := range c.want {
			if problems[i] != "x.go:1: "+want {
				t.Errorf("%s: problem %q, want %q", c.name, problems[i], "x.go:1: "+want)
			}
		}
	}
}

// The bounds this package holds as its own, so it links no runs code, are the
// runs directory's.
func TestBoundsAreTheRunsDirectorys(t *testing.T) {
	if reaction.MaxLifetime != runs.MaxTTL {
		t.Errorf("MaxLifetime = %s, runs.MaxTTL = %s", reaction.MaxLifetime, runs.MaxTTL)
	}
	if reaction.MaxTenantIDBytes != runs.MaxIdentityBytes {
		t.Errorf("MaxTenantIDBytes = %d, runs.MaxIdentityBytes = %d", reaction.MaxTenantIDBytes, runs.MaxIdentityBytes)
	}
	if reaction.MaxLifetime != 720*time.Hour || reaction.MaxTenantIDBytes != 256 {
		t.Errorf("MaxLifetime = %s, MaxTenantIDBytes = %d; want 720h and 256", reaction.MaxLifetime, reaction.MaxTenantIDBytes)
	}
}
