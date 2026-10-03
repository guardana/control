package docscheck

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// Dependabot opens update pull requests only for the gomod directories it is
// told about, so a module missing from that list ages without anyone being
// told. The expected list is the one every Go gate target loops over.
func TestDependabotUpdatesEveryGoModule(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is not on PATH, so scripts/lib/go-modules.sh cannot list the modules: %v", err)
	}
	cmd := exec.Command(bash, "-c", `. scripts/lib/go-modules.sh && go_modules`) //nolint:gosec // G204: bash from PATH sourcing this repository's own script
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go_modules failed: %v", err)
	}
	modules := strings.Fields(string(out))
	if len(modules) < 2 {
		t.Fatalf("go_modules listed %q; the repository holds a nested module, so the listing stopped working", modules)
	}
	directories, err := dependabotGoDirectories(readLines(t, repoFS(t), ".github/dependabot.yml"))
	if err != nil {
		t.Fatalf(".github/dependabot.yml: %v", err)
	}
	for _, problem := range dependabotModuleProblems(directories, modules) {
		t.Errorf(".github/dependabot.yml: %s", problem)
	}
}

// dependabotGoDirectories returns the directories of the one gomod update
// entry, from its directories list or its directory key. It reads the block
// layout this file uses, and an entry it cannot read is an error, never an
// empty list.
func dependabotGoDirectories(lines []string) ([]string, error) {
	var dirs []string
	entries := 0
	for i := 0; i < len(lines); i++ {
		dash, rest, ok := entryStart(lines[i])
		if !ok || strings.TrimSpace(strings.TrimPrefix(rest, "package-ecosystem:")) != "gomod" {
			continue
		}
		entries++
		end := i + 1
		for end < len(lines) && (isBlankOrComment(lines[end]) || indentOf(lines[end]) > dash) {
			end++
		}
		dirs = append(dirs, entryDirectories(lines[i+1:end])...)
		i = end - 1
	}
	switch {
	case entries != 1:
		return nil, fmt.Errorf("found %d gomod update entr(ies), want 1", entries)
	case len(dirs) == 0:
		return nil, errors.New("the gomod update entry names no directory")
	}
	return dirs, nil
}

// entryStart reports a "- package-ecosystem:" line, the column of its dash and
// the text after the dash.
func entryStart(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	rest, ok := strings.CutPrefix(trimmed, "- ")
	if !ok || !strings.HasPrefix(strings.TrimSpace(rest), "package-ecosystem:") {
		return 0, "", false
	}
	return len(line) - len(trimmed), strings.TrimSpace(rest), true
}

func entryDirectories(body []string) []string {
	var dirs []string
	for i := 0; i < len(body); i++ {
		key := strings.TrimSpace(body[i])
		if value, ok := strings.CutPrefix(key, "directory:"); ok {
			dirs = append(dirs, unquote(value))
			continue
		}
		if key != "directories:" {
			continue
		}
		indent := indentOf(body[i])
		for i+1 < len(body) && (isBlankOrComment(body[i+1]) || indentOf(body[i+1]) > indent) {
			i++
			if item, ok := strings.CutPrefix(strings.TrimSpace(body[i]), "- "); ok {
				dirs = append(dirs, unquote(item))
			}
		}
	}
	return dirs
}

func unquote(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"'`)
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func isBlankOrComment(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

// dependabotModuleProblems compares the directories with the modules, "." being
// the root, which dependabot spells "/".
func dependabotModuleProblems(directories, modules []string) []string {
	want := make([]string, 0, len(modules))
	for _, m := range modules {
		if m == "." {
			want = append(want, "/")
		} else {
			want = append(want, "/"+m)
		}
	}
	var problems []string
	for _, w := range want {
		if !slices.Contains(directories, w) {
			problems = append(problems, fmt.Sprintf("the Go module %s has no gomod directory %q, so it gets no update pull requests", strings.TrimPrefix(w, "/"), w))
		}
	}
	for i, d := range directories {
		switch {
		case !slices.Contains(want, d):
			problems = append(problems, fmt.Sprintf("the gomod directory %q is no Go module go_modules lists", d))
		case slices.Contains(directories[:i], d):
			problems = append(problems, fmt.Sprintf("the gomod directory %q is listed twice", d))
		}
	}
	return problems
}

func TestDependabotGoDirectoriesRefusesWhatItCannotRead(t *testing.T) {
	const file = `version: 2
updates:
  - package-ecosystem: gomod
    # comment
    directories:
      - "/"

      - '/examples/a'
    schedule:
      interval: weekly
  - package-ecosystem: github-actions
    directory: "/"
    groups:
      ci:
        patterns:
          - "*"
`
	got, err := dependabotGoDirectories(strings.Split(file, "\n"))
	if err != nil || !slices.Equal(got, []string{"/", "/examples/a"}) {
		t.Errorf("dependabotGoDirectories = %q, %v; want [/ /examples/a]", got, err)
	}
	single := strings.Replace(file, "    directories:\n      - \"/\"\n\n      - '/examples/a'\n", "    directory: \"/\"\n", 1)
	if got, err := dependabotGoDirectories(strings.Split(single, "\n")); err != nil || !slices.Equal(got, []string{"/"}) {
		t.Errorf("a directory key read as %q, %v; want [/]", got, err)
	}
	refused := map[string]string{
		"no gomod entry":    strings.Replace(file, "ecosystem: gomod", "ecosystem: npm", 1),
		"two gomod entries": file + "  - package-ecosystem: gomod\n    directory: \"/x\"\n",
		"no directory":      strings.Replace(single, "    directory: \"/\"\n", "", 1),
	}
	for name, text := range refused {
		if got, err := dependabotGoDirectories(strings.Split(text, "\n")); err == nil {
			t.Errorf("%s: read as %q without an error", name, got)
		}
	}
}

func TestDependabotModuleProblems(t *testing.T) {
	modules := []string{".", "examples/a", "examples/b"}
	if got := dependabotModuleProblems([]string{"/", "/examples/a", "/examples/b"}, modules); len(got) != 0 {
		t.Errorf("matching lists reported %q", got)
	}
	got := dependabotModuleProblems([]string{"/", "/examples/a", "/examples/gone"}, modules)
	want := []string{
		`the Go module examples/b has no gomod directory "/examples/b", so it gets no update pull requests`,
		`the gomod directory "/examples/gone" is no Go module go_modules lists`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("problems = %q, want %q", got, want)
	}
	if got := dependabotModuleProblems([]string{"/examples/a", "/examples/b"}, modules); len(got) != 1 || !strings.Contains(got[0], `"/"`) {
		t.Errorf("a missing root reported %q, want one problem naming \"/\"", got)
	}
	twice := dependabotModuleProblems([]string{"/", "/examples/a", "/examples/b", "/"}, modules)
	if want := []string{`the gomod directory "/" is listed twice`}; !slices.Equal(twice, want) {
		t.Errorf("a directory listed twice reported %q, want %q", twice, want)
	}
}
