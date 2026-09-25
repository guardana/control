package core

import (
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// effectClasses is every declared class, UNSPECIFIED included, and two numbers
// no build declares.
func effectClasses(t *testing.T) []controlv1.EffectClass {
	t.Helper()
	classes := []controlv1.EffectClass{controlv1.EffectClass(-1), controlv1.EffectClass(4096)}
	values := controlv1.EffectClass(0).Descriptor().Values()
	for i := 0; i < values.Len(); i++ {
		classes = append(classes, controlv1.EffectClass(values.Get(i).Number()))
	}
	if len(classes) < 12 {
		t.Fatalf("%d classes under test; the contract declares ten", len(classes))
	}
	return classes
}

// TestFailClosedTableNamedRows: the rows of the fail-closed table (ADR-0012),
// and the edges failClosed adds around it, each written out.
func TestFailClosedTableNamedRows(t *testing.T) {
	read, write := controlv1.EffectClass_EFFECT_CLASS_READ, controlv1.EffectClass_EFFECT_CLASS_WRITE
	allow, deny := controlv1.Verdict_VERDICT_ALLOW, controlv1.Verdict_VERDICT_DENY
	rows := []struct {
		name string
		in   undecided
		want EnforcementAction
	}{
		{"no snapshot, READ, fail-open on", undecided{effect: read, availability: true, failOpenRead: true}, Execute},
		{"no snapshot, READ, fail-open off", undecided{effect: read, availability: true}, Block},
		{"no snapshot, WRITE, fail-open on", undecided{effect: write, availability: true, failOpenRead: true}, Block},
		{"stale, determinate ALLOW, READ, fail-open on", undecided{effect: read, availability: true, failOpenRead: true, snapshot: true, determinate: allow}, Execute},
		{"stale, determinate ALLOW, READ, fail-open off", undecided{effect: read, availability: true, snapshot: true, determinate: allow}, Block},
		{"stale, determinate ALLOW, WRITE, fail-open on", undecided{effect: write, availability: true, failOpenRead: true, snapshot: true, determinate: allow}, Block},
		{"stale and nothing matched: determinate DENY", undecided{effect: read, availability: true, failOpenRead: true, snapshot: true, determinate: deny}, Block},
		{"stale beside an undetermined rule: an input cause", undecided{effect: read, input: true, availability: true, failOpenRead: true, snapshot: true, determinate: allow}, Block},
		{"an input cause alone, READ, fail-open on", undecided{effect: read, input: true, failOpenRead: true}, Block},
		{"a snapshot nobody compiled: present, determinate DENY", undecided{effect: read, availability: true, failOpenRead: true, snapshot: true, determinate: deny}, Block},
		{"UNSPECIFIED, availability only, fail-open on", undecided{effect: controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED, availability: true, failOpenRead: true}, Block},
		{"undeclared, availability only, fail-open on", undecided{effect: controlv1.EffectClass(4096), availability: true, failOpenRead: true}, Block},
		{"no cause at all", undecided{effect: read, failOpenRead: true}, Block},
	}
	for _, r := range rows {
		got, opened := failClosed(r.in)
		if got != r.want {
			t.Errorf("%s: %d, want %d", r.name, got, r.want)
		}
		if opened != (got == Execute) {
			t.Errorf("%s: fail-open reported %v with action %d", r.name, opened, got)
		}
	}
}

// wayOut is the one row of the table that executes, restated from ADR-0012
// rather than read from the table: a READ, no cause in the request, a cause
// in availability, FailOpenRead on, and no snapshot or a determinate ALLOW.
func wayOut(in undecided) bool {
	return in.effect == controlv1.EffectClass_EFFECT_CLASS_READ && !in.input && in.availability && in.failOpenRead &&
		(!in.snapshot || in.determinate == controlv1.Verdict_VERDICT_ALLOW)
}

// everyUndecided crosses every effect class, cause class, FailOpenRead,
// snapshot presence and determinate verdict, the undeclared values included.
func everyUndecided(t *testing.T) []undecided {
	t.Helper()
	verdicts := []controlv1.Verdict{controlv1.Verdict(-1)}
	values := controlv1.Verdict(0).Descriptor().Values()
	for i := 0; i < values.Len(); i++ {
		verdicts = append(verdicts, controlv1.Verdict(values.Get(i).Number()))
	}
	var all []undecided
	for _, effect := range effectClasses(t) {
		for _, c := range []struct{ input, availability bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
			for _, failOpenRead := range []bool{false, true} {
				for _, snapshot := range []bool{false, true} {
					for _, determinate := range verdicts {
						all = append(all, undecided{effect: effect, input: c.input, availability: c.availability,
							failOpenRead: failOpenRead, snapshot: snapshot, determinate: determinate})
					}
				}
			}
		}
	}
	return all
}

// TestFailClosedTableExhaustively holds every combination to wayOut: UNSPECIFIED
// and an undeclared class block under every cause and never reach FailOpenRead.
func TestFailClosedTableExhaustively(t *testing.T) {
	executed := 0
	for _, in := range everyUndecided(t) {
		want := Block
		if wayOut(in) {
			want = Execute
			executed++
		}
		got, opened := failClosed(in)
		if got != want || opened != (want == Execute) {
			t.Errorf("%+v: (%d, %v), want (%d, %v)", in, got, opened, want, want == Execute)
		}
	}
	// One class, one cause combination, fail-open on: no snapshot with every
	// determinate value (seven), plus a snapshot with ALLOW.
	if executed != 8 {
		t.Errorf("%d combinations execute, want 8", executed)
	}
}

// TestActionFor: the four determinate verdicts have fixed actions, and any
// other verdict, UNSPECIFIED and an undeclared number included, goes to the
// table, which blocks it here because nothing caused it.
func TestActionFor(t *testing.T) {
	fixed := map[controlv1.Verdict]EnforcementAction{
		controlv1.Verdict_VERDICT_DENY:                   Block,
		controlv1.Verdict_VERDICT_REQUIRE_APPROVAL:       AwaitApproval,
		controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS: ExecuteWithObligations,
		controlv1.Verdict_VERDICT_ALLOW:                  Execute,
	}
	for _, v := range []controlv1.Verdict{-1, 0, 1, 2, 3, 4, 5, 6, 4096} {
		got, opened := actionFor(v, undecided{effect: controlv1.EffectClass_EFFECT_CLASS_READ, failOpenRead: true})
		want, ok := fixed[v]
		if !ok {
			want = Block
		}
		if got != want || opened {
			t.Errorf("%s: (%d, %v), want (%d, false)", v, got, opened, want)
		}
	}
}
