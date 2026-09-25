package match_test

import (
	"slices"
	"testing"

	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/match"
	"github.com/guardana/control/internal/policy/rules"
)

// veto is the rule "veto": DENY when w holds and the decision point denies.
func veto(w rules.When) rules.Rule {
	w.External = &rules.ExternalWhen{Denies: true}
	return rules.Rule{ID: "veto", Effect: deny, When: w}
}

// TestExternalConstraint is the veto rule against each answer: a denial holds,
// an answer that does not deny fails, and no answer, or one this build cannot
// name, is unknown. A rule whose other constraint is false fails whatever the
// answer and is not reported as reading it; one whose other constraint is
// unknown is reported, because an answer that does not deny still settles it.
func TestExternalConstraint(t *testing.T) {
	scoped := compile(t, veto(actionNamed("refund")))
	unscoped := compile(t, veto(rules.When{}))
	labelled := compile(t, veto(rules.When{Resource: &rules.ResourceWhen{Labels: map[string][]string{"region": {"eu"}}}}))
	undetermined := want{verdict: indeterminate, determinate: deny, undetermined: list("veto"), codes: list(ruleUndetermined), external: list("veto")}
	vetoed := want{verdict: deny, determinate: deny, ids: list("veto"), codes: list(ruleDeny), external: list("veto")}
	lifted := want{verdict: deny, determinate: deny, codes: list(noMatchingRule), external: list("veto")}
	outOfScope := want{verdict: deny, determinate: deny, codes: list(noMatchingRule)}
	archive := with(func(e *controlv1.ActionEnvelope) { e.Action.Name = "archive" })
	cases := []struct {
		name   string
		p      *match.Program
		env    *controlv1.ActionEnvelope
		answer match.External
		want   want
	}{
		{"no answer", scoped, envelope(), match.ExternalUnknown, undetermined},
		{"an undeclared answer", scoped, envelope(), 9, undetermined},
		{"a denial", scoped, envelope(), match.ExternalDenies, vetoed},
		{"an answer that does not deny", scoped, envelope(), match.ExternalAllows, lifted},
		{"alone, a denial", unscoped, envelope(), match.ExternalDenies, vetoed},
		{"alone, no answer", unscoped, envelope(), match.ExternalUnknown, undetermined},
		{"out of scope, no answer", scoped, archive, match.ExternalUnknown, outOfScope},
		{"out of scope, a denial", scoped, archive, match.ExternalDenies, outOfScope},
		{"out of scope, not denied", scoped, archive, match.ExternalAllows, outOfScope},
		{"scope unknown, a denial", labelled, envelope(), match.ExternalDenies, undetermined},
		{"scope unknown, not denied", labelled, envelope(), match.ExternalAllows, lifted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertResult(t, c.p.Evaluate(c.env, match.Inputs{External: c.answer}), c.want)
		})
	}
}

// TestProgramReadsExternal: a program reads the external answer exactly when
// one of its rules names the constraint, and a program nobody compiled reads
// nothing.
func TestProgramReadsExternal(t *testing.T) {
	if !compile(t, veto(actionNamed("refund")), rules.Rule{ID: "allow", Effect: allow, When: actionNamed("refund")}).ReadsExternal() {
		t.Error("a program with a veto rule does not read the external answer")
	}
	if compile(t, rules.Rule{ID: "deny", Effect: deny, When: actionNamed("refund")}).ReadsExternal() {
		t.Error("a program without a veto rule reads the external answer")
	}
	var none *match.Program
	if none.ReadsExternal() || (&match.Program{}).ReadsExternal() {
		t.Error("a program nobody compiled reads the external answer")
	}
}

// TestTheAnswerMattersOnlyWhereReported evaluates each drawn call under every
// answer. ReadExternal is the same under all of them; when it is empty every
// answer gives the same result; each rule it names is undetermined without an
// answer and settled by one that does not deny. An answer that does not deny
// gives exactly the result of the document with every veto rule deleted, so
// the answer never grants, and no answer permits more than that one.
func TestTheAnswerMattersOnlyWhereReported(t *testing.T) {
	reported := 0
	rapid.Check(t, func(rt *rapid.T) {
		spec := drawDocument(rt)
		env, in := drawEnvelope(rt), drawInputs(rt)
		program := mustCompile(rt, spec.build())
		results := make(map[match.External]match.Result)
		for _, answer := range answers() {
			in.External = answer
			results[answer] = program.Evaluate(env, in)
		}
		unknown, allows := results[match.ExternalUnknown], results[match.ExternalAllows]
		for answer, got := range results {
			if !slices.Equal(got.ReadExternal, unknown.ReadExternal) {
				rt.Fatalf("answer %d reports %q, no answer %q", answer, got.ReadExternal, unknown.ReadExternal)
			}
			if len(unknown.ReadExternal) == 0 {
				assertSame(rt, "an answer nothing reads", unknown, got)
			}
			assertNoMorePermissive(rt, allows, got)
		}
		for _, id := range unknown.ReadExternal {
			if !slices.Contains(unknown.Indeterminate, id) || slices.Contains(allows.RuleIDs, id) || slices.Contains(allows.Indeterminate, id) {
				rt.Fatalf("rule %s is reported, yet without an answer %s and not denied %s", id, summary(unknown), summary(allows))
			}
		}
		reported += len(unknown.ReadExternal)
		without := slices.DeleteFunc(slices.Clone(spec), func(s ruleSpec) bool { return s.external })
		in.External = match.ExternalAllows
		lifted := mustCompile(rt, without.build()).Evaluate(env, in)
		allows.ReadExternal = nil
		assertSame(rt, "not denied, against the document without its veto rules", lifted, allows)
	})
	if reported == 0 {
		t.Fatal("no drawn rule was ever reported as reading the external answer")
	}
}
