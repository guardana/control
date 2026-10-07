package supervise

import (
	"cmp"
	"slices"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

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
// Proposals of one time in two exports are untold against each other: when a
// retry may have come first the pair is indeterminate, and of several next
// instances the one of the least request id is cited.
func (e *evaluation) afterFailure(line []*instance, failed *instance, other func(*instance) bool) (*instance, controlv1.FindingVerdict) {
	from := failed.endAt
	switch {
	case failed.endTimed:
	case failed.timed:
		from = failed.at
	default:
		return nil, indeterminate
	}
	// With no failure time, only what may follow the proposal counts: not the
	// proposal itself, nor one its own export appended before it at that instant.
	follows := func(in *instance) bool {
		return failed.endTimed || in != failed && (in.export != failed.export || !in.at.Equal(failed.at) || in.seq > failed.seq)
	}
	i, _ := slices.BinarySearchFunc(line, from, func(in *instance, t time.Time) int { return in.at.Compare(t) })
	ends, nexts := firstAfter(line[i:], failed, follows, other)
	if len(nexts) == 0 {
		return nil, indeterminate
	}
	next := leastRequest(nexts)
	if len(ends) > 0 || !failed.endTimed || next.at.Equal(failed.endAt) {
		return next, indeterminate
	}
	return next, e.absenceCap()
}

// firstAfter takes the earliest instant of line at which an instance that
// follows failed is a retry of it or other, and returns, by export, the first
// such instance of that instant in each: a retry in ends, else in nexts.
func firstAfter(line []*instance, failed *instance, follows, other func(*instance) bool) (ends, nexts map[int]*instance) {
	ends, nexts = map[int]*instance{}, map[int]*instance{}
	var at time.Time
	for _, in := range line {
		retry := retried(failed, in)
		if !follows(in) || !retry && !other(in) {
			continue
		}
		if len(ends)+len(nexts) > 0 && !in.at.Equal(at) {
			break
		}
		at = in.at
		switch {
		case ends[in.export] != nil || nexts[in.export] != nil:
		case retry:
			ends[in.export] = in
		default:
			nexts[in.export] = in
		}
	}
	return ends, nexts
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
