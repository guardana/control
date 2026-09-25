package rules

import (
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// Every enum value in a document is its declared name without the prefix, and
// UNSPECIFIED is never a spelling. The tables write each expected value out.
// The count at the end of each test fails when the contract declares a value
// the table does not hold, so a new value gets a row rather than a pass.

func TestRuleEffectSpellings(t *testing.T) {
	t.Parallel()
	for effect, name := range effectNames {
		if got := expectAccepted(t, ruleOfEffect(name, "")).Rules[0].Effect; got != effect {
			t.Errorf("effect %s read as %v, want %v", name, got, effect)
		}
	}
	for _, name := range []string{"INDETERMINATE", "UNSPECIFIED", "VERDICT_ALLOW", "VERDICT_DENY", "allow", "Deny", ""} {
		expectRefusal(t, ruleOfEffect(name, ""), refusal{err: ErrEnum, field: "rules[0].effect", rule: "r"})
	}
}

func TestEffectClassSpellings(t *testing.T) {
	t.Parallel()
	want := map[string]controlv1.EffectClass{
		"READ":               controlv1.EffectClass_EFFECT_CLASS_READ,
		"WRITE":              controlv1.EffectClass_EFFECT_CLASS_WRITE,
		"DELETE":             controlv1.EffectClass_EFFECT_CLASS_DELETE,
		"EXECUTE":            controlv1.EffectClass_EFFECT_CLASS_EXECUTE,
		"COMMUNICATE":        controlv1.EffectClass_EFFECT_CLASS_COMMUNICATE,
		"TRANSACT":           controlv1.EffectClass_EFFECT_CLASS_TRANSACT,
		"IDENTITY_OR_ACCESS": controlv1.EffectClass_EFFECT_CLASS_IDENTITY_OR_ACCESS,
		"CONFIGURE":          controlv1.EffectClass_EFFECT_CLASS_CONFIGURE,
		"SPAWN_OR_DELEGATE":  controlv1.EffectClass_EFFECT_CLASS_SPAWN_OR_DELEGATE,
	}
	for name, class := range want {
		action := expectAccepted(t, docWithWhen(`{"action":{"effect":["`+name+`"]}}`)).Rules[0].When.Action
		if action == nil || len(action.Effect) != 1 || action.Effect[0] != class {
			t.Errorf("effect class %s read as %v, want %v", name, action, class)
		}
	}
	for _, name := range []string{"UNSPECIFIED", "EFFECT_CLASS_READ", "read", "Read", "READ ", ""} {
		expectRefusal(t, docWithWhen(`{"action":{"effect":["`+name+`"]}}`),
			refusal{err: ErrEnum, field: "rules[0].when.action.effect[0]", rule: "r"})
	}
	if n := controlv1.EffectClass(0).Descriptor().Values().Len() - 1; n != len(want) {
		t.Errorf("the contract declares %d effect classes besides UNSPECIFIED, the table %d", n, len(want))
	}
}

func TestTrustZoneSpellings(t *testing.T) {
	t.Parallel()
	want := map[string]controlv1.TrustZone{
		"TRUSTED_INTERNAL":   controlv1.TrustZone_TRUST_ZONE_TRUSTED_INTERNAL,
		"PARTNER":            controlv1.TrustZone_TRUST_ZONE_PARTNER,
		"UNTRUSTED_EXTERNAL": controlv1.TrustZone_TRUST_ZONE_UNTRUSTED_EXTERNAL,
		"USER_CONTROLLED":    controlv1.TrustZone_TRUST_ZONE_USER_CONTROLLED,
		"MODEL_GENERATED":    controlv1.TrustZone_TRUST_ZONE_MODEL_GENERATED,
	}
	for name, zone := range want {
		dest := expectAccepted(t, docWithWhen(`{"destination":{"trustZone":["`+name+`"]}}`)).Rules[0].When.Destination
		if dest == nil || len(dest.TrustZone) != 1 || dest.TrustZone[0] != zone {
			t.Errorf("trust zone %s read as %v, want %v", name, dest, zone)
		}
	}
	for _, name := range []string{"UNSPECIFIED", "TRUST_ZONE_PARTNER", "partner", "PARTNER ", ""} {
		expectRefusal(t, docWithWhen(`{"destination":{"trustZone":["`+name+`"]}}`),
			refusal{err: ErrEnum, field: "rules[0].when.destination.trustZone[0]", rule: "r"})
	}
	if n := controlv1.TrustZone(0).Descriptor().Values().Len() - 1; n != len(want) {
		t.Errorf("the contract declares %d trust zones besides UNSPECIFIED, the table %d", n, len(want))
	}
}

// TestFloorSpellings covers both floors. UNSPECIFIED is a declared name, so
// the lookup finds it, and ValidSensitivityFloor is what refuses it: a floor
// nobody set is no floor at all, and nothing compares at or above it.
func TestFloorSpellings(t *testing.T) {
	t.Parallel()
	want := map[string]controlv1.Sensitivity{
		"PUBLIC":       controlv1.Sensitivity_SENSITIVITY_PUBLIC,
		"INTERNAL":     controlv1.Sensitivity_SENSITIVITY_INTERNAL,
		"CONFIDENTIAL": controlv1.Sensitivity_SENSITIVITY_CONFIDENTIAL,
		"RESTRICTED":   controlv1.Sensitivity_SENSITIVITY_RESTRICTED,
		"SECRET":       controlv1.Sensitivity_SENSITIVITY_SECRET,
	}
	read := map[string]func(*Document) controlv1.Sensitivity{
		"data": func(d *Document) controlv1.Sensitivity {
			if w := d.Rules[0].When.Data; w != nil {
				return w.SensitivityAtLeast
			}
			return -1
		},
		"flow": func(d *Document) controlv1.Sensitivity {
			if w := d.Rules[0].When.Flow; w != nil {
				return w.ToxicAtLeast
			}
			return -1
		},
	}
	key := map[string]string{"data": "sensitivityAtLeast", "flow": "toxicAtLeast"}
	for group, floorOf := range read {
		doc := func(name string) string {
			return docWithWhen(`{"` + group + `":{"` + key[group] + `":"` + name + `"}}`)
		}
		field := "rules[0].when." + group + "." + key[group]
		for name, floor := range want {
			if got := floorOf(expectAccepted(t, doc(name))); got != floor {
				t.Errorf("%s floor %s read as %v, want %v", group, name, got, floor)
			}
		}
		expectRefusal(t, doc("UNSPECIFIED"), refusal{err: ErrFloor, field: field, rule: "r"})
		for _, name := range []string{"SENSITIVITY_SECRET", "secret", "SECRET ", ""} {
			expectRefusal(t, doc(name), refusal{err: ErrEnum, field: field, rule: "r"})
		}
		expectRefusal(t, docWithWhen(`{"`+group+`":{"`+key[group]+`":["SECRET"]}}`),
			refusal{err: ErrWrongType, field: field, rule: "r"})
	}
	if n := controlv1.Sensitivity(0).Descriptor().Values().Len() - 1; n != len(want) {
		t.Errorf("the contract declares %d sensitivities besides UNSPECIFIED, the table %d", n, len(want))
	}
}
