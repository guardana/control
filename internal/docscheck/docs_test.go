// The documentation consistency gate: a link that stops resolving, a capability
// claim that loses its status label, a project law file that grew past what
// anyone reads. A green run over an empty set is the failure this file exists to
// prevent, so the walk also proves it examined something.
package docscheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	agentsLineLimit  = 200
	minMarkdownFiles = 5
	minComponentRows = 10 // fewer means the table was not parsed, not that it emptied
)

// Local planning material: not part of the project, so its links and claims are
// out of scope. Repository-relative slash paths.
var skipped = []string{"docs/foundation", "docs/plans", "docs/design/foundation-decisions.md", "node_modules"}

var (
	// Exactly one group is set: an inline link or image, or a reference
	// definition. Fenced code is not exempt, because a path shown in an example
	// is still a path a reader will follow.
	linkTarget     = regexp.MustCompile(`\]\(([^)]*)\)|^\[[^\]]+\]:\s*(\S+)`)
	uriScheme      = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
	tableSeparator = regexp.MustCompile(`^\s*\|[\s:|-]*-[\s:|-]*\|\s*$`)
	claimPhrase    = regexp.MustCompile(`supports |provides |integrates with `)
	statusLabel    = regexp.MustCompile("implemented|experimental|planned")
	allowedStatus  = map[string]bool{"`implemented`": true, "`experimental`": true, "`planned`": true}
	componentHead  = "Component | Status | Where"
)

// repoFS resolves the repository from this file's own compiled-in path rather
// than the working directory, and refuses to guess: a root with no go.mod means
// the gate would examine some other tree and report a pass over nothing.
func repoFS(t *testing.T) fs.FS {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve this file's own path; refusing to fall back on the working directory")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(self)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root %q holds no go.mod: %v", root, err)
	}
	return os.DirFS(root)
}

// markdownFiles returns every Markdown file as a repository-relative slash
// path. It walks the directory, not the repository: a Markdown file .gitignore
// names is judged in a work tree and absent in an export. A false red in a work
// tree is accepted because the alternative, dropping files by a hand-mirrored
// ignore list, would let a tracked page escape the check.
func markdownFiles(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var found []string
	err := fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, walkErr error) error {
		switch {
		case walkErr != nil:
			return walkErr
		case d.IsDir():
			// Dot-directories are pruned generically, with .github the one named
			// exception, as in scripts/lib/repo-files.sh: its templates ship
			// publicly and rot like any other document.
			hidden := strings.HasPrefix(d.Name(), ".") && rel != ".github"
			if rel != "." && (hidden || slices.Contains(skipped, rel)) {
				return fs.SkipDir
			}
		case strings.HasSuffix(rel, ".md") && !slices.Contains(skipped, rel):
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	return found
}

func readLines(t *testing.T, fsys fs.FS, rel string) []string {
	t.Helper()
	data, err := fs.ReadFile(fsys, rel)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
}

func tableCells(line string) []string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

// componentTable returns docs/status.md and the line indexes of the pipe rows
// under "## Components". A section that went missing or lost its rows is fatal,
// so neither test below can pass over nothing.
func componentTable(t *testing.T) ([]string, []int) {
	t.Helper()
	lines := readLines(t, repoFS(t), "docs/status.md")
	var table []int
	in := false
	for i, line := range lines {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "## ") {
			in = s == "## Components"
		} else if in && strings.HasPrefix(s, "|") {
			table = append(table, i)
		}
	}
	if len(table) < minComponentRows+2 {
		t.Fatalf(`docs/status.md: "## Components" holds %d table lines, want a header, a separator and at least %d rows`, len(table), minComponentRows)
	}
	return lines, table
}

// TestAgentsFileStaysShort keeps the project law file readable in one sitting.
func TestAgentsFileStaysShort(t *testing.T) {
	if n := len(readLines(t, repoFS(t), "AGENTS.md")); n > agentsLineLimit {
		t.Errorf("AGENTS.md is %d lines, limit is %d", n, agentsLineLimit)
	}
}

// TestLocalMarkdownLinksResolve reports every broken link, not just the first:
// a path that does not exist, and a #fragment that names no heading of the
// Markdown file it points into.
func TestLocalMarkdownLinksResolve(t *testing.T) {
	fsys := repoFS(t)
	anchors := newAnchorIndex(fsys)
	for _, rel := range markdownFiles(t, fsys) {
		for i, line := range readLines(t, fsys, rel) {
			for _, m := range linkTarget.FindAllStringSubmatch(line, -1) {
				if problem := linkProblem(anchors, rel, m[1]+m[2]); problem != "" {
					t.Errorf("%s:%d: %s", rel, i+1, problem)
				}
			}
		}
	}
}

// TestCapabilityClaims fails a capability sentence carrying no status label, in
// every Markdown file the walk finds: a claim on a page nobody thought to list
// is still read as a claim. Fenced code and table separators are not prose and
// are skipped.
//
// What this does not do: it cannot tell whether a label that is present is
// true. A page that calls a planned thing `implemented` passes here, and on
// the reference pages that carry the project's honesty claim that is held by
// review, not by this gate. Widening claimPhrase would not fix it either,
// because the sentence that lies need not contain any of these verbs.
func TestCapabilityClaims(t *testing.T) {
	fsys := repoFS(t)
	for _, rel := range markdownFiles(t, fsys) {
		fenced := false
		for i, line := range readLines(t, fsys, rel) {
			switch {
			case strings.HasPrefix(strings.TrimSpace(line), "```"):
				fenced = !fenced
			case fenced || tableSeparator.MatchString(line):
			case claimPhrase.MatchString(line) && !statusLabel.MatchString(line):
				t.Errorf("%s:%d: capability claim with no status label: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
		if fenced {
			t.Errorf("%s: unclosed code fence; every claim after it would go unchecked", rel)
		}
	}
}

// TestGateExaminedSomething states the false-green case as its own test: a walk
// that found nothing must never read as documentation in order.
func TestGateExaminedSomething(t *testing.T) {
	fsys := repoFS(t)
	if n := len(markdownFiles(t, fsys)); n < minMarkdownFiles {
		t.Errorf("walk found %d Markdown files, want at least %d", n, minMarkdownFiles)
	}
	for _, rel := range []string{"README.md", "AGENTS.md", "ROADMAP.md", "docs/status.md"} {
		if _, err := fs.Stat(fsys, rel); err != nil {
			t.Errorf("required document %s is missing: %v", rel, err)
		}
	}
}

// TestStatusComponentTableShape fails on a renamed or reordered column and a
// missing separator: each makes the label check read the wrong cell, or none.
func TestStatusComponentTableShape(t *testing.T) {
	lines, table := componentTable(t)
	if got := strings.Join(tableCells(lines[table[0]]), " | "); got != componentHead {
		t.Errorf("docs/status.md:%d: header is %q, want %q", table[0]+1, got, componentHead)
	}
	if !tableSeparator.MatchString(lines[table[1]]) {
		t.Errorf("docs/status.md:%d: want the header separator row, got %q", table[1]+1, strings.TrimSpace(lines[table[1]]))
	}
}

// TestStatusComponentTableLabels is the load-bearing check. The root documents
// no longer keep their own inventories, they all point here, so this table is
// the only place the project states what is implemented, experimental or
// planned; a row that loses its label takes the honesty claim with it.
func TestStatusComponentTableLabels(t *testing.T) {
	lines, table := componentTable(t)
	for _, i := range table[2:] {
		if cells := tableCells(lines[i]); len(cells) != 3 {
			t.Errorf("docs/status.md:%d: row has %d cells, want 3: %s", i+1, len(cells), strings.TrimSpace(lines[i]))
		} else if !allowedStatus[cells[1]] {
			t.Errorf("docs/status.md:%d: %q has status %q, want one of `implemented`, `experimental`, `planned`", i+1, cells[0], cells[1])
		}
	}
}
