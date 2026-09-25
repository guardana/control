package gateway_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/core/approval"
	"github.com/guardana/control/internal/gateway"
)

// resolutionStore is a store that answers about its records exactly as a
// store far away might: it keeps them in memory, and when say is set it
// reports that resolution for every record it hands back, which is how a test
// drives an answer MemoryApprovals never gives on its own. It counts what the
// plane asked of it, so a test can assert the plane never spent a record.
type resolutionStore struct {
	inner gateway.MemoryApprovals

	mu       sync.Mutex
	say      *gateway.Resolution
	alter    func(*controlv1.Approval)
	consumes int
	resolves int
}

// Answer decides a held approval, as the store under it does.
func (s *resolutionStore) Answer(approvalID string, state controlv1.ApprovalState, approverID, reason string, decidedAt time.Time) error {
	return s.inner.Answer(approvalID, state, approverID, reason, decidedAt)
}

func (s *resolutionStore) Hold(ctx context.Context, h gateway.Held, now time.Time) error {
	return s.inner.Hold(ctx, h, now)
}

func (s *resolutionStore) Find(ctx context.Context, binding approval.Binding, now time.Time) ([]gateway.Held, error) {
	found, err := s.inner.Find(ctx, binding, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range found {
		if s.say != nil {
			found[i].Resolution = *s.say
		}
		if s.alter != nil {
			s.alter(found[i].Approval)
		}
	}
	return found, err
}

func (s *resolutionStore) Consume(ctx context.Context, binding approval.Binding, requestID, approvalID string, now time.Time) (*controlv1.Approval, error) {
	s.mu.Lock()
	s.consumes++
	s.mu.Unlock()
	return s.inner.Consume(ctx, binding, requestID, approvalID, now)
}

func (s *resolutionStore) Resolve(ctx context.Context, binding approval.Binding, requestID string, r gateway.Resolution, now time.Time) error {
	s.mu.Lock()
	s.resolves++
	s.mu.Unlock()
	return s.inner.Resolve(ctx, binding, requestID, r, now)
}

func (s *resolutionStore) reports(r gateway.Resolution) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.say = &r
}

// alters edits every record the store hands out, which is how a test asks
// what the plane does with an answer that is not the one it minted.
func (s *resolutionStore) alters(edit func(*controlv1.Approval)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alter = edit
}

func (s *resolutionStore) asked() (consumes, resolves int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consumes, s.resolves
}

// TestARecordThisPlaneDoesNotHoldIsReadAndNeverSpent: a store keeps a record
// for a request this plane never held, which is what a plane that restarted
// finds. What the plane does with it is read off the resolution the store
// already returned: only a record the store can prove was spent refuses the
// call, and every other answer holds it anew. Consume is never called, so no
// answer the plane cannot use is spent, and a later reconciliation can still
// resolve it.
func TestARecordThisPlaneDoesNotHoldIsReadAndNeverSpent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		resolution gateway.Resolution
		used       bool
		unheld     uint64
	}{
		{"a store that cannot say", gateway.ResolutionUnspecified, false, 1},
		{"a record nothing spent", gateway.ResolutionPending, false, 1},
		{"a record a plane gave up on", gateway.ResolutionNotResumed, false, 0},
		{"a record an execution spent", gateway.ResolutionConsumed, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := snapshot(t, approveRefunds)
			store := &resolutionStore{}
			first := build(t, modeEnforce, snap, overStore(store))
			hold(t, first)

			// The plane restarts: the store keeps the record, and the new
			// plane holds nothing.
			next := build(t, modeEnforce, snap, overStore(store))
			store.reports(tc.resolution)
			d := next.admit(retry(t, "req-2"), refundArgs())

			want := []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested}
			if tc.used {
				expectBlock(t, d, verdictDeny, codeApprovalAlreadyUsed, gateway.PDPType)
				want = []controlv1.EventKind{kindProposed, kindDecided, kindBlocked}
			} else if d.Action != core.AwaitApproval || d.Pending == nil {
				t.Fatalf("Action = %d, pending %+v; want a hold of its own", d.Action, d.Pending)
			}
			expectKinds(t, kindsOf(next.trailOf("req-2")), want)
			if s := next.p.Stats(); s.UnheldRecords != tc.unheld {
				t.Errorf("Stats().UnheldRecords = %d, want %d", s.UnheldRecords, tc.unheld)
			}
			if consumes, _ := store.asked(); consumes != 0 {
				t.Errorf("the plane consumed %d record(s) it does not hold; it may consume none", consumes)
			}
		})
	}
}

// overStore builds a pipeline over store.
func overStore(store gateway.ApprovalStore) func(*gateway.Config) {
	return func(cfg *gateway.Config) { cfg.Approvals = store }
}

// TestMemoryApprovalsSaysWhatBecameOfARecord: the store reports pending while
// nothing has touched a record, consumed once an execution spent it, and not
// resumed once a plane gave it up; and it refuses every write that would make
// one of those three a lie.
func TestMemoryApprovalsSaysWhatBecameOfARecord(t *testing.T) {
	var store gateway.MemoryApprovals
	binding := approval.Binding("sha256:1")
	if err := store.Hold(ctx(), held(binding, "a-1", "req-1"), base()); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	expectResolution(t, &store, binding, gateway.ResolutionPending)

	if err := store.Resolve(ctx(), binding, "req-1", gateway.ResolutionConsumed, base()); !errors.Is(err, gateway.ErrResolution) {
		t.Errorf("Resolve as consumed = %v, want ErrResolution: only Consume spends a record", err)
	}
	if err := store.Resolve(ctx(), binding, "req-1", gateway.ResolutionNotResumed, time.Time{}); !errors.Is(err, gateway.ErrZeroTime) {
		t.Errorf("Resolve at the zero time = %v, want ErrZeroTime", err)
	}
	if err := store.Resolve(ctx(), binding, "req-2", gateway.ResolutionNotResumed, base()); !errors.Is(err, gateway.ErrNoApproval) {
		t.Errorf("Resolve of a request it does not keep = %v, want ErrNoApproval", err)
	}
	expectResolution(t, &store, binding, gateway.ResolutionPending)

	if err := store.Resolve(ctx(), binding, "req-1", gateway.ResolutionNotResumed, base()); err != nil {
		t.Fatalf("Resolve as not resumed: %v", err)
	}
	expectResolution(t, &store, binding, gateway.ResolutionNotResumed)
	if a, err := store.Consume(ctx(), binding, "req-1", "a-1", base()); a != nil || !errors.Is(err, gateway.ErrNoApproval) {
		t.Errorf("Consume of a record given up = %v, %v; want nil, ErrNoApproval", a, err)
	}
}

// TestAConsumedRecordSaysSoAndNothingWritesOverIt: the proof that a record
// was spent is the one answer the store never loses.
func TestAConsumedRecordSaysSoAndNothingWritesOverIt(t *testing.T) {
	var store gateway.MemoryApprovals
	binding := approval.Binding("sha256:2")
	if err := store.Hold(ctx(), held(binding, "a-1", "req-1"), base()); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if err := store.Answer("a-1", approved, "alice", "", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if _, err := store.Consume(ctx(), binding, "req-1", "a-1", base()); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	expectResolution(t, &store, binding, gateway.ResolutionConsumed)
	if err := store.Resolve(ctx(), binding, "req-1", gateway.ResolutionNotResumed, base()); !errors.Is(err, gateway.ErrApprovalConsumed) {
		t.Errorf("Resolve of a spent record = %v, want ErrApprovalConsumed", err)
	}
	expectResolution(t, &store, binding, gateway.ResolutionConsumed)
}

// expectResolution asserts the one record under binding reports want.
func expectResolution(t *testing.T, store *gateway.MemoryApprovals, binding approval.Binding, want gateway.Resolution) {
	t.Helper()
	found, err := store.Find(ctx(), binding, base())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("Find returned %d record(s), want 1", len(found))
	}
	if found[0].Resolution != want {
		t.Errorf("Resolution = %d, want %d", found[0].Resolution, want)
	}
}
