package supervise

import (
	"cmp"
	"slices"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// holding is what an exception's condition says of one instance of the rule
// it waives.
type holding int

const (
	notMet holding = iota
	met
	// unknown is a condition the evidence read does not settle: it takes no
	// waiver, and the finding it leaves is indeterminate at most.
	unknown
)

// waiverKey is one exception, by its place in the procedure, and the run
// the findings it waived name, "" for the supervised run.
type waiverKey struct {
	exception int
	run       string
}

// waiver is what one exception took away for one run: the weakest verdict
// of the findings it waived, what they rested on and what met the
// condition, and the requests of the calls it waived.
type waiver struct {
	cap   controlv1.FindingVerdict
	refs  []ref
	seen  map[string]bool
	calls []string
}

func (w *waiver) add(r ref) {
	key := r.r.GetEvent().GetEventId() + "\x00" + r.r.GetObservation().GetObservationId()
	if !w.seen[key] {
		w.seen[key] = true
		w.refs = append(w.refs, r)
	}
}

// except takes from the drafts of rule what an exception of it waives. A
// rule no exception names keeps its drafts.
func (e *evaluation) except(rule string, drafts []draft) []draft {
	if !slices.ContainsFunc(e.p.exceptions, func(x Exception) bool { return x.Waives == rule }) {
		return drafts
	}
	out := make([]draft, 0, len(drafts))
	for _, d := range drafts {
		var kept bool
		if rule == RuleStepOutsideProcedure {
			d, kept = e.exceptCalls(d)
		} else {
			d, kept = e.exceptStep(rule, d)
		}
		if kept {
			out = append(out, d)
		}
	}
	return out
}

// exceptCalls waives, call by call, the calls of a tool outside the
// procedure that an exception names. What is left stands, indeterminate when
// every call left is one whose condition is unknown; a finding a source
// reported names no upstream and is never waived.
func (e *evaluation) exceptCalls(d draft) (draft, bool) {
	if len(d.refs) == 0 || d.refs[0].r.GetEvent() == nil {
		return d, true
	}
	tool, upstream := d.anchor[0], d.anchor[1]
	xs := e.exceptionsOf(RuleStepOutsideProcedure, func(x Exception) bool { return x.Tool == tool && x.Upstream == upstream })
	if len(xs) == 0 {
		return d, true
	}
	var kept []ref
	certain := false
	for _, r := range d.refs {
		x, h, evidence := e.firstMet(xs, e.byRequest[r.r.GetEvent().GetRequestId()])
		switch h {
		case met:
			e.waive(x, d.run, d.cap, []ref{r}, evidence, r.r.GetEvent().GetRequestId())
			continue
		case notMet:
			certain = true
		}
		kept = append(kept, r)
	}
	if len(kept) == 0 {
		return d, false
	}
	d.refs = kept
	if !certain {
		d.cap = weaker(d.cap, indeterminate)
	}
	return d, true
}

// exceptStep waives a finding about one step: a required step skipped, a
// step's first instance out of order, or a failed instance continued from.
func (e *evaluation) exceptStep(rule string, d draft) (draft, bool) {
	step, call := e.subjectOf(rule, d)
	xs := e.exceptionsOf(rule, func(x Exception) bool { return x.Step == step })
	if len(xs) == 0 {
		return d, true
	}
	x, h, evidence := e.firstMet(xs, call)
	switch h {
	case met:
		var waived []string
		if call != nil {
			waived = append(waived, call.id)
		}
		e.waive(x, d.run, d.cap, d.refs, evidence, waived...)
		return d, false
	case unknown:
		d.cap = weaker(d.cap, indeterminate)
	}
	return d, true
}

// subjectOf is the step a finding of rule is about and the call that is its
// instance, nil for a step skipped.
func (e *evaluation) subjectOf(rule string, d draft) (string, *request) {
	switch rule {
	case RuleStepOutOfOrder:
		first, _ := e.firstOf(e.stepIndex[d.anchor[0]])
		return d.anchor[0], e.byRequest[first.request]
	case RuleContinuedAfterFailure:
		rq := e.byRequest[d.anchor[0]]
		tool, _ := rq.tool()
		return e.p.steps[e.ix.byTool[tool].step].ID, rq
	}
	return d.anchor[0], nil
}

// exceptionsOf is the places of the exceptions of rule that cover.
func (e *evaluation) exceptionsOf(rule string, covers func(Exception) bool) []int {
	var out []int
	for i, x := range e.p.exceptions {
		if x.Waives == rule && covers(x) {
			out = append(out, i)
		}
	}
	return out
}

// firstMet is the first of xs whose condition call meets, with the record
// that meets it; otherwise unknown when any condition is, else not met.
func (e *evaluation) firstMet(xs []int, call *request) (int, holding, *ref) {
	out := notMet
	for _, x := range xs {
		h, evidence := e.holds(e.p.exceptions[x], call)
		switch h {
		case met:
			return x, met, evidence
		case unknown:
			out = unknown
		}
	}
	return -1, out, nil
}

// holds is what x's condition says of call, the instance of the rule it
// waives, nil for a step skipped; and the record that meets it.
func (e *evaluation) holds(x Exception, call *request) (holding, *ref) {
	switch x.When {
	case WhenApprovalGranted:
		return approvalHolds(call)
	case WhenReason:
		return reasonHolds(call, x.Subject)
	case WhenStepFailed:
		return e.stepFailed(x.Subject)
	}
	return unknown, nil
}

// approvalHolds is met by an approval the approvals store granted in the
// call's own coherent trail. One still held, or a call not yet ended, may
// still be granted one.
func approvalHolds(rq *request) (holding, *ref) {
	switch {
	case rq == nil:
		return notMet, nil
	case rq.doubt:
		return unknown, nil
	case rq.approval == controlv1.ApprovalState_APPROVAL_STATE_APPROVED:
		for _, ev := range rq.events {
			if ev.GetKind() == controlv1.EventKind_EVENT_KIND_APPROVAL_DECIDED {
				r := rq.ref(ev)
				return met, &r
			}
		}
	case rq.waiting || rq.terminal == nil:
		return unknown, nil
	}
	return notMet, nil
}

// reasonHolds is met by code among the reason codes of the call's own
// policy decision, never a code the plane's block carries. A trail with no
// decision read and no end may still hold one.
func reasonHolds(rq *request, code string) (holding, *ref) {
	switch {
	case rq == nil:
		return notMet, nil
	case rq.doubt, rq.kernel == nil && rq.terminal == nil:
		return unknown, nil
	case slices.Contains(rq.kernel.GetReasonCodes(), code):
		r := rq.ref(rq.decided)
		return met, &r
	}
	return notMet, nil
}

// stepFailed is met by an ACTION_FAILED of the step in a coherent trail,
// the least request id cited; an instance in doubt or not yet ended may
// still be one.
func (e *evaluation) stepFailed(step string) (holding, *ref) {
	out := notMet
	var failed *request
	for _, in := range e.ofStep[e.stepIndex[step]] {
		rq := e.byRequest[in.request]
		switch {
		case rq.doubt || rq.terminal == nil:
			out = unknown
		case rq.terminal.GetKind() == controlv1.EventKind_EVENT_KIND_ACTION_FAILED && (failed == nil || rq.id < failed.id):
			failed = rq
		}
	}
	if failed != nil {
		r := failed.ref(failed.terminal)
		return met, &r
	}
	return out, nil
}

// waive records that exception x took away a finding naming run that could
// say verdict at most and rested on refs; evidence met the condition, and
// calls are the requests the finding was about.
func (e *evaluation) waive(x int, run string, verdict controlv1.FindingVerdict, refs []ref, evidence *ref, calls ...string) {
	key := waiverKey{exception: x, run: run}
	w := e.waived[key]
	if w == nil {
		w = &waiver{cap: confirmed, seen: map[string]bool{}}
		e.waived[key] = w
	}
	w.cap = weaker(w.cap, verdict)
	for _, r := range refs {
		w.add(r)
	}
	if evidence != nil {
		w.add(*evidence)
	}
	w.calls = append(w.calls, calls...)
}

// exceptionsTaken is one EXCEPTION_TAKEN per exception and run whose
// findings it waived, anchored on the exception's id, in the procedure's
// order of exceptions and then of runs.
func (e *evaluation) exceptionsTaken() []draft {
	keys := make([]waiverKey, 0, len(e.waived))
	for k := range e.waived {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b waiverKey) int {
		return cmp.Or(cmp.Compare(a.exception, b.exception), cmp.Compare(a.run, b.run))
	})
	out := make([]draft, 0, len(keys))
	for _, k := range keys {
		w := e.waived[k]
		d := draft{rule: RuleExceptionTaken, anchor: []string{e.p.exceptions[k.exception].ID}, cap: w.cap, refs: w.refs}
		if k.run != "" {
			d.named(k.run)
		}
		out = append(out, d)
	}
	return out
}

// takenState is EXCEPTION_TAKEN's state: checked only when every rule an
// exception waives is, since an exception on a rule not checked was not
// judged. The waivers of the rules that were are written either way.
func (e *evaluation) takenState(states map[string]ruleState, base ruleState) ruleState {
	if len(e.p.exceptions) == 0 {
		return ruleState{state: findingv1alpha1.RuleState_RULE_STATE_OFF, why: "the procedure states no exception"}
	}
	if base.state != checked.state {
		return base
	}
	for _, x := range e.p.exceptions {
		if s := states[x.Waives]; s.state != checked.state {
			return notChecked("exceptions on " + x.Waives + " are not judged: " + s.why)
		}
	}
	return checked
}
