package impact

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const toplevelArgs = "rev-parse --show-toplevel"

func TestGitCommandRunsInTheDirectoryWithNoRedirection(t *testing.T) {
	dir := t.TempDir()
	environ := []string{
		"PATH=/usr/bin", "GIT_DIR=/elsewhere/.git", "GIT_WORK_TREE=/elsewhere", "GIT_COMMON_DIR=/elsewhere/.git",
		"GIT_INDEX_FILE=/elsewhere/index", "GIT_CEILING_DIRECTORIES=/", "HOME=/home/x",
	}
	cmd := gitCommand(dir, environ, "status", "--short")
	if got := strings.Join(cmd.Args, " "); !strings.HasSuffix(got, "git status --short") {
		t.Errorf("Args = %q", cmd.Args)
	}
	if cmd.Dir != dir {
		t.Errorf("Dir = %q, want %q", cmd.Dir, dir)
	}
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE"} {
		if i := slices.IndexFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, name+"=") }); i >= 0 {
			t.Errorf("Env carries %s", cmd.Env[i])
		}
	}
	var ceilings []string
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "GIT_CEILING_DIRECTORIES=") {
			ceilings = append(ceilings, kv)
		}
	}
	if want := "GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir); len(ceilings) != 1 || ceilings[0] != want {
		t.Errorf("ceilings = %q, want %q alone", ceilings, want)
	}
	for _, kept := range []string{"PATH=/usr/bin", "HOME=/home/x"} {
		if !slices.Contains(cmd.Env, kept) {
			t.Errorf("Env lost %s", kept)
		}
	}
}

// linked returns a directory and a symbolic link that resolves to it, so a
// working directory entered through the link has a real path elsewhere.
func linked(t *testing.T) (target, link string) {
	t.Helper()
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return target, link
}

func TestRepositoryAdmitsGitsTopLevelAtTheWorkingDirectory(t *testing.T) {
	target, link := linked(t)
	r, calls := git(t, map[string]string{toplevelArgs: target + "\n", diffArgs: "cmd/gw/main.go\n"})
	got, err := Changed(Repository(r, link), "main..HEAD")
	if err != nil {
		t.Fatalf("Changed through the link: %v", err)
	}
	if len(got) != 1 || got[0] != "cmd/gw/main.go" {
		t.Errorf("Changed = %q", got)
	}
	if got := asked(*calls); len(got) != 2 || got[0] != toplevelArgs || got[1] != diffArgs {
		t.Errorf("git was asked %q, want the top level before the diff", got)
	}
}

func TestRepositoryRefusesGitsTopLevelElsewhere(t *testing.T) {
	target, link := linked(t)
	outer := filepath.Dir(target)
	for name, answer := range map[string]string{
		"the directory above":     outer + "\n",
		"a directory that is not": filepath.Join(target, "missing") + "\n",
		"no directory at all":     "",
	} {
		t.Run(name, func(t *testing.T) {
			r, calls := git(t, map[string]string{toplevelArgs: answer, diffArgs: "cmd/gw/main.go\n"})
			_, err := Changed(Repository(r, link), "main..HEAD")
			if !errors.Is(err, ErrNotMeasured) {
				t.Fatalf("Changed = %v, want ErrNotMeasured", err)
			}
			if name == "the directory above" && (!strings.Contains(err.Error(), outer) || !strings.Contains(err.Error(), target)) {
				t.Errorf("error %q does not name both %s and %s", err, outer, target)
			}
			if got := asked(*calls); len(got) != 1 {
				t.Errorf("git was asked %q; the diff must not be read", got)
			}
		})
	}
}

func TestRepositoryPassesGitsOwnFailureThrough(t *testing.T) {
	_, err := Stale(Repository(noGit(errors.New("fatal: not a git repository")), t.TempDir()), pages())
	if !errors.Is(err, ErrNotMeasured) || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("Stale = %v, want ErrNotMeasured with git's reason", err)
	}
}

func TestRepositoryFallsBackToTheWalkForTheFileList(t *testing.T) {
	target, link := linked(t)
	r, _ := git(t, map[string]string{toplevelArgs: filepath.Dir(target) + "\n", lsArgs: "outer.go\x00"})
	w, walked := walk(t, []string{"a.go"}, nil)
	got, err := ListFiles(Repository(r, link), w)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if *walked != 1 || strings.Join(got.Paths, ",") != "a.go" || !strings.Contains(got.Source, "NOT MEASURED") {
		t.Errorf("ListFiles = %+v after %d walks; want the walk's file and the reason", got, *walked)
	}
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not on PATH: %v", err)
	}
}

func initRepository(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "root"}} {
		cmd := exec.Command("git", args...) //nolint:gosec // G204: the arguments are the two fixed lines above
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func TestGitAnswersFromTheRepositoryItRunsIn(t *testing.T) {
	needGit(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initRepository(t, dir)
	out, err := Git(dir, os.Environ())("rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("Git: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != dir {
		t.Errorf("top level = %q, want %q", got, dir)
	}
}

// An export placed inside another repository never borrows its git: the
// ceiling stops discovery on the real path, and through a symbolic link,
// which git resolves past the ceiling, the top-level check refuses it.
func TestGitDoesNotBorrowAnOuterRepository(t *testing.T) {
	needGit(t)
	outer, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initRepository(t, outer)
	export := filepath.Join(outer, "export")
	if err := os.Mkdir(export, 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(export, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Git(export, os.Environ())("rev-parse", "--show-toplevel"); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("git under the ceiling = %v, want git's own refusal", err)
	}
	for _, env := range [][]string{os.Environ(), append(os.Environ(), "GIT_DIR="+filepath.Join(outer, ".git"), "GIT_WORK_TREE="+outer)} {
		_, err := Changed(Repository(Git(link, env), link), "HEAD")
		if !errors.Is(err, ErrNotMeasured) || !strings.Contains(err.Error(), outer) {
			t.Errorf("Changed through the link = %v, want ErrNotMeasured naming %s", err, outer)
		}
	}
}
