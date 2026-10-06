package approvals_test

import (
	"errors"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
)

// TestTwoApproversAnsweringOneRecordFileOneAnswer: two handles, each with a
// mutex of its own, both read the record pending. The second files its answer
// after the first linked its own and before the first took the held name
// away, so only the link can tell them apart: one answer is filed, the other
// is refused, and the record carries the first.
func TestTwoApproversAnsweringOneRecordFileOneAnswer(t *testing.T) {
	_, dir, _ := heldOne(t)
	second := openApprover(t, dir)
	var secondErr error
	opt, arm := afterLink(t, func() {
		_, secondErr = second.Answer(t.Context(), firstApproval, controlv1.ApprovalState_APPROVAL_STATE_REJECTED,
			"approver-b", "too late", minted.Add(2*time.Minute))
	})
	first := openApprover(t, dir, opt)
	arm()
	_, firstErr := first.Answer(t.Context(), firstApproval, controlv1.ApprovalState_APPROVAL_STATE_APPROVED,
		"approver-a", "first", minted.Add(time.Minute))
	if firstErr != nil {
		t.Fatalf("the first answer = %v, want it filed", firstErr)
	}
	if !errors.Is(secondErr, approvals.ErrApprovalAnswered) {
		t.Fatalf("the second answer = %v, want ErrApprovalAnswered", secondErr)
	}
	assertNames(t, dir, answeredName)
	l, err := openApprover(t, dir).List(t.Context())
	if err != nil || len(l.Entries) != 1 {
		t.Fatalf("listing the record: %d entries, %v", len(l.Entries), err)
	}
	got := l.Entries[0].Record.Approval
	if got.GetState() != controlv1.ApprovalState_APPROVAL_STATE_APPROVED || got.GetApproverId() != "approver-a" ||
		got.GetReason() != "first" || !got.GetDecidedAt().AsTime().Equal(minted.Add(time.Minute)) {
		t.Errorf("the record carries %s by %q for %q at %v, want the first answer",
			got.GetState(), got.GetApproverId(), got.GetReason(), got.GetDecidedAt().AsTime())
	}
}

// TestAnAnswerDecidedAfterTheConsumeIsNotYetAnAnswer: an answer whose decided
// time is later than the clock it is spent at would put the decision after
// the action it allowed. It is no answer yet, and nothing is spent or dropped.
func TestAnAnswerDecidedAfterTheConsumeIsNotYetAnAnswer(t *testing.T) {
	decided := minted.Add(2 * time.Minute)
	t.Run("approved", func(t *testing.T) {
		p, dir, h := heldOne(t)
		answerAt(t, dir, decided)
		if _, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, decided.Add(-time.Nanosecond)); !errors.Is(err, approvals.ErrNoApproval) {
			t.Fatalf("consuming one nanosecond before the decision = %v, want ErrNoApproval", err)
		}
		assertNames(t, dir, answeredName)
		got, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, decided)
		if err != nil || got.GetState() != controlv1.ApprovalState_APPROVAL_STATE_APPROVED {
			t.Fatalf("consuming at the decision = %v, %v; want it approved", got.GetState(), err)
		}
		assertNames(t, dir, consumedName)
	})
	t.Run("rejected", func(t *testing.T) {
		p, dir, h := heldOne(t)
		if _, err := openApprover(t, dir).Answer(t.Context(), firstApproval, controlv1.ApprovalState_APPROVAL_STATE_REJECTED,
			"", "", decided); err != nil {
			t.Fatalf("rejecting: %v", err)
		}
		if _, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, decided.Add(-time.Nanosecond)); !errors.Is(err, approvals.ErrNoApproval) {
			t.Fatalf("consuming one nanosecond before the rejection = %v, want ErrNoApproval", err)
		}
		assertNames(t, dir, answeredName)
		if _, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, decided); !errors.Is(err, approvals.ErrApprovalRejected) {
			t.Fatalf("consuming at the rejection = %v, want ErrApprovalRejected", err)
		}
	})
}

// TestAnApprovalWithNoDecidedTimeIsNotSpent: a record written straight into
// the directory as approved, with no time it was decided at, cannot be shown
// to have been decided before the call it would allow.
func TestAnApprovalWithNoDecidedTimeIsNotSpent(t *testing.T) {
	p, dir, h := heldOne(t)
	forged := withApprovalField(t, readFile(t, dir, heldName), "state", `"APPROVAL_STATE_APPROVED"`)
	writeFile(t, dir, heldName, withApprovalField(t, forged, "approverId", `"approver-1"`))
	if _, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, minted.Add(time.Minute)); !errors.Is(err, approvals.ErrNoApproval) {
		t.Fatalf("consuming an approval decided at no time = %v, want ErrNoApproval", err)
	}
	assertNames(t, dir, heldName)
}
