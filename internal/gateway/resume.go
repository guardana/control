package gateway

import (
	"errors"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/core/approval"
	"github.com/guardana/control/internal/evidence"
)

// resume is a retry of one of matches, oldest first. The fresh decision is a
// gate: a held request resumes only if this call's recorded decision is the
// one on its trail in verdict, rules, obligations and digest, because the
// trail cannot take a second POLICY_DECIDED. The first held request with an
// approval runs; one whose approval expired or was refused is closed on its
// trail; with none approved the agent is told about the oldest still pending.
func (c *call) resume(matches []*heldRequest, binding approval.Binding) Disposition {
	var r resumption
	for _, own := range matches {
		if !sameDecision(own.decision, c.kernel) {
			continue
		}
		if d, done := c.try(own, binding, &r); done {
			return d
		}
	}
	switch {
	case r.waiting != nil:
		return c.pending(r.waiting.approval, r.waiting.decision)
	case r.concluded != nil:
		return *r.concluded
	case r.consumed:
		// The held request's approval was spent and its trail is closed;
		// this retry is a request of its own, refused in full on its own
		// trail.
		c.decide(verdictDeny, codeApprovalAlreadyUsed)
		return c.freshBlock()
	}
	// Every match was held under another decision than this call's, which its
	// trail cannot record, so this call is a request of its own and is held
	// anew: the agent is told to retry that one (ADR-0013).
	return c.hold(binding)
}

// resumption is what a resume found so far among the held requests it tried.
type resumption struct {
	waiting   *heldRequest
	concluded *Disposition
	consumed  bool
}

// try consumes the approval of the held request and reports the answer when it
// ends the resume: the execution of an approved one, or a block for a store
// that cannot answer. Anything else is noted in r and the resume goes on.
//
// The approval id the plane minted goes with the request id, so a record some
// other writer filed under the same binding is never the one spent (ADR-0016).
func (c *call) try(own *heldRequest, binding approval.Binding, r *resumption) (Disposition, bool) {
	if c.pausedNow() {
		// Blocked on this call's own trail with nothing consumed, so the hold
		// stands for a retry after the lift.
		return c.freshBlock(), true
	}
	a, err := c.p.cfg.Approvals.Consume(c.ctx, binding,
		own.approval.GetRequestId(), own.approval.GetApprovalId(), c.now)
	var d Disposition
	switch {
	case err == nil:
		return c.resumeApproved(own, a), true
	case errors.Is(err, ErrNoApproval):
		r.waiting = firstOf(r.waiting, own)
		return d, false
	case errors.Is(err, ErrMultiUse):
		c.p.counts.add(func(s *Stats) *uint64 { return &s.MultiUseRefused })
		r.waiting = firstOf(r.waiting, own)
		return d, false
	case errors.Is(err, ErrApprovalConsumed):
		r.consumed = true
		return d, false
	case errors.Is(err, ErrApprovalExpired):
		d = c.resumeBlocked(own, verdictDeny, codeApprovalExpired, expiredEvent(own.approval))
	case errors.Is(err, ErrApprovalRejected):
		answer := a
		if answer == nil {
			answer = own.approval
		}
		d = c.resumeBlocked(own, verdictDeny, codeApprovalRejected, decidedEvent(answer))
	default:
		c.decide(verdictIndeterminate, codeEvidenceUnavailable)
		return c.freshBlock(), true
	}
	if r.concluded == nil {
		r.concluded = &d
	}
	return d, false
}

func firstOf(first, next *heldRequest) *heldRequest {
	if first != nil {
		return first
	}
	return next
}

// sameDecision reports whether fresh is held again in everything an approval
// was given for.
func sameDecision(held, fresh *controlv1.Decision) bool {
	return held != nil && fresh != nil &&
		held.GetVerdict() == fresh.GetVerdict() &&
		held.GetActionDigest() == fresh.GetActionDigest() &&
		slices.Equal(held.GetPolicyRuleIds(), fresh.GetPolicyRuleIds()) &&
		slices.EqualFunc(held.GetObligations(), fresh.GetObligations(), func(a, b *controlv1.Obligation) bool { return proto.Equal(a, b) })
}

// resumeApproved checks what the store answered, then writes APPROVAL_DECIDED
// and ACTION_STARTED on the held trail and executes exactly once, with the
// decision that trail recorded. The approval is consumed before the record is
// written, because two retries must not both write it; a record the sink
// refuses then blocks with the approval spent.
func (c *call) resumeApproved(own *heldRequest, a *controlv1.Approval) Disposition {
	if verdict, code, refused := c.checkApproval(own, a); refused {
		event := expiredEvent(own.approval)
		if a != nil && code != codeApprovalExpired {
			event = decidedEvent(a)
		}
		return c.resumeBlocked(own, verdict, code, event)
	}
	trail, err := c.resumedTrail(own)
	if err != nil {
		return c.blockUnrecorded()
	}
	if !c.p.takeHold(own) {
		// The hold was dropped or resumed between the lookup and here, so its
		// trail is not this call's to write on.
		c.decide(verdictIndeterminate, codeEvidenceUnavailable)
		return c.freshBlock()
	}
	if !c.p.journalMark(c.ctx, own.ids.RequestID, HoldResuming) {
		// The entry still says this trail stands at its request for approval,
		// and nothing may be appended to it while it says so.
		c.p.counts.leftOpen()
		c.decide(verdictIndeterminate, codeEvidenceUnavailable)
		return c.freshBlock()
	}
	c.requestID, c.trail, c.held = own.ids.RequestID, trail, true
	c.kernel, c.decision = own.decision, own.decision
	if !c.record(trail.ApprovalDecided(a)) {
		return c.blockUnrecorded()
	}
	if len(c.rest) > 0 {
		c.action = core.ExecuteWithObligations
	} else {
		c.action = core.Execute
	}
	// The entry stays until the trail is closed, at Close or Abort: a plane
	// that stops between the action starting and its closing record leaves an
	// entry a reconciliation counts, rather than a trail nothing names.
	return c.start(trail)
}

// checkApproval refuses an approval the store handed out that is not the held
// one, approved, unexpired and single-use: the plane never trusts the store's
// answer, so every field is compared with the pipeline's own record of the
// hold, whose digests are this call's decision's because the resume got that
// far. An expiry later than the one the plane minted is not the record it
// minted. It returns the verdict and code of the block.
func (c *call) checkApproval(own *heldRequest, a *controlv1.Approval) (controlv1.Verdict, string, bool) {
	if a == nil || a.GetState() != approvalApproved || a.GetApproverId() == "" {
		return verdictIndeterminate, codeEvidenceUnavailable, true
	}
	return approvalFields(own.approval, a, own.expires, c.now)
}

// approvalFields compares an answer with the record the plane minted, field
// by field, and is the whole of what the plane trusts about a store: the same
// checks a resume makes are the ones a reconciliation makes about a hold
// nobody is retrying. It leaves the answer's state to its caller, because a
// refusal is an answer where a resume needs an approval.
func approvalFields(want, a *controlv1.Approval, expires, now time.Time) (controlv1.Verdict, string, bool) {
	switch {
	case a == nil, a.GetApprovalId() != want.GetApprovalId(),
		a.GetRequestId() != want.GetRequestId(), a.GetMultiUse():
		return verdictIndeterminate, codeEvidenceUnavailable, true
	case a.GetActionDigest() != want.GetActionDigest():
		return verdictDeny, codeApprovalDigestMismatch, true
	case a.GetPolicyBundleDigest() != want.GetPolicyBundleDigest():
		return verdictDeny, codeApprovalBundleMismatch, true
	case a.GetExpiresAt() == nil || !now.Before(a.GetExpiresAt().AsTime()):
		return verdictDeny, codeApprovalExpired, true
	case a.GetExpiresAt().AsTime().After(expires):
		return verdictIndeterminate, codeEvidenceUnavailable, true
	}
	return 0, "", false
}

func expiredEvent(a *controlv1.Approval) func(*evidence.Builder) *controlv1.Event {
	expired := proto.CloneOf(a)
	expired.State = approvalExpired
	return func(t *evidence.Builder) *controlv1.Event { return t.ApprovalExpired(expired) }
}

func decidedEvent(a *controlv1.Approval) func(*evidence.Builder) *controlv1.Event {
	return func(t *evidence.Builder) *controlv1.Event { return t.ApprovalDecided(a) }
}

// resumeBlocked closes the held trail: the approval's outcome, then
// ACTION_BLOCKED with the plane's decision about the held request. It leaves
// this call's own state alone, so a resume can close one held request and go
// on to the next.
//
// Nothing runs on a trail being closed, so a read gets no exemption from the
// sink here. The entry is forgotten and the request id released only where
// both records landed: a trail a refused append left open keeps both, for a
// reconciliation to report and so that no later call writes into it. A hold
// the sweep or another call took first is theirs to close.
func (c *call) resumeBlocked(own *heldRequest, verdict controlv1.Verdict, code string, outcome func(*evidence.Builder) *controlv1.Event) Disposition {
	if !c.p.takeHold(own) {
		return Disposition{Action: core.Block, Decision: c.p.mint(own.decision, c.now, verdictIndeterminate, codeEvidenceUnavailable)}
	}
	decision := c.p.mint(own.decision, c.now, verdict, code)
	trail, err := c.resumedTrail(own)
	if err == nil && c.p.journalMark(c.ctx, own.ids.RequestID, HoldClosing) &&
		c.p.append(c.ctx, outcome(trail)) && c.p.append(c.ctx, trail.Blocked(decision)) {
		c.p.counts.blocked(code)
		c.p.journalForget(c.ctx, own.ids.RequestID)
		c.p.release(own.ids.RequestID)
		return Disposition{Action: core.Block, Decision: decision}
	}
	c.p.counts.blocked(codeEvidenceUnavailable)
	c.p.counts.leftOpen()
	return Disposition{Action: core.Block, Decision: c.p.mint(own.decision, c.now, verdictIndeterminate, codeEvidenceUnavailable)}
}

// resumedTrail continues the held request's trail where the hold left it.
func (c *call) resumedTrail(own *heldRequest) (*evidence.Builder, error) {
	return evidence.Resume(
		own.ids, c.p.cfg.Mode, func() time.Time { return c.now }, c.p.cfg.NewID,
		evidence.Position{LastEventID: own.lastEventID},
	)
}
