package supervise

import (
	"fmt"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
)

// rules02 are the rules a 0.2 procedure adds that this build applies, in
// the order a report lists them.
var rules02 = []string{RuleResourceOutsideRun, RuleDeniedActionRetriedArguments,
	RuleDeniedActionRetriedResource, RuleDeniedActionRetriedAround}

// states02 says which of rules02 apply, from base, the state of a rule that
// rests on what was seen: an open run or an export not whole leaves each as
// base says.
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
}

// uncheckable02 marks those of rules02 that have nothing to compare, or more
// than they may.
func (e *evaluation) uncheckable02(out map[string]ruleState) {
	if !e.anyHeld() {
		out[RuleResourceOutsideRun] = notChecked("no call of a binding carries a resource id of its type")
	}
	for _, id := range rules02[1:] {
		if n := e.retryIx().pairs(id); n > MaxRetryPairs {
			out[id] = notChecked(fmt.Sprintf("%d pairs of a denial and a call to compare, bound %d", n, MaxRetryPairs))
		}
	}
	if len(e.in.Sources) == 0 {
		out[RuleDeniedActionRetriedAround] = notChecked("no observation source was read")
	}
}

// apply02 applies those of rules02 that are checked; evaluate applies the
// rules of 0.1.
func (e *evaluation) apply02(states map[string]ruleState) []draft {
	var out []draft
	for _, id := range rules02 {
		if s, ok := states[id]; ok && s.state == checked.state {
			out = append(out, e.apply(id)...)
		}
	}
	return out
}
