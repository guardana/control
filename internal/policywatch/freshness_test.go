package policywatch_test

import (
	"context"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policywatch"
)

// TestFreshnessJudgesAsTheKernelDoes: no confirmation is unconfirmed; a
// confirmation within the smaller budget is confirmed, up to and at its
// end; past it, or later than the clock, it is expired.
func TestFreshnessJudgesAsTheKernelDoes(t *testing.T) {
	b := signed(t, doc(planeID, 2, "v2", 300), bundleKey)
	keys := bundle.Keyring{bundleKeyID: publicOf(bundleKey)}
	snapAt := func(at time.Time) *policy.Snapshot {
		s, err := policy.Load(b, keys, at)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	budget := 5 * time.Minute
	for _, c := range []struct {
		name    string
		snap    *policy.Snapshot
		now     time.Time
		state   policywatch.State
		expires time.Time
	}{
		{"no snapshot", nil, t0, policywatch.Unconfirmed, time.Time{}},
		{"no confirmation", snapAt(time.Time{}), t0, policywatch.Unconfirmed, time.Time{}},
		{"just confirmed", snapAt(t0), t0, policywatch.Confirmed, t0.Add(budget)},
		{"at the end of the budget", snapAt(t0), t0.Add(budget), policywatch.Confirmed, t0.Add(budget)},
		{"past the budget", snapAt(t0), t0.Add(budget + time.Nanosecond), policywatch.Expired, t0.Add(budget)},
		{"confirmed after the clock", snapAt(t0), t0.Add(-time.Nanosecond), policywatch.Expired, t0.Add(budget)},
	} {
		state, expires := policywatch.Freshness(c.snap, 10*time.Minute, c.now)
		if state != c.state || !expires.Equal(c.expires) {
			t.Errorf("%s: %s expiring %v, want %s expiring %v", c.name, state, expires, c.state, c.expires)
		}
	}
	if state, _ := policywatch.Freshness(snapAt(t0), time.Minute, t0.Add(2*time.Minute)); state != policywatch.Expired {
		t.Errorf("a confirmation past the operator's smaller budget is %s, want expired", state)
	}
	for state, name := range map[policywatch.State]string{
		policywatch.Unconfirmed: "unconfirmed", policywatch.Confirmed: "confirmed", policywatch.Expired: "expired",
	} {
		if state.String() != name {
			t.Errorf("%d spells %q, want %q", state, state.String(), name)
		}
	}
}

// TestFreshnessIsNotConfirmedAtAClockTheKernelRefuses: a confirmation within
// its budget is still expired at a reading after 9999, or at one earlier than
// the snapshot's NotBefore, since the kernel decides every call stale there.
// At NotBefore itself it is confirmed.
func TestFreshnessIsNotConfirmedAtAClockTheKernelRefuses(t *testing.T) {
	b := signed(t, doc(planeID, 2, "v2", 300), bundleKey)
	keys := bundle.Keyring{bundleKeyID: publicOf(bundleKey)}
	floor, err := policy.NewFloor(planeID, 2, b.GetRef().GetDigest(), t0.Add(-10*time.Minute), t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	h, err := policy.NewFloorHolder(planeID, &memFloor{floor: floor})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.InstallConfirmed(context.Background(), b, keys, statementOf(t, 2, b.GetRef().GetDigest(), t0), t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := h.Current().NotBefore(); !got.Equal(t0.Add(time.Minute)) {
		t.Fatalf("NotBefore %v, want %v", got, t0.Add(time.Minute))
	}
	last := time.Date(9999, time.December, 31, 23, 59, 0, 0, time.UTC)
	late, err := policy.Load(b, keys, last)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		snap  *policy.Snapshot
		now   time.Time
		state policywatch.State
	}{
		{"behind NotBefore", h.Current(), t0.Add(30 * time.Second), policywatch.Expired},
		{"at NotBefore", h.Current(), t0.Add(time.Minute), policywatch.Confirmed},
		{"after 9999", late, time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC), policywatch.Expired},
		{"the last instant of 9999", late, time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.UTC), policywatch.Confirmed},
	} {
		if state, _ := policywatch.Freshness(c.snap, 10*time.Minute, c.now); state != c.state {
			t.Errorf("%s: %s, want %s", c.name, state, c.state)
		}
	}
}
