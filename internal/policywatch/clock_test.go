package policywatch_test

import (
	"fmt"
	"testing"
	"time"

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
// since 1970 can carry: a clock set far ahead and then a little back is
// caught, and one set to a far-past year is caught too.
func TestTheMarkHoldsFarFromNineteenSeventy(t *testing.T) {
	r := newRig(t, emptyFloor(t))
	r.startConfirmed()
	far := time.Date(2400, time.January, 1, 0, 0, 0, 0, time.UTC)
	r.clock.tick(time.Second, far.Sub(r.clock.Wall()))
	r.poll()
	if r.withdrawals() != 0 {
		t.Fatalf("a clock set forward withdrew the confirmation")
	}
	r.clock.tick(time.Second, -2*time.Second)
	r.poll()
	if r.withdrawals() != 1 {
		t.Errorf("a clock in 2400 set back a second past the mark: %d withdrawal(s), want 1", r.withdrawals())
	}

	r = newRig(t, emptyFloor(t))
	r.startConfirmed()
	r.clock.mu.Lock()
	r.clock.wall, r.clock.mono = time.Date(1600, time.January, 1, 0, 0, 0, 0, time.UTC), r.clock.mono+time.Second
	r.clock.mu.Unlock()
	r.poll()
	if r.withdrawals() != 1 {
		t.Errorf("a clock set back to 1600: %d withdrawal(s), want 1", r.withdrawals())
	}
}
