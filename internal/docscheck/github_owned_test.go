package docscheck

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runSettings runs script with scripts/lib/github-settings.sh sourced, from
// the repository's root.
func runSettings(t *testing.T, script string, args ...string) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is not on PATH: %v", err)
	}
	cmd := exec.Command(bash, append([]string{"-c", ". " + githubSettings + " && " + script, "settings"}, args...)...) //nolint:gosec // G204: bash from PATH sourcing this repository's own script
	cmd.Dir = repoRoot(t)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err = cmd.Run()
	return out.String() + stderr.String(), err
}

// TestTheBootstrapFindsEveryTwoApprovalPathOwned: the bootstrap's preflight
// reads CODEOWNERS as GitHub does, so a path a glob gives to the security
// maintainers, .golangci.yml under /.golangci.* among them, is owned, and a
// CODEOWNERS that leaves one out names it.
func TestTheBootstrapFindsEveryTwoApprovalPathOwned(t *testing.T) {
	if out, err := runSettings(t, `security_unowned "$1"`, ".github/CODEOWNERS"); err != nil || out != "" {
		t.Fatalf("security_unowned over .github/CODEOWNERS = %v, %q; want nothing", err, out)
	}
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "CODEOWNERS"))
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "/.golangci.*") {
			kept = append(kept, line)
		}
	}
	if len(kept) == len(strings.Split(string(data), "\n")) {
		t.Fatal(".github/CODEOWNERS holds no /.golangci.* line to leave out")
	}
	without := filepath.Join(t.TempDir(), "CODEOWNERS")
	if err := os.WriteFile(without, []byte(strings.Join(kept, "\n")), 0o600); err != nil { //nolint:gosec // G703: a path in this test's own directory
		t.Fatal(err)
	}
	if out, err := runSettings(t, `security_unowned "$1"`, without); err != nil || out != ".golangci.yml\n" {
		t.Fatalf("security_unowned without /.golangci.* = %v, %q; want .golangci.yml", err, out)
	}
}

func TestTheBootstrapReadsACodeownersPatternAsGitHubDoes(t *testing.T) {
	for _, c := range []struct {
		pattern, path string
		owned         bool
	}{
		{".golangci.*", ".golangci.yml", true},
		{".golangci.*", ".golangci/x.yml", false},
		{"cmd/*", "cmd/a", true},
		{"cmd/*", "cmd/a/b", false},
		{"cmd/plane/build*.go", "cmd/plane/build_pipeline.go", true},
		{"cmd/plane/build*.go", "cmd/plane/rebuild.go", false},
		{"scripts", "scripts/lib/repo-files.sh", true},
		{"scripts", "scriptsx", false},
		{"**/x.go", "a/b/x.go", true},
		{"go.mod", "go.mod", true},
		{"go.mod", "goxmod", false},
	} {
		out, err := runSettings(t, `re="$(codeowners_regex "$1")" && [[ "$2" =~ ${re} ]] && echo owned || echo not`, c.pattern, c.path)
		if err != nil {
			t.Fatalf("%q ~ %q: %v %s", c.pattern, c.path, err, out)
		}
		if got := out == "owned\n"; got != c.owned {
			t.Errorf("%q covers %q: %v, want %v", c.pattern, c.path, got, c.owned)
		}
	}
}
