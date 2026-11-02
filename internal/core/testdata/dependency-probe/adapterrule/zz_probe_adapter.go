// Package ioprobe is the adapter rule's negative fixture.
// scripts/check-imports-probe.sh plants it as a new package under adapters/,
// where importing the reason-code registry and composing a v1 Decision are
// each refused.
package ioprobe

import (
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/reasons"
)

// Codes spells the registry from an adapter.
var Codes = reasons.All()

// Literal composes a Decision by a composite literal.
func Literal() *controlv1.Decision { return &controlv1.Decision{DecisionId: "probe"} }

// Made composes a Decision by new.
func Made() *controlv1.Decision { return new(controlv1.Decision) }
