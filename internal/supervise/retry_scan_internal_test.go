package supervise

import (
	"slices"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// scanCall is request id's proposal, the place-th event read, by run with
// arguments hash.
func scanCall(id, run, hash string, place int, pos map[*controlv1.Event]int) *request {
	ev := &controlv1.Event{EventId: id + "-e1", RequestId: id, RunId: run, Payload: &controlv1.Event_Proposed{
		Proposed: &controlv1.ActionEnvelope{Arguments: &controlv1.Arguments{CanonicalHash: hash}}}}
	pos[ev] = place
	return &request{id: id, proposal: ev}
}

// TestAScanStepsOverItsDenialsKeyAndCountsTheRest: from start on, a scan
// visits every call of another key than the denial's, or every call but the
// denial when it has none; it takes exactly their number from the budget and
// visits nothing when the budget holds one fewer. mayRetry names the runs
// with such a call anywhere in the group, and no run whose calls all share
// the denial's key.
func TestAScanStepsOverItsDenialsKeyAndCountsTheRest(t *testing.T) {
	pos := map[*controlv1.Event]int{}
	d := scanCall("d", "A", "h1", 0, pos)
	calls := []*request{
		scanCall("x", "B", "h1", 6, pos), d, scanCall("y", "C", "h2", 2, pos), scanCall("z", "D", "", 5, pos),
		scanCall("w", "A", "h1", 3, pos), scanCall("v", "C", "h1", 4, pos), scanCall("u", "E", "h2", 1, pos),
	}
	g := newRetryGroup(calls, hashOf, pos)
	for _, c := range []struct {
		key     string
		start   int
		visited string
		runs    []string
	}{
		{"h1", 0, "u y z", []string{"C", "D", "E"}},
		{"h1", 2, "y z", []string{"C", "D", "E"}},
		{"", 0, "u y w v z x", []string{"A", "B", "C", "D", "E"}},
		{"", 3, "w v z x", []string{"A", "B", "C", "D", "E"}},
	} {
		want := uint64(len(strings.Fields(c.visited)))
		short := want - 1
		if g.scan(d, c.key, c.start, &short, func(*request) { t.Fatal("a scan past its budget visited a call") }) {
			t.Errorf("key %q from %d: a budget of %d took %d calls", c.key, c.start, want-1, want)
		}
		budget := want + 5
		var got []string
		if !g.scan(d, c.key, c.start, &budget, func(r *request) { got = append(got, r.id) }) || budget != 5 {
			t.Errorf("key %q from %d: not scanned, or %d of the budget left", c.key, c.start, budget)
		}
		if !slices.Equal(got, strings.Fields(c.visited)) {
			t.Errorf("key %q from %d visited %q, want %q", c.key, c.start, got, c.visited)
		}
		if runs := g.mayRetry(d, c.key); !slices.Equal(runs, c.runs) {
			t.Errorf("key %q: may retry %q, want %q", c.key, runs, c.runs)
		}
	}
}
