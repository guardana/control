package supervise

import (
	"maps"
	"slices"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// boundCalls is the run's tool calls of each binding, whatever their
// outcome, in reading order.
func (e *evaluation) boundCalls() map[string][]*request {
	if e.bound != nil {
		return e.bound
	}
	binds := map[[2]string]string{}
	for i, s := range e.p.steps {
		binds[[2]string{s.Tool, s.Upstream}] = bindingAt(e.p.stepBinds, i)
	}
	for i, a := range e.p.allow {
		binds[[2]string{a.Tool, a.Upstream}] = bindingAt(e.p.allowBinds, i)
	}
	e.bound = map[string][]*request{}
	for _, rq := range e.reqs {
		if tool, ok := rq.tool(); ok && binds[tool] != "" {
			e.bound[binds[tool]] = append(e.bound[binds[tool]], rq)
		}
	}
	return e.bound
}

// binds reports whether a step or an allowed tool binds a resource.
func (p *Procedure) binds() bool {
	return slices.ContainsFunc(slices.Concat(p.stepBinds, p.allowBinds), func(b string) bool { return b != "" })
}

// held is the resource rq carries for binding b, and false when it carries
// no id or an id of another type: a call that says nothing of which resource
// of b it reached.
func held(rq *request, b Binding) (resourceKey, bool) {
	k := resourceOf(rq)
	return k, k.id != "" && k.typ == b.ResourceType
}

// anyHeld reports whether a call of a binding carries a resource of its
// type, without which the rule has nothing to compare.
func (e *evaluation) anyHeld() bool {
	for _, b := range e.p.bindings {
		for _, rq := range e.boundCalls()[b.Name] {
			if _, ok := held(rq, b); ok {
				return true
			}
		}
	}
	return false
}

// resourceOutside fires, per binding and per run that called it, when the
// binding's calls across the run or its tree carry more than one resource.
func (e *evaluation) resourceOutside() []draft {
	var out []draft
	for _, b := range e.p.bindings {
		out = append(out, e.outsideBinding(b, e.boundCalls()[b.Name])...)
	}
	return out
}

// outsideBinding judges the calls of b, one finding per run that made one.
// It cites the run's own calls first, then the calls that show the
// resources.
func (e *evaluation) outsideBinding(b Binding, calls []*request) []draft {
	most, fires := bindingVerdict(b, calls)
	if !fires {
		return nil
	}
	proof := shownBy(calls, b, most == confirmed)
	byRun := map[string][]*request{}
	for _, rq := range calls {
		byRun[rq.proposal.GetRunId()] = append(byRun[rq.proposal.GetRunId()], rq)
	}
	inProof := map[string]uint64{}
	for _, rq := range proof {
		inProof[rq.proposal.GetRunId()]++
	}
	out := make([]draft, 0, len(byRun))
	for _, run := range slices.Sorted(maps.Keys(byRun)) {
		d := draft{rule: RuleResourceOutsideRun, anchor: []string{b.Name}, cap: runVerdict(most, byRun[run])}
		s := &refSet{}
		for _, rq := range byRun[run] {
			if rank(rq.verdict()) >= rank(d.cap) {
				s.event(rq, rq.proposal)
			}
		}
		var offered uint64
		for _, rq := range proof {
			if offered == MaxFindingRefs {
				break
			}
			if rq.proposal.GetRunId() != run {
				s.event(rq, rq.proposal)
				offered++
			}
		}
		s.leftOut += uint64(len(proof)) - inProof[run] - offered
		s.into(&d)
		d.named(run)
		out = append(out, d)
	}
	return out
}

// bindingVerdict is the most the calls of b say, and whether they fire. Two
// resources from trails not in doubt confirm whatever else the calls carry;
// two only a trail in doubt tells apart, or one beside a call that names
// none of b's type, are indeterminate.
func bindingVerdict(b Binding, calls []*request) (controlv1.FindingVerdict, bool) {
	clean, all := map[resourceKey]bool{}, map[resourceKey]bool{}
	unknown := false
	for _, rq := range calls {
		k, ok := held(rq, b)
		switch {
		case !ok:
			unknown = true
		case rq.doubt:
			all[k] = true
		default:
			all[k], clean[k] = true, true
		}
	}
	switch {
	case len(clean) >= 2:
		return confirmed, true
	case len(all) >= 2 || len(all) == 1 && unknown:
		return indeterminate, true
	}
	return indeterminate, false
}

// runVerdict is what a finding naming a run can say: a run none of whose
// calls of the binding is in a coherent trail is not confirmed to have made
// one.
func runVerdict(most controlv1.FindingVerdict, calls []*request) controlv1.FindingVerdict {
	if slices.ContainsFunc(calls, func(rq *request) bool { return !rq.doubt }) {
		return most
	}
	return weaker(most, indeterminate)
}

// shownBy is the calls that show b's resources: the first call of each, from
// a coherent trail when coherent alone confirms, and otherwise every call
// that names none of b's type besides.
func shownBy(calls []*request, b Binding, coherent bool) []*request {
	seen := map[resourceKey]bool{}
	var out []*request
	for _, rq := range calls {
		k, ok := held(rq, b)
		switch {
		case coherent && (!ok || rq.doubt):
		case !ok:
			out = append(out, rq)
		case !seen[k]:
			seen[k] = true
			out = append(out, rq)
		}
	}
	return out
}
