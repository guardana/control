package supervise

import (
	"maps"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
)

// ruleState is what became of one rule, and why when it was not checked.
type ruleState struct {
	state findingv1alpha1.RuleState
	why   string
}

var checked = ruleState{state: findingv1alpha1.RuleState_RULE_STATE_CHECKED}

func notChecked(why string) ruleState {
	return ruleState{state: findingv1alpha1.RuleState_RULE_STATE_NOT_CHECKED, why: why}
}

func off(member string) ruleState {
	return ruleState{state: findingv1alpha1.RuleState_RULE_STATE_OFF, why: "the procedure leaves " + member + " out"}
}

// states says which rules apply. With no event of the run none does. A rule
// that rests on something not seen applies only once nothing more can arrive
// and nothing was left out of what did: on a closed run, from exports that
// are all whole. With no event that has a time, no time and no order can be
// told.
func (e *evaluation) states() map[string]ruleState {
	out := make(map[string]ruleState, len(ruleIDs))
	base := checked
	if len(e.rd.events) == 0 {
		base = notChecked("no plane event of the run")
	}
	absence := base
	if absence.state == checked.state {
		absence = e.absenceState()
	}
	for _, id := range ruleIDs {
		out[id] = base
	}
	out[RuleRequiredStepSkipped], out[RuleStepOutOfOrder], out[RuleContinuedAfterFailure] = absence, absence, absence
	if base.state == checked.state && e.first == nil {
		untimed := notChecked("no event of the run has a time")
		out[RuleDeadlineExceeded] = untimed
		for _, id := range []string{RuleStepOutOfOrder, RuleContinuedAfterFailure} {
			if out[id].state == checked.state {
				out[id] = untimed
			}
		}
	}
	if e.p.maxDenials == 0 {
		out[RuleRepeatedDenial] = off("max_denials")
	}
	if e.p.deadlineSeconds == 0 {
		out[RuleDeadlineExceeded] = off("deadline_seconds")
	}
	return out
}

func (e *evaluation) absenceState() ruleState {
	if !e.in.Run.Closed {
		return notChecked("the run is open")
	}
	for _, x := range e.in.Exports {
		if !x.Whole {
			return notChecked("an export is not whole: " + x.NotWhole)
		}
	}
	return checked
}

func (e *evaluation) result(drafts []draft, states map[string]ruleState) *Result {
	s := scope{tenant: e.in.Run.Tenant, project: e.rd.project, run: e.in.Run.ID, proc: e.p}
	res := &Result{PlaneBlocks: map[string]uint64{}, SourcesNotRead: e.in.SourcesNotRead,
		NeverHeard: e.neverHeard, Silent: e.lapsed}
	for _, d := range drafts {
		res.Findings = append(res.Findings, s.record(d))
	}
	res.Report = &findingv1alpha1.SuperviseReport{
		SchemaVersion: RecordSchemaVersion, TenantId: s.tenant, ProjectId: s.project, RunId: s.run,
		Procedure: s.procedureRef(), Read: e.rd.counts, FindingsWritten: uint64(len(res.Findings)),
	}
	for _, id := range ruleIDs {
		res.Report.Rules = append(res.Report.Rules, &findingv1alpha1.RuleResult{
			RuleId: id, RuleVersion: RuleVersion, State: states[id].state, Why: states[id].why})
	}
	for _, rq := range e.reqs {
		if rq.terminal.GetKind() == blocked && !rq.denied() {
			res.PlaneBlocks[rq.blockCode()]++
		}
	}
	res.Report.Read.PlaneBlocks = maps.Clone(res.PlaneBlocks)
	for i, step := range e.p.steps {
		m := StepMatch{StepID: step.ID, ObservationIDs: e.reported[i]}
		for _, in := range e.ofStep[i] {
			m.RequestIDs = append(m.RequestIDs, in.request)
		}
		res.Steps = append(res.Steps, m)
	}
	return res
}
