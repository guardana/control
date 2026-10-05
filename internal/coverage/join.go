package coverage

import (
	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

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
// observations of its tenant and project.
func (f family) spans(o *observev1.Observation) []string {
	root := spanKey{o.GetTenantId(), o.GetProjectId(), o.GetCorrelation().GetTraceId(), o.GetCorrelation().GetSpanId()}
	seen := map[string]bool{root.span: true}
	out := []string{root.span}
	for i := 0; i < len(out); i++ {
		parent := root
		parent.span = out[i]
		for _, child := range f[parent] {
			if !seen[child] {
				seen[child] = true
				out = append(out, child)
			}
		}
	}
	return out
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
func joinOf(o *observev1.Observation, f family, path Path, need modeClass, planes []Plane) JoinCheck {
	check := JoinCheck{ObservationID: o.GetObservationId()}
	trace, span := o.GetCorrelation().GetTraceId(), o.GetCorrelation().GetSpanId()
	if trace == "" || span == "" {
		check.Why = "no trace or span id"
		return check
	}
	spans := f.spans(o)
	for _, p := range planes {
		if x := p.Export; x != nil && x.Whole && x.holds(o, trace, spans, path, need) {
			check.Join = Joined
			return check
		}
	}
	check.Join, check.Why = unjoined(o, planes)
	return check
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
	env := ev.GetProposed()
	action := env.GetAction()
	class, err := classOf(ev.GetEnforcementMode())
	return err == nil && class >= need && sameScope(ev, env, o) && action.GetKind() == actionKindTool &&
		action.GetName() == path.Tool && action.GetProvider() == path.Upstream
}

func sameScope(ev *controlv1.Event, env *controlv1.ActionEnvelope, o *observev1.Observation) bool {
	return ev.GetTenantId() == o.GetTenantId() && env.GetTenantId() == o.GetTenantId() &&
		ev.GetProjectId() == o.GetProjectId() && env.GetProjectId() == o.GetProjectId()
}
