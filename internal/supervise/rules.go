package supervise

import (
	"cmp"
	"slices"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// byTool groups requests that proposed a tool by that tool and upstream, the
// groups in tool order.
func byTool(reqs []*request, keep func(*request) bool) ([][2]string, map[[2]string][]*request) {
	groups := map[[2]string][]*request{}
	var keys [][2]string
	for _, rq := range reqs {
		tool, ok := rq.tool()
		if !ok || !keep(rq) {
			continue
		}
		if _, seen := groups[tool]; !seen {
			keys = append(keys, tool)
		}
		groups[tool] = append(groups[tool], rq)
	}
	slices.SortFunc(keys, func(a, b [2]string) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })
	return keys, groups
}

// repeatedDenial fires for a tool on an upstream whose calls the policy
// denied max_denials times or more.
func (e *evaluation) repeatedDenial() []draft {
	keys, groups := byTool(e.reqs, (*request).denied)
	var out []draft
	for _, tool := range keys {
		denials := groups[tool]
		if uint64(len(denials)) < uint64(e.p.maxDenials) {
			continue
		}
		d := draft{rule: RuleRepeatedDenial, anchor: tool[:], cap: confirmed}
		for _, rq := range denials {
			d.refs = append(d.refs, rq.ref(rq.terminal))
		}
		out = append(out, d)
	}
	return out
}

// outsideProcedure fires for each tool the plane saw called that is neither a
// step nor allowed, then for each name a source reported of a call no plane
// call joins and no entry lists.
func (e *evaluation) outsideProcedure() []draft {
	keys, groups := byTool(e.reqs, func(rq *request) bool {
		tool, _ := rq.tool()
		_, known := e.ix.byTool[tool]
		return !known
	})
	var out []draft
	for _, tool := range keys {
		d := draft{rule: RuleStepOutsideProcedure, anchor: tool[:], cap: confirmed}
		for _, rq := range groups[tool] {
			d.refs = append(d.refs, rq.ref(rq.proposal))
		}
		out = append(out, d)
	}
	named := map[string]*draft{}
	var names []string
	for _, o := range e.unjoined {
		name := o.GetSubject().GetName()
		if _, known := e.ix.byName[name]; known {
			continue
		}
		if named[name] == nil {
			named[name] = &draft{rule: RuleStepOutsideProcedure, anchor: []string{name}, cap: confirmed}
			names = append(names, name)
		}
		named[name].refs = append(named[name].refs, e.obsRef(o))
	}
	slices.Sort(names)
	for _, n := range names {
		out = append(out, *named[n])
	}
	return out
}

// deadline fires when the run's plane events span more than
// deadline_seconds. Events from more than one export may come from planes
// whose clocks differ, so then it only suggests.
func (e *evaluation) deadline() []draft {
	if e.first == nil || e.last.GetOccurredAt().AsTime().Sub(e.first.GetOccurredAt().AsTime()) <=
		time.Duration(e.p.deadlineSeconds)*time.Second {
		return nil
	}
	d := draft{rule: RuleDeadlineExceeded, cap: confirmed}
	if len(e.in.Exports) != 1 {
		d.cap = suspected
	}
	for _, ev := range []*controlv1.Event{e.first, e.last} {
		rq := e.byRequest[ev.GetRequestId()]
		d.refs = append(d.refs, rq.ref(ev))
	}
	return []draft{d}
}

// absenceCap is the most a finding that rests on something not seen can
// say: it suggests, and while a source is silent past its heartbeat or was
// named and not read it cannot say even that.
func (e *evaluation) absenceCap() controlv1.FindingVerdict {
	if e.anySilent {
		return indeterminate
	}
	return suspected
}

// skipped fires for each required step with no instance.
func (e *evaluation) skipped() []draft {
	var out []draft
	for i, s := range e.p.steps {
		if s.Required && len(e.ofStep[i]) == 0 {
			out = append(out, draft{rule: RuleRequiredStepSkipped, anchor: []string{s.ID}, cap: e.absenceCap()})
		}
	}
	return out
}

// outOfOrder fires for a step that ran before a step it must follow: its
// first instance comes before the first of that step, or that step has no
// instance at all. An order that cannot be told, because an instance of
// either side has no time, is no pass: the finding is then indeterminate. It
// returns the steps found out of order.
func (e *evaluation) outOfOrder() ([]draft, map[int]bool) {
	var out []draft
	fired := map[int]bool{}
	for i, s := range e.p.steps {
		if len(e.ofStep[i]) == 0 {
			continue
		}
		d, broken := e.order(i, s.ID)
		if d != nil {
			out = append(out, *d)
		}
		fired[i] = broken
	}
	return out, fired
}

// order judges step i, named id, against the steps it must follow, and
// reports whether it is out of order.
func (e *evaluation) order(i int, id string) (*draft, bool) {
	first, told := e.firstOf(i)
	d := draft{rule: RuleStepOutOfOrder, anchor: []string{id}, cap: e.absenceCap(), refs: []ref{first.start}}
	broken := false
	var untold []ref
	for _, a := range e.p.after[id] {
		j := e.stepIndex[a]
		if len(e.ofStep[j]) == 0 {
			broken = true
			continue
		}
		before, beforeTold := e.firstOf(j)
		switch {
		case !told || !beforeTold:
			untold = append(untold, before.start)
		case !before.before(first):
			broken = true
			d.refs = append(d.refs, before.start)
		}
	}
	switch {
	case broken:
		return &d, true
	case len(untold) > 0:
		d.cap, d.refs = indeterminate, append(d.refs, untold...)
		return &d, false
	}
	return nil, false
}

// continued fires for a failed instance that a different step's instance
// follows: the first proposed after the failed one's terminal event. A retry
// of the same step is no finding: one proposed after the failure ends the
// search, since what follows it follows the retry, and one that may have come
// before the failure is passed over, as is the first of a step reported out
// of order, which is not also a continuation. When the line of timed
// proposals names no such instance, one of another step whose place against
// the failure is unknown makes the pair indeterminate.
func (e *evaluation) continued(outOfOrder map[int]bool) []draft {
	var line, unplaced []*instance
	for _, in := range e.inst {
		if in.timed {
			line = append(line, in)
		} else {
			unplaced = append(unplaced, in)
		}
	}
	slices.SortStableFunc(line, func(a, b *instance) int { return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.seq, b.seq)) })
	excused := func(in *instance) bool {
		first, _ := e.firstOf(in.step)
		return outOfOrder[in.step] && first == in
	}
	var out []draft
	for _, failed := range slices.Concat(line, unplaced) {
		if !failed.failed || excused(failed) {
			continue
		}
		other := func(in *instance) bool { return in.step != failed.step && !excused(in) }
		next, verdict := e.afterFailure(line, failed, other)
		if next == nil {
			next, verdict = mayFollow(line, unplaced, failed, other), indeterminate
		}
		if next != nil {
			out = append(out, draft{rule: RuleContinuedAfterFailure, anchor: []string{failed.request},
				cap: verdict, refs: []ref{failed.end, next.start}})
		}
	}
	return out
}

// afterFailure is the first instance of line, ordered by proposal, that
// follows failed's failure and is other, before any retry proposed after the
// failure, and the verdict the pair allows. One
// proposed at the failure's own time, or the next proposed when the failure
// has no time, may have come before it, so the pair is then indeterminate. A
// failure with no time whose proposal has none either has no place in line.
func (e *evaluation) afterFailure(line []*instance, failed *instance, other func(*instance) bool) (*instance, controlv1.FindingVerdict) {
	var i int
	switch {
	case failed.endTimed:
		i, _ = slices.BinarySearchFunc(line, failed.endAt, func(in *instance, t time.Time) int { return in.at.Compare(t) })
	case failed.timed:
		i = slices.Index(line, failed) + 1
	default:
		return nil, indeterminate
	}
	for ; i < len(line) && !other(line[i]); i++ {
		if retried(failed, line[i]) {
			return nil, indeterminate
		}
	}
	switch {
	case i >= len(line):
		return nil, indeterminate
	case !failed.endTimed || line[i].at.Equal(failed.endAt):
		return line[i], indeterminate
	}
	return line[i], e.absenceCap()
}

// retried reports in a retry of failed's step proposed after its failure.
func retried(failed, in *instance) bool {
	return in != failed && in.step == failed.step && failed.endTimed && in.at.After(failed.endAt)
}

// mayFollow is the first instance that is other and may have been proposed
// after failed's failure without line saying so: one whose proposal has no
// time, or, when the failure has no time, any proposed after failed.
func mayFollow(line, unplaced []*instance, failed *instance, other func(*instance) bool) *instance {
	pool := unplaced
	if !failed.endTimed {
		pool = slices.Concat(line[slices.Index(line, failed)+1:], unplaced)
	}
	for _, in := range pool {
		if other(in) {
			return in
		}
	}
	return nil
}
