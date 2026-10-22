//go:build unix

package observelog

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
)

const (
	runX = "run-0123456789abcdef0123456789abcdef"
	runY = "run-11111111111111111111111111111111"
	runZ = "run-22222222222222222222222222222222"
)

// claiming is the span's observation with the tool named name, claimed for
// run, or claiming none when run is "".
func claiming(span, name, run string) *observev1.Observation {
	o := observation(span, name, "2026-10-04T10:05:00Z")
	if run != "" {
		o.Correlation.Basis, o.Correlation.RunId = observev1.Basis_BASIS_CLAIMED, run
	}
	return o
}

// lastReport is the report the log's last line holds.
func lastReport(t *testing.T, dir string) *observev1.ImportReport {
	t.Helper()
	recs := records(t, filepath.Join(dir, FileName))
	return recs[len(recs)-1].GetImportReport()
}

// TestAConflictPutsTheRunItsRefusedCopyClaimsInDoubt: the report of a write
// names, beside the runs the import's own report named, the runs claimed by
// the copies the log refused as conflicts, not by those it kept or found
// duplicates. A refused copy that claims no run puts every run in doubt.
func TestAConflictPutsTheRunItsRefusedCopyClaimsInDoubt(t *testing.T) {
	for name, c := range map[string]struct {
		named []string
		every bool
		obs   []*observev1.Observation
		want  []string
		all   bool
	}{
		"no conflict": {nil, false, []*observev1.Observation{claiming(spanB, "read_file", runY), claiming(spanB, "read_file", runY)}, nil, false},
		"a refused copy claiming Z": {nil, false, []*observev1.Observation{claiming(spanB, "read_file", runY),
			claiming(spanB, "drop_table", runZ), claiming(spanB, "drop_table", runZ)}, []string{runZ}, false},
		"the import's own and a conflict's": {[]string{runY}, false, []*observev1.Observation{claiming(spanA, "drop_table", runX)},
			[]string{runX, runY}, false},
		"a refused copy claiming none": {nil, false, []*observev1.Observation{claiming(spanA, "drop_table", "")}, nil, true},
		"the import's every run":       {nil, true, []*observev1.Observation{claiming(spanA, "drop_table", runX)}, nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			dir := logDir(t)
			l := openLog(t, dir)
			write(t, l, report("2026-10-04T10:05:00Z"), claiming(spanA, "read_file", runY))
			rep := report("2026-10-04T10:06:00Z")
			rep.DoubtedRunIds, rep.EveryRunDoubted = c.named, c.every
			write(t, l, rep, c.obs...)
			got := lastReport(t, dir)
			if got.GetEveryRunDoubted() != c.all || !slices.Equal(got.GetDoubtedRunIds(), c.want) {
				t.Fatalf("every %v, runs %v; want %v, %v", got.GetEveryRunDoubted(), got.GetDoubtedRunIds(), c.all, c.want)
			}
		})
	}
}

// TestAConflictPastTheBoundPutsEveryRunInDoubt: an import that named 64
// runs and a conflict that claims a 65th names none, and every run is in
// doubt.
func TestAConflictPastTheBoundPutsEveryRunInDoubt(t *testing.T) {
	var named []string
	for i := 1; i <= 64; i++ {
		named = append(named, fmt.Sprintf("run-%032x", i+0x100))
	}
	for name, c := range map[string]struct {
		run   string
		every bool
	}{"a run already named": {named[0], false}, "a 65th run": {runX, true}} {
		t.Run(name, func(t *testing.T) {
			dir := logDir(t)
			l := openLog(t, dir)
			write(t, l, report("2026-10-04T10:05:00Z"), claiming(spanA, "read_file", runY))
			rep := report("2026-10-04T10:06:00Z")
			rep.DoubtedRunIds = slices.Clone(named)
			write(t, l, rep, claiming(spanA, "drop_table", c.run))
			if got := lastReport(t, dir); got.GetEveryRunDoubted() != c.every || (len(got.GetDoubtedRunIds()) == 64) == c.every {
				t.Fatalf("every %v, %d runs named; want every %v", got.GetEveryRunDoubted(), len(got.GetDoubtedRunIds()), c.every)
			}
		})
	}
}
