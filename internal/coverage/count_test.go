package coverage_test

import (
	"fmt"
	"testing"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/coverage"
)

// countBound is the bound of counting steps the reference page prints.
const countBound = 1 << 22

const stepsWhy = "counting the spans below it passed its source's bound of 4194304 steps"

func TestTheCountBoundIsThePagesBound(t *testing.T) {
	if coverage.MaxCountSteps != countBound {
		t.Fatalf("MaxCountSteps %d, the reference page prints %d", coverage.MaxCountSteps, countBound)
	}
}

// ladder is n diamonds of traceA below spanAt(0): rung i holds two spans
// under the span above it and one span claiming both as its parents, so the
// spans below spanAt(0) number 3n, while a count that takes a span once per
// way to it doubles with each rung. It returns the records and the span
// closing each rung, the deepest last.
func ladder(n int) ([]*observev1.Record, []string) {
	records := make([]*observev1.Record, 0, 4*n)
	rungs := make([]string, 0, n)
	above := spanAt(0)
	for i := 1; i <= n; i++ {
		left, right, below := spanAt(3*i-2), spanAt(3*i-1), spanAt(3*i)
		for _, edge := range [][2]string{{left, above}, {right, above}, {below, left}, {below, right}} {
			records = append(records, obs{id: fmt.Sprintf("obs-%s-%s", edge[0], edge[1]), name: "other_tool",
				trace: traceA, span: edge[0], parent: edge[1]}.record())
		}
		rungs = append(rungs, below)
		above = below
	}
	return records, rungs
}

// onLadder maps the observations of the path at spans over a ladder of n
// rungs, against plane a enforcing the path with one proposal at the
// ladder's deepest span.
func onLadder(t *testing.T, n int, spans func(rungs []string) []string) []coverage.JoinCheck {
	t.Helper()
	records, rungs := ladder(n)
	for i, span := range spans(rungs) {
		records = append(records, obs{id: fmt.Sprintf("obs-at-%d", i), trace: traceA, span: span}.record())
	}
	return enforcedChain(t, 3*n, records).Joins
}

// TestADiamondLadderIsCountedOnce: a span reached by two ways is one span.
// Fifteen rungs put 45 spans below the top, which a count per way takes past
// the walk's bound; a ladder of exactly the bound's spans is walked, and one
// rung more is cut.
func TestADiamondLadderIsCountedOnce(t *testing.T) {
	top := func([]string) []string { return []string{spanAt(0)} }
	for _, c := range []struct {
		rungs int
		want  coverage.JoinCheck
	}{
		{15, coverage.JoinCheck{ObservationID: "obs-at-0", Join: coverage.Joined}},
		{(walkBound - 1) / 3, coverage.JoinCheck{ObservationID: "obs-at-0", Join: coverage.Joined}},
		{(walkBound-1)/3 + 1, coverage.JoinCheck{ObservationID: "obs-at-0", Join: coverage.JoinNotChecked, Why: cutWhy, Cut: true}},
	} {
		joins := onLadder(t, c.rungs, top)
		if len(joins) != 1 || joins[0] != c.want {
			t.Errorf("%d rungs: joins %+v, want %+v", c.rungs, joins, c.want)
		}
	}
}

// TestCountingIsBoundedPerSource: an observation at every rung of a long
// ladder makes each count walk the rungs below it again, which grows with
// the square of the rungs. Within the source's bound every one joins; past
// it the counts left are cut, and none is joined or refused for another
// reason.
func TestCountingIsBoundedPerSource(t *testing.T) {
	every := func(rungs []string) []string { return append([]string{spanAt(0)}, rungs...) }
	for _, c := range []struct {
		rungs   int
		wantCut bool
	}{
		{1000, false},
		{2500, true},
	} {
		cut := 0
		for _, j := range onLadder(t, c.rungs, every) {
			switch {
			case j.Join == coverage.Joined:
			case j.Cut && j.Why == stepsWhy:
				cut++
			default:
				t.Fatalf("%d rungs: %+v, want joined or cut by the counting bound", c.rungs, j)
			}
		}
		if (cut > 0) != c.wantCut {
			t.Errorf("%d rungs: %d count(s) cut, want cut %v", c.rungs, cut, c.wantCut)
		}
	}
}
