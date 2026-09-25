package contract_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// Short spellings for the tables below: the generated names are long enough
// that a table of them cannot be read across a row. Each is tied back to the
// name the contract declares by TestSensitivityScaleMatchesTheContract and
// TestIsUntrustedCoversEveryDeclaredZone, so a mis-bound alias fails there
// rather than passing quietly through every table that uses it.
const (
	unset        = controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED
	public       = controlv1.Sensitivity_SENSITIVITY_PUBLIC
	internal     = controlv1.Sensitivity_SENSITIVITY_INTERNAL
	confidential = controlv1.Sensitivity_SENSITIVITY_CONFIDENTIAL
	restricted   = controlv1.Sensitivity_SENSITIVITY_RESTRICTED
	secret       = controlv1.Sensitivity_SENSITIVITY_SECRET

	zoneUnset    = controlv1.TrustZone_TRUST_ZONE_UNSPECIFIED
	zoneInternal = controlv1.TrustZone_TRUST_ZONE_TRUSTED_INTERNAL
	zonePartner  = controlv1.TrustZone_TRUST_ZONE_PARTNER
	zoneExternal = controlv1.TrustZone_TRUST_ZONE_UNTRUSTED_EXTERNAL
	zoneUser     = controlv1.TrustZone_TRUST_ZONE_USER_CONTROLLED
	zoneModel    = controlv1.TrustZone_TRUST_ZONE_MODEL_GENERATED
)

type sensitivityRank struct {
	name  string
	value controlv1.Sensitivity
}

// sensitivityScale is the ordered scale the contract states, least sensitive
// first, written out rather than derived from the enum numbers: the
// implementation compares those numbers, so an expectation read off them would
// assert only that the code agrees with itself.
//
// SENSITIVITY_UNSPECIFIED is deliberately not on it. It is not the bottom of
// the scale, it is the absence of a rank.
func sensitivityScale() []sensitivityRank {
	return []sensitivityRank{
		{"SENSITIVITY_PUBLIC", public},
		{"SENSITIVITY_INTERNAL", internal},
		{"SENSITIVITY_CONFIDENTIAL", confidential},
		{"SENSITIVITY_RESTRICTED", restricted},
		{"SENSITIVITY_SECRET", secret},
	}
}

type zoneRule struct {
	name      string
	value     controlv1.TrustZone
	untrusted bool
}

// zonesUnderTest is the rule as the contract states it, one row per declared
// zone. A copy on purpose, for the same reason as sensitivityScale.
func zonesUnderTest() []zoneRule {
	return []zoneRule{
		// An envelope that does not say must not read as the safe case.
		{"TRUST_ZONE_UNSPECIFIED", zoneUnset, true},
		{"TRUST_ZONE_TRUSTED_INTERNAL", zoneInternal, false},
		{"TRUST_ZONE_PARTNER", zonePartner, false},
		{"TRUST_ZONE_UNTRUSTED_EXTERNAL", zoneExternal, true},
		{"TRUST_ZONE_USER_CONTROLLED", zoneUser, true},
		{"TRUST_ZONE_MODEL_GENERATED", zoneModel, true},
	}
}

// TestSensitivityScaleMatchesTheContract checks three things the comparison in
// SensitivityAtLeast rests on: the short spellings above name the values they
// claim to, every declared value has a place (a value added in a later minor
// arrives here with none, and this says so), and the scale ascends in declared
// number, because the implementation compares numbers and the contract's claim
// that they are ordered least sensitive first is what makes that legal.
//
// The third one binds every future minor and is stated nowhere else: a value
// added later has a higher number, so it has to be more sensitive than every
// value already on the scale. A SENSITIVITY_PUBLIC_ARCHIVE = 6 meaning "less
// sensitive than PUBLIC" is a legal proto addition and would be read here as
// above SECRET. It fails this test, which is the only thing saying so.
func TestSensitivityScaleMatchesTheContract(t *testing.T) {
	declared := declaredValues(t, controlv1.Sensitivity(0).Descriptor().Values())
	byName := make(map[string]protoreflect.EnumNumber, len(declared))
	for _, value := range declared {
		byName[string(value.Name())] = value.Number()
	}

	scale := sensitivityScale()
	placed := map[string]bool{"SENSITIVITY_UNSPECIFIED": true}
	previous := protoreflect.EnumNumber(-1)
	for _, rank := range scale {
		number, ok := byName[rank.name]
		if !ok {
			t.Errorf("this scale names %s, which the contract does not declare", rank.name)
			continue
		}
		if protoreflect.EnumNumber(rank.value) != number {
			t.Errorf("%s is %d in the contract and %d here", rank.name, number, rank.value)
		}
		if number <= previous {
			t.Errorf("%s is %d, at or below the value before it (%d): the scale no longer ascends, and a numeric comparison no longer means what it says",
				rank.name, number, previous)
		}
		previous = number
		placed[rank.name] = true
	}
	for _, value := range declared {
		if !placed[string(value.Name())] {
			t.Errorf("%s is declared in the contract and this scale gives it no place: put it where it belongs on the scale",
				value.Name())
		}
	}
	if unspecified := byName["SENSITIVITY_UNSPECIFIED"]; unspecified != 0 {
		t.Errorf("SENSITIVITY_UNSPECIFIED is %d, not 0; absence is no longer the zero value", unspecified)
	}
}

// TestSensitivityAtLeastAbsence spells out the rules the contract states, one
// row each, so the rule is readable here and does not depend on the matrix
// below being built correctly.
func TestSensitivityAtLeastAbsence(t *testing.T) {
	for _, c := range []struct {
		name     string
		s, floor controlv1.Sensitivity
		want     bool
		because  string
	}{
		{name: "nothing said, lowest floor", s: unset, floor: public, want: false,
			because: "a producer that did not say is not evidence that data is public"},
		{name: "nothing said, no floor", s: unset, floor: unset, want: false,
			because: "a comparison nobody parameterised does not pass"},
		{name: "top of the scale, no floor", s: secret, floor: unset, want: false,
			because: "an unset floor is not a floor of zero"},
		{name: "exactly at the floor", s: internal, floor: internal, want: true,
			because: "a floor is met by standing on it"},
		{name: "above the floor", s: secret, floor: public, want: true,
			because: "the scale is ordered least sensitive first"},
		{name: "below the floor", s: public, floor: secret, want: false,
			because: "the scale is ordered least sensitive first"},
	} {
		if got := contract.SensitivityAtLeast(c.s, c.floor); got != c.want {
			t.Errorf("%s: SensitivityAtLeast(%s, %s) = %t, want %t (%s)", c.name, c.s, c.floor, got, c.want, c.because)
		}
	}
}

// TestSensitivityAtLeastMatrix covers every ordered pair, UNSPECIFIED in both
// positions included. The expectation comes from the position on the
// hand-written scale, not from the enum numbers the implementation compares.
func TestSensitivityAtLeastMatrix(t *testing.T) {
	// Index 0 is absence and carries no rank; 1 upwards are the scale.
	all := []controlv1.Sensitivity{unset}
	for _, rank := range sensitivityScale() {
		all = append(all, rank.value)
	}

	for si, s := range all {
		for fi, floor := range all {
			want := si > 0 && fi > 0 && si >= fi
			if got := contract.SensitivityAtLeast(s, floor); got != want {
				t.Errorf("SensitivityAtLeast(%s, %s) = %t, want %t", s, floor, got, want)
			}
		}
	}
}

// TestSensitivityAtLeastUndeclaredNumber covers the number a later minor sends
// and this build cannot name. Validate refuses such an envelope, so this is the
// second line: on the scale's own terms an unknown value extends the top, which
// satisfies the floors below it, and a floor nobody here can name is satisfied
// by nothing this build knows.
func TestSensitivityAtLeastUndeclaredNumber(t *testing.T) {
	future := controlv1.Sensitivity(42)

	for _, rank := range sensitivityScale() {
		if !contract.SensitivityAtLeast(future, rank.value) {
			t.Errorf("SensitivityAtLeast(Sensitivity(42), %s) = false; a value above the scale meets the floors below it", rank.name)
		}
		if contract.SensitivityAtLeast(rank.value, future) {
			t.Errorf("SensitivityAtLeast(%s, Sensitivity(42)) = true; a floor this build cannot name is not met by a value below it", rank.name)
		}
	}
	if contract.SensitivityAtLeast(unset, future) {
		t.Error("SensitivityAtLeast(UNSPECIFIED, Sensitivity(42)) = true; absence satisfies no floor")
	}
	// Below the scale rather than above it, which is the case that makes the
	// rule about the left-hand side load-bearing. Everywhere else, absence
	// fails a floor by being the lowest number; against a floor an int32 field
	// can carry but the contract never declares, only the rule stops it.
	for _, floor := range []controlv1.Sensitivity{-1, -42} {
		if contract.SensitivityAtLeast(unset, floor) {
			t.Errorf("SensitivityAtLeast(UNSPECIFIED, Sensitivity(%d)) = true; a producer that said nothing satisfies no floor, including one below the scale",
				floor)
		}
	}
}

// TestIsUntrustedCoversEveryDeclaredZone checks the rule in both directions: a
// zone the contract declares and this table does not name fails, and a row
// naming a zone the contract does not declare fails too.
func TestIsUntrustedCoversEveryDeclaredZone(t *testing.T) {
	declared := declaredValues(t, controlv1.TrustZone(0).Descriptor().Values())
	byName := make(map[string]protoreflect.EnumNumber, len(declared))
	for _, value := range declared {
		byName[string(value.Name())] = value.Number()
	}

	covered := make(map[string]bool, len(byName))
	for _, rule := range zonesUnderTest() {
		number, ok := byName[rule.name]
		if !ok {
			t.Errorf("this table has a row for %s, which the contract does not declare", rule.name)
			continue
		}
		if protoreflect.EnumNumber(rule.value) != number {
			t.Errorf("%s is %d in the contract and %d here", rule.name, number, rule.value)
		}
		if got := contract.IsUntrusted(rule.value); got != rule.untrusted {
			t.Errorf("IsUntrusted(%s) = %t, want %t", rule.name, got, rule.untrusted)
		}
		covered[rule.name] = true
	}
	for _, value := range declared {
		if !covered[string(value.Name())] {
			t.Errorf("%s is declared in the contract and this table has no row for it: decide whether the system trusts it and add one",
				value.Name())
		}
	}
}

// TestIsUntrustedUndeclaredNumber covers the zone a later minor sends. A
// receiver that cannot name a zone has no ground on which to trust it.
func TestIsUntrustedUndeclaredNumber(t *testing.T) {
	for _, number := range []int32{6, 99, 2147483647, -1} {
		if !contract.IsUntrusted(controlv1.TrustZone(number)) {
			t.Errorf("IsUntrusted(TrustZone(%d)) = false; an undeclared zone is not one the system trusts", number)
		}
	}
}

// TestToxicFlow covers the negative cases first: a predicate that always
// returned true would pass a suite that only tested the flow it is named for.
//
// A row with refused set is a question the predicate has to decline. Answering
// false there is what ADR-0011 removed: an unknown part of what the call carries
// counted as below every floor, so a producer that said nothing got the answer a
// producer declaring PUBLIC gets.
func TestToxicFlow(t *testing.T) {
	influenced := contract.NewFlowState(true, secret)
	unknownRun := contract.NewFlowState(true, unset)

	for _, c := range []struct {
		name    string
		state   contract.FlowState
		env     *controlv1.ActionEnvelope
		floor   controlv1.Sensitivity
		want    bool
		refused error
	}{
		{name: "no untrusted influence", state: contract.NewFlowState(false, secret),
			env: flowEnv(zoneExternal), floor: public, want: false},
		// Influence is known to be absent, so nothing unknown about the data can
		// make the flow toxic.
		{name: "no untrusted influence and nothing known about the data", state: contract.NewFlowState(false, unset),
			env: flowEnv(zoneExternal), floor: public, want: false},
		{name: "every part known and below the floor", state: contract.NewFlowState(true, internal),
			env: withLabels(flowEnv(zoneExternal), public), floor: confidential, want: false},
		{name: "exactly at the floor", state: contract.NewFlowState(true, confidential),
			env: flowEnv(zoneExternal), floor: confidential, want: true},
		{name: "destination the system trusts", state: influenced,
			env: flowEnv(zoneInternal), floor: public, want: false},
		{name: "a trusted destination and nothing known about the data", state: unknownRun,
			env: flowEnv(zoneInternal), floor: public, want: false},
		// A partner is trusted here. It is the row to change first if that
		// turns out to be the wrong opinion.
		{name: "partner destination", state: influenced,
			env: flowEnv(zonePartner), floor: public, want: false},
		{name: "no destination at all", state: influenced,
			env: noDestination(), floor: public, want: true},
		{name: "destination present, zone unspecified", state: influenced,
			env: flowEnv(zoneUnset), floor: public, want: true},
		{name: "no envelope at all", state: influenced,
			env: nil, floor: public, want: true},
		{name: "user-controlled destination", state: influenced,
			env: flowEnv(zoneUser), floor: restricted, want: true},
		{name: "model-generated destination", state: influenced,
			env: flowEnv(zoneModel), floor: restricted, want: true},
		{name: "the envelope carries the sensitivity, not the run", state: unknownRun,
			env: withLabels(flowEnv(zoneExternal), secret), floor: secret, want: true},
		{name: "the highest label counts, not the last", state: unknownRun,
			env: withLabels(flowEnv(zoneExternal), secret, public), floor: secret, want: true},
		{name: "asserted secrets with no labels at all", state: unknownRun,
			env: withSecrets(flowEnv(zoneExternal)), floor: secret, want: true},
		{name: "no assertion of secrets does not lower a label", state: unknownRun,
			env: withoutSecretAssertion(withLabels(flowEnv(zoneExternal), secret)), floor: secret, want: true},
		{name: "the positive case", state: influenced,
			env: flowEnv(zoneExternal), floor: internal, want: true},
		// max() of the run and the envelope, and contains_secrets beside a
		// label, each need a row that fails when it is broken.
		{name: "the run read more than the envelope declares", state: contract.NewFlowState(true, secret),
			env: withLabels(flowEnv(zoneExternal), public), floor: secret, want: true},
		{name: "asserted secrets beside a lower label", state: unknownRun,
			env: withSecrets(withLabels(flowEnv(zoneExternal), public)), floor: secret, want: true},

		// Unknown: nothing known reaches the floor, and some part is unknown.
		{name: "nothing read and nothing declared", state: unknownRun,
			env: flowEnv(zoneExternal), floor: public, refused: contract.ErrMissingField},
		{name: "an unknown run beside a declared PUBLIC", state: unknownRun,
			env: withLabels(flowEnv(zoneExternal), public), floor: internal, refused: contract.ErrMissingField},
		{name: "a known run below the floor and no label", state: contract.NewFlowState(true, internal),
			env: flowEnv(zoneExternal), floor: confidential, refused: contract.ErrMissingField},
		{name: "no envelope and nothing read", state: unknownRun,
			env: nil, floor: public, refused: contract.ErrMissingField},
		{name: "a label that says nothing", state: unknownRun,
			env: withLabels(flowEnv(zoneExternal), unset), floor: internal, refused: contract.ErrMissingField},
		{name: "a label that says nothing beside one below the floor", state: contract.NewFlowState(true, internal),
			env: withLabels(flowEnv(zoneExternal), public, unset), floor: confidential, refused: contract.ErrMissingField},
		// A producer that fills sources and not sensitivities says where the
		// data came from and nothing about how sensitive it is.
		{name: "data with a source and no label", state: contract.NewFlowState(true, internal),
			env: withSources(flowEnv(zoneExternal), "orders-db"), floor: confidential, refused: contract.ErrMissingField},

		// A number the scale does not hold, read by its number, lands below
		// PUBLIC or above SECRET. It is refused before anything is compared,
		// as an unusable floor is.
		{name: "a run reading below the scale", state: contract.NewFlowState(true, -1),
			env: flowEnv(zoneExternal), floor: public, refused: contract.ErrInvalidEnum},
		{name: "a run reading above the scale", state: contract.NewFlowState(true, 42),
			env: flowEnv(zoneExternal), floor: secret, refused: contract.ErrInvalidEnum},
		{name: "a label below the scale", state: unknownRun,
			env: withLabels(flowEnv(zoneExternal), -7), floor: public, refused: contract.ErrInvalidEnum},
		{name: "a label above the scale", state: unknownRun,
			env: withLabels(flowEnv(zoneExternal), 6), floor: secret, refused: contract.ErrInvalidEnum},
		{name: "an undeclared label beside one that reaches the floor", state: influenced,
			env: withLabels(flowEnv(zoneExternal), secret, -1), floor: public, refused: contract.ErrInvalidEnum},
		{name: "an undeclared reading with no untrusted influence", state: contract.NewFlowState(false, -1),
			env: flowEnv(zoneInternal), floor: public, refused: contract.ErrInvalidEnum},
	} {
		got, err := contract.ToxicFlow(c.state, c.env, c.floor)
		switch {
		case c.refused != nil:
			if !errors.Is(err, c.refused) {
				t.Errorf("%s: ToxicFlow = %t, %v; want a refusal carrying %v", c.name, got, err, c.refused)
			}
			if got {
				t.Errorf("%s: ToxicFlow refused and answered true", c.name)
			}
		case err != nil:
			t.Errorf("%s: ToxicFlow refused a question it can answer: %v", c.name, err)
		case got != c.want:
			t.Errorf("%s: ToxicFlow(%+v, ..., %s) = %t, want %t", c.name, c.state, c.floor, got, c.want)
		}
	}
}

// TestValidSensitivityFloor reads its answer off the contract rather than off a
// table here: every declared rank is usable as a floor, the zero value is not,
// and a number no version of this contract declares is not.
func TestValidSensitivityFloor(t *testing.T) {
	for _, value := range declaredValues(t, controlv1.Sensitivity(0).Descriptor().Values()) {
		floor := controlv1.Sensitivity(value.Number())
		want := value.Number() != 0
		if got := contract.ValidSensitivityFloor(floor); got != want {
			t.Errorf("ValidSensitivityFloor(%s) = %t, want %t", value.Name(), got, want)
		}
	}
	// 6 is the number a bundle written against the next minor would carry; the
	// negatives are what an int32 field can hold and the scale never gives a
	// meaning to.
	for _, number := range []int32{6, 42, 2147483647, -1, -42} {
		if contract.ValidSensitivityFloor(controlv1.Sensitivity(number)) {
			t.Errorf("ValidSensitivityFloor(Sensitivity(%d)) = true; the contract declares no such rank", number)
		}
	}
}

// TestToxicFlowRefusesAFloorItCannotEvaluate pins a decision, not a detail.
// false out of this predicate means "not a toxic flow", so a floor nobody set,
// or one from a bundle written against a later minor, would answer no on every
// call and leave an allow in the trail with nothing recording why. Refusing is
// what gives the caller something to turn into an indeterminate verdict.
func TestToxicFlowRefusesAFloorItCannotEvaluate(t *testing.T) {
	hot := contract.NewFlowState(true, secret)

	for _, floor := range []controlv1.Sensitivity{unset, 6, 42, -1} {
		toxic, err := contract.ToxicFlow(hot, flowEnv(zoneExternal), floor)
		if !errors.Is(err, contract.ErrInvalidEnum) {
			t.Errorf("ToxicFlow(..., Sensitivity(%d)) = %t, %v; want a refusal carrying ErrInvalidEnum", floor, toxic, err)
		}
		if toxic {
			t.Errorf("ToxicFlow(..., Sensitivity(%d)) refused and answered true", floor)
		}
	}
	// The same flow at a floor this build can name is toxic, so the rows above
	// are refused for the floor and not because there was nothing to report.
	if toxic, err := contract.ToxicFlow(hot, flowEnv(zoneExternal), restricted); err != nil || !toxic {
		t.Errorf("ToxicFlow at a declared floor = %t, %v; want true and no error", toxic, err)
	}
}

// TestToxicFlowRefusesAStateNobodyComputed covers the Go zero value, which the
// wire rules say nothing about and which is the permissive answer in every
// field. FlowState{} is what a caller holds after a tracker returned an error
// it dropped, and a predicate that answered "no untrusted influence" to that
// would be reporting a fact nobody established.
func TestToxicFlowRefusesAStateNobodyComputed(t *testing.T) {
	for _, c := range []struct {
		name  string
		state contract.FlowState
	}{
		{"the zero value", contract.FlowState{}},
		// Filling the exported fields by hand is refused too: only the
		// constructor marks a state as one somebody computed.
		{"fields set by hand", contract.FlowState{UntrustedInfluence: true, MaxSensitivityRead: secret}},
	} {
		toxic, err := contract.ToxicFlow(c.state, flowEnv(zoneExternal), restricted)
		if !errors.Is(err, contract.ErrMissingField) {
			t.Errorf("%s: ToxicFlow = %t, %v; want a refusal carrying ErrMissingField", c.name, toxic, err)
		}
		if toxic {
			t.Errorf("%s: ToxicFlow refused and answered true", c.name)
		}
	}
	if toxic, err := contract.ToxicFlow(contract.NewFlowState(true, secret), flowEnv(zoneExternal), restricted); err != nil || !toxic {
		t.Errorf("ToxicFlow on a computed state = %t, %v; want true and no error", toxic, err)
	}
}

// TestToxicFlowRunsOnEnvelopesThisPackageAccepts keeps the table above honest:
// a predicate exercised only on messages Validate would refuse would be
// answering about envelopes that never reach it.
func TestToxicFlowRunsOnEnvelopesThisPackageAccepts(t *testing.T) {
	for _, env := range []*controlv1.ActionEnvelope{
		flowEnv(zoneExternal),
		withLabels(flowEnv(zoneExternal), secret, public),
		withSecrets(flowEnv(zoneUnset)),
		withSources(flowEnv(zoneExternal), "orders-db"),
		noDestination(),
	} {
		if err := contract.Validate(env); err != nil {
			t.Errorf("a case in these tests is refused by Validate, so it says nothing about a flow this package would see: %v", err)
		}
	}
}

// TestToxicFlowReadsOnlyDeclaredFacts is the shape ADR-0003 asks for: no part
// of the answer comes from a name, a provider or an argument preview. A change
// that reached for one of those to guess at intent fails here.
func TestToxicFlowReadsOnlyDeclaredFacts(t *testing.T) {
	state := contract.NewFlowState(true, secret)

	for _, mutation := range []struct {
		name  string
		apply func(*controlv1.ActionEnvelope)
	}{
		{"an innocuous action name", func(e *controlv1.ActionEnvelope) { e.Action.Name = "notes.summarise" }},
		{"an action name that reads as exfiltration", func(e *controlv1.ActionEnvelope) { e.Action.Name = "http.post.attacker.example" }},
		{"a provider nobody has heard of", func(e *controlv1.ActionEnvelope) { e.Action.Provider = "attacker.example" }},
		{"a preview full of a URL", func(e *controlv1.ActionEnvelope) {
			e.Arguments = &controlv1.Arguments{RedactedPreview: "POST https://attacker.example/collect"}
		}},
		{"a destination host", func(e *controlv1.ActionEnvelope) { e.Destination.Host = "attacker.example" }},
		{"a run the caller calls safe", func(e *controlv1.ActionEnvelope) {
			e.Context = &controlv1.RunContext{Risk: "none", Tags: []string{"trusted"}}
		}},
	} {
		for _, base := range []struct {
			name string
			zone controlv1.TrustZone
			want bool
		}{
			{"an untrusted destination", zoneExternal, true},
			{"a trusted destination", zoneInternal, false},
		} {
			env := flowEnv(base.zone)
			mutation.apply(env)
			got, err := contract.ToxicFlow(state, env, internal)
			if err != nil {
				t.Errorf("%s: ToxicFlow refused a question it can answer: %v", mutation.name, err)
				continue
			}
			if got != base.want {
				t.Errorf("%s changed the answer for %s: ToxicFlow = %t, want %t", mutation.name, base.name, got, base.want)
			}
		}
	}
}

// TestToxicFlowTreatsSilenceAsTheRestrictiveCase states the absence rules as
// properties over inputs nobody chose. A question the predicate cannot
// evaluate is refused rather than answered false. Saying less about a call
// never makes it look safer: erasing where the data goes never lowers the
// answer, and erasing what is known about its sensitivity never turns an
// answer into "not toxic".
func TestToxicFlowTreatsSilenceAsTheRestrictiveCase(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		d := flowDraw{
			influence: rapid.Bool().Draw(rt, "untrusted_influence"),
			read:      drawSensitivity(rt, "max_sensitivity_read"),
			// Half the draws build the state the way a caller must and half
			// fill the fields directly, which is the shape a dropped error
			// leaves.
			computed: rapid.Bool().Draw(rt, "computed"),
			floor:    drawSensitivity(rt, "floor"),
			zone:     rapid.SampledFrom(everyZone()).Draw(rt, "zone"),
			labels:   rapid.SliceOfN(rapid.SampledFrom(everySensitivity()), 0, 3).Draw(rt, "labels"),
			secrets:  rapid.Bool().Draw(rt, "contains_secrets"),
		}
		toxic, err := contract.ToxicFlow(d.state(), d.envelope(), d.floor)

		// Stated from the hand-written scale and zone table, not read back out
		// of the package, so this asserts the rule and not the code.
		switch {
		case !d.computed:
			assertFlowRefused(rt, toxic, err, contract.ErrMissingField, "a state nobody computed")
			return
		case !d.wellFormed():
			assertFlowRefused(rt, toxic, err, contract.ErrInvalidEnum, "a number the scale does not hold")
			return
		}
		got := flowRank(rt, toxic, err)
		if (!d.influence || trustedZone(d.zone)) && got != notToxic {
			rt.Fatalf("ToxicFlow = %t, %v with influence %t and zone %s; want false", toxic, err, d.influence, d.zone)
		}
		assertSilenceNeverLowersIt(rt, d, got)
	})
}

// assertSilenceNeverLowersIt re-asks the question about calls that say less.
// The first three only make the destination less trusted or the data more
// sensitive, so the answer may not drop at all. The last two forget what is
// known about sensitivity, which may turn "toxic" into "unknown" and may never
// turn anything into "not toxic".
func assertSilenceNeverLowersIt(rt *rapid.T, d flowDraw, got int) {
	for _, w := range []struct {
		name     string
		state    contract.FlowState
		env      *controlv1.ActionEnvelope
		monotone bool
	}{
		{"the destination removed", d.state(), d.without(func(e *controlv1.ActionEnvelope) { e.Destination = nil }), true},
		{"the zone left unset", d.state(), d.without(func(e *controlv1.ActionEnvelope) { e.Destination.TrustZone = zoneUnset }), true},
		{"secrets asserted as well", d.state(), withSecrets(d.envelope()), true},
		{"the labels removed", d.state(), d.without(func(e *controlv1.ActionEnvelope) { e.Data.Sensitivities = nil }), false},
		{"the run's reading forgotten", contract.NewFlowState(d.influence, unset), d.envelope(), false},
	} {
		toxic, err := contract.ToxicFlow(w.state, w.env, d.floor)
		weakened := flowRank(rt, toxic, err)
		if w.monotone && weakened < got {
			rt.Fatalf("with %s the answer drops from rank %d to %d (%t, %v)", w.name, got, weakened, toxic, err)
		}
		if got != notToxic && weakened == notToxic {
			rt.Fatalf("with %s a call that was not safe answers not toxic", w.name)
		}
	}
}

// Ranks of an answer, least restrictive first: a refusal holds the action
// indeterminate, which is between an allow and a deny.
const (
	notToxic = iota
	unknownSensitivity
	toxicFlow
)

// flowRank orders an answer. The only refusal a well-formed question may get is
// the unknown one.
func flowRank(rt *rapid.T, toxic bool, err error) int {
	switch {
	case err != nil && !errors.Is(err, contract.ErrMissingField):
		rt.Fatalf("a well-formed question was refused with %v", err)
	case err != nil && toxic:
		rt.Fatalf("ToxicFlow refused and answered true: %v", err)
	case err != nil:
		return unknownSensitivity
	case toxic:
		return toxicFlow
	}
	return notToxic
}

func assertFlowRefused(rt *rapid.T, toxic bool, err, want error, what string) {
	if !errors.Is(err, want) {
		rt.Fatalf("ToxicFlow = %t, %v for %s; want a refusal carrying %v", toxic, err, what, want)
	}
	if toxic {
		rt.Fatalf("ToxicFlow refused %s and answered true", what)
	}
}

// flowDraw is one drawn question. Each weakening rebuilds the envelope, so no
// two calls share a message.
type flowDraw struct {
	influence, computed, secrets bool
	read, floor                  controlv1.Sensitivity
	zone                         controlv1.TrustZone
	labels                       []controlv1.Sensitivity
}

func (d flowDraw) state() contract.FlowState {
	if d.computed {
		return contract.NewFlowState(d.influence, d.read)
	}
	return contract.FlowState{UntrustedInfluence: d.influence, MaxSensitivityRead: d.read}
}

func (d flowDraw) envelope() *controlv1.ActionEnvelope {
	env := withLabels(flowEnv(d.zone), d.labels...)
	if d.secrets {
		env = withSecrets(env)
	}
	return env
}

func (d flowDraw) without(erase func(*controlv1.ActionEnvelope)) *controlv1.ActionEnvelope {
	env := d.envelope()
	if env.Data == nil {
		env.Data = &controlv1.DataLabels{}
	}
	erase(env)
	return env
}

// wellFormed: a floor on the scale, and a reading and labels that are on it or
// are the zero value, which is unknown rather than malformed.
func (d flowDraw) wellFormed() bool {
	placeable := func(s controlv1.Sensitivity) bool { return s == unset || onTheScale(s) }
	for _, s := range d.labels {
		if !placeable(s) {
			return false
		}
	}
	return onTheScale(d.floor) && placeable(d.read)
}

// trustedZone reads the hand-written zone table; a zone it has no row for is
// untrusted.
func trustedZone(z controlv1.TrustZone) bool {
	for _, rule := range zonesUnderTest() {
		if rule.value == z {
			return !rule.untrusted
		}
	}
	return false
}

// onTheScale reports whether a value has a rank on the hand-written scale,
// which is what makes it usable as a floor.
func onTheScale(s controlv1.Sensitivity) bool {
	for _, rank := range sensitivityScale() {
		if rank.value == s {
			return true
		}
	}
	return false
}

// TestExportedTypeSetIsClosed pins the last shape of the public surface the
// other closure tests do not read: limits_test.go covers constants and
// variables, TestExportedFunctionSetIsClosed covers functions, and a type could
// enter a package ADR-0007 calls a compatibility promise with nothing saying
// so.
func TestExportedTypeSetIsClosed(t *testing.T) {
	promised := map[string]bool{"ValidationError": true, "FlowState": true}

	found := exportedTypes(t)
	if len(found) == 0 {
		t.Fatal("no exported type found in the package; this test reads its subject from the source")
	}
	for _, name := range found {
		if !promised[name] {
			t.Errorf("%s is exported from pkg/contract and this test does not know it: document it on docs/contracts.md and list it here, or unexport it",
				name)
		}
		delete(promised, name)
	}
	for name := range promised {
		t.Errorf("%s is promised here and the package no longer declares it", name)
	}
}

// TestExportedTypeShapesAreClosed pins what TestExportedTypeSetIsClosed cannot
// see: the exported fields of each exported type, and the methods of its
// pointer type, which hold the value's too. A name pin lets FlowState's
// computed flag be exported, and that flag is what keeps FlowState{} from
// being answered about; an Is method on ValidationError would change errors.Is
// for every caller.
func TestExportedTypeShapesAreClosed(t *testing.T) {
	for _, tc := range []struct {
		value   any
		fields  []string
		methods []string
	}{
		{contract.FlowState{}, []string{"UntrustedInfluence", "MaxSensitivityRead"}, nil},
		{contract.ValidationError{}, []string{"Field", "Err"}, []string{"Error", "Unwrap"}},
	} {
		typ := reflect.TypeOf(tc.value)
		var fields, methods []string
		for i := range typ.NumField() {
			if f := typ.Field(i); f.IsExported() {
				fields = append(fields, f.Name)
			}
		}
		ptr := reflect.PointerTo(typ)
		for i := range ptr.NumMethod() {
			methods = append(methods, ptr.Method(i).Name)
		}
		if !slices.Equal(fields, tc.fields) {
			t.Errorf("%s exports the fields %v, and this test knows %v", typ.Name(), fields, tc.fields)
		}
		if !slices.Equal(methods, tc.methods) {
			t.Errorf("*%s has the methods %v, and this test knows %v", typ.Name(), methods, tc.methods)
		}
	}
}

// exportedTypes returns the exported package-level type names, read from the
// source because Go cannot enumerate them at run time. The whole package and
// not one named file: the promise is package-wide.
func exportedTypes(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(packageDir)
	if err != nil {
		t.Fatalf("reading %s: %v", packageDir, err)
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		names = append(names, exportedTypesIn(t, filepath.Join(packageDir, name))...)
	}
	return names
}

func exportedTypesIn(t *testing.T, path string) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			if typed, ok := spec.(*ast.TypeSpec); ok && typed.Name.IsExported() {
				names = append(names, typed.Name.Name)
			}
		}
	}
	return names
}

// flowEnv returns an envelope declaring one destination zone, built on the
// envelope the validation tests use so that a case here is a message this
// package also accepts.
func flowEnv(zone controlv1.TrustZone) *controlv1.ActionEnvelope {
	env := valid()
	env.Destination = &controlv1.Destination{TrustZone: zone}
	return env
}

// noDestination returns an envelope that declares no destination at all, which
// is a different thing from one that declares a zone of UNSPECIFIED and has to
// read the same way.
func noDestination() *controlv1.ActionEnvelope {
	env := valid()
	env.Destination = nil
	return env
}

func withLabels(env *controlv1.ActionEnvelope, labels ...controlv1.Sensitivity) *controlv1.ActionEnvelope {
	if len(labels) == 0 {
		return env
	}
	if env.Data == nil {
		env.Data = &controlv1.DataLabels{}
	}
	env.Data.Sensitivities = append(env.Data.Sensitivities, labels...)
	return env
}

func withSecrets(env *controlv1.ActionEnvelope) *controlv1.ActionEnvelope {
	if env.Data == nil {
		env.Data = &controlv1.DataLabels{}
	}
	env.Data.ContainsSecrets = true
	return env
}

func withSources(env *controlv1.ActionEnvelope, sources ...string) *controlv1.ActionEnvelope {
	if env.Data == nil {
		env.Data = &controlv1.DataLabels{}
	}
	env.Data.Sources = append(env.Data.Sources, sources...)
	return env
}

// withoutSecretAssertion spells out the false a producer leaves behind by not
// asserting secrets, so the row that uses it says what it is testing: false is
// not a statement that there are none.
func withoutSecretAssertion(env *controlv1.ActionEnvelope) *controlv1.ActionEnvelope {
	if env.Data == nil {
		env.Data = &controlv1.DataLabels{}
	}
	env.Data.ContainsSecrets = false
	return env
}

// everySensitivity includes numbers no version of this contract declares, one
// above the scale and one below it, so the properties are stated over what an
// int32 field can carry and not only over what this build can name.
func everySensitivity() []controlv1.Sensitivity {
	all := []controlv1.Sensitivity{unset, controlv1.Sensitivity(42), controlv1.Sensitivity(-1)}
	for _, rank := range sensitivityScale() {
		all = append(all, rank.value)
	}
	return all
}

func everyZone() []controlv1.TrustZone {
	all := []controlv1.TrustZone{controlv1.TrustZone(42)}
	for _, rule := range zonesUnderTest() {
		all = append(all, rule.value)
	}
	return all
}

func drawSensitivity(rt *rapid.T, label string) controlv1.Sensitivity {
	return rapid.SampledFrom(everySensitivity()).Draw(rt, label)
}
