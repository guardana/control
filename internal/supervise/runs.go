package supervise

import (
	"cmp"
	"slices"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// namesRuns reports whether findings name the run of the plane event they
// rest on, as a 0.2 procedure's do; a 0.1 finding names the supervised run.
func (e *evaluation) namesRuns() bool { return e.p.schema == ProcedureSchema02 }

// named makes d name run and adds run to its anchor, so findings of one
// rule and anchor in two runs of a tree keep two ids.
func (d *draft) named(run string) {
	d.run = run
	d.anchor = append(slices.Clip(d.anchor), run)
}

// byRun splits calls by the run of each one's proposal, in run order, when
// findings name runs; otherwise calls is one group.
func (e *evaluation) byRun(calls []*request) [][]*request {
	if !e.namesRuns() {
		return [][]*request{calls}
	}
	groups := map[string][]*request{}
	for _, rq := range calls {
		run := rq.proposal.GetRunId()
		groups[run] = append(groups[run], rq)
	}
	runs := make([]string, 0, len(groups))
	for run := range groups {
		runs = append(runs, run)
	}
	slices.Sort(runs)
	out := make([][]*request, 0, len(runs))
	for _, run := range runs {
		out = append(out, groups[run])
	}
	return out
}

// eventOrder orders events by time, one with no time after every timed one,
// and events of one time by request and event id, so the order the exports
// were read in decides nothing.
func eventOrder(a, b *controlv1.Event) int {
	at, bt := a.GetOccurredAt(), b.GetOccurredAt()
	switch {
	case at.IsValid() != bt.IsValid() && at.IsValid():
		return -1
	case at.IsValid() != bt.IsValid():
		return 1
	}
	return cmp.Or(at.AsTime().Compare(bt.AsTime()), cmp.Compare(a.GetRequestId(), b.GetRequestId()),
		cmp.Compare(a.GetEventId(), b.GetEventId()))
}

// crossing is the denial that brought the count to n, by the order of the
// blocks: the one a stop must land on. There are at least n denials. It is
// not told when its block has no time, when a block of another run shares
// its time, or when the denials span two runs and one block has no time,
// which could stand anywhere in the order.
func crossing(denials []*request, n uint32) (*request, bool) {
	sorted := slices.SortedFunc(slices.Values(denials), func(a, b *request) int { return eventOrder(a.terminal, b.terminal) })
	c := sorted[n-1]
	if !c.terminal.GetOccurredAt().IsValid() {
		return c, false
	}
	twoRuns, untimed := false, false
	for _, rq := range denials {
		other := rq.terminal.GetRunId() != c.terminal.GetRunId()
		twoRuns = twoRuns || other
		untimed = untimed || !rq.terminal.GetOccurredAt().IsValid()
		if other && tied(rq.terminal, c.terminal) {
			return c, false
		}
	}
	return c, !twoRuns || !untimed
}

// tied reports two events with a time, the same one.
func tied(a, b *controlv1.Event) bool {
	return a.GetOccurredAt().IsValid() && b.GetOccurredAt().IsValid() &&
		a.GetOccurredAt().AsTime().Equal(b.GetOccurredAt().AsTime())
}

// pastDeadline is the earliest event of the run more than limit after its
// first, and whether no event of another run shares its time; the run's
// last event is past the limit, so there is one.
func (e *evaluation) pastDeadline(limit time.Duration) (*controlv1.Event, bool) {
	start := e.first.GetOccurredAt().AsTime()
	var out *controlv1.Event
	for _, ev := range e.rd.events {
		if ev.GetOccurredAt().IsValid() && ev.GetOccurredAt().AsTime().Sub(start) > limit &&
			(out == nil || eventOrder(ev, out) < 0) {
			out = ev
		}
	}
	for _, ev := range e.rd.events {
		if ev.GetRunId() != out.GetRunId() && tied(ev, out) {
			return out, false
		}
	}
	return out, true
}
