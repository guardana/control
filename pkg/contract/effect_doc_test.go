package contract

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// The effect table on docs/contracts.md is the set of authorization
// requirements an integrator reads before building an envelope, and until this
// test existed nothing connected it to effectRequirements. An audit falsified
// ten claims on that page one at a time and only one was caught; two of the ten
// were rows of this table, including "EFFECT_CLASS_TRANSACT requires nothing".
//
// A page that tells an integrator to omit a field this package refuses without
// produces a call that cannot be authorized, and the integrator has no reason
// to doubt the page.
func TestEffectTableIsDocumented(t *testing.T) {
	rows := effectRowsInDoc(t)

	for effect, want := range effectRequirements() {
		got, ok := rows[effect.String()]
		if !ok {
			t.Errorf("docs/contracts.md: no row for %s; every effect class this package requires fields for is a row an integrator needs", effect)
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("docs/contracts.md: %s documents %v, this package requires %v", effect, got, want)
		}
		delete(rows, effect.String())
	}
	for name := range rows {
		t.Errorf("docs/contracts.md: documents an effect class %q that this package has no requirements for", name)
	}
}

// effectRowsInDoc reads the table under the line that introduces it, returning
// each effect class named in the first cell against the fields in the second.
// A row naming several classes contributes one entry per class, because the
// page groups the three mutating effects and the code does not.
func effectRowsInDoc(t *testing.T) map[string][]string {
	t.Helper()

	const header = "| Effect class | Also required |"

	rows := make(map[string][]string)
	inTable := false
	for _, line := range strings.Split(readContractsPage(t), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == header:
			inTable = true
		case !inTable:
		case !strings.HasPrefix(trimmed, "|"):
			// The table ended at the first line that is not a row.
			return checkedNonEmptyRows(t, rows)
		case strings.Contains(trimmed, "---"):
		default:
			cells := strings.Split(strings.Trim(trimmed, "|"), "|")
			if len(cells) != 2 {
				t.Errorf("docs/contracts.md: effect row has %d cells, want 2: %s", len(cells), trimmed)
				continue
			}
			for _, name := range backtickedList(cells[0]) {
				rows[name] = backtickedList(cells[1])
			}
		}
	}
	return checkedNonEmptyRows(t, rows)
}

// checkedNonEmptyRows fails rather than return an empty table: a scanner that
// found nothing has stopped understanding the page, and comparing against
// nothing would report every row as agreeing.
func checkedNonEmptyRows(t *testing.T, rows map[string][]string) map[string][]string {
	t.Helper()
	if len(rows) < len(effectRequirements()) {
		t.Fatalf("docs/contracts.md: read %d effect row(s), want %d; the table moved or changed shape",
			len(rows), len(effectRequirements()))
	}
	return rows
}

// backtickedList returns the backquoted tokens of a cell, in the order written.
func backtickedList(cell string) []string {
	var found []string
	for _, part := range strings.Split(cell, "`") {
		if part = strings.TrimSpace(part); part != "" && !strings.HasPrefix(part, ",") {
			found = append(found, part)
		}
	}
	return found
}

// readContractsPage resolves the page from this file's own compiled-in path,
// never the working directory, and refuses to guess.
func readContractsPage(t *testing.T) string {
	t.Helper()

	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve this file's own path; refusing to fall back on the working directory")
	}
	page := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(self))), "docs", "contracts.md")
	data, err := os.ReadFile(page) //nolint:gosec // the path is built from this file's own location, not from input
	if err != nil {
		t.Fatalf("reading %s: %v", page, err)
	}
	return string(data)
}

// TestNoEffectRowRepeatsAnAlwaysRequiredField: a row naming a field every
// envelope requires is a check that has already run, and on the page it reads
// as something the class needs. Such a row can also hide that a field a check
// compares, a tenant of the cross-tenant check say, is required by nothing.
// The list is checkSemantics' own, copied: the property is about the two
// lists in this package, and the copy fails when the table drifts back.
func TestNoEffectRowRepeatsAnAlwaysRequiredField(t *testing.T) {
	always := []string{"request_id", "project_id", "tenant_id", "principal.id", "action.name", "occurred_at"}
	for effect, paths := range effectRequirements() {
		for _, path := range paths {
			if slices.Contains(always, path) {
				t.Errorf("%s requires %s, which every envelope requires already", effect, path)
			}
		}
	}
}

// A guard against the enum growing a value nobody documented or required.
func TestEveryDeclaredEffectHasRequirements(t *testing.T) {
	values := controlv1.EffectClass(0).Descriptor().Values()
	required := effectRequirements()

	for i := range values.Len() {
		effect := controlv1.EffectClass(values.Get(i).Number())
		if effect == controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED {
			continue // never valid on an envelope; validation refuses it before this table
		}
		if _, ok := required[effect]; !ok {
			t.Errorf("%s is declared by the contract and this package requires nothing for it; an effect nobody stated requirements for is one nobody decided on", effect)
		}
	}
}
