package supervise_test

import (
	"slices"
	"testing"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	"github.com/guardana/control/internal/supervise"
)

// TestOnlyAToolCallIsAStep: the plane records a prompt or a resource read
// under a name the agent chose, on the one upstream it routes to. One named
// as the refund is no instance of it, so the refund is still skipped; it
// joins none of the runtime's refund spans, and one named as no entry is no
// call outside the procedure. A tool call at the same span is the control.
func TestOnlyAToolCallIsAStep(t *testing.T) {
	p := procWith(t)
	lookup := call{req: "r1", tool: "get_order", upstream: "shop"}
	skipped := want(p, "REQUIRED_STEP_SKIPPED", high, alert, suspected, id("REQUIRED_STEP_SKIPPED", "refund"))
	sameFindings(t, closedRun(t, p, export(lookup)).Findings, skipped)

	reported := source(ob{id: "obs-1", name: "issue_refund", span: "00000000000000a1", at: 11 * time.Second})
	for _, c := range []struct {
		kind      string
		requests  []string
		observed  []string
		want      []*findingv1alpha1.FindingRecord
		unrelated string
	}{
		{kind: "tool", requests: []string{"r2"}},
		{kind: "prompt", observed: []string{"obs-1"}, want: []*findingv1alpha1.FindingRecord{skipped}, unrelated: "wipe_disk"},
		{kind: "resource", observed: []string{"obs-1"}, want: []*findingv1alpha1.FindingRecord{skipped}, unrelated: "wipe_disk"},
	} {
		t.Run(c.kind, func(t *testing.T) {
			calls := []call{lookup, {req: "r2", kind: c.kind, tool: "issue_refund", upstream: "pay",
				at: 10 * time.Second, span: "00000000000000a1"}}
			if c.unrelated != "" {
				calls = append(calls, call{req: "r3", kind: c.kind, tool: c.unrelated, upstream: "pay", at: 20 * time.Second})
			}
			res := evaluate(t, supervise.Input{Procedure: p, Run: supervise.Run{Closed: true},
				Exports: []supervise.Export{export(calls...)}, Sources: []supervise.Source{reported}})
			sameFindings(t, res.Findings, c.want...)
			refund := res.Steps[1]
			if refund.StepID != "refund" || !slices.Equal(refund.RequestIDs, c.requests) ||
				!slices.Equal(refund.ObservationIDs, c.observed) {
				t.Fatalf("refund matched %+v", refund)
			}
		})
	}
}

// TestOnlyAToolCallCountsAsADenial: three refunds the policy denies and a
// fourth denied prompt of the same name stay under max_denials 4.
func TestOnlyAToolCallCountsAsADenial(t *testing.T) {
	p := procWith(t)
	fourth := call{req: "d4", tool: "issue_refund", upstream: "pay", at: 40 * time.Second, outcome: "deny"}
	sameFindings(t, evaluate(t, supervise.Input{Procedure: p,
		Exports: []supervise.Export{export(denials(3, fourth)...)}}).Findings,
		want(p, "REPEATED_DENIAL", high, alert, confirmed, id("REPEATED_DENIAL", "issue_refund", "pay"),
			evRef("d1-e3", "d1"), evRef("d2-e3", "d2"), evRef("d3-e3", "d3"), evRef("d4-e3", "d4")))

	fourth.kind = "prompt"
	sameFindings(t, evaluate(t, supervise.Input{Procedure: p,
		Exports: []supervise.Export{export(denials(3, fourth)...)}}).Findings)
}
