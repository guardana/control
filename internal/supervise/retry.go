package supervise

import (
	"cmp"
	"maps"
	"slices"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// resourceKey is a call's resource as its bytes say. No id is normalised:
// "042" and "42" are two resources.
type resourceKey struct{ typ, id, tenant, env string }

func resourceOf(rq *request) resourceKey {
	r := rq.proposal.GetProposed().GetResource()
	return resourceKey{typ: r.GetType(), id: r.GetId(), tenant: r.GetTenantId(), env: r.GetEnvironment()}
}

// retryIndex is what the retry rules compare: the policy's denials of a tool
// call, by request id, the run's tool calls by tool and by resource, its
// reports no plane call joins by name, and where each event was read.
type retryIndex struct {
	denials    []*request
	byTool     map[[2]string]*retryGroup
	byResource map[resourceKey]*retryGroup
	reported   map[string][]*observev1.Observation
	// names are the names a source reports each entry's tool under.
	names map[[2]string][]string
	pos   map[*controlv1.Event]int
}

func (e *evaluation) retryIx() *retryIndex {
	if e.retry != nil {
		return e.retry
	}
	x := &retryIndex{byTool: map[[2]string]*retryGroup{}, byResource: map[resourceKey]*retryGroup{},
		reported: map[string][]*observev1.Observation{}, names: map[[2]string][]string{}, pos: map[*controlv1.Event]int{}}
	for i, ev := range e.rd.events {
		x.pos[ev] = i
	}
	tools, resources := map[[2]string][]*request{}, map[resourceKey][]*request{}
	for _, rq := range e.reqs {
		tool, ok := rq.tool()
		if !ok {
			continue
		}
		tools[tool] = append(tools[tool], rq)
		if k := resourceOf(rq); k.id != "" {
			resources[k] = append(resources[k], rq)
		}
		if rq.denied() {
			x.denials = append(x.denials, rq)
		}
	}
	for tool, calls := range tools {
		x.byTool[tool] = newRetryGroup(calls, hashOf, x.pos)
	}
	for k, calls := range resources {
		x.byResource[k] = newRetryGroup(calls, toolKey, x.pos)
	}
	slices.SortStableFunc(x.denials, func(a, b *request) int { return cmp.Compare(a.id, b.id) })
	for _, o := range e.unjoined {
		x.reported[o.GetSubject().GetName()] = append(x.reported[o.GetSubject().GetName()], o)
	}
	for _, name := range slices.Sorted(maps.Keys(e.ix.byName)) {
		tool := e.ix.byName[name].tool
		x.names[tool] = append(x.names[tool], name)
	}
	e.retry = x
	return x
}

func toolOf(rq *request) [2]string {
	tool, _ := rq.tool()
	return tool
}

// hashOf keys a call by its arguments hash, "" when it carries none.
func hashOf(rq *request) string {
	return rq.proposal.GetProposed().GetArguments().GetCanonicalHash()
}

// toolKey keys a call by its tool and upstream; it is never "".
func toolKey(rq *request) string {
	t := toolOf(rq)
	return t[0] + "\x00" + t[1]
}

// retryFinding gathers the retries of one denial from one run by the verdict
// each allows. The finding is the strongest of them and cites those that
// carry it, so a retry in doubt beside a told one cannot weaken it.
type retryFinding struct{ by [3]refSet }

func (f *retryFinding) empty() bool { return f.by[0].empty() && f.by[1].empty() && f.by[2].empty() }

// strongest is the verdict of the strongest retry and the refs that carry
// it; there is one.
func (f *retryFinding) strongest() (controlv1.FindingVerdict, *refSet) {
	for _, v := range []controlv1.FindingVerdict{confirmed, suspected} {
		if !f.by[rank(v)].empty() {
			return v, &f.by[rank(v)]
		}
	}
	return indeterminate, &f.by[rank(indeterminate)]
}

// retryDrafts is one finding of rule per run in byRun, anchored on the
// denied request and that run.
func retryDrafts(rule string, d *request, byRun map[string]*retryFinding) []draft {
	out := make([]draft, 0, len(byRun))
	for _, run := range slices.Sorted(maps.Keys(byRun)) {
		v, s := byRun[run].strongest()
		dr := draft{rule: rule, anchor: []string{d.id}, cap: v, refs: []ref{d.ref(d.decided)}}
		s.into(&dr)
		dr.named(run)
		out = append(out, dr)
	}
	return out
}

// retried fires for each denial with a call of its group that form names a
// retry of it, proposed after the denial was decided, per run that made one.
// A run that is neither the denied run nor its descendant suggests at most.
func (e *evaluation) retried(rule string, group func(*request) *retryGroup, keyOf func(*request) string,
	form func(d, r *request) (controlv1.FindingVerdict, bool)) []draft {
	var out []draft
	budget := uint64(MaxRetryPairs)
	for _, d := range e.retryIx().denials {
		g := group(d)
		if g == nil {
			continue
		}
		byRun := map[string]*retryFinding{}
		judged := g.scan(d, keyOf(d), e.startOf(g, d), &budget, func(r *request) {
			v, ok := form(d, r)
			if !ok {
				return
			}
			w, ok := r.unapproved()
			if !ok {
				return
			}
			o, ok := e.after(d.decided, r.proposal)
			if !ok {
				return
			}
			v = weaker(weaker(v, w), weaker(o, r.verdict()))
			run := r.proposal.GetRunId()
			if !e.ownScope(d.proposal.GetRunId(), run) {
				v = weaker(v, suspected)
			}
			if byRun[run] == nil {
				byRun[run] = &retryFinding{}
			}
			byRun[run].by[rank(v)].event(r, r.proposal)
		})
		if !judged {
			out = append(out, unjudgedDrafts(rule, d, g.mayRetry(d, keyOf(d)))...)
			continue
		}
		out = append(out, retryDrafts(rule, d, byRun)...)
	}
	return out
}

// unapproved reports whether rq may be a retry, and the most it can say: a
// call the approvals store approved is none, and one still waiting on an
// approval may yet be approved.
func (rq *request) unapproved() (controlv1.FindingVerdict, bool) {
	switch {
	case rq.approval == controlv1.ApprovalState_APPROVAL_STATE_APPROVED && !rq.doubt:
		return indeterminate, false
	case rq.waiting:
		return indeterminate, true
	}
	return confirmed, true
}

// retriedArguments fires for a call of the denied tool and upstream with
// other arguments. A hash not read cannot tell a variant from the call
// repeated.
func (e *evaluation) retriedArguments() []draft {
	x := e.retryIx()
	return e.retried(RuleDeniedActionRetriedArguments, func(d *request) *retryGroup { return x.byTool[toolOf(d)] }, hashOf,
		func(d, r *request) (controlv1.FindingVerdict, bool) {
			hd, hr := d.proposal.GetProposed().GetArguments().GetCanonicalHash(), r.proposal.GetProposed().GetArguments().GetCanonicalHash()
			switch {
			case hd == "" || hr == "":
				return indeterminate, true
			case hd == hr:
				return indeterminate, false
			}
			return confirmed, true
		})
}

// retriedResource fires for a call of another tool or upstream on the
// denied call's resource. A read after the denial of anything but a read
// does not count; an effect class not declared, or one this build does not
// know, might be either.
func (e *evaluation) retriedResource() []draft {
	x := e.retryIx()
	return e.retried(RuleDeniedActionRetriedResource, func(d *request) *retryGroup { return x.byResource[resourceOf(d)] }, toolKey,
		func(d, r *request) (controlv1.FindingVerdict, bool) {
			if toolOf(d) == toolOf(r) {
				return indeterminate, false
			}
			dRead, dKnown := isRead(d)
			rRead, rKnown := isRead(r)
			switch {
			case dKnown && dRead, rKnown && !rRead:
				return confirmed, true
			case dKnown && rKnown:
				return indeterminate, false
			}
			return indeterminate, true
		})
}

// isRead reports whether rq's effect class is a read, and whether it is one
// this build declares, other than none.
func isRead(rq *request) (bool, bool) {
	c := rq.proposal.GetProposed().GetAction().GetEffect()
	_, declared := controlv1.EffectClass_name[int32(c)]
	return c == controlv1.EffectClass_EFFECT_CLASS_READ, declared && c != controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED
}

// retriedAround fires for a source's report of the denied tool, under a name
// its entry lists, that no plane call joins and whose time is after the
// denial was decided. A source's clock is not the plane's, so it suggests at
// most; the finding names the denial's run, since a report's claimed run is
// never named.
func (e *evaluation) retriedAround() []draft {
	x := e.retryIx()
	var out []draft
	budget := uint64(MaxRetryPairs)
	for _, d := range x.denials {
		var reports uint64
		for _, name := range x.names[toolOf(d)] {
			reports += uint64(len(x.reported[name]))
		}
		if reports > budget {
			out = append(out, unjudgedDrafts(RuleDeniedActionRetriedAround, d, []string{d.decided.GetRunId()})...)
			continue
		}
		budget -= reports
		f := &retryFinding{}
		for _, name := range x.names[toolOf(d)] {
			for _, o := range x.reported[name] {
				v, ok := timedAfter(d.decided.GetOccurredAt(), o.GetEventTime())
				if !ok {
					continue
				}
				v = weaker(v, e.obsVerdict(o))
				if s := &f.by[rank(v)]; s.take(e.obsVerdict(o)) {
					s.refs = append(s.refs, e.obsRef(o))
				}
			}
		}
		if f.empty() {
			continue
		}
		out = append(out, retryDrafts(RuleDeniedActionRetriedAround, d, map[string]*retryFinding{d.decided.GetRunId(): f})...)
	}
	return out
}
