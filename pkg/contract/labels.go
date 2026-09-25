package contract

import (
	"fmt"
	"strconv"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// SensitivityAtLeast reports whether s is known to meet the floor.
//
// The scale is ordered least sensitive first, so the comparison is on the
// declared numbers. A value added in a later minor extends the top of the
// scale, which makes it satisfy the floors below it: that is the restrictive
// answer for a receiver that cannot name it.
//
// SENSITIVITY_UNSPECIFIED is false in either position, and false here means
// "not known to meet it", never "below it". On the left it is a producer that
// did not say, which is unknown and never evidence that data is public. On the
// right it is a comparison nobody parameterised: an unset floor is not a floor
// of zero, so nothing satisfies it. A check that denies at or above a floor
// cannot take false for a pass when s is unknown; ToxicFlow keeps that case
// apart, and any other such check has to as well.
//
// A floor this build cannot name is false for a different reason and to the
// same effect: by the scale's ordering rule an undeclared number sits above
// every rank declared here, so nothing this build can name reaches it. Both
// answers are the permissive one for a caller that blocks on true, which is why
// ToxicFlow refuses such a floor rather than comparing against it, and why a
// caller that compares here refuses the floor first with ValidSensitivityFloor.
func SensitivityAtLeast(s, floor controlv1.Sensitivity) bool {
	unspecified := controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED
	if s == unspecified || floor == unspecified {
		return false
	}
	return s >= floor
}

// ValidSensitivityFloor reports whether floor is one this build can compare
// against: declared in the contract, and not the zero value absence leaves
// behind.
//
// It exists because a floor is configuration rather than producer data. A
// bundle written against a later minor carries a number this build cannot
// place, and the honest answer to "is this data at least that sensitive" is
// then neither yes nor no. Whoever loads the bundle refuses it here, once, so
// that no evaluation silently answers no.
func ValidSensitivityFloor(floor controlv1.Sensitivity) bool {
	return floor != controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED && declared(floor)
}

// IsUntrusted reports whether a trust zone is one the system does not trust.
//
// An allowlist of the zones that are trusted, so everything else is untrusted
// by construction: TRUST_ZONE_UNSPECIFIED, because an envelope that does not
// say must not be read as the safe case, and a zone added in a later minor,
// because a receiver that cannot name a zone has no ground on which to trust
// it.
//
// Which declared zones are on the allowlist is this package's default and not
// something the contract states: common.proto says only that UNSPECIFIED counts
// as untrusted. An operator who wants a narrower set ("secrets do not leave the
// building, partner or not") narrows it in policy, where an operator's opinion
// belongs, rather than here.
func IsUntrusted(t controlv1.TrustZone) bool {
	switch t {
	case controlv1.TrustZone_TRUST_ZONE_TRUSTED_INTERNAL, controlv1.TrustZone_TRUST_ZONE_PARTNER:
		return false
	default:
		return true
	}
}

// FlowState is what the run is known to have taken in before the action being
// decided: whether any of it was under untrusted influence, and the highest
// sensitivity it reached.
//
// The receiver computes this from envelopes it has already validated and from
// the operator's declared classification of what each call returns; it is
// never decoded from a producer. That is what makes plain fields safe here. On
// the wire, false is the value a producer leaves behind by saying nothing, so
// the influence field would have to be spelled the other way round.
//
// MaxSensitivityRead of SENSITIVITY_UNSPECIFIED is unknown, not "nothing": a
// tracker that knows the run read nothing above PUBLIC, or nothing at all, says
// SENSITIVITY_PUBLIC.
//
// The Go zero value is the same trap in a different place: FlowState{} is what
// a caller holds after a tracker returned an error it dropped, and every field
// in it is the permissive answer. Only NewFlowState marks a state as computed,
// and ToxicFlow refuses one that is not.
type FlowState struct {
	UntrustedInfluence bool
	MaxSensitivityRead controlv1.Sensitivity

	computed bool
}

// NewFlowState records what a run took in. It is the only way to obtain a state
// ToxicFlow will answer about, which is what stops a dropped error from
// reaching the predicate as "nothing untrusted, nothing sensitive".
func NewFlowState(untrustedInfluence bool, maxSensitivityRead controlv1.Sensitivity) FlowState {
	return FlowState{
		UntrustedInfluence: untrustedInfluence,
		MaxSensitivityRead: maxSensitivityRead,
		computed:           true,
	}
}

// ToxicFlow reports whether this action moves data that was read under
// untrusted influence, at or above floor, to a destination that is not trusted.
//
// A predicate over declared facts. It performs no I/O, reads no clock and
// inspects no string for a pattern: the action's name, its provider and its
// arguments change nothing here, which is what ADR-0003 asks of the decision
// path.
//
// The answer has three values (ADR-0011). Toxic when the run was under
// untrusted influence, the destination is untrusted and a known part of what
// the call carries is at or above the floor: what the run read, a label the
// envelope declares, or contains_secrets, which counts as SENSITIVITY_SECRET.
// Not toxic when influence is absent, the destination is trusted, or every part
// is known and below the floor. Otherwise unknown, which is a refusal carrying
// ErrMissingField: some part is unknown (the run's reading is
// SENSITIVITY_UNSPECIFIED, the envelope carries no label, or a label is
// SENSITIVITY_UNSPECIFIED) and nothing known reaches the floor. Answering false
// there would give a producer that said nothing the answer a producer that
// declared PUBLIC gets.
//
// It refuses first, whatever the rest would answer, a question it cannot
// evaluate: a FlowState nobody computed (ErrMissingField), and a floor, a
// reading or a label the scale does not hold (ErrInvalidEnum). Read by its
// number, such a value lands below PUBLIC or above SECRET, and either answer
// would be made up. A refusal is the caller's cue to hold the action
// indeterminate, which is what common.proto asks of a receiver holding a
// number it cannot name.
//
// A nil envelope and an envelope carrying no destination both yield
// TRUST_ZONE_UNSPECIFIED, which IsUntrusted counts as untrusted, because a
// destination nobody described is not one anybody vouched for.
func ToxicFlow(state FlowState, env *controlv1.ActionEnvelope, floor controlv1.Sensitivity) (bool, error) {
	if !state.computed {
		return false, &ValidationError{Err: fmt.Errorf("%w: a flow state nobody computed", ErrMissingField)}
	}
	if !ValidSensitivityFloor(floor) {
		return false, &ValidationError{Err: fmt.Errorf("%w: %d is not a floor this build can compare against", ErrInvalidEnum, floor)}
	}
	if err := checkOnTheScale(state.MaxSensitivityRead, env.GetData().GetSensitivities()); err != nil {
		return false, err
	}
	if !state.UntrustedInfluence || !IsUntrusted(env.GetDestination().GetTrustZone()) {
		return false, nil
	}
	switch reach(state.MaxSensitivityRead, env.GetData(), floor) {
	case reached:
		return true, nil
	case unknown:
		return false, &ValidationError{
			Err: fmt.Errorf("%w: nothing known reaches the floor and part of the data has no known sensitivity", ErrMissingField),
		}
	default:
		return false, nil
	}
}

// answer is a three-valued comparison with a floor.
type answer int

const (
	below answer = iota
	unknown
	reached
)

// reach compares what the call carries with the floor one part at a time. A
// known part at or above the floor settles it; failing that, an unknown part
// leaves it unknown. max() over the parts is the defect this replaces: it reads
// an unknown as the lowest rank, so a declared PUBLIC turned an unknown run
// into a known PUBLIC one.
//
// contains_secrets is read in one direction only. True is a known part at
// SENSITIVITY_SECRET; false is a producer that did not assert secrets rather
// than one that established their absence, so it is no part at all.
func reach(run controlv1.Sensitivity, d *controlv1.DataLabels, floor controlv1.Sensitivity) answer {
	unspecified := controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED
	if d.GetContainsSecrets() && SensitivityAtLeast(controlv1.Sensitivity_SENSITIVITY_SECRET, floor) {
		return reached
	}
	if SensitivityAtLeast(run, floor) {
		return reached
	}
	result := below
	if run == unspecified || len(d.GetSensitivities()) == 0 {
		result = unknown
	}
	for _, s := range d.GetSensitivities() {
		if SensitivityAtLeast(s, floor) {
			return reached
		}
		if s == unspecified {
			result = unknown
		}
	}
	return result
}

// checkOnTheScale refuses a reading or a label this build cannot place. The
// zero value passes: it is unknown, which reach handles, not malformed.
func checkOnTheScale(run controlv1.Sensitivity, labels []controlv1.Sensitivity) error {
	if !declared(run) {
		return &ValidationError{Err: fmt.Errorf("%w: a run reading of %d is not on the scale", ErrInvalidEnum, run)}
	}
	for i, s := range labels {
		if !declared(s) {
			return &ValidationError{
				Field: "data.sensitivities[" + strconv.Itoa(i) + "]",
				Err:   fmt.Errorf("%w: %d is not on the scale", ErrInvalidEnum, s),
			}
		}
	}
	return nil
}

// declared reports whether this build's contract names s, zero included.
func declared(s controlv1.Sensitivity) bool {
	return s.Descriptor().Values().ByNumber(s.Number()) != nil
}
