package gateway_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/core/approval"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/gateway"
)

var errStore = errors.New("store: unavailable")

// fakeStore is a memory store whose answers a test can break: an error from
// Hold, Find or Consume, or a rewrite of what Consume hands out.
type fakeStore struct {
	*gateway.MemoryApprovals
	holdErr, findErr, consumeErr error
	answer                       func(*controlv1.Approval, error) (*controlv1.Approval, error)
	beforeConsume                func()
}

func newFakeStore() *fakeStore { return &fakeStore{MemoryApprovals: &gateway.MemoryApprovals{}} }

func (f *fakeStore) Hold(ctx context.Context, h gateway.Held, now time.Time) error {
	if f.holdErr != nil {
		return f.holdErr
	}
	return f.MemoryApprovals.Hold(ctx, h, now)
}

func (f *fakeStore) Find(ctx context.Context, b approval.Binding, now time.Time) ([]gateway.Held, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.MemoryApprovals.Find(ctx, b, now)
}

func (f *fakeStore) Consume(ctx context.Context, b approval.Binding, requestID, approvalID string, now time.Time) (*controlv1.Approval, error) {
	if f.beforeConsume != nil {
		f.beforeConsume()
	}
	if f.consumeErr != nil {
		return nil, f.consumeErr
	}
	a, err := f.MemoryApprovals.Consume(ctx, b, requestID, approvalID, now)
	if f.answer != nil {
		return f.answer(a, err)
	}
	return a, err
}

// withStore builds a harness over approveRefunds whose store is f.
func withStore(t *testing.T, f *fakeStore) *harness {
	t.Helper()
	return build(t, modeEnforce, snapshot(t, approveRefunds), func(c *gateway.Config) { c.Approvals = f })
}

// holdApproved holds the first refund in f and approves it.
func holdApproved(t *testing.T, h *harness, f *fakeStore) {
	t.Helper()
	first := hold(t, h)
	if err := f.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
}

// TestThePipelineNeverTrustsTheStoresApproval: a store that hands out a
// record that is not the held approval, approved, unexpired and single-use is
// refused, the held trail is closed with the code for what is wrong, and
// nothing runs; the genuine record runs.
func TestThePipelineNeverTrustsTheStoresApproval(t *testing.T) {
	cases := []struct {
		name    string
		lie     func(*controlv1.Approval) *controlv1.Approval
		verdict controlv1.Verdict
		code    string
		outcome controlv1.EventKind
	}{
		{"no record", func(*controlv1.Approval) *controlv1.Approval { return nil }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalExpired},
		{"still pending", func(a *controlv1.Approval) *controlv1.Approval {
			a.State = controlv1.ApprovalState_APPROVAL_STATE_PENDING
			return a
		}, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalDecided},
		{"no approver", func(a *controlv1.Approval) *controlv1.Approval { a.ApproverId = ""; return a }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalDecided},
		{"another approval", func(a *controlv1.Approval) *controlv1.Approval { a.ApprovalId = "a-other"; return a }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalDecided},
		{"another request", func(a *controlv1.Approval) *controlv1.Approval { a.RequestId = "req-other"; return a }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalDecided},
		{"multi-use", func(a *controlv1.Approval) *controlv1.Approval { a.MultiUse = true; return a }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalDecided},
		{"another digest", func(a *controlv1.Approval) *controlv1.Approval { a.ActionDigest = "sha256:" + digits; return a }, verdictDeny, codeApprovalDigestMismatch, kindApprovalDecided},
		{"another bundle", func(a *controlv1.Approval) *controlv1.Approval { a.PolicyBundleDigest = "sha256:" + digits; return a }, verdictDeny, codeApprovalBundleMismatch, kindApprovalDecided},
		{"expiring now", func(a *controlv1.Approval) *controlv1.Approval { a.ExpiresAt = timestamppb.New(base()); return a }, verdictDeny, codeApprovalExpired, kindApprovalExpired},
		{"no expiry", func(a *controlv1.Approval) *controlv1.Approval { a.ExpiresAt = nil; return a }, verdictDeny, codeApprovalExpired, kindApprovalExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeStore()
			h := withStore(t, f)
			holdApproved(t, h, f)
			f.answer = func(a *controlv1.Approval, err error) (*controlv1.Approval, error) {
				if err != nil {
					t.Fatalf("the genuine Consume failed: %v", err)
				}
				return tc.lie(a), nil
			}
			d := h.admit(retry(t, "req-2"), refundArgs())
			expectBlock(t, d, tc.verdict, tc.code, gateway.PDPType)
			if d.Decision.GetRequestId() != "req-1" {
				t.Errorf("the block names request %q, want the held req-1", d.Decision.GetRequestId())
			}
			trail := h.trailOf("req-1")
			expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, tc.outcome, kindBlocked})
			if err := evidence.ValidateChain(trail); err != nil {
				t.Errorf("ValidateChain: %v", err)
			}
			if s := h.p.Stats(); s.Executed != 0 || s.Open != 0 || s.Held != 0 {
				t.Errorf("Stats = %+v", s)
			}
		})
	}
	f := newFakeStore()
	h := withStore(t, f)
	holdApproved(t, h, f)
	f.answer = func(a *controlv1.Approval, err error) (*controlv1.Approval, error) { return a, err }
	if d := h.admit(retry(t, "req-2"), refundArgs()); d.Action != core.Execute {
		t.Errorf("the genuine approval: Action = %d, decision %+v", d.Action, d.Decision)
	}
}

const digits = "0000000000000000000000000000000000000000000000000000000000000000"

// TestAStoreThatCannotAnswerBlocksWithEveryRecordWritten: a Find or a Consume
// that fails blocks the call on a whole trail of its own, and a Hold that
// fails closes the window it opened; every trail validates and nothing is
// held.
func TestAStoreThatCannotAnswerBlocksWithEveryRecordWritten(t *testing.T) {
	t.Run("find", func(t *testing.T) {
		f := newFakeStore()
		f.findErr = errStore
		h := withStore(t, f)
		d := h.admit(refundEnvelope(t, refundArgs()), refundArgs())
		expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
		expectKinds(t, h.kinds(), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
		expectValidHeld(t, h, "req-1", 0)
	})
	t.Run("hold", func(t *testing.T) {
		f := newFakeStore()
		f.holdErr = errStore
		h := withStore(t, f)
		d := h.admit(refundEnvelope(t, refundArgs()), refundArgs())
		expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
		expectKinds(t, h.kinds(), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalExpired, kindBlocked})
		if got := h.events()[3].GetApproval().GetState(); got != controlv1.ApprovalState_APPROVAL_STATE_EXPIRED {
			t.Errorf("APPROVAL_EXPIRED carries state %s", got)
		}
		expectValidHeld(t, h, "req-1", 0)
	})
	t.Run("consume", func(t *testing.T) {
		f := newFakeStore()
		h := withStore(t, f)
		holdApproved(t, h, f)
		f.consumeErr = errStore
		d := h.admit(retry(t, "req-2"), refundArgs())
		expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
		expectKinds(t, kindsOf(h.trailOf("req-2")), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
		expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
		expectValidHeld(t, h, "req-2", 1)
	})
}

func expectValidHeld(t *testing.T, h *harness, requestID string, held int) {
	t.Helper()
	if err := evidenceOf(h, requestID); err != nil {
		t.Errorf("ValidateChain over %s: %v", requestID, err)
	}
	if s := h.p.Stats(); s.Held != held || s.Executed != 0 {
		t.Errorf("Stats = %+v; want %d held and nothing executed", s, held)
	}
}

// TestHoldsPastTheBoundAreRefused: with MaxHeld requests held, the next one
// closes the window it opened and blocks; one held request expiring makes
// room.
func TestHoldsPastTheBoundAreRefused(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds), func(c *gateway.Config) { c.MaxHeld = 1 })
	hold(t, h)
	other := retry(t, "req-2")
	other.Context.SessionId = "sess-2"
	d := h.admit(other, refundArgs())
	expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	expectKinds(t, kindsOf(h.trailOf("req-2")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalExpired, kindBlocked})
	if err := evidenceOf(h, "req-2"); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
	if s := h.p.Stats(); s.Held != 1 {
		t.Errorf("Stats().Held = %d, want 1", s.Held)
	}
	h.clock.set(base().Add(10 * time.Minute))
	third := retry(t, "req-3")
	third.Context.SessionId = "sess-3"
	if d := h.admit(third, refundArgs()); d.Action != core.AwaitApproval {
		t.Errorf("after the first hold expired: Action = %d, want a hold", d.Action)
	}
}

// approveReads asks for an approval on every read.
const approveReads = `{"id":"approve-reads","effect":"REQUIRE_APPROVAL","when":{"action":{"effect":["READ"]}}}`

// TestAnUnrecordedCallIsNeverHeld: under the risk setting a read the sink
// refuses runs unrecorded, but one that would wait for an approval is
// blocked instead, because its retry would resume a trail nobody wrote.
func TestAnUnrecordedCallIsNeverHeld(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveReads), func(c *gateway.Config) { c.AllowReadsUnrecorded = true })
	h.sink.fail(true)
	d := h.admit(readEnvelope(), []byte(`{}`))
	expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	if s := h.p.Stats(); s.Held != 0 || s.Pending != 0 {
		t.Errorf("Stats = %+v", s)
	}
	h.sink.fail(false)
	if d := h.admit(readEnvelope(), []byte(`{}`)); d.Action != core.AwaitApproval {
		t.Errorf("the same read with a sink: Action = %d, want a hold", d.Action)
	}
}

// taintedRefunds asks for an approval of a refund in a run that took in
// restricted data, beside approveRefunds: the digest and the binding are the
// same whatever the run took in, the rules that decide are not.
const taintedRefunds = `{"id":"approve-tainted-refunds","effect":"REQUIRE_APPROVAL","obligations":[{"type":"cap_amount","params":{"max":"1000"}}],"when":{"action":{"name":["refund"]},"flow":{"toxicAtLeast":"RESTRICTED"}}}`

// taintRun executes a read, as requestID, whose result the operator did not
// declare trusted and declared restricted, and closes it: from then on the
// run of the refunds is one that took in restricted data under untrusted
// influence.
func taintRun(t *testing.T, h *harness, requestID string) {
	t.Helper()
	env := readEnvelope()
	env.RequestId = requestID
	a := admission(env, []byte(`{}`))
	a.ResultSensitivity = controlv1.Sensitivity_SENSITIVITY_RESTRICTED
	d := h.p.Admit(context.Background(), a)
	if d.Action != core.Execute {
		t.Fatalf("the read that taints the run: Action = %d, decision %+v", d.Action, d.Decision)
	}
	if err := h.p.Close(context.Background(), d, d.AuthorizedArgs, result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS)); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// tainted is a retry of the first refund once the run is tainted: the same
// envelope, so the same binding, and another decision.
func tainted(t *testing.T, requestID string) gateway.Admission {
	t.Helper()
	return admission(retry(t, requestID), refundArgs())
}

// TestARetryDecidedOtherwiseIsHeldAnew: the retry's decision names another
// rule than the held one, which the held trail cannot record, so the retry is
// a request of its own, held with its own approval on its own trail, and the
// approval of the first hold is left for a retry the kernel decides as it did.
func TestARetryDecidedOtherwiseIsHeldAnew(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds, taintedRefunds, allowReads))
	first := hold(t, h)
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	taintRun(t, h, "read-1")
	d := h.p.Admit(context.Background(), tainted(t, "req-2"))
	if d.Action != core.AwaitApproval || d.Pending == nil || d.Pending.ApprovalID == first.Pending.ApprovalID {
		t.Fatalf("a retry decided otherwise: Action = %d, pending %+v; want a hold of its own", d.Action, d.Pending)
	}
	if d.Pending.ActionDigest != first.Pending.ActionDigest {
		t.Errorf("the new hold is about another action digest; the flow is not in the digest")
	}
	trail := h.trailOf("req-2")
	expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	if err := evidence.ValidateChain(trail); err != nil {
		t.Errorf("ValidateChain over the new hold: %v", err)
	}
	held := h.trailOf("req-1")[1].GetDecision()
	if fresh := trail[1].GetDecision(); fresh.GetActionDigest() != held.GetActionDigest() || slices.Equal(fresh.GetPolicyRuleIds(), held.GetPolicyRuleIds()) {
		t.Errorf("the new hold records %v about %s; the held trail %v about %s", fresh.GetPolicyRuleIds(), fresh.GetActionDigest(), held.GetPolicyRuleIds(), held.GetActionDigest())
	}
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	// The run stays tainted, so every later retry is decided as req-2 was and
	// never spends the approval given for the first hold.
	if again := h.admit(retry(t, "req-3"), refundArgs()); pendingID(again) != d.Pending.ApprovalID {
		t.Errorf("a retry in the tainted run: Action = %d, pending %+v; want the tainted hold", again.Action, again.Pending)
	}
}

// TestAResumeTakesTheApprovedOneOfSeveralHolds: two requests end up held under
// one binding, the second because the run was tainted in between and the
// kernel decided it otherwise; a retry is told about the hold its own decision
// matches, the approved one is the one consumed, and every trail is one chain.
func TestAResumeTakesTheApprovedOneOfSeveralHolds(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds, taintedRefunds, allowReads))
	first := hold(t, h)
	taintRun(t, h, "read-1")
	second := h.p.Admit(context.Background(), tainted(t, "req-2"))
	if second.Action != core.AwaitApproval || second.Pending == nil || second.Pending.ApprovalID == first.Pending.ApprovalID {
		t.Fatalf("the second hold: Action = %d, pending %+v", second.Action, second.Pending)
	}
	if s := h.p.Stats(); s.Held != 2 {
		t.Fatalf("Stats().Held = %d, want both holds", s.Held)
	}
	if d := h.p.Admit(context.Background(), tainted(t, "req-3")); pendingID(d) != second.Pending.ApprovalID {
		t.Errorf("a tainted retry with neither answered is told about %+v; want the tainted hold", d.Pending)
	}
	if err := h.store.Answer(second.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	run := h.p.Admit(context.Background(), tainted(t, "req-5"))
	if run.Action != core.Execute {
		t.Fatalf("with the tainted hold approved: Action = %d, decision %+v", run.Action, run.Decision)
	}
	if err := h.p.Close(context.Background(), run, run.AuthorizedArgs, result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS)); err != nil {
		t.Fatalf("Close: %v", err)
	}
	expectOneHoldConsumed(t, h)
}

// expectOneHoldConsumed asserts the tainted hold ran on its own trail, the
// first hold is the prefix it was, and no retry wrote a trail of its own.
func expectOneHoldConsumed(t *testing.T, h *harness) {
	t.Helper()
	expectKinds(t, kindsOf(h.trailOf("req-2")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindStarted, kindCompleted})
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	for _, id := range []string{"req-1", "req-2"} {
		if err := evidenceOf(h, id); err != nil {
			t.Errorf("ValidateChain over %s: %v", id, err)
		}
	}
	if got := len(h.events()) - len(h.trailOf("req-1")) - len(h.trailOf("req-2")) - len(h.trailOf("read-1")); got != 0 {
		t.Errorf("%d event(s) outside the two held trails and the read; a retry that resumes one writes no trail of its own", got)
	}
	if s := h.p.Stats(); s.Held != 1 || s.Executed != 2 || s.Pending != 3 {
		t.Errorf("Stats = %+v; want the first hold still held, the read and the resume executed", s)
	}
}

// TestARejectionCarriesTheRecordTheStoreRead: an answer that lands between
// the lookup and the consumption is what APPROVAL_DECIDED records, not the
// pending copy the lookup returned.
func TestARejectionCarriesTheRecordTheStoreRead(t *testing.T) {
	f := newFakeStore()
	h := withStore(t, f)
	first := hold(t, h)
	f.beforeConsume = func() {
		if err := f.Answer(first.Pending.ApprovalID, rejected, "bob", "not today", base()); err != nil {
			t.Errorf("Answer: %v", err)
		}
	}
	d := h.admit(retry(t, "req-2"), refundArgs())
	expectBlock(t, d, verdictDeny, codeApprovalRejected, gateway.PDPType)
	trail := h.trailOf("req-1")
	expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindBlocked})
	if a := trail[3].GetApproval(); a.GetState() != rejected || a.GetApproverId() != "bob" || a.GetReason() != "not today" {
		t.Errorf("APPROVAL_DECIDED carries %+v; want the rejection the store read", a)
	}
}

// TestAnExpiryTheStoreReportsClosesTheHeldTrail: a store that reports the
// approval expired has the held trail closed with APPROVAL_EXPIRED and the
// plane's DENY about the held request.
func TestAnExpiryTheStoreReportsClosesTheHeldTrail(t *testing.T) {
	f := newFakeStore()
	h := withStore(t, f)
	holdApproved(t, h, f)
	f.consumeErr = gateway.ErrApprovalExpired
	d := h.admit(retry(t, "req-2"), refundArgs())
	expectBlock(t, d, verdictDeny, codeApprovalExpired, gateway.PDPType)
	trail := h.trailOf("req-1")
	expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalExpired, kindBlocked})
	if err := evidence.ValidateChain(trail); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
	if trail[3].GetApproval().GetState() != controlv1.ApprovalState_APPROVAL_STATE_EXPIRED || trail[4].GetDecision().GetRequestId() != "req-1" {
		t.Errorf("APPROVAL_EXPIRED carries %+v and ACTION_BLOCKED names %q", trail[3].GetApproval(), trail[4].GetDecision().GetRequestId())
	}
}

// lyingStore rewrites the approval of every record it hands out, in Find and in
// Consume alike, so a lie is consistent whichever answer the plane reads. With
// frozen set it answers as at that reading, so it keeps a record the plane
// minted to expire.
type lyingStore struct {
	*gateway.MemoryApprovals
	lie    func(*controlv1.Approval)
	frozen time.Time
}

func (l *lyingStore) at(now time.Time) time.Time {
	if l.frozen.IsZero() {
		return now
	}
	return l.frozen
}

func (l *lyingStore) Find(ctx context.Context, b approval.Binding, now time.Time) ([]gateway.Held, error) {
	helds, err := l.MemoryApprovals.Find(ctx, b, l.at(now))
	for i := range helds {
		l.lie(helds[i].Approval)
	}
	return helds, err
}

func (l *lyingStore) Consume(ctx context.Context, b approval.Binding, requestID, approvalID string, now time.Time) (*controlv1.Approval, error) {
	a, err := l.MemoryApprovals.Consume(ctx, b, requestID, approvalID, l.at(now))
	if a != nil {
		l.lie(a)
	}
	return a, err
}

// TestAStoreThatLiesTheSameWayTwiceIsStillRefused: a store that rewrites the
// action digest and the bundle in both of its answers is checked against the
// pipeline's own record of the hold and this call's own decision, so nothing
// runs and the held trail is closed.
func TestAStoreThatLiesTheSameWayTwiceIsStillRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		lie  func(*controlv1.Approval)
		code string
	}{
		"another digest in both answers": {func(a *controlv1.Approval) { a.ActionDigest = "sha256:" + digits }, codeApprovalDigestMismatch},
		"another bundle in both answers": {func(a *controlv1.Approval) { a.PolicyBundleDigest = "sha256:" + digits }, codeApprovalBundleMismatch},
	} {
		t.Run(name, func(t *testing.T) {
			l := &lyingStore{MemoryApprovals: &gateway.MemoryApprovals{}, lie: tc.lie}
			h := build(t, modeEnforce, snapshot(t, approveRefunds), func(c *gateway.Config) { c.Approvals = l })
			first := hold(t, h)
			if err := l.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
				t.Fatalf("Answer: %v", err)
			}
			d := h.admit(retry(t, "req-2"), refundArgs())
			expectBlock(t, d, verdictDeny, tc.code, gateway.PDPType)
			expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindBlocked})
			if err := evidenceOf(h, "req-1"); err != nil {
				t.Errorf("ValidateChain: %v", err)
			}
			if s := h.p.Stats(); s.Executed != 0 || s.Open != 0 || s.Held != 0 {
				t.Errorf("Stats = %+v", s)
			}
		})
	}
}

// TestAStoreThatStretchesTheExpiryIsRefused: an approval that outlives the
// window the plane minted is not the record it minted, and past that window the
// plane holds no such request at all, whatever the store still answers.
func TestAStoreThatStretchesTheExpiryIsRefused(t *testing.T) {
	stretch := func(a *controlv1.Approval) { a.ExpiresAt = timestamppb.New(base().Add(24 * time.Hour)) }

	inside := &lyingStore{MemoryApprovals: &gateway.MemoryApprovals{}, lie: stretch}
	h := build(t, modeEnforce, snapshot(t, approveRefunds), func(c *gateway.Config) { c.Approvals = inside })
	first := hold(t, h)
	if err := inside.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	d := h.admit(retry(t, "req-2"), refundArgs())
	expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindBlocked})

	// Past the window the plane minted, the plane holds no such request, so the
	// retry is a request of its own and the hold's trail is closed as expired,
	// however alive the store still says its record is.
	after := &lyingStore{MemoryApprovals: &gateway.MemoryApprovals{}, lie: stretch, frozen: base()}
	h = build(t, modeEnforce, snapshot(t, approveRefunds), func(c *gateway.Config) {
		c.Approvals = after
		c.ApprovalTTL = time.Minute
	})
	first = hold(t, h)
	if err := after.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	h.clock.set(base().Add(2 * time.Minute))
	d = h.admit(retry(t, "req-2"), refundArgs())
	if d.Action != core.AwaitApproval || d.Pending == nil || d.Pending.ApprovalID == first.Pending.ApprovalID {
		t.Fatalf("past the minted expiry: Action = %d, pending %+v; want a hold of its own", d.Action, d.Pending)
	}
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalExpired, kindBlocked})
	if err := evidenceOf(h, "req-1"); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
	if s := h.p.Stats(); s.Executed != 0 {
		t.Errorf("Stats = %+v; want nothing executed on an approval the plane minted to expire", s)
	}
}

// forgedStore files every hold under an approval id the plane never minted,
// which is what a writer of an approvals directory can do to a record that
// carries the binding and request id of a live hold. It remembers the approval
// ids the plane asked to consume by.
type forgedStore struct {
	*gateway.MemoryApprovals
	forged  string
	binding approval.Binding
	asked   []string
}

func (f *forgedStore) Hold(ctx context.Context, h gateway.Held, now time.Time) error {
	f.binding = h.Binding
	h.Approval = proto.CloneOf(h.Approval)
	h.Approval.ApprovalId = f.forged
	return f.MemoryApprovals.Hold(ctx, h, now)
}

func (f *forgedStore) Consume(ctx context.Context, b approval.Binding, requestID, approvalID string, now time.Time) (*controlv1.Approval, error) {
	f.asked = append(f.asked, approvalID)
	return f.MemoryApprovals.Consume(ctx, b, requestID, approvalID, now)
}

// TestARecordUnderAnotherApprovalIdIsNeverSpent: the store keeps a record
// under the binding and request id of a live hold, carrying an approval id the
// plane never minted, and an approver answers that record. The plane consumes
// by the approval it minted, so the record is refused, nothing runs, and it is
// left unspent for a reconciliation to resolve (ADR-0016).
func TestARecordUnderAnotherApprovalIdIsNeverSpent(t *testing.T) {
	f := &forgedStore{MemoryApprovals: &gateway.MemoryApprovals{}, forged: "apr-forged"}
	h := build(t, modeEnforce, snapshot(t, approveRefunds), func(c *gateway.Config) { c.Approvals = f })
	first := hold(t, h)
	if first.Pending.ApprovalID == f.forged {
		t.Fatalf("the plane minted %q, which the store forged", f.forged)
	}
	if err := f.Answer(f.forged, approved, "mallory", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	d := h.admit(retry(t, "req-2"), refundArgs())
	if d.Action == core.Execute {
		t.Fatalf("a record the plane never minted ran the call: %+v", d.Decision)
	}
	if d.Action != core.AwaitApproval || pendingID(d) != first.Pending.ApprovalID {
		t.Errorf("Action = %d, pending %+v; want the call still awaiting the approval the plane minted", d.Action, d.Pending)
	}
	if !slices.Equal(f.asked, []string{first.Pending.ApprovalID}) {
		t.Errorf("the plane consumed by %q; want the one approval id it minted", f.asked)
	}
	found, err := f.Find(context.Background(), f.binding, base())
	if err != nil || len(found) != 1 || found[0].Resolution != gateway.ResolutionPending {
		t.Errorf("the store holds %+v, %v; want the forged record still unspent", found, err)
	}
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	if s := h.p.Stats(); s.Executed != 0 || s.Held != 1 {
		t.Errorf("Stats = %+v; want the hold still held and nothing executed", s)
	}
}

// TestAStoreThatConsumesOneApprovalTwiceRunsItOnce: the flip from held to
// running is the pipeline's, so a store that hands the same approval to a
// second retry cannot have the held trail written twice; the second retry is
// refused on its own trail.
func TestAStoreThatConsumesOneApprovalTwiceRunsItOnce(t *testing.T) {
	f := newFakeStore()
	h := withStore(t, f)
	holdApproved(t, h, f)
	var spent *controlv1.Approval
	f.answer = func(a *controlv1.Approval, err error) (*controlv1.Approval, error) {
		if err == nil {
			spent = a
			return a, nil
		}
		if errors.Is(err, gateway.ErrApprovalConsumed) && spent != nil {
			return spent, nil
		}
		return a, err
	}
	inner := false
	f.beforeConsume = func() {
		if inner {
			return
		}
		inner = true
		// The held request runs, and its trail stops being held, while the
		// outer retry is between the lookup and its own consumption.
		if d := h.admit(retry(t, "req-inner"), refundArgs()); d.Action != core.Execute {
			t.Errorf("the inner retry: Action = %d, decision %+v", d.Action, d.Decision)
		}
	}
	d := h.admit(retry(t, "req-2"), refundArgs())
	expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindStarted})
	expectKinds(t, kindsOf(h.trailOf("req-2")), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
	if err := evidenceOf(h, "req-1"); err != nil {
		t.Errorf("ValidateChain over the resumed trail: %v", err)
	}
	if s := h.p.Stats(); s.Executed != 1 {
		t.Errorf("Stats = %+v; want the one execution the approval was for", s)
	}
}

// TestAStoreThatRewritesTheHeldEnvelopeResumesNothing: what the retry is
// compared with is the envelope the pipeline holds, so a store that hands out
// an envelope matching a call the approval was not given for resumes nothing
// and that call is held on its own.
func TestAStoreThatRewritesTheHeldEnvelopeResumesNothing(t *testing.T) {
	l := &envelopeStore{MemoryApprovals: &gateway.MemoryApprovals{}}
	h := build(t, modeEnforce, snapshot(t, approveRefunds), func(c *gateway.Config) { c.Approvals = l })
	first := hold(t, h)
	if err := l.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	other := retry(t, "req-2")
	other.Context.SessionId = "sess-2"
	l.session = "sess-2"
	d := h.admit(other, refundArgs())
	if d.Action != core.AwaitApproval || d.Pending == nil || d.Pending.ApprovalID == first.Pending.ApprovalID {
		t.Fatalf("a retry the store dressed as the held request: Action = %d, pending %+v; want a hold of its own", d.Action, d.Pending)
	}
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	expectKinds(t, kindsOf(h.trailOf("req-2")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	if s := h.p.Stats(); s.Executed != 0 {
		t.Errorf("Stats = %+v; want nothing executed on an approval given for another run context", s)
	}
}

// envelopeStore rewrites the run context of every held envelope it hands out.
type envelopeStore struct {
	*gateway.MemoryApprovals
	session string
}

func (e *envelopeStore) Find(ctx context.Context, b approval.Binding, now time.Time) ([]gateway.Held, error) {
	helds, err := e.MemoryApprovals.Find(ctx, b, now)
	for i := range helds {
		if e.session != "" {
			helds[i].Envelope.Context.SessionId = e.session
		}
	}
	return helds, err
}

// anyBindingStore answers every lookup with the records it holds under the
// first binding it was given, whatever binding was asked for.
type anyBindingStore struct {
	*gateway.MemoryApprovals
	first approval.Binding
}

func (s *anyBindingStore) Hold(ctx context.Context, held gateway.Held, now time.Time) error {
	if s.first == "" {
		s.first = held.Binding
	}
	return s.MemoryApprovals.Hold(ctx, held, now)
}

func (s *anyBindingStore) Find(ctx context.Context, _ approval.Binding, now time.Time) ([]gateway.Held, error) {
	return s.MemoryApprovals.Find(ctx, s.first, now)
}

// TestAHoldIsResumedOnlyUnderTheBindingItWasHeldFor: the bundle changes between
// the hold and the retry, so the same call has another binding; a store that
// hands out the hold anyway resumes nothing, because the pipeline's own record
// says which binding it was held under.
func TestAHoldIsResumedOnlyUnderTheBindingItWasHeldFor(t *testing.T) {
	s := &anyBindingStore{MemoryApprovals: &gateway.MemoryApprovals{}}
	h := build(t, modeEnforce, snapshot(t, approveRefunds), func(c *gateway.Config) { c.Approvals = s })
	first := hold(t, h)
	if err := s.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	h.policy.snap = snapshot(t, approveRefunds, allowReads)
	d := h.admit(retry(t, "req-2"), refundArgs())
	if d.Action != core.AwaitApproval || d.Pending == nil || d.Pending.ApprovalID == first.Pending.ApprovalID {
		t.Fatalf("under another bundle: Action = %d, pending %+v; want a hold of its own", d.Action, d.Pending)
	}
	if d.Pending.ActionDigest != first.Pending.ActionDigest {
		t.Errorf("the two calls have different action digests, so this test says nothing about the binding")
	}
	expectKinds(t, kindsOf(h.trailOf("req-2")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	if s := h.p.Stats(); s.Executed != 0 || s.Held != 2 {
		t.Errorf("Stats = %+v; want both requests held and nothing executed", s)
	}
}
