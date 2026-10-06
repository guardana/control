package coverage

import (
	"fmt"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// MaxJoinSteps bounds the span steps one map walks to join observations to
// proposals. A join whose walk the bound cut is not checked, never joined or
// a call around the plane.
const MaxJoinSteps = 1 << 20

// actionKindTool is how an envelope's action kind names a tool call. A prompt
// or a resource read through the plane under a tool's name is not the call.
const actionKindTool = "tool"

// spanKey is a span of one trace among one tenant's project's observations.
type spanKey struct{ tenant, project, trace, span string }

// family indexes one source's observations by tenant, project, trace and
// parent span, so the descendants of a span are found without a scan per
// observation, and never among another tenant's or project's observations.
type family map[spanKey][]string

func familyOf(src *Source) family {
	f := family{}
	for _, r := range src.Records {
		o := r.GetObservation()
		c := o.GetCorrelation()
		if o == nil || o.GetSource().GetSourceId() != src.Descriptor.GetSourceId() ||
			c.GetTraceId() == "" || c.GetParentSpanId() == "" || c.GetSpanId() == "" {
			continue
		}
		key := spanKey{o.GetTenantId(), o.GetProjectId(), c.GetTraceId(), c.GetParentSpanId()}
		f[key] = append(f[key], c.GetSpanId())
	}
	return f
}

// spans is o's span and every span below it in its trace, among the
// observations of its tenant and project. Each span taken is one of the map's
// steps; ok is false when the steps ran out before the walk ended.
func (f family) spans(o *observev1.Observation, steps *int) (out []string, ok bool) {
	root := spanKey{o.GetTenantId(), o.GetProjectId(), o.GetCorrelation().GetTraceId(), o.GetCorrelation().GetSpanId()}
	seen := map[string]bool{}
	take := func(span string) bool {
		if *steps >= MaxJoinSteps {
			return false
		}
		*steps++
		seen[span] = true
		out = append(out, span)
		return true
	}
	if !take(root.span) {
		return nil, false
	}
	for i := 0; i < len(out); i++ {
		parent := root
		parent.span = out[i]
		for _, child := range f[parent] {
			if !seen[child] && !take(child) {
				return nil, false
			}
		}
	}
	return out, true
}

// joinOf checks one observation of path against the exports of the planes
// that classify it. It joins when a proposal in a whole export, recorded in a
// mode at least as strong as need, carries its trace id and its own or a
// descendant's span id, in its tenant and project, for a call of the path's
// tool on its upstream: an enforced path joins only an enforcing plane's
// proposal, so an observing plane cannot cover a call around an enforcing
// one. Nothing joins on an empty id, and a trace id alone joins nothing. A
// join needs no event time: the time only bounds calling a call one around
// the plane.
func joinOf(o *observev1.Observation, f family, path Path, need modeClass, planes []Plane, steps *int) JoinCheck {
	check := JoinCheck{ObservationID: o.GetObservationId()}
	trace, span := o.GetCorrelation().GetTraceId(), o.GetCorrelation().GetSpanId()
	if trace == "" || span == "" {
		check.Why = "no trace or span id"
		return check
	}
	spans, ok := f.spans(o, steps)
	if !ok {
		check.Why = fmt.Sprintf("the span walk stopped at its bound of %d steps", MaxJoinSteps)
		return check
	}
	for _, p := range planes {
		if x := p.Export; x != nil && x.Whole && x.holds(o, trace, spans, path, need) {
			check.Join = Joined
			return check
		}
	}
	for _, p := range planes {
		if x := p.Export; x != nil && x.refusedAt(o, trace, spans, path) {
			check.Why = "the record of a call at its span in the export of plane " + printable(p.Name) + " does not validate"
			return check
		}
	}
	check.Join, check.Why = unjoined(o, planes)
	return check
}

// refusedAt reports a proposal the contract refuses under the trace and one
// of the spans that names o's call as a valid one would, whatever its mode:
// the plane saw that call, but its record cannot be joined.
func (x *Export) refusedAt(o *observev1.Observation, trace string, spans []string, path Path) bool {
	for _, span := range spans {
		for _, ev := range x.refused[proposalKey{trace, span}] {
			if names(ev, o, path) {
				return true
			}
		}
	}
	return false
}

// unjoined says whether the planes' exports could have shown a join they do
// not hold: the call may have passed any plane in front of the path, so each
// one's export must be given, whole, and hold the observation's time.
func unjoined(o *observev1.Observation, planes []Plane) (Join, string) {
	for _, p := range planes {
		switch x := p.Export; {
		case x == nil:
			return JoinNotChecked, "no export of plane " + printable(p.Name)
		case !x.Whole:
			return JoinNotChecked, "the export of plane " + printable(p.Name) + " is not whole: " + x.NotWhole
		}
	}
	if !o.GetEventTime().IsValid() {
		return JoinNotChecked, "no event time"
	}
	t := o.GetEventTime().AsTime()
	for _, p := range planes {
		if x := p.Export; !x.window || t.Before(x.earliest) || t.After(x.latest) {
			return JoinNotChecked, "outside the window of the export of plane " + printable(p.Name)
		}
	}
	return JoinAround, ""
}

func (x *Export) holds(o *observev1.Observation, trace string, spans []string, path Path, need modeClass) bool {
	for _, span := range spans {
		for _, ev := range x.proposals[proposalKey{trace, span}] {
			if proposes(ev, o, path, need) {
				return true
			}
		}
	}
	return false
}

// proposes reports whether ev, a proposal carrying o's ids, is a call of
// path's tool in o's tenant and project, recorded in a mode at least need.
func proposes(ev *controlv1.Event, o *observev1.Observation, path Path, need modeClass) bool {
	class, err := classOf(ev.GetEnforcementMode())
	return err == nil && class >= need && names(ev, o, path)
}

// names reports whether ev, a proposal carrying o's ids, names a call of
// path's tool on its upstream in o's tenant and project. An empty tenant or
// project names nothing: a refused envelope may lack both.
func names(ev *controlv1.Event, o *observev1.Observation, path Path) bool {
	action := ev.GetProposed().GetAction()
	return sameScope(ev, ev.GetProposed(), o) && action.GetKind() == actionKindTool &&
		action.GetName() == path.Tool && action.GetProvider() == path.Upstream
}

func sameScope(ev *controlv1.Event, env *controlv1.ActionEnvelope, o *observev1.Observation) bool {
	return o.GetTenantId() != "" && o.GetProjectId() != "" &&
		ev.GetTenantId() == o.GetTenantId() && env.GetTenantId() == o.GetTenantId() &&
		ev.GetProjectId() == o.GetProjectId() && env.GetProjectId() == o.GetProjectId()
}
