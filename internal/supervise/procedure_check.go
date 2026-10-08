package supervise

import (
	"errors"
	"slices"
)

// check refuses a procedure whose entries cannot be told apart, whose order
// names an unknown step or cannot be followed, or whose binds or exceptions
// name what the procedure lacks.
func (p *Procedure) check() error {
	if len(p.steps) == 0 {
		return errors.New("no step")
	}
	ids := make(map[string]bool, len(p.steps))
	for _, s := range p.steps {
		if ids[s.ID] {
			return errors.New("a step id is named twice")
		}
		ids[s.ID] = true
	}
	if err := p.checkNames(); err != nil {
		return err
	}
	if err := p.checkOrder(ids); err != nil {
		return err
	}
	if err := p.checkBinds(); err != nil {
		return err
	}
	return p.checkExceptions(ids)
}

// checkNames refuses a tool on an upstream, or a name a source reports, that
// two entries share: a call must map to one entry.
func (p *Procedure) checkNames() error {
	tools := map[[2]string]bool{}
	names := map[string]bool{}
	add := func(tool, upstream string, observedAs []string) error {
		if tools[[2]string{tool, upstream}] {
			return errors.New("two entries name one tool on one upstream")
		}
		tools[[2]string{tool, upstream}] = true
		for _, n := range observedAs {
			if names[n] {
				return errors.New("two entries name one reported name")
			}
			names[n] = true
		}
		return nil
	}
	for _, s := range p.steps {
		if err := add(s.Tool, s.Upstream, s.ObservedAs); err != nil {
			return err
		}
	}
	for _, a := range p.allow {
		if err := add(a.Tool, a.Upstream, a.ObservedAs); err != nil {
			return err
		}
	}
	return nil
}

// checkOrder refuses an order that cannot be followed: it removes, round by
// round, every step whose predecessors are all removed, and a step left over
// is on a cycle, after one, or after a step the steps lack.
func (p *Procedure) checkOrder(ids map[string]bool) error {
	for step := range p.after {
		if !ids[step] {
			return errors.New("the order names a step the steps lack")
		}
	}
	removed := make(map[string]bool, len(ids))
	for progress := true; progress; {
		progress = false
		for _, s := range p.steps {
			if !removed[s.ID] && p.allRemoved(s.ID, removed) {
				removed[s.ID], progress = true, true
			}
		}
	}
	if len(removed) != len(p.steps) {
		return errors.New("the order has a cycle or follows a step the steps lack")
	}
	return nil
}

func (p *Procedure) allRemoved(step string, removed map[string]bool) bool {
	for _, a := range p.after[step] {
		if !removed[a] {
			return false
		}
	}
	return true
}

// checkBinds refuses a binds naming no binding.
func (p *Procedure) checkBinds() error {
	for _, b := range slices.Concat(p.stepBinds, p.allowBinds) {
		if b != "" && !slices.ContainsFunc(p.bindings, func(x Binding) bool { return x.Name == b }) {
			return errors.New("a binds names no binding")
		}
	}
	return nil
}

func (p *Procedure) checkExceptions(steps map[string]bool) error {
	ids := make(map[string]bool, len(p.exceptions))
	for _, e := range p.exceptions {
		if ids[e.ID] {
			return errors.New("an exception id is named twice")
		}
		ids[e.ID] = true
		if err := p.checkException(e, steps); err != nil {
			return err
		}
	}
	return nil
}

// checkException refuses an exception the rule it names cannot take. A rule
// that may stop a run is waived only on an approval, since the agent can
// make a step fail or draw a reason code itself.
func (p *Procedure) checkException(e Exception, steps map[string]bool) error {
	rule, known := ruleOf(e.Waives)
	switch {
	case !known:
		return errors.New("an exception waives no rule")
	case !rule.waivable:
		return errors.New("an exception waives a rule no exception may waive")
	case rule.mayStop && e.When != WhenApprovalGranted:
		return errors.New("an exception on a rule that may stop a run is taken only on an approval")
	case e.When == WhenStepFailed && !steps[e.Subject]:
		return errors.New("an exception's failed step is no step")
	}
	if e.Waives == RuleStepOutsideProcedure {
		if _, held := p.entry(e.Tool, e.Upstream); e.Step != "" || held {
			return errors.New("an exception on " + RuleStepOutsideProcedure + " names a step or a tool the procedure holds")
		}
		return nil
	}
	return checkStepException(e, steps)
}

// checkStepException refuses an exception on a rule about steps that names
// no step, and one on a step skipped taken on an approval, which no call of
// a step skipped can hold.
func checkStepException(e Exception, steps map[string]bool) error {
	switch {
	case !steps[e.Step]:
		return errors.New("an exception on a rule about steps names no step")
	case e.Waives == RuleRequiredStepSkipped && e.When == WhenApprovalGranted:
		return errors.New("an exception on " + RuleRequiredStepSkipped + " is never taken on an approval: a step skipped has no call")
	}
	return nil
}
