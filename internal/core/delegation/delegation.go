// Package delegation checks a delegation chain against the receiver's clock:
// expiry, cycles, and that no hop claims more than its parent (ADR-0012).
package delegation

import (
	"slices"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// The codes Check refuses with. They are the registry's identifiers, kept here
// as literals because nothing on the decision path imports the registry
// (ADR-0012); a test holds each one to it.
const (
	codeExpired       = "DELEGATION_EXPIRED"
	codeCycle         = "DELEGATION_CYCLE"
	codeExceedsParent = "DELEGATION_EXCEEDS_PARENT"
)

// Effective is what a chain that passed Check grants.
type Effective struct {
	// Delegated is false when there was no chain at all.
	Delegated bool
	// Scopes are the last hop's scopes; an empty list grants nothing.
	Scopes []string
}

// Error is a refused chain. Code is DELEGATION_EXPIRED, DELEGATION_CYCLE or
// DELEGATION_EXCEEDS_PARENT.
type Error struct {
	Code string
}

// Error names the code and nothing else: a party or a scope is a producer's
// text, and this message can reach a log.
func (e *Error) Error() string {
	return "delegation: " + e.Code
}

// Check refuses a chain that has expired, that meets a party twice, or whose
// hop claims a scope its parent did not hold (ADR-0012). A chain it passes
// grants a copy of its last hop's scopes. No chain grants nothing and is not a
// refusal. A refusal grants nothing either, so a caller that drops the error
// holds no scopes.
//
// The chain is read root outwards, as the envelope carries it. Check does not
// establish that the hops link, start at the principal or end at the agent:
// contract.Validate does, and the kernel runs it first.
//
// A chain wrong in several ways gets one code, chosen by kind rather than by
// position: an expired hop, then a party met twice, then a hop beyond its
// parent.
func Check(chain []*controlv1.Delegation, now time.Time) (Effective, error) {
	if len(chain) == 0 {
		return Effective{}, nil
	}
	switch {
	case anyExpired(chain, now):
		return Effective{}, &Error{Code: codeExpired}
	case meetsAPartyTwice(chain):
		return Effective{}, &Error{Code: codeCycle}
	case anyExceedsParent(chain):
		return Effective{}, &Error{Code: codeExceedsParent}
	}
	return Effective{Delegated: true, Scopes: slices.Clone(chain[len(chain)-1].GetScopes())}, nil
}

// anyExpired is true when a hop's expiry is at or before now, or is not an
// expiry at all: absent, or a value a Timestamp cannot hold. Converting one of
// those still yields an instant, and it can lie after now, so it is refused
// before it is compared.
func anyExpired(chain []*controlv1.Delegation, now time.Time) bool {
	for _, hop := range chain {
		expires := hop.GetExpiresAt()
		if expires.CheckValid() != nil || !expires.AsTime().After(now) {
			return true
		}
	}
	return false
}

// meetsAPartyTwice counts every party the chain names. A hop's sender is the
// same naming as the recipient of the hop before it when the two are equal,
// which is what linked hops are, and a naming of its own otherwise: a chain
// cannot hide a repeat by not linking.
func meetsAPartyTwice(chain []*controlv1.Delegation) bool {
	named := make(map[string]bool, len(chain)+1)
	meet := func(party string) bool {
		if named[party] {
			return true
		}
		named[party] = true
		return false
	}
	for i, hop := range chain {
		if (i == 0 || hop.GetFrom() != chain[i-1].GetTo()) && meet(hop.GetFrom()) {
			return true
		}
		if meet(hop.GetTo()) {
			return true
		}
	}
	return false
}

// anyExceedsParent compares each hop with the hop before it, scope by scope as
// exact strings: no scope covers another by case, prefix or pattern, and an
// empty parent holds nothing to pass on. The first hop's parent is the
// principal, whose authority the chain does not state.
func anyExceedsParent(chain []*controlv1.Delegation) bool {
	for i := 1; i < len(chain); i++ {
		held := make(map[string]bool, len(chain[i-1].GetScopes()))
		for _, scope := range chain[i-1].GetScopes() {
			held[scope] = true
		}
		for _, scope := range chain[i].GetScopes() {
			if !held[scope] {
				return true
			}
		}
	}
	return false
}
