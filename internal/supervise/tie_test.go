package supervise_test

import (
	"testing"
	"time"
)

// TestATieAcrossExportsIsUntold: two planes' clocks tell no order within one
// instant. Lookup in one export and a refund in another, both proposed at
// 10 s, leave the order untold, read in either order.
func TestATieAcrossExportsIsUntold(t *testing.T) {
	p := procWith(t)
	lookup := export(call{req: "r1", tool: "get_order", upstream: "shop", at: 10 * time.Second})
	refund := export(call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second})
	untold := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
		evRef("r2-e1", "r2"), evRef("r1-e1", "r1"))
	sameFindings(t, closedRun(t, p, lookup, refund).Findings, untold)
	sameFindings(t, closedRun(t, p, refund, lookup).Findings, untold)
}

// TestATieInOneExportKeepsItsAppendOrder: within one export the plane's
// append order tells two proposals of one instant apart.
func TestATieInOneExportKeepsItsAppendOrder(t *testing.T) {
	p := procWith(t)
	lookup := call{req: "r1", tool: "get_order", upstream: "shop", at: 10 * time.Second}
	refund := call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second}
	sameFindings(t, closedRun(t, p, export(lookup, refund)).Findings)
	sameFindings(t, closedRun(t, p, export(refund, lookup)).Findings,
		want(p, "STEP_OUT_OF_ORDER", medium, inform, suspected, id("STEP_OUT_OF_ORDER", "refund"),
			evRef("r2-e1", "r2"), evRef("r1-e1", "r1")))
}

// TestAFirstInstanceTiedAcrossExportsIsUntold: one export holds a refund r2
// and then a lookup r1, both at 10 s; another a lookup r5 at 10 s. Which
// lookup came first, and so whether the refund followed one, is untold, read
// in either order; the finding cites the lookup of the least request id.
func TestAFirstInstanceTiedAcrossExportsIsUntold(t *testing.T) {
	p := procWith(t)
	mine := export(call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second},
		call{req: "r1", tool: "get_order", upstream: "shop", at: 10 * time.Second})
	theirs := export(call{req: "r5", tool: "get_order", upstream: "shop", at: 10 * time.Second})
	untold := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
		evRef("r2-e1", "r2"), evRef("r1-e1", "r1"))
	sameFindings(t, closedRun(t, p, mine, theirs).Findings, untold)
	sameFindings(t, closedRun(t, p, theirs, mine).Findings, untold)
}

// TestARetryTiedAcrossExportsWithTheNextStepIsUntold: lookup r1 fails at
// 3 s; a retry r5 and a refund r2 are both proposed at 10 s. In one export
// the append order says whether the retry came first; across two it is
// untold, so the refund may continue after the failure, read in either order.
func TestARetryTiedAcrossExportsWithTheNextStepIsUntold(t *testing.T) {
	p := procWith(t)
	fail := call{req: "r1", tool: "get_order", upstream: "shop", outcome: "fail"}
	retry := call{req: "r5", tool: "get_order", upstream: "shop", at: 10 * time.Second}
	refund := call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second}
	untold := want(p, "CONTINUED_AFTER_FAILURE", critical, alert, indetermin, id("CONTINUED_AFTER_FAILURE", "r1"),
		evRef("r1-e4", "r1"), evRef("r2-e1", "r2"))
	sameFindings(t, closedRun(t, p, export(fail, retry), export(refund)).Findings, untold)
	sameFindings(t, closedRun(t, p, export(refund), export(fail, retry)).Findings, untold)

	sameFindings(t, closedRun(t, p, export(fail, retry, refund)).Findings)
	sameFindings(t, closedRun(t, p, export(fail, refund, retry)).Findings,
		want(p, "CONTINUED_AFTER_FAILURE", critical, alert, suspected, id("CONTINUED_AFTER_FAILURE", "r1"),
			evRef("r1-e4", "r1"), evRef("r2-e1", "r2")))
}

// TestAFailureWithoutAnEndTimeTiedAcrossExportsMayBeContinued: lookup r1 is
// proposed at 10 s and fails at no known time; a refund r2 in another export
// is proposed at 10 s too. It may have followed the failure, read in either
// order, and its order against the lookup is untold.
func TestAFailureWithoutAnEndTimeTiedAcrossExportsMayBeContinued(t *testing.T) {
	p := procWith(t)
	fail := untimed(export(call{req: "r1", tool: "get_order", upstream: "shop", outcome: "fail", at: 10 * time.Second}), "r1", 4)
	refund := export(call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second})
	order := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
		evRef("r2-e1", "r2"), evRef("r1-e1", "r1"))
	continued := want(p, "CONTINUED_AFTER_FAILURE", critical, alert, indetermin, id("CONTINUED_AFTER_FAILURE", "r1"),
		evRef("r1-e4", "r1"), evRef("r2-e1", "r2"))
	sameFindings(t, closedRun(t, p, fail, refund).Findings, order, continued)
	sameFindings(t, closedRun(t, p, refund, fail).Findings, order, continued)
}
