package rules

import (
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// Each group below is read in one composite literal, whose calls run in the
// order written, and checked in the statement after it: Go does not order a
// plain read of r.err against the calls in the same expression.

// readWhen reads a rule's constraints. A rule that constrains nothing would
// match every call, so when is required and never empty. An absent group
// constrains nothing; a present one constrains something.
func readWhen(v any, a at) (When, error) {
	m, err := group(v, a, "principal", "agent", "action", "resource", "destination", "data", "flow", "delegation", "external")
	if err != nil {
		return When{}, err
	}
	var r reader
	w := When{
		Principal:   optional(&r, m, "principal", a, readPrincipal),
		Agent:       optional(&r, m, "agent", a, readAgent),
		Action:      optional(&r, m, "action", a, readAction),
		Resource:    optional(&r, m, "resource", a, readResource),
		Destination: optional(&r, m, "destination", a, readDestination),
		Data:        optional(&r, m, "data", a, readData),
		Flow:        optional(&r, m, "flow", a, readFlow),
		Delegation:  optional(&r, m, "delegation", a, readDelegation),
		External:    optional(&r, m, "external", a, readExternal),
	}
	if r.err != nil {
		return When{}, r.err
	}
	return w, nil
}

// done hands out a group, or nothing beside a refusal.
func done[T any](g *T, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	return g, nil
}

func readPrincipal(v any, a at) (*PrincipalWhen, error) {
	m, err := group(v, a, "id", "type", "authnStrength", "tenantId", "attributes")
	if err != nil {
		return nil, err
	}
	var r reader
	w := &PrincipalWhen{
		ID:            optional(&r, m, "id", a, identifiers),
		Type:          optional(&r, m, "type", a, identifiers),
		AuthnStrength: optional(&r, m, "authnStrength", a, identifiers),
		TenantID:      optional(&r, m, "tenantId", a, identifiers),
		Attributes:    optional(&r, m, "attributes", a, valueMap),
	}
	return done(w, r.err)
}

func readAgent(v any, a at) (*AgentWhen, error) {
	m, err := group(v, a, "id", "framework")
	if err != nil {
		return nil, err
	}
	var r reader
	w := &AgentWhen{
		ID:        optional(&r, m, "id", a, identifiers),
		Framework: optional(&r, m, "framework", a, identifiers),
	}
	return done(w, r.err)
}

func readAction(v any, a at) (*ActionWhen, error) {
	m, err := group(v, a, "name", "provider", "protocol", "kind", "effect")
	if err != nil {
		return nil, err
	}
	var r reader
	w := &ActionWhen{
		Name:     optional(&r, m, "name", a, identifiers),
		Provider: optional(&r, m, "provider", a, identifiers),
		Protocol: optional(&r, m, "protocol", a, identifiers),
		Kind:     optional(&r, m, "kind", a, identifiers),
		Effect:   optional(&r, m, "effect", a, effectClasses),
	}
	return done(w, r.err)
}

func readResource(v any, a at) (*ResourceWhen, error) {
	m, err := group(v, a, "type", "id", "tenantId", "environment", "labels")
	if err != nil {
		return nil, err
	}
	var r reader
	w := &ResourceWhen{
		Type:        optional(&r, m, "type", a, identifiers),
		ID:          optional(&r, m, "id", a, identifiers),
		TenantID:    optional(&r, m, "tenantId", a, identifiers),
		Environment: optional(&r, m, "environment", a, identifiers),
		Labels:      optional(&r, m, "labels", a, valueMap),
	}
	return done(w, r.err)
}

func readDestination(v any, a at) (*DestinationWhen, error) {
	m, err := group(v, a, "trustZone", "host")
	if err != nil {
		return nil, err
	}
	var r reader
	w := &DestinationWhen{
		TrustZone: optional(&r, m, "trustZone", a, trustZones),
		Host:      optional(&r, m, "host", a, hosts),
	}
	return done(w, r.err)
}

// readData reads the floor the envelope's own labels are compared with.
func readData(v any, a at) (*DataWhen, error) {
	m, err := group(v, a, "sensitivityAtLeast")
	if err != nil {
		return nil, err
	}
	var r reader
	w := &DataWhen{SensitivityAtLeast: need(&r, m, "sensitivityAtLeast", a, floor)}
	return done(w, r.err)
}

// readFlow reads the floor the run's flow is compared with.
func readFlow(v any, a at) (*FlowWhen, error) {
	m, err := group(v, a, "toxicAtLeast")
	if err != nil {
		return nil, err
	}
	var r reader
	w := &FlowWhen{ToxicAtLeast: need(&r, m, "toxicAtLeast", a, floor)}
	return done(w, r.err)
}

func readDelegation(v any, a at) (*DelegationWhen, error) {
	m, err := group(v, a, "scopes")
	if err != nil {
		return nil, err
	}
	var r reader
	w := &DelegationWhen{Scopes: need(&r, m, "scopes", a, identifiers)}
	return done(w, r.err)
}

// readExternal reads the one form of the external constraint, a denial. A
// false would read as the decision point allowing, which no rule may act on.
func readExternal(v any, a at) (*ExternalWhen, error) {
	m, err := group(v, a, "denies")
	if err != nil {
		return nil, err
	}
	var r reader
	w := &ExternalWhen{Denies: need(&r, m, "denies", a, denial)}
	return done(w, r.err)
}

func denial(v any, a at) (bool, error) {
	b, err := flag(v, a)
	if err != nil {
		return false, err
	}
	if !b {
		return false, a.refuse(ErrExternal)
	}
	return true, nil
}

func effectClasses(v any, a at) ([]controlv1.EffectClass, error) {
	return each(v, a, maxValues, effectClass)
}

func trustZones(v any, a at) ([]controlv1.TrustZone, error) {
	return each(v, a, maxValues, trustZone)
}
