package match_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/match"
	"github.com/guardana/control/internal/policy/rules"
	"github.com/guardana/control/pkg/contract"
)

// fuzzRules read every kind of constraint, under every effect.
func fuzzRules() []rules.Rule {
	return []rules.Rule{
		{ID: "allow-reads", Effect: allow, When: rules.When{Action: &rules.ActionWhen{Effect: []controlv1.EffectClass{read}}}},
		{ID: "deny-toxic", Effect: deny, Reason: "TOXIC_FLOW_SENSITIVE_TO_EXTERNAL",
			When: rules.When{Flow: &rules.FlowWhen{ToxicAtLeast: confidential}}},
		{ID: "deny-secrets-out", Effect: deny, When: rules.When{
			Data:        &rules.DataWhen{SensitivityAtLeast: restricted},
			Destination: &rules.DestinationWhen{TrustZone: []controlv1.TrustZone{zoneExternal}},
		}},
		{ID: "approve-admin", Effect: approval, When: rules.When{Delegation: &rules.DelegationWhen{Scopes: list("admin")}},
			Obligations: []rules.Obligation{{Type: "second_approver"}}},
		{ID: "cap-refunds", Effect: withObligations, When: rules.When{
			Action: &rules.ActionWhen{Name: list("refund"), Provider: list("payments"), Protocol: list("mcp"), Kind: list("tool_call")},
			Principal: &rules.PrincipalWhen{ID: list("alice"), Type: list("user"), AuthnStrength: list("mfa"),
				Attributes: map[string][]string{"team": {"payments"}}},
			Resource: &rules.ResourceWhen{Type: list("invoice"), ID: list("inv-1"), Labels: map[string][]string{"tier": {"gold"}}},
		}, Obligations: []rules.Obligation{{Type: "cap_amount", Params: map[string]string{"max": "100"}}}},
		{ID: "veto-refunds", Effect: deny, When: rules.When{
			Action: &rules.ActionWhen{Name: list("refund")}, External: &rules.ExternalWhen{Denies: true},
		}},
		{ID: "boundary", Effect: deny, Reason: "ENVIRONMENT_BOUNDARY", When: rules.When{
			Principal:   &rules.PrincipalWhen{TenantID: list("tenant-b")},
			Resource:    &rules.ResourceWhen{TenantID: list("tenant-a"), Environment: list("prod")},
			Agent:       &rules.AgentWhen{ID: list("agent-7"), Framework: list("langgraph")},
			Destination: &rules.DestinationWhen{Host: list("ledger.internal")},
		}},
	}
}

// FuzzEvaluate feeds the evaluator what the kernel never sends it: envelopes
// nobody validated, enum numbers this build cannot name, labels that say
// nothing, bytes that do not parse, external answers this build does not
// declare. It may answer anything assertWellFormed allows, and nothing else,
// and it may not panic.
func FuzzEvaluate(f *testing.F) {
	doc := fuzzRules()
	p, err := match.Compile(document(doc...))
	if err != nil {
		f.Fatalf("Compile refused the fuzz program: %v", err)
	}
	order := make([]string, 0, len(doc))
	effects := make(map[string]controlv1.Verdict, len(doc))
	for _, r := range doc {
		order = append(order, r.ID)
		effects[r.ID] = r.Effect
	}
	for _, env := range []*controlv1.ActionEnvelope{
		envelope(),
		with(func(e *controlv1.ActionEnvelope) {
			e.Destination.TrustZone = zoneExternal
			e.Data.ContainsSecrets = true
		}),
		with(func(e *controlv1.ActionEnvelope) {
			e.Data.Sensitivities = []controlv1.Sensitivity{unlabelled, 6}
			e.Action.Effect = 42
			e.Destination = nil
		}),
		{},
	} {
		raw, err := proto.Marshal(env)
		if err != nil {
			f.Fatalf("marshal a seed: %v", err)
		}
		f.Add(raw, true, true, int32(secret), true, "admin", uint8(match.ExternalDenies))
		f.Add(raw, false, false, int32(unlabelled), false, "", uint8(match.ExternalUnknown))
		f.Add(raw, false, true, int32(public), false, "read", uint8(match.ExternalAllows))
	}
	f.Add([]byte{0xff, 0xff, 0xff}, true, true, int32(-1), true, "read", uint8(9))

	f.Fuzz(func(t *testing.T, raw []byte, computed, influenced bool, reading int32, delegated bool, scope string, answer uint8) {
		env := new(controlv1.ActionEnvelope)
		if proto.Unmarshal(raw, env) != nil {
			// Bytes that do not parse are answered too, as no envelope at all.
			env = nil
		}
		in := match.Inputs{Delegated: delegated, External: match.External(answer)}
		if computed {
			in.Flow = contract.NewFlowState(influenced, controlv1.Sensitivity(reading))
		}
		if scope != "" {
			in.Scopes = []string{scope}
		}
		assertWellFormed(t, order, effects, p.Evaluate(env, in))
	})
}
