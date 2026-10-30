package operators

import (
	"context"
	"fmt"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/trust"
)

// Authorizer is the operator authorizer port: whether an authenticated
// operator may do an act over one tenant's one project.
type Authorizer interface {
	Authorize(ctx context.Context, who Authentication, req Request) Permission
	Status() Status
}

// AuthorizerKind is how a plane authorizes operators. It is a closed set.
type AuthorizerKind string

// AuthorizerRoster permits what the grants of the signing key, in the roster
// the plane holds when it asks, permit. It is the default.
const AuthorizerRoster AuthorizerKind = "roster"

// ParseAuthorizerKind reads a configured kind: the empty kind is the
// default, and every kind this build lacks is ErrUnknownKind.
func ParseAuthorizerKind(s string) (AuthorizerKind, error) {
	switch AuthorizerKind(s) {
	case "", AuthorizerRoster:
		return AuthorizerRoster, nil
	}
	return "", fmt.Errorf("%w: authorizer %q", ErrUnknownKind, s)
}

// NewAuthorizer starts the authorizer of kind over roster, its calls to the
// roster held to bounds.
func NewAuthorizer(kind AuthorizerKind, roster trust.Roster, bounds Bounds) (Authorizer, error) {
	k, err := ParseAuthorizerKind(string(kind))
	if err != nil {
		return nil, err
	}
	if roster == nil {
		return nil, fmt.Errorf("%w: the authorizer has no trust roster", ErrConfig)
	}
	c, err := newCaller(bounds)
	if err != nil {
		return nil, err
	}
	return &rosterAuthorizer{kind: k, roster: roster, caller: c}, nil
}

type rosterAuthorizer struct {
	kind   AuthorizerKind
	roster trust.Roster
	*caller
}

func (a *rosterAuthorizer) Status() Status {
	return Status{Kind: string(a.kind), Counts: a.snapshot()}
}

// Authorize permits req when who is authenticated for req's act, req names
// one tenant and one project, the roster held now still names the key that
// signed with the same subject, and one of that key's grants is over req's
// tenant and project at the act's level or above. A grant over another
// project, or an empty one, permits nothing.
func (a *rosterAuthorizer) Authorize(ctx context.Context, who Authentication, req Request) Permission {
	out := a.authorize(ctx, who, req)
	a.count(out.cause)
	return out
}

func (a *rosterAuthorizer) authorize(ctx context.Context, who Authentication, req Request) Permission {
	rule, refusal := admissible(who, req)
	if refusal != nil {
		return *refusal
	}
	now, refusal := a.currentKey(ctx, who)
	if refusal != nil {
		return *refusal
	}
	for _, g := range now.Grants() {
		if g.Tenant == req.Tenant && g.Project == req.Project && g.Level >= rule.level {
			return Permission{state: PermPermitted, grant: g, rosterSerial: now.RosterSerial()}
		}
	}
	return permDenied(CauseNoGrant)
}

// admissible refuses an authentication that is not one, an act it was not
// made for, and a request that names no one tenant and one project.
func admissible(who Authentication, req Request) (actRule, *Permission) {
	refuse := func(p Permission) (actRule, *Permission) { return actRule{}, &p }
	switch who.State() {
	case AuthAuthenticated:
	case AuthRefused:
		return refuse(permDenied(CauseNotAuthenticated))
	default:
		return refuse(permUnavailable(CauseNotAuthenticated))
	}
	rule, ok := acts[req.Act]
	switch {
	case !ok || who.Act() != req.Act:
		return refuse(permDenied(CauseAct))
	case !scope(req.Tenant) || !scope(req.Project):
		return refuse(permDenied(CauseScope))
	}
	return rule, nil
}

// currentKey asks the roster again for the key that signed, and refuses one
// it no longer names for the same subject as an operator.
func (a *rosterAuthorizer) currentKey(ctx context.Context, who Authentication) (trust.Key, *Permission) {
	refuse := func(p Permission) (trust.Key, *Permission) { return trust.Key{}, &p }
	answer, cause := call(ctx, a.caller, func(ctx context.Context) trust.Answer { return a.roster.Lookup(ctx, who.KeyID()) })
	if cause != CauseNone {
		return refuse(permUnavailable(cause))
	}
	switch answer.State() {
	case trust.StateKey:
	case trust.StateUnknownKey:
		return refuse(permDenied(CauseRevoked))
	default:
		return refuse(permUnavailable(CauseRosterUnavailable))
	}
	now := answer.Key()
	switch {
	case !now.SameKey(who.key) || now.Subject() != who.Subject():
		return refuse(permDenied(CauseRevoked))
	case now.Role() != trust.RoleOperator:
		return refuse(permDenied(CauseRole))
	}
	return now, nil
}

func scope(s string) bool { return policy.ValidID(s) }
