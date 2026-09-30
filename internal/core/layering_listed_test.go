package core_test

import (
	"os"
	"strings"
	"testing"
)

// TestGuardedFilesAreNamedByEveryListing refuses a Go file of a guarded tree,
// tests included, that go list does not name natively and for foreignPlatform.
// A package whose only files a name suffix keeps to another platform is in no
// listing at all, so no other check reads its imports. A file a listing names
// as left out is refused by TestGuardedTreesImportOnlyAllowedPackages instead.
func TestGuardedFilesAreNamedByEveryListing(t *testing.T) {
	modulePath, moduleDir := mainModule(t)
	repo := os.DirFS(moduleDir)
	platforms := []struct {
		name string
		env  []string
	}{
		{"this platform", nil},
		{strings.Join(foreignPlatform, "/"), []string{"GOOS=" + foreignPlatform[0], "GOARCH=" + foreignPlatform[1]}},
	}
	files := 0
	for _, tree := range guardedTrees {
		mustExist(t, moduleDir, tree)
		sources := treeSources(t, repo, tree)
		files += len(sources)
		for _, platform := range platforms {
			named := namedFiles(t, modulePath, moduleDir, platform.env, tree)
			for _, rel := range sources {
				if !named[rel] {
					t.Errorf("%s is named by no go list listing on %s, so the rule never examines it", rel, platform.name)
				}
			}
		}
	}
	if files == 0 {
		t.Fatalf("none of %v holds a Go file outside testdata, so no listing was searched", guardedTrees)
	}
	t.Logf("%d Go file(s) of the guarded trees sought in %d listing(s)", files, len(platforms))
}

// namedFiles returns every Go file go list names in the packages of tree, the
// ones it leaves out included, as module-relative slash paths. A pattern that
// matches no package prints a warning and exits 0, and then names nothing.
func namedFiles(t *testing.T, modulePath, moduleDir string, env []string, tree string) map[string]bool {
	t.Helper()
	const format = "{{.ImportPath}}{{range .GoFiles}} {{.}}{{end}}{{range .CgoFiles}} {{.}}{{end}}" +
		"{{range .TestGoFiles}} {{.}}{{end}}{{range .XTestGoFiles}} {{.}}{{end}}" +
		"{{range .IgnoredGoFiles}} {{.}}{{end}}"
	named := make(map[string]bool)
	for _, line := range goListEnv(t, moduleDir, env, "-f", format, "./"+tree+"/...") {
		fields := strings.Fields(line)
		rel, ok := strings.CutPrefix(fields[0], modulePath+"/")
		if !ok {
			continue
		}
		for _, name := range fields[1:] {
			named[rel+"/"+name] = true
		}
	}
	return named
}
