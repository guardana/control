package match

import (
	"slices"

	"google.golang.org/protobuf/reflect/protoreflect"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// truth is a constraint's value. The zero value is unknown, so an answer
// nobody computed is never read as false, which a DENY rule would take for
// "does not apply".
type truth uint8

const (
	unknown truth = iota
	no
	yes
)

// constraint reads one field of the envelope or of the kernel's inputs.
type constraint func(env *controlv1.ActionEnvelope, in *Inputs) truth

// holds is a rule's value: false if any constraint is false, else unknown if
// any is unknown, else true, whatever order the constraints are read in. The
// second result is whether that value turns on the external answer: the rule
// reads it and no other constraint is false, whatever the answer.
func (r *rule) holds(env *controlv1.ActionEnvelope, in *Inputs) (truth, bool) {
	result := yes
	for _, c := range r.constraints {
		v := c(env, in)
		if v == no {
			return no, false
		}
		if v != yes {
			result = unknown
		}
	}
	if !r.external {
		return result, false
	}
	switch in.External {
	case ExternalDenies:
		return result, true
	case ExternalAllows:
		return no, true
	}
	return unknown, true
}

// oneOf is "the field is one of these". An empty string is an absent field:
// unknown, never a value that fails to match.
func oneOf(values []string, got string) truth {
	switch {
	case got == "":
		return unknown
	case slices.Contains(values, got):
		return yes
	}
	return no
}

// enum is a generated enum type.
type enum interface {
	comparable
	protoreflect.Enum
}

// enumOneOf is oneOf for an enum field. The zero value is what a producer that
// said nothing leaves behind, and a number this build does not declare is one
// it cannot read; either is an absent input.
func enumOneOf[E enum](values []E, got E) truth {
	switch {
	case !named(got):
		return unknown
	case slices.Contains(values, got):
		return yes
	}
	return no
}

// named reports whether e is a value this build declares, other than zero.
func named[E enum](e E) bool {
	n := e.Number()
	return n != 0 && e.Descriptor().Values().ByNumber(n) != nil
}

// dataAtLeast is data.sensitivityAtLeast (ADR-0012): true when the envelope
// asserts secrets or carries a label at or above the floor, unknown when it
// carries no label and asserts no secrets, false otherwise. It reads the
// envelope's own data and never the run's, which is flow.toxicAtLeast's input.
// contract.SensitivityAtLeast is not this predicate: it answers false for an
// unknown.
//
// A label this build cannot place, SENSITIVITY_UNSPECIFIED or an undeclared
// number, is an unknown part as well. Validate refuses both; read by its number
// an undeclared label would meet every floor, or none.
func dataAtLeast(floor controlv1.Sensitivity) constraint {
	return func(env *controlv1.ActionEnvelope, _ *Inputs) truth {
		data := env.GetData()
		if data.GetContainsSecrets() {
			return yes
		}
		result := no
		if len(data.GetSensitivities()) == 0 {
			result = unknown
		}
		for _, label := range data.GetSensitivities() {
			switch {
			case !contract.ValidSensitivityFloor(label): // not on the scale
				result = unknown
			case contract.SensitivityAtLeast(label, floor):
				return yes
			}
		}
		return result
	}
}

// toxicAtLeast is contract.ToxicFlow's answer, with its refusal read as
// unknown: a run nobody tracked, or data whose sensitivity nobody knows, is
// never a flow that is not toxic.
func toxicAtLeast(floor controlv1.Sensitivity) constraint {
	return func(env *controlv1.ActionEnvelope, in *Inputs) truth {
		toxic, err := contract.ToxicFlow(in.Flow, env, floor)
		switch {
		case err != nil:
			return unknown
		case toxic:
			return yes
		}
		return no
	}
}

// delegatedScope is "the delegated call holds one of these scopes". Without a
// chain that passed the kernel's check the scopes are an absent input; a chain
// that grants nothing is false, not unknown.
func delegatedScope(values []string, in *Inputs) truth {
	if !in.Delegated {
		return unknown
	}
	for _, s := range in.Scopes {
		if slices.Contains(values, s) {
			return yes
		}
	}
	return no
}
