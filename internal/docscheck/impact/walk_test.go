package impact

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

const goodPage = "---\ntitle: The guide\nsummary: How to run it.\ntype: how-to\ncovers: [cmd/**]\n---\n\n# The guide\n"

func TestWalkPagesParsesEveryPageTheConfigurationAdmits(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/guides/run.md":    {Data: []byte(goodPage)},
		"docs/README.md":        {Data: []byte("# no frontmatter here\n")},
		"docs/notes.txt":        {Data: []byte(goodPage)},
		"docs/plans/plan.md":    {Data: []byte("not a page\n")},
		"docs/foundation.md":    {Data: []byte("not a page\n")},
		"docs/reference/bad.md": {Data: []byte("# missing frontmatter\n")},
	}
	pages, broken, err := WalkPages(fsys, "docs", []string{"docs/plans/", "docs/foundation.md"})
	if err != nil {
		t.Fatalf("WalkPages: %v", err)
	}
	if len(pages) != 1 || pages[0].Path != "docs/guides/run.md" || strings.Join(pages[0].Covers, ",") != "cmd/**" {
		t.Errorf("pages = %+v, want the guide alone with its covers", pages)
	}
	if len(broken) != 1 || broken[0].Path != "docs/reference/bad.md" || broken[0].Err == nil {
		t.Errorf("broken = %+v, want the one page without frontmatter and its reason", broken)
	}
}

func TestWalkPagesNamesEveryBrokenPageWhenNoneParsed(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/a.md":          {Data: []byte("# a\n")},
		"docs/b.md":          {Data: []byte("---\ntitle: b\n")},
		"docs/sub/c.md":      {Data: []byte("---\nbogus: c\n---\n")},
		"docs/sub/README.md": {Data: []byte("# readme\n")},
	}
	_, _, err := WalkPages(fsys, "docs", nil)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("WalkPages = %v, want ErrInvalid", err)
	}
	for _, want := range []string{"3 pages", "docs/a.md", "docs/b.md", "docs/sub/c.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestWalkPagesRefusesADirectoryWithNoPage(t *testing.T) {
	fsys := fstest.MapFS{"docs/README.md": {Data: []byte("# readme\n")}}
	_, _, err := WalkPages(fsys, "docs", nil)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "no page") {
		t.Errorf("WalkPages = %v, want ErrInvalid saying no page", err)
	}
	if _, _, err := WalkPages(fsys, "missing", nil); err == nil {
		t.Error("WalkPages over a directory that does not exist passed")
	}
}

func TestWalkFilesLeavesOutWhatTheRepositoryIgnores(t *testing.T) {
	kept := []string{
		"Makefile", "cmd/gw/main.go", ".github/workflows/ci.yml", ".env.example",
		"bench/results/.keep", "docs/design/other.md", "go.work.example",
	}
	dropped := []string{
		".git/HEAD", ".tooling/x", "bin/gw", "dist/gw", "coverage/c.out", "node_modules/m/i.js",
		"docs/foundation/spec.md", "docs/plans/p.md", "cmd/.DS_Store", "cmd/gw.test",
		"cmd/cover.out", ".env", ".env.local", "go.work", "go.work.sum", "AGENTS.local.md",
		"bench/results/run.txt", "bench/results/deep/run.txt", "docs/design/foundation-decisions.md",
	}
	fsys := fstest.MapFS{}
	for _, p := range slices.Concat(kept, dropped) {
		fsys[p] = &fstest.MapFile{Data: []byte("x")}
	}
	got, err := WalkFiles(fsys)
	if err != nil {
		t.Fatalf("WalkFiles: %v", err)
	}
	slices.Sort(kept)
	if !slices.Equal(got, kept) {
		t.Errorf("WalkFiles = %q\nwant %q", got, kept)
	}
}

func TestWalkFilesRefusesAnEmptyTree(t *testing.T) {
	_, err := WalkFiles(fstest.MapFS{"bin/gw": {Data: []byte("x")}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "no file") {
		t.Errorf("WalkFiles = %v, want ErrInvalid saying no file", err)
	}
}
