package gateway_test

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/core/approval"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/gateway"
)

// retry is the held request again from the agent's side: a new request id,
// a later occurred_at, the same everything else.
func retry(t *testing.T, requestID string) *controlv1.ActionEnvelope {
	t.Helper()
	env := refundEnvelope(t, refundArgs())
	env.RequestId = requestID
	env.OccurredAt = timestamppb.New(base().Add(time.Minute))
	env.TraceId = "trace-" + requestID
	return env
}

// hold admits the first refund and returns its pending state.
func hold(t *testing.T, h *harness) gateway.Disposition {
	t.Helper()
	d := h.admit(refundEnvelope(t, refundArgs()), refundArgs())
	if d.Action != core.AwaitApproval || d.Pending == nil {
		t.Fatalf("the first refund: Action = %d, pending %+v, decision %+v", d.Action, d.Pending, d.Decision)
	}
	return d
}

// TestAHeldRequestIsResumedByItsRetry: the first call is held and answered
// pending; a retry before the approval is still pending with the same id and
// writes nothing; once approved, the retry writes APPROVAL_DECIDED and
// ACTION_STARTED on the held trail, executes with the capped arguments, and
// the held trail validates as one chain after Close. The next identical call
// finds the approval consumed.
func TestAHeldRequestIsResumedByItsRetry(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds))
	first := hold(t, h)
	expectFirstHold(t, h, first)

	again := h.admit(retry(t, "req-2"), refundArgs())
	if again.Action != core.AwaitApproval || again.Pending == nil || again.Pending.ApprovalID != first.Pending.ApprovalID {
		t.Fatalf("a retry before the approval: Action = %d, pending %+v", again.Action, again.Pending)
	}
	if got := len(h.events()); got != 3 {
		t.Errorf("a pending retry wrote %d event(s) more", got-3)
	}

	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base().Add(time.Minute)); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	h.clock.set(base().Add(2 * time.Minute))
	want := `{"amount":1000,"currency":"EUR","ssn":"x"}`
	run := h.admit(retry(t, "req-3"), refundArgs())
	if run.Action != core.Execute || string(run.AuthorizedArgs) != want || run.AuthorizedDigest != argumentsHash(t, []byte(want)) {
		t.Fatalf("the approved retry: Action = %d, args %s, digest %s; want Execute with %s", run.Action, run.AuthorizedArgs, run.AuthorizedDigest, want)
	}
	if err := h.p.Close(context.Background(), run, []byte(want), result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS)); err != nil {
		t.Fatalf("Close: %v", err)
	}
	expectResumedTrail(t, h, first.Pending.ApprovalID)
	if s := h.p.Stats(); s.Pending != 2 || s.Executed != 1 {
		t.Errorf("Stats = %+v", s)
	}

	expectUsed(t, h, "req-4")
}

// expectUsed asserts the next identical call is a request of its own,
// refused in full on its own trail as already used.
func expectUsed(t *testing.T, h *harness, requestID string) {
	t.Helper()
	used := h.admit(retry(t, requestID), refundArgs())
	expectBlock(t, used, verdictDeny, codeApprovalAlreadyUsed, gateway.PDPType)
	expectKinds(t, kindsOf(h.trailOf(requestID)), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
	if err := evidence.ValidateChain(h.trailOf(requestID)); err != nil {
		t.Errorf("ValidateChain over the refused retry: %v", err)
	}
}

// expectFirstHold asserts the pending state and the APPROVAL_REQUESTED record
// of a fresh hold.
func expectFirstHold(t *testing.T, h *harness, first gateway.Disposition) {
	t.Helper()
	if first.Pending.ActionDigest == "" || first.Pending.RetryAfter != 5*time.Second || !first.Pending.ExpiresAt.Equal(base().Add(10*time.Minute)) {
		t.Errorf("Pending = %+v", first.Pending)
	}
	if first.Decision.GetVerdict() != verdictApproval || first.AuthorizedArgs != nil {
		t.Errorf("the pending disposition = %+v; want the kernel's REQUIRE_APPROVAL and nothing to send", first)
	}
	expectKinds(t, h.kinds(), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	requested := h.events()[2].GetApproval()
	if requested.GetApprovalId() != first.Pending.ApprovalID || requested.GetRequestId() != "req-1" ||
		requested.GetActionDigest() != first.Pending.ActionDigest || requested.GetState() != controlv1.ApprovalState_APPROVAL_STATE_PENDING ||
		!requested.GetExpiresAt().AsTime().Equal(first.Pending.ExpiresAt) || requested.GetPolicyBundleDigest() == "" {
		t.Errorf("APPROVAL_REQUESTED carries %+v", requested)
	}
}

// expectResumedTrail asserts the held trail is one validating chain, the only
// trail written, closed after the approval by approvalID.
func expectResumedTrail(t *testing.T, h *harness, approvalID string) {
	t.Helper()
	trail := h.trailOf("req-1")
	expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindStarted, kindCompleted})
	if err := evidence.ValidateChain(trail); err != nil {
		t.Errorf("ValidateChain over the held trail: %v", err)
	}
	if got := len(h.events()); got != len(trail) {
		t.Errorf("%d event(s) were written outside the held trail; a resume writes no new ACTION_PROPOSED", got-len(trail))
	}
	decided := trail[3].GetApproval()
	if decided.GetState() != approved || decided.GetApproverId() != "alice" || decided.GetApprovalId() != approvalID {
		t.Errorf("APPROVAL_DECIDED carries %+v", decided)
	}
}

// TestARetryWithAnotherLabelIsANewRequest: the binding matches but a field
// the digest leaves out differs, so the retry is held on its own, and the
// first hold is untouched.
func TestARetryWithAnotherLabelIsANewRequest(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds))
	first := hold(t, h)
	for name, mut := range map[string]func(*controlv1.ActionEnvelope){
		"a data label": func(env *controlv1.ActionEnvelope) {
			env.Data = &controlv1.DataLabels{Sensitivities: []controlv1.Sensitivity{controlv1.Sensitivity_SENSITIVITY_PUBLIC}}
		},
		"a run context": func(env *controlv1.ActionEnvelope) { env.Context.SessionId = "sess-2" },
	} {
		env := retry(t, "req-"+name)
		mut(env)
		d := h.admit(env, refundArgs())
		if d.Action != core.AwaitApproval || d.Pending == nil || d.Pending.ApprovalID == first.Pending.ApprovalID {
			t.Errorf("a retry with %s: Action = %d, pending %+v; want a new hold", name, d.Action, d.Pending)
		}
		if d.Pending != nil && d.Pending.ActionDigest != first.Pending.ActionDigest {
			t.Errorf("a retry with %s has another action digest; the digest leaves that field out", name)
		}
		expectKinds(t, kindsOf(h.trailOf("req-"+name)), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	}
	// The approval of the first hold does not carry over.
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	env := retry(t, "req-later")
	env.Context.SessionId = "sess-2"
	if d := h.admit(env, refundArgs()); d.Action != core.AwaitApproval {
		t.Errorf("a differing retry ran on the first request's approval: Action = %d", d.Action)
	}
}

// TestAnExpiredHoldIsForgottenAndTheRetryIsHeldAnew: at the expiry the store
// no longer finds the held request, so a retry is a new request held on its
// own trail; the plane closes the trail of the hold it drops, APPROVAL_EXPIRED
// and then its own ACTION_BLOCKED, before the request id is free again.
func TestAnExpiredHoldIsForgottenAndTheRetryIsHeldAnew(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds))
	first := hold(t, h)
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	h.clock.set(base().Add(10*time.Minute - time.Nanosecond))
	if s := h.p.Stats(); s.Held != 1 {
		t.Errorf("Stats().Held a nanosecond before the expiry = %d, want 1", s.Held)
	}
	h.clock.set(base().Add(10 * time.Minute))
	if s := h.p.Stats(); s.Held != 0 {
		t.Errorf("Stats().Held at the expiry = %d, want 0", s.Held)
	}
	next := h.admit(retry(t, "req-2"), refundArgs())
	if next.Action != core.AwaitApproval || next.Pending == nil || next.Pending.ApprovalID == first.Pending.ApprovalID {
		t.Errorf("after the expiry: Action = %d, pending %+v; want a new hold", next.Action, next.Pending)
	}
	expectClosedHold(t, h, first.Pending.ApprovalID)
	// The expired request's id is free again: its trail is concluded.
	if d := h.admit(refundEnvelope(t, refundArgs()), refundArgs()); d.Action != core.AwaitApproval {
		t.Errorf("a request under the expired request's id: Action = %d, want a hold", d.Action)
	}
}

// expectClosedHold asserts the trail of the dropped hold ends in its expiry
// and the plane's own block about the held request.
func expectClosedHold(t *testing.T, h *harness, approvalID string) {
	t.Helper()
	trail := h.trailOf("req-1")
	expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalExpired, kindBlocked})
	if err := evidence.ValidateChain(trail); err != nil {
		t.Errorf("ValidateChain over the closed hold: %v", err)
	}
	if a := trail[3].GetApproval(); a.GetState() != controlv1.ApprovalState_APPROVAL_STATE_EXPIRED || a.GetApprovalId() != approvalID {
		t.Errorf("APPROVAL_EXPIRED carries %+v", a)
	}
	blocked := trail[4].GetDecision()
	if blocked.GetVerdict() != verdictDeny || blocked.GetPdpType() != gateway.PDPType ||
		!slices.Contains(blocked.GetReasonCodes(), codeApprovalExpired) || blocked.GetRequestId() != "req-1" {
		t.Errorf("ACTION_BLOCKED carries %+v; want the plane's DENY about req-1", blocked)
	}
	if h.p.Stats().Blocks[codeApprovalExpired] != 1 {
		t.Errorf("Stats().Blocks = %v, want APPROVAL_EXPIRED once", h.p.Stats().Blocks)
	}
}

// TestAHoldWhoseClosingRecordIsRefusedKeepsItsRequestID: the sink refuses the
// records that would close a dropped hold, so the id stays reserved rather
// than taking a second trail.
func TestAHoldWhoseClosingRecordIsRefusedKeepsItsRequestID(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds))
	hold(t, h)
	h.sink.refuse[kindApprovalExpired] = true
	h.clock.set(base().Add(10 * time.Minute))
	again := h.admit(refundEnvelope(t, refundArgs()), refundArgs())
	expectBlock(t, again, verdictIndeterminate, codeInvalidFieldValue, gateway.PDPType)
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	if s := h.p.Stats(); s.Held != 0 || s.SinkFailuresBeforeEffect != 1 {
		t.Errorf("Stats = %+v; want nothing held and the refused record counted", s)
	}
}

// TestARejectedApprovalBlocks: the approver's no closes the held trail with
// APPROVAL_DECIDED and ACTION_BLOCKED naming APPROVAL_REJECTED.
func TestARejectedApprovalBlocks(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds))
	first := hold(t, h)
	if err := h.store.Answer(first.Pending.ApprovalID, rejected, "", "no", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	d := h.admit(retry(t, "req-2"), refundArgs())
	expectBlock(t, d, verdictDeny, codeApprovalRejected, gateway.PDPType)
	trail := h.trailOf("req-1")
	expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindBlocked})
	if err := evidence.ValidateChain(trail); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
	if trail[3].GetApproval().GetState() != rejected {
		t.Errorf("APPROVAL_DECIDED carries state %s, want REJECTED", trail[3].GetApproval().GetState())
	}
}

// TestAClosingBlockTheSinkRefusesKeepsTheJournalEntry: the approver's no is
// on the trail but the plane's own ACTION_BLOCKED is refused, so the trail is
// not closed. The entry stays for a reconciliation to close, nothing counts a
// block that was never written, and the agent is still refused.
func TestAClosingBlockTheSinkRefusesKeepsTheJournalEntry(t *testing.T) {
	j := &journalDouble{}
	h := build(t, modeEnforce, snapshot(t, approveRefunds), func(cfg *gateway.Config) { cfg.Journal = j })
	first := hold(t, h)
	if err := h.store.Answer(first.Pending.ApprovalID, rejected, "", "no", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	h.sink.refuseKind(kindBlocked)

	d := h.admit(retry(t, "req-2"), refundArgs())
	expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	expectKinds(t, kindsOf(h.trailOf("req-1")),
		[]controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided})
	state, kept := j.state("req-1")
	if !kept {
		t.Errorf("the entry of a trail nothing closed is forgotten; no reconciliation can close that trail")
	} else if state != gateway.HoldClosing {
		t.Errorf("the entry reads state %v, want the closing state the plane flipped it to", state)
	}
	if s := h.p.Stats(); s.Blocks[codeApprovalRejected] != 0 || s.Blocks[codeEvidenceUnavailable] != 1 {
		t.Errorf("Stats().Blocks = %v; want no block counted for a record the sink refused", s.Blocks)
	}
}

// TestTheKernelDecidesTheRetryAgain: a delegation that expired between the
// hold and the retry makes the retry a DENY of the kernel's, refused on its
// own trail, whatever the approval says.
func TestTheKernelDecidesTheRetryAgain(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds))
	first := hold(t, h)
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	env := retry(t, "req-2")
	env.Delegation = []*controlv1.Delegation{{
		From: "user-1", To: "agent-1", Scopes: []string{"refund"},
		IssuedAt:  timestamppb.New(base().Add(-2 * time.Hour)),
		ExpiresAt: timestamppb.New(base().Add(-time.Hour)),
	}}
	d := h.admit(env, refundArgs())
	expectBlock(t, d, verdictDeny, codeDelegationExpired, "builtin")
	expectKinds(t, kindsOf(h.trailOf("req-2")), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
	if got := len(h.trailOf("req-1")); got != 3 {
		t.Errorf("the held trail has %d event(s); a denied retry does not touch it", got)
	}
}

// TestUnderApproveTheHoldIsSatisfiedLikeRequireApproval: an allowed write
// under APPROVE is held, and its approval resumes it exactly as the kernel's
// REQUIRE_APPROVAL would.
func TestUnderApproveTheHoldIsSatisfiedLikeRequireApproval(t *testing.T) {
	h := build(t, modeApprove, snapshot(t, allowWrites))
	first := h.admit(writeEnvelope(), []byte(`{"k":1}`))
	if first.Action != core.AwaitApproval || first.Pending == nil {
		t.Fatalf("an allowed write under APPROVE: Action = %d", first.Action)
	}
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	env := writeEnvelope()
	env.RequestId = "req-2"
	run := h.admit(env, []byte(`{"k":1}`))
	if run.Action != core.Execute || string(run.AuthorizedArgs) != `{"k":1}` {
		t.Fatalf("the approved retry: Action = %d, args %s", run.Action, run.AuthorizedArgs)
	}
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindStarted})
}

// TestConcurrentIdenticalRetriesExecuteOnce: many retries of one approved
// hold at once, one executes, every other is refused as already used, and
// the held trail is one chain.
func TestConcurrentIdenticalRetriesExecuteOnce(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds))
	first := hold(t, h)
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	const retries = 16
	var wg sync.WaitGroup
	results := make([]gateway.Disposition, retries)
	for i := range retries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = h.admit(retry(t, "req-retry-"+strconv.Itoa(i)), refundArgs())
		}()
	}
	wg.Wait()
	executed, used := 0, 0
	for _, d := range results {
		switch {
		case d.Action == core.Execute:
			executed++
		case d.Action == core.Block && d.Decision.GetReasonCodes()[0] == codeApprovalAlreadyUsed:
			used++
		default:
			t.Errorf("a retry answered %d with %v", d.Action, d.Decision.GetReasonCodes())
		}
	}
	if executed != 1 || used != retries-1 {
		t.Errorf("%d executed and %d refused as used; want 1 and %d", executed, used, retries-1)
	}
	trail := h.trailOf("req-1")
	expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindStarted})
	if err := evidence.ValidateChain(trail); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
}

// lateFindStore runs afterFind once, after the next Find has read the store
// and before that Find returns, so its answer is stale by the time the caller
// sees it.
type lateFindStore struct {
	gateway.ApprovalStore
	afterFind func()
}

func (s *lateFindStore) Find(ctx context.Context, b approval.Binding, now time.Time) ([]gateway.Held, error) {
	helds, err := s.ApprovalStore.Find(ctx, b, now)
	if run := s.afterFind; run != nil {
		s.afterFind = nil
		run()
	}
	return helds, err
}

// TestARetryWhoseLookupOutlivesTheResumeIsAlreadyUsed: a retry whose store
// lookup read the approval unspent, and returned only after another retry
// consumed it and took the hold, is refused as already used on its own trail
// and holds nothing anew.
func TestARetryWhoseLookupOutlivesTheResumeIsAlreadyUsed(t *testing.T) {
	var store *lateFindStore
	h := build(t, modeEnforce, snapshot(t, approveRefunds), func(cfg *gateway.Config) {
		store = &lateFindStore{ApprovalStore: cfg.Approvals}
		cfg.Approvals = store
	})
	first := hold(t, h)
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	var resumed gateway.Disposition
	store.afterFind = func() { resumed = h.admit(retry(t, "req-2"), refundArgs()) }
	late := h.admit(retry(t, "req-3"), refundArgs())
	if resumed.Action != core.Execute {
		t.Fatalf("the retry that resumed: Action = %d, codes %v", resumed.Action, resumed.Decision.GetReasonCodes())
	}
	expectBlock(t, late, verdictDeny, codeApprovalAlreadyUsed, gateway.PDPType)
	expectKinds(t, kindsOf(h.trailOf("req-3")), []controlv1.EventKind{kindProposed, kindDecided, kindBlocked})
	expectKinds(t, kindsOf(h.trailOf("req-1")), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalDecided, kindStarted})
	if s := h.p.Stats(); s.Held != 0 || s.Pending != 1 {
		t.Errorf("Held = %d, Pending = %d; want nothing held after the resume and only the first hold counted", s.Held, s.Pending)
	}
}

// TestASinkThatRefusesTheApprovalRecordBlocksWithTheApprovalSpent: the
// approval is consumed before its record is written, so a refused record
// blocks and the next retry is already used.
func TestASinkThatRefusesTheApprovalRecordBlocksWithTheApprovalSpent(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, approveRefunds))
	first := hold(t, h)
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	h.sink.refuse[kindApprovalDecided] = true
	expectBlock(t, h.admit(retry(t, "req-2"), refundArgs()), verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	h.sink.refuse[kindApprovalDecided] = false
	expectBlock(t, h.admit(retry(t, "req-3"), refundArgs()), verdictDeny, codeApprovalAlreadyUsed, gateway.PDPType)
	if got := len(h.trailOf("req-1")); got != 3 {
		t.Errorf("the held trail has %d event(s), want the 3 before the refused record", got)
	}
}

// TestARefusedHoldWhoseBlockTheSinkRefusesKeepsItsEntry: a hold the store
// would not keep is closed by ACTION_BLOCKED; when the sink refuses that
// record the trail stays open, so its entry stays listed for a reconciliation
// to report. A read under AllowReadsUnrecorded goes on without the record, and
// its trail is just as open.
func TestARefusedHoldWhoseBlockTheSinkRefusesKeepsItsEntry(t *testing.T) {
	for name, tc := range map[string]struct {
		rule string
		env  func(*testing.T) *controlv1.ActionEnvelope
		args []byte
		mut  func(*gateway.Config)
	}{
		"a refund": {
			rule: approveRefunds,
			env:  func(t *testing.T) *controlv1.ActionEnvelope { return refundEnvelope(t, refundArgs()) },
			args: refundArgs(),
			mut:  func(*gateway.Config) {},
		},
		"a read under the risk setting": {
			rule: approveReads,
			env:  func(*testing.T) *controlv1.ActionEnvelope { return readEnvelope() },
			args: []byte(`{}`),
			mut:  func(cfg *gateway.Config) { cfg.AllowReadsUnrecorded = true },
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := newFakeStore()
			store.holdErr = errStore
			j := &journalDouble{}
			h := build(t, modeEnforce, snapshot(t, tc.rule), overStore(store), tc.mut,
				func(cfg *gateway.Config) { cfg.Journal = j })
			h.sink.refuseKind(kindBlocked)

			d := h.admit(tc.env(t), tc.args)
			expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
			expectKinds(t, kindsOf(h.trailOf("req-1")),
				[]controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalExpired})
			if state, kept := j.state("req-1"); !kept || state != gateway.HoldClosing {
				t.Errorf("the entry of req-1 is %v, kept %v; want it kept in the closing state", state, kept)
			}
			listing, err := j.List(context.Background(), 16)
			if err != nil || listing.Interrupted != 1 || !listing.Complete {
				t.Errorf("List = %+v, %v; want the open trail's entry listed once", listing, err)
			}
			if s := h.p.Stats(); s.Blocks[codeEvidenceUnavailable] != 1 || len(s.Blocks) != 1 {
				t.Errorf("Stats().Blocks = %v; want EVIDENCE_UNAVAILABLE once", s.Blocks)
			}
		})
	}
}
