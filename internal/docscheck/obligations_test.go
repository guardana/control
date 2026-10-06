package docscheck

import (
	"bytes"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/docscheck/obligationdoc"
	"github.com/guardana/control/internal/gateway"
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

// A parameter an applier gains without the page being rebuilt leaves the
// committed page different from what the renderer produces, which is what
// the pin above refuses.
func TestObligationsDocPinCatchesANewParameter(t *testing.T) {
	src, err := fs.ReadFile(repoFS(t), obligationsCatalogue)
	if err != nil {
		t.Fatalf("reading %s: %v", obligationsCatalogue, err)
	}
	want, err := fs.ReadFile(repoFS(t), obligationsDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", obligationsDoc, err)
	}
	appliers := obligationdoc.Appliers()
	gainer := ""
	for _, a := range appliers {
		if len(a.Types) > 0 {
			a.Params[a.Types[0]] = append(a.Params[a.Types[0]], "unrendered")
			gainer = a.Name
			break
		}
	}
	if gainer == "" {
		t.Fatal("no applier declares a type to add a parameter to")
	}
	got, err := obligationdoc.Page(src, appliers)
	if err != nil {
		t.Fatalf("rendering the page: %v", err)
	}
	if bytes.Equal(got, want) {
		t.Errorf("a parameter added to %s's table renders the committed page unchanged", gainer)
	}
}

// The committed page's Parameters column, read as a reader reads it, holds
// exactly the parameters each applier's own table declares: `none` for a type
// that reads none and an empty cell for a type nothing applies. The expected
// lists come from the appliers' packages, not from the renderer.
func TestObligationsDocParametersAreTheTables(t *testing.T) {
	declared := declaredParams(t)
	page, err := fs.ReadFile(repoFS(t), obligationsDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", obligationsDoc, err)
	}
	cells := paramsColumn(t, string(page))
	for typ, cell := range cells {
		want, applied := declared[typ]
		if err := checkParamsCell(cell, want, applied); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
	for typ := range declared {
		if _, ok := cells[typ]; !ok {
			t.Errorf("%s declares parameters and has no row on the page", typ)
		}
	}
}

// declaredParams merges the appliers' parameter tables, refusing a type two
// appliers read differently.
func declaredParams(t *testing.T) map[string][]string {
	t.Helper()
	declared := map[string][]string{}
	for _, table := range []map[string][]string{gateway.ObligationParams(), mcp.ObligationParams()} {
		for typ, params := range table {
			if prior, ok := declared[typ]; ok && !slices.Equal(prior, params) {
				t.Fatalf("%s is applied twice with different parameters: %v and %v", typ, prior, params)
			}
			declared[typ] = params
		}
	}
	return declared
}

// paramsColumn reads the page's table into each type's Parameters cell.
func paramsColumn(t *testing.T, page string) map[string]string {
	t.Helper()
	if !strings.Contains(page, "\n| Type | Applied by | Parameters |\n") {
		t.Fatalf("%s has no Parameters column", obligationsDoc)
	}
	column := map[string]string{}
	for line := range strings.SplitSeq(page, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "| "), " |"), " | ")
		if len(cells) != 3 {
			t.Fatalf("row %q has %d cells, want 3", line, len(cells))
		}
		column[strings.Trim(cells[0], "`")] = cells[2]
	}
	if len(column) == 0 {
		t.Fatalf("%s holds no rows", obligationsDoc)
	}
	return column
}

// checkParamsCell holds one cell to the parameters its type's applier reads.
func checkParamsCell(cell string, want []string, applied bool) error {
	switch {
	case !applied:
		if cell != "" {
			return fmt.Errorf("nothing applies it, yet the page lists parameters %q", cell)
		}
	case len(want) == 0:
		if cell != "none" {
			return fmt.Errorf("reads no parameter, the page says %q", cell)
		}
	default:
		var got []string
		for p := range strings.SplitSeq(cell, ", ") {
			got = append(got, strings.Trim(p, "`"))
		}
		if !slices.Equal(got, want) {
			return fmt.Errorf("reads %v, the page says %v", want, got)
		}
	}
	return nil
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
