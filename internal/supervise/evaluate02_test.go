package supervise_test

import (
	"slices"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/supervise"
)

// TestA02ProcedureIsJudgedByEveryRuleItConfigures: Evaluate takes a 0.2
// procedure, and a closed tree that keeps to it, with every binding called
// by its id and a source heard, checks each of the eleven rules and finds
// nothing.
func TestA02ProcedureIsJudgedByEveryRuleItConfigures(t *testing.T) {
	customer := &controlv1.Resource{Type: "crm_customer", Id: "c1", TenantId: tenant, Environment: "prod"}
	x := acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("42")).in(childRun),
		act{call: call{req: "r3", tool: "send_mail", upstream: "mail", at: 20 * time.Second}, res: customer})
	tree := closedFamily()
	res, err := supervise.Evaluate(supervise.Input{Procedure: inheritProc(t), Run: tree[0], Tree: tree,
		Exports: []supervise.Export{x}, Sources: []supervise.Source{source()}})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(res.Findings) != 0 || res.Report.GetSchemaVersion() != "0.2" {
		t.Fatalf("report %s with findings:\n%s", res.Report.GetSchemaVersion(), dump(res.Findings))
	}
	var judged []string
	for _, r := range res.Report.GetRules() {
		if r.GetRuleVersion() != ruleVersions[r.GetRuleId()] || r.GetState().String()+":"+r.GetWhy() != checked {
			t.Errorf("%s at version %q is %s: %s", r.GetRuleId(), r.GetRuleVersion(), r.GetState(), r.GetWhy())
		}
		judged = append(judged, r.GetRuleId())
	}
	if want := []string{"REPEATED_DENIAL", "STEP_OUTSIDE_PROCEDURE", "DEADLINE_EXCEEDED", "REQUIRED_STEP_SKIPPED",
		"STEP_OUT_OF_ORDER", "CONTINUED_AFTER_FAILURE", "RESOURCE_OUTSIDE_RUN", "DENIED_ACTION_RETRIED_ARGUMENTS",
		"DENIED_ACTION_RETRIED_RESOURCE", "DENIED_ACTION_RETRIED_AROUND", "EXCEPTION_TAKEN"}; !slices.Equal(judged, want) {
		t.Fatalf("the report lists %q, want %q", judged, want)
	}
}
