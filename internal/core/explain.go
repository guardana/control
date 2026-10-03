package core

import (
	"context"
	"errors"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/match"
	"github.com/guardana/control/pkg/contract"
)

// Explanation says how the kernel reached one decision: what it found before
// the policy and how each rule read the call. It names fields, never their
// values.
type Explanation struct {
	// Refusal is set when steps 1 and 2 stopped the decision.
	Refusal *Refusal
	// TenantUnstated is the tenant field a material call left out while the
	// other side named one, in wire spelling.
	TenantUnstated string
	// Clock is what the clock step made of the decision's reading.
	Clock ClockState
	// Delegation is what step 3 made of the chain.
	Delegation Delegation
	// Rules are how each rule read the call, in document order; nil when the
	// policy was not evaluated.
	Rules []match.RuleTrace
}

// Refusal is what stopped a request before the policy. Field is the envelope
// field the refusal named, in wire spelling, and empty when it named none: the
// message as a whole, or the arguments, which the reason code tells apart.
type Refusal struct {
	Field string
}

// ClockState is the clock step's reading of the decision's first clock
// reading. The zero value is a reading nobody checked, because the request
// was refused first.
type ClockState uint8

const (
	// ClockNotChecked is a request refused before the clock step.
	ClockNotChecked ClockState = iota
	// ClockUsable is a reading the decision went on with.
	ClockUsable
	// ClockOutOfRange is a reading policy.UsableTime refuses: before 1970 or
	// after the last instant a Timestamp holds.
	ClockOutOfRange
	// ClockBeforeVerified is a reading earlier than the snapshot's
	// NotBefore, a time the plane verified.
	ClockBeforeVerified
)

// DelegationState is step 3's reading of the chain. The zero value is a chain
// nobody checked, because the request was refused first or the clock step
// stopped the decision.
type DelegationState uint8

const (
	// DelegationNotChecked is a request stopped before step 3.
	DelegationNotChecked DelegationState = iota
	// DelegationAbsent is a call that carried no chain.
	DelegationAbsent
	// DelegationRefused is a chain the check refused, a DENY of the kernel's.
	DelegationRefused
	// DelegationPassed is a chain that passed, whose scopes the policy reads.
	DelegationPassed
)

// Delegation is the chain's state and, when it was refused, the refusal's code.
type Delegation struct {
	State DelegationState
	Code  string
}

// Explain decides req exactly as Decide does and also says how. It is for a
// person reading one decision, never for the request path.
func (k *Kernel) Explain(_ context.Context, req Request, snap *policy.Snapshot) (Outcome, Explanation) {
	var e Explanation
	out := k.decide(req, snap, &e)
	return out, e
}

// refusedField is the field a refusal names. A ValidationError's Field never
// carries caller data; its message may, and is never read here. A nil
// *ValidationError inside the error names nothing.
func refusedField(err error) string {
	var refused *contract.ValidationError
	if errors.As(err, &refused) && refused != nil {
		return refused.Field
	}
	return ""
}
