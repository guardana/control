package observe_test

import (
	"fmt"
	"slices"
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/observe"
)

func runN(i int) string { return fmt.Sprintf("run-%032x", i) }

func runsUpTo(n int) []string {
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, runN(i))
	}
	return out
}

// TestTheCodecHoldsARunsInDoubtToOneForm: run ids in ascending order, each
// once, at most 64, and none beside every_run_doubted.
func TestTheCodecHoldsARunsInDoubtToOneForm(t *testing.T) {
	for name, c := range map[string]struct {
		runs   []string
		every  bool
		member string
	}{
		"64 run ids":              {runsUpTo(64), false, ""},
		"65 run ids":              {runsUpTo(65), false, "doubted_run_ids"},
		"every run":               {nil, true, ""},
		"every run and a run id":  {runsUpTo(1), true, "doubted_run_ids"},
		"two out of order":        {[]string{runN(2), runN(1)}, false, "doubted_run_ids[1]"},
		"one twice":               {[]string{runN(1), runN(1)}, false, "doubted_run_ids[1]"},
		"not a run id":            {[]string{"run-1"}, false, "doubted_run_ids[0]"},
		"an upper-case run id":    {[]string{"run-0000000000000000000000000000000A"}, false, "doubted_run_ids[0]"},
		"a run id and none after": {[]string{runN(1), ""}, false, "doubted_run_ids[1]"},
	} {
		r := fixtureReport()
		r.DoubtedRunIds, r.EveryRunDoubted = c.runs, c.every
		checkBothWays(t, name, reportRecord(r), c.member)
	}
}

func filled(d *observe.Doubt) (bool, []string) {
	r := &observev1.ImportReport{DoubtedRunIds: []string{runN(99)}, EveryRunDoubted: true}
	d.Fill(r)
	return r.GetEveryRunDoubted(), r.GetDoubtedRunIds()
}

// TestADoubtNamesItsRunsSortedAndOnce: the runs claimed and merged, sorted
// and once each.
func TestADoubtNamesItsRunsSortedAndOnce(t *testing.T) {
	var none observe.Doubt
	if every, runs := filled(&none); every || runs != nil {
		t.Errorf("no claim: every %v, runs %v", every, runs)
	}
	var some observe.Doubt
	for _, run := range []string{runN(3), runN(1), runN(3)} {
		some.Claim(run)
	}
	some.Merge(&observev1.ImportReport{DoubtedRunIds: []string{runN(2)}})
	if every, runs := filled(&some); every || !slices.Equal(runs, []string{runN(1), runN(2), runN(3)}) {
		t.Errorf("three runs: every %v, runs %v", every, runs)
	}
	var claimed observe.Doubt
	claimed.ClaimOf(&observev1.Observation{Correlation: &observev1.Correlation{Basis: observev1.Basis_BASIS_CLAIMED, RunId: runN(5)}})
	if every, runs := filled(&claimed); every || !slices.Equal(runs, []string{runN(5)}) {
		t.Errorf("a claimed observation: every %v, runs %v", every, runs)
	}
}

// TestADoubtOfNoRunOrPastItsBoundIsEveryRun: a claim of no run, of a value
// that is no run id or of a 65th run puts every run in doubt; 64 are named.
func TestADoubtOfNoRunOrPastItsBoundIsEveryRun(t *testing.T) {
	for name, claim := range map[string]func(d *observe.Doubt){
		"no run":       func(d *observe.Doubt) { d.Claim("") },
		"not a run id": func(d *observe.Doubt) { d.Claim("run-1") },
		"an observation that claims none": func(d *observe.Doubt) {
			d.ClaimOf(&observev1.Observation{Correlation: &observev1.Correlation{Basis: observev1.Basis_BASIS_NONE}})
		},
		"a report of every run": func(d *observe.Doubt) { d.Merge(&observev1.ImportReport{EveryRunDoubted: true}) },
		"a 65th run": func(d *observe.Doubt) {
			for _, run := range runsUpTo(65) {
				d.Claim(run)
			}
		},
	} {
		var d observe.Doubt
		d.Claim(runN(7))
		claim(&d)
		d.Claim(runN(8))
		if every, runs := filled(&d); !every || runs != nil {
			t.Errorf("%s: every %v, runs %v; want every run", name, every, runs)
		}
	}
	var full observe.Doubt
	for _, run := range runsUpTo(64) {
		full.Claim(run)
	}
	if every, runs := filled(&full); every || len(runs) != 64 || runs[63] != runN(64) {
		t.Errorf("64 runs: every %v, %d runs", every, len(runs))
	}
}

// TestDoubtedRunsReadsEveryReport: the runs of every report add up; one of
// every run, or one that counts a refusal or a conflict and names no run,
// puts every run in doubt.
func TestDoubtedRunsReadsEveryReport(t *testing.T) {
	report := func(counts *observev1.ImportCounts, every bool, runs ...string) *observev1.Record {
		return reportRecord(&observev1.ImportReport{Counts: counts, EveryRunDoubted: every, DoubtedRunIds: runs})
	}
	refused, conflict := &observev1.ImportCounts{Refused: 1}, &observev1.ImportCounts{Conflict: 2}
	seen := obsRecord(fixtureObservation())
	for name, c := range map[string]struct {
		records []*observev1.Record
		every   bool
		runs    []string
	}{
		"none":                     {[]*observev1.Record{seen, report(nil, false)}, false, nil},
		"two reports":              {[]*observev1.Record{report(refused, false, runN(2)), seen, report(conflict, false, runN(1), runN(2))}, false, []string{runN(1), runN(2)}},
		"every run":                {[]*observev1.Record{report(refused, false, runN(2)), report(nil, true)}, true, nil},
		"a refusal naming no run":  {[]*observev1.Record{report(refused, false)}, true, nil},
		"a conflict naming no run": {[]*observev1.Record{report(nil, false, runN(1)), report(conflict, false)}, true, nil},
	} {
		every, runs := observe.DoubtedRuns(c.records)
		if every != c.every || !slices.Equal(runs, c.runs) {
			t.Errorf("%s: every %v, runs %v; want %v, %v", name, every, runs, c.every, c.runs)
		}
	}
}
