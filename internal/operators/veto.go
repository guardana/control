package operators

import (
	"context"
	"fmt"

	"github.com/guardana/control/internal/trust"
)

// ObjState is what a veto answered. The zero ObjState is ObjUnknown.
type ObjState uint8

// The objection states.
const (
	// ObjUnknown is no answer from the veto: it was unavailable, late, or
	// answered something this build cannot read.
	ObjUnknown ObjState = iota
	// ObjObjects takes the permission away.
	ObjObjects
	// ObjNone leaves the permission as the authorizer before it gave it.
	ObjNone
)

// Objection is a veto's answer. A veto has no way to say "permitted": the
// most it can answer is no objection.
type Objection struct {
	state ObjState
}

// Objects is the objection that takes a permission away.
func Objects() Objection { return Objection{state: ObjObjects} }

// NoObjection is the answer that leaves a permission as it was.
func NoObjection() Objection { return Objection{state: ObjNone} }

// State is what the veto answered.
func (o Objection) State() ObjState { return o.state }

// Review is what a veto is asked about: a permission an authorizer gave.
type Review struct {
	Act             Act
	Tenant, Project string
	Issuer, Subject string
	Level           trust.Level
}

// Veto is a decision point asked after an authorizer permits, which can only
// object.
type Veto interface {
	Review(ctx context.Context, r Review) Objection
}

// OnUnavailable is what a permission becomes when its veto does not answer.
// The zero value blocks.
type OnUnavailable uint8

// The settings of operators.authorizer.on_unavailable.
const (
	// VetoBlocks makes the answer unavailable, which blocks the act.
	VetoBlocks OnUnavailable = iota
	// RosterOnly keeps the roster's permission when the veto gave no answer
	// in time. It is the named risk setting a plane chooses explicitly.
	RosterOnly
)

// ParseOnUnavailable reads the setting: empty or "block" blocks,
// "roster_only" keeps the roster's permission, and anything else is
// ErrConfig.
func ParseOnUnavailable(s string) (OnUnavailable, error) {
	switch s {
	case "", "block":
		return VetoBlocks, nil
	case "roster_only":
		return RosterOnly, nil
	}
	return VetoBlocks, fmt.Errorf("%w: on_unavailable %q", ErrConfig, s)
}

// WithVeto is first, then veto: veto is asked only about what first
// permits, within bounds, and only an answer of no objection leaves that
// permission standing. An objection denies. No answer in time, late or for
// want of a slot, is unavailable unless on is RosterOnly; an answer this
// build cannot read, and a caller that gave up, are unavailable whatever on
// says.
func WithVeto(first Authorizer, veto Veto, on OnUnavailable, bounds Bounds) (Authorizer, error) {
	if first == nil || veto == nil {
		return nil, fmt.Errorf("%w: a veto needs an authorizer before it and a decision point", ErrConfig)
	}
	if on != VetoBlocks && on != RosterOnly {
		return nil, fmt.Errorf("%w: on_unavailable %d", ErrConfig, on)
	}
	c, err := newCaller(bounds)
	if err != nil {
		return nil, err
	}
	return &vetoed{first: first, veto: veto, on: on, caller: c}, nil
}

type vetoed struct {
	first Authorizer
	veto  Veto
	on    OnUnavailable
	*caller
}

func (v *vetoed) Status() Status {
	s := v.first.Status()
	return Status{Kind: s.Kind + "+veto", Counts: v.snapshot()}
}

func (v *vetoed) Authorize(ctx context.Context, who Authentication, req Request) Permission {
	out := v.authorize(ctx, who, req)
	v.count(out.cause)
	return out
}

func (v *vetoed) authorize(ctx context.Context, who Authentication, req Request) Permission {
	p := v.first.Authorize(ctx, who, req)
	if p.State() != PermPermitted {
		return p
	}
	r := Review{Act: req.Act, Tenant: req.Tenant, Project: req.Project, Issuer: who.Issuer(), Subject: who.Subject(), Level: p.grant.Level}
	o, cause := call(ctx, v.caller, func(ctx context.Context) Objection { return v.veto.Review(ctx, r) })
	switch {
	case cause == CauseNone && o.State() == ObjNone:
		return p
	case cause == CauseNone && o.State() == ObjObjects:
		return permDenied(CauseVeto)
	case cause == CauseNone:
		return permUnavailable(CauseVetoUnavailable)
	case v.on == RosterOnly && (cause == CauseLate || cause == CauseBusy):
		return p
	}
	return permUnavailable(cause)
}
