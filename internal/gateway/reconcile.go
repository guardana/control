package gateway

import (
	"context"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// Reconciliation is what one pass over the hold journal settled, and what it
// could not. It is a report, never a verdict: a pass that was stopped, or one
// that met an entry it cannot speak for, says so and leaves those trails
// alone.
type Reconciliation struct {
	// Closed is the lost holds whose trails this pass ended.
	Closed int
	// Unclosed is the held entries whose closing could not be written, so
	// their trails still stand at their request for approval and their
	// entries still say so.
	Unclosed int
	// Interrupted is the entries left mid-close by a plane that stopped. One
	// per interrupted close; what landed on those trails is not something
	// this plane can say.
	Interrupted int
	// Unreadable is the entries the journal could not decode. Unmeasured,
	// never skipped.
	Unreadable int
	// Complete is false when the bound stopped the pass, an entry could not
	// be decoded, the journal could not be read, or there was no journal to
	// read: what this report says is then a floor, not a total. A pass that
	// did not read every hold may not say it is done.
	Complete bool
}

// Reconcile closes the trails of holds this plane lost, and reports what it
// could not close. It runs once, at start, before the plane serves: a command
// that only inspects a plane never calls it, however it builds its pipeline,
// because this writes evidence and resolves records.
//
// For each entry that reads as held it asks the store about the record, makes
// the same field checks a live resume makes, and ends that trail: with the
// answer and APPROVAL_NOT_RESUMED where an approver said yes,
// APPROVAL_REJECTED where an approver said no, and APPROVAL_EXPIRED where
// there is no answer, none the checks accept, or no store to ask. The record
// is then resolved as not resumed and never consumed, so the next identical
// call is held anew rather than told that an action ran. Entries in any other
// state, and entries that will not decode, are counted and left untouched.
//
// The closings carry this plane's mode, because what they state is that the
// call was never run, not that this plane enforced anything. A plane with no
// journal reconciles nothing and reports Complete false; Stats().HoldJournal
// is where that plane's declared limit shows.
func (p *Pipeline) Reconcile(ctx context.Context) Reconciliation {
	var r Reconciliation
	if p == nil || p.kernel == nil || p.cfg.Journal == nil {
		return r
	}
	now := p.cfg.Clock()
	listing, err := p.cfg.Journal.List(ctx, p.reconcileMax())
	if err != nil {
		p.counts.reconciled(r)
		return r
	}
	r.Interrupted, r.Unreadable = listing.Interrupted, listing.Unreadable
	r.Complete = listing.Complete && r.Unreadable == 0
	for _, entry := range listing.Held {
		if p.closeLostHold(ctx, entry, now) {
			r.Closed++
			continue
		}
		r.Unclosed++
	}
	p.counts.reconciled(r)
	return r
}

// reconcileMax is the bound on one pass: the configured one, or a bound of
// this build's own, so an unbounded journal never costs an unbounded start.
func (p *Pipeline) reconcileMax() int {
	if p.cfg.ReconcileMax > 0 {
		return p.cfg.ReconcileMax
	}
	return defaultReconcileMax
}

// closeLostHold ends one lost hold's trail and reports whether it is closed.
// Nothing is appended before the entry has left held, and the entry is
// forgotten once the trail is closed, so a pass that is interrupted leaves an
// entry a later pass reports rather than a trail a later pass closes again.
func (p *Pipeline) closeLostHold(ctx context.Context, entry HoldEntry, now time.Time) bool {
	answer, code := p.lostHoldAnswer(ctx, entry, now)
	if !p.journalMark(ctx, entry.IDs.RequestID, HoldClosing) {
		return false
	}
	trail, err := evidence.Resume(
		entry.IDs, p.cfg.Mode, func() time.Time { return now }, p.cfg.NewID,
		evidence.Position{LastEventID: entry.LastEventID},
	)
	if err != nil {
		return false
	}
	outcome := expiredEvent(entry.Approval)
	if answer != nil {
		outcome = decidedEvent(answer)
	}
	if !p.append(ctx, outcome(trail)) {
		return false
	}
	if !p.append(ctx, trail.Blocked(p.mint(lostHoldDecision(entry), now, verdictDeny, code))) {
		return false
	}
	p.counts.blocked(code)
	p.counts.add(func(s *Stats) *uint64 { return &s.HoldsClosed })
	p.resolveLostHold(ctx, entry, now)
	p.journalForget(ctx, entry.IDs.RequestID)
	return true
}

// lostHoldAnswer asks the store what became of a lost hold's approval and
// returns the answer to record, with the code the close carries. Nobody
// answered is not an answer, and neither is one the field checks refuse: both
// close the trail as expired, with the approval the plane itself minted,
// because the plane records nothing a store said that it did not check.
func (p *Pipeline) lostHoldAnswer(ctx context.Context, entry HoldEntry, now time.Time) (*controlv1.Approval, string) {
	helds, err := p.cfg.Approvals.Find(ctx, entry.Binding, now)
	if err != nil {
		return nil, codeApprovalExpired
	}
	for _, held := range helds {
		a := held.Approval
		if a.GetRequestId() != entry.Approval.GetRequestId() {
			continue
		}
		if _, _, refused := approvalFields(entry.Approval, a, entry.Expires, now); refused {
			return nil, codeApprovalExpired
		}
		switch {
		case a.GetState() == approvalApproved && a.GetApproverId() != "":
			return a, codeApprovalNotResumed
		case a.GetState() == approvalRejected:
			return a, codeApprovalRejected
		}
		return nil, codeApprovalExpired
	}
	return nil, codeApprovalExpired
}

// resolveLostHold tells the store the request will not be resumed. A store
// that will not take it keeps a record nothing spent, which the next
// identical call reads as a record it cannot prove was used, so that call is
// held anew: the failure direction is a second approval, never an action
// reported as run.
func (p *Pipeline) resolveLostHold(ctx context.Context, entry HoldEntry, now time.Time) {
	requestID := entry.Approval.GetRequestId()
	if err := p.cfg.Approvals.Resolve(ctx, entry.Binding, requestID, ResolutionNotResumed, now); err != nil {
		p.counts.add(func(s *Stats) *uint64 { return &s.UnheldRecords })
	}
}

// lostHoldDecision is what the plane's own block about a lost hold is minted
// from: the request and the digests the approval carries. The decision that
// hold was taken under is not kept anywhere, by design, so the freshness of
// the policy behind it is not claimed here either.
func lostHoldDecision(entry HoldEntry) *controlv1.Decision {
	return &controlv1.Decision{
		RequestId:          entry.Approval.GetRequestId(),
		ActionDigest:       entry.Approval.GetActionDigest(),
		PolicyBundleDigest: entry.Approval.GetPolicyBundleDigest(),
	}
}

// reconciled records what a pass reported: what it closed is already counted
// as it happened, and what it could not settle is counted here.
func (c *counters) reconciled(r Reconciliation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if unmeasured := r.Unclosed + r.Interrupted + r.Unreadable; unmeasured > 0 {
		c.stats.HoldsUnmeasured += uint64(unmeasured)
	}
	if !r.Complete {
		c.stats.ReconcileIncomplete = true
	}
}
