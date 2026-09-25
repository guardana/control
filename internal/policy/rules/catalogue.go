package rules

import (
	"fmt"
	"slices"

	"google.golang.org/protobuf/reflect/protoreflect"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// reasonsFor lists the reason codes a rule of an effect may name, its default
// first. A code the kernel emits about its own checks, TENANT_MISMATCH,
// POLICY_STALE or a delegation code, is on no list: a policy that could name
// one could dress its own denial as the kernel's. The codes are literals
// because nothing that decides imports the registry; a test holds each to it.
func reasonsFor(effect controlv1.Verdict) []string {
	switch effect {
	case controlv1.Verdict_VERDICT_ALLOW:
		return []string{"RULE_ALLOW"}
	case controlv1.Verdict_VERDICT_DENY:
		return []string{"RULE_DENY", "ENVIRONMENT_BOUNDARY", "OUT_OF_SCOPE_ACTION", "TOXIC_FLOW_SENSITIVE_TO_EXTERNAL"}
	case controlv1.Verdict_VERDICT_REQUIRE_APPROVAL:
		return []string{"APPROVAL_REQUIRED"}
	case controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS:
		return []string{"OBLIGATIONS_ATTACHED"}
	}
	return nil
}

// obligationTypes is the obligation catalogue. The enforcement point applies
// some of these types; a receiver that cannot apply a non-advisory one denies
// (ADR-0011).
func obligationTypes() []string {
	return []string{
		"redact_fields", "read_only", "restrict_resources", "require_idempotency_key",
		"cap_amount", "cap_rate", "require_sandbox", "second_approver", "emit_alert",
		"shorten_timeout", "deny_external_sink",
	}
}

// KnownObligation reports whether name is an obligation type of the
// catalogue, spelled exactly. It is the catalogue's one exported reading, for
// a receiver that must refuse an obligation type it could never be handed.
func KnownObligation(name string) bool {
	return slices.Contains(obligationTypes(), name)
}

// ruleEffect reads a rule's effect. The four are named here rather than
// looked up, so a verdict the contract adds later is no rule effect until
// someone decides it is one.
func ruleEffect(v any, a at) (controlv1.Verdict, error) {
	s, err := text(v, a)
	if err != nil {
		return controlv1.Verdict_VERDICT_UNSPECIFIED, err
	}
	switch s {
	case "ALLOW":
		return controlv1.Verdict_VERDICT_ALLOW, nil
	case "DENY":
		return controlv1.Verdict_VERDICT_DENY, nil
	case "REQUIRE_APPROVAL":
		return controlv1.Verdict_VERDICT_REQUIRE_APPROVAL, nil
	case "ALLOW_WITH_OBLIGATIONS":
		return controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS, nil
	}
	return controlv1.Verdict_VERDICT_UNSPECIFIED,
		a.refuse(fmt.Errorf("%w: want ALLOW, DENY, REQUIRE_APPROVAL or ALLOW_WITH_OBLIGATIONS", ErrEnum))
}

// reason reads a rule's reason against its effect. The effect's own default
// reads as "", written out or not, so the model has one spelling of it.
func reason(v any, a at, effect controlv1.Verdict) (string, error) {
	s, err := text(v, a)
	if err != nil {
		return "", err
	}
	allowed := reasonsFor(effect)
	if !slices.Contains(allowed, s) {
		return "", a.refuse(ErrReason)
	}
	if s == allowed[0] {
		return "", nil
	}
	return s, nil
}

func obligationType(v any, a at) (string, error) {
	s, err := text(v, a)
	if err != nil {
		return "", err
	}
	if !slices.Contains(obligationTypes(), s) {
		return "", a.refuse(ErrObligationType)
	}
	return s, nil
}

// effectClass reads an effect class the way contract.ParseEffect spells it.
func effectClass(v any, a at) (controlv1.EffectClass, error) {
	s, err := text(v, a)
	if err != nil {
		return controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED, err
	}
	class, err := contract.ParseEffect(s)
	if err != nil {
		return controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED,
			a.refuse(fmt.Errorf("%w: want an effect class without its prefix", ErrEnum))
	}
	return class, nil
}

// trustZone reads a trust zone by its name without the prefix. The lookup goes
// through the descriptor, as ParseEffect's does, so a zone a later minor adds
// parses once the generated code carries it.
func trustZone(v any, a at) (controlv1.TrustZone, error) {
	s, err := text(v, a)
	if err != nil {
		return controlv1.TrustZone_TRUST_ZONE_UNSPECIFIED, err
	}
	n, ok := enumNumber(controlv1.TrustZone(0).Descriptor(), "TRUST_ZONE_", s)
	if !ok || n == 0 {
		return controlv1.TrustZone_TRUST_ZONE_UNSPECIFIED,
			a.refuse(fmt.Errorf("%w: want a trust zone without its prefix", ErrEnum))
	}
	return controlv1.TrustZone(n), nil
}

// floor reads a sensitivity floor. UNSPECIFIED is a declared name and the
// lookup finds it; ValidSensitivityFloor then refuses it, as it refuses every
// floor nothing can be compared against, because a floor nobody set is not a
// floor of zero.
func floor(v any, a at) (controlv1.Sensitivity, error) {
	s, err := text(v, a)
	if err != nil {
		return controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED, err
	}
	n, ok := enumNumber(controlv1.Sensitivity(0).Descriptor(), "SENSITIVITY_", s)
	if !ok {
		return controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED,
			a.refuse(fmt.Errorf("%w: want a sensitivity without its prefix", ErrEnum))
	}
	if f := controlv1.Sensitivity(n); contract.ValidSensitivityFloor(f) {
		return f, nil
	}
	return controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED, a.refuse(ErrFloor)
}

func enumNumber(d protoreflect.EnumDescriptor, prefix, name string) (protoreflect.EnumNumber, bool) {
	value := d.Values().ByName(protoreflect.Name(prefix + name))
	if value == nil {
		return 0, false
	}
	return value.Number(), true
}
