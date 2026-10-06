package policy_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/pkg/contract"
)

// Digests the floor tests name, typed: d7 is the example bundle's, the others
// stand for bundles no test loads.
const (
	d7      = exampleDigest
	d7other = "sha256:7777777777777777777777777777777777777777777777777777777777777777"
	d6      = "sha256:6666666666666666666666666666666666666666666666666666666666666666"
	d8      = "sha256:8888888888888888888888888888888888888888888888888888888888888888"
)

// utc is 2026-09-11 at the time of day given as "15:04:05".
func utc(clock string) time.Time {
	t, err := time.Parse("2006-01-02T15:04:05Z", "2026-09-11T"+clock+"Z")
	if err != nil {
		return time.Time{}
	}
	return t
}

// statementFor is a verified statement with these values, signed by the
// freshness key over a body written here.
func statementFor(t tb, id string, serial int64, digest, issuedAt string) policy.Statement {
	t.Helper()
	body := fmt.Sprintf(`{"kind":"agent-policy-freshness/v1alpha1","bundleId":%q,"serial":%d,"digest":%q,"issuedAt":"2026-09-11T%sZ"}`,
		id, serial, digest, issuedAt)
	st, err := policy.VerifyStatement(signed(body), freshKeys())
	if err != nil {
		t.Fatalf("a statement this test builds as valid: %v", err)
	}
	return st
}

// floorOf is a floor of "payments" holding these values.
func floorOf(t tb, serial int64, digest, issuedAt, latest string) policy.Floor {
	t.Helper()
	f, err := policy.NewFloor("payments", serial, digest, utc(issuedAt), utc(latest))
	if err != nil {
		t.Fatalf("NewFloor refused a floor this test builds as valid: %v", err)
	}
	return f
}

func emptyFloor(t tb, id string) policy.Floor {
	t.Helper()
	f, err := policy.EmptyFloor(id)
	if err != nil {
		t.Fatalf("EmptyFloor(%q): %v", id, err)
	}
	return f
}

// expectFloor fails unless f is a floor of "payments" holding exactly these
// values.
func expectFloor(t tb, what string, f policy.Floor, serial int64, digest, issuedAt, latest string) {
	t.Helper()
	const id = "payments"
	if !f.HasSerial() || f.BundleID() != id || f.Serial() != serial || f.Digest() != digest ||
		!f.IssuedAt().Equal(utc(issuedAt)) || !f.LatestIssuedAt().Equal(utc(latest)) {
		t.Errorf("%s: floor %q %v %d %s %v %v; want %q %d %s %s %s", what, f.BundleID(), f.HasSerial(), f.Serial(), f.Digest(),
			f.IssuedAt(), f.LatestIssuedAt(), id, serial, digest, issuedAt, latest)
	}
}

func expectFloorRefusal(t tb, what string, err, want error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: accepted, want %q", what, want)
		return
	}
	for _, s := range floorSentinels() {
		if got, wanted := errors.Is(err, s), errors.Is(s, want); got != wanted {
			t.Errorf("%s: errors.Is(%q, %q) = %v, want %v", what, err, s, got, wanted)
		}
	}
}

// TestFloorOrdering is ADR-0038's ordering, against a floor at serial 7
// issued at 12:00:00, judged at 13:00:00: by serial, then by issuedAt; the
// floor's own statement again; the latest issuedAt kept.
func TestFloorOrdering(t *testing.T) {
	t.Parallel()
	floor := floorOf(t, 7, d7, "12:00:00", "12:00:00")
	now := utc("13:00:00")
	type raised struct {
		serial           int64
		digest           string
		issuedAt, latest string
	}
	cases := []struct {
		name     string
		st       policy.Statement
		want     *raised
		refusals error
	}{
		{"the floor's own statement", statementFor(t, "payments", 7, d7, "12:00:00"), &raised{7, d7, "12:00:00", "12:00:00"}, nil},
		{"a renewal", statementFor(t, "payments", 7, d7, "12:05:00"), &raised{7, d7, "12:05:00", "12:05:00"}, nil},
		{"a renewal one second on", statementFor(t, "payments", 7, d7, "12:00:01"), &raised{7, d7, "12:00:01", "12:00:01"}, nil},
		{"an older statement replayed", statementFor(t, "payments", 7, d7, "11:55:00"), nil, policy.ErrBelowFloor},
		{"one second older", statementFor(t, "payments", 7, d7, "11:59:59"), nil, policy.ErrBelowFloor},
		{"the floor's serial, another digest, later", statementFor(t, "payments", 7, d7other, "12:05:00"), nil, policy.ErrFloorSerialReused},
		{"the floor's serial, another digest, same time", statementFor(t, "payments", 7, d7other, "12:00:00"), nil, policy.ErrFloorSerialReused},
		{"a lower serial issued later", statementFor(t, "payments", 6, d6, "12:30:00"), nil, policy.ErrBelowFloor},
		{"a higher serial issued earlier", statementFor(t, "payments", 8, d8, "11:00:00"), &raised{8, d8, "11:00:00", "12:00:00"}, nil},
		{"a higher serial issued later", statementFor(t, "payments", 8, d8, "12:30:00"), &raised{8, d8, "12:30:00", "12:30:00"}, nil},
		{"another bundle id", statementFor(t, "risk", 8, d8, "12:30:00"), nil, policy.ErrFloorBundle},
		{"dated one second after the clock", statementFor(t, "payments", 8, d8, "13:00:01"), nil, policy.ErrStatementFuture},
		{"dated at the clock", statementFor(t, "payments", 8, d8, "13:00:00"), &raised{8, d8, "13:00:00", "13:00:00"}, nil},
	}
	for _, c := range cases {
		got, err := floor.Raise(c.st, now)
		taken := floor.Takes(c.st, now)
		if c.want == nil {
			expectFloorRefusal(t, c.name, err, c.refusals)
			expectFloorRefusal(t, c.name+", asked whether it takes", taken, c.refusals)
			if !got.Equal(policy.Floor{}) {
				t.Errorf("%s: a floor beside the refusal", c.name)
			}
			continue
		}
		if err != nil || taken != nil {
			t.Errorf("%s: refused: %v, asked whether it takes: %v", c.name, err, taken)
			continue
		}
		expectFloor(t, c.name, got, c.want.serial, c.want.digest, c.want.issuedAt, c.want.latest)
	}
	expectFloor(t, "the floor raised from", floor, 7, d7, "12:00:00", "12:00:00")
}

// A floor whose newest statement is older than its latest issuedAt keeps the
// latest through a renewal that does not pass it, and moves it on one that
// does.
func TestFloorKeepsTheLatestIssuedAt(t *testing.T) {
	t.Parallel()
	floor := floorOf(t, 8, d8, "11:00:00", "12:00:00")
	now := utc("13:00:00")
	got, err := floor.Raise(statementFor(t, "payments", 8, d8, "11:30:00"), now)
	if err != nil {
		t.Fatal(err)
	}
	expectFloor(t, "a renewal before the latest", got, 8, d8, "11:30:00", "12:00:00")
	got, err = got.Raise(statementFor(t, "payments", 8, d8, "12:10:00"), now)
	if err != nil {
		t.Fatal(err)
	}
	expectFloor(t, "a renewal past the latest", got, 8, d8, "12:10:00", "12:10:00")
}

// A clock earlier than the floor's latest issuedAt takes no statement, even
// one above the floor; at the latest itself it does.
func TestFloorRefusesAClockBehindItsLatest(t *testing.T) {
	t.Parallel()
	floor := floorOf(t, 7, d7, "11:00:00", "12:00:00")
	st := statementFor(t, "payments", 8, d8, "11:30:00")
	_, err := floor.Raise(st, utc("11:59:59"))
	expectFloorRefusal(t, "a clock one second behind", err, policy.ErrClockBehindFloor)
	expectFloorRefusal(t, "a clock one second behind, asked whether it takes", floor.Takes(st, utc("11:59:59")), policy.ErrClockBehindFloor)
	got, err := floor.Raise(st, utc("12:00:00"))
	if err != nil {
		t.Fatalf("a clock at the latest: %v", err)
	}
	if err := floor.Takes(st, utc("12:00:00")); err != nil {
		t.Errorf("a clock at the latest, asked whether it takes: %v", err)
	}
	expectFloor(t, "a clock at the latest", got, 8, d8, "11:30:00", "12:00:00")
}

// A floor with no serial yet takes any statement of its bundle id dated no
// later than the clock, and that statement becomes the floor.
func TestAnEmptyFloorTakesItsFirstStatement(t *testing.T) {
	t.Parallel()
	floor := emptyFloor(t, "payments")
	if floor.HasSerial() || floor.BundleID() != "payments" || floor.Serial() != 0 {
		t.Fatalf("EmptyFloor: %v %q %d", floor.HasSerial(), floor.BundleID(), floor.Serial())
	}
	got, err := floor.Raise(statementFor(t, "payments", 3, d6, "12:00:00"), utc("12:00:00"))
	if err != nil {
		t.Fatal(err)
	}
	expectFloor(t, "the first statement", got, 3, d6, "12:00:00", "12:00:00")
	_, err = floor.Raise(statementFor(t, "risk", 3, d6, "12:00:00"), utc("12:00:00"))
	expectFloorRefusal(t, "another bundle id", err, policy.ErrFloorBundle)
	_, err = floor.Raise(statementFor(t, "payments", 3, d6, "12:00:01"), utc("12:00:00"))
	expectFloorRefusal(t, "dated after the clock", err, policy.ErrStatementFuture)
}

// The zero Floor is no floor: it takes nothing, and neither constructor makes
// a floor from values no accepted statement could leave.
func TestFloorConstructorsRefuseWhatNoStatementLeaves(t *testing.T) {
	t.Parallel()
	_, err := policy.Floor{}.Raise(statementFor(t, "payments", 7, d7, "12:00:00"), utc("13:00:00"))
	expectFloorRefusal(t, "the zero Floor", err, policy.ErrFloorInvalid)
	err = policy.Floor{}.Takes(statementFor(t, "payments", 7, d7, "12:00:00"), utc("13:00:00"))
	expectFloorRefusal(t, "the zero Floor, asked whether it takes", err, policy.ErrFloorInvalid)
	if _, err := policy.EmptyFloor(""); !errors.Is(err, policy.ErrFloorInvalid) {
		t.Errorf("EmptyFloor(\"\") = %v, want ErrFloorInvalid", err)
	}
	if _, err := policy.EmptyFloor("payments\n"); !errors.Is(err, policy.ErrFloorInvalid) {
		t.Errorf("EmptyFloor with a control character = %v, want ErrFloorInvalid", err)
	}
	cases := []struct {
		name             string
		id               string
		serial           int64
		digest           string
		issuedAt, latest time.Time
	}{
		{"an empty bundle id", "", 7, d7, utc("12:00:00"), utc("12:00:00")},
		{"serial 0", "payments", 0, d7, utc("12:00:00"), utc("12:00:00")},
		{"a malformed digest", "payments", 7, strings.ToUpper(d7), utc("12:00:00"), utc("12:00:00")},
		{"the zero issuedAt", "payments", 7, d7, time.Time{}, utc("12:00:00")},
		{"an issuedAt with a fraction", "payments", 7, d7, utc("12:00:00").Add(time.Millisecond), utc("12:00:01")},
		{"a latest before issuedAt", "payments", 7, d7, utc("12:00:00"), utc("11:59:59")},
		{"a latest with a fraction", "payments", 7, d7, utc("12:00:00"), utc("12:00:01").Add(time.Millisecond)},
	}
	for _, c := range cases {
		if _, err := policy.NewFloor(c.id, c.serial, c.digest, c.issuedAt, c.latest); !errors.Is(err, policy.ErrFloorInvalid) {
			t.Errorf("NewFloor with %s = %v, want ErrFloorInvalid", c.name, err)
		}
	}
	expectFloor(t, "NewFloor at the bounds", floorOf(t, 1, d7, "12:00:00", "12:00:00"), 1, d7, "12:00:00", "12:00:00")
}

// A floor takes the largest serial canonical JSON holds and a bundle id of
// exactly the string bound, and refuses one past either.
func TestFloorBoundsAreTheStatementsOwn(t *testing.T) {
	t.Parallel()
	at := utc("12:00:00")
	if _, err := policy.NewFloor("payments", 9007199254740991, d7, at, at); err != nil {
		t.Errorf("serial 2^53-1: %v", err)
	}
	if _, err := policy.NewFloor("payments", 9007199254740992, d7, at, at); !errors.Is(err, policy.ErrFloorInvalid) {
		t.Errorf("serial 2^53 = %v, want ErrFloorInvalid", err)
	}
	longest := strings.Repeat("b", contract.MaxStringBytes)
	if _, err := policy.NewFloor(longest, 7, d7, at, at); err != nil {
		t.Errorf("NewFloor with a bundle id at the bound: %v", err)
	}
	if _, err := policy.EmptyFloor(longest); err != nil {
		t.Errorf("EmptyFloor with a bundle id at the bound: %v", err)
	}
	over := longest + "b"
	if _, err := policy.NewFloor(over, 7, d7, at, at); !errors.Is(err, policy.ErrFloorInvalid) {
		t.Errorf("NewFloor with a bundle id one byte over = %v, want ErrFloorInvalid", err)
	}
	if _, err := policy.EmptyFloor(over); !errors.Is(err, policy.ErrFloorInvalid) {
		t.Errorf("EmptyFloor with a bundle id one byte over = %v, want ErrFloorInvalid", err)
	}
}

// Equal compares every part of two floors, the times as instants.
func TestFloorEqual(t *testing.T) {
	t.Parallel()
	base := floorOf(t, 7, d7, "12:00:00", "12:30:00")
	if !base.Equal(floorOf(t, 7, d7, "12:00:00", "12:30:00")) {
		t.Error("a floor is not equal to one of the same values")
	}
	plus2 := time.FixedZone("plus2", 2*60*60)
	elsewhere, err := policy.NewFloor("payments", 7, d7, utc("12:00:00").In(plus2), utc("12:30:00").In(plus2))
	if err != nil || !base.Equal(elsewhere) {
		t.Errorf("the same instants given in another zone: %v", err)
	}
	for name, other := range map[string]policy.Floor{
		"another serial":    floorOf(t, 8, d7, "12:00:00", "12:30:00"),
		"another digest":    floorOf(t, 7, d7other, "12:00:00", "12:30:00"),
		"another issuedAt":  floorOf(t, 7, d7, "12:00:01", "12:30:00"),
		"another latest":    floorOf(t, 7, d7, "12:00:00", "12:30:01"),
		"no serial yet":     emptyFloor(t, "payments"),
		"another bundle id": mustFloor(t)(policy.NewFloor("risk", 7, d7, utc("12:00:00"), utc("12:30:00"))),
		"the zero Floor":    {},
	} {
		if base.Equal(other) || other.Equal(base) {
			t.Errorf("%s: Equal", name)
		}
	}
	if !emptyFloor(t, "payments").Equal(emptyFloor(t, "payments")) || !(policy.Floor{}).Equal(policy.Floor{}) {
		t.Error("an empty floor, or the zero Floor, is not equal to itself")
	}
	if emptyFloor(t, "payments").Equal(policy.Floor{}) {
		t.Error("an empty floor equals the zero Floor")
	}
}

func mustFloor(t tb) func(policy.Floor, error) policy.Floor {
	return func(f policy.Floor, err error) policy.Floor {
		t.Helper()
		if err != nil {
			t.Fatalf("a floor this test builds as valid: %v", err)
		}
		return f
	}
}

// floorAccepts is ADR-0038's ordering written apart from Floor.Raise: the
// floor's bundle id, dated no later than the clock, and, once the floor holds
// a serial, a clock not behind its latest issuedAt and a statement above it
// by serial, or of its serial and digest and issued no earlier.
func floorAccepts(f policy.Floor, st policy.Statement, now time.Time) bool {
	if st.BundleID() != f.BundleID() || st.IssuedAt().After(now) {
		return false
	}
	if !f.HasSerial() {
		return true
	}
	if now.Before(f.LatestIssuedAt()) {
		return false
	}
	return st.Serial() > f.Serial() || (st.Serial() == f.Serial() && st.Digest() == f.Digest() && !st.IssuedAt().Before(f.IssuedAt()))
}

// TestFloorRaiseNeverMovesDown runs random statements and clocks through a
// store over random floors: an accepted statement becomes the floor, which
// never moves down by (serial, issuedAt) and whose latest issuedAt never
// decreases; a refusal returns the zero Floor and leaves the stored floor.
func TestFloorRaiseNeverMovesDown(t *testing.T) {
	t.Parallel()
	times := []string{"11:00:00", "11:30:00", "12:00:00", "12:30:00", "13:00:00"}
	clocks := append([]string{"10:00:00", "14:00:00"}, times...)
	var all []policy.Statement
	for serial := int64(1); serial <= 4; serial++ {
		for _, digest := range []string{d7, d7other} {
			for _, issued := range times {
				all = append(all, statementFor(t, "payments", serial, digest, issued))
			}
		}
	}
	all = append(all, statementFor(t, "risk", 2, d7, "11:00:00"))
	rapid.Check(t, func(rt *rapid.T) {
		start := emptyFloor(rt, "payments")
		if rapid.Bool().Draw(rt, "hasSerial") {
			i := rapid.IntRange(0, len(times)-1).Draw(rt, "issued")
			j := rapid.IntRange(i, len(times)-1).Draw(rt, "latest")
			start = floorOf(rt, rapid.Int64Range(1, 4).Draw(rt, "serial"), rapid.SampledFrom([]string{d7, d7other}).Draw(rt, "digest"), times[i], times[j])
		}
		store := newMemStore(start)
		for range rapid.IntRange(1, 20).Draw(rt, "steps") {
			st := rapid.SampledFrom(all).Draw(rt, "statement")
			now := utc(rapid.SampledFrom(clocks).Draw(rt, "clock"))
			before := store.stored("payments")
			next, err := store.Raise(context.Background(), st, now)
			checkRaise(rt, before, store.stored("payments"), st, now, next, err)
		}
	})
}

func checkRaise(t tb, before, after policy.Floor, st policy.Statement, now time.Time, next policy.Floor, err error) {
	t.Helper()
	want := floorAccepts(before, st, now)
	if want != (err == nil) {
		t.Fatalf("serial %d %s issued %v at %v over floor %d %v latest %v: refused %v, the ordering says accepted %v",
			st.Serial(), st.Digest(), st.IssuedAt(), now, before.Serial(), before.IssuedAt(), before.LatestIssuedAt(), err, want)
	}
	if taken := before.Takes(st, now); want != (taken == nil) {
		t.Fatalf("serial %d %s issued %v at %v over floor %d %v latest %v: Takes says %v, the ordering says accepted %v",
			st.Serial(), st.Digest(), st.IssuedAt(), now, before.Serial(), before.IssuedAt(), before.LatestIssuedAt(), taken, want)
	}
	if err != nil {
		if !next.Equal(policy.Floor{}) || !after.Equal(before) {
			t.Fatalf("a refusal returned a floor or moved the stored one")
		}
		return
	}
	if problem := raisedProblem(before, after, st, next); problem != "" {
		t.Fatalf("serial %d issued %v over floor %d issued %v: %s", st.Serial(), st.IssuedAt(), before.Serial(), before.IssuedAt(), problem)
	}
}

// raisedProblem is what is wrong with next, the floor raised from before by
// the accepted st and stored as after, or "".
func raisedProblem(before, after policy.Floor, st policy.Statement, next policy.Floor) string {
	switch {
	case !after.Equal(next) || next.Serial() != st.Serial() || next.Digest() != st.Digest() || !next.IssuedAt().Equal(st.IssuedAt()):
		return "the floor after an accepted statement is not that statement"
	case next.LatestIssuedAt().Before(next.IssuedAt()) || next.LatestIssuedAt().Before(before.LatestIssuedAt()):
		return "the latest issuedAt went down"
	case before.HasSerial() && (next.Serial() < before.Serial() || (next.Serial() == before.Serial() && next.IssuedAt().Before(before.IssuedAt()))):
		return "the floor moved down"
	}
	return ""
}
