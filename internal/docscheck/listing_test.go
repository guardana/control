package docscheck

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// listingFixture is a work tree holding a tracked page, a page nobody has
// added yet, a page under a directory .gitignore names and one under a
// directory .git/info/exclude names. Each links to a file that does not exist.
func listingFixture(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is needed to build a work tree: %v", err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn := func(args ...string) {
		t.Helper()
		var stderr bytes.Buffer
		cmd := exec.Command(gitPath, args...) //nolint:gosec // G204: git from PATH with this test's own arguments
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+dir, "XDG_CONFIG_HOME="+dir,
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
		}
	}
	gitIn("init", "-q")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for rel, text := range map[string]string{
		".git/info/exclude": "/local/\n",
		".gitignore":        "/ignored/\n",
		"tracked.md":        "# Tracked\n\n[a](missing.md)\n",
		"new.md":            "# New\n\n[b](gone.md) and [c](tracked.md)\n",
		"ignored/a.md":      "# Ignored\n\n[d](void.md)\n",
		"local/b.md":        "# Local\n\n[e](nowhere.md)\n",
		".github/t.md":      "# Template\n\n[f](absent.md)\n",
	} {
		p := filepath.FromSlash(rel)
		if err := root.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := root.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitIn("add", "tracked.md")
	return dir
}

func TestMarkdownFilesAreWhatTheRepositoryHolds(t *testing.T) {
	got := markdownFiles(t, listingFixture(t))
	if want := []string{".github/t.md", "new.md", "tracked.md"}; !slices.Equal(got, want) {
		t.Errorf("markdownFiles = %q, want %q: the tracked page and the new one, never an ignored one", got, want)
	}
}

// The negative control of the link check over the listing: a broken link in a
// tracked page and in a page not yet added still fails; one in a file git
// ignores is not read.
func TestABrokenLinkInATrackedPageStillFails(t *testing.T) {
	problems := strings.Join(brokenLinks(t, listingFixture(t)), "\n")
	for _, want := range []string{"tracked.md:3: ", "missing.md", "new.md:3: ", "gone.md", ".github/t.md:3: ", "absent.md"} {
		if !strings.Contains(problems, want) {
			t.Errorf("the link check does not report %q; it said:\n%s", want, problems)
		}
	}
	for _, unread := range []string{"void.md", "nowhere.md"} {
		if strings.Contains(problems, unread) {
			t.Errorf("the link check reports %q; it said:\n%s", unread, problems)
		}
	}
}
