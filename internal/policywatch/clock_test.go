package policywatch_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policywatch"
)

func (r *rig) withdrawals() uint64 { return r.refresher.Stats().Withdrawals }

// One step of the wall clock back, against a monotonic clock that went on,
// unconfirms the snapshot only when it is over a second: the bound is shown
// at the step it bites and the step beside it.
func TestOneStepBackUnconfirmsOnlyPastASecond(t *testing.T) {
	for _, c := range []struct {
		step        time.Duration
		unconfirmed bool
	}{
		{900 * time.Millisecond, false},
		{time.Second, false},
		{time.Second + 2*time.Millisecond, true},
	} {
		t.Run(c.step.String(), func(t *testing.T) {
			r := newRig(t, emptyFloor(t))
			digest := r.startConfirmed()
			r.clock.tick(time.Second, time.Second-c.step)
			r.poll()
			want := t0
			if c.unconfirmed {
				want = time.Time{}
			}
			r.expectSnapshot(2, digest, want)
			if got, w := r.withdrawals(), map[bool]uint64{true: 1, false: 0}[c.unconfirmed]; got != w {
				t.Errorf("%d withdrawal(s), want %d", got, w)
			}
		})
	}
}

// Steps of 0.9 s back, none over a second, unconfirm once they add up past
// one: the mark is the highest offset seen, not the last. While the clock
// stands below the mark no statement is taken, a newer one included; back
// within it, the statement that was withdrawn does not confirm again, and a
// newer one does.
func TestStepsBackPastASecondUnconfirmUntilTheClockIsBack(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	digest := r.startConfirmed()
	r.clock.tick(time.Second, 100*time.Millisecond)
	r.poll()
	r.expectSnapshot(2, digest, t0)
	r.clock.tick(time.Second, 100*time.Millisecond)
	r.poll()
	r.expectSnapshot(2, digest, time.Time{})
	if r.withdrawals() != 1 {
		t.Fatalf("%d withdrawal(s), want 1", r.withdrawals())
	}
	r.expectStats(map[policywatch.Cause]uint64{policywatch.CauseClockBack: 1}, 0, 0, 0, 0)

	r.clock.tick(time.Second, 2800*time.Millisecond)
	r.poll()
	r.expectSnapshot(2, digest, time.Time{})
	r.expectStats(map[policywatch.Cause]uint64{policywatch.CauseClockBack: 1, policywatch.CauseWithdrawn: 1}, 0, 0, 0, 0)

	renewed := t0.Add(10 * time.Second)
	r.writeStatement(2, digest, renewed)
	r.clock.tick(time.Second, -time.Second)
	r.poll()
	r.expectSnapshot(2, digest, time.Time{})
	r.expectStats(map[policywatch.Cause]uint64{policywatch.CauseClockBack: 2, policywatch.CauseWithdrawn: 1}, 0, 0, 0, 0)

	r.clock.tick(time.Second, 3*time.Second)
	r.poll()
	r.expectSnapshot(2, digest, renewed)
	r.expectStats(map[policywatch.Cause]uint64{policywatch.CauseClockBack: 2, policywatch.CauseWithdrawn: 1}, 0, 0, 1, 0)
}

// A wall clock that runs slower than the monotonic one by under 100 parts per
// million unconfirms nothing, however far it falls behind in all; one that
// runs slower by more is caught once the difference passes a second.
func TestDriftUnconfirmsOnlyPast100PartsPerMillion(t *testing.T) {
	const polls, every = 40, 1000 * time.Second
	for _, c := range []struct {
		ppm         int64
		unconfirmed bool
	}{
		{50, false},
		{99, false},
		{150, true},
	} {
		t.Run(fmt.Sprint(c.ppm, "ppm"), func(t *testing.T) {
			r := newRig(t, emptyFloor(t))
			r.startConfirmed()
			slow := every * time.Duration(c.ppm) / 1_000_000
			for range polls {
				r.clock.tick(every, every-slow)
				r.poll()
			}
			if got := r.withdrawals() > 0; got != c.unconfirmed {
				t.Errorf("after %d polls %v apart, slow by %d ppm: withdrawn %t, want %t", polls, every, c.ppm, got, c.unconfirmed)
			}
		})
	}
}

// A raise waiting on the floor directory's lock is given up within the poll
// interval and counted under the floor; and a poll made meanwhile, after the
// wall clock was set back, still withdraws the confirmation once the waiting
// raise is given up, so a held lock cannot hold the clock rule off.
func TestARaiseWaitingOnTheLockCannotHoldTheClockRuleOff(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	r.opts.Interval = 200 * time.Millisecond
	digest := r.startConfirmed()
	r.writeStatement(3, r.writeBundle(3, "v3", 600), t0.Add(10*time.Second))
	blocked := make(chan struct{}, 1)
	r.store.mu.Lock()
	r.store.blocked = blocked
	r.store.mu.Unlock()

	first := make(chan struct{})
	go func() {
		defer close(first)
		r.poll()
	}()
	select {
	case <-blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("the poll never reached the raise")
	}
	r.clock.tick(time.Second, -time.Second)
	second := make(chan struct{})
	go func() {
		defer close(second)
		r.poll()
	}()
	for name, done := range map[string]chan struct{}{"the waiting poll": first, "the poll after the clock step": second} {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not return within 10s at a 200ms interval", name)
		}
	}
	r.expectSnapshot(2, digest, time.Time{})
	s := r.refresher.Stats()
	if s.Refused[policywatch.CauseFloor] != 1 || s.Refused[policywatch.CauseClockBack] != 1 || s.Withdrawals != 1 {
		t.Errorf("refused %v, %d withdrawal(s); want one floor, one clock step and one withdrawal", s.Refused, s.Withdrawals)
	}
}

// The bound is a second exactly: with no monotonic time between the
// readings, so no drift allowance, a wall clock a second below the mark
// withdraws nothing and one a nanosecond further does.
func TestTheClockBoundIsOneSecondExactly(t *testing.T) {
	for _, c := range []struct {
		back      time.Duration
		withdrawn uint64
	}{{time.Second, 0}, {time.Second + time.Nanosecond, 1}} {
		r := newRig(t, emptyFloor(t))
		r.startConfirmed()
		r.clock.tick(0, -c.back)
		r.poll()
		if got := r.withdrawals(); got != c.withdrawn {
			t.Errorf("%v back: %d withdrawal(s), want %d", c.back, got, c.withdrawn)
		}
	}
}

// The mark holds for wall readings outside the range a count of nanoseconds
// since 1970 can carry: in 2200 a step forward is taken and a step back is
// caught, and a reading further from the first than a Duration holds, ahead
// or behind, is taken as a step back, since it cannot be judged.
func TestTheMarkHoldsFarFromNineteenSeventy(t *testing.T) {
	setWall := func(r *rig, wall time.Time) {
		r.clock.mu.Lock()
		r.clock.wall, r.clock.mono = wall, r.clock.mono+time.Second
		r.clock.mu.Unlock()
	}
	r := newRig(t, emptyFloor(t))
	r.startConfirmed()
	setWall(r, time.Date(2200, time.January, 1, 0, 0, 0, 0, time.UTC))
	r.poll()
	if r.withdrawals() != 0 {
		t.Fatalf("a clock set forward to 2200 withdrew the confirmation")
	}
	setWall(r, time.Date(2199, time.December, 31, 23, 0, 0, 0, time.UTC))
	r.poll()
	if r.withdrawals() != 1 {
		t.Errorf("a clock in 2200 set back an hour: %d withdrawal(s), want 1", r.withdrawals())
	}
	for _, far := range []time.Time{
		time.Date(2400, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(1600, time.January, 1, 0, 0, 0, 0, time.UTC),
	} {
		r := newRig(t, emptyFloor(t))
		r.startConfirmed()
		setWall(r, far)
		r.poll()
		if r.withdrawals() != 1 {
			t.Errorf("a clock set to %d, further than a Duration from the first reading: %d withdrawal(s), want 1", far.Year(), r.withdrawals())
		}
	}
}

// A poll held inside a file read cannot hold the clock rule off: Run judges
// the clock on a timer of its own, so a step back is withdrawn within the
// bound while the poll still waits.
func TestAPollStuckInAReadCannotHoldTheClockRuleOff(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	r.opts.Interval = 200 * time.Millisecond
	digest := r.startConfirmed()
	entered, release := make(chan struct{}, 1), make(chan struct{})
	policywatch.SetBeforeRead(r.refresher, func(string) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.refresher.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		close(release)
		<-done
	})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no poll reached a read")
	}
	r.clock.tick(time.Second, -time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for !r.holder.Current().ConfirmedAt().IsZero() {
		if time.Now().After(deadline) {
			t.Fatal("a step back was not withdrawn within 3s while a poll was held in a read")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.expectSnapshot(2, digest, time.Time{})
}

// A withdrawal that lands while a poll reads, after the poll judged the clock
// and before it confirms, refuses that poll's newer statement: the confirm
// judges the clock again under the clock's lock.
func TestAWithdrawalDuringAPollRefusesItsStatement(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	digest := r.startConfirmed()
	r.writeStatement(2, digest, t0.Add(10*time.Second))
	policywatch.SetBeforeRead(r.refresher, func(path string) {
		if path == r.opts.StatementPath {
			r.clock.tick(time.Second, -time.Second)
			if !r.refresher.JudgeClock() {
				t.Error("the clock set back was not judged back")
			}
		}
	})
	r.poll()
	r.expectSnapshot(2, digest, time.Time{})
	if got := r.refresher.Stats().Refused[policywatch.CauseClockBack]; got != 1 {
		t.Errorf("%d poll(s) refused for the clock, want 1", got)
	}
}

// A raise hung on a disk that stopped answering, whatever its context says,
// is given up at the poll's deadline through Bounded, so neither the poll nor
// the clock rule waits on it.
func TestAHungRaiseIsGivenUp(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	r.opts.Interval = 200 * time.Millisecond
	digest := r.startConfirmed()
	h, err := policy.NewFloorHolder(planeID, policywatch.Bounded(r.store))
	if err != nil {
		t.Fatal(err)
	}
	r.holder, r.opts.Holder = h, h
	r.writeStatement(2, digest, t0)
	if res, err := policywatch.Start(context.Background(), r.opts); err != nil || res.Action != policy.StartConfirmed {
		t.Fatalf("Start = %+v, %v", res, err)
	}
	r.newRefresher()
	r.writeStatement(3, r.writeBundle(3, "v3", 600), t0.Add(10*time.Second))
	hung := make(chan struct{})
	r.store.mu.Lock()
	r.store.hung = hung
	r.store.mu.Unlock()
	t.Cleanup(func() { close(hung) })
	polled := make(chan struct{})
	go func() {
		defer close(polled)
		r.poll()
	}()
	select {
	case <-hung:
	case <-time.After(10 * time.Second):
		t.Fatal("the poll never reached the raise")
	}
	select {
	case <-polled:
	case <-time.After(5 * time.Second):
		t.Fatal("a poll whose raise hung did not return within 5s at a 200ms interval")
	}
	r.clock.tick(time.Second, -time.Second)
	judged := make(chan bool, 1)
	go func() { judged <- r.refresher.JudgeClock() }()
	select {
	case back := <-judged:
		if !back {
			t.Error("the clock set back was not judged back")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("judging the clock waited on a hung raise")
	}
	r.expectSnapshot(2, digest, time.Time{})
	if got := r.refresher.Stats().Refused[policywatch.CauseFloor]; got != 1 {
		t.Errorf("%d poll(s) refused for the floor, want 1", got)
	}
}

// A statement read so slowly that its budget runs out before it would be
// published is refused as expired at that moment: expiry is judged on the
// wall clock read under the clock's lock, not on the poll's start.
func TestAStatementThatExpiresWhileItIsReadIsRefused(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	r.startConfirmed()
	next := r.writeBundle(3, "v3", 600)
	r.writeStatement(3, next, t0.Add(10*time.Second))
	policywatch.SetBeforeRead(r.refresher, func(path string) {
		if path == r.opts.StatementPath {
			r.clock.advance(11 * time.Minute)
		}
	})
	_, writes := r.store.stored()
	r.poll()
	if got := r.refresher.Stats(); got.Refused[policywatch.CauseStatementExpired] != 1 || got.Confirmations != 0 {
		t.Errorf("refused %v, %d confirmation(s); want one refused as expired and none taken", got.Refused, got.Confirmations)
	}
	if r.holder.Current().Serial() != 2 {
		t.Errorf("a statement expired before it was published installed serial %d", r.holder.Current().Serial())
	}
	if _, after := r.store.stored(); after != writes {
		t.Error("a statement expired before it was published raised the floor")
	}
}
