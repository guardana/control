package policywatch_test

import (
	"context"
	"maps"
	"os"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policywatch"
)

// expectStats fails unless the refresher counted exactly the refusals in
// refused, and the other counters as given.
func (r *rig) expectStats(refused map[policywatch.Cause]uint64, awaitingStatement, awaitingBundle, confirmations, installs uint64) {
	r.t.Helper()
	s := r.refresher.Stats()
	if !maps.Equal(s.Refused, refused) {
		r.t.Errorf("refused %v, want %v", s.Refused, refused)
	}
	if s.AwaitingStatement != awaitingStatement || s.AwaitingBundle != awaitingBundle || s.Confirmations != confirmations || s.Installs != installs {
		r.t.Errorf("awaiting a statement %d, a bundle %d, %d confirmation(s), %d install(s); want %d, %d, %d, %d",
			s.AwaitingStatement, s.AwaitingBundle, s.Confirmations, s.Installs, awaitingStatement, awaitingBundle, confirmations, installs)
	}
}

// Reading the same bundle and statement again moves nothing: the snapshot is
// the one installed, confirmed where it was, and the floor is not written.
func TestTheSameFilesAgainLeaveTheConfirmation(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	digest := r.startConfirmed()
	before := r.holder.Current()
	_, writes := r.store.stored()
	for range 3 {
		r.clock.advance(interval)
		r.poll()
	}
	if r.holder.Current() != before {
		t.Fatal("a poll of unchanged files replaced the snapshot")
	}
	r.expectSnapshot(2, digest, t0)
	if _, after := r.store.stored(); after != writes {
		t.Errorf("polls of unchanged files wrote the floor %d time(s)", after-writes)
	}
	r.expectStats(map[policywatch.Cause]uint64{}, 0, 0, 0, 0)
	if got := r.refresher.Stats().Polls; got != 3 {
		t.Errorf("%d polls counted, want 3", got)
	}
}

// A renewal of the current bundle moves its confirmation to the renewal's
// issuedAt and raises the floor.
func TestARenewalConfirmsAgain(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	digest := r.startConfirmed()
	renewed := t0.Add(20 * time.Second)
	r.writeStatement(2, digest, renewed)
	r.poll()
	r.expectSnapshot(2, digest, renewed)
	r.expectFloor(2, digest, renewed)
	r.expectStats(map[policywatch.Cause]uint64{}, 0, 0, 1, 0)
}

// replacement is one change to the files a plane must refuse, and the cause
// it is counted under.
type replacement struct {
	name  string
	cause policywatch.Cause
	// at is how long after t0 the poll runs; 0 is the rig's 30 s.
	at     time.Duration
	change func(r *rig, current string)
}

func refusedReplacements() []replacement {
	return []replacement{
		{name: "a torn bundle", cause: policywatch.CauseBundleInvalid, change: func(r *rig, _ string) {
			digest := r.writeBundle(3, "v3", 600)
			r.writeStatement(3, digest, t0.Add(time.Second))
			raw, err := os.ReadFile(r.opts.BundlePath)
			if err != nil {
				r.t.Fatal(err)
			}
			r.write(r.opts.BundlePath, raw[:len(raw)/2])
		}},
		{name: "a missing bundle", cause: policywatch.CauseBundleUnreadable, change: func(r *rig, _ string) {
			if err := os.Remove(r.opts.BundlePath); err != nil {
				r.t.Fatal(err)
			}
		}},
		{name: "an unsigned bundle", cause: policywatch.CauseBundleInvalid, change: func(r *rig, _ string) {
			b := signed(r.t, doc(planeID, 3, "v3", 600), bundleKey)
			b.Signature = nil
			r.write(r.opts.BundlePath, marshal(r.t, b))
			r.writeStatement(3, b.GetRef().GetDigest(), t0.Add(time.Second))
		}},
		{name: "a bundle signed under another key", cause: policywatch.CauseBundleInvalid, change: func(r *rig, _ string) {
			b := signed(r.t, doc(planeID, 3, "v3", 600), otherKey)
			r.write(r.opts.BundlePath, marshal(r.t, b))
			r.writeStatement(3, b.GetRef().GetDigest(), t0.Add(time.Second))
		}},
		{name: "a bundle of another id", cause: policywatch.CauseBundleID, change: func(r *rig, _ string) {
			b := signed(r.t, doc("other", 3, "v3", 600), bundleKey)
			r.write(r.opts.BundlePath, marshal(r.t, b))
			r.write(r.opts.StatementPath, statementBytes(r.t, "other", 3, b.GetRef().GetDigest(), t0.Add(time.Second), freshKey, freshKeyID))
		}},
		{name: "a lower serial", cause: policywatch.CauseRollback, change: func(r *rig, _ string) {
			r.writeStatement(1, r.writeBundle(1, "v1", 600), t0.Add(time.Second))
		}},
		{name: "the same serial with another digest", cause: policywatch.CauseSerialReused, change: func(r *rig, _ string) {
			r.writeStatement(2, r.writeBundle(2, "v2-other", 600), t0.Add(time.Second))
		}},
		{name: "a budget not longer than the interval", cause: policywatch.CauseBundleBudget, change: func(r *rig, _ string) {
			r.writeStatement(3, r.writeBundle(3, "v3", int64(interval/time.Second)), t0.Add(time.Second))
		}},
		{name: "a torn statement", cause: policywatch.CauseStatementInvalid, change: func(r *rig, _ string) {
			raw := statementBytes(r.t, planeID, 3, r.writeBundle(3, "v3", 600), t0.Add(time.Second), freshKey, freshKeyID)
			r.write(r.opts.StatementPath, raw[:len(raw)/2])
		}},
		{name: "a statement under another key", cause: policywatch.CauseStatementInvalid, change: func(r *rig, _ string) {
			r.write(r.opts.StatementPath, statementBytes(r.t, planeID, 3, r.writeBundle(3, "v3", 600), t0.Add(time.Second), otherKey, freshKeyID))
		}},
		{name: "a statement under the bundle key", cause: policywatch.CauseStatementInvalid, change: func(r *rig, _ string) {
			r.write(r.opts.StatementPath, statementBytes(r.t, planeID, 3, r.writeBundle(3, "v3", 600), t0.Add(time.Second), bundleKey, bundleKeyID))
		}},
		{name: "a missing statement", cause: policywatch.CauseStatementMissing, change: func(r *rig, _ string) {
			r.writeBundle(3, "v3", 600)
			if err := os.Remove(r.opts.StatementPath); err != nil {
				r.t.Fatal(err)
			}
		}},
		{name: "a statement of another id", cause: policywatch.CauseStatementUnbound, change: func(r *rig, current string) {
			r.write(r.opts.StatementPath, statementBytes(r.t, "other", 2, current, t0.Add(time.Second), freshKey, freshKeyID))
		}},
		{name: "a statement naming an older bundle", cause: policywatch.CauseStatementUnbound, change: func(r *rig, _ string) {
			r.writeStatement(1, r.digests["1v1"], t0.Add(time.Second))
		}},
		{name: "a future-dated statement", cause: policywatch.CauseStatementFuture, change: func(r *rig, _ string) {
			r.writeStatement(3, r.writeBundle(3, "v3", 600), t0.Add(31*time.Second))
		}},
		{name: "a future-dated renewal", cause: policywatch.CauseStatementFuture, change: func(r *rig, current string) {
			r.writeStatement(2, current, t0.Add(31*time.Second))
		}},
		{name: "an expired statement", cause: policywatch.CauseStatementExpired, at: 700 * time.Second, change: func(r *rig, _ string) {
			r.writeStatement(3, r.writeBundle(3, "v3", 600), t0.Add(time.Second))
		}},
		{name: "a renewal below the floor", cause: policywatch.CauseBelowFloor, change: func(r *rig, current string) {
			r.writeStatement(2, current, t0.Add(-time.Second))
		}},
		{name: "a statement of the current serial with another digest", cause: policywatch.CauseStatementUnbound, change: func(r *rig, current string) {
			r.writeStatement(2, current[:len(current)-1]+"0", t0.Add(time.Second))
		}},
		{name: "a statement path that is not a file", cause: policywatch.CauseStatementUnreadable, change: func(r *rig, _ string) {
			if err := os.Remove(r.opts.StatementPath); err != nil {
				r.t.Fatal(err)
			}
			if err := os.Mkdir(r.opts.StatementPath, 0o700); err != nil {
				r.t.Fatal(err)
			}
		}},
		{name: "a clock behind the floor's latest issuedAt", cause: policywatch.CauseClockBehindFloor, change: func(r *rig, current string) {
			r.store.mu.Lock()
			defer r.store.mu.Unlock()
			ahead, err := policy.NewFloor(planeID, 2, current, t0, t0.Add(time.Minute))
			if err != nil {
				r.t.Fatal(err)
			}
			r.store.floor = ahead
			r.writeStatement(2, current, t0.Add(time.Second))
		}},
	}
}

// TestEveryRefusedReplacementMovesNothing: each replacement leaves the last
// good snapshot served, its confirmation where it was and the floor
// unwritten, and is counted under its cause.
func TestEveryRefusedReplacementMovesNothing(t *testing.T) {
	for _, c := range refusedReplacements() {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, emptyFloor(t))
			r.digests["1v1"] = signed(t, doc(planeID, 1, "v1", 600), bundleKey).GetRef().GetDigest()
			digest := r.startConfirmed()
			before := r.holder.Current()
			_, writes := r.store.stored()
			c.change(r, digest)
			if c.at != 0 {
				r.clock.advance(c.at - 30*time.Second)
			}
			r.poll()
			if r.holder.Current() != before {
				t.Fatal("the refused replacement replaced the snapshot")
			}
			r.expectSnapshot(2, digest, t0)
			r.expectFloor(2, digest, t0)
			if _, after := r.store.stored(); after != writes {
				t.Errorf("the refused replacement wrote the floor")
			}
			r.expectStats(map[policywatch.Cause]uint64{c.cause: 1}, 0, 0, 0, 0)
		})
	}
}

// A new bundle whose statement still names the current one awaits its
// statement, and is installed, confirmed, once the statement arrives.
func TestAnUnpairedBundleAwaitsItsStatement(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	current := r.startConfirmed()
	next := r.writeBundle(3, "v3", 600)
	r.poll()
	r.poll()
	r.expectSnapshot(2, current, t0)
	r.expectStats(map[policywatch.Cause]uint64{}, 2, 0, 0, 0)
	issued := t0.Add(10 * time.Second)
	r.writeStatement(3, next, issued)
	r.poll()
	r.expectSnapshot(3, next, issued)
	r.expectFloor(3, next, issued)
	r.expectStats(map[policywatch.Cause]uint64{}, 2, 0, 1, 1)
}

// A statement deployed ahead of its bundle awaits the bundle; the current
// snapshot stays until the bundle arrives.
func TestAStatementAheadOfItsBundleAwaitsTheBundle(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	current := r.startConfirmed()
	nextBundle := signed(t, doc(planeID, 3, "v3", 600), bundleKey)
	issued := t0.Add(10 * time.Second)
	r.writeStatement(3, nextBundle.GetRef().GetDigest(), issued)
	r.poll()
	r.expectSnapshot(2, current, t0)
	r.expectStats(map[policywatch.Cause]uint64{}, 0, 1, 0, 0)
	r.write(r.opts.BundlePath, marshal(t, nextBundle))
	r.poll()
	r.expectSnapshot(3, nextBundle.GetRef().GetDigest(), issued)
	r.expectStats(map[policywatch.Cause]uint64{}, 0, 1, 1, 1)
}

// A statement first read dated ahead of the clock is taken, its bytes
// unchanged, once the clock reaches its time: the time is judged at every
// poll and never kept with the bytes.
func TestAStatementDatedAheadIsTakenOnceItsTimeComes(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	r.startConfirmed()
	ahead := r.clock.Wall().Add(10 * time.Second)
	next := r.writeBundle(3, "v3", 600)
	r.writeStatement(3, next, ahead)
	r.poll()
	r.expectStats(map[policywatch.Cause]uint64{policywatch.CauseStatementFuture: 1}, 0, 0, 0, 0)
	r.clock.advance(10 * time.Second)
	r.poll()
	r.expectSnapshot(3, next, ahead)
	r.expectStats(map[policywatch.Cause]uint64{policywatch.CauseStatementFuture: 1}, 0, 0, 1, 1)
}

// A floor that could not be raised refuses the install, and the next poll
// tries it again.
func TestAFailedRaiseIsTriedAgain(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	current := r.startConfirmed()
	next := r.writeBundle(3, "v3", 600)
	issued := t0.Add(10 * time.Second)
	r.writeStatement(3, next, issued)
	r.store.mu.Lock()
	r.store.failRaise = true
	r.store.mu.Unlock()
	r.poll()
	r.expectSnapshot(2, current, t0)
	r.expectStats(map[policywatch.Cause]uint64{policywatch.CauseFloor: 1}, 0, 0, 0, 0)
	r.store.mu.Lock()
	r.store.failRaise = false
	r.store.mu.Unlock()
	r.poll()
	r.expectSnapshot(3, next, issued)
	r.expectFloor(3, next, issued)
}

// A floor another plane raised past a renewal refuses the renewal.
func TestAFloorRaisedElsewhereRefusesAnOlderRenewal(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	digest := r.startConfirmed()
	r.store.mu.Lock()
	raised, err := r.store.floor.Raise(statementOf(t, 2, digest, t0.Add(20*time.Second)), t0.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	r.store.floor = raised
	r.store.mu.Unlock()
	r.writeStatement(2, digest, t0.Add(10*time.Second))
	r.poll()
	r.expectSnapshot(2, digest, t0)
	r.expectStats(map[policywatch.Cause]uint64{policywatch.CauseBelowFloor: 1}, 0, 0, 0, 0)
}

func TestNewRefusesIncompleteOptions(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	for name, edit := range map[string]func(*policywatch.Options){
		"no bundle id":       func(o *policywatch.Options) { o.BundleID = "" },
		"no holder":          func(o *policywatch.Options) { o.Holder = nil },
		"no floor":           func(o *policywatch.Options) { o.Floor = nil },
		"no wall clock":      func(o *policywatch.Options) { o.Wall = nil },
		"no monotonic clock": func(o *policywatch.Options) { o.Mono = nil },
		"no interval":        func(o *policywatch.Options) { o.Interval = 0 },
		"no freshness key":   func(o *policywatch.Options) { o.FreshnessKeys = nil },
	} {
		o := r.opts
		edit(&o)
		if _, err := policywatch.New(o); err == nil {
			t.Errorf("New took options with %s", name)
		}
	}
}

// Run polls every interval until its context ends, and then returns.
func TestRunPollsUntilItsContextEnds(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	r.opts.Interval = 10 * time.Millisecond
	r.startConfirmed()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.refresher.Run(ctx)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for r.refresher.Stats().Polls < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("Run polled %d time(s) in 10s at a 10ms interval", r.refresher.Stats().Polls)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return once its context ended")
	}
}

// A refresher over a holder Start never filled refuses every poll, counted.
func TestAPollOfAnEmptyHolderIsRefused(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	r.writeStatement(2, r.writeBundle(2, "v2", 600), t0)
	r.newRefresher()
	r.poll()
	if r.holder.Current() != nil {
		t.Fatal("a poll installed a bundle Start never installed")
	}
	r.expectStats(map[policywatch.Cause]uint64{policywatch.CauseFloor: 1}, 0, 0, 0, 0)
}
