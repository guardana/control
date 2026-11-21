package docscheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stageModule writes a tree with no .git holding the repository's
// scripts/lib, the named scripts and the given files, so repo_files lists it
// by walking it.
func stageModule(t *testing.T, scripts []string, files map[string]string) string {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()
	libs, err := filepath.Glob(filepath.Join(root, "scripts", "lib", "*.sh"))
	if err != nil || len(libs) == 0 {
		t.Fatalf("no script under scripts/lib to stage: %v", err)
	}
	for _, lib := range libs {
		scripts = append(scripts, "scripts/lib/"+filepath.Base(lib))
	}
	for _, rel := range scripts {
		data, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // G304: a file of this repository the test names
		if err != nil {
			t.Fatal(err)
		}
		files[rel] = string(data)
	}
	for rel, body := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runIn runs a bash command line in dir with GOFLAGS and a workspace shut
// out, and returns its output and error.
func runIn(t *testing.T, dir, line string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/bash", "-c", line) //nolint:gosec // G204: a fixed command line over a staged tree
	cmd.Dir = dir
	cmd.Env = append(cleanMakeEnv(), "GOWORK=off", "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

const stagedGoMod = "module example.com/staged\n\ngo 1.25\n"

// TestFuzzListRefusesALineNoSourceDeclares: a TestMain that prints a line
// beginning with Fuzz before the listing would be counted as a target and
// fuzzed for nothing, since a -fuzz pattern that matches no target passes
// with a warning. The same package without that TestMain lists its one
// target.
func TestFuzzListRefusesALineNoSourceDeclares(t *testing.T) {
	const target = "package staged\n\nimport \"testing\"\n\nfunc FuzzStaged(f *testing.F) {\n\tf.Add(1)\n\tf.Fuzz(func(t *testing.T, _ int) {})\n}\n"
	const printer = "package staged\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"testing\"\n)\n\n" +
		"func TestMain(m *testing.M) {\n\tfmt.Println(\"Fuzz configuration loaded\")\n\tos.Exit(m.Run())\n}\n"
	files := func() map[string]string {
		return map[string]string{"go.mod": stagedGoMod, "staged.go": "package staged\n", "staged_test.go": target}
	}
	clean := stageModule(t, []string{"scripts/fuzz-smoke.sh"}, files())
	if out, err := runIn(t, clean, "bash scripts/fuzz-smoke.sh --list"); err != nil || !strings.Contains(out, "fuzz-smoke: 1 target(s) compiled on") {
		t.Fatalf("fuzz-smoke --list over one target = %v, want it listed:\n%s", err, out)
	}
	planted := files()
	planted["main_test.go"] = printer
	dir := stageModule(t, []string{"scripts/fuzz-smoke.sh"}, planted)
	const want = `fuzz-smoke: . lists "Fuzz configuration loaded", which no file the build includes declares`
	for _, args := range []string{"--list", "1s"} {
		if out, err := runIn(t, dir, "bash scripts/fuzz-smoke.sh "+args); err == nil || !strings.Contains(out, want) {
			t.Errorf("fuzz-smoke %s with the TestMain planted = %v, want a refusal saying %q:\n%s", args, err, want, out)
		}
	}
}

// TestFuzzListRefusesANameOnlyAnExcludedFileDeclares: a TestMain that prints
// the name of a target declared only in a file a build constraint leaves out
// would have that name counted as compiled, and fuzzing it would match
// nothing and pass. A listed name must be declared in a file the build takes.
func TestFuzzListRefusesANameOnlyAnExcludedFileDeclares(t *testing.T) {
	dir := stageModule(t, []string{"scripts/fuzz-smoke.sh"}, map[string]string{
		"go.mod":          stagedGoMod,
		"b/b.go":          "package b\n",
		"b/never_test.go": "//go:build never\n\npackage b\n\nimport \"testing\"\n\nfunc FuzzB(f *testing.F) { f.Fuzz(func(t *testing.T, _ int) {}) }\n",
		"b/main_test.go":  testMainThat("if flag.Lookup(\"test.list\").Value.String() != \"\" {\n\t\tfmt.Println(\"FuzzB\")\n\t}", "b"),
	})
	const want = `fuzz-smoke: ./b lists "FuzzB", which no file the build includes declares`
	if out, err := runIn(t, dir, "bash scripts/fuzz-smoke.sh --list"); err == nil || !strings.Contains(out, want) {
		t.Errorf("fuzz-smoke --list with FuzzB declared only under a constraint that never holds = %v, want a refusal saying %q:\n%s", err, want, out)
	}
}

// TestFuzzRunCountsOnlyATargetThatFuzzed: a TestMain that exits 0 when the
// binary is told to fuzz makes go test pass without fuzzing. A target counts
// as run only once the fuzzing engine reports executions; the same package
// without that TestMain fuzzes and passes.
func TestFuzzRunCountsOnlyATargetThatFuzzed(t *testing.T) {
	files := func() map[string]string {
		return map[string]string{
			"go.mod":         stagedGoMod,
			"a/a.go":         "package a\n",
			"a/fuzz_test.go": "package a\n\nimport \"testing\"\n\nfunc FuzzA(f *testing.F) {\n\tf.Add(1)\n\tf.Fuzz(func(t *testing.T, _ int) {})\n}\n",
		}
	}
	clean := stageModule(t, []string{"scripts/fuzz-smoke.sh"}, files())
	if out, err := runIn(t, clean, "bash scripts/fuzz-smoke.sh 1s"); err != nil || !strings.Contains(out, "fuzz-smoke: 1 target(s) run for 1s each") {
		t.Fatalf("fuzz-smoke 1s over one target = %v, want it fuzzed:\n%s", err, out)
	}
	planted := files()
	planted["a/main_test.go"] = testMainThat("if flag.Lookup(\"test.fuzz\").Value.String() != \"\" {\n\t\tfmt.Println(\"PASS\")\n\t\tos.Exit(0)\n\t}", "a")
	dir := stageModule(t, []string{"scripts/fuzz-smoke.sh"}, planted)
	const want = "fuzz-smoke: ./a FuzzA reported no execution of the fuzzing engine"
	out, err := runIn(t, dir, "bash scripts/fuzz-smoke.sh 1s")
	if err == nil || !strings.Contains(out, want) || strings.Contains(out, "target(s) run") {
		t.Errorf("fuzz-smoke 1s with a TestMain that exits before fuzzing = %v, want a refusal saying %q:\n%s", err, want, out)
	}
}

// testMainThat is a TestMain of package pkg that parses the test flags, runs
// body, then the tests.
func testMainThat(body, pkg string) string {
	return "package " + pkg + "\n\nimport (\n\t\"flag\"\n\t\"fmt\"\n\t\"os\"\n\t\"testing\"\n)\n\n" +
		"func TestMain(m *testing.M) {\n\tflag.Parse()\n\t" + body + "\n\tos.Exit(m.Run())\n}\n"
}

// TestTheTestTargetsRefuseAModuleThatHidesAPackage: a go.mod's ignore
// directive drops a directory from ./..., and the reporter sees only the
// packages that ran, so the failing test planted there would pass make test
// unseen. go_packages_covered lists the module's packages by path and refuses
// the one ./... leaves out; without the directive it passes. A package under
// a directory named testdata or starting with "_" is not in ./... either, and
// is refused once the module's code imports it, so it is built and shipped
// with no test of its own run. Both test targets run it for every module
// before their tests.
func TestTheTestTargetsRefuseAModuleThatHidesAPackage(t *testing.T) {
	files := func(gomod string) map[string]string {
		return map[string]string{
			"go.mod":                        gomod,
			"staged.go":                     "package staged\n",
			"skipped/skipped_test.go":       "package skipped\n\nimport \"testing\"\n\nfunc TestHidden(t *testing.T) { t.Fatal(\"hidden\") }\n",
			"windows/only_windows.go":       "package windows\n",
			"scripts/gen-staged.go":         "//go:build ignore\n\npackage main\n\nfunc main() {}\n",
			"skipped/testdata/x/fixture.go": "package fixture\n",
		}
	}
	const covered = ". scripts/lib/go-modules.sh; go_packages_covered go ."
	open := stageModule(t, nil, files(stagedGoMod))
	if out, err := runIn(t, open, covered); err != nil {
		t.Errorf("go_packages_covered over a module ./... covers whole = %v:\n%s", err, out)
	}
	hidden := stageModule(t, nil, files(stagedGoMod+"\nignore ./skipped\n"))
	const want = "go_packages_covered: ./... in module . leaves out a package that holds Go files: example.com/staged/skipped"
	if out, err := runIn(t, hidden, covered); err == nil || !strings.Contains(out, want) {
		t.Errorf("go_packages_covered over a module that ignores ./skipped = %v, want a refusal saying %q:\n%s", err, want, out)
	}

	imported := files(stagedGoMod)
	imported["staged.go"] = "package staged\n\nimport (\n\t_ \"example.com/staged/_hidden\"\n\t_ \"example.com/staged/internal/testdata/td\"\n)\n"
	imported["_hidden/h.go"] = "package hidden\n"
	imported["_hidden/h_test.go"] = "package hidden\n\nimport \"testing\"\n\nfunc TestH(t *testing.T) { t.Fatal(\"hidden\") }\n"
	imported["internal/testdata/td/td.go"] = "package td\n"
	imported["internal/testdata/td/td_test.go"] = "package td\n\nimport \"testing\"\n\nfunc TestT(t *testing.T) { t.Fatal(\"testdata\") }\n"
	out, err := runIn(t, stageModule(t, nil, imported), covered)
	const leaves = "go_packages_covered: ./... in module . leaves out a package that holds Go files:"
	if err == nil || !strings.Contains(out, leaves) || !strings.Contains(out, " example.com/staged/_hidden") ||
		!strings.Contains(out, " example.com/staged/internal/testdata/td") {
		t.Errorf("go_packages_covered over a module whose code imports _hidden and internal/testdata/td = %v, want a refusal naming both:\n%s", err, out)
	}

	wantTestTargetsCovered(t)
}

// wantTestTargetsCovered wants the Makefile's test and test-race to run
// go_packages_covered over each module before its tests.
func wantTestTargetsCovered(t *testing.T) {
	t.Helper()
	lines := readLines(t, repoFS(t), "Makefile")
	for _, target := range []string{"test", "test-race"} {
		i := slices.Index(lines, target+":")
		if i < 0 || i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "\t$(call each_module,{ [[ \"$$pkgs\" != ./... ]] || ($(COVERED) \"$(GO)\" \"$$dir\"); } && $(GO) -C \"$$dir\" test ") {
			t.Errorf("Makefile: %s does not run go_packages_covered over each module before its tests", target)
		}
	}
	if !slices.Contains(lines, "COVERED := . scripts/lib/go-modules.sh; go_packages_covered") {
		t.Error("Makefile: COVERED does not name go_packages_covered")
	}
}
