package coverage

import (
	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// family indexes one source's observations by trace and parent span, so the
// descendants of a span are found without a scan per observation.
type family map[[2]string][]string

func familyOf(src *Source) family {
	f := family{}
	for _, r := range src.Records {
		o := r.GetObservation()
		c := o.GetCorrelation()
		if o == nil || o.GetSource().GetSourceId() != src.Descriptor.GetSourceId() ||
			c.GetTraceId() == "" || c.GetParentSpanId() == "" || c.GetSpanId() == "" {
			continue
		}
		key := [2]string{c.GetTraceId(), c.GetParentSpanId()}
		f[key] = append(f[key], c.GetSpanId())
	}
	return f
}

// spans is span and every span below it in trace.
func (f family) spans(trace, span string) map[string]bool {
	seen := map[string]bool{span: true}
	queue := []string{span}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, child := range f[[2]string{trace, next}] {
			if !seen[child] {
				seen[child] = true
				queue = append(queue, child)
			}
		}
	}
	return seen
}

// joinOf checks one observation of path against the exports of the planes
// that classify it. It joins when a proposal in a whole export, recorded in a
// mode at least as strong as need, carries its trace id and its own or a
// descendant's span id, in its tenant and project, for the path's tool on its
// upstream: an enforced path joins only an enforcing plane's proposal, so an
// observing plane cannot cover a call around an enforcing one. Nothing joins
// on an empty id, and a trace id alone joins nothing.
func joinOf(o *observev1.Observation, f family, path Path, need modeClass, planes []Plane) JoinCheck {
	check := JoinCheck{ObservationID: o.GetObservationId()}
	trace, span := o.GetCorrelation().GetTraceId(), o.GetCorrelation().GetSpanId()
	if trace == "" || span == "" {
		check.Why = "no trace or span id"
		return check
	}
	spans := f.spans(trace, span)
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

func (x *Export) holds(o *observev1.Observation, trace string, spans map[string]bool, path Path, need modeClass) bool {
	for _, ev := range x.proposals {
		env := ev.GetProposed()
		class, err := classOf(ev.GetEnforcementMode())
		if err != nil || class < need || env.GetTraceId() != trace || !spans[env.GetSpanId()] {
			continue
		}
		if sameScope(ev, env, o) && env.GetAction().GetName() == path.Tool && env.GetAction().GetProvider() == path.Upstream {
			return true
		}
	}
	return false
}

func sameScope(ev *controlv1.Event, env *controlv1.ActionEnvelope, o *observev1.Observation) bool {
	return ev.GetTenantId() == o.GetTenantId() && env.GetTenantId() == o.GetTenantId() &&
		ev.GetProjectId() == o.GetProjectId() && env.GetProjectId() == o.GetProjectId()
}
