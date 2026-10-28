package approval_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// constructorName is the one function that spells the reserved prefix,
// written here rather than taken from the package, so a rename is a failure.
const constructorName = "AuthenticatedApproverID"

// mayCallTheConstructor is every directory, relative to the module root,
// whose sources may name the constructor: the package that declares it and
// the plane's pipeline, which builds an approver id from an authentication.
var mayCallTheConstructor = []string{"internal/core/approval", "internal/gateway"}

// namingTheConstructor returns every Go or assembly source under root, outside
// the allowed directories, that names the constructor, and how many sources
// it read. Any spelling counts, a call, a method value, an alias's selector,
// a linkname or a comment, because none of them leaves the name out.
func namingTheConstructor(t *testing.T, root string) (found []string, read int) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && notCompiled(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); ext != ".go" && ext != ".s" {
			return nil
		}
		rel, named, err := namesTheConstructor(root, path)
		if err != nil {
			return err
		}
		read++
		if named && !slices.Contains(mayCallTheConstructor, filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))) {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return found, read
}

// notCompiled is a directory the go command never builds from.
func notCompiled(name string) bool {
	return name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".")
}

// namesTheConstructor reads one source and says, beside its slash-separated
// path under root, whether it spells the constructor's name.
func namesTheConstructor(root, path string) (string, bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false, err
	}
	src, err := os.ReadFile(path) //nolint:gosec // G304: a source file under the module root
	if err != nil {
		return "", false, err
	}
	return filepath.ToSlash(rel), bytes.Contains(src, []byte(constructorName)), nil
}

// moduleRoot is the directory of the go.mod above this package.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the package")
		}
		dir = parent
	}
}

// TestOnlyThePipelineNamesTheConstructor: outside this package and
// internal/gateway, no source of the module names the one function that
// spells the reserved prefix, so no other package can hand a person's input
// to it.
func TestOnlyThePipelineNamesTheConstructor(t *testing.T) {
	root := moduleRoot(t)
	found, read := namingTheConstructor(t, root)
	if len(found) > 0 {
		t.Errorf("sources outside %v name %s: %v", mayCallTheConstructor, constructorName, found)
	}
	// The walk must have reached the declaration and a good part of the tree,
	// or a walk that read nothing would pass.
	if src, err := os.ReadFile(filepath.Join(root, "internal", "core", "approval", "authn.go")); err != nil || //nolint:gosec // G304: a source file under the module root
		!bytes.Contains(src, []byte("func "+constructorName+"(")) {
		t.Fatalf("the declaration is not where the test looks: %v", err)
	}
	if read < 500 {
		t.Fatalf("the walk read %d sources; it cannot have covered the module", read)
	}
}

// TestTheReachWalkFindsACallerOutsideThePipeline is the walk's negative
// control: a tree with a caller under an alias, one in a directory whose name
// only begins like the pipeline's, and one in assembly, reports all three,
// and leaves the pipeline's own caller and a fixture under testdata alone.
func TestTheReachWalkFindsACallerOutsideThePipeline(t *testing.T) {
	root := t.TempDir()
	for rel, src := range map[string]string{
		"internal/gateway/ok.go":         "package gateway\n\nvar _ = approval." + constructorName + "\n",
		"internal/core/approval/x.go":    "package approval\n\nfunc " + constructorName + "() {}\n",
		"internal/approvals/bad.go":      "package approvals\n\nimport ap \"m/internal/core/approval\"\n\nvar _ = ap." + constructorName + "\n",
		"internal/gatewayx/bad.go":       "package gatewayx\n\nvar _ = approval." + constructorName + "\n",
		"internal/console/bad.s":         "TEXT \u00b7" + constructorName + "(SB),0,$0\n",
		"internal/console/testdata/f.go": "package f\n\nvar _ = approval." + constructorName + "\n",
		"internal/console/clean.go":      "package console\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	found, read := namingTheConstructor(t, root)
	slices.Sort(found)
	want := []string{"internal/approvals/bad.go", "internal/console/bad.s", "internal/gatewayx/bad.go"}
	if !slices.Equal(found, want) || read != 6 {
		t.Errorf("the walk found %v after reading %d sources; want %v after 6", found, read, want)
	}
}
