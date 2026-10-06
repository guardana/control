package gateway_test

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/gateway"
)

// TestARejectionTheChecksRefuseIsNeverRecordedAsDecided: a store that answers
// a rejection beside a record that is not the held approval, rejected, of this
// contract's major and decided by the plane's clock is checked as an approval
// is. Its record is never written as APPROVAL_DECIDED: the held trail closes
// with the approval the plane minted, expired, and nothing runs. The genuine
// rejection is the record APPROVAL_DECIDED carries.
func TestARejectionTheChecksRefuseIsNeverRecordedAsDecided(t *testing.T) {
	cases := []struct {
		name    string
		lie     func(*controlv1.Approval) *controlv1.Approval
		verdict controlv1.Verdict
		code    string
		outcome controlv1.EventKind
	}{
		{"the genuine rejection", func(a *controlv1.Approval) *controlv1.Approval { return a }, verdictDeny, codeApprovalRejected, kindApprovalDecided},
		{"no record", func(*controlv1.Approval) *controlv1.Approval { return nil }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalExpired},
		{"another approval", func(a *controlv1.Approval) *controlv1.Approval { a.ApprovalId = "a-other"; return a }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalExpired},
		{"another request", func(a *controlv1.Approval) *controlv1.Approval { a.RequestId = "req-other"; return a }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalExpired},
		{"multi-use", func(a *controlv1.Approval) *controlv1.Approval { a.MultiUse = true; return a }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalExpired},
		{"another digest", func(a *controlv1.Approval) *controlv1.Approval { a.ActionDigest = "sha256:" + digits; return a }, verdictDeny, codeApprovalDigestMismatch, kindApprovalExpired},
		{"another bundle", func(a *controlv1.Approval) *controlv1.Approval { a.PolicyBundleDigest = "sha256:" + digits; return a }, verdictDeny, codeApprovalBundleMismatch, kindApprovalExpired},
		{"an expiry past the minted one", func(a *controlv1.Approval) *controlv1.Approval {
			a.ExpiresAt = timestamppb.New(base().Add(time.Hour))
			return a
		}, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalExpired},
		{"a grant", func(a *controlv1.Approval) *controlv1.Approval { a.State = approved; return a }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalExpired},
		{"another major", func(a *controlv1.Approval) *controlv1.Approval { a.SchemaVersion = "9.0"; return a }, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalExpired},
		{"decided after the reading", func(a *controlv1.Approval) *controlv1.Approval {
			a.DecidedAt = timestamppb.New(base().Add(time.Nanosecond))
			return a
		}, verdictIndeterminate, codeEvidenceUnavailable, kindApprovalExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeStore()
			h := withStore(t, f)
			first := hold(t, h)
			if err := f.Answer(first.Pending.ApprovalID, rejected, "bob", "not today", base()); err != nil {
				t.Fatalf("Answer: %v", err)
			}
			f.answer = func(a *controlv1.Approval, err error) (*controlv1.Approval, error) {
				if !errors.Is(err, gateway.ErrApprovalRejected) {
					t.Fatalf("the genuine Consume: %v, want a rejection", err)
				}
				return tc.lie(a), err
			}
			d := h.admit(retry(t, "req-2"), refundArgs())
			expectBlock(t, d, tc.verdict, tc.code, gateway.PDPType)
			trail := h.trailOf("req-1")
			expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, tc.outcome, kindBlocked})
			if err := evidence.ValidateChain(trail); err != nil {
				t.Errorf("ValidateChain: %v", err)
			}
			if tc.outcome == kindApprovalExpired && len(trail) > 3 {
				expectOwnApprovalExpired(t, trail, first)
			}
			if s := h.p.Stats(); s.Executed != 0 || s.Held != 0 || s.Blocks[tc.code] != 1 {
				t.Errorf("Stats: %d executed, %d held, blocks %v; want nothing run or held and one %s", s.Executed, s.Held, s.Blocks, tc.code)
			}
		})
	}
}

// TestAnApprovalOfAnotherMajorOrDecidedLaterNeverRuns: an approval of a major
// this build does not read, with none, with no decision time, or decided
// after the plane's clock reading or before its request, is refused and the
// call never runs; a later minor, and an answer decided at that very reading,
// run.
func TestAnApprovalOfAnotherMajorOrDecidedLaterNeverRuns(t *testing.T) {
	cases := []struct {
		name string
		edit func(*controlv1.Approval)
		runs bool
	}{
		{"major 1", func(*controlv1.Approval) {}, true},
		{"a later minor", func(a *controlv1.Approval) { a.SchemaVersion = "1.7" }, true},
		{"decided at the reading", func(a *controlv1.Approval) { a.DecidedAt = timestamppb.New(base()) }, true},
		{"another major", func(a *controlv1.Approval) { a.SchemaVersion = "9.0" }, false},
		{"major 10", func(a *controlv1.Approval) { a.SchemaVersion = "10.0" }, false},
		{"no version", func(a *controlv1.Approval) { a.SchemaVersion = "" }, false},
		{"a major alone", func(a *controlv1.Approval) { a.SchemaVersion = "1" }, false},
		{"a major and a dot", func(a *controlv1.Approval) { a.SchemaVersion = "1." }, false},
		{"a minor that is no number", func(a *controlv1.Approval) { a.SchemaVersion = "1.x" }, false},
		{"a third part", func(a *controlv1.Approval) { a.SchemaVersion = "1.0.0" }, false},
		{"a signed minor", func(a *controlv1.Approval) { a.SchemaVersion = "1.-3" }, false},
		{"a minor past 32 bits", func(a *controlv1.Approval) { a.SchemaVersion = "1.4294967296" }, false},
		{"a major with a leading zero", func(a *controlv1.Approval) { a.SchemaVersion = "01.0" }, false},
		{"decided after the reading", func(a *controlv1.Approval) { a.DecidedAt = timestamppb.New(base().Add(time.Nanosecond)) }, false},
		{"no decision time", func(a *controlv1.Approval) { a.DecidedAt = nil }, false},
		{"decided before the request", func(a *controlv1.Approval) {
			a.DecidedAt = timestamppb.New(a.GetRequestedAt().AsTime().Add(-time.Nanosecond))
		}, false},
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
				tc.edit(a)
				return a, nil
			}
			d := h.admit(retry(t, "req-2"), refundArgs())
			if tc.runs {
				if d.Action != core.Execute {
					t.Errorf("%s %v, want the approved call handed out", d.Decision.GetVerdict(), d.Decision.GetReasonCodes())
				}
				return
			}
			expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
			if s := h.p.Stats(); s.Executed != 0 || s.Open != 0 {
				t.Errorf("Stats: %d executed, %d open; want nothing run", s.Executed, s.Open)
			}
		})
	}
}

// TestAMultiUseApprovalIsLeftPending: a store that holds the approval as
// multi-use is refused without anything being spent or written: the retry is
// answered pending with the held approval, nothing is handed out, and the
// refusal is counted once.
func TestAMultiUseApprovalIsLeftPending(t *testing.T) {
	f := newFakeStore()
	h := withStore(t, f)
	first := hold(t, h)
	f.consumeErr = gateway.ErrMultiUse
	d := h.admit(retry(t, "req-2"), refundArgs())
	if d.Action != core.AwaitApproval || pendingID(d) != first.Pending.ApprovalID {
		t.Errorf("the retry: Action = %d, pending %q; want pending on %q", d.Action, pendingID(d), first.Pending.ApprovalID)
	}
	if d.AuthorizedArgs != nil || d.ExecutionID != "" {
		t.Errorf("the retry handed out %q under execution %q", d.AuthorizedArgs, d.ExecutionID)
	}
	expectKinds(t, h.kinds(), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested})
	if s := h.p.Stats(); s.MultiUseRefused != 1 || s.Pending != 2 || s.Executed != 0 || s.Held != 1 || len(s.Blocks) != 0 {
		t.Errorf("Stats: %d multi-use refused, %d pending, %d executed, %d held, blocks %v; want 1, 2, 0, 1 and none",
			s.MultiUseRefused, s.Pending, s.Executed, s.Held, s.Blocks)
	}
}
