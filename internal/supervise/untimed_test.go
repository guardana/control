package supervise_test

import (
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/supervise"
)

// TestAnUntimedFirstInstanceLeavesTheOrderUntold: a step with an instance
// that has no time may have run before the step it follows, whatever another
// instance's time says, and so may that step's first, whichever was read
// first.
func TestAnUntimedFirstInstanceLeavesTheOrderUntold(t *testing.T) {
	p := procWith(t)
	lookup := call{req: "r1", tool: "get_order", upstream: "shop", at: 10 * time.Second}
	refundUntold := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
		evRef("r2-e1", "r2"), evRef("r1-e1", "r1"))
	sameFindings(t, closedRun(t, p, untimed(export(lookup,
		call{req: "r2", tool: "issue_refund", upstream: "pay"},
		call{req: "r3", tool: "issue_refund", upstream: "pay", at: 20 * time.Second}), "r2", 0)).Findings, refundUntold)

	sameFindings(t, closedRun(t, p, untimed(export(
		call{req: "r1", tool: "get_order", upstream: "shop"},
		call{req: "r5", tool: "get_order", upstream: "shop", at: 5 * time.Second},
		call{req: "r2", tool: "issue_refund", upstream: "pay", at: 20 * time.Second}), "r1", 0)).Findings, refundUntold)

	sameFindings(t, closedRun(t, p, untimed(export(lookup,
		call{req: "r2", tool: "issue_refund", upstream: "pay", at: 20 * time.Second},
		call{req: "r3", tool: "issue_refund", upstream: "pay"}), "r3", 0)).Findings,
		want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
			evRef("r3-e1", "r3"), evRef("r1-e1", "r1")))
}

// TestTheOrderDoesNotDependOnTheOrderOfTheExports: one export holds lookup
// at 10 s and a refund at 20 s, the other a call with no time that may have
// run before either. Read in either order, the order is untold, and of two
// refunds with no time the finding cites the same one.
func TestTheOrderDoesNotDependOnTheOrderOfTheExports(t *testing.T) {
	p := procWith(t)
	timed := export(call{req: "r1", tool: "get_order", upstream: "shop", at: 10 * time.Second},
		call{req: "r2", tool: "issue_refund", upstream: "pay", at: 20 * time.Second})
	for name, c := range map[string]struct {
		other   call
		refRefs []string
	}{
		"a refund with no time": {call{req: "r3", tool: "issue_refund", upstream: "pay"}, []string{"r3", "r1"}},
		"a lookup with no time": {call{req: "r5", tool: "get_order", upstream: "shop"}, []string{"r2", "r5"}},
	} {
		t.Run(name, func(t *testing.T) {
			other := untimed(export(c.other), c.other.req, 0)
			untold := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
				evRef(c.refRefs[0]+"-e1", c.refRefs[0]), evRef(c.refRefs[1]+"-e1", c.refRefs[1]))
			sameFindings(t, closedRun(t, p, timed, other).Findings, untold)
			sameFindings(t, closedRun(t, p, other, timed).Findings, untold)
		})
	}

	mine := untimed(export(call{req: "r1", tool: "get_order", upstream: "shop", at: 10 * time.Second},
		call{req: "r4", tool: "issue_refund", upstream: "pay"}), "r4", 0)
	theirs := untimed(export(call{req: "r3", tool: "issue_refund", upstream: "pay"}), "r3", 0)
	untold := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
		evRef("r3-e1", "r3"), evRef("r1-e1", "r1"))
	sameFindings(t, closedRun(t, p, mine, theirs).Findings, untold)
	sameFindings(t, closedRun(t, p, theirs, mine).Findings, untold)
}

// TestATimedFailureSkipsAnExcusedStep: r1 fails at 3 s; mail r4 at 5 s,
// before the refund it must follow, is out of order and so no continuation,
// and the refund r2 at 10 s continues after the failure, as it does when the
// failure has no time.
func TestATimedFailureSkipsAnExcusedStep(t *testing.T) {
	p := procWith(t)
	res := closedRun(t, p, export(
		call{req: "r1", tool: "get_order", upstream: "shop", outcome: "fail"},
		call{req: "r4", tool: "send_mail", upstream: "mail", at: 5 * time.Second},
		call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second}))
	sameFindings(t, res.Findings,
		want(p, "STEP_OUT_OF_ORDER", medium, inform, suspected, id("STEP_OUT_OF_ORDER", "notify"),
			evRef("r4-e1", "r4"), evRef("r2-e1", "r2")),
		want(p, "CONTINUED_AFTER_FAILURE", critical, alert, suspected, id("CONTINUED_AFTER_FAILURE", "r1"),
			evRef("r1-e4", "r1"), evRef("r2-e1", "r2")))
}

// TestAFailureIsPlacedByItsOwnTime: get_order r1 fails at 3 s and a refund
// r2 is proposed at 10 s. A failure with a time counts whatever its proposal
// time; a next step whose proposal time, or a failure whose every time, is
// unknown leaves the pair indeterminate, never a pass. Lookup or refund then
// has a first instance with no time, so the order is untold as well.
func TestAFailureIsPlacedByItsOwnTime(t *testing.T) {
	p := procWith(t)
	fail := call{req: "r1", tool: "get_order", upstream: "shop", outcome: "fail"}
	refund := call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second}
	retry := call{req: "r5", tool: "get_order", upstream: "shop", at: 5 * time.Second}
	order := want(p, "STEP_OUT_OF_ORDER", medium, inform, indetermin, id("STEP_OUT_OF_ORDER", "refund"),
		evRef("r2-e1", "r2"), evRef("r1-e1", "r1"))
	for name, c := range map[string]struct {
		x supervise.Export
		v controlv1.FindingVerdict
	}{
		"a failure at 3 s proposed with no time":   {untimed(export(fail, refund), "r1", 1), suspected},
		"a next step proposed with no time":        {untimed(export(fail, refund), "r2", 1), indetermin},
		"a step with no time beside a timed retry": {untimed(export(fail, retry, refund), "r2", 0), indetermin},
		"a failure with no time at all":            {untimed(export(fail, refund), "r1", 0), indetermin},
		"a failure with no time before a retry":    {untimed(export(fail, retry, refund), "r1", 0), indetermin},
	} {
		t.Run(name, func(t *testing.T) {
			continued := want(p, "CONTINUED_AFTER_FAILURE", critical, alert, c.v, id("CONTINUED_AFTER_FAILURE", "r1"),
				evRef("r1-e4", "r1"), evRef("r2-e1", "r2"))
			sameFindings(t, closedRun(t, p, c.x).Findings, order, continued)
		})
	}
}

// TestAnUnplacedFailureSkipsAnExcusedStep: r1 fails at no known time; mail
// r4 at 5 s, before the refund it must follow, is reported out of order and
// so is no continuation, and the refund r2 at 10 s may be one.
func TestAnUnplacedFailureSkipsAnExcusedStep(t *testing.T) {
	p := procWith(t)
	res := closedRun(t, p, untimed(export(
		call{req: "r1", tool: "get_order", upstream: "shop", outcome: "fail"},
		call{req: "r4", tool: "send_mail", upstream: "mail", at: 5 * time.Second},
		call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second}), "r1", 4))
	sameFindings(t, res.Findings,
		want(p, "STEP_OUT_OF_ORDER", medium, inform, suspected, id("STEP_OUT_OF_ORDER", "notify"),
			evRef("r4-e1", "r4"), evRef("r2-e1", "r2")),
		want(p, "CONTINUED_AFTER_FAILURE", critical, alert, indetermin, id("CONTINUED_AFTER_FAILURE", "r1"),
			evRef("r1-e4", "r1"), evRef("r2-e1", "r2")))
}

// TestAFailureProposedAfterItEndedIsNoRetryOfItself: r1's proposal says 5 s
// and its failure 3 s; the refund r2 at 10 s still continues after it.
func TestAFailureProposedAfterItEndedIsNoRetryOfItself(t *testing.T) {
	p := procWith(t)
	x := export(call{req: "r1", tool: "get_order", upstream: "shop", outcome: "fail"},
		call{req: "r2", tool: "issue_refund", upstream: "pay", at: 10 * time.Second})
	x.Events[0].OccurredAt = at(5 * time.Second)
	sameFindings(t, closedRun(t, p, x).Findings,
		want(p, "CONTINUED_AFTER_FAILURE", critical, alert, suspected, id("CONTINUED_AFTER_FAILURE", "r1"),
			evRef("r1-e4", "r1"), evRef("r2-e1", "r2")))
}
