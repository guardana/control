package gateway_test

import (
	"testing"

	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/gateway"
)

// TestAnUnjudgedFileHoldsTheCallUntilARejection: alice's approval beside a
// file the authority could not judge leaves the call INDETERMINATE, the hold
// standing and the cause counted, at every retry; carol's rejection filed
// then wins over both.
func TestAnUnjudgedFileHoldsTheCallUntilARejection(t *testing.T) {
	h, box, requested := signedRig(t)
	box.file(requested.GetApprovalId(), signedBy(t, "alice", answerTo(requested, approvals.DecisionApprove, nil)))
	box.setUnjudged()
	for _, id := range []string{"req-2", "req-3"} {
		d := h.admit(retry(t, id), refundArgs())
		expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	}
	if trail := h.trailOf("req-1"); len(trail) != 3 {
		t.Errorf("the held trail holds %d events; the hold should stand", len(trail))
	}
	if h.spent() != 0 {
		t.Errorf("the ledger was asked %d spend(s)", h.spent())
	}
	if got := h.p.Stats().Answers[gateway.AnswerBatchUnjudged]; got != 2 {
		t.Errorf("Stats().Answers[batch_unjudged] = %d, want one per retry", got)
	}
	box.file(requested.GetApprovalId(), signedBy(t, "carol", answerTo(requested, approvals.DecisionReject, nil)))
	expectBlock(t, h.admit(retry(t, "req-4"), refundArgs()), verdictDeny, codeApprovalRejected, gateway.PDPType)
}

// TestAnAnswerThatDoesNotVerifyIsNeverNoRejection: a rejection in carol's
// name another key signed may stand where carol's own was refused, so beside
// alice's approval it holds the call INDETERMINATE and is counted
// unauthenticated; alice's approval alone runs.
func TestAnAnswerThatDoesNotVerifyIsNeverNoRejection(t *testing.T) {
	h, box, requested := signedRig(t)
	box.file(requested.GetApprovalId(), signedBy(t, "alice", answerTo(requested, approvals.DecisionApprove, nil)))
	forged := signedWith(t, operatorKey("mallory"), operatorID("carol"), answerTo(requested, approvals.DecisionReject, nil))
	box.file(requested.GetApprovalId(), forged)
	expectBlock(t, h.admit(retry(t, "req-2"), refundArgs()), verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	if got := h.p.Stats().Answers[gateway.AnswerUnauthenticated]; got != 1 {
		t.Errorf("Stats().Answers[unauthenticated] = %d", got)
	}
	h, box, requested = signedRig(t)
	box.file(requested.GetApprovalId(), signedBy(t, "alice", answerTo(requested, approvals.DecisionApprove, nil)))
	if d := h.admit(retry(t, "req-2"), refundArgs()); d.Action != core.Execute {
		t.Fatalf("alice's approval alone: Action = %d, %v", d.Action, d.Decision.GetReasonCodes())
	}
}

// TestEveryAnswerCauseIsListed: the batch cause an unjudged file is counted
// under is one the health page and doctor list.
func TestEveryAnswerCauseIsListed(t *testing.T) {
	listed := map[string]bool{}
	for _, c := range gateway.AnswerCauses() {
		listed[c] = true
	}
	for _, c := range []string{gateway.AnswerBatchUnavailable, gateway.AnswerBatchTruncated, gateway.AnswerBatchUnjudged, gateway.AnswerUnauthenticated} {
		if !listed[c] {
			t.Errorf("AnswerCauses lacks %s", c)
		}
	}
}
