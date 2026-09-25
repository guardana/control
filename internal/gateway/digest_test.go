package gateway_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/gateway"
)

// actionDigest is canon's digest of env with args, the reference the
// pipeline's records are held to.
func actionDigest(t *testing.T, env *controlv1.ActionEnvelope, args string) string {
	t.Helper()
	d, err := canon.DigestV1(env, []byte(args))
	if err != nil {
		t.Fatalf("DigestV1: %v", err)
	}
	return d
}

// cappedArgs is refundArgs after approveRefunds' cap_amount.
const cappedArgs = `{"amount":1000,"currency":"EUR","ssn":"x"}`

// TestARewrittenCallIsDecidedBoundAndClosedAsTheAuthorizedAction: a cap under
// REQUIRE_APPROVAL changes the bytes, so POLICY_DECIDED, APPROVAL_REQUESTED,
// the pending state, the resumed execution and the closing record all name
// the digest of the capped call, never the one the agent proposed.
func TestARewrittenCallIsDecidedBoundAndClosedAsTheAuthorizedAction(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds))
	authorized := actionDigest(t, refundEnvelope(t, refundArgs()), cappedArgs)
	if proposed := actionDigest(t, refundEnvelope(t, refundArgs()), string(refundArgs())); proposed == authorized {
		t.Fatalf("the cap does not change the digest; this test examines nothing")
	}
	first := hold(t, h)
	expectHeldAs(t, h, first, authorized)
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	run := h.admit(retry(t, "req-2"), refundArgs())
	if run.Action != core.Execute || run.Decision.GetActionDigest() != authorized {
		t.Fatalf("the approved retry: Action = %d, decision digest %s", run.Action, run.Decision.GetActionDigest())
	}
	if held := h.events()[1].GetDecision().GetDecisionId(); run.Decision.GetDecisionId() != held {
		t.Errorf("the resumed execution carries decision %s, want the one on the held trail, %s", run.Decision.GetDecisionId(), held)
	}
	if err := h.p.Close(context.Background(), run, []byte(cappedArgs), result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS)); err != nil {
		t.Fatalf("Close: %v", err)
	}
	expectClosedOnHeldTrail(t, h, run, authorized)
}

// expectHeldAs asserts the hold's records and pending state name authorized,
// and ACTION_PROPOSED what the agent proposed.
func expectHeldAs(t *testing.T, h *harness, first gateway.Disposition, authorized string) {
	t.Helper()
	events := h.events()
	if got := events[1].GetDecision().GetActionDigest(); got != authorized {
		t.Errorf("POLICY_DECIDED names %s, want the authorized %s", got, authorized)
	}
	if got := events[2].GetApproval().GetActionDigest(); got != authorized {
		t.Errorf("APPROVAL_REQUESTED names %s, want %s", got, authorized)
	}
	if first.Pending.ActionDigest != authorized || first.Decision.GetActionDigest() != authorized {
		t.Errorf("the pending state names %s and its decision %s, want %s", first.Pending.ActionDigest, first.Decision.GetActionDigest(), authorized)
	}
	if got := events[0].GetProposed().GetArguments().GetCanonicalHash(); got != argumentsHash(t, refundArgs()) {
		t.Errorf("ACTION_PROPOSED carries arguments hash %s; it records what was proposed", got)
	}
}

// expectClosedOnHeldTrail asserts the held trail closes run with authorized
// as the executed digest, under the held request's id and run's execution.
func expectClosedOnHeldTrail(t *testing.T, h *harness, run gateway.Disposition, authorized string) {
	t.Helper()
	trail := h.trailOf("req-1")
	closing := trail[len(trail)-1]
	if closing.GetKind() != kindCompleted || closing.GetResult().GetExecutedActionDigest() != authorized {
		t.Errorf("the closing record is %s with %s, want COMPLETED with %s", closing.GetKind(), closing.GetResult().GetExecutedActionDigest(), authorized)
	}
	if closing.GetResult().GetRequestId() != "req-1" || closing.GetResult().GetExecutionId() != run.ExecutionID || run.ExecutionID == "" {
		t.Errorf("the closing record names request %q and execution %q; want the held req-1 and %q",
			closing.GetResult().GetRequestId(), closing.GetResult().GetExecutionId(), run.ExecutionID)
	}
	if err := evidence.ValidateChain(trail); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
}

// TestARewriteUnderAllowIsDecidedAgainAndClosesClean: the recorded decision
// is the one about the redacted and capped call, and sending exactly those
// bytes closes without a mismatch.
func TestARewriteUnderAllowIsDecidedAgainAndClosesClean(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, cappedRefunds))
	d := h.admit(refundEnvelope(t, refundArgs()), refundArgs())
	sent := `{"amount":1000,"currency":"EUR"}`
	authorized := actionDigest(t, refundEnvelope(t, refundArgs()), sent)
	if d.Action != core.ExecuteWithObligations || d.Decision.GetActionDigest() != authorized {
		t.Fatalf("Action = %d, decision digest %s; want ExecuteWithObligations about %s", d.Action, d.Decision.GetActionDigest(), authorized)
	}
	if got := h.events()[1].GetDecision().GetActionDigest(); got != authorized {
		t.Errorf("POLICY_DECIDED names %s, want %s", got, authorized)
	}
	if err := h.p.Close(context.Background(), d, []byte(sent), result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS)); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if s := h.p.Stats(); s.Halted || s.Mismatches != 0 {
		t.Errorf("Stats = %+v", s)
	}
}

// steppingClock reads base for its first n readings and later after that,
// so the kernel's second decision about one call sees another time than its
// first.
func steppingClock(n int64, later time.Time) func() time.Time {
	var reads atomic.Int64
	return func() time.Time {
		if reads.Add(1) > n {
			return later
		}
		return base()
	}
}

// Readings of one Admit before the second decision: the call's own, and the
// first decision's entry and latency.
const readsBeforeSecondDecision = 3

// TestASecondDecisionThatDiffersIsEnforcedAsItIs: the clock crosses the
// staleness budget between the two decisions, and the stale one is what is
// recorded and enforced.
func TestASecondDecisionThatDiffersIsEnforcedAsItIs(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, cappedRefunds), func(c *gateway.Config) {
		c.Clock = steppingClock(readsBeforeSecondDecision, base().Add(11*time.Minute))
	})
	d := h.admit(refundEnvelope(t, refundArgs()), refundArgs())
	expectBlock(t, d, verdictIndeterminate, "POLICY_STALE", "builtin")
	events := h.events()
	expectKinds(t, kindsOf(events), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
	decided := events[1].GetDecision()
	if decided.GetVerdict() != verdictIndeterminate || decided.GetActionDigest() != actionDigest(t, refundEnvelope(t, refundArgs()), `{"amount":1000,"currency":"EUR"}`) {
		t.Errorf("POLICY_DECIDED = %+v; want the second, stale decision about the rewritten call", decided)
	}
}
