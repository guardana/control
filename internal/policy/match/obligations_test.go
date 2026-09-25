package match_test

import (
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/match"
	"github.com/guardana/control/internal/policy/rules"
)

// TestObligationsAreTheUnionInDocumentOrder: the union over every matched
// ALLOW_WITH_OBLIGATIONS and REQUIRE_APPROVAL rule, in document order and then
// each rule's own order, one kept of any two with the same type, parameters
// and advisory flag.
func TestObligationsAreTheUnionInDocumentOrder(t *testing.T) {
	p := compile(t,
		rules.Rule{ID: "cap", Effect: withObligations, When: actionNamed("refund"), Obligations: []rules.Obligation{
			{Type: "cap_amount", Params: map[string]string{"max": "100"}},
			{Type: "emit_alert", Advisory: true},
		}},
		rules.Rule{ID: "approve", Effect: approval, When: actionNamed("refund"), Obligations: []rules.Obligation{
			{Type: "second_approver"},
			{Type: "cap_amount", Params: map[string]string{"max": "100"}},
		}},
		rules.Rule{ID: "cap-again", Effect: withObligations, When: actionNamed("refund"), Obligations: []rules.Obligation{
			{Type: "cap_amount", Params: map[string]string{"max": "200"}},
			{Type: "emit_alert"},
		}},
	)
	assertResult(t, p.Evaluate(envelope(), match.Inputs{}), want{
		verdict: approval, determinate: approval,
		ids:   list("cap", "approve", "cap-again"),
		codes: list(attached, approvalRequired),
		obligations: []*controlv1.Obligation{
			{Type: "cap_amount", Params: map[string]string{"max": "100"}},
			{Type: "emit_alert", Advisory: true},
			{Type: "second_approver"},
			{Type: "cap_amount", Params: map[string]string{"max": "200"}},
			{Type: "emit_alert"},
		},
	})
}

// TestObligationsBindOnlyACallThatProceeds: carried under REQUIRE_APPROVAL and
// ALLOW_WITH_OBLIGATIONS, never under DENY or INDETERMINATE, and never shed
// because a broader ALLOW matched too.
func TestObligationsBindOnlyACallThatProceeds(t *testing.T) {
	capAmount := func() []rules.Obligation {
		return []rules.Obligation{{Type: "cap_amount", Params: map[string]string{"max": "100"}}}
	}
	capped := []*controlv1.Obligation{{Type: "cap_amount", Params: map[string]string{"max": "100"}}}
	undecided := with(func(e *controlv1.ActionEnvelope) { e.Resource.Environment = "" })
	for _, c := range []struct {
		name string
		doc  []rules.Rule
		env  *controlv1.ActionEnvelope
		want want
	}{
		{"under ALLOW_WITH_OBLIGATIONS",
			[]rules.Rule{{ID: "cap", Effect: withObligations, When: actionNamed("refund"), Obligations: capAmount()}},
			envelope(),
			want{verdict: withObligations, determinate: withObligations, ids: list("cap"), codes: list(attached), obligations: capped}},
		{"under REQUIRE_APPROVAL",
			[]rules.Rule{{ID: "approve", Effect: approval, When: actionNamed("refund"), Obligations: capAmount()}},
			envelope(),
			want{verdict: approval, determinate: approval, ids: list("approve"), codes: list(approvalRequired), obligations: capped}},
		{"a broader ALLOW sheds nothing",
			[]rules.Rule{
				{ID: "allow", Effect: allow, When: actionNamed("refund")},
				{ID: "cap", Effect: withObligations, When: actionNamed("refund"), Obligations: capAmount()},
			},
			envelope(),
			want{verdict: withObligations, determinate: withObligations, ids: list("allow", "cap"), codes: list(ruleAllow, attached), obligations: capped}},
		{"not under DENY",
			[]rules.Rule{
				{ID: "cap", Effect: withObligations, When: actionNamed("refund"), Obligations: capAmount()},
				{ID: "deny", Effect: deny, When: actionNamed("refund")},
			},
			envelope(),
			want{verdict: deny, determinate: deny, ids: list("cap", "deny"), codes: list(attached, ruleDeny)}},
		{"not under INDETERMINATE",
			[]rules.Rule{
				{ID: "cap", Effect: withObligations, When: actionNamed("refund"), Obligations: capAmount()},
				{ID: "unknown-deny", Effect: deny, When: rules.When{Resource: &rules.ResourceWhen{Environment: list("prod")}}},
			},
			undecided,
			want{verdict: indeterminate, determinate: withObligations, ids: list("cap"), undetermined: list("unknown-deny"),
				codes: list(attached, ruleUndetermined)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			assertResult(t, compile(t, c.doc...).Evaluate(c.env, match.Inputs{}), c.want)
		})
	}
}

// TestObligationIdentity: two obligations are one when their type, parameters
// and advisory flag are equal, and only then. A parameter's name is part of
// the parameters, not only its value.
func TestObligationIdentity(t *testing.T) {
	p := compile(t,
		rules.Rule{ID: "first", Effect: withObligations, When: actionNamed("refund"), Obligations: []rules.Obligation{
			{Type: "cap_amount", Params: map[string]string{"max": "100"}},
			{Type: "cap_amount", Params: map[string]string{"max": "100"}},
			{Type: "cap_amount", Params: map[string]string{"max": "200"}},
			{Type: "emit_alert", Advisory: true},
			{Type: "emit_alert"},
			// Joined as key=value with commas, these two read alike.
			{Type: "restrict_resources", Params: map[string]string{"a": "1,b=2"}},
			{Type: "restrict_resources", Params: map[string]string{"a": "1", "b": "2"}},
			{Type: "redact_fields"},
		}},
		rules.Rule{ID: "second", Effect: approval, When: actionNamed("refund"), Obligations: []rules.Obligation{
			// No parameters and an empty map of them are one obligation.
			{Type: "redact_fields", Params: map[string]string{}},
			{Type: "cap_amount", Params: map[string]string{"max": "100"}},
		}},
		rules.Rule{ID: "third", Effect: withObligations, When: actionNamed("refund"), Obligations: []rules.Obligation{
			// One value under two names.
			{Type: "redact_fields", Params: map[string]string{"field": "ssn"}},
			{Type: "redact_fields", Params: map[string]string{"path": "ssn"}},
		}},
	)
	assertResult(t, p.Evaluate(envelope(), match.Inputs{}), want{
		verdict: approval, determinate: approval,
		ids:   list("first", "second", "third"),
		codes: list(attached, approvalRequired),
		obligations: []*controlv1.Obligation{
			{Type: "cap_amount", Params: map[string]string{"max": "100"}},
			{Type: "cap_amount", Params: map[string]string{"max": "200"}},
			{Type: "emit_alert", Advisory: true},
			{Type: "emit_alert"},
			{Type: "restrict_resources", Params: map[string]string{"a": "1,b=2"}},
			{Type: "restrict_resources", Params: map[string]string{"a": "1", "b": "2"}},
			{Type: "redact_fields"},
			{Type: "redact_fields", Params: map[string]string{"field": "ssn"}},
			{Type: "redact_fields", Params: map[string]string{"path": "ssn"}},
		},
	})
}

// TestObligationIdentityHasOneReadingOrder: one obligation with four
// parameters, carried by two rules, is kept once. Go ranges over a map in an
// order that changes from one range to the next, so an identity that followed
// it would differ between the two rules in some compilations; the document is
// compiled afresh each time.
func TestObligationIdentityHasOneReadingOrder(t *testing.T) {
	restrict := func() []rules.Obligation {
		return []rules.Obligation{{Type: "restrict_resources", Params: map[string]string{
			"tenant": "tenant-a", "environment": "prod", "region": "eu", "tier": "gold",
		}}}
	}
	once := []*controlv1.Obligation{{Type: "restrict_resources", Params: map[string]string{
		"tenant": "tenant-a", "environment": "prod", "region": "eu", "tier": "gold",
	}}}
	const compilations = 200
	other := 0
	for range compilations {
		p := compile(t,
			rules.Rule{ID: "first", Effect: withObligations, When: actionNamed("refund"), Obligations: restrict()},
			rules.Rule{ID: "second", Effect: approval, When: actionNamed("refund"), Obligations: restrict()},
		)
		if got := p.Evaluate(envelope(), match.Inputs{}); !sameObligations(got.Obligations, once) {
			other++
		}
	}
	if other > 0 {
		t.Errorf("%d of %d compilations did not keep %s exactly once", other, compilations, describe(once))
	}
}

// TestRulesThatDoNotMatchAttachNothing: only a matched rule contributes.
func TestRulesThatDoNotMatchAttachNothing(t *testing.T) {
	p := compile(t,
		rules.Rule{ID: "false-cap", Effect: withObligations, When: actionNamed("archive"),
			Obligations: []rules.Obligation{{Type: "read_only"}}},
		rules.Rule{ID: "false-approve", Effect: approval, When: actionNamed("archive"),
			Obligations: []rules.Obligation{{Type: "second_approver"}}},
		rules.Rule{ID: "cap", Effect: withObligations, When: actionNamed("refund"),
			Obligations: []rules.Obligation{{Type: "cap_amount", Params: map[string]string{"max": "100"}}}},
	)
	assertResult(t, p.Evaluate(envelope(), match.Inputs{}), want{
		verdict: withObligations, determinate: withObligations,
		ids: list("cap"), codes: list(attached),
		obligations: []*controlv1.Obligation{{Type: "cap_amount", Params: map[string]string{"max": "100"}}},
	})
}
