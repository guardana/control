package docscheck

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/guardana/control/internal/docscheck/obligationdoc"
	"github.com/guardana/control/internal/policy/rules"
)

const (
	obligationsDoc       = "docs/reference/obligations.md"
	obligationsCatalogue = "internal/policy/rules/catalogue.go"
	obligationsCommand   = "make docs-gen"
)

// The pin between the catalogue and the page. The renderer is called
// directly, not through `go run`: a subprocess resolves its program through
// $PATH, and a shim that printed the committed page would pass while nothing
// rendered. The names the renderer read out of the source are each held to
// the catalogue's own membership test, so a read of the wrong list cannot
// pin a page the parser would not honour.
func TestObligationsDocIsCurrent(t *testing.T) {
	fsys := repoFS(t)
	src, err := fs.ReadFile(fsys, obligationsCatalogue)
	if err != nil {
		t.Fatalf("reading %s: %v", obligationsCatalogue, err)
	}
	names, err := obligationdoc.Names(src)
	if err != nil {
		t.Fatalf("reading the catalogue: %v", err)
	}
	for _, name := range names {
		if !rules.KnownObligation(name) {
			t.Errorf("%q was read out of the catalogue's source and the catalogue does not know it", name)
		}
	}
	got, err := obligationdoc.Page(src, obligationdoc.Appliers())
	if err != nil {
		t.Fatalf("rendering the page: %v", err)
	}
	want, err := fs.ReadFile(fsys, obligationsDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", obligationsDoc, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the renderer produces; rebuild it with `%s`\n%s",
			obligationsDoc, obligationsCommand, firstDifferingLine(got, want))
	}
}

// firstDifferingLine names the first line that differs, quoted, so a
// trailing space reads as a stale page rather than as a broken test.
func firstDifferingLine(got, want []byte) string {
	gotLines := strings.Split(string(got), "\n")
	wantLines := strings.Split(string(want), "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		g, w := "<end of file>", "<end of file>"
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			return fmt.Sprintf("first difference at line %d\nrendered:  %q\ncommitted: %q", i+1, g, w)
		}
	}
	return "the pages differ in trailing bytes only"
}
