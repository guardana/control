package impact

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const config = `{
  "budgets": {"tutorial": 1500, "how-to": 1200, "explanation": 3000, "reference": 1500, "extending": 2000},
  "exempt_types": ["spec", "project"],
  "readme": {"root": 600, "folder": 300},
  "ceilings": {},
  "directories": {"docs": {"type": "project"}, "docs/guides": {"type": "how-to"}},
  "page_types": {},
  "frozen": ["Makefile"],
  "excluded": ["docs/plans/"],
  "surfaces": ["cmd/**", "adapters/**"]
}
`

// tree is a repository root every run below examines: one page covering
// cmd/**, a surface no page covers, a frozen file and a directory the
// configuration excludes.
func tree() fstest.MapFS {
	return fstest.MapFS{
		"go.mod":              {Data: []byte("module x\n")},
		"docs/docs.json":      {Data: []byte(config)},
		"docs/guides/run.md":  {Data: []byte(goodPage)},
		"docs/plans/plan.md":  {Data: []byte("not a page\n")},
		"cmd/gw/main.go":      {Data: []byte("package main\n")},
		"adapters/mcp/mcp.go": {Data: []byte("package mcp\n")},
		"Makefile":            {Data: []byte("all:\n")},
		"changed.txt":         {Data: []byte("adapters/mcp/mcp.go\n")},
	}
}

const listed = "go.mod\x00docs/docs.json\x00docs/guides/run.md\x00cmd/gw/main.go\x00adapters/mcp/mcp.go\x00Makefile\x00changed.txt\x00"

// repository answers git for a full clone rooted at wd.
func repository(t *testing.T, wd string) map[string]string {
	t.Helper()
	root, err := filepath.EvalSymlinks(wd)
	if err != nil {
		t.Fatal(err)
	}
	answers := history()
	answers[toplevelArgs] = root + "\n"
	answers[lsArgs] = listed
	answers[deletedArgs] = ""
	answers[diffArgs] = "cmd/gw/main.go\n"
	return answers
}

type outcome struct {
	code           int
	stdout, stderr string
}

func runTool(t *testing.T, args []string, stdin string, run Runner, fsys fstest.MapFS, wd string) outcome {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &stdout, &stderr, run, fsys, wd)
	return outcome{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (o outcome) expect(t *testing.T, code int, stdout, stderr []string) {
	t.Helper()
	if o.code != code {
		t.Errorf("exit %d, want %d\nstdout:\n%sstderr:\n%s", o.code, code, o.stdout, o.stderr)
	}
	for _, want := range stdout {
		if !strings.Contains(o.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, o.stdout)
		}
	}
	for _, want := range stderr {
		if !strings.Contains(o.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, o.stderr)
		}
	}
}

func TestRunReportsTheFourListsFromStandardInput(t *testing.T) {
	wd := t.TempDir()
	r, _ := git(t, repository(t, wd))
	runTool(t, []string{"--changed", "-"}, "cmd/gw/main.go\nadapters/mcp/mcp.go\nMakefile\n", r, tree(), wd).expect(t, 0, []string{
		"3 changed paths read",
		"1 pages to review of 1 examined",
		"  docs/guides/run.md <- cmd/gw/main.go",
		"1 changed paths under a surface no page covers, of 2 surfaces examined",
		"  adapters/mcp/mcp.go (adapters/**)",
		"1 changed paths under a frozen path: a contract change, of 1 frozen paths examined",
		"  Makefile (Makefile)",
		"0 covers globs matching no file, of 1 pages and 7 files examined (git ls-files)",
	}, nil)
}

// An empty list is a legitimate answer and exits 0, and the first line says
// that nothing was read, so a producer that failed silently upstream of a
// pipe is visible.
func TestRunSaysWhenNoChangedPathWasRead(t *testing.T) {
	wd := t.TempDir()
	r, _ := git(t, repository(t, wd))
	out := runTool(t, []string{"--changed", "-"}, "", r, tree(), wd)
	out.expect(t, 0, []string{"0 pages to review of 1 examined"}, nil)
	if !strings.HasPrefix(out.stdout, "0 changed paths read\n") {
		t.Errorf("stdout does not open with the count read:\n%s", out.stdout)
	}
	out = runTool(t, []string{"--changed", "-"}, "cmd/gw/main.go\nadapters/mcp/mcp.go\n", r, tree(), wd)
	if !strings.HasPrefix(out.stdout, "2 changed paths read\n") {
		t.Errorf("stdout does not open with the count read:\n%s", out.stdout)
	}
}

func TestRunReadsTheChangedFileAndTheRange(t *testing.T) {
	wd := t.TempDir()
	r, _ := git(t, repository(t, wd))
	runTool(t, []string{"--changed", "changed.txt"}, "", r, tree(), wd).expect(t, 0, []string{
		"0 pages to review of 1 examined",
		"  adapters/mcp/mcp.go (adapters/**)",
	}, nil)
	runTool(t, []string{"--range", "main..HEAD"}, "", r, tree(), wd).expect(t, 0, []string{
		"1 pages to review of 1 examined",
		"  docs/guides/run.md <- cmd/gw/main.go",
	}, nil)
}

func TestRunWalksTheTreeWhenGitCannotList(t *testing.T) {
	wd := t.TempDir()
	r := noGit(errors.New("fatal: not a git repository"))
	runTool(t, []string{"--changed", "-"}, "", r, tree(), wd).expect(t, 0, []string{
		"0 pages to review of 1 examined",
		"0 covers globs matching no file, of 1 pages and 7 files examined (a walk of the tree; git ls-files: fatal: not a git repository)",
	}, nil)
}

func TestRunIsNotMeasuredWhenGitCannotAnswer(t *testing.T) {
	wd := t.TempDir()
	for name, args := range map[string][]string{"a range": {"--range", "main..HEAD"}, "staleness": {"--stale"}} {
		t.Run(name, func(t *testing.T) {
			r := noGit(errors.New(`exec: "git": executable file not found`))
			out := runTool(t, args, "", r, tree(), wd)
			out.expect(t, 2, nil, []string{"NOT MEASURED", "not found"})
			if out.stdout != "" {
				t.Errorf("stdout = %q, want nothing beside NOT MEASURED", out.stdout)
			}
		})
	}
}

func TestRunIsNotMeasuredFromAnotherRepositorysTopLevel(t *testing.T) {
	wd := t.TempDir()
	answers := repository(t, wd)
	answers[toplevelArgs] = filepath.Dir(filepath.Dir(answers[toplevelArgs])) + "\n"
	r, _ := git(t, answers)
	runTool(t, []string{"--range", "main..HEAD"}, "", r, tree(), wd).expect(t, 2, nil, []string{"NOT MEASURED", "top level"})
	runTool(t, []string{"--stale"}, "", r, tree(), wd).expect(t, 2, nil, []string{"NOT MEASURED", "top level"})
	runTool(t, []string{"--changed", "-"}, "", r, tree(), wd).expect(t, 0, []string{
		"7 files examined (a walk of the tree; git ls-files: NOT MEASURED",
	}, nil)
}

func TestRunPrintsStalenessAndTheCoveringPages(t *testing.T) {
	wd := t.TempDir()
	answers := repository(t, wd)
	answers[logLine] = "\x00c1 feat: the command\n\ncmd/gw/main.go\n\x00c0 docs: the guide\n\ndocs/guides/run.md\n"
	r, _ := git(t, answers)
	runTool(t, []string{"--stale"}, "", r, tree(), wd).expect(t, 0, []string{
		"1 pages examined",
		"  docs/guides/run.md: last commit c0, 1 commits since touching its covers",
		"    c1 feat: the command",
	}, nil)
	runTool(t, []string{"--for", "cmd/gw/main.go"}, "", r, tree(), wd).expect(t, 0, []string{
		"1 pages cover cmd/gw/main.go of 1 examined",
		"  docs/guides/run.md",
	}, nil)
	runTool(t, []string{"--for", "Makefile"}, "", r, tree(), wd).expect(t, 0, []string{"0 pages cover Makefile of 1 examined"}, nil)
}

func TestRunExitsOneWhenAPageDoesNotParse(t *testing.T) {
	wd := t.TempDir()
	r, _ := git(t, repository(t, wd))
	fsys := tree()
	fsys["docs/guides/bad.md"] = &fstest.MapFile{Data: []byte("# no frontmatter\n")}
	runTool(t, []string{"--changed", "-"}, "cmd/gw/main.go\n", r, fsys, wd).expect(t, 1, []string{
		"1 pages to review of 1 examined",
		"1 pages did not parse and are missing above",
		"  docs/guides/bad.md: ",
	}, nil)
}

func TestRunRefusesWhatItCannotRun(t *testing.T) {
	wd := t.TempDir()
	noRoot := tree()
	delete(noRoot, "go.mod")
	noPage := tree()
	noPage["docs/guides/run.md"] = &fstest.MapFile{Data: []byte("# no frontmatter\n")}
	badConfig := tree()
	badConfig["docs/docs.json"] = &fstest.MapFile{Data: []byte("{}\n")}
	for name, tc := range map[string]struct {
		args   []string
		stdin  string
		fsys   fstest.MapFS
		reason string
	}{
		"no mode":              {nil, "", tree(), "exactly one"},
		"two modes":            {[]string{"--stale", "--for", "x"}, "", tree(), "exactly one"},
		"a positional":         {[]string{"--stale", "x"}, "", tree(), "unexpected argument"},
		"an unknown flag":      {[]string{"--bogus"}, "", tree(), "bogus"},
		"a range as an option": {[]string{"--range", "--output=x"}, "", tree(), "revision range"},
		"not at the root":      {[]string{"--stale"}, "", noRoot, "repository root"},
		"no page parses":       {[]string{"--stale"}, "", noPage, "none of the 1 pages"},
		"a broken config":      {[]string{"--stale"}, "", badConfig, "docs/docs.json"},
		"a changed file gone":  {[]string{"--changed", "gone.txt"}, "", tree(), "gone.txt"},
		"a changed path dirty": {[]string{"--changed", "-"}, "cmd/x y.go\n", tree(), "white space"},
		"a for path dirty":     {[]string{"--for", "../x"}, "", tree(), "clean"},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := git(t, repository(t, wd))
			out := runTool(t, tc.args, tc.stdin, r, tc.fsys, wd)
			out.expect(t, 2, nil, []string{tc.reason})
			if out.stdout != "" {
				t.Errorf("stdout = %q, want nothing from a run that was not made", out.stdout)
			}
		})
	}
}
