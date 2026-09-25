package console

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// refusedTrees are what the page must never link: the key reader, the
// plane's evidence, its spool, journal and trail, every adapter, and a
// process launcher. The plane's pipeline is refusedPrefix.
var refusedTrees = []string{
	brand.ModulePath + "/internal/policykey",
	brand.ModulePath + "/internal/evidence",
	brand.ModulePath + "/internal/spool",
	brand.ModulePath + "/internal/holdjournal",
	brand.ModulePath + "/internal/trailfile",
	brand.ModulePath + "/adapters",
	"os/exec",
}

// TestThePageLinksOnlyWhatItAnswersThrough walks the non-test build of this
// package transitively. A listing that does not reach the approvals store,
// the pause file and the key text withholding examined some other package
// and would pass whatever it found.
func TestThePageLinksOnlyWhatItAnswersThrough(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := strings.Fields(string(out))
	for _, need := range []string{brand.ModulePath + "/internal/approvals", brand.ModulePath + "/internal/pause", brand.ModulePath + "/internal/keytext", "net/http"} {
		if !slices.Contains(deps, need) {
			t.Fatalf("go list -deps reached %d package(s) and not %s; nothing was examined", len(deps), need)
		}
	}
	for _, dep := range deps {
		if refusedDep(dep) {
			t.Errorf("the page reaches %s", dep)
		}
	}
}

// refusedPrefix is every package whose name starts with the gateway's: the
// pipeline, its configuration and whatever is added beside them.
var refusedPrefix = brand.ModulePath + "/internal/gateway"

func refusedDep(dep string) bool {
	if strings.HasPrefix(dep, refusedPrefix) {
		return true
	}
	for _, tree := range refusedTrees {
		if dep == tree || strings.HasPrefix(dep, tree+"/") {
			return true
		}
	}
	return false
}

func TestTheRefusedTreesMatch(t *testing.T) {
	for dep, want := range map[string]bool{
		brand.ModulePath + "/internal/policykey":     true,
		brand.ModulePath + "/internal/gatewayfoo":    true,
		brand.ModulePath + "/internal/gateway/serve": true,
		brand.ModulePath + "/adapters/mcp":           true,
		"os/exec":                                    true,
		brand.ModulePath + "/internal/approvals":     false,
		brand.ModulePath + "/internal/pause":         false,
		brand.ModulePath + "/internal/policykeyring": false,
		"os": false,
	} {
		if got := refusedDep(dep); got != want {
			t.Errorf("refusedDep(%q) = %v, want %v", dep, got, want)
		}
	}
}

// refusedCalls are the ways a handler reaches a file by a name a request
// chose: every file the page serves is embedded and looked up by exact path.
var refusedCalls = map[string][]string{
	"net/http": {"Dir", "FileServer", "FileServerFS", "ServeFile", "ServeFileFS", "FS", "ServeContent"},
	"os":       {"Open", "OpenFile", "OpenRoot", "OpenInRoot", "ReadFile", "ReadDir", "DirFS", "Stat", "Lstat"},
}

// refusedUses returns every refused selector in one file's source, however
// the package was imported.
func refusedUses(t *testing.T, name string, src any) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	local := map[string]string{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		as := path[strings.LastIndexByte(path, '/')+1:]
		if imp.Name != nil {
			as = imp.Name.Name
		}
		local[as] = path
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && slices.Contains(refusedCalls[local[x.Name]], sel.Sel.Name) {
			found = append(found, fset.Position(sel.Pos()).String()+" "+local[x.Name]+"."+sel.Sel.Name)
		}
		return true
	})
	return found
}

// TestThePageOpensNoFileByName parses every non-test Go file of the package.
func TestThePageOpensNoFileByName(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	parsed := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name) //nolint:gosec // G304: this package's own source
		if err != nil {
			t.Fatal(err)
		}
		parsed++
		for _, use := range refusedUses(t, name, src) {
			t.Errorf("the page uses %s", use)
		}
	}
	if parsed < 3 {
		t.Fatalf("parsed %d source file(s); the check examined nothing", parsed)
	}
}

func TestTheFileCheckBites(t *testing.T) {
	src := `package x
import (
	web "net/http"
	"os"
)
func a() { _ = web.FileServer(web.Dir(".")); _, _ = os.Open("k") }
`
	got := refusedUses(t, "planted.go", src)
	if len(got) != 3 {
		t.Errorf("found %q in a source that uses three refused calls", got)
	}
}
