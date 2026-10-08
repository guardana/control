package supervise

import findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"

// rules02 are the rules a 0.2 procedure adds that rest on the calls seen, in
// the order a report lists them; EXCEPTION_TAKEN follows them and rests on
// what the rules applied before it waived.
var rules02 = []string{RuleResourceOutsideRun, RuleDeniedActionRetriedArguments,
	RuleDeniedActionRetriedResource, RuleDeniedActionRetriedAround}

// states02 says which of rules02 apply, from base, the state of a rule that
// rests on what was seen: an open run or an export not whole leaves each as
// base says. EXCEPTION_TAKEN follows the rules its exceptions waive.
func (e *evaluation) states02(out map[string]ruleState, base ruleState) {
	if e.p.schema != ProcedureSchema02 {
		return
	}
	for _, id := range rules02 {
		out[id] = base
	}
	if base.state == checked.state {
		e.uncheckable02(out)
	}
	if !e.p.binds() {
		out[RuleResourceOutsideRun] = ruleState{state: findingv1alpha1.RuleState_RULE_STATE_OFF,
			why: "no step or allowed tool binds a resource"}
	}
	out[RuleExceptionTaken] = e.takenState(out, base)
}

// uncheckable02 marks those of rules02 that have nothing to compare.
func (e *evaluation) uncheckable02(out map[string]ruleState) {
	if !e.anyHeld() {
		out[RuleResourceOutsideRun] = notChecked("no call of a binding carries a resource id of its type")
	}
	if len(e.in.Sources) == 0 {
		out[RuleDeniedActionRetriedAround] = notChecked("no observation source was read")
	}
}

// apply02 applies those of rules02 that are checked, then reports the
// exceptions taken by every rule applied before; evaluate applies the rules
// of 0.1.
func (e *evaluation) apply02(states map[string]ruleState) []draft {
	if e.p.schema != ProcedureSchema02 {
		return nil
	}
	var out []draft
	for _, id := range rules02 {
		if states[id].state == checked.state {
			out = append(out, e.apply(id)...)
		}
	}
	return append(out, e.apply(RuleExceptionTaken)...)
}
