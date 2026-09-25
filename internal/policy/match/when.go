package match

import (
	"maps"
	"slices"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/rules"
	"github.com/guardana/control/pkg/contract"
)

// when collects a rule's constraints and keeps the first refusal with the
// field it was found in. The groups and fields are read in a fixed order, and
// map keys in sorted order, so the same document is always refused for the
// same field.
type when struct {
	constraints []constraint
	external    bool
	field       string
	err         error
}

func (w *when) refuse(field string, err error) {
	if w.err == nil {
		w.field, w.err = field, err
	}
}

func (w *when) add(c constraint) { w.constraints = append(w.constraints, c) }

// compileWhen compiles a rule's constraint groups. A nil group or list
// constrains nothing, as rules.When says; a rule left with no constraint at
// all would match every call, which is why Parse refuses an empty when. The
// external constraint is kept apart from the others, for holds to read last.
func compileWhen(src *rules.When) when {
	var w when
	w.principal(src.Principal)
	w.agent(src.Agent)
	w.action(src.Action)
	w.resource(src.Resource)
	w.destination(src.Destination)
	if d := src.Data; d != nil {
		w.floor("when.data.sensitivityAtLeast", d.SensitivityAtLeast, dataAtLeast)
	}
	if f := src.Flow; f != nil {
		w.floor("when.flow.toxicAtLeast", f.ToxicAtLeast, toxicAtLeast)
	}
	if d := src.Delegation; d != nil {
		w.scopes("when.delegation.scopes", d.Scopes)
	}
	if e := src.External; e != nil {
		if !e.Denies {
			w.refuse("when.external.denies", errExternal)
		}
		w.external = true
	}
	if w.err == nil && len(w.constraints) == 0 && !w.external {
		w.refuse("when", errNoConstraint)
	}
	return w
}

func (w *when) principal(p *rules.PrincipalWhen) {
	if p == nil {
		return
	}
	w.strings("when.principal.id", p.ID, func(e *controlv1.ActionEnvelope) string { return e.GetPrincipal().GetId() })
	w.strings("when.principal.type", p.Type, func(e *controlv1.ActionEnvelope) string { return e.GetPrincipal().GetType() })
	w.strings("when.principal.authnStrength", p.AuthnStrength,
		func(e *controlv1.ActionEnvelope) string { return e.GetPrincipal().GetAuthnStrength() })
	w.strings("when.principal.tenantId", p.TenantID, func(e *controlv1.ActionEnvelope) string { return e.GetPrincipal().GetTenantId() })
	w.stringMap("when.principal.attributes", p.Attributes,
		func(e *controlv1.ActionEnvelope) map[string]string { return e.GetPrincipal().GetAttributes() })
}

func (w *when) agent(a *rules.AgentWhen) {
	if a == nil {
		return
	}
	w.strings("when.agent.id", a.ID, func(e *controlv1.ActionEnvelope) string { return e.GetAgent().GetId() })
	w.strings("when.agent.framework", a.Framework, func(e *controlv1.ActionEnvelope) string { return e.GetAgent().GetFramework() })
}

func (w *when) action(a *rules.ActionWhen) {
	if a == nil {
		return
	}
	w.strings("when.action.name", a.Name, func(e *controlv1.ActionEnvelope) string { return e.GetAction().GetName() })
	w.strings("when.action.provider", a.Provider, func(e *controlv1.ActionEnvelope) string { return e.GetAction().GetProvider() })
	w.strings("when.action.protocol", a.Protocol, func(e *controlv1.ActionEnvelope) string { return e.GetAction().GetProtocol() })
	w.strings("when.action.kind", a.Kind, func(e *controlv1.ActionEnvelope) string { return e.GetAction().GetKind() })
	enumIn(w, "when.action.effect", a.Effect,
		func(e *controlv1.ActionEnvelope) controlv1.EffectClass { return e.GetAction().GetEffect() })
}

func (w *when) resource(r *rules.ResourceWhen) {
	if r == nil {
		return
	}
	w.strings("when.resource.type", r.Type, func(e *controlv1.ActionEnvelope) string { return e.GetResource().GetType() })
	w.strings("when.resource.id", r.ID, func(e *controlv1.ActionEnvelope) string { return e.GetResource().GetId() })
	w.strings("when.resource.tenantId", r.TenantID, func(e *controlv1.ActionEnvelope) string { return e.GetResource().GetTenantId() })
	w.strings("when.resource.environment", r.Environment,
		func(e *controlv1.ActionEnvelope) string { return e.GetResource().GetEnvironment() })
	w.stringMap("when.resource.labels", r.Labels,
		func(e *controlv1.ActionEnvelope) map[string]string { return e.GetResource().GetLabels() })
}

func (w *when) destination(d *rules.DestinationWhen) {
	if d == nil {
		return
	}
	enumIn(w, "when.destination.trustZone", d.TrustZone,
		func(e *controlv1.ActionEnvelope) controlv1.TrustZone { return e.GetDestination().GetTrustZone() })
	w.strings("when.destination.host", d.Host, func(e *controlv1.ActionEnvelope) string { return e.GetDestination().GetHost() })
}

// strings adds "the field is one of these".
func (w *when) strings(field string, values []string, read func(*controlv1.ActionEnvelope) string) {
	if !usable(w, field, values, present) {
		return
	}
	set := slices.Clone(values)
	w.add(func(env *controlv1.ActionEnvelope, _ *Inputs) truth { return oneOf(set, read(env)) })
}

// enumIn adds "the field is one of these" for an enum field.
func enumIn[E enum](w *when, field string, values []E, read func(*controlv1.ActionEnvelope) E) {
	if !usable(w, field, values, named[E]) {
		return
	}
	set := slices.Clone(values)
	w.add(func(env *controlv1.ActionEnvelope, _ *Inputs) truth { return enumOneOf(set, read(env)) })
}

// stringMap adds, for each key, "the value at this key is one of these". All
// the keys have to hold, as every field of a group does. The empty key is a
// key like any other: an envelope may carry it, and one that does not leaves
// it missing, which reads as unknown.
func (w *when) stringMap(field string, want map[string][]string, read func(*controlv1.ActionEnvelope) map[string]string) {
	switch {
	case want == nil:
		return
	case len(want) == 0:
		w.refuse(field, errEmptyList)
		return
	}
	for _, key := range slices.Sorted(maps.Keys(want)) {
		values := want[key]
		switch {
		case len(values) == 0:
			// Under a key, no list at all is not "no constraint": the key
			// was named with nothing to match it against.
			w.refuse(field, errEmptyList)
		case !usable(w, field, values, present):
		default:
			set := slices.Clone(values)
			w.add(func(env *controlv1.ActionEnvelope, _ *Inputs) truth { return oneOf(set, read(env)[key]) })
			continue
		}
		return
	}
}

// floor adds a comparison with a sensitivity floor. An unset floor, or one a
// later minor declares, has no answer this build can give, so the rule would
// never mean what its author wrote.
func (w *when) floor(field string, floor controlv1.Sensitivity, against func(controlv1.Sensitivity) constraint) {
	if !contract.ValidSensitivityFloor(floor) {
		w.refuse(field, errFloor)
		return
	}
	w.add(against(floor))
}

// scopes adds "the delegated call holds one of these scopes".
func (w *when) scopes(field string, values []string) {
	if !usable(w, field, values, present) {
		return
	}
	set := slices.Clone(values)
	w.add(func(_ *controlv1.ActionEnvelope, in *Inputs) truth { return delegatedScope(set, in) })
}

// usable reports whether a list is a constraint to add. A nil list constrains
// nothing; an empty one, and one holding a value no envelope can carry, are
// refused.
func usable[T any](w *when, field string, values []T, valid func(T) bool) bool {
	switch {
	case values == nil:
		return false
	case len(values) == 0:
		w.refuse(field, errEmptyList)
		return false
	case slices.ContainsFunc(values, func(v T) bool { return !valid(v) }):
		w.refuse(field, errUnusable)
		return false
	}
	return true
}

// present is the test a policy's string value has to pass: the empty string is
// what an envelope leaves behind by saying nothing, which is never a match.
func present(s string) bool { return s != "" }
