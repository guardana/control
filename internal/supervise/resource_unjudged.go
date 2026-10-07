package supervise

import (
	"maps"
	"slices"
)

// unjudged is, for each binding none of whose calls carries a resource of its
// type, one indeterminate finding per run that called it. The rule applies
// once another binding's call carries one, and the agent chooses whether a
// call carries an id, so a binding it called with none is said to be in
// doubt: leaving an id out never passes for that binding, nor turns the rule
// off for one that fires.
func (e *evaluation) unjudged() []draft {
	var out []draft
	for _, b := range e.p.bindings {
		calls := e.boundCalls()[b.Name]
		if slices.ContainsFunc(calls, func(rq *request) bool { _, ok := held(rq, b); return ok }) {
			continue
		}
		byRun := map[string]*refSet{}
		for _, rq := range calls {
			run := rq.proposal.GetRunId()
			if byRun[run] == nil {
				byRun[run] = &refSet{}
			}
			byRun[run].event(rq, rq.proposal)
		}
		for _, run := range slices.Sorted(maps.Keys(byRun)) {
			d := draft{rule: RuleResourceOutsideRun, anchor: []string{b.Name}, cap: indeterminate}
			byRun[run].into(&d)
			d.named(run)
			out = append(out, d)
		}
	}
	return out
}
