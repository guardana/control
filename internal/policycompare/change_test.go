package policycompare

import (
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
)

func decided(v controlv1.Verdict, a core.EnforcementAction, codes ...string) side {
	return side{evaluated: true, verdict: v, action: a, codes: codes}
}

var (
	execute     = decided(controlv1.Verdict_VERDICT_ALLOW, core.Execute, "RULE_ALLOW")
	obligations = side{evaluated: true, verdict: controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS,
		action: core.ExecuteWithObligations, codes: []string{"OBLIGATIONS_ATTACHED"},
		obligations: []*controlv1.Obligation{{Type: "emit_alert"}}}
	hold  = decided(controlv1.Verdict_VERDICT_REQUIRE_APPROVAL, core.AwaitApproval, "APPROVAL_REQUIRED")
	block = decided(controlv1.Verdict_VERDICT_DENY, core.Block, "RULE_DENY")
)

// Every ordered pair of the four actions, each with the change written out.
func TestClassifyEveryPairOfActions(t *testing.T) {
	sides := map[string]side{"execute": execute, "obligations": obligations, "hold": hold, "block": block}
	want := map[[2]string]Change{
		{"execute", "execute"}: Unchanged, {"execute", "obligations"}: Stricter, {"execute", "hold"}: Stricter, {"execute", "block"}: Stricter,
		{"obligations", "execute"}: Looser, {"obligations", "obligations"}: Unchanged, {"obligations", "hold"}: Stricter, {"obligations", "block"}: Stricter,
		{"hold", "execute"}: Looser, {"hold", "obligations"}: Looser, {"hold", "hold"}: Unchanged, {"hold", "block"}: Stricter,
		{"block", "execute"}: Looser, {"block", "obligations"}: Looser, {"block", "hold"}: Looser, {"block", "block"}: Unchanged,
	}
	if len(want) != 16 {
		t.Fatalf("%d pairs written, want 16", len(want))
	}
	for pair, change := range want {
		if got := classify(sides[pair[0]], sides[pair[1]]); got != change {
			t.Errorf("%s to %s: %s, want %s", pair[0], pair[1], got, change)
		}
	}
}

// The same action with anything else different is other, never unchanged.
func TestClassifySameActionOtherwiseDifferent(t *testing.T) {
	for name, c := range map[string]struct{ before, after side }{
		"another verdict": {block, decided(controlv1.Verdict_VERDICT_INDETERMINATE, core.Block, "RULE_DENY")},
		"another code":    {block, decided(controlv1.Verdict_VERDICT_DENY, core.Block, "NO_MATCHING_RULE")},
		"a code more":     {block, decided(controlv1.Verdict_VERDICT_DENY, core.Block, "RULE_DENY", "RULE_UNDETERMINED")},
		"codes reordered": {decided(controlv1.Verdict_VERDICT_DENY, core.Block, "A", "B"), decided(controlv1.Verdict_VERDICT_DENY, core.Block, "B", "A")},
		"an obligation added": {hold, side{evaluated: true, verdict: hold.verdict, action: hold.action, codes: hold.codes,
			obligations: []*controlv1.Obligation{{Type: "second_approver"}}}},
		"another obligation param": {obligations, side{evaluated: true, verdict: obligations.verdict, action: obligations.action,
			codes: obligations.codes, obligations: []*controlv1.Obligation{{Type: "emit_alert", Params: map[string]string{"to": "x"}}}}},
		"an obligation advisory": {obligations, side{evaluated: true, verdict: obligations.verdict, action: obligations.action,
			codes: obligations.codes, obligations: []*controlv1.Obligation{{Type: "emit_alert", Advisory: true}}}},
	} {
		if got := classify(c.before, c.after); got != Other {
			t.Errorf("%s: %s, want other", name, got)
		}
	}
	same := side{evaluated: true, verdict: obligations.verdict, action: obligations.action, codes: []string{"OBLIGATIONS_ATTACHED"},
		obligations: []*controlv1.Obligation{{Type: "emit_alert"}}}
	if got := classify(obligations, same); got != Unchanged {
		t.Errorf("equal decisions built apart: %s, want unchanged", got)
	}
}

// A side that was not evaluated makes the case unknown whatever the other
// side decided, and so does an action this build does not name.
func TestClassifyUnevaluatedIsUnknown(t *testing.T) {
	for name, c := range map[string]struct{ before, after side }{
		"before not evaluated": {side{}, block},
		"after not evaluated":  {block, side{}},
		"neither":              {side{}, side{}},
		"an unnamed action":    {block, side{evaluated: true, verdict: block.verdict, action: core.EnforcementAction(9), codes: block.codes}},
	} {
		if got := classify(c.before, c.after); got != Unknown {
			t.Errorf("%s: %s, want unknown", name, got)
		}
	}
}
