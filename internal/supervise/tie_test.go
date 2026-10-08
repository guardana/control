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

// TestATieInOneExportIsUntold: a plane ships evidence several batches at a
// time, so an export's place tells no order either. Lookup r1 and refund r2,
// both proposed at 10 s in one export, leave the order untold, listed in
// either order.
func TestATieInOneExportIsUntold(t *testing.T) {
	p := procWith(t)
	lookup := call{req: "r1", tool: "get_order", upstream: "shop", at: 10 * time.Second}
	refund := call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second}
	untold := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
		evRef("r2-e1", "r2"), evRef("r1-e1", "r1"))
	sameFindings(t, closedRun(t, p, export(lookup, refund)).Findings, untold)
	sameFindings(t, closedRun(t, p, export(refund, lookup)).Findings, untold)
}

// TestDistinctTimesInOneExportTellTheOrder: listed against the order of their
// times, the times decide. A refund listed before a lookup proposed a second
// earlier is in order; one listed after a lookup proposed a second later is
// not.
func TestDistinctTimesInOneExportTellTheOrder(t *testing.T) {
	p := procWith(t)
	sameFindings(t, closedRun(t, p, export(
		call{req: "r2", tool: "issue_refund", upstream: "pay", at: 11 * time.Second},
		call{req: "r1", tool: "get_order", upstream: "shop", at: 10 * time.Second})).Findings)
	sameFindings(t, closedRun(t, p, export(
		call{req: "r1", tool: "get_order", upstream: "shop", at: 11 * time.Second},
		call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second})).Findings,
		want(p, "STEP_OUT_OF_ORDER", medium, inform, suspected, id("STEP_OUT_OF_ORDER", "refund"),
			evRef("r2-e1", "r2"), evRef("r1-e1", "r1")))
}

// TestAFirstInstanceTiedInOneExportIsCitedByRequest: lookups r5 and r1 and a
// refund r2, all at 10 s in one export, leave the order untold, and the
// finding cites the lookup of the least request id, listed in either order.
func TestAFirstInstanceTiedInOneExportIsCitedByRequest(t *testing.T) {
	p := procWith(t)
	r5 := call{req: "r5", tool: "get_order", upstream: "shop", at: 10 * time.Second}
	r1 := call{req: "r1", tool: "get_order", upstream: "shop", at: 10 * time.Second}
	refund := call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second}
	untold := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
		evRef("r2-e1", "r2"), evRef("r1-e1", "r1"))
	sameFindings(t, closedRun(t, p, export(r5, r1, refund)).Findings, untold)
	sameFindings(t, closedRun(t, p, export(refund, r1, r5)).Findings, untold)
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
}

// TestARetryTiedInOneExportWithTheNextStepIsUntold: lookup r1 fails at 3 s; a
// retry r5 and a refund r2 are both proposed at 10 s in one export. Whether
// the retry came first is untold, so the refund may continue after the
// failure, listed in either order. With distinct times listed against their
// order, the times decide.
func TestARetryTiedInOneExportWithTheNextStepIsUntold(t *testing.T) {
	p := procWith(t)
	fail := call{req: "r1", tool: "get_order", upstream: "shop", outcome: "fail"}
	retry := call{req: "r5", tool: "get_order", upstream: "shop", at: 10 * time.Second}
	refund := call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second}
	untold := want(p, "CONTINUED_AFTER_FAILURE", critical, alert, indetermin, id("CONTINUED_AFTER_FAILURE", "r1"),
		evRef("r1-e4", "r1"), evRef("r2-e1", "r2"))
	sameFindings(t, closedRun(t, p, export(fail, retry, refund)).Findings, untold)
	sameFindings(t, closedRun(t, p, export(fail, refund, retry)).Findings, untold)

	later := refund
	later.at = 11 * time.Second
	sameFindings(t, closedRun(t, p, export(fail, later, retry)).Findings)
	retry.at = 12 * time.Second
	sameFindings(t, closedRun(t, p, export(fail, retry, later)).Findings,
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

// TestAFailureWithoutAnEndTimeTiedInOneExportMayBeContinued: lookup r1 is
// proposed at 10 s and fails at no known time; a refund r2 in the same
// export is proposed at 10 s too. Listed before the lookup or after it, the
// refund may have followed the failure, and its order against the lookup is
// untold.
func TestAFailureWithoutAnEndTimeTiedInOneExportMayBeContinued(t *testing.T) {
	p := procWith(t)
	fail := call{req: "r1", tool: "get_order", upstream: "shop", outcome: "fail", at: 10 * time.Second}
	refund := call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second}
	order := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
		evRef("r2-e1", "r2"), evRef("r1-e1", "r1"))
	continued := want(p, "CONTINUED_AFTER_FAILURE", critical, alert, indetermin, id("CONTINUED_AFTER_FAILURE", "r1"),
		evRef("r1-e4", "r1"), evRef("r2-e1", "r2"))
	sameFindings(t, closedRun(t, p, untimed(export(fail, refund), "r1", 4)).Findings, order, continued)
	sameFindings(t, closedRun(t, p, untimed(export(refund, fail), "r1", 4)).Findings, order, continued)
}

// TestAFirstInstanceTiedWithItsOwnStepIsUntold: refunds r3 and r2 share the
// earliest refund time in one export, so which of them is the step's first is
// untold, and with it the order, though the lookup came earlier.
func TestAFirstInstanceTiedWithItsOwnStepIsUntold(t *testing.T) {
	p := procWith(t)
	lookup := call{req: "r1", tool: "get_order", upstream: "shop", at: 10 * time.Second}
	r3 := call{req: "r3", tool: "issue_refund", upstream: "pay", at: 20 * time.Second}
	r2 := call{req: "r2", tool: "issue_refund", upstream: "pay", at: 20 * time.Second}
	untold := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
		evRef("r2-e1", "r2"), evRef("r1-e1", "r1"))
	sameFindings(t, closedRun(t, p, export(lookup, r3, r2)).Findings, untold)
	sameFindings(t, closedRun(t, p, export(r2, lookup, r3)).Findings, untold)

	r2.at = 21 * time.Second
	sameFindings(t, closedRun(t, p, export(r2, r3, lookup)).Findings)
}

// TestTiedNextStepsAreCitedByRequest: lookup r1 fails at 3 s; a refund r3 and
// a notify r2 are both proposed at 10 s in one export. The finding cites the
// one of the least request id, listed in either order.
func TestTiedNextStepsAreCitedByRequest(t *testing.T) {
	p := procWith(t)
	fail := call{req: "r1", tool: "get_order", upstream: "shop", outcome: "fail"}
	refund := call{req: "r3", tool: "issue_refund", upstream: "pay", at: 10 * time.Second}
	notify := call{req: "r2", tool: "send_mail", upstream: "mail", at: 10 * time.Second}
	order := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "notify"),
		evRef("r2-e1", "r2"), evRef("r3-e1", "r3"))
	continued := want(p, "CONTINUED_AFTER_FAILURE", critical, alert, suspected, id("CONTINUED_AFTER_FAILURE", "r1"),
		evRef("r1-e4", "r1"), evRef("r2-e1", "r2"))
	sameFindings(t, closedRun(t, p, export(fail, refund, notify)).Findings, order, continued)
	sameFindings(t, closedRun(t, p, export(notify, refund, fail)).Findings, order, continued)
}
