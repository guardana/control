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
// ignoring the context when deaf, and counts how often it was asked. With
// cancel set it first cancels its caller's context.
type fakeVeto struct {
	answer  operators.Objection
	release chan struct{}
	deaf    bool
	cancel  context.CancelFunc
	asked   atomic.Int32
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
