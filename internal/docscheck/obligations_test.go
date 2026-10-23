package docscheck

import (
	"bytes"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/docscheck/obligationdoc"
	"github.com/guardana/control/internal/policy/rules"
)

const (
	obligationsDoc     = "docs/reference/obligations.md"
	obligationsCommand = "make docs-gen"
)

// The pin between the catalogue and the page. The renderer is called
// directly, not through `go run`: a subprocess resolves its program through
// $PATH, and a shim that printed the committed page would pass while nothing
// rendered.
func TestObligationsDocIsCurrent(t *testing.T) {
	got, err := obligationdoc.Page(rules.Obligations(), obligationdoc.Appliers())
	if err != nil {
		t.Fatalf("rendering the page: %v", err)
	}
	want, err := fs.ReadFile(repoFS(t), obligationsDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", obligationsDoc, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what the renderer produces; rebuild it with `%s`\n%s",
			obligationsDoc, obligationsCommand, firstDifferingLine(got, want))
	}
}

// A parameter the catalogue gains without the page being rebuilt leaves the
// committed page different from what the renderer produces, which is what
// the pin above refuses.
func TestObligationsDocPinCatchesANewParameter(t *testing.T) {
	want, err := fs.ReadFile(repoFS(t), obligationsDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", obligationsDoc, err)
	}
	specs := rules.Obligations()
	specs[0].Params = append(specs[0].Params, rules.Param{Name: "unrendered", Grammar: rules.GrammarInteger})
	got, err := obligationdoc.Page(specs, obligationdoc.Appliers())
	if err != nil {
		t.Fatalf("rendering the page: %v", err)
	}
	if bytes.Equal(got, want) {
		t.Errorf("a parameter added to %s renders the committed page unchanged", specs[0].Type)
	}
}

// The committed page's Parameter column, read as a reader reads it, holds
// exactly the parameters rules.ParamNames gives each type, in its order, and
// `none` for a type that takes none.
func TestObligationsDocParametersAreTheCatalogues(t *testing.T) {
	page, err := fs.ReadFile(repoFS(t), obligationsDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", obligationsDoc, err)
	}
	column := paramsColumn(t, string(page))
	for _, spec := range rules.Obligations() {
		want, ok := rules.ParamNames(spec.Type)
		if !ok {
			t.Fatalf("ParamNames does not know %s", spec.Type)
		}
		if len(want) == 0 {
			want = []string{"none"}
		}
		if got := column[spec.Type]; !slices.Equal(got, want) {
			t.Errorf("%s: the page lists %v, the catalogue %v", spec.Type, got, want)
		}
		delete(column, spec.Type)
	}
	for typ := range column {
		t.Errorf("the page has rows for %s, which the catalogue does not hold", typ)
	}
}

// paramsColumn reads the page's table into each type's Parameter cells, in
// row order, a parameter's code span unwrapped.
func paramsColumn(t *testing.T, page string) map[string][]string {
	t.Helper()
	if !strings.Contains(page, "\n| Type | Applied by | Parameter | Required | Value |\n") {
		t.Fatalf("%s has no Parameter column", obligationsDoc)
	}
	column := map[string][]string{}
	for line := range strings.SplitSeq(page, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "| "), " |"), " | ")
		if len(cells) != 5 {
			t.Fatalf("row %q has %d cells, want 5", line, len(cells))
		}
		typ := strings.Trim(cells[0], "`")
		column[typ] = append(column[typ], strings.Trim(cells[2], "`"))
	}
	if len(column) == 0 {
		t.Fatalf("%s holds no rows", obligationsDoc)
	}
	return column
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
