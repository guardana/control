package match

import (
	"errors"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/rules"
)

const (
	allowEffect       = controlv1.Verdict_VERDICT_ALLOW
	denyEffect        = controlv1.Verdict_VERDICT_DENY
	approvalEffect    = controlv1.Verdict_VERDICT_REQUIRE_APPROVAL
	obligationsEffect = controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS
)

// refusalCase is a rule Compile refuses and its accepting twin, one change
// away. A refusal that fires for another reason fails on the field or the
// sentinel, and a Compile that refuses everything fails on the twin.
type refusalCase struct {
	name    string
	refused rules.Rule
	twin    rules.Rule
	field   string
	err     error
}

func checkRefusals(t *testing.T, cases []refusalCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Compile(&rules.Document{Rules: []rules.Rule{c.refused}})
			var ce *compileError
			switch {
			case !errors.As(err, &ce):
				t.Errorf("Compile = %v, %v; want a refusal", p, err)
			case !errors.Is(err, c.err) || ce.rule != 0 || ce.field != c.field:
				t.Errorf("refused with %q at rules[%d].%s, want %q at rules[0].%s", ce.err, ce.rule, ce.field, c.err, c.field)
			}
			if _, err := Compile(&rules.Document{Rules: []rules.Rule{c.twin}}); err != nil {
				t.Errorf("Compile refused the accepting twin: %v", err)
			}
		})
	}
}

func refund() rules.When { return rules.When{Action: &rules.ActionWhen{Name: []string{"refund"}}} }

func capped() []rules.Obligation {
	return []rules.Obligation{{Type: "cap_amount", Params: map[string]string{"max": "100"}}}
}

func TestCompileRefusesWhatARuleCannotCarry(t *testing.T) {
	r := func(id string, effect controlv1.Verdict, reason string, obligations []rules.Obligation) rules.Rule {
		return rules.Rule{ID: id, Effect: effect, Reason: reason, Obligations: obligations, When: refund()}
	}
	checkRefusals(t, []refusalCase{
		{"an empty id", r("", denyEffect, "", nil), r("r", denyEffect, "", nil), "id", errRuleID},
		{"an unspecified effect", r("r", controlv1.Verdict_VERDICT_UNSPECIFIED, "", nil), r("r", denyEffect, "", nil), "effect", errEffect},
		{"INDETERMINATE as an effect", r("r", controlv1.Verdict_VERDICT_INDETERMINATE, "", nil), r("r", denyEffect, "", nil), "effect", errEffect},
		{"an undeclared effect", r("r", controlv1.Verdict(42), "", nil), r("r", denyEffect, "", nil), "effect", errEffect},
		{"a kernel fact as a reason", r("r", denyEffect, "POLICY_STALE", nil), r("r", denyEffect, "ENVIRONMENT_BOUNDARY", nil), "reason", errReason},
		{"TENANT_MISMATCH as a reason", r("r", denyEffect, "TENANT_MISMATCH", nil), r("r", denyEffect, "OUT_OF_SCOPE_ACTION", nil), "reason", errReason},
		{"another effect's own reason", r("r", allowEffect, "RULE_DENY", nil), r("r", allowEffect, "RULE_ALLOW", nil), "reason", errReason},
		{"a DENY reason on ALLOW", r("r", allowEffect, "ENVIRONMENT_BOUNDARY", nil), r("r", allowEffect, "", nil), "reason", errReason},
		{"a DENY reason on REQUIRE_APPROVAL", r("r", approvalEffect, "TOXIC_FLOW_SENSITIVE_TO_EXTERNAL", nil),
			r("r", approvalEffect, "APPROVAL_REQUIRED", nil), "reason", errReason},
		{"a DENY reason on ALLOW_WITH_OBLIGATIONS", r("r", obligationsEffect, "OUT_OF_SCOPE_ACTION", capped()),
			r("r", obligationsEffect, "OBLIGATIONS_ATTACHED", capped()), "reason", errReason},
		{"a code in another case", r("r", denyEffect, "rule_deny", nil), r("r", denyEffect, "RULE_DENY", nil), "reason", errReason},
		{"obligations on ALLOW", r("r", allowEffect, "", capped()), r("r", allowEffect, "", nil), "obligations", errObligations},
		{"obligations on DENY", r("r", denyEffect, "", capped()), r("r", denyEffect, "", nil), "obligations", errObligations},
		{"ALLOW_WITH_OBLIGATIONS with none", r("r", obligationsEffect, "", nil), r("r", obligationsEffect, "", capped()), "obligations", errObligations},
		{"only ALLOW_WITH_OBLIGATIONS needs one", r("r", obligationsEffect, "", nil), r("r", approvalEffect, "", nil), "obligations", errObligations},
	})
}

func TestCompileRefusesWhatAConstraintCannotMean(t *testing.T) {
	deny := func(w rules.When) rules.Rule { return rules.Rule{ID: "r", Effect: denyEffect, When: w} }
	effects := func(e ...controlv1.EffectClass) rules.When { return rules.When{Action: &rules.ActionWhen{Effect: e}} }
	zones := func(z ...controlv1.TrustZone) rules.When {
		return rules.When{Destination: &rules.DestinationWhen{TrustZone: z}}
	}
	data := func(s controlv1.Sensitivity) rules.When {
		return rules.When{Data: &rules.DataWhen{SensitivityAtLeast: s}}
	}
	flow := func(s controlv1.Sensitivity) rules.When { return rules.When{Flow: &rules.FlowWhen{ToxicAtLeast: s}} }
	attributes := func(m map[string][]string) rules.When {
		return rules.When{Principal: &rules.PrincipalWhen{Attributes: m}}
	}
	team := map[string][]string{"team": {"payments"}}
	checkRefusals(t, []refusalCase{
		{"a when that constrains nothing", deny(rules.When{}), deny(refund()), "when", errNoConstraint},
		{"a when whose only group is empty", deny(rules.When{Action: &rules.ActionWhen{}}), deny(refund()), "when", errNoConstraint},
		{"an empty list", deny(rules.When{Action: &rules.ActionWhen{Name: []string{}}}), deny(refund()), "when.action.name", errEmptyList},
		{"an empty value", deny(rules.When{Action: &rules.ActionWhen{Name: []string{""}}}), deny(refund()), "when.action.name", errUnusable},
		{"an empty value beside a usable one", deny(rules.When{Action: &rules.ActionWhen{Name: []string{"refund", ""}}}),
			deny(refund()), "when.action.name", errUnusable},
		{"an unspecified effect class", deny(effects(controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED)),
			deny(effects(controlv1.EffectClass_EFFECT_CLASS_READ)), "when.action.effect", errUnusable},
		{"an undeclared effect class", deny(effects(controlv1.EffectClass(42))),
			deny(effects(controlv1.EffectClass_EFFECT_CLASS_READ)), "when.action.effect", errUnusable},
		{"an empty list of effect classes", deny(rules.When{Action: &rules.ActionWhen{Effect: []controlv1.EffectClass{}}}), deny(effects(controlv1.EffectClass_EFFECT_CLASS_READ)), "when.action.effect", errEmptyList},
		{"an unspecified trust zone", deny(zones(controlv1.TrustZone_TRUST_ZONE_UNSPECIFIED)),
			deny(zones(controlv1.TrustZone_TRUST_ZONE_TRUSTED_INTERNAL)), "when.destination.trustZone", errUnusable},
		{"an undeclared trust zone", deny(zones(controlv1.TrustZone(42))),
			deny(zones(controlv1.TrustZone_TRUST_ZONE_TRUSTED_INTERNAL)), "when.destination.trustZone", errUnusable},
		{"an unset data floor", deny(data(controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED)),
			deny(data(controlv1.Sensitivity_SENSITIVITY_PUBLIC)), "when.data.sensitivityAtLeast", errFloor},
		{"a data floor above the scale", deny(data(6)), deny(data(controlv1.Sensitivity_SENSITIVITY_SECRET)), "when.data.sensitivityAtLeast", errFloor},
		{"a data floor below the scale", deny(data(-1)), deny(data(controlv1.Sensitivity_SENSITIVITY_PUBLIC)), "when.data.sensitivityAtLeast", errFloor},
		{"an unset flow floor", deny(flow(controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED)),
			deny(flow(controlv1.Sensitivity_SENSITIVITY_CONFIDENTIAL)), "when.flow.toxicAtLeast", errFloor},
		{"a flow floor above the scale", deny(flow(6)), deny(flow(controlv1.Sensitivity_SENSITIVITY_SECRET)), "when.flow.toxicAtLeast", errFloor},
		{"a map with no keys", deny(attributes(map[string][]string{})), deny(attributes(team)), "when.principal.attributes", errEmptyList},
		{"a key with an empty list", deny(attributes(map[string][]string{"team": {}})), deny(attributes(team)), "when.principal.attributes", errEmptyList},
		{"a key with no list", deny(attributes(map[string][]string{"team": nil})), deny(attributes(team)), "when.principal.attributes", errEmptyList},
		// The twin names the empty key, which an envelope may carry: it is a
		// key like any other, and only what is listed under it is refused.
		{"an empty value under the empty key", deny(attributes(map[string][]string{"": {""}})),
			deny(attributes(map[string][]string{"": {"payments"}})), "when.principal.attributes", errUnusable},
		{"an empty value under a key", deny(rules.When{Resource: &rules.ResourceWhen{Labels: map[string][]string{"tier": {""}}}}),
			deny(rules.When{Resource: &rules.ResourceWhen{Labels: map[string][]string{"tier": {"gold"}}}}), "when.resource.labels", errUnusable},
		{"an empty list of scopes", deny(rules.When{Delegation: &rules.DelegationWhen{Scopes: []string{}}}),
			deny(rules.When{Delegation: &rules.DelegationWhen{Scopes: []string{"read"}}}), "when.delegation.scopes", errEmptyList},
		{"an empty scope", deny(rules.When{Delegation: &rules.DelegationWhen{Scopes: []string{""}}}),
			deny(rules.When{Delegation: &rules.DelegationWhen{Scopes: []string{"read"}}}), "when.delegation.scopes", errUnusable},
	})
}

// TestCompileRefusesExternalOffDeny: the external constraint compiles on a
// DENY rule alone, in the one form a denial takes, whatever else the rule
// reads. Each other effect is refused with the rule otherwise as its twin.
func TestCompileRefusesExternalOffDeny(t *testing.T) {
	vetoing := func(effect controlv1.Verdict, obligations []rules.Obligation, w rules.When) rules.Rule {
		w.External = &rules.ExternalWhen{Denies: true}
		return rules.Rule{ID: "r", Effect: effect, Obligations: obligations, When: w}
	}
	allowing := rules.Rule{ID: "r", Effect: denyEffect, When: rules.When{External: &rules.ExternalWhen{}}}
	checkRefusals(t, []refusalCase{
		{"on ALLOW", vetoing(allowEffect, nil, refund()), vetoing(denyEffect, nil, refund()), "when.external", errExternalEffect},
		{"on REQUIRE_APPROVAL", vetoing(approvalEffect, nil, refund()), vetoing(denyEffect, nil, refund()), "when.external", errExternalEffect},
		{"on REQUIRE_APPROVAL with obligations", vetoing(approvalEffect, capped(), refund()), vetoing(denyEffect, nil, refund()),
			"when.external", errExternalEffect},
		{"on ALLOW_WITH_OBLIGATIONS", vetoing(obligationsEffect, capped(), refund()), vetoing(denyEffect, nil, refund()),
			"when.external", errExternalEffect},
		{"alone on ALLOW", vetoing(allowEffect, nil, rules.When{}), vetoing(denyEffect, nil, rules.When{}), "when.external", errExternalEffect},
		{"a form other than a denial", allowing, vetoing(denyEffect, nil, rules.When{}), "when.external.denies", errExternal},
	})
}

// TestEveryStringFieldIsCompiled sweeps the string fields: each alone makes a
// rule that compiles, so each is wired to a constraint, and each refuses an
// empty list and an empty value under its own name.
func TestEveryStringFieldIsCompiled(t *testing.T) {
	for _, f := range []struct {
		field string
		set   func(*rules.When, []string)
	}{
		{"when.principal.id", func(w *rules.When, v []string) { w.Principal = &rules.PrincipalWhen{ID: v} }},
		{"when.principal.type", func(w *rules.When, v []string) { w.Principal = &rules.PrincipalWhen{Type: v} }},
		{"when.principal.authnStrength", func(w *rules.When, v []string) { w.Principal = &rules.PrincipalWhen{AuthnStrength: v} }},
		{"when.principal.tenantId", func(w *rules.When, v []string) { w.Principal = &rules.PrincipalWhen{TenantID: v} }},
		{"when.principal.attributes", func(w *rules.When, v []string) {
			w.Principal = &rules.PrincipalWhen{Attributes: map[string][]string{"team": v}}
		}},
		{"when.agent.id", func(w *rules.When, v []string) { w.Agent = &rules.AgentWhen{ID: v} }},
		{"when.agent.framework", func(w *rules.When, v []string) { w.Agent = &rules.AgentWhen{Framework: v} }},
		{"when.action.name", func(w *rules.When, v []string) { w.Action = &rules.ActionWhen{Name: v} }},
		{"when.action.provider", func(w *rules.When, v []string) { w.Action = &rules.ActionWhen{Provider: v} }},
		{"when.action.protocol", func(w *rules.When, v []string) { w.Action = &rules.ActionWhen{Protocol: v} }},
		{"when.action.kind", func(w *rules.When, v []string) { w.Action = &rules.ActionWhen{Kind: v} }},
		{"when.resource.type", func(w *rules.When, v []string) { w.Resource = &rules.ResourceWhen{Type: v} }},
		{"when.resource.id", func(w *rules.When, v []string) { w.Resource = &rules.ResourceWhen{ID: v} }},
		{"when.resource.tenantId", func(w *rules.When, v []string) { w.Resource = &rules.ResourceWhen{TenantID: v} }},
		{"when.resource.environment", func(w *rules.When, v []string) { w.Resource = &rules.ResourceWhen{Environment: v} }},
		{"when.resource.labels", func(w *rules.When, v []string) {
			w.Resource = &rules.ResourceWhen{Labels: map[string][]string{"tier": v}}
		}},
		{"when.destination.host", func(w *rules.When, v []string) { w.Destination = &rules.DestinationWhen{Host: v} }},
		{"when.delegation.scopes", func(w *rules.When, v []string) { w.Delegation = &rules.DelegationWhen{Scopes: v} }},
	} {
		t.Run(f.field, func(t *testing.T) {
			rule := func(values []string) rules.Rule {
				r := rules.Rule{ID: "r", Effect: denyEffect}
				f.set(&r.When, values)
				return r
			}
			checkRefusals(t, []refusalCase{
				{"an empty list", rule([]string{}), rule([]string{"x"}), f.field, errEmptyList},
				{"an empty value", rule([]string{"x", ""}), rule([]string{"x"}), f.field, errUnusable},
			})
		})
	}
}

func TestCompileRefusesARepeatedRuleID(t *testing.T) {
	doc := func(ids ...string) *rules.Document {
		d := &rules.Document{}
		for _, id := range ids {
			d.Rules = append(d.Rules, rules.Rule{ID: id, Effect: denyEffect, When: refund()})
		}
		return d
	}
	_, err := Compile(doc("same", "other", "same"))
	var ce *compileError
	if !errors.As(err, &ce) || !errors.Is(err, errRuleID) || ce.rule != 2 || ce.field != "id" {
		t.Errorf("Compile = %v; want errRuleID at rules[2].id", err)
	}
	if _, err := Compile(doc("same", "other", "third")); err != nil {
		t.Errorf("Compile refused three distinct ids: %v", err)
	}
}

func TestCompileRefusesNoDocumentByName(t *testing.T) {
	_, err := Compile(nil)
	var ce *compileError
	if !errors.As(err, &ce) || !errors.Is(err, errNoDocument) || ce.rule != -1 {
		t.Errorf("Compile(nil) = %v; want errNoDocument for the document as a whole", err)
	}
}

// TestCompileErrorsRepeatNoValue: a refusal names the position and the field
// and never what the document holds there.
func TestCompileErrorsRepeatNoValue(t *testing.T) {
	const marker = "do-not-echo"
	for _, doc := range []*rules.Document{
		{Rules: []rules.Rule{{ID: marker, Effect: denyEffect, When: refund()}, {ID: marker, Effect: denyEffect, When: refund()}}},
		{Rules: []rules.Rule{{ID: marker, Effect: denyEffect, Reason: marker, When: refund()}}},
		{Rules: []rules.Rule{{ID: marker, Effect: denyEffect, When: rules.When{
			Principal: &rules.PrincipalWhen{Attributes: map[string][]string{marker: {marker, ""}}},
		}}}},
	} {
		_, err := Compile(doc)
		if err == nil {
			t.Fatal("Compile accepted a document this test builds to be refused")
		}
		if msg := err.Error(); strings.Contains(msg, marker) || !strings.HasPrefix(msg, "match: rules[") {
			t.Errorf("refusal %q repeats a value from the document or names no rule", msg)
		}
	}
}
