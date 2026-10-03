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

// DelegationState is step 3's reading of the chain. The zero value is a chain
// nobody checked, because the request was refused first or the clock reading
// was before the epoch.
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
