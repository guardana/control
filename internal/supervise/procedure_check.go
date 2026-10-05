package supervise

import "errors"

// check refuses a procedure whose entries cannot be told apart, or whose
// order names an unknown step or cannot be followed.
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
	return p.checkOrder(ids)
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
