package core

import (
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// undecided is what the fail-closed table reads of an INDETERMINATE decision.
type undecided struct {
	effect controlv1.EffectClass
	// input is a cause in the request: a refusal, an argument that cannot be
	// digested, a hash that does not match, an undetermined rule or a
	// one-sided tenant.
	input bool
	// availability is a cause in the policy's availability: no snapshot, or
	// one past its budget.
	availability bool
	failOpenRead bool
	// snapshot is whether one was handed in, read from the pointer and never
	// from a reason code: a program nobody compiled answers POLICY_UNAVAILABLE
	// from a snapshot that exists, and the table must not open a read for it.
	snapshot    bool
	determinate controlv1.Verdict
}

// actionFor maps a verdict to what is enforced (ADR-0012): the four
// determinate verdicts have fixed actions, and anything else, INDETERMINATE
// and every value that is no verdict, goes to the fail-closed table. The
// second result is whether the table let a read run under FailOpenRead, which
// the decision then records.
func actionFor(verdict controlv1.Verdict, in undecided) (EnforcementAction, bool) {
	switch verdict {
	case controlv1.Verdict_VERDICT_DENY:
		return Block, false
	case controlv1.Verdict_VERDICT_REQUIRE_APPROVAL:
		return AwaitApproval, false
	case controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS:
		return ExecuteWithObligations, false
	case controlv1.Verdict_VERDICT_ALLOW:
		return Execute, false
	}
	return failClosed(in)
}

// failClosed is the fail-closed table of ADR-0012 for an INDETERMINATE
// verdict. Every row blocks but the last: a READ whose only causes are in the
// policy's availability, under FailOpenRead, with no snapshot or a determinate
// ALLOW. The first row stands on its own although IsMaterial covers it: an
// effect nobody declared, or this build cannot name, blocks under every cause
// and never reaches FailOpenRead. A decision with no cause at all is not
// INDETERMINATE for a reason this table knows, and blocks.
func failClosed(in undecided) (EnforcementAction, bool) {
	switch {
	case in.effect == controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED || !declaredEffect(in.effect):
		return Block, false
	case contract.IsMaterial(in.effect):
		return Block, false
	case in.input || !in.availability:
		return Block, false
	case !in.failOpenRead:
		return Block, false
	case in.snapshot && in.determinate != controlv1.Verdict_VERDICT_ALLOW:
		return Block, false
	}
	return Execute, true
}

func declaredEffect(e controlv1.EffectClass) bool {
	return e.Descriptor().Values().ByNumber(e.Number()) != nil
}

// FailClosedRow is one cell of the fail-closed table, as the rule answers it.
type FailClosedRow struct {
	Effect controlv1.EffectClass
	// Declared is false for the one number no build declares, which the
	// table blocks like an unspecified effect.
	Declared     bool
	Input        bool
	Availability bool
	FailOpenRead bool
	Snapshot     bool
	Determinate  controlv1.Verdict
	Action       EnforcementAction
	// OpenedRead is whether the table let a read run under FailOpenRead.
	OpenedRead bool
}

// FailClosedRows evaluates the fail-closed rule over every combination of
// its inputs: every declared effect class and one number no build declares,
// each cause on and off, the setting on and off, a snapshot present and
// absent, and every declared verdict as the determinate one. It is a listing
// for the reference page, derived from the rule itself so it cannot say
// something the rule does not; nothing that decides reads it.
func FailClosedRows() []FailClosedRow {
	effects := controlv1.EffectClass(0).Descriptor().Values()
	verdicts := controlv1.Verdict(0).Descriptor().Values()
	undeclared := controlv1.EffectClass(0)
	for i := 0; i < effects.Len(); i++ {
		if n := controlv1.EffectClass(effects.Get(i).Number()); n >= undeclared {
			undeclared = n + 1
		}
	}
	var rows []FailClosedRow
	for e := 0; e <= effects.Len(); e++ {
		effect, declared := undeclared, false
		if e < effects.Len() {
			effect, declared = controlv1.EffectClass(effects.Get(e).Number()), true
		}
		for _, input := range []bool{false, true} {
			for _, availability := range []bool{false, true} {
				for _, failOpen := range []bool{false, true} {
					for _, snapshot := range []bool{false, true} {
						for v := 0; v < verdicts.Len(); v++ {
							in := undecided{effect: effect, input: input, availability: availability,
								failOpenRead: failOpen, snapshot: snapshot, determinate: controlv1.Verdict(verdicts.Get(v).Number())}
							action, opened := failClosed(in)
							rows = append(rows, FailClosedRow{Effect: effect, Declared: declared, Input: input,
								Availability: availability, FailOpenRead: failOpen, Snapshot: snapshot,
								Determinate: in.determinate, Action: action, OpenedRead: opened})
						}
					}
				}
			}
		}
	}
	return rows
}
