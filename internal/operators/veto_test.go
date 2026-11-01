package operators_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guardana/control/internal/operators"
)

// fakeVeto answers answer, after release is closed when it is not nil,
// ignoring the context when deaf, and counts how often it was asked and
// whether its context was still live when asked. With cancel set it first
// cancels its caller's context.
type fakeVeto struct {
	answer  operators.Objection
	release chan struct{}
	deaf    bool
	cancel  context.CancelFunc
	asked   atomic.Int32
	live    atomic.Bool
	mu      sync.Mutex
	last    operators.Review
}

func (f *fakeVeto) lastReview() operators.Review {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func (f *fakeVeto) Review(ctx context.Context, r operators.Review) operators.Objection {
	f.mu.Lock()
	f.last = r
	f.mu.Unlock()
	f.asked.Add(1)
	f.live.Store(ctx.Err() == nil)
	if f.cancel != nil {
		f.cancel()
	}
	switch {
	case f.release != nil && f.deaf:
		<-f.release
	case f.release != nil:
		select {
		case <-f.release:
		case <-ctx.Done():
		}
	}
	return f.answer
}

func vetoed(t *testing.T, w *world, v operators.Veto, on operators.OnUnavailable) operators.Authorizer {
	t.Helper()
	z, err := operators.WithVeto(authorizer(t, w.roster, operators.Bounds{}), v, on, operators.Bounds{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return z
}

func TestAVetoCanOnlyTakeAPermissionAway(t *testing.T) {
	w := newWorld(t, standard())
	alice, bob := authenticated(t, w, "alice"), authenticated(t, w, "bob")
	cases := []struct {
		name  string
		veto  *fakeVeto
		on    operators.OnUnavailable
		state operators.PermState
		cause operators.Cause
	}{
		{"no objection", &fakeVeto{answer: operators.NoObjection()}, operators.VetoBlocks, operators.PermPermitted, operators.CauseNone},
		{"an objection", &fakeVeto{answer: operators.Objects()}, operators.VetoBlocks, operators.PermDenied, operators.CauseVeto},
		{"an objection under roster_only", &fakeVeto{answer: operators.Objects()}, operators.RosterOnly, operators.PermDenied, operators.CauseVeto},
		{"the zero objection", &fakeVeto{}, operators.VetoBlocks, operators.PermUnavailable, operators.CauseVetoUnavailable},
		{"a late answer", &fakeVeto{answer: operators.NoObjection(), release: make(chan struct{})}, operators.VetoBlocks, operators.PermUnavailable, operators.CauseLate},
		{"the zero objection under roster_only", &fakeVeto{}, operators.RosterOnly, operators.PermUnavailable, operators.CauseVetoUnavailable},
		{"a late answer under roster_only", &fakeVeto{answer: operators.Objects(), release: make(chan struct{})}, operators.RosterOnly, operators.PermPermitted, operators.CauseNone},
	}
	for _, c := range cases {
		got := vetoed(t, w, c.veto, c.on).Authorize(t.Context(), alice, approve("t1", "A"))
		wantPerm(t, c.name, got, c.state, c.cause)
		if r := c.veto.lastReview(); c.veto.asked.Load() != 1 || r.Subject != "alice" || r.Project != "A" || r.Issuer != "roster/roster-a" {
			t.Errorf("%s: the veto was asked %d times about %+v", c.name, c.veto.asked.Load(), r)
		}
		if c.veto.release != nil {
			close(c.veto.release)
		}
	}
	for _, on := range []operators.OnUnavailable{operators.VetoBlocks, operators.RosterOnly} {
		v := &fakeVeto{answer: operators.NoObjection()}
		wantPerm(t, "bob on A behind a veto with no objection", vetoed(t, w, v, on).Authorize(t.Context(), bob, approve("t1", "A")), operators.PermDenied, operators.CauseNoGrant)
		if v.asked.Load() != 0 {
			t.Errorf("the veto was asked about what the roster denied")
		}
	}
}

// TestRosterOnlyKeepsAPermissionOnlyWhenTheVetoGaveNoAnswerInTime: a veto
// with every slot taken keeps it, a caller that gave up does not.
func TestRosterOnlyKeepsAPermissionOnlyWhenTheVetoGaveNoAnswerInTime(t *testing.T) {
	w := newWorld(t, standard())
	alice := authenticated(t, w, "alice")
	for _, on := range []operators.OnUnavailable{operators.VetoBlocks, operators.RosterOnly} {
		stuck := &fakeVeto{answer: operators.Objects(), release: make(chan struct{}), deaf: true}
		z, err := operators.WithVeto(authorizer(t, w.roster, operators.Bounds{}), stuck, on, operators.Bounds{Timeout: 20 * time.Millisecond, InFlight: 1})
		if err != nil {
			t.Fatal(err)
		}
		_ = z.Authorize(t.Context(), alice, approve("t1", "A"))
		busy := z.Authorize(t.Context(), alice, approve("t1", "A"))
		close(stuck.release)
		if on == operators.RosterOnly {
			wantPerm(t, "a busy veto under roster_only", busy, operators.PermPermitted, operators.CauseNone)
		} else {
			wantPerm(t, "a busy veto under block", busy, operators.PermUnavailable, operators.CauseBusy)
		}
		ctx, cancel := context.WithCancel(t.Context())
		gives := &fakeVeto{answer: operators.Objects(), release: make(chan struct{}), cancel: cancel}
		defer close(gives.release)
		got := vetoed(t, w, gives, on).Authorize(ctx, alice, approve("t1", "A"))
		wantPerm(t, "a caller that gave up while the veto was asked", got, operators.PermUnavailable, operators.CauseCanceled)
	}
}

// TestACallerDeadlineDuringTheVetoIsCanceledNotLate: only the call's own
// timeout is late; a caller whose deadline passes first, though it is shorter
// than the call's timeout, gave up, and no setting keeps its permission.
func TestACallerDeadlineDuringTheVetoIsCanceledNotLate(t *testing.T) {
	w := newWorld(t, standard())
	alice := authenticated(t, w, "alice")
	for _, on := range []operators.OnUnavailable{operators.VetoBlocks, operators.RosterOnly} {
		waits := &fakeVeto{answer: operators.Objects(), release: make(chan struct{})}
		defer close(waits.release)
		z, err := operators.WithVeto(authorizer(t, w.roster, operators.Bounds{}), waits, on, operators.Bounds{Timeout: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		got := z.Authorize(ctx, alice, approve("t1", "A"))
		cancel()
		wantPerm(t, "a caller deadline that passed while the veto was asked", got, operators.PermUnavailable, operators.CauseCanceled)
		if waits.asked.Load() != 1 || !waits.live.Load() {
			t.Errorf("the veto was asked %d times, live %t: the deadline did not pass during the veto", waits.asked.Load(), waits.live.Load())
		}

		late := &fakeVeto{answer: operators.Objects(), release: make(chan struct{})}
		defer close(late.release)
		z, err = operators.WithVeto(authorizer(t, w.roster, operators.Bounds{}), late, on, operators.Bounds{Timeout: 30 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel = context.WithTimeout(t.Context(), 10*time.Second)
		got = z.Authorize(ctx, alice, approve("t1", "A"))
		cancel()
		if on == operators.RosterOnly {
			wantPerm(t, "the veto's own timeout with the caller alive, under roster_only", got, operators.PermPermitted, operators.CauseNone)
		} else {
			wantPerm(t, "the veto's own timeout with the caller alive, under block", got, operators.PermUnavailable, operators.CauseLate)
		}
	}
}

// nestedVeto asks inner from inside its own review, under the context its
// call gave it, and sends what inner answered.
type nestedVeto struct {
	inner operators.Authorizer
	who   operators.Authentication
	got   chan operators.Permission
}

func (n *nestedVeto) Review(ctx context.Context, r operators.Review) operators.Objection {
	n.got <- n.inner.Authorize(ctx, n.who, approve(r.Tenant, r.Project))
	return operators.NoObjection()
}

// TestAPortAskedInsideACallThatTimedOutIsCanceled: when the outer call's
// timeout ends a call made from inside it, that inner call's caller gave up,
// so its own roster_only does not keep the permission.
func TestAPortAskedInsideACallThatTimedOutIsCanceled(t *testing.T) {
	w := newWorld(t, standard())
	alice := authenticated(t, w, "alice")
	waits := &fakeVeto{answer: operators.Objects(), release: make(chan struct{})}
	defer close(waits.release)
	inner, err := operators.WithVeto(authorizer(t, w.roster, operators.Bounds{}), waits, operators.RosterOnly, operators.Bounds{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	outer := &nestedVeto{inner: inner, who: alice, got: make(chan operators.Permission, 1)}
	z, err := operators.WithVeto(authorizer(t, w.roster, operators.Bounds{}), outer, operators.VetoBlocks, operators.Bounds{Timeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	wantPerm(t, "the outer call", z.Authorize(t.Context(), alice, approve("t1", "A")), operators.PermUnavailable, operators.CauseLate)
	select {
	case got := <-outer.got:
		wantPerm(t, "the call inside it", got, operators.PermUnavailable, operators.CauseCanceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the call inside the outer one never returned")
	}
	if !waits.live.Load() {
		t.Error("the inner veto was asked after the outer call's timeout")
	}
}

func TestTheVetoSettingIsClosed(t *testing.T) {
	for s, want := range map[string]operators.OnUnavailable{"": operators.VetoBlocks, "block": operators.VetoBlocks, "roster_only": operators.RosterOnly} {
		if got, err := operators.ParseOnUnavailable(s); err != nil || got != want {
			t.Errorf("ParseOnUnavailable(%q) = %d, %v", s, got, err)
		}
	}
	for _, s := range []string{"allow", "Roster_only", "roster-only"} {
		if got, err := operators.ParseOnUnavailable(s); !errors.Is(err, operators.ErrConfig) || got != operators.VetoBlocks {
			t.Errorf("ParseOnUnavailable(%q) = %d, %v", s, got, err)
		}
	}
	w := newWorld(t, standard())
	z := authorizer(t, w.roster, operators.Bounds{})
	if _, err := operators.WithVeto(z, nil, operators.VetoBlocks, operators.Bounds{}); !errors.Is(err, operators.ErrConfig) {
		t.Errorf("a veto with no decision point = %v", err)
	}
	if _, err := operators.WithVeto(z, &fakeVeto{}, 7, operators.Bounds{}); !errors.Is(err, operators.ErrConfig) {
		t.Errorf("a setting outside the two = %v", err)
	}
}
