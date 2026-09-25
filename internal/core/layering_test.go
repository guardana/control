// The dependency rule as a test, so `go test ./...` enforces it and not only
// golangci-lint and scripts/check-imports.sh. The rule is an allowlist: a
// package in a guarded tree may import a package of this module outside the
// denied trees, any package of an accepted module and a named set of standard
// library packages. A package nobody thought to deny is denied.
//
// A package held to the rule also builds from the same Go files on every
// platform. `go list` reports the build of the platform it runs on, so a file
// behind a build constraint for another platform, or assembly that needs no
// import at all, would pass every check on each machine the gate runs on. Both
// are refused rather than walked once per platform.
//
// The three mechanisms carry the same lists and are edited independently, so
// removing one does not remove the rule; layering_agreement_test.go fails when
// they stop agreeing. scripts/check-imports-probe.sh plants a fixture that
// breaks every part of the rule in each guarded package of a copy of the
// repository, and fails unless every mechanism refuses it.
//
// `go list -deps`, which scripts/check-imports.sh shares, reports the non-test
// build, so an import written only in a _test.go file is invisible here.
// depguard reads each file's own imports and covers that case.
package core_test

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// Trees that hold the decision path and must stay free of I/O. Each is read as
// the pattern ./<tree>/... . Every one exists and holds packages; a tree that
// does not resolve fails the test, so a rename or a move out from under its
// name cannot leave the rule examining less than this list says.
var guardedTrees = []string{
	"internal/core",
	"internal/policy",
	"internal/canon",
	"internal/evidence",
	"pkg/contract",
}

var (
	// Standard library packages a guarded tree may import, matched exactly:
	// "io" does not admit "io/ioutil". The functions in them that read the
	// clock, do I/O or draw randomness by name are refused in .golangci.yml.
	allowedStdlib = []string{
		"bufio",
		"bytes",
		"cmp",
		"context",
		"crypto/ed25519",
		"crypto/sha256",
		"crypto/subtle",
		"encoding/binary",
		"encoding/hex",
		"encoding/json",
		"errors",
		"fmt",
		"io",
		"maps",
		"math",
		"math/bits",
		"slices",
		"sort",
		"strconv",
		"strings",
		"sync",
		"sync/atomic",
		"time",
		"unicode",
		"unicode/utf16",
		"unicode/utf8",
	}

	// Modules outside this one, every package of which a guarded tree may
	// import.
	acceptedModules = []string{"google.golang.org/protobuf"}

	// Trees of this module a guarded tree must not import. Module-relative, so
	// renaming the module never touches this file.
	deniedInModule = []string{
		"adapters",
		"internal/storage",
		"internal/controlapi",
		"internal/gateway",
		"internal/ingest",
	}

	// Trees of this module accepted whole, their own imports not examined.
	// Generated code imports reflect and unsafe by construction, and
	// proto-check keeps it equal to what the pinned generator writes.
	generatedInModule = []string{"api/gen"}

	// go list fields a package held to the rule may not hold a file in. The
	// first two list what build constraints leave out on this platform; the
	// rest is every source the go command compiles or links besides plain Go.
	refusedFileLists = []string{
		"IgnoredGoFiles",
		"IgnoredOtherFiles",
		"CgoFiles",
		"CFiles",
		"CXXFiles",
		"MFiles",
		"HFiles",
		"FFiles",
		"SFiles",
		"SwigFiles",
		"SwigCXXFiles",
		"SysoFiles",
	}
)

// Refused whatever the lists above say, together with every package below
// each: the network, files and processes, system calls, randomness, and the two
// ways around the type system and the build. TestNothingNeverAllowedIsAllowed
// holds the lists to this one, so adding one of these to all three mechanisms at
// once still fails.
var neverAllowed = []string{"net", "os", "syscall", "crypto/rand", "math/rand", "unsafe", "plugin"}

// importVerdict reports whether a package in a guarded tree may import imp,
// and when it may not, the end of a sentence saying why.
func importVerdict(modulePath, imp string) (bool, string) {
	if underPath(imp, modulePath) {
		for _, rel := range deniedInModule {
			if underPath(imp, modulePath+"/"+rel) {
				return false, "a tree the dependency rule denies"
			}
		}
		return true, ""
	}
	for _, module := range acceptedModules {
		if underPath(imp, module) {
			return true, ""
		}
	}
	if slices.Contains(allowedStdlib, imp) {
		return true, ""
	}
	return false, "which the dependency rule does not allow"
}

// fileVerdict returns why a package held to the rule may not hold a file that
// go list reports in list, or "" when it may.
func fileVerdict(list string) string {
	switch {
	case !slices.Contains(refusedFileLists, list):
		return ""
	case strings.HasPrefix(list, "Ignored"):
		return "which build constraints leave out on this platform"
	default:
		return "which is not plain Go (" + list + ")"
	}
}

// heldToRule reports whether the imports and files of pkg are examined: it
// belongs to this module and is not generated. A helper package outside the
// guarded trees is examined too, or it would be a way around the rule.
func heldToRule(modulePath, pkg string) bool {
	if !underPath(pkg, modulePath) {
		return false
	}
	for _, rel := range generatedInModule {
		if underPath(pkg, modulePath+"/"+rel) {
			return false
		}
	}
	return true
}

// underPath reports whether dep is prefix itself or a package below it.
// Compared on path segments, not as a raw string prefix: "net/httptest" is not
// under "net/http".
func underPath(dep, prefix string) bool {
	return dep == prefix || strings.HasPrefix(dep, prefix+"/")
}

// Deliberately not `go list -deps ./...`: adapters legitimately use the
// network, so a whole-module assertion would fail on correct code the moment
// the product is built.
func TestGuardedTreesImportOnlyAllowedPackages(t *testing.T) {
	modulePath, moduleDir := mainModule(t)

	imports, files := 0, 0
	examined := make(map[string]bool)
	for _, tree := range guardedTrees {
		for _, pkg := range treeListing(t, moduleDir, tree) {
			// Two trees can reach the same package; it is judged once.
			if examined[pkg.path] || !heldToRule(modulePath, pkg.path) {
				continue
			}
			examined[pkg.path] = true
			imports += len(pkg.imports)
			for _, imp := range pkg.imports {
				if ok, why := importVerdict(modulePath, imp); !ok {
					t.Errorf("%s imports %s, %s", pkg.path, imp, why)
				}
			}
			files += len(pkg.files)
			for _, file := range pkg.files {
				t.Errorf("%s holds %s, %s", pkg.path, file.name, fileVerdict(file.list))
			}
		}
	}

	// A run in which nothing was imported examined nothing and must not
	// report success.
	if imports == 0 {
		t.Fatalf("%d package(s) of this module listed and not one import among them; the go list template no longer yields imports",
			len(examined))
	}
	t.Logf("%d guarded tree(s) resolved; %d package(s) of this module and %d import(s) examined; %d file(s) refused",
		len(guardedTrees), len(examined), imports, files)
}

// listedPackage is one line of a tree listing: a package, its direct imports
// and the files it holds in a refused list.
type listedPackage struct {
	path    string
	imports []string
	files   []listedFile
}

// listedFile is a file of a package and the go list field that named it.
type listedFile struct {
	list, name string
}

// treeListing returns every package the non-test build of tree reaches. A
// tree that is not there, or holds no package, is fatal: the rule would
// otherwise examine nothing where the list says it examines a tree.
func treeListing(t *testing.T, moduleDir, tree string) []listedPackage {
	t.Helper()
	pattern := "./" + tree + "/..."

	// Statted first, so the failure names the tree rather than reading like a
	// toolchain failure of go list.
	mustExist(t, moduleDir, tree)

	lines := goList(t, moduleDir, "-deps", "-f", listTemplate(), pattern)
	if len(lines) == 0 {
		t.Fatalf("%s resolved to no package, so the rule examined nothing there", pattern)
	}
	listing, err := parseListing(lines)
	if err != nil {
		t.Fatalf("go list %s: %v", pattern, err)
	}
	return listing
}

// mustExist fails the test when the guarded tree is not a directory of the
// module.
func mustExist(t *testing.T, moduleDir, tree string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(moduleDir, filepath.FromSlash(tree)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		t.Fatalf("guarded tree %s does not exist, so the rule examined nothing there", tree)
	case err != nil:
		t.Fatalf("stat %s: %v", tree, err)
	case !info.IsDir():
		t.Fatalf("guarded tree %s is not a directory", tree)
	}
}

// listTemplate prints one line per package: its path, its direct imports, a
// lone "|", then each file it holds in a refused list, written <list>:<file>.
// scripts/check-imports.sh builds the same line from the same lists.
func listTemplate() string {
	var b strings.Builder
	b.WriteString("{{.ImportPath}}{{range .Imports}} {{.}}{{end}} |")
	for _, list := range refusedFileLists {
		fmt.Fprintf(&b, "{{range .%s}} %s:{{.}}{{end}}", list, list)
	}
	return b.String()
}

// parseListing reads the lines listTemplate prints. A line without the "|", or
// a file that does not name a refused list, is an error rather than skipped: a
// reader that skipped it would examine less than go list named.
func parseListing(lines []string) ([]listedPackage, error) {
	listing := make([]listedPackage, 0, len(lines))
	for _, line := range lines {
		head, tail, ok := strings.Cut(line, " |")
		fields := strings.Fields(head)
		if !ok || len(fields) == 0 {
			return nil, fmt.Errorf("line %q holds no package followed by \" |\"", line)
		}
		pkg := listedPackage{path: fields[0], imports: fields[1:]}
		for _, token := range strings.Fields(tail) {
			list, name, found := strings.Cut(token, ":")
			if !found || name == "" || !slices.Contains(refusedFileLists, list) {
				return nil, fmt.Errorf("line %q: %q is not <list>:<file> for a refused list", line, token)
			}
			pkg.files = append(pkg.files, listedFile{list: list, name: name})
		}
		listing = append(listing, pkg)
	}
	return listing, nil
}

// TestGuardedTreesTakeNoInlineException refuses a nolint directive in a
// guarded tree that excuses a line from depguard or forbidigo, the two linters
// that carry the dependency rule there. The rule takes no inline exception: an
// exception needs an edit to .golangci.yml, which the security maintainers
// review, not a comment in the file it excuses.
func TestGuardedTreesTakeNoInlineException(t *testing.T) {
	_, moduleDir := mainModule(t)
	repo := os.DirFS(moduleDir)
	files := 0
	for _, tree := range guardedTrees {
		mustExist(t, moduleDir, tree)
		for _, rel := range goFilesOf(t, moduleDir, tree) {
			files++
			for _, problem := range inlineExceptions(t, repo, rel) {
				t.Error(problem)
			}
		}
	}
	if files == 0 {
		t.Fatalf("none of %v holds a Go file, so no nolint directive was looked for", guardedTrees)
	}
	t.Logf("%d Go file(s) of the guarded trees read for nolint directives", files)
}

// goFilesOf returns every Go file of the packages in tree, test files and the
// files build constraints leave out included, as module-relative slash paths.
func goFilesOf(t *testing.T, moduleDir, tree string) []string {
	t.Helper()
	const format = "{{.Dir}}{{range .GoFiles}}\t{{.}}{{end}}{{range .CgoFiles}}\t{{.}}{{end}}" +
		"{{range .TestGoFiles}}\t{{.}}{{end}}{{range .XTestGoFiles}}\t{{.}}{{end}}" +
		"{{range .IgnoredGoFiles}}\t{{.}}{{end}}"
	var files []string
	for _, line := range goList(t, moduleDir, "-f", format, "./"+tree+"/...") {
		parts := strings.Split(line, "\t")
		dir, err := filepath.Rel(moduleDir, parts[0])
		if err != nil {
			t.Fatalf("go list named %s, outside %s: %v", parts[0], moduleDir, err)
		}
		for _, name := range parts[1:] {
			files = append(files, filepath.ToSlash(filepath.Join(dir, name)))
		}
	}
	return files
}

// inlineExceptions returns a message for each comment of the file that excuses
// a line from depguard or forbidigo.
func inlineExceptions(t *testing.T, repo fs.FS, rel string) []string {
	t.Helper()
	src, err := fs.ReadFile(repo, rel)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}
	var problems []string
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if excused := nolintExcuses(comment.Text); excused != "" {
				problems = append(problems, fmt.Sprintf("%s:%d: %q excuses a guarded file from %s; the dependency rule takes no inline exception",
					rel, fset.Position(comment.Pos()).Line, comment.Text, excused))
			}
		}
	}
	return problems
}

// nolintExcuses returns what the comment excuses its line from among depguard
// and forbidigo, or "" when it excuses neither. It reads a directive the way
// golangci-lint does: leading slashes and spaces dropped, then "nolint"
// followed by a space, a colon or nothing, where no list, or "all" in the
// list, means every linter. It also drops a leading "*" and a trailing "*/",
// ignores case and reads only the first word of each item, which golangci-lint
// does not, so a spelling a later version might accept is refused now.
func nolintExcuses(comment string) string {
	text := strings.ToLower(strings.TrimSpace(strings.TrimLeft(comment, "/* \t")))
	text = strings.TrimSpace(strings.TrimSuffix(text, "*/"))
	rest, ok := strings.CutPrefix(text, "nolint")
	if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\t' && rest[0] != ':') {
		return ""
	}
	list, named := strings.CutPrefix(rest, ":")
	if !named {
		return "every linter"
	}
	list, _, _ = strings.Cut(list, "//")
	var excused []string
	for _, item := range strings.Split(list, ",") {
		words := strings.Fields(item)
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "all":
			return "every linter"
		case "depguard", "forbidigo":
			if !slices.Contains(excused, words[0]) {
				excused = append(excused, words[0])
			}
		}
	}
	return strings.Join(excused, " and ")
}

func TestNolintExcuses(t *testing.T) {
	cases := map[string]string{
		"//nolint:forbidigo // why":        "forbidigo",
		"//nolint:depguard":                "depguard",
		"//nolint:gosec,forbidigo // why":  "forbidigo",
		"//nolint:depguard,forbidigo":      "depguard and forbidigo",
		"//nolint:forbidigo,forbidigo":     "forbidigo",
		"//nolint":                         "every linter",
		"//nolint // why":                  "every linter",
		"//nolint:all":                     "every linter",
		"//nolint:gosec,all":               "every linter",
		"//nolint :forbidigo":              "every linter",
		"// nolint:forbidigo":              "forbidigo",
		"//nolint: forbidigo":              "forbidigo",
		"//nolint:FORBIDIGO":               "forbidigo",
		"//NOLINT:forbidigo":               "forbidigo",
		"/* nolint:depguard */":            "depguard",
		"//nolint:forbidigo reason":        "forbidigo",
		"//nolint:gosec // G204: no input": "",
		"//nolint:errorlint // see above":  "",
		"//nolint:":                        "",
		"// see nolint:forbidigo":          "",
		"//nolintforbidigo":                "",
		"// a //nolint:forbidigo in prose": "",
	}
	for comment, want := range cases {
		if got := nolintExcuses(comment); got != want {
			t.Errorf("nolintExcuses(%q) = %q, want %q", comment, got, want)
		}
	}
}

// The expected values are written out rather than derived from the lists
// above, so an edit to a list that admits one of these fails here.
func TestImportVerdict(t *testing.T) {
	const module = "example.invalid/m"
	cases := []struct {
		imp  string
		want bool
	}{
		{"strings", true},
		{"io", true},
		{"crypto/sha256", true},
		{"crypto/ed25519", true},
		{"math", true},
		{"sync/atomic", true},
		{"google.golang.org/protobuf/proto", true},
		{module + "/internal/canon", true},
		{module + "/api/gen/go/x/v1", true},
		{module + "/adaptersx", true},
		{module + "/internal/storagegen", true},
		{module + "/internal/controlapix", true},
		{module + "/internal/ingestion", true},

		{"net", false},
		{"net/http", false},
		{"net/netip", false},
		{"os", false},
		{"os/exec", false},
		{"syscall", false},
		{"crypto/rand", false},
		{"math/rand", false},
		{"math/rand/v2", false},
		{"unsafe", false},
		{"plugin", false},
		{"io/ioutil", false},
		{"time/tzdata", false},
		{"hash/maphash", false},
		{"crypto/sha512", false},
		{"reflect", false},
		{"regexp", false},
		{"runtime", false},
		{"log/slog", false},
		{"database/sql", false},
		{"golang.org/x/sync/errgroup", false},
		{"pgregory.net/rapid", false},
		{"google.golang.org/protobufx/proto", false},
		{"google.golang.org/grpc", false},
		{module + "/adapters", false},
		{module + "/adapters/mcp", false},
		{module + "/internal/storage", false},
		{module + "/internal/storage/pg", false},
		{module + "/internal/controlapi", false},
		{module + "/internal/controlapi/v1", false},
		{module + "/internal/gateway", false},
		{module + "/internal/gateway/tls", false},
		{module + "/internal/ingest", false},
		{module + "/internal/ingest/x", false},
		{"example.invalid/mx/internal/canon", false},
	}
	for _, c := range cases {
		if got, why := importVerdict(module, c.imp); got != c.want {
			t.Errorf("importVerdict(%q) = %v (%s), want %v", c.imp, got, why, c.want)
		}
	}
}

// Written out, not read from refusedFileLists: dropping a list from it and
// from scripts/lib/dependency-rule.sh at once would still pass the agreement
// test.
func TestFileVerdict(t *testing.T) {
	refused := []string{
		"IgnoredGoFiles", "IgnoredOtherFiles", "CgoFiles", "CFiles", "CXXFiles", "MFiles",
		"HFiles", "FFiles", "SFiles", "SwigFiles", "SwigCXXFiles", "SysoFiles",
	}
	allowed := []string{"GoFiles", "CompiledGoFiles", "TestGoFiles", "XTestGoFiles", "EmbedFiles", "TestEmbedFiles"}
	for _, list := range refused {
		if fileVerdict(list) == "" {
			t.Errorf("fileVerdict(%q) allows the file; a package held to the rule may not hold one", list)
		}
	}
	for _, list := range allowed {
		if why := fileVerdict(list); why != "" {
			t.Errorf("fileVerdict(%q) = %q; plain Go built on every platform is what a package is made of", list, why)
		}
	}
}

// Written out rather than read from guardedTrees: a tree dropped from every
// mechanism at once still passes the agreement tests, and the probe plants
// only in the trees the lists name.
func TestTheDecisionPathIsGuarded(t *testing.T) {
	for _, tree := range []string{"internal/core", "internal/policy", "internal/canon", "internal/evidence", "pkg/contract"} {
		if !slices.Contains(guardedTrees, tree) {
			t.Errorf("guardedTrees lacks %s, which holds the decision path", tree)
		}
	}
}

func TestParseListing(t *testing.T) {
	got, err := parseListing([]string{
		"example.invalid/m/a fmt example.invalid/m/b | SFiles:x.s IgnoredGoFiles:y_windows.go",
		"example.invalid/m/b |",
	})
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	want := []listedPackage{
		{
			path:    "example.invalid/m/a",
			imports: []string{"fmt", "example.invalid/m/b"},
			files:   []listedFile{{list: "SFiles", name: "x.s"}, {list: "IgnoredGoFiles", name: "y_windows.go"}},
		},
		{path: "example.invalid/m/b", imports: []string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseListing = %+v, want %+v", got, want)
	}
	for _, line := range []string{
		"example.invalid/m/a fmt",
		" |",
		"example.invalid/m/a | x.s",
		"example.invalid/m/a | GoFiles:x.go",
		"example.invalid/m/a | SFiles:",
	} {
		if _, err := parseListing([]string{line}); err == nil {
			t.Errorf("parseListing(%q) read without an error", line)
		}
	}
}

func TestHeldToRule(t *testing.T) {
	const module = "example.invalid/m"
	cases := []struct {
		pkg  string
		want bool
	}{
		{module + "/internal/canon", true},
		{module + "/internal/probehelper", true},
		{module + "/api/generated", true},
		{module + "/apix/gen", true},
		{module + "/api/gen", false},
		{module + "/api/gen/go/x/v1", false},
		{"google.golang.org/protobuf/proto", false},
		{"fmt", false},
		{"example.invalid/mx/internal/canon", false},
	}
	for _, c := range cases {
		if got := heldToRule(module, c.pkg); got != c.want {
			t.Errorf("heldToRule(%q) = %v, want %v", c.pkg, got, c.want)
		}
	}
}

// Everything below a refused root is refused, at any depth: naming a
// subpackage is not a way around the rule.
func TestVerdictRefusesEveryPackageBelowANeverAllowedRoot(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		root := rapid.SampledFrom(neverAllowed).Draw(t, "root")
		below := rapid.SliceOfN(rapid.StringMatching(`[a-z0-9_.]{1,8}`), 0, 3).Draw(t, "below")
		imp := strings.Join(append([]string{root}, below...), "/")
		if ok, _ := importVerdict("example.invalid/m", imp); ok {
			t.Fatalf("importVerdict admitted %q, below the refused root %q", imp, root)
		}
	})
}

// A path that merely starts with the same characters as the module or an
// accepted module names another module: "google.golang.org/protobufx" is not
// protobuf. No standard library path contains a dot, so none can collide.
func TestVerdictComparesWholeSegments(t *testing.T) {
	const module = "example.invalid/m"
	rapid.Check(t, func(t *rapid.T) {
		base := rapid.SampledFrom(append([]string{module}, acceptedModules...)).Draw(t, "base")
		tail := rapid.StringMatching(`[a-z0-9_.-][a-z0-9_./-]{0,12}`).Draw(t, "tail")
		if ok, _ := importVerdict(module, base+tail); ok {
			t.Fatalf("importVerdict admitted %q, which is neither %q nor below it", base+tail, base)
		}
	})
}

// A package of this module is admitted exactly when it lies outside every
// denied tree, whatever it is called.
func TestVerdictAdmitsTheModuleOutsideTheDeniedTrees(t *testing.T) {
	const module = "example.invalid/m"
	segment := rapid.StringMatching(`[a-z0-9_]{1,8}`)
	rapid.Check(t, func(t *rapid.T) {
		var parts []string
		if rapid.Bool().Draw(t, "in a denied tree") {
			parts = append(parts, rapid.SampledFrom(deniedInModule).Draw(t, "denied"))
		}
		parts = append(parts, rapid.SliceOfN(segment, 0, 3).Draw(t, "segments")...)
		if len(parts) == 0 {
			return
		}
		rel := strings.Join(parts, "/")
		want := true
		for _, denied := range deniedInModule {
			if strings.HasPrefix(rel+"/", denied+"/") {
				want = false
			}
		}
		if got, _ := importVerdict(module, module+"/"+rel); got != want {
			t.Fatalf("importVerdict(%q) = %v, want %v", module+"/"+rel, got, want)
		}
	})
}

func TestNothingNeverAllowedIsAllowed(t *testing.T) {
	for _, root := range neverAllowed {
		for _, pkg := range allowedStdlib {
			if underPath(pkg, root) {
				t.Errorf("allowedStdlib holds %s, which is under %s, which the rule never allows", pkg, root)
			}
		}
		for _, module := range acceptedModules {
			if underPath(module, root) || underPath(root, module) {
				t.Errorf("acceptedModules holds %s, which overlaps %s, which the rule never allows", module, root)
			}
		}
	}
}

// The fictional paths below use the reserved .invalid TLD (RFC 2606), which no
// module can ever be published under, so a rename cannot turn them into paths
// that collide with the real module.
func TestUnderPathComparesWholeSegments(t *testing.T) {
	cases := []struct {
		dep    string
		prefix string
		want   bool
	}{
		{"net/http", "net/http", true},
		{"net/http/httptest", "net/http", true},
		{"database/sql/driver", "database/sql", true},
		{"connectrpc.com/connect", "connectrpc.com", true},
		{"github.com/jackc/pgx/v5", "github.com/jackc", true},
		{"example.invalid/m/adapters/mcp", "example.invalid/m/adapters", true},
		{"net", "net/http", false},
		{"net/httptest", "net/http", false},
		{"example.invalid/m/internal/storagegen", "example.invalid/m/internal/storage", false},
		{"example.invalid/m/internal/core", "example.invalid/m/adapters", false},
	}
	for _, c := range cases {
		if got := underPath(c.dep, c.prefix); got != c.want {
			t.Errorf("underPath(%q, %q) = %v, want %v", c.dep, c.prefix, got, c.want)
		}
	}
}

// mainModule reports the module path and root directory as the go command sees
// them. Read, never hard-coded, so a module rename does not touch this file.
func mainModule(t *testing.T) (string, string) {
	t.Helper()
	lines := goList(t, "", "-m", "-f", "{{.Path}}\t{{.Dir}}")
	if len(lines) != 1 {
		t.Fatalf("go list -m returned %d line(s), want 1: %q", len(lines), lines)
	}
	path, dir, ok := strings.Cut(lines[0], "\t")
	if !ok || path == "" || dir == "" {
		t.Fatalf("go list -m returned %q, want a module path and a directory", lines[0])
	}
	return path, dir
}

// goList runs `go list` in dir (the test's own directory when empty) and
// returns its non-empty stdout lines. Any failure is fatal: a check that could
// not run must not read like a clean tree.
func goList(t *testing.T, dir string, args ...string) []string {
	t.Helper()
	// The program is the fixed string "go" and every argument originates in a
	// literal in this file; gosec cannot see that through the variadic call.
	//nolint:gosec // G204: no external input reaches this argument list.
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"list"}, args...)...)
	cmd.Dir = dir

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}

	var lines []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
