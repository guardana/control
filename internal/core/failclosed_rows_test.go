package core_test

import (
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
)

// The listing is the rule evaluated over its whole domain: every declared
// effect and one undeclared number, both values of each cause and of the
// setting, a snapshot present and absent, every declared verdict. The counts
// are computed here from the descriptors, independently of the listing.
func TestFailClosedRowsCoverTheWholeDomain(t *testing.T) {
	rows := core.FailClosedRows()
	effects := controlv1.EffectClass(0).Descriptor().Values().Len()
	verdicts := controlv1.Verdict(0).Descriptor().Values().Len()
	if want := (effects + 1) * 2 * 2 * 2 * 2 * verdicts; len(rows) != want {
		t.Fatalf("%d rows, want %d", len(rows), want)
	}
	seen := map[core.FailClosedRow]bool{}
	undeclared := 0
	for _, row := range rows {
		key := row
		key.Action, key.OpenedRead = 0, false
		if seen[key] {
			t.Errorf("cell listed twice: %+v", key)
		}
		seen[key] = true
		if !row.Declared {
			undeclared++
			if row.Effect.Descriptor().Values().ByNumber(row.Effect.Number()) != nil {
				t.Errorf("row says %s is undeclared and the descriptor declares it", row.Effect)
			}
		}
	}
	if undeclared != 2*2*2*2*verdicts {
		t.Errorf("%d rows for the undeclared effect, want %d", undeclared, 2*2*2*2*verdicts)
	}
}

// The cells ADR-0012 names, typed here from the record: every material call
// blocks; the one open cell is a READ whose only causes are in the policy's
// availability, under the setting, with no snapshot or a determinate ALLOW.
func TestFailClosedRowsNameTheRecordsCells(t *testing.T) {
	opened := 0
	for _, row := range core.FailClosedRows() {
		if problem := cellProblem(row); problem != "" {
			t.Errorf("%s: %+v", problem, row)
		}
		if row.OpenedRead {
			opened++
		}
	}
	// No snapshot with any of the verdicts, or a snapshot with ALLOW.
	verdicts := controlv1.Verdict(0).Descriptor().Values().Len()
	if want := verdicts + 1; opened != want {
		t.Errorf("%d open cells, want %d", opened, want)
	}
}

// cellProblem restates the record's rule for one cell and compares.
func cellProblem(row core.FailClosedRow) string {
	switch open := shouldOpen(row); {
	case open && (row.Action != core.Execute || !row.OpenedRead):
		return "the open cell blocks"
	case !open && (row.Action != core.Block || row.OpenedRead):
		return "a blocking cell runs"
	}
	return ""
}

// shouldOpen is the record's one open cell: a declared READ whose only causes
// are in the policy's availability, under the setting, with no snapshot or a
// determinate ALLOW.
func shouldOpen(row core.FailClosedRow) bool {
	read := row.Declared && row.Effect == controlv1.EffectClass_EFFECT_CLASS_READ
	causes := !row.Input && row.Availability && row.FailOpenRead
	return read && causes && (!row.Snapshot || row.Determinate == controlv1.Verdict_VERDICT_ALLOW)
}
